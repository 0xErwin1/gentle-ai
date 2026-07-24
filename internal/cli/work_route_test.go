package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type cliRouteRuntime struct {
	route       workrun.WorkRouteV1
	decisions   []workrun.WorkRouteChoice
	bindRunRefs []string
}

func (runtime *cliRouteRuntime) Capabilities(
	context.Context,
) (workprovider.RuntimeCapabilitiesV1, error) {
	return workprovider.RuntimeCapabilitiesV1{}, errors.New(
		"work-route must not call capabilities directly",
	)
}

func (runtime *cliRouteRuntime) StartOutcome(
	context.Context,
	workprovider.OutcomeStartRequest,
) (workrun.WorkStatusV1, error) {
	return workrun.WorkStatusV1{}, errors.New(
		"work-route must not start outcomes",
	)
}

func (runtime *cliRouteRuntime) DecideRoute(
	_ context.Context,
	_ string,
	_ string,
	choice workrun.WorkRouteChoice,
) (workrun.WorkRouteV1, error) {
	runtime.decisions = append(runtime.decisions, choice)
	return runtime.route, nil
}

func (runtime *cliRouteRuntime) BindSDDRoute(
	_ context.Context,
	_ string,
	_ string,
	runRef string,
) (workrun.WorkRouteV1, error) {
	runtime.bindRunRefs = append(runtime.bindRunRefs, runRef)
	return runtime.route, nil
}

func (runtime *cliRouteRuntime) AdvanceOutcome(
	context.Context,
	string,
	string,
) (workrun.WorkAdvanceV1, error) {
	return workrun.WorkAdvanceV1{}, errors.New(
		"work-route must not advance outcomes",
	)
}

func (runtime *cliRouteRuntime) ReconcileOutcome(
	context.Context,
	string,
	string,
	string,
) (workrun.WorkReconcileV1, error) {
	return workrun.WorkReconcileV1{}, errors.New(
		"work-route must not reconcile outcomes",
	)
}

type cliRouteRuntimeOpener struct {
	runtime *cliRouteRuntime
	calls   int
}

func (opener *cliRouteRuntimeOpener) OpenRuntimeOutcome(
	context.Context,
	string,
) (workprovider.RuntimeOutcome, error) {
	opener.calls++
	if opener.runtime == nil {
		return nil, errors.New("runtime must not open")
	}
	return opener.runtime, nil
}

func TestRunWorkRouteUnsupportedContractOpensNothing(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"decide", "--contract=", "--json"},
		{"bind-sdd", "--contract=", "--json"},
	} {
		opener := &cliRouteRuntimeOpener{}
		controller := workprovider.NewRuntimeController(opener)
		var output bytes.Buffer
		if err := runWorkRoute(
			context.Background(),
			args,
			&output,
			controller,
		); err != nil {
			t.Fatalf("runWorkRoute(%q) error = %v", args[0], err)
		}
		var diagnostic workrun.WorkDiagnosticV1
		if err := json.Unmarshal(output.Bytes(), &diagnostic); err != nil {
			t.Fatal(err)
		}
		if opener.calls != 0 ||
			diagnostic.Operation != workrun.DiagnosticOperationWorkRoute ||
			diagnostic.RequestedContract != "" ||
			diagnostic.SupportedContracts[0] !=
				workrun.WorkRouteContractV1 {
			t.Fatalf("unsupported result = %#v calls=%d", diagnostic, opener.calls)
		}
	}
}

