package reviewtransaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStoreResetFile creates one fixture file and every parent directory it
// needs. Fixtures are written straight to disk on purpose: this command has to
// work against a store the normal writers can no longer open, so building the
// fixture through those writers would only ever prove the healthy case.
func writeStoreResetFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// storeResetGentleAIRoot returns <git-common-dir>/gentle-ai for a fixture repo.
func storeResetGentleAIRoot(t *testing.T, repo string) string {
	t.Helper()
	authority, _, err := reviewAuthorityRoot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(authority)
}

// seedStoreResetLineage writes one compact-v2 lineage holding the given state.
func seedStoreResetLineage(t *testing.T, repo, lineage string, state State) {
	t.Helper()
	root := storeResetGentleAIRoot(t, repo)
	writeStoreResetFile(t, filepath.Join(root, "review-transactions", "v2", lineage, "review-state.json"),
		`{"schema":"gentle-ai.review-state-record/v2","revision":"sha256:00","state":{"schema":"gentle-ai.review-state/v2","lineage_id":"`+lineage+`","state":"`+string(state)+`"}}`+"\n")
}

// seedStoreResetKillSwitch writes the clone-scoped kill switch into both the
// current location and the pre-#2882 mirror that still-installed builds read.
func seedStoreResetKillSwitch(t *testing.T, repo string) (current, legacy string) {
	t.Helper()
	root := storeResetGentleAIRoot(t, repo)
	current = filepath.Join(root, "review-mode", "rar-authority", "v1", "rdd-mode", "gen-0000000001.json")
	legacy = filepath.Join(root, "review-transactions", "rar-authority", "v1", "rdd-mode", "gen-0000000001.json")
	writeStoreResetFile(t, current, `{"schema":"gentle-ai.rdd-mode-override/v1","mode":"off"}`+"\n")
	writeStoreResetFile(t, legacy, `{"schema":"gentle-ai.rdd-mode-override/v1","mode":"off"}`+"\n")
	return current, legacy
}

// stubStoreResetRename swaps the rename seam for one test and restores it
// afterwards, mirroring reclaimQuarantineResidue in compact_reclaim.go.
func stubStoreResetRename(t *testing.T, rename func(oldpath, newpath string) error) {
	t.Helper()
	previous := storeResetRename
	storeResetRename = rename
	t.Cleanup(func() { storeResetRename = previous })
}

func storeResetEntryByName(t *testing.T, entries []StoreResetEntry, name string) StoreResetEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("no entry named %q in %#v", name, entries)
	return StoreResetEntry{}
}

// TestSurveyReviewStoreReportsEveryCategoryWithoutMutating is the preview
// contract: it must name what it would remove, count it, and leave every byte
// exactly where it found it.
func TestSurveyReviewStoreReportsEveryCategoryWithoutMutating(t *testing.T) {
	repo := initSnapshotRepo(t)
	root := storeResetGentleAIRoot(t, repo)
	seedStoreResetLineage(t, repo, "review-settled", StateApproved)
	writeStoreResetFile(t, filepath.Join(root, "candidate-views", "1a09d6f9", "README.md"), "checkout\n")
	writeStoreResetFile(t, filepath.Join(root, "reviews", "IDENTITY"), "{}\n")
	seedStoreResetKillSwitch(t, repo)

	report, err := SurveyReviewStore(context.Background(), repo)
	if err != nil {
		t.Fatalf("survey: %v", err)
	}
	if report.Operation != StoreResetPreview {
		t.Fatalf("operation = %q, want %q", report.Operation, StoreResetPreview)
	}
	if report.Schema != StoreResetReportSchema {
		t.Fatalf("schema = %q", report.Schema)
	}
	for _, name := range []string{"candidate-views", "review-transactions/v2", "reviews"} {
		entry := storeResetEntryByName(t, report.Removable, name)
		if !entry.Present || entry.Files == 0 || entry.Bytes == 0 {
			t.Fatalf("removable %q = %#v, want a present non-empty category", name, entry)
		}
		if entry.Removed {
			t.Fatalf("survey reported %q as removed", name)
		}
	}
	if report.RemovedFiles != 0 || report.RemovedBytes != 0 {
		t.Fatalf("survey reported removals: %d files / %d bytes", report.RemovedFiles, report.RemovedBytes)
	}

	// Nothing moved.
	for _, path := range []string{
		filepath.Join(root, "candidate-views", "1a09d6f9", "README.md"),
		filepath.Join(root, "reviews", "IDENTITY"),
		filepath.Join(root, "review-transactions", "v2", "review-settled", "review-state.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("survey mutated the store: %v", err)
		}
	}
}

