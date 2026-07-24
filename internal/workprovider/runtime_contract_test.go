package workprovider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type stubRuntimeOutcomeOpener struct {
	runtime RuntimeOutcome
	err     error
	calls   int
	repo    string
}

func (opener *stubRuntimeOutcomeOpener) OpenRuntimeOutcome(
	_ context.Context,
	repo string,
) (RuntimeOutcome, error) {
	opener.calls++
	opener.repo = repo
	return opener.runtime, opener.err
}

type stubRuntimeOutcome struct {
	capabilities RuntimeCapabilitiesV1
	status       workrun.WorkStatusV1
	route        workrun.WorkRouteV1
	advance      workrun.WorkAdvanceV1
	reconcile    *workrun.WorkReconcileV1
	startErr     error
	routeErr     error
	advanceErr   error
	reconcileErr error
	started      []OutcomeStartRequest
	routeCalls   []stubRuntimeRouteRequest
	advanced     []stubRuntimeAdvanceRequest
	reconciled   []stubRuntimeReconcileRequest
}

type stubRuntimeRouteRequest struct {
	workRunID        string
	expectedRevision string
	choice           workrun.WorkRouteChoice
	runRef           string
}

type stubRuntimeAdvanceRequest struct {
	workRunID        string
	expectedRevision string
}

type stubRuntimeReconcileRequest struct {
	workRunID        string
	expectedRevision string
	diagnosticRef    string
}

func (runtime *stubRuntimeOutcome) Capabilities(
	context.Context,
) (RuntimeCapabilitiesV1, error) {
	return runtime.capabilities, nil
}

func (runtime *stubRuntimeOutcome) StartOutcome(
	_ context.Context,
	request OutcomeStartRequest,
) (workrun.WorkStatusV1, error) {
	runtime.started = append(runtime.started, request)
	return runtime.status, runtime.startErr
}

func (runtime *stubRuntimeOutcome) DecideRoute(
	_ context.Context,
	workRunID string,
	expectedRevision string,
	choice workrun.WorkRouteChoice,
) (workrun.WorkRouteV1, error) {
	runtime.routeCalls = append(runtime.routeCalls, stubRuntimeRouteRequest{
		workRunID:        workRunID,
		expectedRevision: expectedRevision,
		choice:           choice,
	})
	return runtime.route, runtime.routeErr
}

func (runtime *stubRuntimeOutcome) BindSDDRoute(
	_ context.Context,
	workRunID string,
	expectedRevision string,
	runRef string,
) (workrun.WorkRouteV1, error) {
	runtime.routeCalls = append(runtime.routeCalls, stubRuntimeRouteRequest{
		workRunID:        workRunID,
		expectedRevision: expectedRevision,
		runRef:           runRef,
	})
	return runtime.route, runtime.routeErr
}

func (runtime *stubRuntimeOutcome) AdvanceOutcome(
	_ context.Context,
	workRunID string,
	expectedRevision string,
) (workrun.WorkAdvanceV1, error) {
	runtime.advanced = append(runtime.advanced, stubRuntimeAdvanceRequest{
		workRunID:        workRunID,
		expectedRevision: expectedRevision,
	})
	return runtime.advance, runtime.advanceErr
}

func (runtime *stubRuntimeOutcome) ReconcileOutcome(
	_ context.Context,
	workRunID string,
	expectedRevision string,
	diagnosticRef string,
) (workrun.WorkReconcileV1, error) {
	runtime.reconciled = append(
		runtime.reconciled,
		stubRuntimeReconcileRequest{
			workRunID:        workRunID,
			expectedRevision: expectedRevision,
			diagnosticRef:    diagnosticRef,
		},
	)
	if runtime.reconcileErr != nil {
		return workrun.WorkReconcileV1{}, runtime.reconcileErr
	}
	if runtime.reconcile == nil {
		return workrun.WorkReconcileV1{}, errors.New(
			"stub runtime reconcile is not configured",
		)
	}
	return *runtime.reconcile, nil
}

