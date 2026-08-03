package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// Wave 5 fix cycle 2, CRITICAL-A (verify-report #10186, coordinator's
// "minimal capture primitive" decision, NOT a rebuilt v2-shaped admission
// pipeline): the exact verify repro is the RED shape below --
// `GENTLE_AI_RDD_NEW_LINEAGE=1 gentle-ai review start --lineage med1`
// (medium tier, consent granted) then `gentle-ai review finalize --lineage
// med1` fails with "new-lineage finalize requires captured results for
// every frozen selected lens before approving" because no production code
// ever set FinalizeAdvanceRequest.CapturedLensResults and `review
// capture-result` bound only the v2 compact store.

// startMediumTierNewLineage starts a v3 lineage whose content classifies as
// RiskMedium: an executable (non-documentation) file change is enough
// (risk_test.go's own "remaining executable change is medium" fixture:
// Stats{Additions: 1} on a .go path). Consent is granted through the plain
// (non-negotiated) one-time console path: authorizeReviewStart's own
// non-interactive branch (review_mode.go, "!console.Interactive") shows the
// "reviewed without asking" notice and proceeds without blocking -- the
// exact shape a non-interactive test process reaches with no --consent flag
// at all (--consent itself requires the negotiated --contract form, a
// separate surface this fixture deliberately does not use, matching the
// verify repro's own plain `review start` shape).
func startMediumTierNewLineage(t *testing.T, repo, lineage string) ReviewFacadeStartNewLineageResult {
	t.Helper()
	t.Setenv("GENTLE_AI_RDD_NEW_LINEAGE", "1")
	path := filepath.Join(repo, "lib", lineage+".go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package lib\n\nfunc Example() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RunReviewFacadeStart([]string{"--cwd", repo, "--lineage", lineage}, &out); err != nil {
		t.Fatalf("medium-tier new-lineage start: %v\n%s", err, out.String())
	}
	var started ReviewFacadeStartNewLineageResult
	decodeStrictReviewJSON(t, out.Bytes(), &started)
	if started.LineageID != lineage || started.State != reviewtransaction.NewLineageStateReviewing {
		t.Fatalf("medium-tier new-lineage start = %#v", started)
	}
	if started.RiskLevel != reviewtransaction.RiskMedium {
		t.Fatalf("fixture sanity check failed: tier = %q, want medium (the exact verify repro tier)", started.RiskLevel)
	}
	if len(started.SelectedLenses) == 0 {
		t.Fatalf("fixture sanity check failed: medium tier selected zero lenses, nothing to capture")
	}
	t.Setenv("GENTLE_AI_RDD_NEW_LINEAGE", "")
	return started
}

// captureNewLineageLensResults submits one strict reviewer result for every
// one of the lineage's frozen selected lenses through the real CLI
// `review capture-result`, deriving each lens's provider-owned subject hash
// via --preflight first (the exact discovery path an operator or reviewing
// agent uses) rather than reimplementing the derivation in the test.
func captureNewLineageLensResults(t *testing.T, repo, lineage string, lenses []string) {
	t.Helper()
	for order, lens := range lenses {
		var preflightOut bytes.Buffer
		if err := RunReviewCaptureResult([]string{
			"--cwd", repo, "--lineage", lineage, "--target", "unused-for-new-lineage",
			"--lens", lens, "--order", strconv.Itoa(order), "--preflight",
		}, &preflightOut); err != nil {
			t.Fatalf("capture-result preflight for lens %q: %v\n%s", lens, err, preflightOut.String())
		}
		var preflight ReviewFacadeCaptureResultNewLineageResult
		decodeStrictReviewJSON(t, preflightOut.Bytes(), &preflight)
		if preflight.SubjectHash == "" {
			t.Fatalf("capture-result preflight for lens %q returned no subject hash: %#v", lens, preflight)
		}

		inputPath := filepath.Join(t.TempDir(), lens+".json")
		payload := `{"subject_hash":"` + preflight.SubjectHash + `","findings":[],"evidence":["reviewed"]}`
		if err := os.WriteFile(inputPath, []byte(payload), 0o644); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := RunReviewCaptureResult([]string{
			"--cwd", repo, "--lineage", lineage, "--target", "unused-for-new-lineage",
			"--lens", lens, "--order", strconv.Itoa(order), "--input", inputPath,
		}, &out); err != nil {
			t.Fatalf("capture-result for lens %q: %v\n%s", lens, err, out.String())
		}
		var result ReviewFacadeCaptureResultNewLineageResult
		decodeStrictReviewJSON(t, out.Bytes(), &result)
		if result.SubjectHash != preflight.SubjectHash {
			t.Fatalf("capture-result for lens %q did not echo the preflight subject hash: got %q want %q", lens, result.SubjectHash, preflight.SubjectHash)
		}
	}
}

