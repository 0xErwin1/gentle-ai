package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type cliRuntimeOpener struct {
	runtime *cliRuntime
	calls   int
}

func (opener *cliRuntimeOpener) OpenRuntimeOutcome(
	context.Context,
	string,
) (workprovider.RuntimeOutcome, error) {
	opener.calls++
	return opener.runtime, nil
}

type cliRuntime struct {
	capabilities workprovider.RuntimeCapabilitiesV1
	status       workrun.WorkStatusV1
	requests     []workprovider.OutcomeStartRequest
}

func (runtime *cliRuntime) Capabilities(
	context.Context,
) (workprovider.RuntimeCapabilitiesV1, error) {
	return runtime.capabilities, nil
}

func (runtime *cliRuntime) StartOutcome(
	_ context.Context,
	request workprovider.OutcomeStartRequest,
) (workrun.WorkStatusV1, error) {
	runtime.requests = append(runtime.requests, request)
	return runtime.status, nil
}

func TestWorkCapabilitiesAndStartUseMachineContracts(t *testing.T) {
	t.Parallel()

	canonical, err := capabilitymanifest.ForAgent(model.AgentPi)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &cliRuntime{
		capabilities: workprovider.RuntimeCapabilitiesV1{
			Schema:        workrun.WorkCapabilitiesContractV1,
			Contract:      workrun.WorkCapabilitiesContractV1,
			RepositoryRef: cliRuntimeRef("repo"),
			AgentID:       model.AgentPi,
			WorkRouting: workprovider.RuntimeCapabilityClaimV1{
				ID:                    workrun.WorkRoutingCapabilityV1,
				Exposure:              workprovider.WorkRoutingAdvertised,
				ImplementationRouting: canonical.ImplementationRouting,
			},
			Contracts: workprovider.RuntimeContractSetV1{
				Start:      workrun.WorkStartContractV1,
				Status:     workrun.WorkStatusContractV1,
				Transition: workrun.WorkTransitionContractV1,
			},
			ConnectorSessionRef: cliRuntimeRef("session"),
		},
		status: workrun.WorkStatusV1{
			Schema:              workrun.WorkStatusContractV1,
			Contract:            workrun.WorkStatusContractV1,
			WorkRunID:           "work-cli-runtime",
			Revision:            cliRuntimeRef("revision"),
			PublicState:         workrun.PublicStateWorking,
			RouteDecision:       workrun.RouteDecisionDelegatedDirect,
			ImplementationRoute: workrun.ImplementationRouteDelegatedDirect,
			Verification: workrun.VerificationSummaryV1{
				Outcome:    workrun.VerificationPending,
				ResultRefs: []string{},
			},
		},
	}
	opener := &cliRuntimeOpener{runtime: runtime}
	controller := workprovider.NewRuntimeController(opener)

	var output bytes.Buffer
	if err := runWorkCapabilities(
		context.Background(),
		[]string{
			"--cwd", "/repo",
			"--contract", workrun.WorkCapabilitiesContractV1,
			"--json",
		},
		&output,
		controller,
	); err != nil {
		t.Fatalf("runWorkCapabilities() error = %v", err)
	}
	var capabilities workprovider.RuntimeCapabilitiesV1
	if err := json.Unmarshal(output.Bytes(), &capabilities); err != nil {
		t.Fatalf("decode capabilities: %v\n%s", err, output.String())
	}
	if capabilities.ConnectorSessionRef !=
		runtime.capabilities.ConnectorSessionRef {
		t.Fatalf("capabilities = %#v", capabilities)
	}

	output.Reset()
	if err := runWorkStart(
		context.Background(),
		[]string{
			"--cwd", "/repo",
			"--contract", workrun.WorkStartContractV1,
			"--json",
		},
		strings.NewReader(
			`{"outcome":"Implement the requested change.","explicitSddRequested":false}`,
		),
		&output,
		controller,
	); err != nil {
		t.Fatalf("runWorkStart() error = %v", err)
	}
	var status workrun.WorkStatusV1
	if err := json.Unmarshal(output.Bytes(), &status); err != nil {
		t.Fatalf("decode work status: %v\n%s", err, output.String())
	}
	if status.WorkRunID != runtime.status.WorkRunID ||
		len(runtime.requests) != 1 ||
		runtime.requests[0].Outcome != "Implement the requested change." {
		t.Fatalf("status/requests = %#v / %#v", status, runtime.requests)
	}
}

func TestWorkRuntimeCommandsRejectAuthorityFlags(t *testing.T) {
	t.Parallel()

	controller := workprovider.NewRuntimeController(&cliRuntimeOpener{})
	for _, test := range []struct {
		name string
		run  func() error
	}{
		{
			name: "capabilities agent",
			run: func() error {
				return runWorkCapabilities(
					context.Background(),
					[]string{"--agent", "pi", "--json"},
					&bytes.Buffer{},
					controller,
				)
			},
		},
		{
			name: "start route",
			run: func() error {
				return runWorkStart(
					context.Background(),
					[]string{"--route", "direct_main", "--json"},
					strings.NewReader(`{"outcome":"Do it."}`),
					&bytes.Buffer{},
					controller,
				)
			},
		},
		{
			name: "start work run",
			run: func() error {
				return runWorkStart(
					context.Background(),
					[]string{"--work-run", "chosen", "--json"},
					strings.NewReader(`{"outcome":"Do it."}`),
					&bytes.Buffer{},
					controller,
				)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil || !strings.Contains(err.Error(), "not defined") {
				t.Fatalf("command error = %v", err)
			}
		})
	}
}

func TestWorkStartUnsupportedContractDoesNotValidateBodyOrOpen(t *testing.T) {
	t.Parallel()

	opener := &cliRuntimeOpener{}
	controller := workprovider.NewRuntimeController(opener)
	var output bytes.Buffer
	if err := runWorkStart(
		context.Background(),
		[]string{"--contract=", "--json"},
		strings.NewReader(`{"route":"direct_main"}`),
		&output,
		controller,
	); err != nil {
		t.Fatalf("runWorkStart() error = %v", err)
	}
	var diagnostic workrun.WorkDiagnosticV1
	if err := json.Unmarshal(output.Bytes(), &diagnostic); err != nil {
		t.Fatalf("decode diagnostic: %v\n%s", err, output.String())
	}
	if diagnostic.Operation != workrun.DiagnosticOperationWorkStart ||
		diagnostic.MutationOutcome != "not_started" ||
		opener.calls != 0 {
		t.Fatalf("diagnostic/opener = %#v/%d", diagnostic, opener.calls)
	}
}

func cliRuntimeRef(seed string) string {
	return "sha256:" + strings.Repeat(
		string("0123456789abcdef"[len(seed)%16]),
		64,
	)
}