// TestSurveyReviewStoreCreatesNoStateOnACleanClone proves the preview is safe
// to run anywhere: a repository that never reviewed anything must not gain a
// gentle-ai directory just because someone asked what a reset would do.
func TestSurveyReviewStoreCreatesNoStateOnACleanClone(t *testing.T) {
	repo := initSnapshotRepo(t)

	report, err := SurveyReviewStore(context.Background(), repo)
	if err != nil {
		t.Fatalf("survey: %v", err)
	}
	if report.RemovedFiles != 0 || len(report.InFlight) != 0 {
		t.Fatalf("clean clone report = %#v", report)
	}
	for _, entry := range report.Removable {
		if entry.Present {
			t.Fatalf("clean clone reported %q present", entry.Name)
		}
	}
	if _, err := os.Lstat(storeResetGentleAIRoot(t, repo)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("survey created review state: %v", err)
	}
}

// TestResetReviewStoreRefusesWhileLineagesAreInFlight is the central safety
// rule. A lineage that is not approved, escalated, or invalidated is a review
// somebody is in the middle of, and the default answer is no.
func TestResetReviewStoreRefusesWhileLineagesAreInFlight(t *testing.T) {
	repo := initSnapshotRepo(t)
	seedStoreResetLineage(t, repo, "review-settled", StateApproved)
	seedStoreResetLineage(t, repo, "review-active", StateReviewing)
	seedStoreResetLineage(t, repo, "review-correcting", StateCorrectionRequired)
	root := storeResetGentleAIRoot(t, repo)

	report, err := ResetReviewStore(context.Background(), repo, StoreResetRequest{})
	var inFlight *StoreResetInFlightError
	if !errors.As(err, &inFlight) {
		t.Fatalf("reset with in-flight lineages = %v, want a typed refusal", err)
	}
	if len(inFlight.Lineages) != 2 {
		t.Fatalf("refusal listed %#v, want both in-flight lineages", inFlight.Lineages)
	}
	// The refusal has to name them; a count alone does not tell the user which
	// review they are about to lose.
	for _, lineage := range []string{"review-active", "review-correcting"} {
		if !strings.Contains(inFlight.Error(), lineage) {
			t.Fatalf("refusal does not name %q: %v", lineage, inFlight)
		}
	}
	if strings.Contains(inFlight.Error(), "review-settled") {
		t.Fatalf("refusal named a settled lineage: %v", inFlight)
	}
	if !strings.Contains(inFlight.Error(), "--include-in-flight") {
		t.Fatalf("refusal does not name the override flag: %v", inFlight)
	}
	if report.RemovedFiles != 0 || report.RemovedBytes != 0 {
		t.Fatalf("refused reset still removed %d files", report.RemovedFiles)
	}
	// A refusal removes nothing at all -- not even the settled lineages.
	for _, lineage := range []string{"review-settled", "review-active", "review-correcting"} {
		if _, err := os.Stat(filepath.Join(root, "review-transactions", "v2", lineage, "review-state.json")); err != nil {
			t.Fatalf("refused reset removed %q: %v", lineage, err)
		}
	}
}

