package workprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestTransitionAuthorityStoreIsReadOnlyUntilOwnerPublication(t *testing.T) {
	repo := initWorkRunAdapterRepository(t)
	store, err := OpenTransitionAuthorityStore(
		context.Background(),
		repo,
		"transition-read-only",
	)
	if err != nil {
		t.Fatalf("OpenTransitionAuthorityStore() error = %v", err)
	}
	authorization, err := store.CurrentAuthorization(
		context.Background(),
		testRevision("a"),
		15,
	)
	if err != nil {
		t.Fatalf("CurrentAuthorization() error = %v", err)
	}
	if authorization != nil {
		t.Fatalf("CurrentAuthorization() = %#v, want nil", authorization)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "gentle-ai")); !os.IsNotExist(err) {
		t.Fatalf("read-only open created provider storage: %v", err)
	}
}

func TestTransitionAuthorityStoreExactSlotClaimAndcompletionReplay(t *testing.T) {
	repo := initWorkRunAdapterRepository(t)
	store, err := OpenTransitionAuthorityStore(
		context.Background(),
		repo,
		"transition-store",
	)
	if err != nil {
		t.Fatalf("OpenTransitionAuthorityStore() error = %v", err)
	}
	authorization := validVerificationTransitionAuthorization(t)
	authorization.WorkRunID = "transition-store"
	authorization.AuthorizationRef, err = verificationTransitionAuthorizationRef(authorization)
	if err != nil {
		t.Fatal(err)
	}
	if err := authorization.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishAuthorization(
		context.Background(),
		authorization,
	); err != nil {
		t.Fatalf("PublishAuthorization() error = %v", err)
	}
	if err := store.PublishAuthorization(
		context.Background(),
		authorization,
	); err != nil {
		t.Fatalf("PublishAuthorization(replay) error = %v", err)
	}
	current, err := store.CurrentAuthorization(
		context.Background(),
		authorization.ExpectedRevision,
		15,
	)
	if err != nil || current == nil ||
		current.AuthorizationRef != authorization.AuthorizationRef {
		t.Fatalf("CurrentAuthorization() = %#v, %v", current, err)
	}

	conflict := authorization
	conflict.CandidateRef = testRevision("f")
	conflict.AuthorizationRef, err = verificationTransitionAuthorizationRef(conflict)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishAuthorization(
		context.Background(),
		conflict,
	); !errors.Is(err, ErrTransitionAuthorityConflict) {
		t.Fatalf("PublishAuthorization(conflict) error = %v", err)
	}

	err = store.withAuthorizationLock(
		context.Background(),
		authorization.AuthorizationRef,
		func() error {
			return store.markClaimed(authorization, 15)
		},
	)
	if err != nil {
		t.Fatalf("claim error = %v", err)
	}
	current, err = store.CurrentAuthorization(
		context.Background(),
		authorization.ExpectedRevision,
		15,
	)
	if err != nil || current != nil {
		t.Fatalf("claimed CurrentAuthorization() = %#v, %v", current, err)
	}
	resolved, phase, err := store.ResolveAuthorization(
		context.Background(),
		authorization.AuthorizationRef,
		authorization.ExpiresAt+100,
	)
	if err != nil || phase != TransitionClaimed ||
		resolved.AuthorizationRef != authorization.AuthorizationRef {
		t.Fatalf("expired claimed resolve = %#v, %q, %v", resolved, phase, err)
	}

	receipt := workrun.WorkTransitionV1{
		Schema:                  workrun.WorkTransitionContractV1,
		Contract:                workrun.WorkTransitionContractV1,
		WorkRunID:               authorization.WorkRunID,
		PreviousRevision:        authorization.ExpectedRevision,
		Revision:                testRevision("e"),
		AppliedAuthorizationRef: authorization.AuthorizationRef,
		PublicState:             workrun.PublicStateChecking,
	}
	if err := store.markCompleted(
		authorization,
		testRevision("9"),
		receipt,
	); err != nil {
		t.Fatalf("markCompleted() error = %v", err)
	}
	if err := store.markCompleted(
		authorization,
		testRevision("9"),
		receipt,
	); err != nil {
		t.Fatalf("markCompleted(replay) error = %v", err)
	}
	completed, evidenceRef, err := store.completion(authorization)
	if err != nil || completed == nil ||
		*completed != receipt ||
		evidenceRef != testRevision("9") {
		t.Fatalf("completion() = %#v, %q, %v", completed, evidenceRef, err)
	}
	_, phase, err = store.ResolveAuthorization(
		context.Background(),
		authorization.AuthorizationRef,
		authorization.ExpiresAt+100,
	)
	if err != nil || phase != TransitionCompleted {
		t.Fatalf("completed resolve phase = %q, %v", phase, err)
	}
}

