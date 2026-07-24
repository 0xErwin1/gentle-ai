package deliveryadmission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

type linkedDeliveryRepositories struct {
	mainRoot       string
	linkedRoot     string
	mainIdentity   reviewtransaction.RepositoryIdentity
	linkedIdentity reviewtransaction.RepositoryIdentity
	mainKey        string
	linkedKey      string
	owner          *trustedRepository
}

func newLinkedDeliveryRepositories(t *testing.T) linkedDeliveryRepositories {
	t.Helper()
	if testing.Short() {
		t.Skip("linked-worktree integration is skipped in short mode")
	}
	parent := t.TempDir()
	mainRoot := filepath.Join(parent, "main")
	runDeliveryGitTest(t, "", "init", "--quiet", mainRoot)
	runDeliveryGitTest(t, mainRoot, "config", "user.name", "PAD Identity Test")
	runDeliveryGitTest(
		t,
		mainRoot,
		"config",
		"user.email",
		"pad-identity@example.invalid",
	)
	if err := os.WriteFile(
		filepath.Join(mainRoot, "README.md"),
		[]byte("PAD identity fixture\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	runDeliveryGitTest(t, mainRoot, "add", "README.md")
	runDeliveryGitTest(t, mainRoot, "commit", "--quiet", "-m", "fixture")
	linkedRoot := filepath.Join(parent, "linked")
	runDeliveryGitTest(
		t,
		mainRoot,
		"worktree",
		"add",
		"--quiet",
		"-b",
		"pad-linked",
		linkedRoot,
	)
	mainRoot = absoluteDeliveryTestPath(t, mainRoot)
	linkedRoot = absoluteDeliveryTestPath(t, linkedRoot)
	mainIdentity, mainKey := exactDeliveryIdentityForRoot(t, mainRoot)
	linkedIdentity, linkedKey := exactDeliveryIdentityForRoot(t, linkedRoot)
	if mainIdentity.GitCommonDir != linkedIdentity.GitCommonDir {
		t.Fatalf(
			"linked worktrees use different common dirs: %q != %q",
			mainIdentity.GitCommonDir,
			linkedIdentity.GitCommonDir,
		)
	}
	if mainIdentity.RepositoryRef == linkedIdentity.RepositoryRef ||
		mainKey == linkedKey {
		t.Fatalf(
			"linked worktree identities collapsed: refs %q/%q, keys %q/%q",
			mainIdentity.RepositoryRef,
			linkedIdentity.RepositoryRef,
			mainKey,
			linkedKey,
		)
	}
	owner := newTrustedRepository()
	owner.repositories[mainIdentity.RepositoryRef] = mainRoot
	owner.repositories[linkedIdentity.RepositoryRef] = linkedRoot
	return linkedDeliveryRepositories{
		mainRoot: mainRoot, linkedRoot: linkedRoot,
		mainIdentity: mainIdentity, linkedIdentity: linkedIdentity,
		mainKey: mainKey, linkedKey: linkedKey, owner: owner,
	}
}

func TestTrustedRepositoryShardsLinkedWorktreesAndReopens(t *testing.T) {
	fixture := newLinkedDeliveryRepositories(t)
	ctx := context.Background()
	clock := &mutableOwnerClock{now: testNow}
	type openResult struct {
		ref        string
		repository *TrustedRepository
		err        error
	}
	results := make(chan openResult, 2)
	var wait sync.WaitGroup
	for _, ref := range []string{
		fixture.mainIdentity.RepositoryRef,
		fixture.linkedIdentity.RepositoryRef,
	} {
		wait.Add(1)
		go func(ref string) {
			defer wait.Done()
			repository, err := OpenTrustedRepository(
				ctx,
				fixture.owner,
				ref,
				WithTrustedRepositoryClock(clock),
			)
			results <- openResult{ref: ref, repository: repository, err: err}
		}(ref)
	}
	wait.Wait()
	close(results)
	opened := make(map[string]*TrustedRepository, 2)
	for result := range results {
		if result.err != nil {
			t.Fatalf("open linked PAD repository %s: %v", result.ref, result.err)
		}
		opened[result.ref] = result.repository
		t.Cleanup(func() { _ = result.repository.Close() })
	}
	mainRepository := opened[fixture.mainIdentity.RepositoryRef]
	linkedRepository := opened[fixture.linkedIdentity.RepositoryRef]
	if mainRepository == nil || linkedRepository == nil {
		t.Fatalf("opened PAD repositories = %#v", opened)
	}
	wantMain := filepath.Join(
		fixture.mainIdentity.GitCommonDir,
		"gentle-ai",
		"delivery-admission",
		"v1",
		"repositories",
		fixture.mainKey,
	)
	wantLinked := filepath.Join(
		fixture.linkedIdentity.GitCommonDir,
		"gentle-ai",
		"delivery-admission",
		"v1",
		"repositories",
		fixture.linkedKey,
	)
	if mainRepository.directory.directoryPath != wantMain ||
		linkedRepository.directory.directoryPath != wantLinked ||
		wantMain == wantLinked {
		t.Fatalf(
			"PAD shard paths main=%q linked=%q, want %q/%q",
			mainRepository.directory.directoryPath,
			linkedRepository.directory.directoryPath,
			wantMain,
			wantLinked,
		)
	}
	assertTrustedBindingIdentity(
		t,
		mainRepository,
		fixture.mainIdentity,
		fixture.mainKey,
	)
	assertTrustedBindingIdentity(
		t,
		linkedRepository,
		fixture.linkedIdentity,
		fixture.linkedKey,
	)

	policy, err := NewRoutePolicy(
		"policy:linked-worktree",
		"revision:1",
		RouteDirectMain,
		true,
		false,
		false,
		120,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyRef, err := mainRepository.PublishRoutePolicy(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	beforeMissing := fingerprintDeliveryTree(t, wantLinked)
	if _, err := linkedRepository.ResolveRoutePolicy(
		ctx,
		policyRef,
	); !errors.Is(err, ErrTrustedRepositoryUnavailable) {
		t.Fatalf("cross-worktree preimage lookup error = %v", err)
	}
	if afterMissing := fingerprintDeliveryTree(t, wantLinked); afterMissing != beforeMissing {
		t.Fatalf(
			"missing linked-worktree read created storage:\nbefore %s\nafter  %s",
			beforeMissing,
			afterMissing,
		)
	}
	linkedPolicyRef, err := linkedRepository.PublishRoutePolicy(ctx, policy)
	if err != nil || linkedPolicyRef != policyRef {
		t.Fatalf("publish isolated linked policy = %q, %v", linkedPolicyRef, err)
	}
	if _, err := linkedRepository.ResolveRoutePolicy(ctx, policyRef); err != nil {
		t.Fatal(err)
	}

	if err := mainRepository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenTrustedRepository(
		ctx,
		fixture.owner,
		fixture.mainIdentity.RepositoryRef,
		WithTrustedRepositoryClock(clock),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if reopened.directory.directoryPath != wantMain {
		t.Fatalf(
			"reopened PAD path = %q, want %q",
			reopened.directory.directoryPath,
			wantMain,
		)
	}
	if resolved, err := reopened.ResolveRoutePolicy(ctx, policyRef); err != nil ||
		resolved != policy {
		t.Fatalf("reopen replay policy = %#v, %v", resolved, err)
	}
	beforeReadMiss := fingerprintDeliveryTree(t, wantMain)
	if _, err := reopened.ResolveIntent(
		ctx,
		testDigest("f"),
	); !errors.Is(err, ErrTrustedRepositoryUnavailable) {
		t.Fatalf("missing intent error = %v", err)
	}
	if afterReadMiss := fingerprintDeliveryTree(t, wantMain); afterReadMiss != beforeReadMiss {
		t.Fatalf(
			"missing PAD read created records:\nbefore %s\nafter  %s",
			beforeReadMiss,
			afterReadMiss,
		)
	}
}

func TestDirectoryUseStoreShardsLinkedWorktreeUses(t *testing.T) {
	fixture := newLinkedDeliveryRepositories(t)
	ctx := context.Background()
	mainStore, err := OpenDirectoryUseStore(
		ctx,
		fixture.owner,
		fixture.mainIdentity.RepositoryRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mainStore.Close() })
	linkedStore, err := OpenDirectoryUseStore(
		ctx,
		fixture.owner,
		fixture.linkedIdentity.RepositoryRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = linkedStore.Close() })
	if mainStore.directory == linkedStore.directory ||
		!strings.HasSuffix(mainStore.directory, filepath.Join("repositories", fixture.mainKey)) ||
		!strings.HasSuffix(linkedStore.directory, filepath.Join("repositories", fixture.linkedKey)) {
		t.Fatalf(
			"authorization-use shards main=%q linked=%q",
			mainStore.directory,
			linkedStore.directory,
		)
	}
	const seed = "a"
	authorizationRef := testDigest(seed)
	mainUse := testAuthorizationUse(
		t,
		fixture.mainIdentity.RepositoryRef,
		authorizationRef,
	)
	linkedUse := testAuthorizationUse(
		t,
		fixture.linkedIdentity.RepositoryRef,
		authorizationRef,
	)
	if err := mainStore.consumeOnce(ctx, mainUse); err != nil {
		t.Fatal(err)
	}
	if err := linkedStore.consumeOnce(ctx, linkedUse); err != nil {
		t.Fatal(err)
	}
	if err := mainStore.consumeOnce(ctx, mainUse); !errors.Is(err, ErrAlreadyConsumed) {
		t.Fatalf("main exact use replay error = %v", err)
	}
	if err := linkedStore.consumeOnce(
		ctx,
		linkedUse,
	); !errors.Is(err, ErrAlreadyConsumed) {
		t.Fatalf("linked exact use replay error = %v", err)
	}
	if resolved, found, err := mainStore.authorizationUse(
		ctx,
		authorizationRef,
	); err != nil || !found || resolved != mainUse {
		t.Fatalf("main authorization use = %#v, %t, %v", resolved, found, err)
	}
	if resolved, found, err := linkedStore.authorizationUse(
		ctx,
		authorizationRef,
	); err != nil || !found || resolved != linkedUse {
		t.Fatalf("linked authorization use = %#v, %t, %v", resolved, found, err)
	}
	beforeMiss := fingerprintDeliveryTree(t, mainStore.directory)
	if _, found, err := mainStore.authorizationUse(
		ctx,
		testDigest("f"),
	); err != nil || found {
		t.Fatalf("missing authorization use = %t, %v", found, err)
	}
	if afterMiss := fingerprintDeliveryTree(t, mainStore.directory); afterMiss != beforeMiss {
		t.Fatalf(
			"missing authorization-use read created records:\nbefore %s\nafter  %s",
			beforeMiss,
			afterMiss,
		)
	}

	if err := mainStore.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDirectoryUseStore(
		ctx,
		fixture.owner,
		fixture.mainIdentity.RepositoryRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if resolved, found, err := reopened.authorizationUse(
		ctx,
		authorizationRef,
	); err != nil || !found || resolved != mainUse {
		t.Fatalf("reopened authorization use = %#v, %t, %v", resolved, found, err)
	}
}

func TestPADIdentityLeaseRejectsGitReplacementWithoutWritingReplacement(
	t *testing.T,
) {
	if testing.Short() {
		t.Skip("Git control replacement integration is skipped in short mode")
	}
	root, identity, _ := initExactDeliveryRepository(t)
	owner := newTrustedRepository()
	owner.repositories[identity.RepositoryRef] = root
	ctx := context.Background()
	repository, err := OpenTrustedRepository(
		ctx,
		owner,
		identity.RepositoryRef,
		WithTrustedRepositoryClock(&mutableOwnerClock{now: testNow}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	useStore, err := OpenDirectoryUseStore(ctx, owner, identity.RepositoryRef)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = useStore.Close() })
	gitControl := filepath.Join(root, ".git")
	retiredControl := filepath.Join(root, ".git-retired")
	if err := os.Rename(gitControl, retiredControl); err != nil {
		t.Fatal(err)
	}
	runDeliveryGitTest(t, "", "init", "--quiet", root)
	replacementNamespace := filepath.Join(root, ".git", "gentle-ai")
	if _, err := os.Lstat(replacementNamespace); !os.IsNotExist(err) {
		t.Fatalf("replacement Git identity already contains PAD state: %v", err)
	}
	policy, err := NewRoutePolicy(
		"policy:stale-handle",
		"revision:1",
		RouteDirectMain,
		true,
		false,
		false,
		120,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PublishRoutePolicy(
		ctx,
		policy,
	); !errors.Is(err, reviewtransaction.ErrRepositoryIdentityChanged) {
		t.Fatalf("stale trusted repository error = %v", err)
	}
	if _, _, err := useStore.authorizationUse(
		ctx,
		testDigest("e"),
	); !errors.Is(err, reviewtransaction.ErrRepositoryIdentityChanged) {
		t.Fatalf("stale authorization-use store error = %v", err)
	}
	if _, err := os.Lstat(replacementNamespace); !os.IsNotExist(err) {
		t.Fatalf("stale PAD handles wrote replacement Git identity: %v", err)
	}
}

func TestPADExactRepositoryRefMismatchCreatesNoStorage(t *testing.T) {
	root, identity, _ := initExactDeliveryRepository(t)
	owner := newTrustedRepository()
	const alias = "github:repository-alias"
	owner.repositories[alias] = root
	if repository, err := OpenTrustedRepository(
		context.Background(),
		owner,
		alias,
	); !errors.Is(err, ErrTrustedRepositoryCorrupt) {
		if repository != nil {
			_ = repository.Close()
		}
		t.Fatalf("non-exact trusted repository ref error = %v", err)
	}
	if store, err := OpenDirectoryUseStore(
		context.Background(),
		owner,
		alias,
	); !errors.Is(err, ErrBindingMismatch) {
		if store != nil {
			_ = store.Close()
		}
		t.Fatalf("non-exact authorization-use ref error = %v", err)
	}
	if _, err := os.Lstat(
		filepath.Join(identity.GitCommonDir, "gentle-ai"),
	); !os.IsNotExist(err) {
		t.Fatalf("repository-ref mismatch created PAD storage: %v", err)
	}
}

func TestPADIdentityShardsIgnoreUnshardedPreReleaseState(t *testing.T) {
	root, identity, _ := initExactDeliveryRepository(t)
	owner := newTrustedRepository()
	owner.repositories[identity.RepositoryRef] = root
	policy, err := NewRoutePolicy(
		"policy:legacy-unsharded",
		"revision:1",
		RouteDirectMain,
		true,
		false,
		false,
		120,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyRef, err := policy.Ref()
	if err != nil {
		t.Fatal(err)
	}
	policyPayload, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	legacyAdmission := filepath.Join(
		identity.GitCommonDir,
		"gentle-ai",
		"delivery-admission",
		"v1",
	)
	legacyUses := filepath.Join(
		identity.GitCommonDir,
		"gentle-ai",
		"delivery-authorization-uses",
		"v1",
	)
	for _, directory := range []string{legacyAdmission, legacyUses} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(
			legacyAdmission,
			objectRecordName(policyRef, "route-policy"),
		),
		policyPayload,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	authorizationRef := testDigest("a")
	legacyUse := testAuthorizationUse(
		t,
		identity.RepositoryRef,
		authorizationRef,
	)
	usePayload, err := json.Marshal(legacyUse)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(
			legacyUses,
			strings.TrimPrefix(authorizationRef, "sha256:")+".json",
		),
		usePayload,
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	repository, err := OpenTrustedRepository(
		ctx,
		owner,
		identity.RepositoryRef,
		WithTrustedRepositoryClock(&mutableOwnerClock{now: testNow}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	if _, err := repository.ResolveRoutePolicy(
		ctx,
		policyRef,
	); !errors.Is(err, ErrTrustedRepositoryUnavailable) {
		t.Fatalf("unsharded PAD policy was reused: %v", err)
	}
	useStore, err := OpenDirectoryUseStore(ctx, owner, identity.RepositoryRef)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = useStore.Close() })
	if _, found, err := useStore.authorizationUse(
		ctx,
		authorizationRef,
	); err != nil || found {
		t.Fatalf("unsharded authorization use was reused: found=%t err=%v", found, err)
	}
}

func assertTrustedBindingIdentity(
	t *testing.T,
	repository *TrustedRepository,
	identity reviewtransaction.RepositoryIdentity,
	storageKey string,
) {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(
		repository.directory.directoryPath,
		"repository-binding.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var binding trustedRepositoryBinding
	if err := json.Unmarshal(payload, &binding); err != nil {
		t.Fatal(err)
	}
	if err := binding.validate(); err != nil {
		t.Fatal(err)
	}
	if binding.RepositoryRef != identity.RepositoryRef ||
		binding.RepositoryRoot != identity.RepositoryRoot ||
		binding.GitCommonDir != identity.GitCommonDir ||
		binding.GitDir != identity.GitDir ||
		binding.StorageKey != storageKey {
		t.Fatalf("trusted repository binding = %#v", binding)
	}
}

func testAuthorizationUse(
	t *testing.T,
	repositoryRef string,
	authorizationRef string,
) AuthorizationUse {
	t.Helper()
	policy, err := NewRoutePolicy(
		"policy:linked-use",
		"revision:1",
		RouteDirectMain,
		true,
		false,
		false,
		120,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	use := AuthorizationUse{
		Schema:           AuthorizationUseContractV1,
		Kind:             AuthorizationNormal,
		AuthorizationRef: authorizationRef,
		DecisionRef:      testDigest("b"),
		IntentRef:        testDigest("c"),
		Route:            RouteDirectMain,
		Candidate: CandidateBinding{
			Ref:    "candidate:linked-use",
			Digest: testDigest("d"),
		},
		Destination: DestinationBinding{
			RepositoryRef:    repositoryRef,
			TargetRef:        "refs/heads/main",
			ObservedRevision: "git:base/1",
			DefaultBranch:    true,
		},
		PolicyRef:              testDigest("e"),
		Policy:                 policy.Binding(),
		ConsumedAt:             testNow,
		AuthorizationExpiresAt: testNow + 300,
	}
	if err := use.Validate(); err != nil {
		t.Fatal(err)
	}
	return use
}

func fingerprintDeliveryTree(t *testing.T, root string) string {
	t.Helper()
	entries := make([]string, 0, 16)
	if err := filepath.WalkDir(
		root,
		func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			line := fmt.Sprintf(
				"%s|%s|%d",
				filepath.ToSlash(relative),
				info.Mode().String(),
				info.Size(),
			)
			if !entry.IsDir() {
				payload, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				sum := sha256.Sum256(payload)
				line += "|" + hex.EncodeToString(sum[:])
			}
			entries = append(entries, line)
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return hex.EncodeToString(sum[:])
}

func runDeliveryGitTest(
	t *testing.T,
	repository string,
	args ...string,
) {
	t.Helper()
	commandArgs := append([]string{}, args...)
	if repository != "" {
		commandArgs = append([]string{"-C", repository}, commandArgs...)
	}
	if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(commandArgs, " "), err, output)
	}
}

func absoluteDeliveryTestPath(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(absolute)
}