// TestReviewFacadeCaptureResultNewLineage_MediumTierFinalizeAllowsAllFiveGates
// is C-A's END STATE: the coordinator's exact repro (medium-tier v3, consent
// granted, capture via the real CLI, finalize -> approved) then all five
// gates allow. Before this fix, finalize refused with
// ErrFinalizeRequiresLensResults and there was no way to satisfy it.
func TestReviewFacadeCaptureResultNewLineage_MediumTierFinalizeAllowsAllFiveGates(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	const lineage = "medium-tier-capture-lineage"
	started := startMediumTierNewLineage(t, repo, lineage)

	captureNewLineageLensResults(t, repo, lineage, started.SelectedLenses)

	var finalizeOut bytes.Buffer
	if err := RunReviewFacadeFinalize([]string{"--cwd", repo, "--lineage", lineage, "--captured-results=true"}, &finalizeOut); err != nil {
		t.Fatalf("finalize after capturing every selected lens: %v\n%s", err, finalizeOut.String())
	}
	var finalized ReviewFacadeFinalizeNewLineageResult
	decodeStrictReviewJSON(t, finalizeOut.Bytes(), &finalized)
	if finalized.State != reviewtransaction.NewLineageStateApproved {
		t.Fatalf("finalize(captured) state = %q, want approved", finalized.State)
	}

	dir := t.TempDir()
	releaseArtifact := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	releaseArgs := []string{
		"--release-configuration", releaseArtifact("configuration.txt", "configuration\n"),
		"--release-generated", releaseArtifact("generated.txt", "generated\n"),
		"--release-provenance", releaseArtifact("provenance.txt", "provenance\n"),
		"--release-publication-boundary", releaseArtifact("publication-boundary.txt", "publication boundary\n"),
		"--release-evidence-freshness", releaseArtifact("evidence-freshness.txt", "evidence freshness\n"),
	}
	runReviewCLIGit(t, repo, "add", "-A")

	for _, gate := range []struct {
		kind  reviewtransaction.GateKind
		extra []string
	}{
		{kind: reviewtransaction.GatePostApply},
		{kind: reviewtransaction.GatePreCommit},
		{kind: reviewtransaction.GatePrePush},
		{kind: reviewtransaction.GatePrePR},
		{kind: reviewtransaction.GateRelease, extra: releaseArgs},
	} {
		t.Run(string(gate.kind), func(t *testing.T) {
			args := append([]string{"--cwd", repo, "--lineage", lineage, "--gate", string(gate.kind)}, gate.extra...)
			var out bytes.Buffer
			if err := RunReviewFacadeValidate(args, &out); err != nil {
				t.Fatalf("gate %q must allow the approved medium-tier v3 candidate: %v\n%s", gate.kind, err, out.String())
			}
			assertReviewGateResult(t, out.Bytes(), reviewtransaction.GateAllow)
		})
	}
}

// TestReviewFacadeCaptureResultNewLineage_TierLowZeroResultsStillWorks pins
// the non-regression: a tier-low v3 lineage (SelectedLenses is empty) still
// finalizes with zero captured results -- the C-A fix must not require a
// capture that a tier-low lineage never asked for.
func TestReviewFacadeCaptureResultNewLineage_TierLowZeroResultsStillWorks(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	const lineage = "tier-low-zero-results-lineage"
	startNewLineageForFinalizeTest(t, repo, lineage)

	var out bytes.Buffer
	if err := RunReviewFacadeFinalize([]string{"--cwd", repo, "--lineage", lineage, "--captured-results=true"}, &out); err != nil {
		t.Fatalf("tier-low finalize with --captured-results=true and zero results: %v\n%s", err, out.String())
	}
	var result ReviewFacadeFinalizeNewLineageResult
	decodeStrictReviewJSON(t, out.Bytes(), &result)
	if result.State != reviewtransaction.NewLineageStateApproved {
		t.Fatalf("tier-low finalize(captured-results, zero lenses) state = %q, want approved", result.State)
	}
}
