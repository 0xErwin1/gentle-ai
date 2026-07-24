package workrun

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

type productiveReconciliationTestPort struct {
	mu          sync.Mutex
	authorities map[string]ProductiveReconciliationAuthority
	errors      map[string]error
	calls       int
	failAfter   int
	failErr     error
}

func newProductiveReconciliationTestPort() *productiveReconciliationTestPort {
	return &productiveReconciliationTestPort{
		authorities: map[string]ProductiveReconciliationAuthority{},
		errors:      map[string]error{},
	}
}

func (port *productiveReconciliationTestPort) ResolveProductiveReconciliation(
	_ context.Context,
	ref string,
) (ProductiveReconciliationAuthority, error) {
	port.mu.Lock()
	defer port.mu.Unlock()
	port.calls++
	if port.failAfter > 0 && port.calls > port.failAfter {
		return ProductiveReconciliationAuthority{}, port.failErr
	}
	if err, exists := port.errors[ref]; exists {
		return ProductiveReconciliationAuthority{}, err
	}
	authority, exists := port.authorities[ref]
	if !exists {
		return ProductiveReconciliationAuthority{}, os.ErrNotExist
	}
	authority.Handoff = cloneHandoff(authority.Handoff)
	if authority.Diagnostic != nil {
		diagnostic := *authority.Diagnostic
		authority.Diagnostic = &diagnostic
	}
	return authority, nil
}

func (port *productiveReconciliationTestPort) register(
	authority ProductiveReconciliationAuthority,
) {
	port.mu.Lock()
	defer port.mu.Unlock()
	port.authorities[authority.ReconciliationRef] = authority
}

func (port *productiveReconciliationTestPort) callCount() int {
	port.mu.Lock()
	defer port.mu.Unlock()
	return port.calls
}