func TestRuntimeControllerNegotiatesBeforeOpenOrPayloadValidation(t *testing.T) {
	t.Parallel()

	opener := &stubRuntimeOutcomeOpener{
		err: errors.New("runtime opener must not be called"),
	}
	controller := NewRuntimeController(opener)

	capabilities, err := controller.Capabilities(
		context.Background(),
		RuntimeCapabilitiesRequest{Contract: ""},
	)
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if capabilities.Diagnostic == nil ||
		capabilities.Diagnostic.Operation !=
			workrun.DiagnosticOperationWorkCapabilities {
		t.Fatalf("Capabilities() result = %#v", capabilities)
	}

	start, err := controller.Start(context.Background(), RuntimeStartRequest{
		Contract: "", Payload: []byte(`{"not":"validated"}`),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if start.Diagnostic == nil ||
		start.Diagnostic.Operation != workrun.DiagnosticOperationWorkStart {
		t.Fatalf("Start() result = %#v", start)
	}
	advance, err := controller.Advance(
		context.Background(),
		RuntimeAdvanceRequest{Contract: ""},
	)
	if err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	if advance.Diagnostic == nil ||
		advance.Diagnostic.Operation != workrun.DiagnosticOperationWorkAdvance {
		t.Fatalf("Advance() result = %#v", advance)
	}
	if opener.calls != 0 {
		t.Fatalf("unsupported contracts opened runtime %d times", opener.calls)
	}
}

func TestRuntimeControllerNegotiatesRouteBeforeValidationOrOpen(t *testing.T) {
	t.Parallel()
	opener := &stubRuntimeOutcomeOpener{
		err: errors.New("runtime opener must not be called"),
	}
	controller := NewRuntimeController(opener)
	decision, err := controller.DecideRoute(
		context.Background(),
		RuntimeRouteDecisionRequest{Contract: ""},
	)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := controller.BindSDDRoute(
		context.Background(),
		RuntimeRouteBindSDDRequest{Contract: ""},
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, result := range map[string]RuntimeRouteResult{
		"decide": decision,
		"bind":   binding,
	} {
		if result.Diagnostic == nil ||
			result.Diagnostic.Operation !=
				workrun.DiagnosticOperationWorkRoute ||
			result.Diagnostic.SupportedContracts[0] !=
				workrun.WorkRouteContractV1 {
			t.Fatalf("%s route diagnostic = %#v", name, result)
		}
	}
	if opener.calls != 0 {
		t.Fatalf("unsupported route contracts opened runtime %d times", opener.calls)
	}
}

func TestRuntimeControllerReconcileRejectsInvalidEnvelopeBeforeOpen(
	t *testing.T,
) {
	t.Parallel()
	base := RuntimeReconcileRequest{
		Repo:             "/repo",
		WorkRunID:        "reconcile-run",
		ExpectedRevision: runtimeContractRef("reconcile-previous"),
		DiagnosticRef:    runtimeContractRef("reconcile-diagnostic"),
		Contract:         workrun.WorkReconcileContractV1,
	}
	tests := []struct {
		name   string
		mutate func(*RuntimeReconcileRequest)
	}{
		{
			name: "work run identifier",
			mutate: func(request *RuntimeReconcileRequest) {
				request.WorkRunID = "INVALID"
			},
		},
		{
			name: "expected revision",
			mutate: func(request *RuntimeReconcileRequest) {
				request.ExpectedRevision = "sha256:invalid"
			},
		},
		{
			name: "diagnostic reference",
			mutate: func(request *RuntimeReconcileRequest) {
				request.DiagnosticRef = "sha256:invalid"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opener := &stubRuntimeOutcomeOpener{
				err: errors.New("runtime must not open"),
			}
			request := base
			test.mutate(&request)
			if _, err := NewRuntimeController(opener).Reconcile(
				context.Background(),
				request,
			); err == nil {
				t.Fatal("Reconcile() accepted an invalid request envelope")
			}
			if opener.calls != 0 {
				t.Fatalf("invalid request opened runtime %d times", opener.calls)
			}
		})
	}
}

func TestRuntimeControllerReconcileRejectsDivergentProviderResult(
	t *testing.T,
) {
	t.Parallel()
	base := runtimeContractReconcileFixture(t)
	request := RuntimeReconcileRequest{
		Repo:             "/repo",
		WorkRunID:        base.Status.WorkRunID,
		ExpectedRevision: base.PreviousRevision,
		DiagnosticRef:    base.DiagnosticRef,
		Contract:         workrun.WorkReconcileContractV1,
	}
	tests := []struct {
		name   string
		mutate func(*workrun.WorkReconcileV1)
	}{
		{
			name: "status work run",
			mutate: func(result *workrun.WorkReconcileV1) {
				result.Status.WorkRunID = "different-run"
			},
		},
		{
			name: "previous revision",
			mutate: func(result *workrun.WorkReconcileV1) {
				result.PreviousRevision =
					runtimeContractRef("different-previous")
			},
		},
		{
			name: "diagnostic reference",
			mutate: func(result *workrun.WorkReconcileV1) {
				result.DiagnosticRef =
					runtimeContractRef("different-diagnostic")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := base
			test.mutate(&result)
			if err := result.Validate(); err != nil {
				t.Fatalf("divergent fixture is not schema-valid: %v", err)
			}
			runtime := &stubRuntimeOutcome{reconcile: &result}
			opener := &stubRuntimeOutcomeOpener{runtime: runtime}
			reconciled, err := NewRuntimeController(opener).Reconcile(
				context.Background(),
				request,
			)
			if !errors.Is(err, ErrProviderResultMismatch) {
				t.Fatalf("Reconcile() error = %v", err)
			}
			if reconciled != (RuntimeReconcileResult{}) ||
				opener.calls != 1 ||
				len(runtime.reconciled) != 1 {
				t.Fatalf(
					"mismatch result/open/calls = %#v/%d/%#v",
					reconciled,
					opener.calls,
					runtime.reconciled,
				)
			}
		})
	}
}

func TestRuntimeControllerForwardsOnlyRouteChoiceOrRunReference(t *testing.T) {
	t.Parallel()
	previous := runtimeContractRef("route-previous")
	accepted := runtimeContractAcceptedRoute(previous)
	runtime := &stubRuntimeOutcome{route: accepted}
	opener := &stubRuntimeOutcomeOpener{runtime: runtime}
	controller := NewRuntimeController(opener)
	result, err := controller.DecideRoute(
		context.Background(),
		RuntimeRouteDecisionRequest{
			Repo:             "/repo",
			WorkRunID:        accepted.Status.WorkRunID,
			ExpectedRevision: previous,
			Contract:         workrun.WorkRouteContractV1,
			Choice:           workrun.WorkRouteChoiceAcceptSDD,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Route == nil ||
		!reflect.DeepEqual(*result.Route, accepted) ||
		len(runtime.routeCalls) != 1 ||
		runtime.routeCalls[0].choice !=
			workrun.WorkRouteChoiceAcceptSDD {
		t.Fatalf("route decision result/calls = %#v/%#v", result, runtime.routeCalls)
	}

	bound := accepted
	bound.Operation = workrun.WorkRouteOperationBindSDD
	bound.Choice = ""
	bound.Status.Revision = runtimeContractRef("route-bound")
	bound.Status.PublicState = workrun.PublicStateWorking
	bound.Status.RoutePhase = workrun.RoutePhaseImplementationSelected
	bound.Status.ImplementationRoute = workrun.ImplementationRouteSDD
	bound.Status.SDDRunRef = "organic-recovery"
	runtime.route = bound
	result, err = controller.BindSDDRoute(
		context.Background(),
		RuntimeRouteBindSDDRequest{
			Repo:             "/repo",
			WorkRunID:        bound.Status.WorkRunID,
			ExpectedRevision: previous,
			Contract:         workrun.WorkRouteContractV1,
			RunRef:           "organic-recovery",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Route == nil ||
		!reflect.DeepEqual(*result.Route, bound) ||
		len(runtime.routeCalls) != 2 ||
		runtime.routeCalls[1].runRef != "organic-recovery" {
		t.Fatalf("route binding result/calls = %#v/%#v", result, runtime.routeCalls)
	}
}

func TestRuntimeControllerRejectsRouteInferenceAndMismatchedResults(
	t *testing.T,
) {
	t.Parallel()
	previous := runtimeContractRef("route-reject-previous")
	accepted := runtimeContractAcceptedRoute(previous)
	runtime := &stubRuntimeOutcome{route: accepted}
	opener := &stubRuntimeOutcomeOpener{runtime: runtime}
	controller := NewRuntimeController(opener)
	_, err := controller.DecideRoute(
		context.Background(),
		RuntimeRouteDecisionRequest{
			Repo:             "/repo",
			WorkRunID:        accepted.Status.WorkRunID,
			ExpectedRevision: previous,
			Contract:         workrun.WorkRouteContractV1,
			Choice:           workrun.WorkRouteChoice("direct_inline"),
		},
	)
	if err == nil || !strings.Contains(err.Error(), "accept_sdd or decline_sdd") ||
		opener.calls != 0 {
		t.Fatalf("caller-authored route error/calls = %v/%d", err, opener.calls)
	}
	_, err = controller.BindSDDRoute(
		context.Background(),
		RuntimeRouteBindSDDRequest{
			Repo:             "/repo",
			WorkRunID:        accepted.Status.WorkRunID,
			ExpectedRevision: previous,
			Contract:         workrun.WorkRouteContractV1,
			RunRef:           "sdd:invented",
		},
	)
	if err == nil || !strings.Contains(err.Error(), "canonical existing") ||
		opener.calls != 0 {
		t.Fatalf("invalid SDD run error/calls = %v/%d", err, opener.calls)
	}

	mismatched := accepted
	mismatched.Status.WorkRunID = "other-work-run"
	runtime.route = mismatched
	_, err = controller.DecideRoute(
		context.Background(),
		RuntimeRouteDecisionRequest{
			Repo:             "/repo",
			WorkRunID:        accepted.Status.WorkRunID,
			ExpectedRevision: previous,
			Contract:         workrun.WorkRouteContractV1,
			Choice:           workrun.WorkRouteChoiceAcceptSDD,
		},
	)
	if !errors.Is(err, ErrProviderResultMismatch) {
		t.Fatalf("mismatched route error = %v", err)
	}
}

func TestRuntimeControllerReturnsTypedUnmanagedOutcomeBeforeWorkRun(
	t *testing.T,
) {
	t.Parallel()
	runtime := &stubRuntimeOutcome{startErr: ErrOutcomeNotManaged}
	opener := &stubRuntimeOutcomeOpener{runtime: runtime}
	result, err := NewRuntimeController(opener).Start(
		context.Background(),
		RuntimeStartRequest{
			Repo:     "/repo",
			Contract: workrun.WorkStartContractV1,
			Payload:  []byte(`{"outcome":"Explain the current behavior."}`),
		},
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.Status != nil || result.Diagnostic == nil {
		t.Fatalf("Start() result = %#v", result)
	}
	diagnostic := *result.Diagnostic
	if diagnostic.Code != "outcome_not_managed" ||
		diagnostic.Operation != workrun.DiagnosticOperationWorkStart ||
		diagnostic.MutationOutcome != "not_started" ||
		diagnostic.NextAction != "continue_unmanaged" ||
		!reflect.DeepEqual(
			diagnostic.SupportedContracts,
			[]string{workrun.WorkStartContractV1},
		) {
		t.Fatalf("unmanaged diagnostic = %#v", diagnostic)
	}
	if err := diagnostic.Validate(); err != nil {
		t.Fatalf("diagnostic.Validate() error = %v", err)
	}
}

func TestRuntimeControllerReturnsRepositoryBoundHandshakeAndWorkRun(t *testing.T) {
	t.Parallel()

	canonical, err := capabilitymanifest.ForAgent(model.AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &stubRuntimeOutcome{
		capabilities: RuntimeCapabilitiesV1{
			Schema:        workrun.WorkCapabilitiesContractV1,
			Contract:      workrun.WorkCapabilitiesContractV1,
			RepositoryRef: runtimeContractRef("repository"),
			AgentID:       model.AgentCodex,
			WorkRouting: RuntimeCapabilityClaimV1{
				ID:                    workrun.WorkRoutingCapabilityV1,
				Exposure:              WorkRoutingAdvertised,
				ImplementationRouting: canonical.ImplementationRouting,
			},
			Contracts: RuntimeContractSetV1{
				Start:      workrun.WorkStartContractV1,
				Route:      workrun.WorkRouteContractV1,
				Advance:    workrun.WorkAdvanceContractV1,
				Reconcile:  workrun.WorkReconcileContractV1,
				Status:     workrun.WorkStatusContractV1,
				Transition: workrun.WorkTransitionContractV1,
			},
			ConnectorSessionRef: runtimeContractRef("connector-session"),
		},
		status: workrun.WorkStatusV1{
			Schema:              workrun.WorkStatusContractV1,
			Contract:            workrun.WorkStatusContractV1,
			WorkRunID:           "work-runtime-contract",
			Revision:            runtimeContractRef("revision"),
			PublicState:         workrun.PublicStateWorking,
			RouteDecision:       workrun.RouteDecisionDirectInline,
			RoutePhase:          workrun.RoutePhaseImplementationSelected,
			ImplementationRoute: workrun.ImplementationRouteDirectInline,
			Verification: workrun.VerificationSummaryV1{
				Outcome:    workrun.VerificationPending,
				ResultRefs: []string{},
			},
		},
	}
	opener := &stubRuntimeOutcomeOpener{runtime: runtime}
	controller := NewRuntimeController(opener)

	capabilities, err := controller.Capabilities(
		context.Background(),
		RuntimeCapabilitiesRequest{
			Repo: "/repo", Contract: workrun.WorkCapabilitiesContractV1,
		},
	)
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if capabilities.Capabilities == nil ||
		capabilities.Capabilities.ConnectorSessionRef !=
			runtime.capabilities.ConnectorSessionRef {
		t.Fatalf("Capabilities() result = %#v", capabilities)
	}

	start, err := controller.Start(context.Background(), RuntimeStartRequest{
		Repo: "/repo", Contract: workrun.WorkStartContractV1,
		Payload: []byte(
			`{"outcome":"Create the requested poster.","explicitSddRequested":false}`,
		),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if start.Status == nil ||
		start.Status.WorkRunID != runtime.status.WorkRunID {
		t.Fatalf("Start() result = %#v", start)
	}
	if len(runtime.started) != 1 ||
		runtime.started[0] != (OutcomeStartRequest{
			Outcome: "Create the requested poster.",
		}) {
		t.Fatalf("runtime requests = %#v", runtime.started)
	}
	if opener.calls != 2 || opener.repo != "/repo" {
		t.Fatalf("opener calls/repo = %d/%q", opener.calls, opener.repo)
	}
}

func TestRuntimeControllerAdvanceRejectsInvalidAuthorityBeforeOpen(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		workRun  string
		revision string
		want     string
	}{
		{
			name:     "invalid work run",
			workRun:  "not canonical!",
			revision: runtimeContractRef("advance-previous"),
			want:     "--work-run",
		},
		{
			name:     "invalid expected revision",
			workRun:  "work-runtime-advance",
			revision: "sha256:ABC",
			want:     "--expected-revision",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opener := &stubRuntimeOutcomeOpener{
				err: errors.New("invalid authority must not open runtime"),
			}
			result, err := NewRuntimeController(opener).Advance(
				context.Background(),
				RuntimeAdvanceRequest{
					Repo:             "/repo",
					WorkRunID:        test.workRun,
					ExpectedRevision: test.revision,
					Contract:         workrun.WorkAdvanceContractV1,
				},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Advance() result/error = %#v, %v", result, err)
			}
			if opener.calls != 0 {
				t.Fatalf("invalid authority opened runtime %d times", opener.calls)
			}
		})
	}
}

func TestRuntimeControllerAdvanceReturnsExactTerminalBranches(t *testing.T) {
	t.Parallel()
	const workRunID = "work-runtime-advance"
	expectedRevision := runtimeContractRef("advance-previous")
	tests := []struct {
		name    string
		advance workrun.WorkAdvanceV1
	}{
		{
			name: "ready",
			advance: runtimeContractReadyAdvance(
				workRunID,
				expectedRevision,
			),
		},
		{
			name: "needs decision",
			advance: runtimeContractDecisionAdvance(
				workRunID,
				expectedRevision,
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &stubRuntimeOutcome{advance: test.advance}
			opener := &stubRuntimeOutcomeOpener{runtime: runtime}
			request := RuntimeAdvanceRequest{
				Repo:             "/repo",
				WorkRunID:        workRunID,
				ExpectedRevision: expectedRevision,
				Contract:         workrun.WorkAdvanceContractV1,
			}
			result, err := NewRuntimeController(opener).Advance(
				context.Background(),
				request,
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Diagnostic != nil || result.Advance == nil ||
				!reflect.DeepEqual(*result.Advance, test.advance) ||
				!reflect.DeepEqual(result.Output(), test.advance) {
				t.Fatalf("Advance() result = %#v", result)
			}
			if opener.calls != 1 || opener.repo != request.Repo ||
				!reflect.DeepEqual(
					runtime.advanced,
					[]stubRuntimeAdvanceRequest{{
						workRunID:        request.WorkRunID,
						expectedRevision: request.ExpectedRevision,
					}},
				) {
				t.Fatalf(
					"Advance() open/call = %d/%q/%#v",
					opener.calls,
					opener.repo,
					runtime.advanced,
				)
			}
		})
	}
}

func TestRuntimeControllerAdvanceRejectsInvalidOrMismatchedProviderResult(
	t *testing.T,
) {
	t.Parallel()
	const workRunID = "work-runtime-advance"
	expectedRevision := runtimeContractRef("advance-previous")
	base := runtimeContractReadyAdvance(workRunID, expectedRevision)
	tests := []struct {
		name     string
		mutate   func(*workrun.WorkAdvanceV1)
		mismatch bool
	}{
		{
			name: "invalid response",
			mutate: func(advance *workrun.WorkAdvanceV1) {
				advance.Schema = ""
			},
		},
		{
			name: "different work run",
			mutate: func(advance *workrun.WorkAdvanceV1) {
				advance.Status.WorkRunID = "work-runtime-other"
			},
			mismatch: true,
		},
		{
			name: "different previous revision",
			mutate: func(advance *workrun.WorkAdvanceV1) {
				advance.PreviousRevision =
					runtimeContractRef("another-previous")
			},
			mismatch: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			advance := base
			test.mutate(&advance)
			runtime := &stubRuntimeOutcome{advance: advance}
			opener := &stubRuntimeOutcomeOpener{runtime: runtime}
			result, err := NewRuntimeController(opener).Advance(
				context.Background(),
				RuntimeAdvanceRequest{
					Repo:             "/repo",
					WorkRunID:        workRunID,
					ExpectedRevision: expectedRevision,
					Contract:         workrun.WorkAdvanceContractV1,
				},
			)
			if err == nil || result.Advance != nil || result.Diagnostic != nil {
				t.Fatalf("Advance() result/error = %#v, %v", result, err)
			}
			if test.mismatch != errors.Is(err, ErrProviderResultMismatch) {
				t.Fatalf(
					"Advance() mismatch classification = %v, want %t",
					err,
					test.mismatch,
				)
			}
			if !test.mismatch &&
				!strings.Contains(err.Error(), "validate productive runtime advance") {
				t.Fatalf("Advance() validation error = %v", err)
			}
			if opener.calls != 1 || len(runtime.advanced) != 1 {
				t.Fatalf(
					"Advance() provider calls = %d/%d",
					opener.calls,
					len(runtime.advanced),
				)
			}
		})
	}
}

func TestRuntimeCapabilitiesValidateAdvertisementBinding(t *testing.T) {
	t.Parallel()

	canonical, err := capabilitymanifest.ForAgent(model.AgentPi)
	if err != nil {
		t.Fatal(err)
	}
	base := RuntimeCapabilitiesV1{
		Schema:        workrun.WorkCapabilitiesContractV1,
		Contract:      workrun.WorkCapabilitiesContractV1,
		RepositoryRef: runtimeContractRef("repository"),
		AgentID:       model.AgentPi,
		WorkRouting: RuntimeCapabilityClaimV1{
			ID:                    workrun.WorkRoutingCapabilityV1,
			Exposure:              WorkRoutingAdvertised,
			ImplementationRouting: canonical.ImplementationRouting,
		},
		Contracts: RuntimeContractSetV1{
			Start:      workrun.WorkStartContractV1,
			Route:      workrun.WorkRouteContractV1,
			Advance:    workrun.WorkAdvanceContractV1,
			Reconcile:  workrun.WorkReconcileContractV1,
			Status:     workrun.WorkStatusContractV1,
			Transition: workrun.WorkTransitionContractV1,
		},
		ConnectorSessionRef: runtimeContractRef("connector"),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	dormant := base
	dormant.WorkRouting.Exposure = WorkRoutingDormant
	dormant.ConnectorSessionRef = ""
	if err := dormant.Validate(); err != nil {
		t.Fatalf("dormant Validate() error = %v", err)
	}

	dormantPayload, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"contracts",
		"work-routing",
		"v1",
		"fixtures",
		"work-capabilities-dormant.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var dormantFixture RuntimeCapabilitiesV1
	if err := json.Unmarshal(dormantPayload, &dormantFixture); err != nil {
		t.Fatal(err)
	}
	if err := dormantFixture.Validate(); err != nil {
		t.Fatalf("dormant fixture Validate() error = %v", err)
	}
	if dormantFixture.WorkRouting.Exposure != WorkRoutingDormant ||
		dormantFixture.ConnectorSessionRef != "" {
		t.Fatalf("dormant fixture = %#v", dormantFixture)
	}

	unicodeRefs := base
	unicodeRefs.RepositoryRef = strings.Repeat("界", 240)
	unicodeRefs.ConnectorSessionRef = strings.Repeat("界", 240)
	if err := unicodeRefs.Validate(); err != nil {
		t.Fatalf("240-code-point references Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*RuntimeCapabilitiesV1)
		want   string
	}{
		{
			name: "advertised without connector",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.ConnectorSessionRef = ""
			},
			want: "connectorSessionRef",
		},
		{
			name: "dormant with connector",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.WorkRouting.Exposure = WorkRoutingDormant
			},
			want: "dormant",
		},
		{
			name: "routing drift",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.WorkRouting.ImplementationRouting.DirectInline.
					MaxUnderstandingFiles = 4
			},
			want: "canonical ACI",
		},
		{
			name: "repository over 240 Unicode code points",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.RepositoryRef = strings.Repeat("界", 241)
			},
			want: "canonical",
		},
		{
			name: "repository with U+0085 edge",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.RepositoryRef = "\u0085repository"
			},
			want: "canonical",
		},
		{
			name: "repository with NBSP edge",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.RepositoryRef = "\u00A0repository"
			},
			want: "canonical",
		},
		{
			name: "repository with U+2003 edge",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.RepositoryRef += "\u2003"
			},
			want: "canonical",
		},
		{
			name: "connector with BOM edge",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.ConnectorSessionRef += "\uFEFF"
			},
			want: "canonical",
		},
		{
			name: "repository with invalid UTF-8",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.RepositoryRef = string([]byte{0xff})
			},
			want: "canonical",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			err := value.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDecodeOutcomeStartEnvelopeIsStrictAndBounded(t *testing.T) {
	t.Parallel()

	valid, err := decodeOutcomeStartEnvelope(
		[]byte(`{"outcome":"Fix the typo.","explicitSddRequested":true}`),
	)
	if err != nil {
		t.Fatalf("decodeOutcomeStartEnvelope() error = %v", err)
	}
	if valid.Outcome != "Fix the typo." || !valid.ExplicitSDDRequested {
		t.Fatalf("decoded request = %#v", valid)
	}

	multiline, err := decodeOutcomeStartEnvelope(
		[]byte(`{"outcome":"First line.\nSecond line."}`),
	)
	if err != nil {
		t.Fatalf("multiline outcome error = %v", err)
	}
	if multiline.Outcome != "First line.\nSecond line." {
		t.Fatalf("multiline outcome = %q", multiline.Outcome)
	}

	pairedSurrogate, err := decodeOutcomeStartEnvelope(
		[]byte(`{"outcome":"\ud83d\ude00"}`),
	)
	if err != nil {
		t.Fatalf("paired surrogate outcome error = %v", err)
	}
	if pairedSurrogate.Outcome != "😀" {
		t.Fatalf("paired surrogate outcome = %q", pairedSurrogate.Outcome)
	}
	escapedSurrogateText, err := decodeOutcomeStartEnvelope(
		[]byte(`{"outcome":"literal \\ud800 text"}`),
	)
	if err != nil {
		t.Fatalf("escaped surrogate text error = %v", err)
	}
	if escapedSurrogateText.Outcome != `literal \ud800 text` {
		t.Fatalf(
			"escaped surrogate text = %q",
			escapedSurrogateText.Outcome,
		)
	}

	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "unknown field",
			payload: `{"outcome":"Fix it.","route":"direct_main"}`,
			want:    "unknown field",
		},
		{
			name:    "duplicate",
			payload: `{"outcome":"Fix it.","outcome":"Replace it."}`,
			want:    "repeats field",
		},
		{
			name:    "null explicit SDD intent",
			payload: `{"outcome":"Fix it.","explicitSddRequested":null}`,
			want:    "must be boolean",
		},
		{
			name:    "trailing",
			payload: `{"outcome":"Fix it."}{}`,
			want:    "trailing JSON",
		},
		{
			name:    "non canonical outcome",
			payload: `{"outcome":" Fix it."}`,
			want:    "canonical outcome",
		},
		{
			name:    "leading newline",
			payload: `{"outcome":"\nFix it."}`,
			want:    "canonical outcome",
		},
		{
			name:    "leading U+0085",
			payload: `{"outcome":"\u0085Fix it."}`,
			want:    "canonical outcome",
		},
		{
			name:    "leading NBSP",
			payload: `{"outcome":"\u00A0Fix it."}`,
			want:    "canonical outcome",
		},
		{
			name:    "trailing U+2003",
			payload: `{"outcome":"Fix it.\u2003"}`,
			want:    "canonical outcome",
		},
		{
			name:    "trailing BOM",
			payload: `{"outcome":"Fix it.\uFEFF"}`,
			want:    "canonical outcome",
		},
		{
			name:    "unpaired high surrogate",
			payload: `{"outcome":"\ud800"}`,
			want:    "unpaired Unicode surrogate",
		},
		{
			name:    "unpaired low surrogate",
			payload: `{"outcome":"\udc00"}`,
			want:    "unpaired Unicode surrogate",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeOutcomeStartEnvelope([]byte(test.payload))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf(
					"decodeOutcomeStartEnvelope() error = %v, want %q",
					err,
					test.want,
				)
			}
		})
	}

	invalidUTF8Payload := append(
		[]byte(`{"outcome":"`),
		0xff,
	)
	invalidUTF8Payload = append(invalidUTF8Payload, []byte(`"}`)...)
	if _, err := decodeOutcomeStartEnvelope(invalidUTF8Payload); err == nil ||
		!strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("invalid UTF-8 payload error = %v", err)
	}

	for _, test := range []struct {
		name    string
		payload []byte
		valid   bool
	}{
		{
			name: "compact UTF-8 at code-point boundary",
			payload: []byte(
				`{"outcome":"` +
					strings.Repeat("界", maximumOutcomeRequestCodePoints) +
					`"}`,
			),
			valid: true,
		},
		{
			name: "compact UTF-8 over code-point boundary",
			payload: []byte(
				`{"outcome":"` +
					strings.Repeat(
						"界",
						maximumOutcomeRequestCodePoints+1,
					) +
					`"}`,
			),
		},
		{
			name: "escaped Unicode at code-point boundary",
			payload: []byte(
				`{"outcome":"` +
					strings.Repeat(
						`\ud83d\ude00`,
						maximumOutcomeRequestCodePoints,
					) +
					`"}`,
			),
			valid: true,
		},
		{
			name: "escaped Unicode over code-point boundary",
			payload: []byte(
				`{"outcome":"` +
					strings.Repeat(
						`\ud83d\ude00`,
						maximumOutcomeRequestCodePoints+1,
					) +
					`"}`,
			),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := decodeOutcomeStartEnvelope(test.payload)
			if (err == nil) != test.valid {
				t.Fatalf(
					"decodeOutcomeStartEnvelope() error = %v, want valid=%t",
					err,
					test.valid,
				)
			}
			if test.valid &&
				utf8.RuneCountInString(request.Outcome) !=
					maximumOutcomeRequestCodePoints {
				t.Fatalf(
					"decoded outcome code points = %d",
					utf8.RuneCountInString(request.Outcome),
				)
			}
		})
	}

	if err := (OutcomeStartRequest{
		Outcome: string([]byte{0xff}),
	}).validate(); err == nil {
		t.Fatal("OutcomeStartRequest.validate() accepted invalid UTF-8")
	}

	_, err = decodeOutcomeStartEnvelope(
		make([]byte, maximumOutcomeStartEnvelopeBytes+1),
	)
	if err == nil || !strings.Contains(err.Error(), "bounded") {
		t.Fatalf("oversize error = %v", err)
	}
}

