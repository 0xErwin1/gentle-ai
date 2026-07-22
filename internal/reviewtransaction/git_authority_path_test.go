package reviewtransaction

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalGitDirectoryRejectsMalformedRecords(t *testing.T) {
	repo := t.TempDir()
	gitDir := filepath.Join(repo, ".git")
	if err := os.Mkdir(gitDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	tests := []struct {
		name    string
		output  []byte
		want    string
		wantErr bool
	}{
		{name: "relative directory", output: []byte(".git\n"), want: gitDir},
		{name: "absolute linked worktree common directory", output: []byte(outside + "\n"), want: outside},
		{name: "unsupported option echo", output: []byte("--path-format=absolute\n"), wantErr: true},
		{name: "option echo plus path", output: []byte("--path-format=absolute\n.git\n"), wantErr: true},
		{name: "multiple paths", output: []byte(".git\nother\n"), wantErr: true},
		{name: "empty", output: []byte("\n"), wantErr: true},
		{name: "nul", output: []byte(".git\x00outside\n"), wantErr: true},
		{name: "relative escape", output: []byte("../" + filepath.Base(outside) + "\n"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canonicalGitDirectory(repo, tt.output)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("canonicalGitDirectory() = %q, want error", got)
				}
				return
			}
			if err != nil || got != filepath.Clean(tt.want) {
				t.Fatalf("canonicalGitDirectory() = %q, %v; want %q", got, err, filepath.Clean(tt.want))
			}
		})
	}
}

func TestGitAuthorityResolversDoNotUsePathFormatAbsolute(t *testing.T) {
	requireSnapshotGit(t)
	repo := initSnapshotRepo(t)
	original := gitCommandContext
	var forbidden [][]string
	gitCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		for _, arg := range args {
			if arg == "--path-format=absolute" {
				forbidden = append(forbidden, append([]string(nil), args...))
				break
			}
		}
		return original(ctx, name, args...)
	}
	t.Cleanup(func() { gitCommandContext = original })

	if _, _, err := reviewAuthorityRoot(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	isolation, cleanup, err := isolatedImmutableTreeGit(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(isolation) == 0 {
		t.Fatal("isolatedImmutableTreeGit() returned no environment")
	}
	cleanup()
	if _, err := reviewRepositoryIdentity(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	if len(forbidden) != 0 {
		t.Fatalf("runtime Git authority resolution used --path-format=absolute: %v", forbidden)
	}
}

func TestReviewAuthorityRootRejectsLegacyOptionEchoWithoutMutation(t *testing.T) {
	requireSnapshotGit(t)
	repo := initSnapshotRepo(t)
	original := gitCommandContext
	t.Setenv("GENTLE_AI_TEST_GIT_PATH_OUTPUT", base64.StdEncoding.EncodeToString([]byte("--path-format=absolute\n.git\n")))
	gitCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if slicesContain(args, "--git-common-dir") {
			return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGitAuthorityPathHelperProcess$")
		}
		return original(ctx, name, args...)
	}
	t.Cleanup(func() { gitCommandContext = original })

	if _, _, err := reviewAuthorityRoot(context.Background(), repo); err == nil {
		t.Fatal("reviewAuthorityRoot() accepted legacy option echo")
	}
	for _, path := range []string{
		filepath.Join(repo, "--path-format=absolute\n.git"),
		filepath.Join(repo, "gentle-ai"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("malformed Git output created %q", path)
		}
	}
}

func TestResolveRepositoryRootRejectsUnrelatedAbsoluteOutput(t *testing.T) {
	requireSnapshotGit(t)
	repo := initSnapshotRepo(t)
	outside := t.TempDir()
	original := gitCommandContext
	t.Setenv("GENTLE_AI_TEST_GIT_PATH_OUTPUT", base64.StdEncoding.EncodeToString([]byte(outside+"\n")))
	gitCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if slicesContain(args, "--show-toplevel") {
			return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGitAuthorityPathHelperProcess$")
		}
		return original(ctx, name, args...)
	}
	t.Cleanup(func() { gitCommandContext = original })

	if root, err := (SnapshotBuilder{Repo: repo}).ResolveRepositoryRoot(context.Background()); err == nil {
		t.Fatalf("ResolveRepositoryRoot() = %q, want unrelated-root error", root)
	}
}

func TestReviewAuthorityRootPreservesSymlinkedGitDirectory(t *testing.T) {
	requireSnapshotGit(t)
	repo := initSnapshotRepo(t)
	externalGitDir := filepath.Join(t.TempDir(), "git-dir")
	if err := os.Rename(filepath.Join(repo, ".git"), externalGitDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalGitDir, filepath.Join(repo, ".git")); err != nil {
		t.Skipf("symlinked Git directories are unavailable: %v", err)
	}

	root, resolvedRepo, err := reviewAuthorityRoot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(externalGitDir, "gentle-ai", "review-transactions")
	if root != want || resolvedRepo != repo {
		t.Fatalf("reviewAuthorityRoot() = %q, %q; want %q, %q", root, resolvedRepo, want, repo)
	}
}

func TestReviewRepositoryIdentityPreservesGitCommandError(t *testing.T) {
	requireSnapshotGit(t)
	repo := initSnapshotRepo(t)
	original := gitCommandContext
	showTopLevelCalls := 0
	t.Setenv("GENTLE_AI_TEST_GIT_PATH_FAIL", "1")
	gitCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if slicesContain(args, "--show-toplevel") {
			showTopLevelCalls++
			if showTopLevelCalls == 2 {
				return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGitAuthorityPathHelperProcess$")
			}
		}
		return original(ctx, name, args...)
	}
	t.Cleanup(func() { gitCommandContext = original })

	_, err := reviewRepositoryIdentity(context.Background(), repo)
	var commandErr *GitCommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("reviewRepositoryIdentity() error = %T %v, want *GitCommandError", err, err)
	}
}

func TestGitAuthorityPathHelperProcess(t *testing.T) {
	if os.Getenv("GENTLE_AI_TEST_GIT_PATH_FAIL") == "1" {
		os.Exit(23)
	}
	encoded := os.Getenv("GENTLE_AI_TEST_GIT_PATH_OUTPUT")
	if encoded == "" {
		return
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		os.Exit(2)
	}
	_, _ = os.Stdout.Write(payload)
	os.Exit(0)
}

func slicesContain(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
