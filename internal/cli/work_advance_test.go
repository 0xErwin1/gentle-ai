package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type cliAdvanceRuntimeCall struct {
	workRunID        string
	expectedRevision string
}

type cliAdvanceRuntime struct {
	advance workrun.WorkAdvanceV1
	err     error
	calls   []cliAdvanceRuntimeCall
}

func (runtime *cliAdvanceRuntime) Capabilities(
	context.Context,
) (workprovider.RuntimeCapabilitiesV1, error) {
	return workprovider.RuntimeCapabilitiesV1{}, errors.New(
		"work advance must not call capabilities",
	)
}

func (runtime *cliAdvanceRuntime) StartOutcome(
	context.Context,
	workprovider.OutcomeStartRequest,
) (workrun.WorkStatusV1, error) {
	return workrun.WorkStatusV1{}, errors.New(
		"work advance must not call outcome start",
	)
}

func (runtime *cliAdvanceRuntime) DecideRoute(
	context.Context,
	string,
	string,
	workrun.WorkRouteChoice,
) (workrun.WorkRouteV1, error) {
	return workrun.WorkRouteV1{}, errors.New(
		"work advance must not decide routes",
	)
}

func (runtime *cliAdvanceRuntime) BindSDDRoute(
	context.Context,
	string,
	string,
	string,
) (workrun.WorkRouteV1, error) {
	return workrun.WorkRouteV1{}, errors.New(
		"work advance must not bind SDD routes",
	)
}

func (runtime *cliAdvanceRuntime) AdvanceOutcome(
	_ context.Context,
	workRunID string,
	expectedRevision string,
) (workrun.WorkAdvanceV1, error) {
	runtime.calls = append(runtime.calls, cliAdvanceRuntimeCall{
		workRunID:        workRunID,
		expectedRevision: expectedRevision,
	})
	return runtime.advance, runtime.err
}

func (runtime *cliAdvanceRuntime) ReconcileOutcome(
	context.Context,
	string,
	string,
	string,
) (workrun.WorkReconcileV1, error) {
	return workrun.WorkReconcileV1{}, errors.New(
		"work advance must not reconcile outcomes",
	)
}

type cliAdvanceRuntimeOpener struct {
	runtime *cliAdvanceRuntime
	calls   int
	repos   []string
}

func (opener *cliAdvanceRuntimeOpener) OpenRuntimeOutcome(
	_ context.Context,
	repo string,
) (workprovider.RuntimeOutcome, error) {
	opener.calls++
	opener.repos = append(opener.repos, repo)
	if opener.runtime == nil {
		return nil, errors.New("runtime must not open")
	}
	return opener.runtime, nil
}

func TestRunWorkAdvanceKeepsMachineFlagsClosed(t *testing.T) {
	t.Parallel()
	base := []string{
		"--cwd", "/repo",
		"--work-run", "work-cli-advance",
		"--expected-revision", cliAdvanceRef("previous"),
		"--contract", workrun.WorkAdvanceContractV1,
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json is required",
			args: append([]string(nil), base...),
			want: "requires --json",
		},
		{
			name: "unknown flag",
			args: append(append([]string(nil), base...), "--json", "--route", "direct_main"),
			want: "not defined",
		},
		{
			name: "short flag",
			args: append(append([]string(nil), base...), "--json", "-force"),
			want: "not defined",
		},
		{
			name: "duplicate flag",
			args: append(append([]string(nil), base...), "--json", "--cwd", "/other"),
			want: "more than once",
		},
		{
			name: "positional argument",
			args: append(append([]string(nil), base...), "--json", "extra"),
			want: "no positional arguments",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opener := &cliAdvanceRuntimeOpener{}
			err := runWorkAdvance(
				context.Background(),
				test.args,
				&bytes.Buffer{},
				workprovider.NewRuntimeController(opener),
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runWorkAdvance(%q) error = %v", test.args, err)
			}
			if opener.calls != 0 {
				t.Fatalf("invalid CLI opened runtime %d times", opener.calls)
			}
		})
	}
}

func TestRunWorkAdvanceUnsupportedContractIsJSONAndOpensNothing(t *testing.T) {
	t.Parallel()
	opener := &cliAdvanceRuntimeOpener{}
	var output bytes.Buffer
	err := runWorkAdvance(
		context.Background(),
		[]string{"--contract=unknown", "--json"},
		&output,
		workprovider.NewRuntimeController(opener),
	)
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic workrun.WorkDiagnosticV1
	if err := json.Unmarshal(output.Bytes(), &diagnostic); err != nil {
		t.Fatalf("decode diagnostic: %v\n%s", err, output.String())
	}
	if err := diagnostic.Validate(); err != nil {
		t.Fatal(err)
	}
	if diagnostic.Operation != workrun.DiagnosticOperationWorkAdvance ||
		diagnostic.Code != "unsupported_contract" ||
		diagnostic.MutationOutcome != "not_started" ||
		opener.calls != 0 {
		t.Fatalf("diagnostic/opener = %#v / %d", diagnostic, opener.calls)
	}
}

