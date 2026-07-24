package workprovider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
)

func TestFixedPADGitObjectAuthorityProvesCommitTreeAndAncestry(
	t *testing.T,
) {
	fixture := newFixedPADGitFixture(t)
	authority, err := NewPADRepositoryAuthority(
		context.Background(),
		fixture.root,
	)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := NewFixedPADGitObjectAuthority(
		context.Background(),
		authority,
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := objects.RequireCommit(
		context.Background(),
		authority.RepositoryRef(),
		fixture.headRevision,
	); err != nil {
		t.Fatalf("require exact candidate commit: %v", err)
	}
	if err := objects.RequireCommitTree(
		context.Background(),
		authority.RepositoryRef(),
		fixture.headRevision,
		fixture.headTree,
	); err != nil {
		t.Fatalf("require exact candidate commit/tree: %v", err)
	}
	runFixedPADTestGit(
		t,
		fixture.root,
		"checkout",
		"--quiet",
		"--detach",
		strings.TrimPrefix(fixture.baseRevision, "git:"),
	)
	if err := objects.RequireCommitTree(
		context.Background(),
		authority.RepositoryRef(),
		fixture.headRevision,
		fixture.headTree,
	); err != nil {
		t.Fatalf("exact commit proof depended on ambient HEAD: %v", err)
	}
	if err := objects.RequireCommitTree(
		context.Background(),
		authority.RepositoryRef(),
		fixture.headRevision,
		fixture.baseTree,
	); !errors.Is(err, ErrPADGitObjectMismatch) {
		t.Fatalf("tree mismatch error = %v", err)
	}
	if err := objects.RequireCommit(
		context.Background(),
		authority.RepositoryRef(),
		"git:"+fixture.blob,
	); !errors.Is(err, ErrPADGitObjectMismatch) {
		t.Fatalf("blob-as-commit error = %v", err)
	}

	forward, err := objects.ObserveAncestry(
		context.Background(),
		authority.RepositoryRef(),
		fixture.baseRevision,
		fixture.headRevision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if forward.State != PADGitAncestor ||
		!validPADImmutableRef(forward.EvidenceRef) {
		t.Fatalf("forward ancestry = %#v", forward)
	}
	replayed, err := objects.ObserveAncestry(
		context.Background(),
		authority.RepositoryRef(),
		fixture.baseRevision,
		fixture.headRevision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != forward {
		t.Fatalf("ancestry evidence changed: %#v != %#v", replayed, forward)
	}
	reverse, err := objects.ObserveAncestry(
		context.Background(),
		authority.RepositoryRef(),
		fixture.headRevision,
		fixture.baseRevision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reverse.State != PADGitNotAncestor ||
		reverse.EvidenceRef == forward.EvidenceRef {
		t.Fatalf("reverse ancestry = %#v", reverse)
	}
}

func TestFixedPADGitObjectAuthorityFailsClosedOnIdentityAndContext(
	t *testing.T,
) {
	fixture := newFixedPADGitFixture(t)
	authority, err := NewPADRepositoryAuthority(
		context.Background(),
		fixture.root,
	)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := NewFixedPADGitObjectAuthority(
		context.Background(),
		authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	foreign := newFixedPADGitFixture(t)
	foreignAuthority, err := NewPADRepositoryAuthority(
		context.Background(),
		foreign.root,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.RequireCommit(
		context.Background(),
		foreignAuthority.RepositoryRef(),
		fixture.headRevision,
	); !errors.Is(err, ErrPADRepositoryAuthorityMismatch) {
		t.Fatalf("foreign repository error = %v", err)
	}
	if err := objects.RequireCommit(
		context.Background(),
		authority.RepositoryRef(),
		"git:"+strings.ToUpper(strings.TrimPrefix(
			fixture.headRevision,
			"git:",
		)),
	); !errors.Is(err, ErrPADGitObjectMismatch) {
		t.Fatalf("uppercase object error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := objects.RequireCommit(
		cancelled,
		authority.RepositoryRef(),
		fixture.headRevision,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v", err)
	}
	objects.timeout = time.Nanosecond
	if err := objects.RequireCommit(
		context.Background(),
		authority.RepositoryRef(),
		fixture.headRevision,
	); !errors.Is(err, ErrPADGitObjectUnavailable) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded operation error = %v", err)
	}
}

func TestFixedPADGitObjectAuthorityRejectsRepositoryMetadataSymlinkReplacement(
	t *testing.T,
) {
	fixture := newFixedPADGitFixture(t)
	authority, err := NewPADRepositoryAuthority(
		context.Background(),
		fixture.root,
	)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := NewFixedPADGitObjectAuthority(
		context.Background(),
		authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	gitDirectory := filepath.Join(fixture.root, ".git")
	retained := filepath.Join(fixture.root, ".git-retained")
	if err := os.Rename(gitDirectory, retained); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(retained, gitDirectory); err != nil {
		if restoreErr := os.Rename(retained, gitDirectory); restoreErr != nil {
			t.Fatalf("symlink unsupported (%v) and restore failed: %v", err, restoreErr)
		}
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	defer func() {
		_ = os.Remove(gitDirectory)
		_ = os.Rename(retained, gitDirectory)
	}()
	if err := objects.RequireCommit(
		context.Background(),
		authority.RepositoryRef(),
		fixture.headRevision,
	); !errors.Is(err, ErrPADRepositoryAuthorityMismatch) {
		t.Fatalf("replaced Git metadata error = %v", err)
	}
}

func TestFixedPADGitObjectIdentityAcceptsFullSHA1AndSHA256(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		value  string
		accept bool
	}{
		{name: "sha1", value: strings.Repeat("a", 40), accept: true},
		{name: "sha256", value: strings.Repeat("b", 64), accept: true},
		{name: "uppercase", value: strings.Repeat("A", 40), accept: false},
		{name: "abbreviated", value: strings.Repeat("a", 12), accept: false},
		{name: "non hex", value: strings.Repeat("g", 40), accept: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := validPADGitObjectID(test.value); got != test.accept {
				t.Fatalf("validPADGitObjectID() = %t, want %t", got, test.accept)
			}
		})
	}
}

func TestFixedPADGitObjectAuthorityBindsFullOIDToRepositoryFormat(
	t *testing.T,
) {
	t.Run("sha1", func(t *testing.T) {
		fixture := newFixedPADGitFixtureWithObjectFormat(t, "sha1")
		authority, err := NewPADRepositoryAuthority(
			context.Background(),
			fixture.root,
		)
		if err != nil {
			t.Fatal(err)
		}
		objects, err := NewFixedPADGitObjectAuthority(
			context.Background(),
			authority,
		)
		if err != nil {
			t.Fatal(err)
		}
		if objects.objectFormat != "sha1" || objects.objectIDLength != 40 {
			t.Fatalf(
				"SHA-1 authority format/length = %q/%d",
				objects.objectFormat,
				objects.objectIDLength,
			)
		}
		if objects.validObjectID(strings.Repeat("a", 64)) {
			t.Fatal("SHA-1 authority accepted a 64-character object identity")
		}
	})

	t.Run("sha256 rejects SHA-1-length abbreviation", func(t *testing.T) {
		fixture := newFixedPADGitFixtureWithObjectFormat(t, "sha256")
		if len(strings.TrimPrefix(fixture.headRevision, "git:")) != 64 ||
			len(fixture.headTree) != 64 {
			t.Fatalf("SHA-256 fixture = %#v", fixture)
		}
		authority, err := NewPADRepositoryAuthority(
			context.Background(),
			fixture.root,
		)
		if err != nil {
			t.Fatal(err)
		}
		objects, err := NewFixedPADGitObjectAuthority(
			context.Background(),
			authority,
		)
		if err != nil {
			t.Fatal(err)
		}
		if objects.objectFormat != "sha256" ||
			objects.objectIDLength != 64 {
			t.Fatalf(
				"SHA-256 authority format/length = %q/%d",
				objects.objectFormat,
				objects.objectIDLength,
			)
		}
		full := strings.TrimPrefix(fixture.headRevision, "git:")
		abbreviation := full[:40]
		if got := runFixedPADTestGit(
			t,
			fixture.root,
			"cat-file",
			"-t",
			abbreviation,
		); got != "commit" {
			t.Fatalf("control abbreviation did not resolve: %q", got)
		}
		if err := objects.RequireCommit(
			context.Background(),
			authority.RepositoryRef(),
			"git:"+abbreviation,
		); !errors.Is(err, ErrPADGitObjectMismatch) {
			t.Fatalf("SHA-256 abbreviation error = %v", err)
		}
		if err := objects.RequireCommitTree(
			context.Background(),
			authority.RepositoryRef(),
			fixture.headRevision,
			fixture.headTree,
		); err != nil {
			t.Fatalf("full SHA-256 commit/tree proof: %v", err)
		}

		catalog, err := NewPADCandidateCatalog(
			context.Background(),
			authority,
			objects,
		)
		if err != nil {
			t.Fatal(err)
		}
		binding := fixedPADCandidateBinding(
			authority.RepositoryRef(),
			fixture.baseRevision,
			fixture.headRevision,
			deliveryadmission.MechanismFastForwardOnly,
		)
		abbreviated := binding
		abbreviated.CandidateRevision = "git:" + abbreviation
		if _, err := catalog.BindCandidate(
			context.Background(),
			fixture.headTree,
			abbreviated,
		); !errors.Is(err, ErrPADGitObjectMismatch) {
			t.Fatalf("catalog abbreviation error = %v", err)
		}
		if _, err := catalog.BindCandidate(
			context.Background(),
			fixture.headTree[:40],
			binding,
		); !errors.Is(err, ErrPADGitObjectMismatch) {
			t.Fatalf("catalog abbreviated tree error = %v", err)
		}
		if _, err := catalog.BindCandidate(
			context.Background(),
			fixture.headTree,
			binding,
		); err != nil {
			t.Fatalf("catalog full SHA-256 binding: %v", err)
		}
	})
}

func TestFixedPADGitObjectAuthorityDisablesPromisorLazyFetch(
	t *testing.T,
) {
	source := newFixedPADGitFixture(t)
	target := filepath.Join(t.TempDir(), "partial")
	runFixedPADTestGit(
		t,
		"",
		"init",
		"--quiet",
		"--initial-branch=main",
		target,
	)
	runFixedPADTestGit(t, target, "remote", "add", "origin", source.root)
	runFixedPADTestGit(t, target, "config", "remote.origin.promisor", "true")
	runFixedPADTestGit(
		t,
		target,
		"config",
		"remote.origin.partialclonefilter",
		"blob:none",
	)
	runFixedPADTestGit(
		t,
		target,
		"config",
		"extensions.partialClone",
		"origin",
	)
	authority, err := NewPADRepositoryAuthority(
		context.Background(),
		target,
	)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := NewFixedPADGitObjectAuthority(
		context.Background(),
		authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	before := fixedPADTestObjectFiles(t, target)
	if err := objects.RequireCommit(
		context.Background(),
		authority.RepositoryRef(),
		source.headRevision,
	); !errors.Is(err, ErrPADGitObjectMismatch) {
		t.Fatalf("missing promisor commit error = %v", err)
	}
	after := fixedPADTestObjectFiles(t, target)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf(
			"read-only proof lazy-fetched or mutated objects:\nbefore=%v\nafter=%v",
			before,
			after,
		)
	}
}

func TestFixedPADGitEnvironmentDropsAmbientGitControl(t *testing.T) {
	t.Parallel()
	environment := fixedPADGitEnvironment([]string{
		"PATH=/bin",
		"GIT_DIR=/tmp/foreign",
		"git_work_tree=/tmp/worktree",
		"GIT_CONFIG_COUNT=9",
		"GIT_NO_LAZY_FETCH=0",
		"LANG=es_ES.UTF-8",
		"LC_ALL=es_ES.UTF-8",
	})
	joined := "\n" + strings.Join(environment, "\n") + "\n"
	for _, forbidden := range []string{
		"\nGIT_DIR=",
		"\ngit_work_tree=",
		"\nGIT_CONFIG_COUNT=9\n",
		"\nGIT_NO_LAZY_FETCH=0\n",
		"\nLANG=es_ES.UTF-8\n",
		"\nLC_ALL=es_ES.UTF-8\n",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("sanitized environment retained %q: %q", forbidden, joined)
		}
	}
	for _, required := range []string{
		"\nPATH=/bin\n",
		"\nGIT_CONFIG_NOSYSTEM=1\n",
		"\nGIT_CONFIG_COUNT=0\n",
		"\nGIT_NO_LAZY_FETCH=1\n",
		"\nGIT_TERMINAL_PROMPT=0\n",
		"\nLANG=C\n",
		"\nLC_ALL=C\n",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("sanitized environment lacks %q: %q", required, joined)
		}
	}
}

type fixedPADGitFixture struct {
	root         string
	baseRevision string
	baseTree     string
	headRevision string
	headTree     string
	blob         string
}

func newFixedPADGitFixture(t *testing.T) fixedPADGitFixture {
	t.Helper()
	return newFixedPADGitFixtureWithObjectFormat(t, "sha1")
}

func newFixedPADGitFixtureWithObjectFormat(
	t *testing.T,
	objectFormat string,
) fixedPADGitFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	runFixedPADTestGit(
		t,
		"",
		"init",
		"--quiet",
		"--initial-branch=main",
		"--object-format="+objectFormat,
		root,
	)
	runFixedPADTestGit(t, root, "config", "user.name", "PAD Test")
	runFixedPADTestGit(t, root, "config", "user.email", "pad@example.com")
	path := filepath.Join(root, "candidate.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runFixedPADTestGit(t, root, "add", "candidate.txt")
	runFixedPADTestGit(t, root, "commit", "--quiet", "-m", "base")
	base := "git:" + runFixedPADTestGit(t, root, "rev-parse", "HEAD")
	baseTree := runFixedPADTestGit(t, root, "rev-parse", "HEAD^{tree}")
	if err := os.WriteFile(path, []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runFixedPADTestGit(t, root, "add", "candidate.txt")
	runFixedPADTestGit(t, root, "commit", "--quiet", "-m", "candidate")
	head := "git:" + runFixedPADTestGit(t, root, "rev-parse", "HEAD")
	return fixedPADGitFixture{
		root:         root,
		baseRevision: base,
		baseTree:     baseTree,
		headRevision: head,
		headTree:     runFixedPADTestGit(t, root, "rev-parse", "HEAD^{tree}"),
		blob: runFixedPADTestGit(
			t,
			root,
			"rev-parse",
			"HEAD:candidate.txt",
		),
	}
}

func fixedPADTestObjectFiles(t *testing.T, repository string) []string {
	t.Helper()
	root := filepath.Join(repository, ".git", "objects")
	var files []string
	err := filepath.WalkDir(root, func(
		path string,
		entry os.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

func runFixedPADTestGit(
	t *testing.T,
	repository string,
	args ...string,
) string {
	t.Helper()
	commandArgs := append([]string{}, args...)
	if repository != "" {
		commandArgs = append([]string{"-C", repository}, commandArgs...)
	}
	command := exec.Command("git", commandArgs...)
	command.Env = append(
		os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"LC_ALL=C",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(commandArgs, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
