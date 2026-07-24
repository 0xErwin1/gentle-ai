package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type cliRuntimeOpener struct {
	runtime *cliRuntime
	calls   int
	repo    string
}

func (opener *cliRuntimeOpener) OpenRuntimeOutcome(
	_ context.Context,
	repo string,
) (workprovider.RuntimeOutcome, error) {
	opener.calls++
	opener.repo = repo
	return opener.runtime, nil
}

type cliRuntime struct {
	capabilities    workprovider.RuntimeCapabilitiesV1
	capabilitiesV2  workprovider.RuntimeCapabilitiesV2
	status          workrun.WorkStatusV1
	advance         workrun.WorkAdvanceV1
	advanceV2       workrun.WorkAdvanceV2
	decision        workrun.WorkVerificationDecideV1
	requests        []workprovider.OutcomeStartRequest
	capabilityV1    int
	capabilityV2    int
	decisionRuns    []string
	decisionPrompts []string
	decisionChoices []workrun.VerificationDecisionChoice
}

func (runtime *cliRuntime) Capabilities(
	context.Context,
) (workprovider.RuntimeCapabilitiesV1, error) {
	runtime.capabilityV1++
	return runtime.capabilities, nil
}

func (runtime *cliRuntime) StartOutcome(
	_ context.Context,
	request workprovider.OutcomeStartRequest,
) (workrun.WorkStatusV1, error) {
	runtime.requests = append(runtime.requests, request)
	return runtime.status, nil
}

func (runtime *cliRuntime) DecideRoute(
	context.Context,
	string,
	string,
	workrun.WorkRouteChoice,
) (workrun.WorkRouteV1, error) {
	return workrun.WorkRouteV1{}, nil
}

func (runtime *cliRuntime) BindSDDRoute(
	context.Context,
	string,
	string,
	string,
) (workrun.WorkRouteV1, error) {
	return workrun.WorkRouteV1{}, nil
}

func (runtime *cliRuntime) AdvanceOutcome(
	context.Context,
	string,
	string,
) (workrun.WorkAdvanceV1, error) {
	return runtime.advance, nil
}

func (runtime *cliRuntime) CapabilitiesV2(
	context.Context,
) (workprovider.RuntimeCapabilitiesV2, error) {
	runtime.capabilityV2++
	return runtime.capabilitiesV2, nil
}

func (runtime *cliRuntime) AdvanceOutcomeV2(
	context.Context,
	string,
	string,
) (workrun.WorkAdvanceV2, error) {
	return runtime.advanceV2, nil
}

func (runtime *cliRuntime) DecideVerificationOutcome(
	_ context.Context,
	workRunID string,
	promptRef string,
	choice workrun.VerificationDecisionChoice,
) (workrun.WorkVerificationDecideV1, error) {
	runtime.decisionRuns = append(runtime.decisionRuns, workRunID)
	runtime.decisionPrompts = append(runtime.decisionPrompts, promptRef)
	runtime.decisionChoices = append(runtime.decisionChoices, choice)
	return runtime.decision, nil
}