func runtimeContractReconcileFixture(t *testing.T) workrun.WorkReconcileV1 {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"contracts",
		"work-routing",
		"v1",
		"fixtures",
		"work-reconcile-delivered.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var reconcile workrun.WorkReconcileV1
	if err := json.Unmarshal(payload, &reconcile); err != nil {
		t.Fatal(err)
	}
	if err := reconcile.Validate(); err != nil {
		t.Fatalf("reconcile fixture Validate() error = %v", err)
	}
	return reconcile
}

func runtimeContractReadyAdvance(
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
			Revision:            runtimeContractRef("advance-ready"),
			PublicState:         workrun.PublicStateReady,
			RouteDecision:       workrun.RouteDecisionDirectInline,
			RoutePhase:          workrun.RoutePhaseImplementationSelected,
			ImplementationRoute: workrun.ImplementationRouteDirectInline,
			Verification: workrun.VerificationSummaryV1{
				Outcome: workrun.VerificationNotRequired,
				ResultRefs: []string{
					runtimeContractRef("verification-result"),
				},
			},
			DeliveryIntentRef: runtimeContractRef("delivery-intent"),
			ReviewReceiptRef:  runtimeContractRef("review-receipt"),
		},
		DeliveryResultRef: runtimeContractRef("delivery-result"),
	}
}