// TestResetReviewStoreUnreadableLineageCountsAsInFlight fails closed. A record
// this command cannot parse might be an open review, and a degraded store is
// exactly when that guess gets made, so it guesses in the direction that keeps
// the work.
func TestResetReviewStoreUnreadableLineageCountsAsInFlight(t *testing.T) {
	repo := initSnapshotRepo(t)
	root := storeResetGentleAIRoot(t, repo)
	writeStoreResetFile(t, filepath.Join(root, "review-transactions", "v2", "review-damaged", "review-state.json"), "{not json\n")

	_, err := ResetReviewStore(context.Background(), repo, StoreResetRequest{})
	var inFlight *StoreResetInFlightError
	if !errors.As(err, &inFlight) {
		t.Fatalf("reset over an unreadable record = %v, want a typed refusal", err)
	}
	if len(inFlight.Lineages) != 1 || inFlight.Lineages[0].Unreadable == "" {
		t.Fatalf("unreadable lineage = %#v, want it reported as unreadable", inFlight.Lineages)
	}
}

// TestResetReviewStoreRemovesSettledLineagesWithoutAnOverride is the ordinary
// path: every lineage has reached a terminal state, so no explicit flag is
// required and the store empties.
func TestResetReviewStoreRemovesSettledLineagesWithoutAnOverride(t *testing.T) {
	repo := initSnapshotRepo(t)
	root := storeResetGentleAIRoot(t, repo)
	seedStoreResetLineage(t, repo, "review-approved", StateApproved)
	seedStoreResetLineage(t, repo, "review-escalated", StateEscalated)
	seedStoreResetLineage(t, repo, "review-invalidated", StateInvalidated)
	writeStoreResetFile(t, filepath.Join(root, "review-transactions", "quarantine", "review-old-1", "reclaim-record.json"), "{}\n")
	writeStoreResetFile(t, filepath.Join(root, "review-transactions", "effect-markers", "v1", "marker.json"), "{}\n")
	writeStoreResetFile(t, filepath.Join(root, "candidate-views", "abc", "file.txt"), "checkout\n")

	report, err := ResetReviewStore(context.Background(), repo, StoreResetRequest{})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !report.Complete {
		t.Fatalf("reset reported incomplete: %#v", report)
	}
	if report.Operation != StoreResetApplied {
		t.Fatalf("operation = %q", report.Operation)
	}
	if report.RemovedFiles == 0 || report.RemovedBytes == 0 {
		t.Fatalf("reset removed nothing: %#v", report)
	}
	for _, name := range []string{"review-transactions/v2", "review-transactions/quarantine", "review-transactions/effect-markers", "candidate-views"} {
		if entry := storeResetEntryByName(t, report.Removable, name); !entry.Removed {
			t.Fatalf("category %q was not removed: %#v", name, entry)
		}
	}
	for _, path := range []string{
		filepath.Join(root, "review-transactions", "v2"),
		filepath.Join(root, "review-transactions", "quarantine"),
		filepath.Join(root, "candidate-views"),
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived the reset: %v", path, err)
		}
	}
}

// TestResetReviewStoreOverrideRemovesInFlightLineages proves the escape hatch
// works once the user asks for it explicitly.
func TestResetReviewStoreOverrideRemovesInFlightLineages(t *testing.T) {
	repo := initSnapshotRepo(t)
	root := storeResetGentleAIRoot(t, repo)
	seedStoreResetLineage(t, repo, "review-active", StateReviewing)

	report, err := ResetReviewStore(context.Background(), repo, StoreResetRequest{IncludeInFlight: true})
	if err != nil {
		t.Fatalf("override reset: %v", err)
	}
	if len(report.InFlight) != 1 {
		t.Fatalf("override report still has to name what it destroyed: %#v", report.InFlight)
	}
	if _, err := os.Lstat(filepath.Join(root, "review-transactions", "v2")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("override reset kept the in-flight lineage: %v", err)
	}
}

