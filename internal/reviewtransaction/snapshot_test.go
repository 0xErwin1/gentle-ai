package reviewtransaction

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSnapshotBuilderCurrentChangesIsCompleteAndPreservesRealIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("uses real git commands")
	}
	repo := initSnapshotRepo(t)
	writeSnapshotFile(t, repo, "tracked.txt", "unstaged\n")
	if err := os.Remove(filepath.Join(repo, "deleted.txt")); err != nil {
		t.Fatalf("Remove(deleted.txt): %v", err)
	}
	writeSnapshotFile(t, repo, "staged.txt", "staged\n")
	gitSnapshot(t, repo, "add", "--", "staged.txt")
	writeSnapshotFile(t, repo, "intended.txt", "intended\n")
	writeSnapshotFile(t, repo, "excluded.txt", "excluded\n")

	indexPath := strings.TrimSpace(gitSnapshot(t, repo, "rev-parse", "--git-path", "index"))
	beforeCached := gitSnapshot(t, repo, "diff", "--cached", "--binary")
	beforeIndex, err := os.ReadFile(filepath.Join(repo, indexPath))
	if err != nil {
		t.Fatalf("ReadFile(index): %v", err)
	}

	snapshot, err := (SnapshotBuilder{Repo: repo}).Build(context.Background(), Target{
		Kind:              TargetCurrentChanges,
		IntendedUntracked: []string{"intended.txt"},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	afterIndex, err := os.ReadFile(filepath.Join(repo, indexPath))
	if err != nil {
		t.Fatalf("ReadFile(index after): %v", err)
	}
	if !reflect.DeepEqual(afterIndex, beforeIndex) {
		t.Fatal("SnapshotBuilder mutated the user's real index")
	}
	if afterCached := gitSnapshot(t, repo, "diff", "--cached", "--binary"); afterCached != beforeCached {
		t.Fatalf("cached diff changed:\nbefore:\n%s\nafter:\n%s", beforeCached, afterCached)
	}

	wantPaths := []string{"deleted.txt", "intended.txt", "staged.txt", "tracked.txt"}
	if !reflect.DeepEqual(snapshot.Paths, wantPaths) {
		t.Fatalf("Paths = %v, want %v", snapshot.Paths, wantPaths)
	}
	for path, want := range map[string]string{
		"tracked.txt":  "unstaged\n",
		"staged.txt":   "staged\n",
		"intended.txt": "intended\n",
	} {
		if got := gitSnapshot(t, repo, "show", snapshot.CandidateTree+":"+path); got != want {
			t.Fatalf("candidate %s = %q, want %q", path, got, want)
		}
	}
	for _, absent := range []string{"deleted.txt", "excluded.txt"} {
		if gitSnapshotSucceeds(repo, "show", snapshot.CandidateTree+":"+absent) {
			t.Fatalf("candidate unexpectedly contains %s", absent)
		}
	}
	for name, value := range map[string]string{
		"base tree": snapshot.BaseTree, "candidate tree": snapshot.CandidateTree,
		"paths digest": snapshot.PathsDigest, "untracked proof": snapshot.IntendedUntrackedProof,
		"identity": snapshot.Identity,
	} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("%s is empty", name)
		}
	}

	again, err := (SnapshotBuilder{Repo: repo}).Build(context.Background(), Target{
		Kind:              TargetCurrentChanges,
		IntendedUntracked: []string{"intended.txt"},
	})
	if err != nil {
		t.Fatalf("Build(repeat) error = %v", err)
	}
	if again.Identity != snapshot.Identity || again.CandidateTree != snapshot.CandidateTree {
		t.Fatalf("snapshot is not deterministic: first=%#v second=%#v", snapshot, again)
	}
}

func TestSnapshotBuilderRequiresExplicitIntendedUntrackedAndLedgerBinding(t *testing.T) {
	if testing.Short() {
		t.Skip("uses real git commands")
	}
	repo := initSnapshotRepo(t)
	builder := SnapshotBuilder{Repo: repo}
	if _, err := builder.Build(context.Background(), Target{Kind: TargetCurrentChanges}); err == nil {
		t.Fatal("Build() accepted current changes without an explicit intended-untracked list")
	}
	baseTree := strings.TrimSpace(gitSnapshot(t, repo, "rev-parse", "HEAD^{tree}"))
	if _, err := builder.Build(context.Background(), Target{Kind: TargetFixDiff, BaseRef: baseTree, IntendedUntracked: []string{}}); err == nil {
		t.Fatal("Build() accepted fix diff without ledger IDs")
	}
	if _, err := builder.Build(context.Background(), Target{
		Kind: TargetFixDiff, BaseRef: baseTree, IntendedUntracked: []string{}, LedgerIDs: []string{"R1-001"},
	}); err != nil {
		t.Fatalf("Build(valid fix diff) error = %v", err)
	}
}