func runtimeContractDecisionAdvance(
	workRunID string,
	previousRevision string,
) workrun.WorkAdvanceV1 {
	code := workrun.WorkAdvanceDiagnosticScopeMismatch
	message, ok := workrun.WorkAdvanceDiagnosticMessage(code)
	if !ok {
		panic("closed diagnostic code is unavailable")
	}
	diagnostic := &workrun.WorkAdvanceDiagnosticV1{
		Ref:        runtimeContractRef("advance-diagnostic"),
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
			Revision:            runtimeContractRef("advance-decision"),
			PublicState:         workrun.PublicStateNeedsYourDecision,
			RouteDecision:       workrun.RouteDecisionDelegatedDirect,
			RoutePhase:          workrun.RoutePhaseImplementationSelected,
			ImplementationRoute: workrun.ImplementationRouteDelegatedDirect,
			Verification: workrun.VerificationSummaryV1{
				Outcome:    workrun.VerificationPending,
				ResultRefs: []string{},
			},
			DeliveryIntentRef: runtimeContractRef("delivery-intent"),
			Diagnostic:        diagnostic,
		},
		Diagnostic: diagnostic,
	}
}

func runtimeContractAcceptedRoute(
	previousRevision string,
) workrun.WorkRouteV1 {
	return workrun.WorkRouteV1{
		Schema:           workrun.WorkRouteContractV1,
		Contract:         workrun.WorkRouteContractV1,
		Operation:        workrun.WorkRouteOperationDecideSDD,
		Choice:           workrun.WorkRouteChoiceAcceptSDD,
		PreviousRevision: previousRevision,
		SelectedRoute:    workrun.ImplementationRouteSDD,
		Status: workrun.WorkStatusV1{
			Schema:        workrun.WorkStatusContractV1,
			Contract:      workrun.WorkStatusContractV1,
			WorkRunID:     "work-runtime-route",
			Revision:      runtimeContractRef("route-accepted"),
			PublicState:   workrun.PublicStateWorking,
			RouteDecision: workrun.RouteDecisionProposeSDD,
			RoutePhase:    workrun.RoutePhaseSDDRuntimePending,
			Verification: workrun.VerificationSummaryV1{
				Outcome:    workrun.VerificationPending,
				ResultRefs: []string{},
			},
			DeliveryIntentRef: runtimeContractRef("route-intent"),
		},
	}
}

func runtimeContractRef(seed string) string {
	return testPADRef("runtime-contract-" + seed)
}
