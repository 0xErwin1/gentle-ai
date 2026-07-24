package reviewtransaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
)

type rarPlanFixture struct {
	repo        string
	repository  *RARAuthorityRepository
	publication RARPlanPublication
}

func TestRARPlanAuthorityPublishesResolvesAndReplaysExactLivePlan(t *testing.T) {
	fixture := newRARPlanFixture(t, "plan-exact")
	if _, err := os.Lstat(fixture.repository.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("RAR root exists before plan publication: %v", err)
	}

	first, err := fixture.repository.PublishPlan(context.Background(), fixture.publication)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := fixture.repository.PublishPlan(context.Background(), fixture.publication)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := fixture.repository.ResolvePlan(context.Background(), first.AuthorityRef)
	if err != nil {
		t.Fatal(err)
	}
	if !rarPlanAuthoritiesEqual(first, replay) || !rarPlanAuthoritiesEqual(first, resolved) {
		t.Fatalf("plan authority diverged:\nfirst=%#v\nreplay=%#v\nresolved=%#v", first, replay, resolved)
	}
	if first.AuthorityRef == first.Plan.Digest ||
		first.RepositoryIdentity != fixture.repository.identity.RepositoryIdentity ||
		first.Subject != first.Applicability.Subject ||
		first.Subject != first.Plan.Subject ||
		!SnapshotsEqualExact(first.Snapshot, fixture.publication.Snapshot) {
		t.Fatalf("published plan authority does not preserve its exact preimages: %#v", first)
	}

	objectPath := fixture.repository.planObjectPath(first.AuthorityRef)
	info, err := os.Lstat(objectPath)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("plan object = %#v, %v", info, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("plan object mode = %o, want 600", info.Mode().Perm())
	}
	for _, path := range []string{fixture.repository.root, fixture.repository.planObjectsRoot()} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("plan directory %q = %#v, %v", path, info, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Fatalf("plan directory %q mode = %o, want 700", path, info.Mode().Perm())
		}
	}

	nested := filepath.Join(fixture.repo, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	fromNested, err := OpenRARAuthorityRepository(context.Background(), nested)
	if err != nil {
		t.Fatal(err)
	}
	nestedResolved, err := fromNested.ResolvePlan(context.Background(), first.AuthorityRef)
	if err != nil || !rarPlanAuthoritiesEqual(nestedResolved, first) {
		t.Fatalf("nested resolution = %#v, %v", nestedResolved, err)
	}
}

func TestRARPlanAuthorityConcurrentExactReplayConverges(t *testing.T) {
	fixture := newRARPlanFixture(t, "plan-concurrent")
	const publishers = 8
	results := make(chan RARPlanAuthority, publishers)
	errs := make(chan error, publishers)
	var group sync.WaitGroup
	for range publishers {
		group.Add(1)
		go func() {
			defer group.Done()
			authority, err := fixture.repository.PublishPlan(context.Background(), fixture.publication)
			results <- authority
			errs <- err
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent PublishPlan() error = %v", err)
		}
	}
	var first *RARPlanAuthority
	for authority := range results {
		if first == nil {
			copy := authority
			first = &copy
			continue
		}
		if !rarPlanAuthoritiesEqual(*first, authority) {
			t.Errorf("concurrent plan publication diverged:\nfirst=%#v\nother=%#v", *first, authority)
		}
	}
}

func TestRARAuthorityRepositoryRejectsReplacedGitIdentityWithoutPublishing(
	t *testing.T,
) {
	t.Parallel()

	fixture := newRARPlanFixture(t, "plan-git-identity-swap")
	authority, err := fixture.repository.PublishPlan(
		context.Background(),
		fixture.publication,
	)
	if err != nil {
		t.Fatal(err)
	}
	originalGit := filepath.Join(fixture.repo, ".git")
	retiredGit := filepath.Join(fixture.repo, ".git-retired")
	if err := os.Rename(originalGit, retiredGit); err != nil {
		t.Skipf("cannot replace Git control directory on this platform: %v", err)
	}
	if err := runSnapshotGit(fixture.repo, "init"); err != nil {
		t.Fatalf("reinitialize Git repository: %v", err)
	}

	if _, err := fixture.repository.ResolvePlan(
		context.Background(),
		authority.AuthorityRef,
	); !errors.Is(err, ErrRepositoryIdentityChanged) {
		t.Fatalf("ResolvePlan() after Git identity replacement error = %v", err)
	}
	replacementRoot := filepath.Join(
		originalGit,
		"gentle-ai",
		"review-transactions",
		rarAuthorityDirectory,
		rarAuthorityVersion,
	)
	if _, err := os.Lstat(replacementRoot); !os.IsNotExist(err) {
		t.Fatalf(
			"stale RAR handle published under replacement Git identity: %v",
			err,
		)
	}
}