func (runtime *cliRuntime) ReconcileOutcome(
	context.Context,
	string,
	string,
	string,
) (workrun.WorkReconcileV1, error) {
	return workrun.WorkReconcileV1{}, nil
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
				Exposure:              workprovider.WorkRoutingDormant,
				ImplementationRouting: canonical.ImplementationRouting,
			},
			Contracts: workprovider.RuntimeContractSetV1{
				Start:      workrun.WorkStartContractV1,
				Route:      workrun.WorkRouteContractV1,
				Advance:    workrun.WorkAdvanceContractV1,
				Reconcile:  workrun.WorkReconcileContractV1,
				Status:     workrun.WorkStatusContractV1,
				Transition: workrun.WorkTransitionContractV1,
			},
		},
		capabilitiesV2: workprovider.RuntimeCapabilitiesV2{
			Schema:        workprovider.RuntimeCapabilitiesContractV2,
			Contract:      workprovider.RuntimeCapabilitiesContractV2,
			RepositoryRef: cliRuntimeRef("repo"),
			AgentID:       model.AgentPi,
			WorkRouting: workprovider.RuntimeCapabilityClaimV1{
				ID:                    workrun.WorkRoutingCapabilityV1,
				Exposure:              workprovider.WorkRoutingAdvertised,
				ImplementationRouting: canonical.ImplementationRouting,
			},
			Contracts: workprovider.RuntimeContractSetV2{
				Start:              workrun.WorkStartContractV1,
				Route:              workrun.WorkRouteContractV1,
				Advance:            workrun.WorkAdvanceContractV2,
				VerificationDecide: workrun.WorkVerificationDecideContractV1,
				Reconcile:          workrun.WorkReconcileContractV1,
				Status:             workrun.WorkStatusContractV1,
				Transition:         workrun.WorkTransitionContractV1,
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
			RoutePhase:          workrun.RoutePhaseImplementationSelected,
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
	if capabilities.WorkRouting.Exposure != workprovider.WorkRoutingDormant ||
		capabilities.ConnectorSessionRef != "" {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	if runtime.capabilityV1 != 1 || runtime.capabilityV2 != 0 {
		t.Fatalf(
			"v1 capability dispatch = %d/%d",
			runtime.capabilityV1,
			runtime.capabilityV2,
		)
	}

	output.Reset()
	if err := runWorkCapabilities(
		context.Background(),
		[]string{
			"--cwd", "/repo",
			"--contract", workprovider.RuntimeCapabilitiesContractV2,
			"--json",
		},
		&output,
		controller,
	); err != nil {
		t.Fatalf("runWorkCapabilities(v2) error = %v", err)
	}
	var capabilitiesV2 workprovider.RuntimeCapabilitiesV2
	if err := json.Unmarshal(output.Bytes(), &capabilitiesV2); err != nil {
		t.Fatalf("decode capabilities v2: %v\n%s", err, output.String())
	}
	if err := capabilitiesV2.Validate(); err != nil {
		t.Fatal(err)
	}
	if capabilitiesV2.Contracts.VerificationDecide !=
		workrun.WorkVerificationDecideContractV1 {
		t.Fatalf("capabilities v2 = %#v", capabilitiesV2)
	}
	if runtime.capabilityV1 != 1 || runtime.capabilityV2 != 1 {
		t.Fatalf(
			"v2 capability dispatch = %d/%d",
			runtime.capabilityV1,
			runtime.capabilityV2,
		)
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

func TestWorkStartUnicodeOutcomeBoundariesAndTransportCap(t *testing.T) {
	const maximumOutcomeCodePoints = (maximumWorkStartInputBytes - 1024) / 12
	tests := []struct {
		name    string
		payload string
		valid   bool
	}{
		{
			name: "compact UTF-8 at boundary",
			payload: `{"outcome":"` +
				strings.Repeat("界", maximumOutcomeCodePoints) +
				`"}`,
			valid: true,
		},
		{
			name: "compact UTF-8 over boundary",
			payload: `{"outcome":"` +
				strings.Repeat("界", maximumOutcomeCodePoints+1) +
				`"}`,
		},
		{
			name: "escaped Unicode at boundary",
			payload: `{"outcome":"` +
				strings.Repeat(`\ud83d\ude00`, maximumOutcomeCodePoints) +
				`"}`,
			valid: true,
		},
		{
			name: "escaped Unicode over boundary",
			payload: `{"outcome":"` +
				strings.Repeat(
					`\ud83d\ude00`,
					maximumOutcomeCodePoints+1,
				) +
				`"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &cliRuntime{status: cliRuntimeStartStatus()}
			opener := &cliRuntimeOpener{runtime: runtime}
			err := runWorkStart(
				context.Background(),
				[]string{
					"--cwd", "/repo",
					"--contract", workrun.WorkStartContractV1,
					"--json",
				},
				strings.NewReader(test.payload),
				&bytes.Buffer{},
				workprovider.NewRuntimeController(opener),
			)
			if (err == nil) != test.valid {
				t.Fatalf(
					"runWorkStart() error = %v, want valid=%t",
					err,
					test.valid,
				)
			}
			if test.valid {
				if opener.calls != 1 || len(runtime.requests) != 1 ||
					utf8.RuneCountInString(runtime.requests[0].Outcome) !=
						maximumOutcomeCodePoints {
					t.Fatalf(
						"valid start calls/requests = %d/%#v",
						opener.calls,
						runtime.requests,
					)
				}
			} else if opener.calls != 0 || len(runtime.requests) != 0 {
				t.Fatalf(
					"invalid start calls/requests = %d/%#v",
					opener.calls,
					runtime.requests,
				)
			}
		})
	}

	opener := &cliRuntimeOpener{}
	err := runWorkStart(
		context.Background(),
		[]string{
			"--contract", workrun.WorkStartContractV1,
			"--json",
		},
		strings.NewReader(strings.Repeat(
			"x",
			maximumWorkStartInputBytes+1,
		)),
		&bytes.Buffer{},
		workprovider.NewRuntimeController(opener),
	)
	if err == nil || !strings.Contains(err.Error(), "input limit") {
		t.Fatalf("transport-cap error = %v", err)
	}
	if opener.calls != 0 {
		t.Fatalf("transport-cap request opened runtime %d times", opener.calls)
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

func TestWorkCapabilitiesRejectsUnknownContractBeforeOpen(t *testing.T) {
	t.Parallel()
	opener := &cliRuntimeOpener{}
	var output bytes.Buffer
	err := runWorkCapabilities(
		context.Background(),
		[]string{"--contract=unknown", "--json"},
		&output,
		workprovider.NewRuntimeController(opener),
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported capabilities error = %v", err)
	}
	if output.Len() != 0 || opener.calls != 0 {
		t.Fatalf(
			"unsupported capabilities output/opener = %q / %d",
			output.String(),
			opener.calls,
		)
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

func cliRuntimeStartStatus() workrun.WorkStatusV1 {
	return workrun.WorkStatusV1{
		Schema:              workrun.WorkStatusContractV1,
		Contract:            workrun.WorkStatusContractV1,
		WorkRunID:           "work-cli-unicode",
		Revision:            cliRuntimeRef("unicode-revision"),
		PublicState:         workrun.PublicStateWorking,
		RouteDecision:       workrun.RouteDecisionDirectInline,
		RoutePhase:          workrun.RoutePhaseImplementationSelected,
		ImplementationRoute: workrun.ImplementationRouteDirectInline,
		Verification: workrun.VerificationSummaryV1{
			Outcome:    workrun.VerificationPending,
			ResultRefs: []string{},
		},
	}
}

func cliRuntimeRef(seed string) string {
	return "sha256:" + strings.Repeat(
		string("0123456789abcdef"[len(seed)%16]),
		64,
	)
}
