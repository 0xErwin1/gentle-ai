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

type cliReconcileCall struct {
	workRunID        string
	expectedRevision string
	diagnosticRef    string
}

type cliReconcileRuntime struct {
	result workrun.WorkReconcileV1
	calls  []cliReconcileCall
}

func (*cliReconcileRuntime) Capabilities(
	context.Context,
) (workprovider.RuntimeCapabilitiesV1, error) {
	return workprovider.RuntimeCapabilitiesV1{}, errors.New(
		"work-reconcile must not call capabilities",
	)
}

func (*cliReconcileRuntime) StartOutcome(
	context.Context,
	workprovider.OutcomeStartRequest,
) (workrun.WorkStatusV1, error) {
	return workrun.WorkStatusV1{}, errors.New(
		"work-reconcile must not start work",
	)
}

func (*cliReconcileRuntime) DecideRoute(
	context.Context,
	string,
	string,
	workrun.WorkRouteChoice,
) (workrun.WorkRouteV1, error) {
	return workrun.WorkRouteV1{}, errors.New(
		"work-reconcile must not decide routes",
	)
}

func (*cliReconcileRuntime) BindSDDRoute(
	context.Context,
	string,
	string,
	string,
) (workrun.WorkRouteV1, error) {
	return workrun.WorkRouteV1{}, errors.New(
		"work-reconcile must not bind routes",
	)
}

func (*cliReconcileRuntime) AdvanceOutcome(
	context.Context,
	string,
	string,
) (workrun.WorkAdvanceV1, error) {
	return workrun.WorkAdvanceV1{}, errors.New(
		"work-reconcile must not advance work",
	)
}

func (runtime *cliReconcileRuntime) ReconcileOutcome(
	_ context.Context,
	workRunID string,
	expectedRevision string,
	diagnosticRef string,
) (workrun.WorkReconcileV1, error) {
	runtime.calls = append(runtime.calls, cliReconcileCall{
		workRunID:        workRunID,
		expectedRevision: expectedRevision,
		diagnosticRef:    diagnosticRef,
	})
	return runtime.result, nil
}

type cliReconcileOpener struct {
	runtime *cliReconcileRuntime
	calls   int
}

func (opener *cliReconcileOpener) OpenRuntimeOutcome(
	context.Context,
	string,
) (workprovider.RuntimeOutcome, error) {
	opener.calls++
	if opener.runtime == nil {
		return nil, errors.New("runtime must not open")
	}
	return opener.runtime, nil
}

func TestRunWorkReconcileKeepsOwnerOutcomeFlagsClosed(t *testing.T) {
	t.Parallel()
	base := []string{
		"--cwd", "/repo",
		"--work-run", "reconcile-run",
		"--expected-revision", cliReconcileRef("previous"),
		"--diagnostic-ref", cliReconcileRef("diagnostic"),
		"--contract", workrun.WorkReconcileContractV1,
		"--json",
	}
	for _, forbidden := range []string{
		"--outcome", "--route", "--command", "--policy", "--delivery-result",
	} {
		args := append(append([]string(nil), base...), forbidden, "caller")
		opener := &cliReconcileOpener{}
		err := runWorkReconcile(
			context.Background(),
			args,
			&bytes.Buffer{},
			workprovider.NewRuntimeController(opener),
		)
		if err == nil || !strings.Contains(err.Error(), "not defined") {
			t.Fatalf("%s error = %v", forbidden, err)
		}
		if opener.calls != 0 {
			t.Fatalf("%s opened runtime %d times", forbidden, opener.calls)
		}
	}
}

func TestRunWorkReconcileUnsupportedContractOpensNothing(t *testing.T) {
	t.Parallel()
	opener := &cliReconcileOpener{}
	var output bytes.Buffer
	if err := runWorkReconcile(
		context.Background(),
		[]string{"--contract=unknown", "--json"},
		&output,
		workprovider.NewRuntimeController(opener),
	); err != nil {
		t.Fatal(err)
	}
	var diagnostic workrun.WorkDiagnosticV1
	if err := json.Unmarshal(output.Bytes(), &diagnostic); err != nil {
		t.Fatal(err)
	}
	if diagnostic.Operation != workrun.DiagnosticOperationWorkReconcile ||
		diagnostic.SupportedContracts[0] != workrun.WorkReconcileContractV1 ||
		opener.calls != 0 {
		t.Fatalf("diagnostic/opener = %#v / %d", diagnostic, opener.calls)
	}
}

