package mutationintegrity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

func TestCompleteObservesExactIntendedMutationAndPersists(t *testing.T) {
	repo := initRepository(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := Complete(
		context.Background(),
		lease,
		CompleteRequest{
			WorkRunID: "work-readme", Route: "direct_inline",
			ScopeDigest: digest("readme scope"),
			Target: reviewtransaction.Target{
				Kind:              reviewtransaction.TargetCurrentChanges,
				IntendedUntracked: []string{},
			},
			IntendedPaths: []string{"README.md"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Snapshot.Paths[0] != "README.md" ||
		len(completion.Readbacks) != 1 ||
		completion.Readbacks[0].Kind != StructuralRegular ||
		completion.Readbacks[0].ContentSHA256 != digest("changed\n") {
		t.Fatalf("completion = %#v", completion)
	}
	store, err := OpenStore(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(context.Background(), completion)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.Read(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.CompletionRef != completion.CompletionRef ||
		!reviewtransaction.SnapshotsEqualExact(replayed.Snapshot, completion.Snapshot) {
		t.Fatalf("replayed completion mismatch: %#v", replayed)
	}
}

func TestCompleteRejectsScopeExpansion(t *testing.T) {
	repo := initRepository(t)
	for name, body := range map[string]string{
		"README.md": "changed\n",
		"extra.txt": "unexpected\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Complete(
		context.Background(),
		lease,
		CompleteRequest{
			WorkRunID: "work-scope", Route: "delegated_direct",
			ScopeDigest: digest("scope"),
			Target: reviewtransaction.Target{
				Kind:              reviewtransaction.TargetCurrentChanges,
				IntendedUntracked: []string{"extra.txt"},
			},
			IntendedPaths: []string{"README.md"},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "differ from admitted scope") {
		t.Fatalf("Complete() error = %v", err)
	}
}

func TestCompleteRecordsDeletionAsStructuralAbsence(t *testing.T) {
	repo := initRepository(t)
	if err := os.Remove(filepath.Join(repo, "README.md")); err != nil {
		t.Fatal(err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := Complete(
		context.Background(),
		lease,
		CompleteRequest{
			WorkRunID: "work-delete", Route: "direct_inline",
			ScopeDigest: digest("delete scope"),
			Target: reviewtransaction.Target{
				Kind:              reviewtransaction.TargetCurrentChanges,
				IntendedUntracked: []string{},
			},
			IntendedPaths: []string{"README.md"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := completion.Readbacks[0]; got.Kind != StructuralAbsent ||
		got.Mode != 0 || got.ContentSHA256 != "" {
		t.Fatalf("deletion readback = %#v", got)
	}
}

func TestCompleteRejectsSymlinkReadback(t *testing.T) {
	repo := initRepository(t)
	if err := os.Remove(filepath.Join(repo, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.md", filepath.Join(repo, "README.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "target.md"), []byte("target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Complete(
		context.Background(),
		lease,
		CompleteRequest{
			WorkRunID: "work-link", Route: "direct_inline",
			ScopeDigest: digest("link scope"),
			Target: reviewtransaction.Target{
				Kind:              reviewtransaction.TargetCurrentChanges,
				IntendedUntracked: []string{"target.md"},
			},
			IntendedPaths: []string{"README.md", "target.md"},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "regular file or absent") {
		t.Fatalf("Complete() error = %v", err)
	}
}

func TestStoreRejectsTamperedCompletion(t *testing.T) {
	repo := initRepository(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := Complete(
		context.Background(),
		lease,
		CompleteRequest{
			WorkRunID: "work-tamper", Route: "direct_inline",
			ScopeDigest: digest("scope"),
			Target: reviewtransaction.Target{
				Kind:              reviewtransaction.TargetCurrentChanges,
				IntendedUntracked: []string{},
			},
			IntendedPaths: []string{"README.md"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), completion); err != nil {
		t.Fatal(err)
	}
	path, err := store.objectPath(completion.CompletionRef)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	payload = []byte(strings.Replace(
		string(payload),
		`"route":"direct_inline"`,
		`"route":"delegated_direct"`,
		1,
	))
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(context.Background(), completion.CompletionRef); err == nil {
		t.Fatal("Read() accepted tampered completion")
	}
}

func initRepository(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "baseline")
	return repo
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	command.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "missing-gitconfig"),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func digest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(hash[:])
}
