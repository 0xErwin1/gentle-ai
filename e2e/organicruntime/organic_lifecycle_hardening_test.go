//go:build legacy_compact_receipt

// This file carries the review-lifecycle-hardening batch's per-issue,
// end-to-end traceability: one named subtest per defect, grouped into the
// seven mechanism journeys the design lays out. It is kept separate from
// organic_runtime_test.go so that file stays reviewable on its own.
package organicruntime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// currentChangesTargetIdentity derives the exact frozen target identity for
// the live workspace the same way a real negotiated caller must: by building
// the snapshot through the shipped SnapshotBuilder before naming --target.
func currentChangesTargetIdentity(t *testing.T, repo string) string {
	t.Helper()
	builder := reviewtransaction.SnapshotBuilder{Repo: repo}
	intended, err := builder.DiscoverUnignoredUntracked(context.Background())
	if err != nil {
		t.Fatalf("discover intended untracked candidate: %v", err)
	}
	snapshot, err := builder.Build(context.Background(), reviewtransaction.Target{
		Kind: reviewtransaction.TargetCurrentChanges, Projection: reviewtransaction.ProjectionWorkspace, IntendedUntracked: intended,
	})
	if err != nil {
		t.Fatalf("build candidate snapshot: %v", err)
	}
	return snapshot.Identity
}