func TestRecordProductiveReconciliationPersistsOneOwnerOutcomeAndExactReplay(
	t *testing.T,
) {
	tests := []struct {
		name    string
		outcome WorkReconcileOutcome
	}{
		{
			name:    "delivery confirmed",
			outcome: WorkReconcileDeliveryConfirmed,
		},
		{
			name:    "no delivery confirmed",
			outcome: WorkReconcileNoDeliveryConfirmed,
		},
		{
			name:    "manual resolution required",
			outcome: WorkReconcileManualResolution,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, blocked, port :=
				newBlockedProductiveReconciliationFixtureForOutcome(
					t,
					test.outcome,
				)
			reconciliationRef := testSHARef(
				"productive-reconciliation-" + string(test.outcome),
			)
			authority := testProductiveReconciliationAuthority(
				t,
				store,
				blocked,
				reconciliationRef,
				test.outcome,
			)
			port.register(authority)
			request := RecordProductiveReconciliationRequest{
				ExpectedRevision:      blocked.Revision,
				RequestID:             "record-" + string(test.outcome),
				OriginalDiagnosticRef: blocked.ProductiveBlockerRef,
				ReconciliationRef:     reconciliationRef,
			}

			reconciled, err := store.RecordProductiveReconciliation(
				context.Background(),
				request,
			)
			if err != nil {
				t.Fatal(err)
			}
			if reconciled.Revision == blocked.Revision ||
				reconciled.ProductiveBlockerRef !=
					blocked.ProductiveBlockerRef ||
				reconciled.ProductiveBlockerSourceRevision !=
					blocked.ProductiveBlockerSourceRevision ||
				reconciled.ProductiveReconciliationRef !=
					reconciliationRef ||
				reconciled.ProductiveReconciliationSourceRevision !=
					blocked.Revision ||
				reconciled.ProductiveReconciliationOutcome !=
					test.outcome ||
				reconciled.DeliveryResultRef !=
					authority.DeliveryResultRef {
				t.Fatalf(
					"reconciled state = %#v, blocker = %#v, authority = %#v",
					reconciled,
					blocked,
					authority,
				)
			}

			reloaded, err := store.Status()
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Revision != reconciled.Revision ||
				reloaded.ProductiveReconciliationRef !=
					reconciliationRef ||
				reloaded.ProductiveReconciliationSourceRevision !=
					blocked.Revision ||
				reloaded.ProductiveReconciliationOutcome !=
					test.outcome ||
				reloaded.DeliveryResultRef !=
					authority.DeliveryResultRef {
				t.Fatalf("reloaded reconciliation = %#v", reloaded)
			}

			replayed, err := store.RecordProductiveReconciliation(
				context.Background(),
				request,
			)
			if err != nil {
				t.Fatalf("exact reconciliation replay failed: %v", err)
			}
			if replayed.Revision != reconciled.Revision ||
				replayed.ProductiveReconciliationRef !=
					reconciliationRef ||
				replayed.DeliveryResultRef !=
					authority.DeliveryResultRef {
				t.Fatalf(
					"exact reconciliation replay = %#v, want %#v",
					replayed,
					reconciled,
				)
			}
			if calls := port.callCount(); calls != 3 {
				t.Fatalf(
					"reconciliation authority calls = %d, want pre, post, and replay validation",
					calls,
				)
			}

			conflicting := request
			conflicting.OriginalDiagnosticRef = testSHARef(
				"changed-original-" + string(test.outcome),
			)
			if _, err := store.RecordProductiveReconciliation(
				context.Background(),
				conflicting,
			); !errors.Is(err, ErrWorkRunRequestConflict) {
				t.Fatalf("changed exact-request replay error = %v", err)
			}
			if calls := port.callCount(); calls != 3 {
				t.Fatalf(
					"request conflict unexpectedly resolved authority %d times",
					calls,
				)
			}

			secondRef := testSHARef(
				"second-" + string(test.outcome),
			)
			port.register(testProductiveReconciliationAuthority(
				t,
				store,
				reconciled,
				secondRef,
				test.outcome,
			))
			if _, err := store.RecordProductiveReconciliation(
				context.Background(),
				RecordProductiveReconciliationRequest{
					ExpectedRevision: reconciled.Revision,
					RequestID:        "second-" + string(test.outcome),
					OriginalDiagnosticRef: reconciled.
						ProductiveBlockerRef,
					ReconciliationRef: secondRef,
				},
			); !errors.Is(err, ErrWorkRunInvalidTransition) {
				t.Fatalf("second productive reconciliation error = %v", err)
			}

			if _, err := store.RecordProductiveBlocker(
				context.Background(),
				RecordProductiveBlockerRequest{
					ExpectedRevision: reconciled.Revision,
					RequestID:        "mutation-after-" + string(test.outcome),
					DiagnosticRef: testSHARef(
						"mutation-after-" + string(test.outcome),
					),
				},
			); !errors.Is(err, ErrWorkRunInvalidTransition) {
				t.Fatalf("ordinary mutation after reconciliation error = %v", err)
			}
		})
	}
}

func TestProductiveReconciliationRequiresExactOwnerAuthorityBeforeMutation(
	t *testing.T,
) {
	store, blocked, port := newBlockedProductiveReconciliationFixture(t)
	ref := testSHARef("required-productive-reconciliation")
	request := RecordProductiveReconciliationRequest{
		ExpectedRevision:      blocked.Revision,
		RequestID:             "required-productive-reconciliation",
		OriginalDiagnosticRef: blocked.ProductiveBlockerRef,
		ReconciliationRef:     ref,
	}
	if _, err := store.RecordProductiveReconciliation(
		context.Background(),
		request,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fabricated reconciliation error = %v", err)
	}
	assertProductiveReconciliationUnchanged(t, store, blocked)

	wrongOriginal := request
	wrongOriginal.RequestID = "wrong-original-productive-reconciliation"
	wrongOriginal.OriginalDiagnosticRef = testSHARef(
		"another-original-productive-diagnostic",
	)
	if _, err := store.RecordProductiveReconciliation(
		context.Background(),
		wrongOriginal,
	); !errors.Is(err, ErrWorkRunInvalidTransition) {
		t.Fatalf("wrong original diagnostic error = %v", err)
	}
	if calls := port.callCount(); calls != 1 {
		t.Fatalf(
			"wrong original diagnostic resolved owner authority %d additional times",
			calls-1,
		)
	}
	assertProductiveReconciliationUnchanged(t, store, blocked)

	mismatched := testProductiveReconciliationAuthority(
		t,
		store,
		blocked,
		ref,
		WorkReconcileNoDeliveryConfirmed,
	)
	mismatched.SourceRevision = testSHARef(
		"other-productive-reconciliation-source",
	)
	port.register(mismatched)
	if _, err := store.RecordProductiveReconciliation(
		context.Background(),
		request,
	); !errors.Is(err, ErrAuthorityBindingMismatch) {
		t.Fatalf("mismatched reconciliation authority error = %v", err)
	}
	assertProductiveReconciliationUnchanged(t, store, blocked)
}

