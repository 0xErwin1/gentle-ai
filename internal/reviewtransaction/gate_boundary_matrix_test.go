package reviewtransaction

// Wave 5 (Gate Cutover), Slice 1, task 2.6: the 35-cell gate-boundary-matrix
// harness (design decision 7). 5 gates x 7 algebra relations
// (exact, compatible_base_advance, provable_contraction, changed, ambiguous,
// unknown, unrelated). Per tasks.md 2.6, this slice builds the HARNESS ONLY:
// gateVerdict(gate, relation) is a Wave 5 Slice 3 deliverable that does not
// exist yet at this commit, and NativeGateEvaluation carries no Relation
// field until Slice 3 lands it. A cell can therefore be wired here ONLY when
// a REAL, ALREADY-SHIPPED production code path can be driven to prove it —
// this harness must never reimplement the algebra by hand to fabricate a
// verdict. Every cell this slice cannot yet drive through real production
// code is an explicit, reasoned SKIP (Explained: true), never a fabricated
// pass. Wiring lands incrementally in S2-S7 (design's PR Slicing Preview);
// the full 35-cell run with zero unexplained divergences is S6/S7's exit
// bar (tasks.md 6.6, 8.7).
//
// The wired cells drive the REAL compiled gentle-ai binary as a subprocess
// (not an in-process Go call, not a reimplementation) -- the same technique
// e2e/organicruntime uses (buildOrganicBinary): `go build ./cmd/gentle-ai`
// once per test run, then every wired cell execs that one binary through
// `review start` / `review finalize` / `review validate --gate <gate>`.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var gateBoundaryMatrixGates = []string{
	string(GatePostApply), string(GatePreCommit), string(GatePrePush), string(GatePrePR), string(GateRelease),
}

// gateBoundaryMatrixRelations mirrors the 7-value CandidateRelation
// vocabulary (rdd-candidate-relation-algebra, Wave 1) as plain strings: this
// harness is gate-and-verdict-shaped scaffolding, not an algebra consumer,
// so it names the relations without importing the (not-yet-gate-facing)
// CandidateRelation type.
var gateBoundaryMatrixRelations = []string{
	"exact", "compatible_base_advance", "provable_contraction", "changed", "ambiguous", "unknown", "unrelated",
}

const gateBoundaryMatrixNotWiredReason = "gateVerdict(gate, relation) does not exist yet (Wave 5 Slice 3 deliverable); " +
	"NativeGateEvaluation carries no Relation field until Slice 3, and legacy-through-algebra projection lands in " +
	"Slice 4, so no real production code path can be driven to prove this cell yet. This harness must drive real " +
	"code, not reimplement the algebra by hand, so the cell is an explicit SKIP, not a fabricated pass."

type gateBoundaryMatrixRow struct {
	Gate      string `json:"gate"`
	Relation  string `json:"relation"`
	Verdict   string `json:"verdict"`
	NextStep  string `json:"next_step,omitempty"`
	Explained bool   `json:"explained"`
	Reason    string `json:"reason"`
}

// updateGateBoundaryMatrixGolden reuses shadow_matrix_test.go's "-update"
// flag (same package, same golden-update convention) rather than
// registering a second "-update" flag, which would panic at test-binary
// init.
var updateGateBoundaryMatrixGolden = updateShadowMatrixGolden

var (
	gateBoundaryMatrixBinaryOnce sync.Once
	gateBoundaryMatrixBinaryPath string
	gateBoundaryMatrixBinaryErr  error
)

// gateBoundaryMatrixBinary compiles the real gentle-ai binary once for the
// whole test run (mirrors e2e/organicruntime's buildOrganicBinary) so every
// wired cell drives the identical, actually-shipped CLI, never a
// reimplementation of gate-evaluation logic.
func gateBoundaryMatrixBinary(t *testing.T) string {
	t.Helper()
	gateBoundaryMatrixBinaryOnce.Do(func() {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			gateBoundaryMatrixBinaryErr = errors.New("resolve gate boundary matrix test source")
			return
		}
		moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
		name := "gentle-ai-gate-boundary-matrix"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		dir, err := os.MkdirTemp("", "gate-boundary-matrix-binary-*")
		if err != nil {
			gateBoundaryMatrixBinaryErr = err
			return
		}
		path := filepath.Join(dir, name)
		cmd := exec.CommandContext(context.Background(), "go", "build", "-trimpath", "-o", path, "./cmd/gentle-ai")
		cmd.Dir = moduleRoot
		cmd.Env = os.Environ()
		if output, err := cmd.CombinedOutput(); err != nil {
			gateBoundaryMatrixBinaryErr = fmt.Errorf("build gentle-ai test binary: %w\n%s", err, output)
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			gateBoundaryMatrixBinaryErr = fmt.Errorf("built gentle-ai binary %q is unusable: %v", path, err)
			return
		}
		gateBoundaryMatrixBinaryPath = path
	})
	if gateBoundaryMatrixBinaryErr != nil {
		t.Fatalf("gate boundary matrix binary: %v", gateBoundaryMatrixBinaryErr)
	}
	return gateBoundaryMatrixBinaryPath
}

