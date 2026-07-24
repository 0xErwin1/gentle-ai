package workprovider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestProductiveAdvanceResultCacheRevalidatesTerminalAuthority(t *testing.T) {
	t.Run("tampered cached terminal ref", func(t *testing.T) {
		store, state, status, result := newProductiveDecisionCacheFixture(
			t,
			"work-advance-cache-tamper",
		)
		preBlock := state
		preBlock.Revision = state.ProductiveBlockerSourceRevision
		preBlock.ProductiveBlockerRef = ""
		preBlock.ProductiveBlockerSourceRevision = ""
		other, err := store.publishDiagnostic(
			context.Background(),
			preBlock,
			result.PreviousRevision,
			workrun.WorkAdvanceDiagnosticCandidateNotCommitted,
			workrun.WorkAdvanceNextActionStartFresh,
		)
		if err != nil {
			t.Fatal(err)
		}
		result.Diagnostic = &other
		payload, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		payload = append(payload, '\n')
		if err := os.WriteFile(
			filepath.Join(store.root, store.resultName(result.PreviousRevision)),
			payload,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.result(
			context.Background(),
			result.PreviousRevision,
			state,
			status,
		); err == nil {
			t.Fatal("tampered response cache bypassed terminal WorkRun binding")
		}
	})

	t.Run("missing diagnostic authority", func(t *testing.T) {
		store, state, status, result := newProductiveDecisionCacheFixture(
			t,
			"work-advance-cache-missing-diagnostic",
		)
		if err := os.Remove(filepath.Join(
			store.root,
			"diagnostic-"+
				productiveAdvanceRevisionKey(result.Diagnostic.Ref)+".json",
		)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.result(
			context.Background(),
			result.PreviousRevision,
			state,
			status,
		); err == nil {
			t.Fatal("response cache survived missing diagnostic authority")
		}
	})

	t.Run("hard-linked authority record", func(t *testing.T) {
		store, _, _, result := newProductiveDecisionCacheFixture(
			t,
			"work-advance-cache-hard-link",
		)
		path := filepath.Join(
			store.root,
			store.resultName(result.PreviousRevision),
		)
		alias := filepath.Join(store.root, "hard-link-alias.json")
		if err := os.Link(path, alias); err != nil {
			t.Skipf("platform does not support hard links: %v", err)
		}
		if _, err := readProductiveAuthorityFile(
			path,
			maximumProductiveAdvanceRecord,
		); err == nil {
			t.Fatal("hard-linked authority record was accepted")
		}
	})
}

func TestProductiveAdvanceAttemptGateIgnoresForgedExternalMarker(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newProductiveAdvanceTestFixture(
		t,
		"forged-attempt-marker",
		productiveAdvanceTestCASSucceed,
		false,
	)
	factory, err := NewProductiveOwnerCoordinatorFactory(
		ctx,
		fixture.repo,
		fixture.runtime.agent,
		fixture.activation,
		fixture.connector,
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := existingProductiveAdvanceStore(
		factory.lease,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	store.pad = factory.pad
	stale := productiveAdvanceSHA256([]byte("forged-stale-source"))
	// Simulate the retired marker format byte-for-byte. Its presence must be
	// irrelevant because only the WorkRun hash-chain event is authority.
	marker := struct {
		Schema           string `json:"schema"`
		RepositoryRef    string `json:"repositoryRef"`
		WorkRunID        string `json:"workRunId"`
		ExpectedRevision string `json:"expectedRevision"`
	}{
		Schema:           "gentle-ai.productive-advance-attempt/v1",
		RepositoryRef:    factory.lease.Identity().RepositoryRef,
		WorkRunID:        fixture.workRunID,
		ExpectedRevision: stale,
	}
	payload, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(
		filepath.Join(
			store.root,
			"attempt-"+productiveAdvanceRevisionKey(stale)+".json",
		),
		payload,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	coordinator, err := factory.Open(ctx, fixture.workRunID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recordsBefore, err := os.ReadDir(
		filepath.Join(coordinator.work.Dir, "records"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := advanceProductiveWork(
		ctx,
		factory,
		fixture.workRunID,
		stale,
	); err == nil {
		t.Fatal("forged external marker authorized a stale advance")
	}
	after, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recordsAfter, err := os.ReadDir(
		filepath.Join(coordinator.work.Dir, "records"),
	)
	if err != nil {
		t.Fatal(err)
	}
	recordsChanged := len(recordsAfter) != len(recordsBefore)
	if !recordsChanged {
		for index := range recordsBefore {
			if recordsAfter[index].Name() != recordsBefore[index].Name() {
				recordsChanged = true
				break
			}
		}
	}
	if after.Revision != before.Revision ||
		after.ProductiveAdvanceSourceRevision != "" ||
		recordsChanged ||
		fixture.casCalls() != 0 ||
		fixture.remoteRevision(t) != fixture.base {
		t.Fatalf(
			"forged marker mutated WorkRun/effect: before=%#v after=%#v CAS=%d",
			before,
			after,
			fixture.casCalls(),
		)
	}
}

func TestProductiveAdvanceRetryAfterBeginUsesPublicSourceCASAndController(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newProductiveAdvanceTestFixture(
		t,
		"begin-retry-controller",
		productiveAdvanceTestCASSucceed,
		false,
	)
	factory, err := NewProductiveOwnerCoordinatorFactory(
		ctx,
		fixture.repo,
		fixture.runtime.agent,
		fixture.activation,
		fixture.connector,
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := factory.Open(ctx, fixture.workRunID)
	if err != nil {
		t.Fatal(err)
	}
	begun, err := coordinator.beginProductiveAdvance(
		ctx,
		fixture.start.Revision,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if begun.Revision == fixture.start.Revision ||
		begun.ProductiveAdvanceSourceRevision != fixture.start.Revision {
		t.Fatalf("begun WorkRun = %#v", begun)
	}
	public, err := coordinator.work.PublicStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if public.Revision != fixture.start.Revision {
		t.Fatalf(
			"public retry CAS = %q, want source %q",
			public.Revision,
			fixture.start.Revision,
		)
	}

	opener := &stubRuntimeOutcomeOpener{runtime: fixture.runtime}
	result, err := NewRuntimeController(opener).Advance(
		ctx,
		RuntimeAdvanceRequest{
			Repo:             fixture.repo,
			WorkRunID:        fixture.workRunID,
			ExpectedRevision: public.Revision,
			Contract:         workrun.WorkAdvanceContractV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Advance == nil ||
		result.Advance.PreviousRevision != fixture.start.Revision ||
		result.Advance.Status.Revision == fixture.start.Revision ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"controller resume/result = %#v; CAS %d",
			result,
			fixture.casCalls(),
		)
	}
	current, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.ProductiveAdvanceSourceRevision != fixture.start.Revision {
		t.Fatalf("terminal WorkRun lost productive source: %#v", current)
	}
}

func TestProductiveAdvanceUnknownWorkRunDoesNotCreateJournal(t *testing.T) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	factory, err := NewProductionOwnerCoordinatorFactory(
		ctx,
		repo,
		model.AgentCodex,
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	workRunID := "work-advance-unknown"
	root := productiveAdvanceStoreRoot(factory.lease, workRunID)
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("unknown WorkRun journal exists before advance: %v", err)
	}
	if _, err := advanceProductiveWork(
		ctx,
		factory,
		workRunID,
		productiveAdvanceSHA256([]byte("unknown-revision")),
	); err == nil {
		t.Fatal("unknown WorkRun advance unexpectedly succeeded")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("unknown WorkRun advance created journal: %v", err)
	}
}

func TestProductiveDeliveryResultBindsOwnerWorkRunCandidateAndIntent(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newProductiveAdvanceTestFixture(
		t,
		"productive-result-provenance",
		productiveAdvanceTestCASSucceed,
		false,
	)
	advance, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if advance.Status.PublicState != workrun.PublicStateReady ||
		advance.DeliveryResultRef == "" {
		t.Fatalf("productive delivery did not reach Ready: %#v", advance)
	}
	store, factory := fixture.existingStore(t)
	coordinator, err := factory.openForProductiveRecovery(
		ctx,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.ProductiveExecutionResultRef == "" ||
		state.DeliveryResultRef != advance.DeliveryResultRef {
		t.Fatalf("productive WorkRun lacks exact execution anchor: %#v", state)
	}
	executionAuthority, err := store.ResolveProductiveExecutionResult(
		ctx,
		state.ProductiveExecutionResultRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolveDeliveryResult(ctx, state.DeliveryResultRef)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResultRef != state.DeliveryResultRef ||
		resolved.ExecutionResultRef != state.ProductiveExecutionResultRef ||
		resolved.DeliveryIntentRef != state.DeliveryIntentRef ||
		resolved.CandidateRef != state.Handoff.CandidateRef {
		t.Fatalf("resolved productive result = %#v", resolved)
	}
	if resolved.ResultRef == executionAuthority.Execution.EvidenceRef {
		t.Fatal("owner delivery result ref aliases external hosting evidence")
	}
	forged := executionAuthority.Execution
	forged.Candidate.Ref = "candidate:copyable"
	if _, err := store.publishDeliveryResult(ctx, state, forged); err == nil {
		t.Fatal("delivery result accepted a non-WorkRun candidate ref")
	}
	forged = executionAuthority.Execution
	if forged.Route == deliveryadmission.RouteDirectMain {
		forged.Route = deliveryadmission.RoutePRWithoutIssue
	} else {
		forged.Route = deliveryadmission.RouteDirectMain
	}
	if _, err := store.publishDeliveryResult(ctx, state, forged); err == nil {
		t.Fatal("delivery result accepted a route outside the admitted intent")
	}
}

func newProductiveDecisionCacheFixture(
	t *testing.T,
	workRunID string,
) (
	productiveAdvanceStore,
	workrun.WorkRunState,
	workrun.WorkStatusV1,
	workrun.WorkAdvanceV1,
) {
	t.Helper()
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	padAuthority, err := newPADRepositoryAuthorityWithLease(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	pad, err := NewPADWorkRunAdapter(padAuthority)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openProductiveAdvanceStore(ctx, lease, workRunID)
	if err != nil {
		t.Fatal(err)
	}
	store.pad = pad
	decision, err := workrun.DecideImplementationRoute(
		workrun.ImplementationRouteInput{
			WriteIntent:    workrun.WriteIntentAtomicMechanical,
			WriteFileCount: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	advanceExpectedRevision := productiveAdvanceSHA256(
		[]byte(workRunID + ":advance-source"),
	)
	blockerSourceRevision := productiveAdvanceSHA256(
		[]byte(workRunID + ":blocker-source"),
	)
	state := workrun.WorkRunState{
		Schema:                          workrun.WorkRunStateSchemaV1,
		WorkRunID:                       workRunID,
		Revision:                        blockerSourceRevision,
		Started:                         true,
		RouteDecision:                   decision,
		ImplementationRoute:             workrun.ImplementationRouteDirectInline,
		DeliveryIntentRef:               productiveAdvanceSHA256([]byte(workRunID + ":intent")),
		ProductiveAdvanceSourceRevision: advanceExpectedRevision,
	}
	diagnostic, err := store.publishDiagnostic(
		ctx,
		state,
		advanceExpectedRevision,
		workrun.WorkAdvanceDiagnosticScopeMismatch,
		workrun.WorkAdvanceNextActionStartFresh,
	)
	if err != nil {
		t.Fatal(err)
	}
	state.ProductiveBlockerRef = diagnostic.Ref
	state.ProductiveBlockerSourceRevision = blockerSourceRevision
	state.Revision = productiveAdvanceSHA256([]byte(workRunID + ":terminal"))
	status := workrun.WorkStatusV1{
		Schema:              workrun.WorkStatusContractV1,
		Contract:            workrun.WorkStatusContractV1,
		WorkRunID:           workRunID,
		Revision:            state.Revision,
		PublicState:         workrun.PublicStateNeedsYourDecision,
		RouteDecision:       decision.Decision,
		RoutePhase:          workrun.RoutePhaseImplementationSelected,
		ImplementationRoute: workrun.ImplementationRouteDirectInline,
		Verification: workrun.VerificationSummaryV1{
			Outcome:    workrun.VerificationPending,
			ResultRefs: []string{},
		},
		DeliveryIntentRef: state.DeliveryIntentRef,
		Diagnostic:        &diagnostic,
	}
	result := workrun.WorkAdvanceV1{
		Schema:           workrun.WorkAdvanceContractV1,
		Contract:         workrun.WorkAdvanceContractV1,
		PreviousRevision: advanceExpectedRevision,
		Status:           status,
		Diagnostic:       &diagnostic,
	}
	if err := store.publishResult(ctx, result); err != nil {
		t.Fatal(err)
	}
	replayed, ok, err := store.result(
		ctx,
		advanceExpectedRevision,
		state,
		status,
	)
	if err != nil || !ok || replayed.Diagnostic == nil ||
		replayed.Diagnostic.Ref != diagnostic.Ref {
		t.Fatalf("valid response cache replay = %#v, %v, %v", replayed, ok, err)
	}
	return store, state, status, result
}