// TestResetReviewStorePreservesTheKillSwitchInBothLocations is the regression
// this whole allowlist exists for. The pre-#2882 mirror lives *inside*
// review-transactions/, so a reset that removed that directory wholesale would
// silently switch reviews back on for a user who turned them off.
func TestResetReviewStorePreservesTheKillSwitchInBothLocations(t *testing.T) {
	repo := initSnapshotRepo(t)
	current, legacy := seedStoreResetKillSwitch(t, repo)
	seedStoreResetLineage(t, repo, "review-approved", StateApproved)

	if _, err := ResetReviewStore(context.Background(), repo, StoreResetRequest{}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	for _, path := range []string{current, legacy} {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reset destroyed the kill switch at %s: %v", path, err)
		}
		if !strings.Contains(string(payload), `"mode":"off"`) {
			t.Fatalf("kill switch at %s changed: %s", path, payload)
		}
	}
}

// TestResetReviewStorePreservesEverythingItDoesNotOwn covers the rest of the
// exclusion list plus the allowlist rule: an unrecognized directory is reported
// and left alone, never guessed at.
func TestResetReviewStorePreservesEverythingItDoesNotOwn(t *testing.T) {
	repo := initSnapshotRepo(t)
	root := storeResetGentleAIRoot(t, repo)
	seedStoreResetLineage(t, repo, "review-approved", StateApproved)
	preserved := map[string]string{
		"sdd-runtime":      filepath.Join(root, "sdd-runtime", "v1", "some-change", "HEAD"),
		"defect-reports":   filepath.Join(root, "defect-reports", "operation-outcome-unknown-00ff.md"),
		"review-artifacts": filepath.Join(root, "review-artifacts", "issue-1107", "notes.md"),
		"incidents":        filepath.Join(root, "incidents", "1462", "notes.md"),
	}
	for _, path := range preserved {
		writeStoreResetFile(t, path, "keep me\n")
	}
	future := filepath.Join(root, "some-future-feature", "state.json")
	writeStoreResetFile(t, future, "{}\n")
	nestedFuture := filepath.Join(root, "review-transactions", "v3", "state.json")
	writeStoreResetFile(t, nestedFuture, "{}\n")

	report, err := ResetReviewStore(context.Background(), repo, StoreResetRequest{})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	for name, path := range preserved {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("reset removed preserved category %q: %v", name, err)
		}
		if entry := storeResetEntryByName(t, report.Preserved, name); entry.Reason == "" {
			t.Fatalf("preserved category %q carries no reason", name)
		}
	}
	for _, path := range []string{future, nestedFuture} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("reset removed an unrecognized path %s: %v", path, err)
		}
	}
	for _, name := range []string{"some-future-feature", "review-transactions/v3"} {
		if entry := storeResetEntryByName(t, report.Unrecognized, name); !entry.Present {
			t.Fatalf("unrecognized path %q was not reported", name)
		}
	}
}

// TestResetReviewStoreRemovesCandidateViewWorktreeAdminDirs covers the one
// category that is not just files. Candidate views are registered Git
// worktrees, so removing the checkout without its admin directory would leave
// the repository's worktree list pointing at nothing.
func TestResetReviewStoreRemovesCandidateViewWorktreeAdminDirs(t *testing.T) {
	repo := initSnapshotRepo(t)
	root := storeResetGentleAIRoot(t, repo)
	commonDir := filepath.Dir(root)
	view := filepath.Join(root, "candidate-views", "1a09d6f9")
	admin := filepath.Join(commonDir, "worktrees", "1a09d6f9")
	writeStoreResetFile(t, filepath.Join(view, "README.md"), "checkout\n")
	writeStoreResetFile(t, filepath.Join(view, ".git"), "gitdir: "+admin+"\n")
	writeStoreResetFile(t, filepath.Join(admin, "gitdir"), filepath.Join(view, ".git")+"\n")

	// A worktree that is not a candidate view must survive untouched, even
	// though it sits in the same admin directory.
	foreign := filepath.Join(commonDir, "worktrees", "alans-branch")
	writeStoreResetFile(t, filepath.Join(foreign, "gitdir"), filepath.Join(repo, "..", "alans-branch", ".git")+"\n")

	if _, err := ResetReviewStore(context.Background(), repo, StoreResetRequest{}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := os.Lstat(view); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate view survived: %v", err)
	}
	if _, err := os.Lstat(admin); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate view admin directory survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "gitdir")); err != nil {
		t.Fatalf("reset pruned an unrelated worktree: %v", err)
	}
}

