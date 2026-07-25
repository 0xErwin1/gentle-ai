package workprovider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type productiveNondeterministicReviewer struct {
	*productiveAdvanceTestConnector
	mu    sync.Mutex
	calls int
}

func (connector *productiveNondeterministicReviewer) ReviewCandidate(
	ctx context.Context,
	request ProductiveReviewRequest,
) (ProductiveReviewResult, error) {
	result, err := connector.productiveAdvanceTestConnector.ReviewCandidate(
		ctx,
		request,
	)
	if err != nil {
		return ProductiveReviewResult{}, err
	}
	connector.mu.Lock()
	defer connector.mu.Unlock()
	connector.calls++
	if connector.calls > 1 {
		result.Evidence = append(
			result.Evidence,
			"nondeterministic retry returned different reviewer bytes",
		)
	}
	return result, nil
}

func TestProductiveActiveReviewReusesCapturedResultAfterCrash(
	t *testing.T,
) {
	fixture := newProductiveActiveAdvanceTestFixture(
		t,
		"captured-reviewer-recovery",
	)
	reviewer := &productiveNondeterministicReviewer{
		productiveAdvanceTestConnector: fixture.connector,
	}
	fixture.runtime.connector = reviewer
	preCaptureProductiveReviewerResult(t, fixture, reviewer)
	_, reviewCalls := fixture.verificationCalls()
	if reviewCalls != 1 {
		t.Fatalf("pre-crash reviewer calls = %d", reviewCalls)
	}

	advance, err := fixture.runtime.AdvanceOutcome(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	semanticCalls, reviewCalls := fixture.verificationCalls()
	if advance.Status.PublicState != workrun.PublicStateReady ||
		advance.DeliveryResultRef == "" ||
		semanticCalls != 1 ||
		reviewCalls != 1 ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"recovered advance/calls = %#v / semantic:%d review:%d CAS:%d",
			advance,
			semanticCalls,
			reviewCalls,
			fixture.casCalls(),
		)
	}
}

func preCaptureProductiveReviewerResult(
	t *testing.T,
	fixture productiveAdvanceTestFixture,
	connector *productiveNondeterministicReviewer,
) {
	t.Helper()
	ctx := context.Background()
	factory, err := NewProductiveOwnerCoordinatorFactory(
		ctx,
		fixture.repo,
		model.AgentCodex,
		fixture.activation,
		connector,
	)
	if err != nil {
		t.Fatal(err)
	}
	advanceStore, err := existingProductiveAdvanceStore(
		factory.lease,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := advanceStore.startAuthority(ctx)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := factory.openForProductiveRecovery(
		ctx,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := resolveProductiveStartAuthority(
		ctx,
		coordinator,
		authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, candidate, blocker, err := observeProductiveCommittedCandidate(
		ctx,
		factory,
		authority,
	)
	if err != nil || blocker != "" {
		t.Fatalf("observe candidate = blocker %q, error %v", blocker, err)
	}
	preparation, err := prepareProductiveVerification(
		ctx,
		factory,
		candidate,
		admitted.Decision.PolicyRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	lineageID := productiveAdvanceLineage(
		factory.RepositoryRef(),
		fixture.workRunID,
	)
	state, err := reviewtransaction.NewCompactState(
		reviewtransaction.Start{
			LineageID:  lineageID,
			Mode:       reviewtransaction.ModeOrdinaryBounded,
			Generation: 1,
			Snapshot:   candidate,
			PolicyHash: preparation.Plan.PolicyHash,
			RiskLevel:  preparation.Assessment.Level,
			SelectedLenses: append(
				[]string(nil),
				preparation.Lenses...,
			),
			OriginalChangedLines: &preparation.Assessment.ChangedLines,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	start, err := reviewtransaction.StartCompactAuthority(
		ctx,
		fixture.repo,
		reviewtransaction.CompactStartRequest{
			State:           state,
			ExplicitLineage: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(
		ctx,
		fixture.repo,
		lineageID,
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := (reviewtransaction.SnapshotBuilder{
		Repo: fixture.repo,
	}).FrozenCandidateContext(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := reviewtransaction.NewArtifactSubject(
		start.Record.State,
		start.Record.Revision,
		frozen,
		start.Record.State.SelectedLenses[0],
		0,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewProductiveReviewRequest(subject, frozen)
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := connector.ReviewCandidate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(reviewed)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	native := reviewtransaction.LensResult{
		Lens:     subject.Lens,
		Findings: append([]reviewtransaction.Finding{}, reviewed.Findings...),
		Evidence: append([]string{}, reviewed.Evidence...),
	}
	causal, err := productiveCandidateCausalFindingIDs(
		ctx,
		fixture.repo,
		candidate,
		native,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CaptureAdmittedReviewerResult(
		ctx,
		reviewtransaction.CompactAdmittedReviewerResultRequest{
			ExpectedRevision:          start.Record.Revision,
			TargetIdentity:            candidate.Identity,
			FrozenContext:             frozen,
			ArtifactSubject:           subject,
			Inspection:                reviewed.Inspection,
			Result:                    native,
			CandidateCausalFindingIDs: causal,
			RawPayload:                raw,
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(
		store.Dir,
		reviewtransaction.CompactReviewerResultsDir,
		"00-"+subject.Lens+".json",
	)); err != nil {
		t.Fatal(err)
	}
}
