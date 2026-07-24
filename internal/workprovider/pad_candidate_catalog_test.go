package workprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
)

func TestPADCandidateCatalogPublishesIdempotentlyAndSurvivesReopen(
	t *testing.T,
) {
	fixture, authority, objects, catalog := newPADCandidateCatalogFixture(t)
	binding := fixedPADCandidateBinding(
		authority.RepositoryRef(),
		fixture.baseRevision,
		fixture.headRevision,
		deliveryadmission.MechanismFastForwardOnly,
	)

	const workers = 8
	var wait sync.WaitGroup
	results := make(chan PADGitBinding, workers)
	failures := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			resolved, err := catalog.BindCandidate(
				context.Background(),
				fixture.headTree,
				binding,
			)
			if err != nil {
				failures <- err
				return
			}
			results <- resolved
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Errorf("concurrent idempotent publication: %v", err)
	}
	for result := range results {
		if result != binding {
			t.Errorf("published binding = %#v, want %#v", result, binding)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	reopened, err := NewPADCandidateCatalog(
		context.Background(),
		authority,
		objects,
	)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := reopened.ResolvePADGitBinding(
		context.Background(),
		binding.Candidate,
		binding.Destination,
		binding.Mechanism,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != binding {
		t.Fatalf("reopened binding = %#v, want %#v", resolved, binding)
	}
	for _, directory := range []string{"objects", "bindings"} {
		entries, err := os.ReadDir(filepath.Join(catalog.root, directory))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("%s contains %d artifacts, want 1", directory, len(entries))
		}
	}
}

func TestPADCandidateCatalogRejectsRebindingOccupiedSlot(t *testing.T) {
	fixture, authority, _, catalog := newPADCandidateCatalogFixture(t)
	binding := fixedPADCandidateBinding(
		authority.RepositoryRef(),
		fixture.baseRevision,
		fixture.headRevision,
		deliveryadmission.MechanismFastForwardOnly,
	)
	if _, err := catalog.BindCandidate(
		context.Background(),
		fixture.headTree,
		binding,
	); err != nil {
		t.Fatal(err)
	}

	runFixedPADTestGit(
		t,
		fixture.root,
		"commit",
		"--quiet",
		"--allow-empty",
		"-m",
		"same tree, distinct commit",
	)
	conflict := binding
	conflict.CandidateRevision = "git:" + runFixedPADTestGit(
		t,
		fixture.root,
		"rev-parse",
		"HEAD",
	)
	if _, err := catalog.BindCandidate(
		context.Background(),
		fixture.headTree,
		conflict,
	); !errors.Is(err, ErrPADCandidateCatalogConflict) {
		t.Fatalf("rebind error = %v", err)
	}
	resolved, err := catalog.ResolvePADGitBinding(
		context.Background(),
		binding.Candidate,
		binding.Destination,
		binding.Mechanism,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != binding {
		t.Fatalf("conflict changed binding to %#v", resolved)
	}
}

func TestPADCandidateCatalogFailsClosedOnTreeAndRepositoryMismatch(
	t *testing.T,
) {
	fixture, authority, _, catalog := newPADCandidateCatalogFixture(t)
	binding := fixedPADCandidateBinding(
		authority.RepositoryRef(),
		fixture.baseRevision,
		fixture.headRevision,
		deliveryadmission.MechanismFastForwardOnly,
	)
	if _, err := catalog.BindCandidate(
		context.Background(),
		fixture.baseTree,
		binding,
	); !errors.Is(err, ErrPADCandidateCatalogCorrupt) {
		t.Fatalf("candidate tree mismatch error = %v", err)
	}
	if _, err := catalog.ResolvePADGitBinding(
		context.Background(),
		binding.Candidate,
		binding.Destination,
		binding.Mechanism,
	); !errors.Is(err, ErrPADCandidateCatalogUnavailable) {
		t.Fatalf("unpublished mismatch lookup error = %v", err)
	}

	foreign := newFixedPADGitFixture(t)
	foreignAuthority, err := NewPADRepositoryAuthority(
		context.Background(),
		foreign.root,
	)
	if err != nil {
		t.Fatal(err)
	}
	crossRepository := binding
	crossRepository.Destination.RepositoryRef =
		foreignAuthority.RepositoryRef()
	if _, err := catalog.BindCandidate(
		context.Background(),
		fixture.headTree,
		crossRepository,
	); !errors.Is(err, ErrPADRepositoryAuthorityMismatch) {
		t.Fatalf("cross-repository publication error = %v", err)
	}

	staleRemote := binding
	staleRemote.ExpectedRemoteRevision = fixture.headRevision
	if _, err := catalog.BindCandidate(
		context.Background(),
		fixture.headTree,
		staleRemote,
	); !errors.Is(err, ErrPADCandidateCatalogCorrupt) {
		t.Fatalf("expected-remote mismatch error = %v", err)
	}

	secondLease, err := NewPADRepositoryAuthority(
		context.Background(),
		fixture.root,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondObjects, err := NewFixedPADGitObjectAuthority(
		context.Background(),
		secondLease,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPADCandidateCatalog(
		context.Background(),
		authority,
		secondObjects,
	); !errors.Is(err, ErrPADCandidateCatalogUnavailable) {
		t.Fatalf("mixed repository leases error = %v", err)
	}
}

func TestPADCandidateCatalogBindsPullRequestFacts(t *testing.T) {
	fixture, authority, _, catalog := newPADCandidateCatalogFixture(t)
	binding := fixedPADCandidateBinding(
		authority.RepositoryRef(),
		fixture.baseRevision,
		fixture.headRevision,
		deliveryadmission.MechanismPullRequest,
	)
	resolved, err := catalog.BindCandidate(
		context.Background(),
		fixture.headTree,
		binding,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != binding || resolved.PullRequestRef != "github:pull/42" {
		t.Fatalf("pull-request binding = %#v", resolved)
	}

	missingPR := binding
	missingPR.PullRequestRef = ""
	missingPR.Candidate.Ref = "candidate:missing-pr"
	missingPR.Candidate.Digest = padDeliveryDigest(
		"test-pad-candidate",
		missingPR.Candidate.Ref,
	)
	if _, err := catalog.BindCandidate(
		context.Background(),
		fixture.headTree,
		missingPR,
	); !errors.Is(err, ErrPADHostingCorrupt) {
		t.Fatalf("missing PR error = %v", err)
	}
}

func TestPADCandidateCatalogDetectsTamperAndSymlinkReplacement(t *testing.T) {
	t.Run("index tamper", func(t *testing.T) {
		fixture, authority, _, catalog := newPADCandidateCatalogFixture(t)
		binding := fixedPADCandidateBinding(
			authority.RepositoryRef(),
			fixture.baseRevision,
			fixture.headRevision,
			deliveryadmission.MechanismFastForwardOnly,
		)
		if _, err := catalog.BindCandidate(
			context.Background(),
			fixture.headTree,
			binding,
		); err != nil {
			t.Fatal(err)
		}
		_, lookupRef, err := catalog.lookup(
			binding.Candidate,
			binding.Destination,
			binding.Mechanism,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			catalog.indexPath(lookupRef),
			[]byte("{}\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := catalog.ResolvePADGitBinding(
			context.Background(),
			binding.Candidate,
			binding.Destination,
			binding.Mechanism,
		); !errors.Is(err, ErrPADCandidateCatalogCorrupt) {
			t.Fatalf("tampered index error = %v", err)
		}
	})

	t.Run("symlinked catalog directory", func(t *testing.T) {
		fixture, authority, _, catalog := newPADCandidateCatalogFixture(t)
		binding := fixedPADCandidateBinding(
			authority.RepositoryRef(),
			fixture.baseRevision,
			fixture.headRevision,
			deliveryadmission.MechanismFastForwardOnly,
		)
		if _, err := catalog.BindCandidate(
			context.Background(),
			fixture.headTree,
			binding,
		); err != nil {
			t.Fatal(err)
		}
		bindings := filepath.Join(catalog.root, "bindings")
		retained := filepath.Join(catalog.root, "bindings-retained")
		if err := os.Rename(bindings, retained); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(retained, bindings); err != nil {
			if restoreErr := os.Rename(retained, bindings); restoreErr != nil {
				t.Fatalf("symlink unsupported (%v) and restore failed: %v", err, restoreErr)
			}
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		defer func() {
			_ = os.Remove(bindings)
			_ = os.Rename(retained, bindings)
		}()
		if _, err := catalog.ResolvePADGitBinding(
			context.Background(),
			binding.Candidate,
			binding.Destination,
			binding.Mechanism,
		); !errors.Is(err, ErrPADCandidateCatalogUnavailable) {
			t.Fatalf("symlinked directory error = %v", err)
		}
	})
}

func newPADCandidateCatalogFixture(
	t *testing.T,
) (
	fixedPADGitFixture,
	*PADRepositoryAuthority,
	*FixedPADGitObjectAuthority,
	*PADCandidateCatalog,
) {
	t.Helper()
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
	catalog, err := NewPADCandidateCatalog(
		context.Background(),
		authority,
		objects,
	)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, authority, objects, catalog
}

func fixedPADCandidateBinding(
	repositoryRef string,
	expectedRevision string,
	candidateRevision string,
	mechanism deliveryadmission.Mechanism,
) PADGitBinding {
	pullRequestRef := ""
	if mechanism == deliveryadmission.MechanismPullRequest {
		pullRequestRef = "github:pull/42"
	}
	return PADGitBinding{
		Schema: PADGitBindingSchema,
		Candidate: deliveryadmission.CandidateBinding{
			Ref: "candidate:fixed-pad-catalog",
			Digest: padDeliveryDigest(
				"test-pad-candidate",
				"candidate:fixed-pad-catalog",
			),
		},
		Destination: deliveryadmission.DestinationBinding{
			RepositoryRef:    repositoryRef,
			TargetRef:        "refs/heads/main",
			ObservedRevision: expectedRevision,
			DefaultBranch:    true,
		},
		Mechanism:              mechanism,
		HostingRepositoryRef:   "github:gentleman/repository",
		CandidateRevision:      candidateRevision,
		ExpectedRemoteRevision: expectedRevision,
		PullRequestRef:         pullRequestRef,
	}
}