// runGateBoundaryMatrixReview execs the real binary's `review <verb>` for
// one repository, appending `--cwd repo` so callers only name the verb and
// its verb-specific flags.
func runGateBoundaryMatrixReview(binary, repo string, args ...string) (string, error) {
	full := append([]string{"review"}, args...)
	full = append(full, "--cwd", repo)
	cmd := exec.CommandContext(context.Background(), binary, full...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

type gateBoundaryMatrixCLIResult struct {
	Result string `json:"result"`
}

func decodeGateBoundaryMatrixResult(t *testing.T, payload string) gateBoundaryMatrixCLIResult {
	t.Helper()
	var result gateBoundaryMatrixCLIResult
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		t.Fatalf("decode gate boundary matrix CLI result: %v\n%s", err, payload)
	}
	return result
}

// TestGateBoundaryMatrix_35Cells builds and checks
// testdata/gate-boundary-matrix.golden: 35 rows (5 gates x 7 relations).
// Wired cells (currently: the "exact" relation at post-apply, pre-commit,
// pre-push, and pre-pr) are driven through the real compiled binary and
// assert GateAllow. Every other cell -- including release's "exact" cell,
// whose genuine construction needs additional release-evidence artifact
// fixtures beyond this slice's scope -- is an explicit, reasoned SKIP.
func TestGateBoundaryMatrix_35Cells(t *testing.T) {
	binary := gateBoundaryMatrixBinary(t)
	wired := map[[2]string]gateBoundaryMatrixRow{}

	// post-apply / exact: workspace candidate matches the approved receipt
	// with zero further changes.
	{
		repo := initSnapshotRepo(t)
		writeSnapshotFile(t, repo, "docs/notes.md", "release notes\n")
		lineage := "matrix-post-apply-exact"
		if out, err := runGateBoundaryMatrixReview(binary, repo, "start", "--lineage", lineage); err != nil {
			t.Fatalf("post-apply/exact review start: %v\n%s", err, out)
		}
		if out, err := runGateBoundaryMatrixReview(binary, repo, "finalize", "--lineage", lineage); err != nil {
			t.Fatalf("post-apply/exact review finalize: %v\n%s", err, out)
		}
		out, err := runGateBoundaryMatrixReview(binary, repo, "validate", "--lineage", lineage, "--gate", string(GatePostApply))
		if err != nil {
			t.Fatalf("post-apply/exact review validate: %v\n%s", err, out)
		}
		result := decodeGateBoundaryMatrixResult(t, out)
		if result.Result != string(GateAllow) {
			t.Fatalf("post-apply/exact result = %#v", result)
		}
		wired[[2]string{string(GatePostApply), "exact"}] = gateBoundaryMatrixRow{
			Gate: string(GatePostApply), Relation: "exact", Verdict: string(GateAllow), Explained: false,
			Reason: "driven via the real gentle-ai binary: review start -> finalize -> validate --gate post-apply on the identical workspace candidate",
		}
	}

	// pre-commit / exact: same workspace candidate, staged (not committed).
	{
		repo := initSnapshotRepo(t)
		writeSnapshotFile(t, repo, "docs/notes.md", "release notes\n")
		lineage := "matrix-pre-commit-exact"
		if out, err := runGateBoundaryMatrixReview(binary, repo, "start", "--lineage", lineage); err != nil {
			t.Fatalf("pre-commit/exact review start: %v\n%s", err, out)
		}
		if out, err := runGateBoundaryMatrixReview(binary, repo, "finalize", "--lineage", lineage); err != nil {
			t.Fatalf("pre-commit/exact review finalize: %v\n%s", err, out)
		}
		gitSnapshot(t, repo, "add", "docs/notes.md")
		out, err := runGateBoundaryMatrixReview(binary, repo, "validate", "--lineage", lineage, "--gate", string(GatePreCommit))
		if err != nil {
			t.Fatalf("pre-commit/exact review validate: %v\n%s", err, out)
		}
		result := decodeGateBoundaryMatrixResult(t, out)
		if result.Result != string(GateAllow) {
			t.Fatalf("pre-commit/exact result = %#v", result)
		}
		wired[[2]string{string(GatePreCommit), "exact"}] = gateBoundaryMatrixRow{
			Gate: string(GatePreCommit), Relation: "exact", Verdict: string(GateAllow), Explained: false,
			Reason: "driven via the real gentle-ai binary: review start -> finalize -> git add -> validate --gate pre-commit on the identical staged candidate",
		}
	}

	// pre-push and pre-pr / exact: the same committed candidate, one commit
	// ahead of an unchanged origin remote, satisfies both gates' boundary.
	{
		repo := initSnapshotRepo(t)
		branch := currentBranch(context.Background(), repo)
		remote := configurePublicationRemote(t, repo, branch)
		writeSnapshotFile(t, repo, "docs/notes.md", "release notes\n")
		lineage := "matrix-push-pr-exact"
		if out, err := runGateBoundaryMatrixReview(binary, repo, "start", "--lineage", lineage); err != nil {
			t.Fatalf("pre-push/pre-pr exact review start: %v\n%s", err, out)
		}
		if out, err := runGateBoundaryMatrixReview(binary, repo, "finalize", "--lineage", lineage); err != nil {
			t.Fatalf("pre-push/pre-pr exact review finalize: %v\n%s", err, out)
		}
		gitSnapshot(t, repo, "add", "docs/notes.md")
		gitSnapshot(t, repo, "commit", "-m", "reviewed candidate")
		_ = remote

		pushOut, err := runGateBoundaryMatrixReview(binary, repo, "validate", "--lineage", lineage,
			"--gate", string(GatePrePush), "--base-ref", "origin/"+branch)
		if err != nil {
			t.Fatalf("pre-push/exact review validate: %v\n%s", err, pushOut)
		}
		pushResult := decodeGateBoundaryMatrixResult(t, pushOut)
		if pushResult.Result != string(GateAllow) {
			t.Fatalf("pre-push/exact result = %#v", pushResult)
		}
		wired[[2]string{string(GatePrePush), "exact"}] = gateBoundaryMatrixRow{
			Gate: string(GatePrePush), Relation: "exact", Verdict: string(GateAllow), Explained: false,
			Reason: "driven via the real gentle-ai binary: review start -> finalize -> git add/commit -> validate --gate pre-push against an unchanged origin remote",
		}

		prOut, err := runGateBoundaryMatrixReview(binary, repo, "validate", "--lineage", lineage,
			"--gate", string(GatePrePR), "--base-ref", "origin/"+branch)
		if err != nil {
			t.Fatalf("pre-pr/exact review validate: %v\n%s", err, prOut)
		}
		prResult := decodeGateBoundaryMatrixResult(t, prOut)
		if prResult.Result != string(GateAllow) {
			t.Fatalf("pre-pr/exact result = %#v", prResult)
		}
		wired[[2]string{string(GatePrePR), "exact"}] = gateBoundaryMatrixRow{
			Gate: string(GatePrePR), Relation: "exact", Verdict: string(GateAllow), Explained: false,
			Reason: "driven via the real gentle-ai binary: review start -> finalize -> git add/commit -> validate --gate pre-pr against an unchanged origin remote base",
		}
	}

	rows := make([]gateBoundaryMatrixRow, 0, len(gateBoundaryMatrixGates)*len(gateBoundaryMatrixRelations))
	for _, gate := range gateBoundaryMatrixGates {
		for _, relation := range gateBoundaryMatrixRelations {
			if row, ok := wired[[2]string{gate, relation}]; ok {
				rows = append(rows, row)
				continue
			}
			rows = append(rows, gateBoundaryMatrixRow{
				Gate: gate, Relation: relation, Explained: true, Reason: gateBoundaryMatrixNotWiredReason,
			})
		}
	}
	if len(rows) != 35 {
		t.Fatalf("gate boundary matrix has %d rows, want 35 (5 gates x 7 relations)", len(rows))
	}
	if len(wired) != 4 {
		t.Fatalf("gate boundary matrix wired %d cells, want exactly 4 this slice (post-apply/pre-commit/pre-push/pre-pr exact)", len(wired))
	}

	actual, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	actual = append(actual, '\n')

	goldenPath := filepath.Join("testdata", "gate-boundary-matrix.golden")
	if *updateGateBoundaryMatrixGolden {
		if err := os.WriteFile(goldenPath, actual, 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", goldenPath, err)
		}
		t.Logf("updated golden file: %s (%d rows, %d wired)", goldenPath, len(rows), len(wired))
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("missing golden %s -- run: go test ./internal/reviewtransaction/... -run TestGateBoundaryMatrix_35Cells -update (%v)", goldenPath, err)
	}
	if string(want) != string(actual) {
		t.Fatalf("golden mismatch for %s -- rerun with -update after inspecting the diff\nwant:\n%s\ngot:\n%s", goldenPath, want, actual)
	}
}