func TestProductiveReconciliationCannotRunWithoutTerminalBlocker(t *testing.T) {
	repo := initWorkRunRepository(t)
	store := openTestWorkRunStore(t, repo, "reconciliation-without-blocker")
	started := startDirectWorkRun(t, store)
	port := newProductiveReconciliationTestPort()
	ports := store.authority
	ports.ProductiveReconciliation = port
	store = store.WithAuthorityPorts(ports)

	ref := testSHARef("reconciliation-without-blocker")
	if _, err := store.RecordProductiveReconciliation(
		context.Background(),
		RecordProductiveReconciliationRequest{
			ExpectedRevision: started.Revision,
			RequestID:        "reconciliation-without-blocker",
			OriginalDiagnosticRef: testSHARef(
				"missing-original-diagnostic",
			),
			ReconciliationRef: ref,
		},
	); !errors.Is(err, ErrWorkRunInvalidTransition) {
		t.Fatalf("reconciliation without blocker error = %v", err)
	}
	if calls := port.callCount(); calls != 0 {
		t.Fatalf("invalid pre-state resolved owner authority %d times", calls)
	}
}

func TestProductiveReconciliationReportsCommittedPostPublicationAuthorityFailure(
	t *testing.T,
) {
	store, blocked, port := newBlockedProductiveReconciliationFixture(t)
	ref := testSHARef("post-publication-reconciliation")
	port.register(testProductiveReconciliationAuthority(
		t,
		store,
		blocked,
		ref,
		WorkReconcileManualResolution,
	))
	port.failAfter = 1
	port.failErr = os.ErrPermission

	_, err := store.RecordProductiveReconciliation(
		context.Background(),
		RecordProductiveReconciliationRequest{
			ExpectedRevision:      blocked.Revision,
			RequestID:             "post-publication-reconciliation",
			OriginalDiagnosticRef: blocked.ProductiveBlockerRef,
			ReconciliationRef:     ref,
		},
	)
	var publication *PublicationError
	if !errors.As(err, &publication) ||
		!publication.Committed ||
		publication.Revision == "" {
		t.Fatalf("post-publication reconciliation error = %#v", err)
	}
	terminal, statusErr := store.Status()
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if terminal.Revision != publication.Revision ||
		terminal.ProductiveBlockerRef != blocked.ProductiveBlockerRef ||
		terminal.ProductiveReconciliationRef != ref ||
		terminal.ProductiveReconciliationSourceRevision != blocked.Revision ||
		terminal.ProductiveReconciliationOutcome !=
			WorkReconcileManualResolution ||
		terminal.DeliveryResultRef != "" {
		t.Fatalf(
			"committed reconciliation/status = %#v / %#v",
			terminal,
			publication,
		)
	}
}