func TestRARPlanAuthorityDetectsConflictCorruptionAndDurabilityFailure(t *testing.T) {
	t.Run("occupied content address conflicts", func(t *testing.T) {
		fixture := newRARPlanFixture(t, "plan-conflict")
		authority := fixture.planAuthority(t)
		if err := ensureRARRepositoryRoot(fixture.repository.identity.GitCommonDir, fixture.repository.root, true); err != nil {
			t.Fatal(err)
		}
		if err := ensurePrivateRARDirectoryTree(fixture.repository.root, fixture.repository.planObjectsRoot(), true); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture.repository.planObjectPath(authority.AuthorityRef), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.repository.PublishPlan(context.Background(), fixture.publication); !errors.Is(err, ErrRARAuthorityConflict) {
			t.Fatalf("PublishPlan() conflict error = %v", err)
		}
	})

	t.Run("stored object corruption", func(t *testing.T) {
		fixture := newRARPlanFixture(t, "plan-corrupt")
		authority, err := fixture.repository.PublishPlan(context.Background(), fixture.publication)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture.repository.planObjectPath(authority.AuthorityRef), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.repository.ResolvePlan(context.Background(), authority.AuthorityRef); !errors.Is(err, ErrRARAuthorityCorrupt) {
			t.Fatalf("ResolvePlan() corruption error = %v", err)
		}
	})

	t.Run("directory sync failure is retry safe", func(t *testing.T) {
		fixture := newRARPlanFixture(t, "plan-durability")
		original := syncReviewDirectory
		failed := false
		syncReviewDirectory = func(path string) error {
			if !failed {
				failed = true
				return errors.New("injected plan directory sync failure")
			}
			return original(path)
		}
		_, publishErr := fixture.repository.PublishPlan(context.Background(), fixture.publication)
		syncReviewDirectory = original
		t.Cleanup(func() { syncReviewDirectory = original })
		if publishErr == nil {
			t.Fatal("PublishPlan() ignored directory sync failure")
		}
		authority, err := fixture.repository.PublishPlan(context.Background(), fixture.publication)
		if err != nil {
			t.Fatalf("PublishPlan() retry after sync failure = %v", err)
		}
		if _, err := fixture.repository.ResolvePlan(context.Background(), authority.AuthorityRef); err != nil {
			t.Fatalf("ResolvePlan() after retry = %v", err)
		}
	})
}

func TestRARPlanAuthorityRejectsUnsafePlanStorage(t *testing.T) {
	fixture := newRARPlanFixture(t, "plan-path-safety")
	if err := ensureRARRepositoryRoot(fixture.repository.identity.GitCommonDir, fixture.repository.root, true); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, fixture.repository.planObjectsRoot()); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := fixture.repository.PublishPlan(context.Background(), fixture.publication); err == nil {
		t.Fatal("PublishPlan() accepted symlinked plan storage")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unsafe plan storage target mutated: %v", entries)
	}
}