// TestResetReviewStoreRemovesReadOnlyCandidateViews covers the shape the
// adapter actually writes: the checkout is chmod'd read-only, which makes a
// plain RemoveAll fail.
func TestResetReviewStoreRemovesReadOnlyCandidateViews(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores the permission bits this test depends on")
	}
	repo := initSnapshotRepo(t)
	root := storeResetGentleAIRoot(t, repo)
	view := filepath.Join(root, "candidate-views", "read-only-view")
	writeStoreResetFile(t, filepath.Join(view, "nested", "file.txt"), "frozen\n")
	for _, path := range []string{filepath.Join(view, "nested"), view} {
		if err := os.Chmod(path, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
	}

	report, err := ResetReviewStore(context.Background(), repo, StoreResetRequest{})
	if err != nil {
		t.Fatalf("reset over a read-only candidate view: %v", err)
	}
	if !report.Complete {
		t.Fatalf("reset reported incomplete: %#v", report.Removable)
	}
	if _, err := os.Lstat(view); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only candidate view survived: %v", err)
	}
}

// TestResetReviewStoreReportsPartialFailureHonestly is the reporting contract.
// When a category cannot be moved aside, the command must say so, must not
// count it as removed, and must not claim it succeeded.
func TestResetReviewStoreReportsPartialFailureHonestly(t *testing.T) {
	repo := initSnapshotRepo(t)
	root := storeResetGentleAIRoot(t, repo)
	seedStoreResetLineage(t, repo, "review-approved", StateApproved)
	writeStoreResetFile(t, filepath.Join(root, "reviews", "IDENTITY"), "{}\n")

	stubStoreResetRename(t, func(oldpath, newpath string) error {
		if filepath.Base(oldpath) == "reviews" {
			return errors.New("simulated rename failure")
		}
		return os.Rename(oldpath, newpath)
	})

	report, err := ResetReviewStore(context.Background(), repo, StoreResetRequest{})
	if err == nil {
		t.Fatal("a partial reset reported success")
	}
	if report.Complete {
		t.Fatalf("a partial reset reported Complete: %#v", report)
	}
	failed := storeResetEntryByName(t, report.Removable, "reviews")
	if failed.Removed {
		t.Fatalf("the failed category counted as removed: %#v", failed)
	}
	if failed.Skipped == "" || !strings.Contains(failed.Skipped, "simulated rename failure") {
		t.Fatalf("the failed category does not carry its reason: %#v", failed)
	}
	// The category that did work is still reported as removed, and the byte
	// count covers only what actually went away.
	removed := storeResetEntryByName(t, report.Removable, "review-transactions/v2")
	if !removed.Removed || report.RemovedBytes != removed.Bytes {
		t.Fatalf("partial reset byte accounting = %d, want %d", report.RemovedBytes, removed.Bytes)
	}
	if _, err := os.Stat(filepath.Join(root, "reviews", "IDENTITY")); err != nil {
		t.Fatalf("the failed category was destroyed anyway: %v", err)
	}
}

// TestResetReviewStoreLeavesNoStagingResidue proves the staged removal cleans
// up after itself, so a reset never trades one accumulating directory for
// another.
func TestResetReviewStoreLeavesNoStagingResidue(t *testing.T) {
	repo := initSnapshotRepo(t)
	root := storeResetGentleAIRoot(t, repo)
	seedStoreResetLineage(t, repo, "review-approved", StateApproved)

	if _, err := ResetReviewStore(context.Background(), repo, StoreResetRequest{}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), storeResetStagingPrefix) {
			t.Fatalf("reset left staging residue %q", entry.Name())
		}
	}
}
