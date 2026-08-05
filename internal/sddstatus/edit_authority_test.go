package sddstatus

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #2547 (S1 of #2540): a multi-repository change whose tasks target
// sibling Git repositories must not report applyState ready while
// allowedEditRoots names only the planning repository. On the unfixed base
// this fixture reports ready/apply with zero blockers — the contradictory
// route: native status authorizes apply, but the sdd-apply outside-root guard
// forbids every first work unit.

// initEditAuthorityGitRepo turns dir into a Git repository. Only the planning
// repository needs a commit (the native runtime store resolves its repository
// root); sibling service repositories only need a `.git` for root detection.
func initEditAuthorityGitRepo(t *testing.T, dir string, commit bool) {
	t.Helper()
	mkdir(t, dir)
	runRuntimeLedgerGit(t, dir, "init", "-q")
	if !commit {
		return
	}
	runRuntimeLedgerGit(t, dir, "config", "user.email", "edit-authority@example.com")
	runRuntimeLedgerGit(t, dir, "config", "user.name", "Edit Authority Test")
	write(t, filepath.Join(dir, "tracked.txt"), "base\n")
	runRuntimeLedgerGit(t, dir, "add", "tracked.txt")
	runRuntimeLedgerGit(t, dir, "commit", "-qm", "base")
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestMultiRepoTaskTargetsBlockApplyWithoutEditAuthority(t *testing.T) {
	workspace := t.TempDir() // the workspace itself is NOT a Git repository
	planning := filepath.Join(workspace, "planning")
	serviceA := filepath.Join(workspace, "service-a")
	serviceB := filepath.Join(workspace, "service-b")
	initEditAuthorityGitRepo(t, planning, true)
	initEditAuthorityGitRepo(t, serviceA, false)
	initEditAuthorityGitRepo(t, serviceB, false)

	seedReadyChange(t, planning, "multi-repo-rollout", strings.Join([]string{
		"- [ ] 1.1 Update `../service-a/internal/api/handler.go` to accept the new header",
		"- [ ] 1.2 Update `../service-b/internal/worker/consume.go` to forward the header",
		"- [ ] 1.3 Record the rollout order in `openspec/changes/multi-repo-rollout/design.md`",
		"",
	}, "\n"))

	status, err := Resolve(ResolveOptions{CWD: planning, ChangeName: "multi-repo-rollout"})
	if err != nil {
		t.Fatal(err)
	}

	wantA := realPath(t, serviceA)
	wantB := realPath(t, serviceB)
	reasons := strings.Join(status.BlockedReasons, "\n")
	if status.ApplyState != ApplyBlocked {
		t.Fatalf("multi-repo change reported applyState = %q, nextRecommended = %q, blockedReasons = %v; want applyState blocked with blocked(edit_authority_missing) naming %s and %s",
			status.ApplyState, status.NextRecommended, status.BlockedReasons, wantA, wantB)
	}
	if status.NextRecommended == "apply" {
		t.Fatalf("nextRecommended = %q for a change whose every first work unit is outside the authorized edit roots", status.NextRecommended)
	}
	if !strings.Contains(reasons, "blocked(edit_authority_missing)") {
		t.Fatalf("blocked reasons lack the edit-authority reason code: %s", reasons)
	}
	if !strings.Contains(reasons, wantA) || !strings.Contains(reasons, wantB) {
		t.Fatalf("blocked reasons must name each unauthorized target root %q and %q: %s", wantA, wantB, reasons)
	}
}

// TestSingleRepoTasksKeepStatusByteIdentical pins the no-false-positive
// contract: for an ordinary single-repository change, detection matches
// nothing and the wiring mutates nothing, so the status an operator sees is
// byte-identical to the pre-#2547 status. The wiring only touches applyState
// and blockedReasons when at least one unauthorized root is found, so an
// empty JSON footprint here proves the whole fixture is untouched.
func TestSingleRepoTasksKeepStatusByteIdentical(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	seedReadyChange(t, repo, "single-repo-change", strings.Join([]string{
		"- [ ] 1.1 Update `internal/auth/login.go` with the new claim check",
		"- [x] 1.2 Extend `openspec/changes/single-repo-change/tasks.md` after review",
		"- [ ] 1.3 Run `go test ./...` and fix regressions",
		"",
	}, "\n"))

	status, err := Resolve(ResolveOptions{CWD: repo, ChangeName: "single-repo-change"})
	if err != nil {
		t.Fatal(err)
	}

	if status.ApplyState != ApplyReady || status.NextRecommended != "apply" {
		t.Fatalf("single-repo change lost apply readiness: applyState = %q nextRecommended = %q blockedReasons = %v",
			status.ApplyState, status.NextRecommended, status.BlockedReasons)
	}
	if len(status.BlockedReasons) != 0 {
		t.Fatalf("single-repo change gained blocked reasons: %v", status.BlockedReasons)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "edit_authority_missing") {
		t.Fatalf("single-repo status carries an edit-authority footprint: %s", encoded)
	}
}

// TestDetectUnauthorizedEditRootsHonorsAllowedRoots pins the seam a later
// slice of #2540 plugs into: persisted per-change grants extend the
// allowed-roots parameter, and detection needs no rework. It also pins the
// nearest-existing-ancestor rule (task prose names files that do not exist
// yet) and that non-path prose stays invisible.
func TestDetectUnauthorizedEditRootsHonorsAllowedRoots(t *testing.T) {
	workspace := t.TempDir()
	planning := filepath.Join(workspace, "planning")
	serviceA := filepath.Join(workspace, "service-a")
	initEditAuthorityGitRepo(t, planning, false)
	initEditAuthorityGitRepo(t, serviceA, false)
	tasks := strings.Join([]string{
		"- [ ] 1.1 Update `../service-a/does/not/exist/yet.go` for the rollout",
		"- [ ] 1.2 Update the billing service and run `go test ./...`",
		"- [ ] 1.3 Read https://example.com/service-b/docs first",
		"",
	}, "\n")

	wantA := realPath(t, serviceA)
	flagged := detectUnauthorizedEditRoots(tasks, planning, []string{planning})
	if len(flagged) != 1 || flagged[0] != wantA {
		t.Fatalf("detectUnauthorizedEditRoots() = %v, want exactly [%s]", flagged, wantA)
	}

	granted := detectUnauthorizedEditRoots(tasks, planning, []string{planning, serviceA})
	if len(granted) != 0 {
		t.Fatalf("granting %s must clear the block without detector rework, got %v", serviceA, granted)
	}
}

// TestEngramTasksTextBlocksApplyOnUnauthorizedTargets drives the same
// detection through resolveEngramStatus, which has no tasks.md path — only
// the tasks artifact text — so the detector must accept text plus the
// workspace root.
func TestEngramTasksTextBlocksApplyOnUnauthorizedTargets(t *testing.T) {
	workspace := t.TempDir() // the workspace itself is NOT a Git repository
	planning := filepath.Join(workspace, "planning")
	serviceA := filepath.Join(workspace, "service-a")
	initEditAuthorityGitRepo(t, planning, true)
	initEditAuthorityGitRepo(t, serviceA, false)
	mkdir(t, filepath.Join(planning, ".engram"))
	runRuntimeLedgerGit(t, planning, "remote", "add", "origin", "git@github.com:Gentleman-Programming/gentle-ai.git")

	restore := stubEngramExport(t, []engramObservation{
		{Title: "sdd/cross-repo/proposal", Content: "## Proposal\nRoll out the header", Project: "gentle-ai", Scope: "project"},
		{Title: "sdd/cross-repo/spec", Content: "## Requirements\n- SHALL forward the header", Project: "gentle-ai", Scope: "project"},
		{Title: "sdd/cross-repo/design", Content: "## Design\nSequential rollout", Project: "gentle-ai", Scope: "project"},
		{Title: "sdd/cross-repo/tasks", Content: "- [ ] 1.1 Update `../service-a/internal/api/handler.go` first\n", Project: "gentle-ai", Scope: "project"},
	})
	defer restore()

	status, err := Resolve(ResolveOptions{CWD: planning})
	if err != nil {
		t.Fatal(err)
	}
	if status.ArtifactStore != ArtifactStoreEngram {
		t.Fatalf("ArtifactStore = %q, want %q", status.ArtifactStore, ArtifactStoreEngram)
	}

	wantA := realPath(t, serviceA)
	reasons := strings.Join(status.BlockedReasons, "\n")
	if status.ApplyState != ApplyBlocked || status.NextRecommended == "apply" {
		t.Fatalf("engram-path change reported applyState = %q, nextRecommended = %q, blockedReasons = %v; want applyState blocked with blocked(edit_authority_missing) naming %s",
			status.ApplyState, status.NextRecommended, status.BlockedReasons, wantA)
	}
	if !strings.Contains(reasons, "blocked(edit_authority_missing)") || !strings.Contains(reasons, wantA) {
		t.Fatalf("blocked reasons must carry the reason code and name %q: %s", wantA, reasons)
	}
}