func TestProductiveReconciliationAuthorityValidatesExactPreAndReplayState(
	t *testing.T,
) {
	store, blocked, _ :=
		newBlockedProductiveReconciliationFixtureForOutcome(
			t,
			WorkReconcileDeliveryConfirmed,
		)
	ref := testSHARef("reconciliation-authority-pre-post")
	authority := testProductiveReconciliationAuthority(
		t,
		store,
		blocked,
		ref,
		WorkReconcileDeliveryConfirmed,
	)
	if err := authority.Validate(ref, store.RepositoryRef(), blocked); err != nil {
		t.Fatalf("exact reconciliation pre-state rejected: %v", err)
	}

	replayed := blocked
	replayed.Revision = testSHARef("reconciliation-authority-post")
	replayed.ProductiveReconciliationRef = ref
	replayed.ProductiveReconciliationSourceRevision = blocked.Revision
	replayed.ProductiveReconciliationOutcome = authority.Outcome
	replayed.DeliveryResultRef = authority.DeliveryResultRef
	if err := authority.Validate(
		ref,
		store.RepositoryRef(),
		replayed,
	); err != nil {
		t.Fatalf("exact reconciliation post-state rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ProductiveReconciliationAuthority, *WorkRunState)
	}{
		{
			name: "another original diagnostic",
			mutate: func(
				authority *ProductiveReconciliationAuthority,
				_ *WorkRunState,
			) {
				authority.OriginalDiagnosticRef =
					testSHARef("another-original-diagnostic")
			},
		},
		{
			name: "another repository",
			mutate: func(
				authority *ProductiveReconciliationAuthority,
				_ *WorkRunState,
			) {
				authority.RepositoryRef =
					testSHARef("another-reconciliation-repository")
			},
		},
		{
			name: "another work run",
			mutate: func(
				authority *ProductiveReconciliationAuthority,
				_ *WorkRunState,
			) {
				authority.WorkRunID = "another-work-run"
			},
		},
		{
			name: "another source revision",
			mutate: func(
				authority *ProductiveReconciliationAuthority,
				_ *WorkRunState,
			) {
				authority.SourceRevision =
					testSHARef("another-reconciliation-source")
			},
		},
		{
			name: "another advance source revision",
			mutate: func(
				authority *ProductiveReconciliationAuthority,
				_ *WorkRunState,
			) {
				authority.AdvanceSourceRevision =
					testSHARef("another-advance-source")
			},
		},
		{
			name: "invalid historical advance reference",
			mutate: func(
				authority *ProductiveReconciliationAuthority,
				_ *WorkRunState,
			) {
				authority.HistoricalAdvanceRef = "not-a-sha"
			},
		},
		{
			name: "another stage",
			mutate: func(
				authority *ProductiveReconciliationAuthority,
				_ *WorkRunState,
			) {
				authority.DeliveryAuthorizationRef =
					testSHARef("another-reconciliation-stage")
			},
		},
		{
			name: "another replay outcome",
			mutate: func(
				_ *ProductiveReconciliationAuthority,
				state *WorkRunState,
			) {
				state.ProductiveReconciliationOutcome =
					WorkReconcileNoDeliveryConfirmed
			},
		},
		{
			name: "another replay result",
			mutate: func(
				_ *ProductiveReconciliationAuthority,
				state *WorkRunState,
			) {
				state.DeliveryResultRef =
					testSHARef("another-reconciliation-result")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidateAuthority := cloneProductiveReconciliationAuthority(
				authority,
			)
			candidateState := replayed
			test.mutate(&candidateAuthority, &candidateState)
			if err := candidateAuthority.Validate(
				ref,
				store.RepositoryRef(),
				candidateState,
			); !errors.Is(err, ErrAuthorityBindingMismatch) {
				t.Fatalf(
					"mismatched reconciliation validation = %v",
					err,
				)
			}
		})
	}
}

func TestProductiveReconciliationAuthorityEnforcesOutcomeEvidence(
	t *testing.T,
) {
	ref := testSHARef("reconciliation-outcome-evidence")
	tests := []struct {
		name    string
		outcome WorkReconcileOutcome
		mutate  func(*ProductiveReconciliationAuthority)
	}{
		{
			name:    "delivery confirmed without result",
			outcome: WorkReconcileDeliveryConfirmed,
			mutate: func(value *ProductiveReconciliationAuthority) {
				value.DeliveryResultRef = ""
			},
		},
		{
			name:    "delivery confirmed with diagnostic",
			outcome: WorkReconcileDeliveryConfirmed,
			mutate: func(value *ProductiveReconciliationAuthority) {
				value.Diagnostic = testProductiveReconciliationDiagnostic(
					t,
					WorkReconcileManualResolution,
				)
			},
		},
		{
			name:    "no delivery without diagnostic",
			outcome: WorkReconcileNoDeliveryConfirmed,
			mutate: func(value *ProductiveReconciliationAuthority) {
				value.Diagnostic = nil
			},
		},
		{
			name:    "no delivery with result",
			outcome: WorkReconcileNoDeliveryConfirmed,
			mutate: func(value *ProductiveReconciliationAuthority) {
				value.DeliveryResultRef =
					testSHARef("forbidden-no-delivery-result")
			},
		},
		{
			name:    "no delivery with manual diagnostic",
			outcome: WorkReconcileNoDeliveryConfirmed,
			mutate: func(value *ProductiveReconciliationAuthority) {
				value.Diagnostic = testProductiveReconciliationDiagnostic(
					t,
					WorkReconcileManualResolution,
				)
			},
		},
		{
			name:    "manual resolution with no diagnostic",
			outcome: WorkReconcileManualResolution,
			mutate: func(value *ProductiveReconciliationAuthority) {
				value.Diagnostic = nil
			},
		},
		{
			name:    "manual resolution with no-delivery diagnostic",
			outcome: WorkReconcileManualResolution,
			mutate: func(value *ProductiveReconciliationAuthority) {
				value.Diagnostic = testProductiveReconciliationDiagnostic(
					t,
					WorkReconcileNoDeliveryConfirmed,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcomeStore, outcomeBlocked, _ :=
				newBlockedProductiveReconciliationFixtureForOutcome(
					t,
					test.outcome,
				)
			authority := testProductiveReconciliationAuthority(
				t,
				outcomeStore,
				outcomeBlocked,
				ref,
				test.outcome,
			)
			test.mutate(&authority)
			if err := authority.Validate(
				ref,
				outcomeStore.RepositoryRef(),
				outcomeBlocked,
			); err == nil {
				t.Fatal("invalid reconciliation outcome evidence was accepted")
			}
		})
	}
}

func newBlockedProductiveReconciliationFixture(
	t *testing.T,
) (WorkRunStore, WorkRunState, *productiveReconciliationTestPort) {
	return newBlockedProductiveReconciliationFixtureWithExecution(t, "")
}

func newBlockedProductiveReconciliationFixtureForOutcome(
	t *testing.T,
	outcome WorkReconcileOutcome,
) (WorkRunStore, WorkRunState, *productiveReconciliationTestPort) {
	t.Helper()
	var executionOutcome deliveryadmission.ExecutionOutcome
	switch outcome {
	case WorkReconcileDeliveryConfirmed:
		executionOutcome = deliveryadmission.ExecutionSucceeded
	case WorkReconcileNoDeliveryConfirmed:
		// No consumed authorization is itself the authoritative no-delivery
		// case, so no terminal ExecutionResult exists to anchor.
	case WorkReconcileManualResolution:
		executionOutcome = deliveryadmission.ExecutionIndeterminate
	default:
		t.Fatalf("unsupported productive reconciliation outcome %q", outcome)
	}
	return newBlockedProductiveReconciliationFixtureWithExecution(
		t,
		executionOutcome,
	)
}

func newBlockedProductiveReconciliationFixtureWithExecution(
	t *testing.T,
	executionOutcome deliveryadmission.ExecutionOutcome,
) (WorkRunStore, WorkRunState, *productiveReconciliationTestPort) {
	t.Helper()
	fixture := newTerminalWorkRunFixture(
		t,
		reviewtransaction.VerificationAggregateNotRequired,
	)
	authorizationRef := testSHARef(
		"productive-reconciliation-authorization",
	)
	fixture.authority.authorizations[authorizationRef] =
		liveDeliveryAuthorization(
			fixture.state,
			authorizationRef,
			DeliveryAuthorizationNormal,
		)
	bound, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		BindDeliveryAuthorizationRequest{
			ExpectedRevision: fixture.state.Revision,
			RequestID:        "productive-reconciliation-authorization",
			AuthorizationRef: authorizationRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	bound, err = fixture.store.BeginProductiveAdvance(
		context.Background(),
		BeginProductiveAdvanceRequest{
			ExpectedRevision: bound.Revision,
			RequestID:        "productive-reconciliation-begin",
			SourceRevision:   bound.Revision,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if executionOutcome != "" {
		bound = anchorTestProductiveExecutionResult(
			t,
			fixture.store,
			bound,
			executionOutcome,
		)
	}

	diagnosticRef := testSHARef(
		"productive-reconciliation-original-diagnostic",
	)
	diagnosticAuthority := testProductiveDiagnosticAuthority(
		fixture.store,
		bound,
		diagnosticRef,
	)
	code := WorkAdvanceDiagnosticDeliveryOutcomeIndeterminate
	message, ok := WorkAdvanceDiagnosticMessage(code)
	if !ok {
		t.Fatal("productive reconciliation diagnostic code is unavailable")
	}
	diagnosticAuthority.Diagnostic = WorkAdvanceDiagnosticV1{
		Ref:        diagnosticRef,
		Code:       code,
		Message:    message,
		NextAction: WorkAdvanceNextActionReconcile,
	}
	fixture.authority.productiveDiagnostics[diagnosticRef] =
		diagnosticAuthority
	blocked, err := fixture.store.RecordProductiveBlocker(
		context.Background(),
		RecordProductiveBlockerRequest{
			ExpectedRevision: bound.Revision,
			RequestID:        "productive-reconciliation-blocker",
			DiagnosticRef:    diagnosticRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	port := newProductiveReconciliationTestPort()
	ports := fixture.store.authority
	ports.ProductiveReconciliation = port
	store := fixture.store.WithAuthorityPorts(ports)
	return store, blocked, port
}

func anchorTestProductiveExecutionResult(
	t *testing.T,
	store WorkRunStore,
	state WorkRunState,
	outcome deliveryadmission.ExecutionOutcome,
) WorkRunState {
	t.Helper()
	execution := deliveryadmission.ExecutionResult{
		Schema: deliveryadmission.ExecutionResultContractV1,
		CommandRef: testSHARef(
			"productive-execution-command-" + string(outcome),
		),
		AuthorizationRef: state.DeliveryAuthorizationRef,
		Route:            deliveryadmission.RoutePRWithIssue,
		Candidate: deliveryadmission.CandidateBinding{
			Ref:    "work-run:" + state.WorkRunID,
			Digest: state.Handoff.CandidateRef,
		},
		Outcome:     outcome,
		EvidenceRef: testSHARef("productive-execution-evidence-" + string(outcome)),
		CompletedAt: 1,
	}
	if outcome == deliveryadmission.ExecutionSucceeded {
		execution.DeliveryRef = "delivery:productive-reconciliation"
	}
	resultRef, err := productiveExecutionResultRef(execution)
	if err != nil {
		t.Fatal(err)
	}
	authority := ProductiveExecutionResultAuthority{
		ResultRef:                resultRef,
		RepositoryRef:            store.RepositoryRef(),
		WorkRunID:                state.WorkRunID,
		SourceRevision:           state.Revision,
		DeliveryIntentRef:        state.DeliveryIntentRef,
		Handoff:                  cloneHandoff(state.Handoff),
		VerificationResultRef:    state.VerificationResultRef,
		ReviewReceiptRef:         state.ReviewReceiptRef,
		DeliveryAuthorizationRef: state.DeliveryAuthorizationRef,
		CommandRef:               execution.CommandRef,
		Execution:                execution,
	}
	owner := testAuthorityForStore(t, store)
	owner.productiveExecutionResults[resultRef] = authority
	anchored, err := store.RecordProductiveExecutionResult(
		context.Background(),
		RecordProductiveExecutionResultRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "productive-execution-" + string(outcome),
			ResultRef:        resultRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return anchored
}

func testProductiveReconciliationAuthority(
	t *testing.T,
	store WorkRunStore,
	state WorkRunState,
	ref string,
	outcome WorkReconcileOutcome,
) ProductiveReconciliationAuthority {
	t.Helper()
	authority := ProductiveReconciliationAuthority{
		ReconciliationRef:     ref,
		RepositoryRef:         store.RepositoryRef(),
		WorkRunID:             state.WorkRunID,
		SourceRevision:        state.Revision,
		AdvanceSourceRevision: state.ProductiveAdvanceSourceRevision,
		OriginalDiagnosticRef: state.ProductiveBlockerRef,
		HistoricalAdvanceRef: testSHARef(
			"productive-historical-advance-" + string(outcome),
		),
		DeliveryIntentRef:            state.DeliveryIntentRef,
		Handoff:                      cloneHandoff(state.Handoff),
		VerificationResultRef:        state.VerificationResultRef,
		ReviewReceiptRef:             state.ReviewReceiptRef,
		DeliveryAuthorizationRef:     state.DeliveryAuthorizationRef,
		ProductiveExecutionResultRef: state.ProductiveExecutionResultRef,
		Outcome:                      outcome,
	}
	switch outcome {
	case WorkReconcileDeliveryConfirmed:
		authority.DeliveryResultRef = testSHARef(
			"productive-reconciliation-result-" + string(outcome),
		)
		owner := testAuthorityForStore(t, store)
		execution, ok := owner.productiveExecutionResults[state.ProductiveExecutionResultRef]
		if !ok {
			t.Fatal(
				"delivery-confirmed reconciliation lacks anchored execution authority",
			)
		}
		owner.deliveryResults[authority.DeliveryResultRef] =
			DeliveryResultAuthority{
				ResultRef:          authority.DeliveryResultRef,
				ExecutionResultRef: state.ProductiveExecutionResultRef,
				AuthorizationRef:   state.DeliveryAuthorizationRef,
				DeliveryIntentRef:  state.DeliveryIntentRef,
				CandidateRef:       state.Handoff.CandidateRef,
				ReviewReceiptRef:   state.ReviewReceiptRef,
				VerificationResultRef: state.
					VerificationResultRef,
				DeliveryRef: execution.Execution.DeliveryRef,
				CompletedAt: execution.Execution.CompletedAt,
			}
	case WorkReconcileNoDeliveryConfirmed,
		WorkReconcileManualResolution:
		authority.Diagnostic =
			testProductiveReconciliationDiagnostic(t, outcome)
	default:
		t.Fatalf("unsupported test reconciliation outcome %q", outcome)
	}
	return authority
}

func testProductiveReconciliationDiagnostic(
	t *testing.T,
	outcome WorkReconcileOutcome,
) *WorkAdvanceDiagnosticV1 {
	t.Helper()
	var code WorkAdvanceDiagnosticCode
	var nextAction WorkAdvanceDiagnosticNextAction
	switch outcome {
	case WorkReconcileNoDeliveryConfirmed:
		code = WorkAdvanceDiagnosticDeliveryNotCompleted
		nextAction = WorkAdvanceNextActionStartFresh
	case WorkReconcileManualResolution:
		code = WorkAdvanceDiagnosticManualResolutionRequired
		nextAction = WorkAdvanceNextActionManual
	default:
		t.Fatalf(
			"outcome %q does not produce a reconciliation diagnostic",
			outcome,
		)
	}
	message, ok := WorkAdvanceDiagnosticMessage(code)
	if !ok {
		t.Fatalf("reconciliation diagnostic code %q is unavailable", code)
	}
	return &WorkAdvanceDiagnosticV1{
		Ref: testSHARef(
			"productive-reconciliation-diagnostic-" + string(outcome),
		),
		Code:       code,
		Message:    message,
		NextAction: nextAction,
	}
}

func cloneProductiveReconciliationAuthority(
	authority ProductiveReconciliationAuthority,
) ProductiveReconciliationAuthority {
	authority.Handoff = cloneHandoff(authority.Handoff)
	if authority.Diagnostic != nil {
		diagnostic := *authority.Diagnostic
		authority.Diagnostic = &diagnostic
	}
	return authority
}

func assertProductiveReconciliationUnchanged(
	t *testing.T,
	store WorkRunStore,
	before WorkRunState,
) {
	t.Helper()
	after, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision ||
		after.ProductiveBlockerRef != before.ProductiveBlockerRef ||
		after.ProductiveReconciliationRef != "" ||
		after.ProductiveReconciliationSourceRevision != "" ||
		after.ProductiveReconciliationOutcome != "" ||
		after.DeliveryResultRef != "" {
		t.Fatalf(
			"rejected reconciliation mutated WorkRun:\nbefore=%#v\nafter=%#v",
			before,
			after,
		)
	}
}