// TestOrganicReviewLifecycleErrorTyping proves Group A: candidate-causal
// admission canonicalizes both sides before comparing (1699), and the
// operation_outcome_unknown envelope carries its wrapped native cause instead
// of a fixed placeholder (1666, 1807).
func TestOrganicReviewLifecycleErrorTyping(t *testing.T) {
	t.Run("issue-1699", func(t *testing.T) {
		harness := newOrganicHarness(t)
		lineage := "organic-candidate-causal-canonicalization"
		harness.writeFiles(map[string]string{"tracked.txt": organicLines("candidate causal proof line", 6)})
		started, _ := harness.startReview(lineage)
		if len(started.SelectedLenses) == 0 || started.TargetIdentity == "" {
			t.Fatalf("expected at least one selected lens and a target identity: %#v", started)
		}
		lens := started.SelectedLenses[0]
		prefix, known := map[string]string{
			"review-risk": "R1-", "review-readability": "R2-", "review-reliability": "R3-", "review-resilience": "R4-",
		}[lens]
		if !known {
			t.Fatalf("unrecognized selected lens %q", lens)
		}

		preflightPayload := harness.gentle(
			"review", "capture-result", "--cwd", harness.repo.worktree,
			"--lineage", lineage, "--target", started.TargetIdentity,
			"--lens", lens, "--order", "0", "--preflight",
		)
		var preflight struct {
			ArtifactSubject struct {
				SubjectHash string `json:"subject_hash"`
			} `json:"artifact_subject"`
		}
		if err := json.Unmarshal(preflightPayload, &preflight); err != nil {
			t.Fatalf("decode capture-result preflight: %v\n%s", err, preflightPayload)
		}
		if preflight.ArtifactSubject.SubjectHash == "" {
			t.Fatalf("capture-result preflight omitted the artifact subject hash: %s", preflightPayload)
		}

		// The submitted candidate-causal finding ID carries leading/trailing
		// whitespace an agent transport could plausibly introduce. Before the
		// fix, AdmitArtifact compared the canonicalized verified IDs against
		// this raw, non-canonical submission and rejected it out_of_scope even
		// though it names the same proven finding.
		payload := struct {
			SubjectHash string                   `json:"subject_hash"`
			Inspection  organicCaptureInspection `json:"inspection"`
			Findings    []organicFinding         `json:"findings"`
			Evidence    []string                 `json:"evidence"`
		}{
			SubjectHash: preflight.ArtifactSubject.SubjectHash,
			Inspection:  organicCaptureInspection{Status: "completed", Paths: []string{"tracked.txt"}},
			Findings: []organicFinding{{
				ID: "  " + prefix + "001  ", Location: "tracked.txt:1", Severity: "BLOCKER",
				Claim:             "the candidate introduces an unreviewed causal defect",
				ProofRefs:         []string{"diff: tracked.txt:1"},
				EvidenceClass:     "deterministic",
				CausalDisposition: "introduced",
			}},
			Evidence: []string{"inspected every frozen candidate path for " + lens},
		}
		resultPath := harness.writeJSON("candidate-causal-result.json", payload)

		captured := harness.gentle(
			"review", "capture-result", "--cwd", harness.repo.worktree,
			"--lineage", lineage, "--target", started.TargetIdentity,
			"--lens", lens, "--order", "0", "--input", resultPath,
		)
		var artifact struct {
			AdmissionDecision string `json:"admission_decision"`
			Path              string `json:"path"`
		}
		if err := json.Unmarshal(captured, &artifact); err != nil {
			t.Fatalf("decode capture-result: %v\n%s", err, captured)
		}
		if artifact.AdmissionDecision != "completed" {
			t.Fatalf("candidate-causal admission = %q, want completed (non-canonical id must still admit)", artifact.AdmissionDecision)
		}
		envelope, err := os.ReadFile(artifact.Path)
		if err != nil {
			t.Fatalf("read captured reviewer artifact: %v", err)
		}
		var stored struct {
			Admission struct {
				CandidateCausalFindingIDs []string `json:"candidate_causal_finding_ids"`
			} `json:"admission"`
		}
		if err := json.Unmarshal(envelope, &stored); err != nil {
			t.Fatalf("decode stored reviewer artifact: %v\n%s", err, envelope)
		}
		wantID := prefix + "001"
		if len(stored.Admission.CandidateCausalFindingIDs) != 1 || stored.Admission.CandidateCausalFindingIDs[0] != wantID {
			t.Fatalf("admitted candidate-causal finding ids = %v, want canonical [%s]", stored.Admission.CandidateCausalFindingIDs, wantID)
		}
	})

	t.Run("issue-1699-id-less-candidate-causal-finding", func(t *testing.T) {
		// A community reviewer (ftorga) proved the Group A canonicalization fix
		// above did not cover the reachable shape: a severe candidate-causal
		// finding submitted with NO "id" at all (not merely a non-canonical
		// one). verifiedCandidateCausalFindingIDs was reading the raw,
		// pre-canonicalization result, so the omitted ID could never match the
		// canonical fallback ID (`R#-001`) CanonicalCompactLensResult assigns
		// inside admission, permanently producing out-of-scope/incomplete.
		harness := newOrganicHarness(t)
		lineage := "organic-candidate-causal-id-less"
		harness.writeFiles(map[string]string{"tracked.txt": organicLines("candidate causal proof line", 6)})
		started, _ := harness.startReview(lineage)
		if len(started.SelectedLenses) == 0 || started.TargetIdentity == "" {
			t.Fatalf("expected at least one selected lens and a target identity: %#v", started)
		}
		lens := started.SelectedLenses[0]
		prefix, known := map[string]string{
			"review-risk": "R1-", "review-readability": "R2-", "review-reliability": "R3-", "review-resilience": "R4-",
		}[lens]
		if !known {
			t.Fatalf("unrecognized selected lens %q", lens)
		}

		preflightPayload := harness.gentle(
			"review", "capture-result", "--cwd", harness.repo.worktree,
			"--lineage", lineage, "--target", started.TargetIdentity,
			"--lens", lens, "--order", "0", "--preflight",
		)
		var preflight struct {
			ArtifactSubject struct {
				SubjectHash string `json:"subject_hash"`
			} `json:"artifact_subject"`
		}
		if err := json.Unmarshal(preflightPayload, &preflight); err != nil {
			t.Fatalf("decode capture-result preflight: %v\n%s", err, preflightPayload)
		}

		payload := struct {
			SubjectHash string                   `json:"subject_hash"`
			Inspection  organicCaptureInspection `json:"inspection"`
			Findings    []organicFinding         `json:"findings"`
			Evidence    []string                 `json:"evidence"`
		}{
			SubjectHash: preflight.ArtifactSubject.SubjectHash,
			Inspection:  organicCaptureInspection{Status: "completed", Paths: []string{"tracked.txt"}},
			Findings: []organicFinding{{
				// ID intentionally omitted entirely (json:"id,omitempty").
				Location: "tracked.txt:1", Severity: "BLOCKER",
				Claim:             "the candidate introduces an unreviewed causal defect",
				ProofRefs:         []string{"diff: tracked.txt:1"},
				EvidenceClass:     "deterministic",
				CausalDisposition: "introduced",
			}},
			Evidence: []string{"inspected every frozen candidate path for " + lens},
		}
		resultPath := harness.writeJSON("candidate-causal-id-less-result.json", payload)

		captured := harness.gentle(
			"review", "capture-result", "--cwd", harness.repo.worktree,
			"--lineage", lineage, "--target", started.TargetIdentity,
			"--lens", lens, "--order", "0", "--input", resultPath,
		)
		var artifact struct {
			AdmissionDecision string `json:"admission_decision"`
			Path              string `json:"path"`
		}
		if err := json.Unmarshal(captured, &artifact); err != nil {
			t.Fatalf("decode capture-result: %v\n%s", err, captured)
		}
		if artifact.AdmissionDecision != "completed" {
			t.Fatalf("id-less candidate-causal admission = %q, want completed", artifact.AdmissionDecision)
		}
		envelope, err := os.ReadFile(artifact.Path)
		if err != nil {
			t.Fatalf("read captured reviewer artifact: %v", err)
		}
		var stored struct {
			Admission struct {
				CandidateCausalFindingIDs []string `json:"candidate_causal_finding_ids"`
			} `json:"admission"`
		}
		if err := json.Unmarshal(envelope, &stored); err != nil {
			t.Fatalf("decode stored reviewer artifact: %v\n%s", err, envelope)
		}
		wantID := prefix + "001"
		if len(stored.Admission.CandidateCausalFindingIDs) != 1 || stored.Admission.CandidateCausalFindingIDs[0] != wantID {
			t.Fatalf("admitted candidate-causal finding ids = %v, want canonical fallback [%s]", stored.Admission.CandidateCausalFindingIDs, wantID)
		}
	})

	t.Run("issue-1666", func(t *testing.T) {
		harness := newOrganicHarness(t)
		lineage := "organic-operation-outcome-cause-1666"
		harness.writeFiles(map[string]string{"tracked.txt": "cause envelope candidate\n"})
		targetIdentity := currentChangesTargetIdentity(t, harness.repo.worktree)
		missingPolicy := filepath.Join(harness.repo.worktree, "missing-policy.json")

		stdout, stderr, err := harness.gentleAllowFailure(
			"review", "start", "--cwd", harness.repo.worktree,
			"--lineage", lineage,
			"--contract", "gentle-ai.review-integration/v1",
			"--target", targetIdentity,
			"--projection", "workspace",
			"--policy", missingPolicy,
		)
		if err == nil {
			t.Fatalf("negotiated start with an unreadable policy file succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		failure := decodeOrganicIntegrationFailure(t, stdout)
		if failure.Code != "invalid_request" || failure.Phase != "preflight" || failure.MutationOutcome != "not_started" {
			t.Fatalf("policy preflight failure typing = %#v, want invalid_request/preflight/not_started", failure)
		}
		if !strings.Contains(failure.Cause, "read facade review policy") {
			t.Fatalf("policy preflight failure did not carry the wrapped native cause: %#v", failure)
		}
		if _, statErr := os.Stat(filepath.Join(harness.commonDir(), "gentle-ai", "defect-reports")); !os.IsNotExist(statErr) {
			t.Fatalf("deterministic policy preflight failure created a defect report: %v", statErr)
		}
	})

	t.Run("issue-1832", func(t *testing.T) {
		// A disposable repository with no remote and no branch upstream: no
		// publication boundary to derive at all. That is not authority
		// damage, and while receipt-driven development is off it is not
		// something pre-push should block on.
		harness := newOrganicHarness(t)
		harness.git("remote", "remove", "origin")
		lineage := "organic-no-upstream-disabled-1832"
		harness.writeFiles(map[string]string{"tracked.txt": "reviewed candidate behavior\n"})
		started, _ := harness.startReview(lineage)
		approved := harness.approveReview(lineage, started)
		if approved.State != organicStateApproved {
			t.Fatalf("no-upstream fixture did not reach approved: %#v", approved)
		}
		harness.git("add", "-A")
		harness.git("commit", "-qm", "reviewed candidate")

		harness.disableReview()

		result := harness.gate(string(reviewtransaction.GatePrePush))
		if result.Delivery != "disabled/unmanaged" {
			t.Fatalf("disabled pre-push with no upstream gate = %#v, want delivery disabled/unmanaged", result)
		}
		if result.Allowed || result.Result == string(reviewtransaction.GateAllow) {
			t.Fatalf("disabled pre-push with no upstream fabricated an approval: %#v", result)
		}
		// Wave 5 Slice 2 (design decision 4): the switch is consulted before
		// any authority read, so the no-upstream boundary is never even
		// derived while disabled -- no discovery-kind detail leaks.
		if result.Context.Denial != nil {
			t.Fatalf("disabled pre-push with no upstream leaked discovery-kind detail: %#v", result.Context.Denial)
		}
	})

	t.Run("issue-1807", func(t *testing.T) {
		harness := newOrganicHarness(t)
		lineage := "organic-operation-outcome-cause-1807"
		harness.writeFiles(map[string]string{"tracked.txt": "cause envelope candidate two\n"})
		targetIdentity := currentChangesTargetIdentity(t, harness.repo.worktree)
		// A directory where the policy file is expected is a different concrete
		// native failure than a missing file (issue-1666), reached through the
		// identical unwrapped facadePolicyBytes seam: os.ReadFile on a directory
		// returns "is a directory" instead of "no such file".
		directoryAsPolicy := harness.repo.worktree

		stdout, stderr, err := harness.gentleAllowFailure(
			"review", "start", "--cwd", harness.repo.worktree,
			"--lineage", lineage,
			"--contract", "gentle-ai.review-integration/v1",
			"--target", targetIdentity,
			"--projection", "workspace",
			"--policy", directoryAsPolicy,
		)
		if err == nil {
			t.Fatalf("negotiated start with a directory as the policy file succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		failure := decodeOrganicIntegrationFailure(t, stdout)
		if failure.Code != "invalid_request" || failure.Phase != "preflight" || failure.MutationOutcome != "not_started" {
			t.Fatalf("policy preflight failure typing = %#v, want invalid_request/preflight/not_started", failure)
		}
		if !strings.Contains(failure.Cause, "read facade review policy") {
			t.Fatalf("policy preflight failure did not carry the wrapped native cause: %#v", failure)
		}
		if _, statErr := os.Stat(filepath.Join(harness.commonDir(), "gentle-ai", "defect-reports")); !os.IsNotExist(statErr) {
			t.Fatalf("deterministic policy preflight failure created a defect report: %v", statErr)
		}
	})
}

type organicCaptureInspection struct {
	Status string   `json:"status"`
	Paths  []string `json:"paths"`
}

type organicIntegrationFailure struct {
	Code            string `json:"code"`
	Phase           string `json:"phase"`
	MutationOutcome string `json:"mutation_outcome"`
	Cause           string `json:"cause"`
}

func decodeOrganicIntegrationFailure(t *testing.T, payload string) organicIntegrationFailure {
	t.Helper()
	var failure organicIntegrationFailure
	if err := json.Unmarshal([]byte(payload), &failure); err != nil {
		t.Fatalf("decode negotiated review failure: %v\n%s", err, payload)
	}
	return failure
}

type organicNextTransitionArgument struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Token string `json:"token"`
}

type organicNextTransitionExecute struct {
	Operation string                          `json:"operation"`
	Arguments []organicNextTransitionArgument `json:"arguments"`
}

type organicNextTransition struct {
	Kind       string                        `json:"kind"`
	ReasonCode string                        `json:"reason_code"`
	Execute    *organicNextTransitionExecute `json:"execute"`
}

type organicStatusAuthority struct {
	Revision string `json:"revision"`
}

type organicStatusResult struct {
	TargetIdentity string                 `json:"target_identity"`
	Authority      organicStatusAuthority `json:"authority"`
	NextTransition *organicNextTransition `json:"next_transition"`
}

// harnessCaptureResultSubjectHash preflights one capture-result binding and
// returns the artifact subject hash the real payload must echo.
func harnessCaptureResultSubjectHash(t *testing.T, harness *organicHarness, lineage, target, lens string, order int) string {
	t.Helper()
	payload := harness.gentle(
		"review", "capture-result", "--cwd", harness.repo.worktree,
		"--lineage", lineage, "--target", target, "--lens", lens, "--order", fmt.Sprintf("%d", order), "--preflight",
	)
	var preflight struct {
		ArtifactSubject struct {
			SubjectHash string `json:"subject_hash"`
		} `json:"artifact_subject"`
	}
	if err := json.Unmarshal(payload, &preflight); err != nil {
		t.Fatalf("decode capture-result preflight: %v\n%s", err, payload)
	}
	if preflight.ArtifactSubject.SubjectHash == "" {
		t.Fatalf("capture-result preflight omitted the artifact subject hash: %s", payload)
	}
	return preflight.ArtifactSubject.SubjectHash
}

func harnessStatus(t *testing.T, harness *organicHarness, lineage string, extra ...string) organicStatusResult {
	t.Helper()
	arguments := []string{"review", "status", "--cwd", harness.repo.worktree, "--lineage", lineage, "--contract", "gentle-ai.review-integration/v1"}
	arguments = append(arguments, extra...)
	payload := harness.gentle(arguments...)
	var status organicStatusResult
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatalf("decode review status: %v\n%s", err, payload)
	}
	return status
}

// initOrganicUnbornRepository builds a fresh worktree whose HEAD has never
// been committed, without the seeded commit and remote push newOrganicHarness
// always performs. Group G's 1771 and 1641 subtests need a genuinely unborn
// HEAD, which the shared harness fixture cannot represent.
func initOrganicUnbornRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, arguments := range [][]string{
		{"init", "--quiet", "--initial-branch=main", "."},
		{"config", "user.name", "Organic E2E"},
		{"config", "user.email", "organic-e2e@example.invalid"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := organicGitOutput(context.Background(), repo, arguments...); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// TestOrganicReviewTargetShapeRefusals proves Group G: staged base-diff input
// is canonicalized to its committed-only route, so an empty candidate names
// that real continuation rather than the retired staged/base-ref ambiguity;
// a selector-free status call on an unborn HEAD resolves to the empty-tree
// projection instead of surfacing a raw Git command failure (1771).
func TestOrganicReviewTargetShapeRefusals(t *testing.T) {
	t.Run("issue-1812", func(t *testing.T) {
		harness := newOrganicHarness(t)
		harness.writeFiles(map[string]string{"tracked.txt": organicLines("staged candidate", 4)})
		harness.git("add", "--", "tracked.txt")
		base := strings.TrimSpace(harness.git("rev-parse", "HEAD"))

		_, stderr, err := harness.gentleAllowFailure(
			"review", "start", "--cwd", harness.repo.worktree,
			"--projection", "staged", "--base-ref", base, "--committed-only",
		)
		if err == nil {
			t.Fatal("staged projection + base-ref start unexpectedly succeeded")
		}
		if strings.Contains(stderr, "intent is ambiguous") || !strings.Contains(stderr, "candidate has no pending changes") ||
			!strings.Contains(stderr, "--base-ref <commit>") {
			t.Fatalf("staged base-diff empty-candidate continuation = %q", stderr)
		}
		harness.assertNoSDDArtifacts()
	})

	t.Run("issue-1771", func(t *testing.T) {
		harness := newOrganicHarnessForWorktree(t, initOrganicUnbornRepository(t))
		harness.writeFiles(map[string]string{"candidate.txt": organicLines("unborn selector-free candidate", 4)})
		harness.git("add", "--", "candidate.txt")

		stdout, stderr, err := harness.gentleAllowFailure(
			"review", "status", "--cwd", harness.repo.worktree, "--contract", "gentle-ai.review-integration/v1",
		)
		if err != nil {
			t.Fatalf("selector-free status on unborn HEAD failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		var status organicStatusResult
		if err := json.Unmarshal([]byte(stdout), &status); err != nil {
			t.Fatalf("decode selector-free unborn status: %v\n%s", err, stdout)
		}
		if status.TargetIdentity == "" {
			t.Fatalf("selector-free unborn status = %#v, want a resolved target identity instead of a raw Git failure", status)
		}

		// Community-confirmed repro (reporter lu149e): the plain direct-start
		// path (default workspace projection, no --projection flag) hits the
		// exact same resolveCurrentChangesBase seam and used to fail with
		// `build facade review target: git rev-parse --verify HEAD^{tree}
		// failed with exit code 128: fatal: Needed a single revision`.
		startStdout, startStderr, startErr := harness.gentleAllowFailure(
			"review", "start", "--cwd", harness.repo.worktree,
		)
		if startErr != nil {
			t.Fatalf("direct-start on unborn HEAD (default projection) failed: %v\nstdout:\n%s\nstderr:\n%s", startErr, startStdout, startStderr)
		}
		var started organicStartResult
		if err := json.Unmarshal([]byte(startStdout), &started); err != nil {
			t.Fatalf("decode direct-start on unborn HEAD: %v\n%s", err, startStdout)
		}
		if started.TargetIdentity == "" {
			t.Fatalf("direct-start on unborn HEAD = %#v, want a resolved target identity instead of a raw Git failure", started)
		}
	})

}

// TestOrganicReviewStoreRobustness proves Group D (1813): one invalid
// TERMINAL lineage among several healthy ones is quarantined out of
// selector-free store enumeration alone, with a structured diagnostic that
// names it and never flips store-wide completeness, while every other
// healthy lineage stays fully operable and the quarantined lineage itself
// still fails closed when operated on directly.
func TestOrganicReviewStoreRobustness(t *testing.T) {
	t.Run("issue-1813", func(t *testing.T) {
		harness := newOrganicHarness(t)

		// Approved transactions burn, so the diagnosable terminal state is an
		// escalated lineage. It remains only long enough for store corruption
		// handling; delivery never treats it as authority.
		quarantined := "organic-store-robust-invalid"
		escalateOrganicCandidate(t, harness, quarantined)

		// Corrupt the quarantine-target's retained escalated state semantically,
		// on disk, exactly like a real interrupted/tampered diagnostic authority:
		// mark it invalidated without the provenance CompactState.Validate()
		// requires.
		statePath := filepath.Join(harness.commonDir(), "gentle-ai", "review-transactions", "v2", quarantined, "review-state.json")
		payload, err := os.ReadFile(statePath)
		if err != nil {
			t.Fatal(err)
		}
		corrupted := strings.Replace(string(payload), `"state": "escalated",`, `"state": "invalidated",`, 1)
		if corrupted == string(payload) {
			t.Fatalf("quarantine fixture did not contain the expected escalated state marker")
		}
		if err := os.WriteFile(statePath, []byte(corrupted), 0o644); err != nil {
			t.Fatal(err)
		}

		status := harness.gentle("review", "status", "--cwd", harness.repo.worktree)
		var report struct {
			Complete      bool `json:"complete"`
			Authoritative bool `json:"authoritative"`
			Entries       []struct {
				LineageID string `json:"lineage_id"`
				Status    string `json:"status"`
			} `json:"entries"`
			Diagnostics []struct {
				Path    string `json:"path"`
				Problem string `json:"problem"`
			} `json:"diagnostics"`
		}
		if err := json.Unmarshal(status, &report); err != nil {
			t.Fatalf("decode review status: %v\n%s", err, status)
		}
		if !report.Complete || !report.Authoritative {
			t.Fatalf("quarantined terminal lineage flipped store-wide completeness: %#v", report)
		}
		foundDiagnostic := false
		for _, diagnostic := range report.Diagnostics {
			if strings.Contains(diagnostic.Path, quarantined) && strings.HasPrefix(diagnostic.Problem, "quarantined-terminal-lineage:") {
				foundDiagnostic = true
			}
		}
		if !foundDiagnostic {
			t.Fatalf("no structured diagnostic named the quarantined lineage: %#v", report.Diagnostics)
		}
		for _, entry := range report.Entries {
			if entry.LineageID == quarantined {
				t.Fatalf("quarantined lineage still enumerated as a healthy entry: %#v", entry)
			}
		}
		// Fresh work stays operable alongside the quarantined diagnostic
		// lineage, but its own approval burns rather than accumulating another
		// healthy authority record.
		harness.writeFiles(map[string]string{"segment-healthy-c.txt": "healthy candidate c\n"})
		startedC, _ := harness.startReview("organic-store-robust-healthy-c")
		harness.approveReview("organic-store-robust-healthy-c", startedC)

		// The quarantined lineage itself still fails closed when operated on
		// directly by name: an explicit selector never silently reports it
		// healthy, even though selector-free enumeration now quarantines it.
		explicit := harness.gentle(
			"review", "status", "--cwd", harness.repo.worktree, "--contract", "gentle-ai.review-integration/v1",
			"--lineage", quarantined,
		)
		var explicitResult struct {
			Applicability string `json:"applicability"`
		}
		if err := json.Unmarshal(explicit, &explicitResult); err != nil {
			t.Fatalf("decode explicit-selector status: %v\n%s", err, explicit)
		}
		if explicitResult.Applicability != "corrupted" {
			t.Fatalf("explicit status selector on the quarantined lineage did not fail closed: %#v", explicitResult)
		}
		harness.assertNoSDDArtifacts()
	})
}

// TestOrganicReviewNarrationPairedRecoverableVersusTerminal keeps the paired
// scenario from organic-dx Phase 4 -- one underlying machinery condition (no
// existing review record matches this target,
// reviewtransaction.TargetApplicabilityUnrelated) with two caller-selector
// shapes -- under the negotiated-silence contract: a successful negotiated
// (--contract) STATUS is machine-readable end to end, so BOTH shapes keep
// stdout as the byte-for-byte JSON envelope and write zero bytes to stderr.
// The terminal shape's decision stays structural in
// next_transition.reason_code (staged_workspace_overlay_recovery_unavailable)
// instead of being narrated: gentle-pi's adapter fails closed
// (UNEXPECTED_STDERR) on any stderr a successful native process writes, and
// the registered Tier C statement remains in internal/cli/review_narration.go
// as vocabulary only.
func TestOrganicReviewNarrationPairedRecoverableVersusTerminal(t *testing.T) {
	t.Run("issue-organic-dx-phase4-paired-narration", func(t *testing.T) {
		harness := newOrganicHarness(t)
		harness.writeFiles(map[string]string{"tracked.txt": "narration paired scenario\n"})
		harness.git("add", "-A")
		harness.git("commit", "-qm", "narration paired scenario base")
		harness.writeFiles(map[string]string{"tracked.txt": "narration paired scenario, uncommitted\n"})

		t.Run("recoverable", func(t *testing.T) {
			stdout, stderr, err := harness.gentleAllowFailure(
				"review", "status", "--cwd", harness.repo.worktree, "--contract", "gentle-ai.review-integration/v1", "--next-transition",
			)
			if err != nil {
				t.Fatalf("ordinary next-transition on a fresh target failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}
			var status organicStatusResult
			if err := json.Unmarshal([]byte(stdout), &status); err != nil {
				t.Fatalf("decode status: %v\n%s", err, stdout)
			}
			if status.NextTransition == nil || status.NextTransition.Kind != "execute" || status.NextTransition.ReasonCode != "fresh_target_ready" {
				t.Fatalf("ordinary next-transition on a fresh target = %#v, want an executable fresh_target_ready transition", status.NextTransition)
			}
			if strings.TrimSpace(stderr) != "" {
				t.Fatalf("recoverable shape leaked output on the human surface, want zero Tier-B/Tier-C output: stderr=%q", stderr)
			}
		})

		t.Run("terminal", func(t *testing.T) {
			stdout, stderr, err := harness.gentleAllowFailure(
				"review", "status", "--cwd", harness.repo.worktree, "--contract", "gentle-ai.review-integration/v1", "--next-transition",
				"--workspace-overlay", "--projection", "staged", "--base-ref", "HEAD",
			)
			if err != nil {
				t.Fatalf("terminal next-transition on the same fresh target failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}
			var status organicStatusResult
			if err := json.Unmarshal([]byte(stdout), &status); err != nil {
				t.Fatalf("decode status: %v\n%s", err, stdout)
			}
			if status.NextTransition == nil || status.NextTransition.Kind != "stop" || status.NextTransition.ReasonCode != "staged_workspace_overlay_recovery_unavailable" {
				t.Fatalf("terminal next-transition on the same fresh target = %#v, want the staged_workspace_overlay_recovery_unavailable stop", status.NextTransition)
			}
			if stderr != "" {
				t.Fatalf("stop-shaped negotiated STATUS wrote stderr, want zero bytes: %q", stderr)
			}
		})
	})
}

// reviewDefectReportDirEntries lists the files under this repository's
// <GitCommonDir>/gentle-ai/defect-reports/, or nil if the directory does not
// exist yet -- exactly the storage location organic-dx tasks.md Phase 5
// documents (never inside the working tree, never committed).
func reviewDefectReportDirEntries(t *testing.T, harness *organicHarness) []string {
	t.Helper()
	dir := filepath.Join(harness.commonDir(), "gentle-ai", "defect-reports")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// TestOrganicReviewDefectReportToolFaultVersusUserDecision proves organic-dx
// Phase 5: a legitimate occupied reviewer-result slot generates no report and
// directs the caller to the authoritative STATUS continuation.
func TestOrganicReviewDefectReportToolFaultVersusUserDecision(t *testing.T) {
	t.Run("issue-organic-dx-phase5-user-decision-no-report", func(t *testing.T) {
		harness := newOrganicHarness(t)
		lineage := "organic-defect-report-user-decision"
		harness.writeFiles(map[string]string{"tracked.txt": organicLines("defect report user-decision candidate", 6)})
		started, _ := harness.startReview(lineage)
		if len(started.SelectedLenses) == 0 {
			t.Fatal("expected at least one selected lens")
		}
		lens := started.SelectedLenses[0]
		subjectHash := harnessCaptureResultSubjectHash(t, harness, lineage, started.TargetIdentity, lens, 0)

		reviewerResult := struct {
			SubjectHash string                   `json:"subject_hash"`
			Inspection  organicCaptureInspection `json:"inspection"`
			Findings    []organicFinding         `json:"findings"`
			Evidence    []string                 `json:"evidence"`
		}{
			SubjectHash: subjectHash,
			Inspection:  organicCaptureInspection{Status: "completed", Paths: []string{"tracked.txt"}},
			Findings:    []organicFinding{},
		}
		reviewerResult.Evidence = []string{"first pass"}
		firstResult := harness.writeJSON("first-result.json", reviewerResult)
		harness.gentle(
			"review", "capture-result", "--cwd", harness.repo.worktree, "--lineage", lineage,
			"--target", started.TargetIdentity, "--lens", lens, "--order", "0", "--input", firstResult,
		)

		reviewerResult.Evidence = []string{"second pass, different content"}
		secondResult := harness.writeJSON("second-result.json", reviewerResult)
		_, stderr, err := harness.gentleAllowFailure(
			"review", "capture-result", "--cwd", harness.repo.worktree, "--lineage", lineage,
			"--target", started.TargetIdentity, "--lens", lens, "--order", "0", "--input", secondResult,
		)
		if err == nil {
			t.Fatal("conflicting reviewer result capture unexpectedly succeeded")
		}
		for _, want := range []string{"reviewer_result_slot_occupied", "gentle-ai review status --cwd <repo> --contract gentle-ai.review-integration/v2 --next-transition", "authoritative continuation"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("occupied-slot terminal did not name %q: %q", want, stderr)
			}
		}
		for _, forbidden := range []string{"review dispose-result", "review preserve-result", "retry capture-result"} {
			if strings.Contains(stderr, forbidden) {
				t.Fatalf("occupied-slot terminal advertised %q: %q", forbidden, stderr)
			}
		}
		if entries := reviewDefectReportDirEntries(t, harness); len(entries) != 0 {
			t.Fatalf("user-decision terminal wrote a defect report, want none: %v", entries)
		}
	})
}