func TestRARPlanAuthorityRejectsStaleSnapshotPathsAndPolicyDrift(t *testing.T) {
	t.Run("live candidate changed", func(t *testing.T) {
		fixture := newRARPlanFixture(t, "plan-stale-candidate")
		authority, err := fixture.repository.PublishPlan(context.Background(), fixture.publication)
		if err != nil {
			t.Fatal(err)
		}
		writeSnapshotFile(t, fixture.repo, "tracked.txt", "candidate changed after plan publication\n")
		if _, err := fixture.repository.ResolvePlan(context.Background(), authority.AuthorityRef); !errors.Is(err, ErrRARAuthorityStale) {
			t.Fatalf("ResolvePlan() stale snapshot error = %v", err)
		}
	})

	t.Run("live path set changed", func(t *testing.T) {
		fixture := newRARPlanFixture(t, "plan-stale-paths")
		authority, err := fixture.repository.PublishPlan(context.Background(), fixture.publication)
		if err != nil {
			t.Fatal(err)
		}
		writeSnapshotFile(t, fixture.repo, "deleted.txt", "second changed path\n")
		live, err := (SnapshotBuilder{Repo: fixture.repo}).Build(context.Background(), Target{
			Kind: TargetCurrentChanges, IntendedUntracked: []string{},
		})
		if err != nil {
			t.Fatal(err)
		}
		if live.PathsDigest == fixture.publication.Snapshot.PathsDigest {
			t.Fatal("path drift fixture did not change PathsDigest")
		}
		if _, err := fixture.repository.ResolvePlan(context.Background(), authority.AuthorityRef); !errors.Is(err, ErrRARAuthorityStale) {
			t.Fatalf("ResolvePlan() stale paths error = %v", err)
		}
	})

	t.Run("policy preimages differ", func(t *testing.T) {
		fixture := newRARPlanFixture(t, "plan-policy-drift")
		different, err := NewVerificationPlanRegistry(
			verificationTestHash("different-plan-policy"),
			[]string{},
			fixture.publication.Registry.Obligations,
		)
		if err != nil {
			t.Fatal(err)
		}
		drift := fixture.publication
		drift.Registry = different
		if _, err := fixture.repository.PublishPlan(context.Background(), drift); err == nil {
			t.Fatal("PublishPlan() accepted applicability/plan from a stale policy registry")
		}
		if _, err := os.Lstat(fixture.repository.root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid policy preimages created RAR storage: %v", err)
		}
	})

	t.Run("caller applicability differs from native classifier", func(t *testing.T) {
		fixture := newRARPlanFixture(t, "plan-caller-applicability")
		forged := fixture.publication.Applicability
		forged.Decision = VerificationApplicable
		forged.ContentActivity = VerificationContentActive
		forged.Reasons = []VerificationApplicabilityReason{{
			Code: VerificationReasonActiveSource,
			Path: "tracked.txt",
		}}
		var err error
		forged.Digest, err = verificationApplicabilityDigest(forged)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildVerificationPlan(forged, fixture.publication.Registry)
		if err != nil {
			t.Fatal(err)
		}
		drift := fixture.publication
		drift.Applicability = forged
		drift.Plan = plan
		if _, err := fixture.repository.PublishPlan(context.Background(), drift); err == nil {
			t.Fatal("PublishPlan() accepted a caller-forged native applicability decision")
		}
		if _, err := os.Lstat(fixture.repository.root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("forged applicability created RAR storage: %v", err)
		}
	})

	t.Run("snapshot subject paths differ", func(t *testing.T) {
		fixture := newRARPlanFixture(t, "plan-subject-path-drift")
		drift := fixture.publication
		drift.Snapshot.PathsDigest = verificationTestHash("forged-plan-paths")
		if _, err := fixture.repository.PublishPlan(context.Background(), drift); err == nil {
			t.Fatal("PublishPlan() accepted a snapshot that differs from the plan subject")
		}
		if _, err := os.Lstat(fixture.repository.root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid path preimages created RAR storage: %v", err)
		}
	})
}

