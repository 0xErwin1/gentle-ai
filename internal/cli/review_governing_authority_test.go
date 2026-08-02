package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// TestResolveGoverningAuthorityAbsentWithoutMarkerCostsNoGitCall proves the
// cheap common case at the CLI wiring layer: with no explicit --lineage
// marker, resolveGoverningAuthority returns "legacy governs unchanged"
// without resolving a repository root or issuing any Git command — the
// switch-off-equivalence goldens (tasks 5.6-5.10) depend on this being true
// for the ordinary hook-invoked gate call shape.
func TestResolveGoverningAuthorityAbsentWithoutMarkerCostsNoGitCall(t *testing.T) {
	governs, evaluation, discoveryErr := resolveGoverningAuthority(context.Background(), "/does/not/exist", "", reviewtransaction.NativeGateRequestInput{Gate: reviewtransaction.GatePreCommit})
	if governs || discoveryErr != nil || evaluation != (reviewtransaction.NativeGateEvaluation{}) {
		t.Fatalf("resolveGoverningAuthority(no marker) = governs=%v evaluation=%#v discoveryErr=%v, want a pure no-op", governs, evaluation, discoveryErr)
	}
}

// TestReviewValidateDiscoveryIntegrityMarkerCorruptedDeniesNeverLegacy is
// task 5.2's MANDATORY RED test at the CLI integration layer (design
// decision 4's non-matrix discovery-integrity clause): a new-lineage marker
// (the v3/<lineage> directory) is present for the exact lineage id an
// otherwise-valid, otherwise-governing legacy receipt ALSO claims — but the
// v3 record itself was removed. The gate MUST deny and MUST NEVER fall
// through to the legacy receipt, even though that legacy receipt alone would
// have allowed this exact candidate.
func TestReviewValidateDiscoveryIntegrityMarkerCorruptedDeniesNeverLegacy(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	const lineageID = "shared-legacy-and-new-lineage"
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("candidate reviewed under legacy and named by a corrupted v3 marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Build a real, terminal, GOVERNING legacy receipt at this exact
	// lineage id: without the v3 marker below, this candidate would allow.
	finalizeApprovedFacadeReview(t, repo, lineageID)
	runReviewCLIGit(t, repo, "add", "tracked.txt")

	var legacyOnly bytes.Buffer
	if err := RunReviewFacadeValidate([]string{"--cwd", repo, "--lineage", lineageID, "--gate", string(reviewtransaction.GatePreCommit)}, &legacyOnly); err != nil {
		t.Fatalf("legacy-only baseline before adding the v3 marker must allow: %v\n%s", err, legacyOnly.String())
	}
	assertReviewGateResult(t, legacyOnly.Bytes(), reviewtransaction.GateAllow)

	// Now plant the new-lineage marker under the IDENTICAL lineage id and
	// corrupt it: the directory (the marker) exists, but its record does
	// not — task 5.2's exact shape ("new-lineage marker present, v3 record
	// removed, legacy receipt present").
	store, err := reviewtransaction.NewLineageAuthorityStore(context.Background(), repo, lineageID)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(runReviewCLIGit(t, repo, "rev-parse", "HEAD^{tree}"))
	if _, err := store.Mutate(context.Background(), "", func(next *reviewtransaction.NewLineageAuthority) error {
		next.State = reviewtransaction.NewLineageStateReviewing
		next.CandidateIdentity = reviewtransaction.CandidateIdentity{
			RepositoryID: "corrupted-marker-repo", BaseTree: head, CandidateTree: head,
			ChangedPathsModesDigest: "sha256:" + head, PolicyHash: "unknown",
		}
		next.Tier = reviewtransaction.RiskLow
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.StatePath()); err != nil {
		t.Fatal(err)
	}
	if info, statErr := os.Stat(store.Dir); statErr != nil || !info.IsDir() {
		t.Fatalf("v3 marker directory must survive removing only its record: stat=%v err=%v", info, statErr)
	}

	var output bytes.Buffer
	err = RunReviewFacadeValidate([]string{"--cwd", repo, "--lineage", lineageID, "--gate", string(reviewtransaction.GatePreCommit)}, &output)
	if err == nil {
		t.Fatalf("discovery-integrity corruption must deny, got allow:\n%s", output.String())
	}
	var denied ReviewGateDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("discovery-integrity corruption error = %T %v, want ReviewGateDeniedError", err, err)
	}
	if denied.Result == reviewtransaction.GateAllow {
		t.Fatalf("discovery-integrity corruption fell through to the legacy allow: %#v", denied)
	}
	// Task 5.3's decision: the discovery-integrity denial reuses the
	// existing ReviewAuthorityCorrupted constant — this assertion is that
	// decision's regression guard, not a silent default.
	if denied.Context.Denial == nil || denied.Context.Denial.Code != string(ReviewAuthorityCorrupted) {
		t.Fatalf("discovery-integrity corruption denial code = %#v, want %q", denied.Context.Denial, ReviewAuthorityCorrupted)
	}

	// Replay stability: the same denial, byte-identical, and no mutation of
	// either authority system.
	var replay bytes.Buffer
	replayErr := RunReviewFacadeValidate([]string{"--cwd", repo, "--lineage", lineageID, "--gate", string(reviewtransaction.GatePreCommit)}, &replay)
	if replayErr == nil || !bytes.Equal(replay.Bytes(), output.Bytes()) {
		t.Fatalf("discovery-integrity corruption denial is not replay-stable:\nfirst:\n%s\nreplay:\n%s", output.String(), replay.String())
	}
}
