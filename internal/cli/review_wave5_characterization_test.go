package cli

// Wave 5 (Gate Cutover), Slice 1: characterization corpus. These tests pin
// the CURRENT observable behavior of two facade-level surfaces Wave 5 will
// remove or downgrade — the invalidation verb's approved-invalidation branch
// (design decision 2; review_facade.go's `review invalidate --gate` branch,
// which calls reviewtransaction.InvalidateApprovedCompactAuthority) and the
// candidate-decline gate authorization branch (design decision 6;
// runReviewFacadeValidate's ResolveCandidateDeclineForGate branch) — before
// either is deleted/downgraded in later slices (S7 and S6 respectively).
//
// Both surfaces already have coverage of their underlying reviewtransaction
// package functions (compact_approved_invalidation_test.go,
// candidate_decline_test.go), but neither had a test driving the actual CLI
// entry point / funnel branch end to end (verified: no existing test calls
// RunReviewInvalidate with --gate, and no existing test drives a declined
// candidate through RunReviewFacadeValidate). These tests close exactly that
// gap and are safety nets: a first-run pass is valid and expected — they pin
// behavior that already exists, not a bugfix.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// TestInvalidationVerbCharacterization_InvalidateApprovedCompactAuthority
// pins `review invalidate --gate`'s approved-invalidation branch
// (review_facade.go, RunReviewInvalidate): it takes the compact store's
// writer lock (via reviewtransaction.InvalidateApprovedCompactAuthority),
// rewrites persisted state to invalidated with bound InvalidationEvidence,
// and removes the receipt file — before Wave 5 Slice 7 deletes this branch
// and makes `invalidated` a fully derived verdict instead.
func TestInvalidationVerbCharacterization_InvalidateApprovedCompactAuthority(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	stubReviewConsole(t, false, "")
	lineage := "review-invalidation-verb-characterization"
	writeReviewStartCandidate(t, repo, "scripts/deploy.sh", "echo deploy\n", 0o644)
	finalizeApprovedFacadeReview(t, repo, lineage)

	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if before.State.State != reviewtransaction.StateApproved {
		t.Fatalf("fixture did not reach approved: %#v", before.State)
	}
	if _, err := os.Stat(store.ReceiptPath()); err != nil {
		t.Fatalf("approved fixture carries no receipt: %v", err)
	}

	// Introduce an out-of-scope addition so the native gate re-derives
	// invalidated (mirrors compact_approved_invalidation_test.go's
	// TestInvalidateApprovedCompactAuthorityPersistsBoundSemanticDenial at
	// the CLI/facade layer instead of calling the package function directly).
	writeReviewStartCandidate(t, repo, "outside.txt", "outside frozen scope\n", 0o644)

	var output bytes.Buffer
	if err := RunReviewInvalidate([]string{
		"--cwd", repo, "--lineage", lineage, "--expected-revision", before.Revision,
		"--gate", string(reviewtransaction.GatePostApply),
	}, &output); err != nil {
		t.Fatalf("review invalidate --gate: %v\n%s", err, output.String())
	}
	var result ReviewInvalidateResult
	decodeStrictReviewJSON(t, output.Bytes(), &result)
	if result.Operation != "review/invalidate" || result.LineageID != lineage ||
		result.State != reviewtransaction.StateInvalidated || result.StoreRevision == before.Revision {
		t.Fatalf("review invalidate --gate result = %#v", result)
	}

	after, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if after.State.State != reviewtransaction.StateInvalidated || after.Revision != result.StoreRevision {
		t.Fatalf("persisted state after review invalidate --gate = %#v", after)
	}
	if after.State.InvalidationEvidence == nil || after.State.InvalidationEvidence.Gate != reviewtransaction.GatePostApply {
		t.Fatalf("persisted invalidation evidence = %#v", after.State.InvalidationEvidence)
	}
	if _, err := os.Stat(store.ReceiptPath()); !os.IsNotExist(err) {
		t.Fatalf("review invalidate --gate did not remove the receipt: %v", err)
	}
}

// TestCandidateDeclineCharacterization_ResolveCandidateDeclineForGate pins
// the full facade round trip Wave 5 Slice 6 will delete: a relayed consent
// decline persists a CandidateDeclineAuthorization (RecordCandidateDecline,
// review_facade.go's `errReviewDeclinedForCandidate` branch), and a
// subsequent `review validate --gate pre-commit` for the identical staged
// candidate resolves it via ResolveCandidateDeclineForGate and reaches
// ordinary unmanaged delivery (emitCandidateDeclinedUnmanagedDelivery) —
// never review authority, never a receipt-like record. Existing coverage
// (review_consent_relay_test.go's TestRelayedConsentDeclineIsScopedToTheCandidate)
// only proves the decline record is written and scoped; it never drives a
// gate through the decline. This test closes that gap.
func TestCandidateDeclineCharacterization_ResolveCandidateDeclineForGate(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	stubReviewConsole(t, false, "")
	writeReviewStartCandidate(t, repo, "scripts/deploy.sh", "echo deploy\n", 0o644)

	relayArgs := boundNegotiatedStartArgs(t, []string{
		"start", "--contract", ReviewIntegrationContractV1, "--cwd", repo,
		"--lineage", "review-decline-characterization", "--consent", "relay",
	})
	question := decodeConsentQuestion(t, runConsentRelayStart(t, relayArgs).Bytes())
	declineArgs := invocationArgs(t, question.Choices[1].Invocation)
	declined := runConsentRelayStart(t, declineArgs)
	var declinedResult ReviewFacadeStartResult
	decodeStrictReviewJSON(t, declined.Bytes(), &declinedResult)
	if declinedResult.Action != "declined" {
		t.Fatalf("decline did not record: %#v", declinedResult)
	}

	// Turn the identical declined candidate into a supported pre-commit
	// target: ResolveCandidateDeclineForGate only matches pre-commit,
	// pre-push, and pre-pr (candidate_decline.go), never post-apply/release.
	runReviewCLIGit(t, repo, "add", "scripts/deploy.sh")

	var output bytes.Buffer
	err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}, &output)
	if err != nil {
		t.Fatalf("declined candidate validate: %v\n%s", err, output.String())
	}
	var result ReviewValidateResult
	decodeStrictReviewJSON(t, output.Bytes(), &result)
	if result.Result != reviewtransaction.GateInvalidated || result.Allowed ||
		result.Delivery != reviewtransaction.RDDDeliveryCandidateDeclinedUnmanaged {
		t.Fatalf("declined candidate gate verdict = %#v", result)
	}
	if result.Context.Denial == nil || result.Context.Denial.Stage != "candidate-decline" ||
		result.Context.Denial.Code != "exact_candidate" {
		t.Fatalf("declined candidate context = %#v", result.Context)
	}

	// The decline never created review authority for this lineage.
	if store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, "review-decline-characterization"); err == nil {
		if _, loadErr := store.Load(); !errors.Is(loadErr, os.ErrNotExist) {
			t.Fatalf("declined candidate gate evaluation persisted review authority: %v", loadErr)
		}
	}
}