func TestRunWorkAdvanceEmitsExactTerminalJSONAndAuthority(t *testing.T) {
	t.Parallel()
	const workRunID = "work-cli-advance"
	previousRevision := cliAdvanceRef("previous")
	tests := []struct {
		name    string
		advance workrun.WorkAdvanceV1
	}{
		{
			name: "ready",
			advance: cliReadyAdvance(
				workRunID,
				previousRevision,
			),
		},
		{
			name: "needs decision",
			advance: cliDecisionAdvance(
				workRunID,
				previousRevision,
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &cliAdvanceRuntime{advance: test.advance}
			opener := &cliAdvanceRuntimeOpener{runtime: runtime}
			var output bytes.Buffer
			err := runWorkAdvance(
				context.Background(),
				[]string{
					"--cwd=/repo",
					"--work-run=" + workRunID,
					"--expected-revision=" + previousRevision,
					"--contract=" + workrun.WorkAdvanceContractV1,
					"--json",
				},
				&output,
				workprovider.NewRuntimeController(opener),
			)
			if err != nil {
				t.Fatal(err)
			}
			expectedJSON, err := json.MarshalIndent(test.advance, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			expectedJSON = append(expectedJSON, '\n')
			if !bytes.Equal(output.Bytes(), expectedJSON) {
				t.Fatalf(
					"terminal JSON differs:\ngot:\n%s\nwant:\n%s",
					output.Bytes(),
					expectedJSON,
				)
			}
			if opener.calls != 1 ||
				!reflect.DeepEqual(opener.repos, []string{"/repo"}) ||
				!reflect.DeepEqual(runtime.calls, []cliAdvanceRuntimeCall{{
					workRunID:        workRunID,
					expectedRevision: previousRevision,
				}}) {
				t.Fatalf(
					"runtime authority = %d/%#v/%#v",
					opener.calls,
					opener.repos,
					runtime.calls,
				)
			}
		})
	}
}

func cliReadyAdvance(
	workRunID string,
	previousRevision string,
) workrun.WorkAdvanceV1 {
	return workrun.WorkAdvanceV1{
		Schema:           workrun.WorkAdvanceContractV1,
		Contract:         workrun.WorkAdvanceContractV1,
		PreviousRevision: previousRevision,
		Status: workrun.WorkStatusV1{
			Schema:              workrun.WorkStatusContractV1,
			Contract:            workrun.WorkStatusContractV1,
			WorkRunID:           workRunID,
			Revision:            cliAdvanceRef("ready"),
			PublicState:         workrun.PublicStateReady,
			RouteDecision:       workrun.RouteDecisionDirectInline,
			RoutePhase:          workrun.RoutePhaseImplementationSelected,
			ImplementationRoute: workrun.ImplementationRouteDirectInline,
			Verification: workrun.VerificationSummaryV1{
				Outcome: workrun.VerificationNotRequired,
				ResultRefs: []string{
					cliAdvanceRef("verification"),
				},
			},
			DeliveryIntentRef: cliAdvanceRef("intent"),
			ReviewReceiptRef:  cliAdvanceRef("receipt"),
		},
		DeliveryResultRef: cliAdvanceRef("delivery"),
	}
}

func cliDecisionAdvance(
	workRunID string,
	previousRevision string,
) workrun.WorkAdvanceV1 {
	code := workrun.WorkAdvanceDiagnosticScopeMismatch
	message, ok := workrun.WorkAdvanceDiagnosticMessage(code)
	if !ok {
		panic("closed work-advance diagnostic unavailable")
	}
	diagnostic := workrun.WorkAdvanceDiagnosticV1{
		Ref:        cliAdvanceRef("diagnostic"),
		Code:       code,
		Message:    message,
		NextAction: workrun.WorkAdvanceNextActionStartFresh,
	}
	return workrun.WorkAdvanceV1{
		Schema:           workrun.WorkAdvanceContractV1,
		Contract:         workrun.WorkAdvanceContractV1,
		PreviousRevision: previousRevision,
		Status: workrun.WorkStatusV1{
			Schema:              workrun.WorkStatusContractV1,
			Contract:            workrun.WorkStatusContractV1,
			WorkRunID:           workRunID,
			Revision:            cliAdvanceRef("decision"),
			PublicState:         workrun.PublicStateNeedsYourDecision,
			RouteDecision:       workrun.RouteDecisionDelegatedDirect,
			RoutePhase:          workrun.RoutePhaseImplementationSelected,
			ImplementationRoute: workrun.ImplementationRouteDelegatedDirect,
			Verification: workrun.VerificationSummaryV1{
				Outcome:    workrun.VerificationPending,
				ResultRefs: []string{},
			},
			DeliveryIntentRef: cliAdvanceRef("intent"),
			Diagnostic:        &diagnostic,
		},
		Diagnostic: &diagnostic,
	}
}

func cliAdvanceRef(seed string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(seed)))
}
