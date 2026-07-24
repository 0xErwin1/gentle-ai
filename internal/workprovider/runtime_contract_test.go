package workprovider

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

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
	advance      workrun.WorkAdvanceV1
	startErr     error
	advanceErr   error
	started      []OutcomeStartRequest
	advanced     []stubRuntimeAdvanceRequest
}

type stubRuntimeAdvanceRequest struct {
	workRunID        string
	expectedRevision string
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
				Advance:    workrun.WorkAdvanceContractV1,
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
			Advance:    workrun.WorkAdvanceContractV1,
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
			name:    "trailing",
			payload: `{"outcome":"Fix it."}{}`,
			want:    "trailing JSON",
		},
		{
			name:    "non canonical outcome",
			payload: `{"outcome":" Fix it."}`,
			want:    "canonical outcome",
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

	_, err = decodeOutcomeStartEnvelope(
		make([]byte, maximumOutcomeStartEnvelopeBytes+1),
	)
	if err == nil || !strings.Contains(err.Error(), "bounded") {
		t.Fatalf("oversize error = %v", err)
	}
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
			ImplementationRoute: workrun.ImplementationRouteDelegatedDirect,
			Verification: workrun.VerificationSummaryV1{
				Outcome:    workrun.VerificationPending,
				ResultRefs: []string{},
			},
			DeliveryIntentRef: runtimeContractRef("delivery-intent"),
		},
		Diagnostic: &workrun.WorkAdvanceDiagnosticV1{
			Ref:     runtimeContractRef("advance-diagnostic"),
			Code:    code,
			Message: message,
		},
	}
}

func runtimeContractRef(seed string) string {
	return testPADRef("runtime-contract-" + seed)
}