func TestRunWorkRouteKeepsOwnerSelectionFlagsClosed(t *testing.T) {
	t.Parallel()
	base := []string{
		"--cwd", "/repo",
		"--work-run", "route-run",
		"--expected-revision", cliRouteRef("previous"),
		"--contract", workrun.WorkRouteContractV1,
		"--json",
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "decide cannot author route",
			args: append(
				append([]string{"decide"}, base...),
				"--choice", "decline_sdd", "--route", "direct_inline",
			),
			want: "not defined",
		},
		{
			name: "decide cannot author fallback facts",
			args: append(
				append([]string{"decide"}, base...),
				"--choice", "decline_sdd", "--routing-facts", "{}",
			),
			want: "not defined",
		},
		{
			name: "bind cannot author acceptance",
			args: append(
				append([]string{"bind-sdd"}, base...),
				"--run-ref", "organic-recovery",
				"--acceptance-ref", cliRouteRef("acceptance"),
			),
			want: "not defined",
		},
		{
			name: "json required",
			args: []string{
				"decide",
				"--cwd", "/repo",
				"--work-run", "route-run",
				"--expected-revision", cliRouteRef("previous"),
				"--contract", workrun.WorkRouteContractV1,
				"--choice", "accept_sdd",
			},
			want: "requires --json",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := runWorkRoute(
				context.Background(),
				test.args,
				&bytes.Buffer{},
				workprovider.NewRuntimeController(
					&cliRouteRuntimeOpener{},
				),
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runWorkRoute() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRunWorkRouteForwardsOnlyChoiceOrExistingRun(t *testing.T) {
	t.Parallel()
	previous := cliRouteRef("previous")
	accepted := workrun.WorkRouteV1{
		Schema:           workrun.WorkRouteContractV1,
		Contract:         workrun.WorkRouteContractV1,
		Operation:        workrun.WorkRouteOperationDecideSDD,
		Choice:           workrun.WorkRouteChoiceAcceptSDD,
		PreviousRevision: previous,
		SelectedRoute:    workrun.ImplementationRouteSDD,
		Status: workrun.WorkStatusV1{
			Schema:        workrun.WorkStatusContractV1,
			Contract:      workrun.WorkStatusContractV1,
			WorkRunID:     "route-run",
			Revision:      cliRouteRef("accepted"),
			PublicState:   workrun.PublicStateWorking,
			RouteDecision: workrun.RouteDecisionProposeSDD,
			RoutePhase:    workrun.RoutePhaseSDDRuntimePending,
			Verification: workrun.VerificationSummaryV1{
				Outcome:    workrun.VerificationPending,
				ResultRefs: []string{},
			},
			DeliveryIntentRef: cliRouteRef("intent"),
		},
	}
	runtime := &cliRouteRuntime{route: accepted}
	controller := workprovider.NewRuntimeController(
		&cliRouteRuntimeOpener{runtime: runtime},
	)
	var output bytes.Buffer
	if err := runWorkRoute(
		context.Background(),
		[]string{
			"decide",
			"--cwd", "/repo",
			"--work-run", "route-run",
			"--expected-revision", previous,
			"--contract", workrun.WorkRouteContractV1,
			"--choice", "accept_sdd",
			"--json",
		},
		&output,
		controller,
	); err != nil {
		t.Fatal(err)
	}
	var decoded workrun.WorkRouteV1
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.decisions) != 1 ||
		runtime.decisions[0] != workrun.WorkRouteChoiceAcceptSDD {
		t.Fatalf("route decisions = %#v", runtime.decisions)
	}

	bound := accepted
	bound.Operation = workrun.WorkRouteOperationBindSDD
	bound.Choice = ""
	bound.Status.Revision = cliRouteRef("bound")
	bound.Status.PublicState = workrun.PublicStateWorking
	bound.Status.RoutePhase = workrun.RoutePhaseImplementationSelected
	bound.Status.ImplementationRoute = workrun.ImplementationRouteSDD
	bound.Status.SDDRunRef = "organic-recovery"
	runtime.route = bound
	output.Reset()
	if err := runWorkRoute(
		context.Background(),
		[]string{
			"bind-sdd",
			"--cwd", "/repo",
			"--work-run", "route-run",
			"--expected-revision", previous,
			"--contract", workrun.WorkRouteContractV1,
			"--run-ref", "organic-recovery",
			"--json",
		},
		&output,
		controller,
	); err != nil {
		t.Fatal(err)
	}
	if len(runtime.bindRunRefs) != 1 ||
		runtime.bindRunRefs[0] != "organic-recovery" {
		t.Fatalf("route bind refs = %#v", runtime.bindRunRefs)
	}
}

func cliRouteRef(label string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(label)))
}
