package workprovider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
			workrun.WorkAdvanceDiagnosticCandidateNotCommitted,
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

func TestProductiveAdvanceAttemptGateRejectsStaleCASBeforePublication(
	t *testing.T,
) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openProductiveAdvanceStore(
		ctx,
		lease,
		"work-advance-attempt-cas",
	)
	if err != nil {
		t.Fatal(err)
	}
	current := productiveAdvanceSHA256([]byte("attempt-current"))
	stale := productiveAdvanceSHA256([]byte("attempt-stale"))
	state := workrun.WorkRunState{
		WorkRunID: "work-advance-attempt-cas",
		Revision:  current,
		Started:   true,
	}
	if err := gateProductiveAdvanceAttempt(
		ctx,
		store,
		state,
		stale,
	); err == nil {
		t.Fatal("stale work-advance CAS was accepted")
	}
	if exists, err := store.attemptExists(ctx, stale); err != nil || exists {
		t.Fatalf("stale CAS attempt publication = %v, %v", exists, err)
	}
	if err := gateProductiveAdvanceAttempt(
		ctx,
		store,
		state,
		current,
	); err != nil {
		t.Fatal(err)
	}
	if exists, err := store.attemptExists(ctx, current); err != nil || !exists {
		t.Fatalf("exact CAS attempt publication = %v, %v", exists, err)
	}
	state.Revision = productiveAdvanceSHA256([]byte("attempt-progress"))
	if err := gateProductiveAdvanceAttempt(
		ctx,
		store,
		state,
		current,
	); err != nil {
		t.Fatalf("lost-response attempt marker did not resume: %v", err)
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
	owner := newOwnerCoordinatorFixture(t, "productive-result-provenance")
	baseRevision := "git:" + strings.TrimSpace(
		ownerGit(t, owner.repo, "rev-parse", "HEAD"),
	)
	admission := ownerAdmitBoundDelivery(
		t,
		owner,
		"productive-result-provenance",
		baseRevision,
		false,
	)
	store, err := openProductiveAdvanceStore(
		ctx,
		owner.coordinator.pad.authority.identity.lease,
		owner.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	store.pad = owner.coordinator.pad
	candidateRef := productiveAdvanceSHA256([]byte("productive-candidate"))
	state := workrun.WorkRunState{
		WorkRunID:                owner.workRunID,
		DeliveryIntentRef:        admission.Admission.IntentRef,
		Handoff:                  &workrun.ImplementationHandoff{CandidateRef: candidateRef},
		ReviewReceiptRef:         productiveAdvanceSHA256([]byte("productive-review")),
		VerificationResultRef:    productiveAdvanceSHA256([]byte("productive-verification")),
		DeliveryAuthorizationRef: productiveAdvanceSHA256([]byte("productive-authorization")),
	}
	execution := deliveryadmission.ExecutionResult{
		Schema:           deliveryadmission.ExecutionResultContractV1,
		CommandRef:       productiveAdvanceSHA256([]byte("productive-command")),
		AuthorizationRef: state.DeliveryAuthorizationRef,
		Route:            admission.Intent.Route,
		Candidate: deliveryadmission.CandidateBinding{
			Ref:    "work-run:" + owner.workRunID,
			Digest: candidateRef,
		},
		Outcome:     deliveryadmission.ExecutionSucceeded,
		DeliveryRef: baseRevision,
		EvidenceRef: productiveAdvanceSHA256([]byte("hosting-evidence")),
		CompletedAt: owner.now,
	}
	resultRef, err := store.publishDeliveryResult(ctx, state, execution)
	if err != nil {
		t.Fatal(err)
	}
	if resultRef == execution.EvidenceRef {
		t.Fatal("owner delivery result ref aliases external hosting evidence")
	}
	resolved, err := store.ResolveDeliveryResult(ctx, resultRef)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResultRef != resultRef ||
		resolved.DeliveryIntentRef != state.DeliveryIntentRef ||
		resolved.CandidateRef != candidateRef {
		t.Fatalf("resolved productive result = %#v", resolved)
	}
	forged := execution
	forged.Candidate.Ref = "candidate:copyable"
	if _, err := store.publishDeliveryResult(ctx, state, forged); err == nil {
		t.Fatal("delivery result accepted a non-WorkRun candidate ref")
	}
	forged = execution
	forged.Route = deliveryadmission.RouteDirectMain
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
	previousRevision := productiveAdvanceSHA256([]byte(workRunID + ":previous"))
	state := workrun.WorkRunState{
		Schema:              workrun.WorkRunStateSchemaV1,
		WorkRunID:           workRunID,
		Revision:            previousRevision,
		Started:             true,
		RouteDecision:       decision,
		ImplementationRoute: workrun.ImplementationRouteDirectInline,
		DeliveryIntentRef:   productiveAdvanceSHA256([]byte(workRunID + ":intent")),
	}
	diagnostic, err := store.publishDiagnostic(
		ctx,
		state,
		workrun.WorkAdvanceDiagnosticScopeMismatch,
	)
	if err != nil {
		t.Fatal(err)
	}
	state.ProductiveBlockerRef = diagnostic.Ref
	state.ProductiveBlockerSourceRevision = previousRevision
	state.Revision = productiveAdvanceSHA256([]byte(workRunID + ":terminal"))
	status := workrun.WorkStatusV1{
		Schema:              workrun.WorkStatusContractV1,
		Contract:            workrun.WorkStatusContractV1,
		WorkRunID:           workRunID,
		Revision:            state.Revision,
		PublicState:         workrun.PublicStateNeedsYourDecision,
		RouteDecision:       decision.Decision,
		ImplementationRoute: workrun.ImplementationRouteDirectInline,
		Verification: workrun.VerificationSummaryV1{
			Outcome:    workrun.VerificationPending,
			ResultRefs: []string{},
		},
		DeliveryIntentRef: state.DeliveryIntentRef,
	}
	result := workrun.WorkAdvanceV1{
		Schema:           workrun.WorkAdvanceContractV1,
		Contract:         workrun.WorkAdvanceContractV1,
		PreviousRevision: previousRevision,
		Status:           status,
		Diagnostic:       &diagnostic,
	}
	if err := store.publishResult(ctx, result); err != nil {
		t.Fatal(err)
	}
	replayed, ok, err := store.result(
		ctx,
		previousRevision,
		state,
		status,
	)
	if err != nil || !ok || replayed.Diagnostic == nil ||
		replayed.Diagnostic.Ref != diagnostic.Ref {
		t.Fatalf("valid response cache replay = %#v, %v, %v", replayed, ok, err)
	}
	return store, state, status, result
}