func TestTransitionAuthorityStoreIsolatesLinkedWorktreesWithSameRun(
	t *testing.T,
) {
	repo := initProductionRepositoryGit(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runProductionGit(
		t,
		repo,
		"worktree",
		"add",
		"-qb",
		"transition-linked-isolation",
		linked,
		"HEAD",
	)
	t.Cleanup(func() {
		runProductionGit(t, repo, "worktree", "remove", "--force", linked)
	})

	mainStore, err := OpenTransitionAuthorityStore(
		context.Background(),
		repo,
		"shared-transition-run",
	)
	if err != nil {
		t.Fatal(err)
	}
	linkedStore, err := OpenTransitionAuthorityStore(
		context.Background(),
		linked,
		"shared-transition-run",
	)
	if err != nil {
		t.Fatal(err)
	}
	if mainStore.identity.repositoryIdentity ==
		linkedStore.identity.repositoryIdentity {
		t.Fatal("linked transition stores share a repository identity")
	}

	authorization := validVerificationTransitionAuthorization(t)
	authorization.WorkRunID = "shared-transition-run"
	authorization.AuthorizationRef, err =
		verificationTransitionAuthorizationRef(authorization)
	if err != nil {
		t.Fatal(err)
	}
	if err := mainStore.PublishAuthorization(
		context.Background(),
		authorization,
	); err != nil {
		t.Fatal(err)
	}
	if current, err := linkedStore.CurrentAuthorization(
		context.Background(),
		authorization.ExpectedRevision,
		15,
	); err != nil || current != nil {
		t.Fatalf(
			"linked worktree observed main authorization: %#v, %v",
			current,
			err,
		)
	}
	if err := linkedStore.PublishAuthorization(
		context.Background(),
		authorization,
	); err != nil {
		t.Fatal(err)
	}
	if err := mainStore.withAuthorizationLock(
		context.Background(),
		authorization.AuthorizationRef,
		func() error { return mainStore.markClaimed(authorization, 15) },
	); err != nil {
		t.Fatal(err)
	}
	current, err := linkedStore.CurrentAuthorization(
		context.Background(),
		authorization.ExpectedRevision,
		15,
	)
	if err != nil || current == nil {
		t.Fatalf(
			"main claim affected linked transition shard: %#v, %v",
			current,
			err,
		)
	}
}

func TestTransitionAuthorityStoreRejectsReplacedGitIdentityWithoutWriting(
	t *testing.T,
) {
	repo := initProductionRepositoryGit(t)
	store, err := OpenTransitionAuthorityStore(
		context.Background(),
		repo,
		"transition-git-swap",
	)
	if err != nil {
		t.Fatal(err)
	}
	originalGit := filepath.Join(repo, ".git")
	if err := os.Rename(
		originalGit,
		filepath.Join(repo, ".git-retired"),
	); err != nil {
		t.Skipf("cannot replace Git control directory on this platform: %v", err)
	}
	runProductionGit(t, repo, "init")

	if _, err := store.HasIndeterminate(context.Background()); !errors.Is(
		err,
		reviewtransaction.ErrRepositoryIdentityChanged,
	) {
		t.Fatalf("HasIndeterminate() after Git replacement error = %v", err)
	}
	replacementRoot := filepath.Join(
		originalGit,
		"gentle-ai",
		"work-provider",
		"coordination-authority",
		coordinationRootVersion,
	)
	if _, err := os.Lstat(replacementRoot); !os.IsNotExist(err) {
		t.Fatalf(
			"stale transition handle wrote replacement Git identity: %v",
			err,
		)
	}
}

func TestTransitionAuthorityIndeterminateNeverResolvesExecutable(t *testing.T) {
	repo := initWorkRunAdapterRepository(t)
	store, err := OpenTransitionAuthorityStore(
		context.Background(),
		repo,
		"transition-indeterminate",
	)
	if err != nil {
		t.Fatal(err)
	}
	request := validVerificationTransitionAuthorizationRequest()
	request.WorkRunID = "transition-indeterminate"
	authorization, err := NewVerificationTransitionAuthorization(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishAuthorization(context.Background(), authorization); err != nil {
		t.Fatal(err)
	}
	err = store.withAuthorizationLock(
		context.Background(),
		authorization.AuthorizationRef,
		func() error {
			if err := store.markClaimed(authorization, 15); err != nil {
				return err
			}
			return store.markIndeterminate(authorization)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, phase, err := store.ResolveAuthorization(
		context.Background(),
		authorization.AuthorizationRef,
		15,
	); !errors.Is(err, ErrTransitionIndeterminate) ||
		phase != TransitionIndeterminate {
		t.Fatalf("ResolveAuthorization() phase = %q, error = %v", phase, err)
	}
	indeterminate, err := store.HasIndeterminate(context.Background())
	if err != nil || !indeterminate {
		t.Fatalf("HasIndeterminate() = %t, %v", indeterminate, err)
	}
}
