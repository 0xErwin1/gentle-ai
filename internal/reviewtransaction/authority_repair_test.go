package reviewtransaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAssessAuthorityRepairAtRepositoryRootAddsNoGitSubprocess(t *testing.T) {
	repo := initSnapshotRepo(t)
	root, err := (SnapshotBuilder{Repo: repo}).ResolveRepositoryRoot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	originalCommand := gitCommandContext
	t.Cleanup(func() { gitCommandContext = originalCommand })
	count := 0
	gitCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		count++
		return originalCommand(ctx, name, args...)
	}
	assessment, err := AssessAuthorityRepairAtRepositoryRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := assessment.Validate(); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("authority repair assessment added %d Git subprocesses after STATUS root resolution", count)
	}
}

func TestAssessAuthorityRepairDerivesExactCurrentLegacyHeadDeterministically(t *testing.T) {
	repo := initSnapshotRepo(t)
	store, _, offending := legacyAliasRepairFixture(t, repo, "classified-alias")
	offendingRecord, _, err := store.loadRevision(offending)
	if err != nil {
		t.Fatal(err)
	}
	successor := offendingRecord.Transaction
	if err := successor.BeginFinalVerification(); err != nil {
		t.Fatal(err)
	}
	currentHead := writeStoreEvent(t, store, Record{
		Operation: "review/begin-final-verification", PreviousRevision: offending, Transaction: successor,
	})
	if currentHead == offending {
		t.Fatal("fixture did not advance legacy HEAD beyond the offending alias event")
	}
	root, _, err := reviewAuthorityRoot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	before := authorityBytes(t, root)

	first, err := AssessAuthorityRepair(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AssessAuthorityRepair(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated assessment changed:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	if first.Status != AuthorityRepairEligible || first.Class != AuthorityRepairClassLegacyV1HistoricalAlias ||
		first.Cause != AuthorityRepairCauseUnsupportedHistoricalV1OperationAlias || first.Disposition != AuthorityRepairDispositionQuarantineHistoricalAlias ||
		first.Candidate == nil || first.Candidate.LineageID != "classified-alias" || first.Candidate.Revision != currentHead ||
		first.Candidate.Revision == offending || first.Candidate.ChainIdentity == "" ||
		!reflect.DeepEqual(first.Candidate.Operations, []string{"review/validate-fix"}) ||
		first.RepositoryBinding == "" || first.AuthorizationSchema != AuthorityRepairAuthorizationSchema {
		t.Fatalf("eligible assessment = %#v", first)
	}
	if after := authorityBytes(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("repair assessment changed authority bytes")
	}
	payload, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(repo)) || bytes.Contains(payload, []byte(store.Dir)) {
		t.Fatalf("repair assessment leaked a private path: %s", payload)
	}

	linked := filepath.Join(t.TempDir(), "linked")
	gitSnapshot(t, repo, "worktree", "add", "--detach", linked)
	t.Cleanup(func() { gitSnapshot(t, repo, "worktree", "remove", "--force", linked) })
	linkedAssessment, err := AssessAuthorityRepair(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if linkedAssessment.RepositoryBinding != first.RepositoryBinding {
		t.Fatalf("linked repository binding = %q, want %q", linkedAssessment.RepositoryBinding, first.RepositoryBinding)
	}
}

func TestAssessAuthorityRepairStopsUnknownMultipleMixedAndCollidingInventory(t *testing.T) {
	for _, tt := range []struct {
		name string
		want AuthorityRepairStatus
		make func(t *testing.T, repo string)
	}{
		{
			name: "unknown alias", want: AuthorityRepairUnsupported,
			make: func(t *testing.T, repo string) {
				legacyAliasRepairFixture(t, repo, "unknown-alias", "review/unknown-fix")
			},
		},
		{
			name: "multiple eligible", want: AuthorityRepairAmbiguous,
			make: func(t *testing.T, repo string) {
				legacyAliasRepairFixture(t, repo, "alias-one")
				legacyAliasRepairFixture(t, repo, "alias-two", "review/complete-fix")
			},
		},
		{
			name: "eligible plus unknown", want: AuthorityRepairAmbiguous,
			make: func(t *testing.T, repo string) {
				legacyAliasRepairFixture(t, repo, "alias-known")
				legacyAliasRepairFixture(t, repo, "alias-unknown", "review/unknown-fix")
			},
		},
		{
			name: "eligible plus compact atomic residue", want: AuthorityRepairAmbiguous,
			make: func(t *testing.T, repo string) {
				legacyAliasRepairFixture(t, repo, "alias-with-residue")
				state := newCompactTestState(t, repo, "compact-reset-residue")
				store, err := CompactAuthoritativeStore(context.Background(), repo, state.LineageID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.Replace("", "review/start", state); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(store.Dir, ".atomic-interrupted"), []byte("residue\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "v1 v2 collision", want: AuthorityRepairConflicting,
			make: func(t *testing.T, repo string) {
				legacyAliasRepairFixture(t, repo, "alias-collision")
				state := newCompactTestState(t, repo, "alias-collision")
				store, err := CompactAuthoritativeStore(context.Background(), repo, state.LineageID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.Replace("", "review/start", state); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "valid invalidated is not a candidate", want: AuthorityRepairUnsupported,
			make: func(t *testing.T, repo string) {
				state := newCompactTestState(t, repo, "valid-invalidated")
				store, err := CompactAuthoritativeStore(context.Background(), repo, state.LineageID)
				if err != nil {
					t.Fatal(err)
				}
				revision, err := store.Replace("", "review/start", state)
				if err != nil {
					t.Fatal(err)
				}
				if err := state.Invalidate("obsolete"); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Replace(revision, "review/invalidate", state); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := initSnapshotRepo(t)
			tt.make(t, repo)
			assessment, err := AssessAuthorityRepair(context.Background(), repo)
			if err != nil {
				t.Fatal(err)
			}
			if err := assessment.Validate(); err != nil {
				t.Fatal(err)
			}
			if assessment.Status != tt.want || assessment.Candidate != nil || assessment.Class != "" ||
				assessment.Cause != "" || assessment.Disposition != "" || assessment.RepositoryBinding != "" {
				t.Fatalf("stopped assessment = %#v", assessment)
			}
		})
	}
}

func TestAssessAuthorityRepairStopsLockedAndTruncatedInventory(t *testing.T) {
	t.Run("lineage lock", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		store, _, _ := legacyAliasRepairFixture(t, repo, "alias-local-lock")
		lock, err := acquireStoreLock(filepath.Join(store.Dir, "LOCK"))
		if err != nil {
			t.Fatal(err)
		}
		defer lock.release()
		assessment, err := AssessAuthorityRepair(context.Background(), repo)
		if err != nil || assessment.Status != AuthorityRepairConflicting || assessment.Candidate != nil {
			t.Fatalf("local-lock assessment = %#v, %v", assessment, err)
		}
	})

	t.Run("maintenance lock", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		legacyAliasRepairFixture(t, repo, "alias-maintenance-lock")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		lock, err := AcquireReviewMaintenanceExclusive(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Release()
		assessment, err := AssessAuthorityRepair(context.Background(), repo)
		if err != nil || assessment.Status != AuthorityRepairConflicting || assessment.Candidate != nil {
			t.Fatalf("maintenance-lock assessment = %#v, %v", assessment, err)
		}
	})

	t.Run("oversized event", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		store, head, _ := legacyAliasRepairFixture(t, repo, "alias-oversized")
		path := filepath.Join(store.Dir, "events", strings.TrimPrefix(head, "sha256:")+".json")
		if err := os.WriteFile(path, bytes.Repeat([]byte("x"), authorityRepairMaxEventBytes+1), 0o644); err != nil {
			t.Fatal(err)
		}
		assessment, err := AssessAuthorityRepair(context.Background(), repo)
		if err != nil || assessment.Status != AuthorityRepairTruncated || assessment.Candidate != nil {
			t.Fatalf("truncated assessment = %#v, %v", assessment, err)
		}
	})

	t.Run("too many unexpected entries", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		root, _, err := reviewAuthorityRoot(context.Background(), repo)
		if err != nil {
			t.Fatal(err)
		}
		versionRoot := filepath.Join(root, "v1")
		if err := os.MkdirAll(versionRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		for index := 0; index <= authorityRepairMaxLineages; index++ {
			path := filepath.Join(versionRoot, fmt.Sprintf("unexpected-%03d.json", index))
			if err := os.WriteFile(path, []byte("residue\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		assessment, err := AssessAuthorityRepair(context.Background(), repo)
		if err != nil || assessment.Status != AuthorityRepairTruncated || assessment.Candidate != nil {
			t.Fatalf("entry-bound assessment = %#v, %v", assessment, err)
		}
	})
}

func TestAuthorityRepairAuthorizationRejectsNonExactBindings(t *testing.T) {
	repo := initSnapshotRepo(t)
	_, head, _ := legacyAliasRepairFixture(t, repo, "alias-authorization")
	assessment, err := AssessAuthorityRepair(context.Background(), repo)
	if err != nil || assessment.Candidate == nil {
		t.Fatalf("assessment = %#v, %v", assessment, err)
	}
	request := ClassifiedAuthorityRepairRequest{
		Class: assessment.Class, LineageID: assessment.Candidate.LineageID, ExpectedRevision: head,
		Cause: assessment.Cause, Disposition: assessment.Disposition, RepositoryBinding: assessment.RepositoryBinding,
		Actor: "maintainer@example.com", Reason: "quarantine the approved historical alias",
	}
	request.MaintainerAuthorization = authorityRepairAuthorizationBinding(request)
	if err := validateClassifiedAuthorityRepairRequest(request, assessment); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ClassifiedAuthorityRepairRequest){
		func(value *ClassifiedAuthorityRepairRequest) { value.MaintainerAuthorization += "\n" },
		func(value *ClassifiedAuthorityRepairRequest) {
			value.MaintainerAuthorization = strings.ReplaceAll(value.MaintainerAuthorization, "\n", "\r\n")
		},
		func(value *ClassifiedAuthorityRepairRequest) { value.Actor = "other@example.com" },
		func(value *ClassifiedAuthorityRepairRequest) { value.ExpectedRevision = hash("f") },
		func(value *ClassifiedAuthorityRepairRequest) { value.RepositoryBinding = hash("e") },
	} {
		changed := request
		mutate(&changed)
		if err := validateClassifiedAuthorityRepairRequest(changed, assessment); err == nil {
			t.Fatalf("non-exact repair request accepted: %#v", changed)
		}
	}
	if !errors.Is(&AuthorityLockCancelledError{Cause: context.Canceled}, context.Canceled) {
		t.Fatal("test precondition: cancellation cause not preserved")
	}
}

func TestRepairClassifiedAuthorityCommitsAndReplaysExactAssessment(t *testing.T) {
	repo := initSnapshotRepo(t)
	store, _, _ := legacyAliasRepairFixture(t, repo, "classified-execute")
	assessment, err := AssessAuthorityRepair(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	request := classifiedAuthorityRepairRequest(t, assessment)
	committed, err := RepairClassifiedAuthority(context.Background(), repo, request)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Status != "committed" || committed.Class != assessment.Class || committed.Cause != assessment.Cause ||
		committed.Disposition != assessment.Disposition || committed.LineageID != request.LineageID ||
		committed.Revision != request.ExpectedRevision || committed.ChainIdentity != assessment.Candidate.ChainIdentity {
		t.Fatalf("classified repair execution = %#v", committed)
	}
	if _, err := os.Stat(store.Dir); !os.IsNotExist(err) {
		t.Fatalf("classified repair source remains: %v", err)
	}
	replayed, err := RepairClassifiedAuthority(context.Background(), repo, request)
	if err != nil || !reflect.DeepEqual(replayed, committed) {
		t.Fatalf("classified repair replay = %#v, %v", replayed, err)
	}
}

func TestRepairClassifiedAuthorityRechecksHeadCASBeforeMutation(t *testing.T) {
	repo := initSnapshotRepo(t)
	store, _, _ := legacyAliasRepairFixture(t, repo, "classified-stale-head")
	assessment, err := AssessAuthorityRepair(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	request := classifiedAuthorityRepairRequest(t, assessment)
	originalHook := classifiedAuthorityRepairAfterAssessmentHook
	t.Cleanup(func() { classifiedAuthorityRepairAfterAssessmentHook = originalHook })
	classifiedAuthorityRepairAfterAssessmentHook = func() {
		record, _, loadErr := store.loadRevision(request.ExpectedRevision)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		next := record.Transaction
		if beginErr := next.BeginFinalVerification(); beginErr != nil {
			t.Fatal(beginErr)
		}
		writeStoreEvent(t, store, Record{Operation: "review/begin-final-verification", PreviousRevision: request.ExpectedRevision, Transaction: next})
	}
	if _, err := RepairClassifiedAuthority(context.Background(), repo, request); !errors.Is(err, ErrConcurrentUpdate) {
		t.Fatalf("stale classified repair = %v", err)
	}
	if _, err := os.Stat(store.Dir); err != nil {
		t.Fatalf("stale classified repair moved source: %v", err)
	}
}

func TestRepairClassifiedAuthorityRechecksCompleteInventoryUnderMaintenanceCAS(t *testing.T) {
	repo := initSnapshotRepo(t)
	store, _, _ := legacyAliasRepairFixture(t, repo, "classified-global-cas")
	assessment, err := AssessAuthorityRepair(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	request := classifiedAuthorityRepairRequest(t, assessment)
	originalHook := classifiedAuthorityRepairAfterAssessmentHook
	t.Cleanup(func() { classifiedAuthorityRepairAfterAssessmentHook = originalHook })
	classifiedAuthorityRepairAfterAssessmentHook = func() {
		legacyAliasRepairFixture(t, repo, "classified-racing-candidate")
	}
	if _, err := RepairClassifiedAuthority(context.Background(), repo, request); !errors.Is(err, ErrConcurrentUpdate) {
		t.Fatalf("classified repair accepted a newly ambiguous inventory: %v", err)
	}
	if _, err := os.Stat(store.Dir); err != nil {
		t.Fatalf("ambiguous classified repair moved original source: %v", err)
	}
}

func TestRepairClassifiedAuthorityConcurrentExecutionCommitsAndReplays(t *testing.T) {
	repo := initSnapshotRepo(t)
	legacyAliasRepairFixture(t, repo, "classified-concurrent")
	assessment, err := AssessAuthorityRepair(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	request := classifiedAuthorityRepairRequest(t, assessment)
	originalHook := classifiedAuthorityRepairAfterAssessmentHook
	t.Cleanup(func() { classifiedAuthorityRepairAfterAssessmentHook = originalHook })
	ready := make(chan struct{})
	release := make(chan struct{})
	var arrivals atomic.Int32
	classifiedAuthorityRepairAfterAssessmentHook = func() {
		if arrivals.Add(1) == 2 {
			close(ready)
		}
		<-release
	}
	results := make(chan ClassifiedAuthorityRepairExecution, 2)
	errorsOut := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	for range 2 {
		go func() {
			defer workers.Done()
			result, runErr := RepairClassifiedAuthority(context.Background(), repo, request)
			results <- result
			errorsOut <- runErr
		}()
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent classified repairs did not finish assessment")
	}
	close(release)
	workers.Wait()
	first, second := <-results, <-results
	for range 2 {
		if err := <-errorsOut; err != nil {
			t.Fatalf("concurrent classified repair: %v", err)
		}
	}
	if !reflect.DeepEqual(first, second) || first.Status != "committed" {
		t.Fatalf("concurrent classified results = %#v / %#v", first, second)
	}
}

func classifiedAuthorityRepairRequest(t *testing.T, assessment AuthorityRepairAssessment) ClassifiedAuthorityRepairRequest {
	t.Helper()
	if assessment.Status != AuthorityRepairEligible || assessment.Candidate == nil {
		t.Fatalf("repair assessment is not eligible: %#v", assessment)
	}
	request := ClassifiedAuthorityRepairRequest{
		Class: assessment.Class, LineageID: assessment.Candidate.LineageID, ExpectedRevision: assessment.Candidate.Revision,
		Cause: assessment.Cause, Disposition: assessment.Disposition, RepositoryBinding: assessment.RepositoryBinding,
		Actor: "maintainer@example.com", Reason: "quarantine the approved historical alias",
	}
	request.MaintainerAuthorization = authorityRepairAuthorizationBinding(request)
	return request
}