func TestSnapshotBuilderSupportsBaseDiffAndExactCommitRange(t *testing.T) {
	if testing.Short() {
		t.Skip("uses real git commands")
	}
	repo := initSnapshotRepo(t)
	firstCommit := strings.TrimSpace(gitSnapshot(t, repo, "rev-parse", "HEAD"))
	writeSnapshotFile(t, repo, "tracked.txt", "second\n")
	gitSnapshot(t, repo, "add", "--", "tracked.txt")
	gitSnapshot(t, repo, "commit", "-m", "second")
	secondCommit := strings.TrimSpace(gitSnapshot(t, repo, "rev-parse", "HEAD"))

	builder := SnapshotBuilder{Repo: repo}
	baseDiff, err := builder.Build(context.Background(), Target{Kind: TargetBaseDiff, BaseRef: firstCommit})
	if err != nil {
		t.Fatalf("Build(base diff) error = %v", err)
	}
	exact, err := builder.Build(context.Background(), Target{Kind: TargetExactRevision, Revision: firstCommit + ".." + secondCommit})
	if err != nil {
		t.Fatalf("Build(exact range) error = %v", err)
	}
	if baseDiff.BaseTree != exact.BaseTree || baseDiff.CandidateTree != exact.CandidateTree {
		t.Fatalf("base diff and exact range disagree: base=%#v exact=%#v", baseDiff, exact)
	}
}

func TestSnapshotBuilderExactRevisionIgnoresReplacementObjects(t *testing.T) {
	if testing.Short() {
		t.Skip("uses real git commands")
	}
	repo := initSnapshotRepo(t)
	firstCommit := strings.TrimSpace(gitSnapshot(t, repo, "rev-parse", "HEAD"))
	writeSnapshotFile(t, repo, "tracked.txt", "original\n")
	gitSnapshot(t, repo, "add", "--", "tracked.txt")
	gitSnapshot(t, repo, "commit", "-m", "original")
	originalCommit := strings.TrimSpace(gitSnapshot(t, repo, "rev-parse", "HEAD"))
	target := Target{Kind: TargetExactRevision, Revision: originalCommit}
	baseline, err := (SnapshotBuilder{Repo: repo}).Build(context.Background(), target)
	if err != nil {
		t.Fatalf("Build(baseline) error = %v", err)
	}

	gitSnapshot(t, repo, "checkout", "--detach", firstCommit)
	writeSnapshotFile(t, repo, "tracked.txt", "replacement\n")
	gitSnapshot(t, repo, "add", "--", "tracked.txt")
	gitSnapshot(t, repo, "commit", "-m", "replacement")
	replacementCommit := strings.TrimSpace(gitSnapshot(t, repo, "rev-parse", "HEAD"))
	gitSnapshot(t, repo, "replace", originalCommit, replacementCommit)

	snapshot, err := (SnapshotBuilder{Repo: repo}).Build(context.Background(), target)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if snapshot.Identity != baseline.Identity {
		t.Fatalf("Identity = %q, want replacement-independent identity %q", snapshot.Identity, baseline.Identity)
	}
	if snapshot.BaseTree != strings.TrimSpace(gitSnapshot(t, repo, "--no-replace-objects", "rev-parse", firstCommit+"^{tree}")) {
		t.Fatalf("BaseTree = %q, want the original parent tree", snapshot.BaseTree)
	}
	if snapshot.CandidateTree != strings.TrimSpace(gitSnapshot(t, repo, "--no-replace-objects", "rev-parse", originalCommit+"^{tree}")) {
		t.Fatalf("CandidateTree = %q, want the original commit tree", snapshot.CandidateTree)
	}
}

func initSnapshotRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitSnapshot(t, repo, "init")
	gitSnapshot(t, repo, "config", "user.email", "snapshot@example.com")
	gitSnapshot(t, repo, "config", "user.name", "Snapshot Test")
	writeSnapshotFile(t, repo, "tracked.txt", "base\n")
	writeSnapshotFile(t, repo, "deleted.txt", "delete me\n")
	gitSnapshot(t, repo, "add", "--", "tracked.txt", "deleted.txt")
	gitSnapshot(t, repo, "commit", "-m", "base")
	return repo
}

func writeSnapshotFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func gitSnapshot(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func gitSnapshotSucceeds(repo string, args ...string) bool {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	return cmd.Run() == nil
}