func TestRunWorkReconcileRejectsOverlongUnsupportedContractBeforeOpen(
	t *testing.T,
) {
	t.Parallel()
	opener := &cliReconcileOpener{}
	var output bytes.Buffer
	boundary := strings.Repeat("界", 240)
	if err := runWorkReconcile(
		context.Background(),
		[]string{"--contract=" + boundary, "--json"},
		&output,
		workprovider.NewRuntimeController(opener),
	); err != nil {
		t.Fatalf("boundary runWorkReconcile() error = %v", err)
	}
	var diagnostic workrun.WorkDiagnosticV1
	if err := json.Unmarshal(output.Bytes(), &diagnostic); err != nil {
		t.Fatal(err)
	}
	if err := diagnostic.Validate(); err != nil {
		t.Fatal(err)
	}
	if diagnostic.RequestedContract != boundary || opener.calls != 0 {
		t.Fatalf("boundary diagnostic/opener = %#v/%d", diagnostic, opener.calls)
	}

	output.Reset()
	err := runWorkReconcile(
		context.Background(),
		[]string{
			"--contract=" + strings.Repeat("界", 241),
			"--json",
		},
		&output,
		workprovider.NewRuntimeController(opener),
	)
	if err == nil || !strings.Contains(err.Error(), "at most 240 characters") {
		t.Fatalf("runWorkReconcile() error = %v", err)
	}
	if opener.calls != 0 || output.Len() != 0 {
		t.Fatalf(
			"overlong unsupported contract opened/emitted = %d/%q",
			opener.calls,
			output.String(),
		)
	}
}

func TestRunWorkReconcileEmitsExactOwnerResult(t *testing.T) {
	t.Parallel()
	workRunID := "reconcile-run"
	previous := cliReconcileRef("previous")
	original := cliReconcileRef("diagnostic")
	nextDiagnostic := workrun.WorkAdvanceDiagnosticV1{
		Ref:        cliReconcileRef("next-diagnostic"),
		Code:       workrun.WorkAdvanceDiagnosticDeliveryNotCompleted,
		Message:    "Reconciliation proved that the candidate was not delivered.",
		NextAction: workrun.WorkAdvanceNextActionStartFresh,
	}
	result := workrun.WorkReconcileV1{
		Schema:            workrun.WorkReconcileContractV1,
		Contract:          workrun.WorkReconcileContractV1,
		PreviousRevision:  previous,
		DiagnosticRef:     original,
		ReconciliationRef: cliReconcileRef("reconciliation"),
		Outcome:           workrun.WorkReconcileNoDeliveryConfirmed,
		Status: workrun.WorkStatusV1{
			Schema:              workrun.WorkStatusContractV1,
			Contract:            workrun.WorkStatusContractV1,
			WorkRunID:           workRunID,
			Revision:            cliReconcileRef("next"),
			PublicState:         workrun.PublicStateNeedsYourDecision,
			RouteDecision:       workrun.RouteDecisionDirectInline,
			RoutePhase:          workrun.RoutePhaseImplementationSelected,
			ImplementationRoute: workrun.ImplementationRouteDirectInline,
			Verification: workrun.VerificationSummaryV1{
				Outcome:    workrun.VerificationNotRequired,
				ResultRefs: []string{},
			},
			DeliveryIntentRef: cliReconcileRef("intent"),
			Diagnostic:        &nextDiagnostic,
		},
	}
	runtime := &cliReconcileRuntime{result: result}
	opener := &cliReconcileOpener{runtime: runtime}
	var output bytes.Buffer
	if err := runWorkReconcile(
		context.Background(),
		[]string{
			"--cwd", "/repo",
			"--work-run", workRunID,
			"--expected-revision", previous,
			"--diagnostic-ref", original,
			"--contract", workrun.WorkReconcileContractV1,
			"--json",
		},
		&output,
		workprovider.NewRuntimeController(opener),
	); err != nil {
		t.Fatal(err)
	}
	var decoded workrun.WorkReconcileV1
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, result) ||
		len(runtime.calls) != 1 ||
		runtime.calls[0] != (cliReconcileCall{
			workRunID:        workRunID,
			expectedRevision: previous,
			diagnosticRef:    original,
		}) {
		t.Fatalf("decoded/calls = %#v / %#v", decoded, runtime.calls)
	}
}

func cliReconcileRef(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", sum)
}