func TestRARPlanAuthorityBindsLinkedWorktreeIdentity(t *testing.T) {
	repo := initSnapshotRepo(t)
	linked := filepath.Join(t.TempDir(), "linked-plan-worktree")
	gitSnapshot(t, repo, "worktree", "add", "--detach", linked, "HEAD")
	writeSnapshotFile(t, linked, "tracked.txt", "linked candidate\n")

	fixture := newRARPlanFixtureAtRepo(t, linked, "plan-linked")
	authority, err := fixture.repository.PublishPlan(context.Background(), fixture.publication)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.ResolvePlan(context.Background(), authority.AuthorityRef); err != nil {
		t.Fatalf("linked worktree ResolvePlan() = %v", err)
	}

	mainRepository, err := OpenRARAuthorityRepository(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if mainRepository.root != fixture.repository.root {
		t.Fatalf("worktrees do not share Git-common-dir RAR root: %q != %q", mainRepository.root, fixture.repository.root)
	}
	if _, err := mainRepository.ResolvePlan(context.Background(), authority.AuthorityRef); !errors.Is(err, ErrRARAuthorityStale) {
		t.Fatalf("different worktree identity ResolvePlan() error = %v", err)
	}
}

func TestRARPlanAuthorityRejectsNonLiveExactRevision(t *testing.T) {
	repo := initSnapshotRepo(t)
	head := gitSnapshot(t, repo, "rev-parse", "HEAD")
	snapshot, err := (SnapshotBuilder{Repo: repo}).Build(context.Background(), Target{
		Kind: TargetExactRevision, Revision: head,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := rarPlanFixtureForSnapshot(t, repo, "plan-exact-revision", snapshot)
	if _, err := fixture.repository.PublishPlan(context.Background(), fixture.publication); !errors.Is(err, ErrRARAuthorityStale) {
		t.Fatalf("PublishPlan() non-live exact revision error = %v", err)
	}
	if _, err := os.Lstat(fixture.repository.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-live exact revision created RAR storage: %v", err)
	}
}

func newRARPlanFixture(t *testing.T, name string) rarPlanFixture {
	t.Helper()
	repo := initSnapshotRepo(t)
	writeSnapshotFile(t, repo, "tracked.txt", "planned candidate "+name+"\n")
	return newRARPlanFixtureAtRepo(t, repo, name)
}

func newRARPlanFixtureAtRepo(t *testing.T, repo, name string) rarPlanFixture {
	t.Helper()
	snapshot, err := (SnapshotBuilder{Repo: repo}).Build(context.Background(), Target{
		Kind: TargetCurrentChanges, IntendedUntracked: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return rarPlanFixtureForSnapshot(t, repo, name, snapshot)
}

func rarPlanFixtureForSnapshot(t *testing.T, repo, name string, snapshot Snapshot) rarPlanFixture {
	t.Helper()
	policyHash := verificationTestHash(name + "-policy")
	registry, err := NewVerificationPlanRegistry(
		policyHash,
		[]string{},
		[]VerificationObligation{{
			ID:              "unit",
			RequirementRef:  verificationTestHash(name + "-requirement"),
			ArgvRef:         verificationTestHash(name + "-argv"),
			CapabilityRef:   "host.exec",
			Cost:            VerificationCostQuick,
			MandatoryGlobal: true,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	applicability, err := (SnapshotBuilder{Repo: repo}).ClassifyVerificationApplicability(
		context.Background(),
		snapshot,
		registry,
		[]string{},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildVerificationPlan(applicability, registry)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := OpenRARAuthorityRepository(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	return rarPlanFixture{
		repo:       repo,
		repository: repository,
		publication: RARPlanPublication{
			Snapshot: snapshot, Applicability: applicability,
			Registry: registry, Plan: plan,
		},
	}
}

func (fixture rarPlanFixture) planAuthority(t *testing.T) RARPlanAuthority {
	t.Helper()
	subject, err := VerificationSubjectFromSnapshot(fixture.publication.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	authority := RARPlanAuthority{
		Schema:             RARPlanAuthoritySchema,
		RepositoryIdentity: fixture.repository.identity.RepositoryIdentity,
		Snapshot:           fixture.publication.Snapshot,
		Subject:            subject,
		Applicability:      fixture.publication.Applicability,
		Registry:           fixture.publication.Registry,
		Plan:               fixture.publication.Plan,
	}
	authority.AuthorityRef, err = rarPlanAuthorityDigest(authority)
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func rarPlanAuthoritiesEqual(left, right RARPlanAuthority) bool {
	return left.Schema == right.Schema &&
		left.AuthorityRef == right.AuthorityRef &&
		left.RepositoryIdentity == right.RepositoryIdentity &&
		SnapshotsEqualExact(left.Snapshot, right.Snapshot) &&
		left.Subject == right.Subject &&
		reflect.DeepEqual(left.Applicability, right.Applicability) &&
		reflect.DeepEqual(left.Registry, right.Registry) &&
		reflect.DeepEqual(left.Plan, right.Plan)
}
