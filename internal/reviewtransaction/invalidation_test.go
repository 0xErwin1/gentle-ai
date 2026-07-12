package reviewtransaction

import (
	"context"
	"errors"
	"testing"
)

func TestInvalidatePristineLegacyAndCompactAreTerminalAndIdempotent(t *testing.T) {
	repo := initSnapshotRepo(t)
	writeSnapshotFile(t, repo, "tracked.txt", "candidate\n")

	legacy, err := AuthoritativeStore(context.Background(), repo, "invalidate-legacy")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := (SnapshotBuilder{Repo: repo}).Build(context.Background(), Target{Kind: TargetCurrentChanges, IntendedUntracked: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := NewTransaction(Start{LineageID: "invalidate-legacy", Mode: ModeOrdinary4R, Generation: 1, Snapshot: snapshot, PolicyHash: hash("a")})
	if err != nil || transaction.StartReview() != nil {
		t.Fatalf("create legacy review: %v", err)
	}
	genesis, err := legacy.Append("", Record{Operation: "review/start", Transaction: *transaction})
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ = AuthoritativeStore(context.Background(), repo, transaction.LineageID)
	invalidated, err := legacy.InvalidatePristine(genesis, "operator abandoned", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := legacy.InvalidatePristine(genesis, "operator abandoned", snapshot); err != nil || retry != invalidated {
		t.Fatalf("legacy exact retry = %q, %v", retry, err)
	}
	if _, err := legacy.InvalidatePristine(genesis, "different reason", snapshot); !errors.Is(err, ErrConcurrentUpdate) {
		t.Fatalf("legacy conflicting retry error = %v", err)
	}
	if record, _, err := legacy.Load(); err != nil || record.Transaction.State != StateInvalidated || record.Transaction.InvalidationReason != "operator abandoned" {
		t.Fatalf("legacy invalidation = %#v, %v", record.Transaction, err)
	}

	compact := newCompactTestState(t, repo, "invalidate-compact")
	compactStore, _ := CompactAuthoritativeStore(context.Background(), repo, compact.LineageID)
	revision, err := compactStore.Replace("", "review/start", compact)
	if err != nil {
		t.Fatal(err)
	}
	if err := compact.Invalidate("operator abandoned"); err != nil {
		t.Fatal(err)
	}
	invalidated, err = compactStore.Replace(revision, "review/invalidate", compact)
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := compactStore.Replace(revision, "review/invalidate", compact); err != nil || retry != invalidated {
		t.Fatalf("compact exact retry = %q, %v", retry, err)
	}
	if _, err := compact.Receipt(); err == nil {
		t.Fatal("invalidated compact review produced a receipt")
	}
}

func TestInvalidatePristineRejectsLiveCandidateDriftWithoutMutation(t *testing.T) {
	for _, mutate := range []struct {
		name  string
		apply func(t *testing.T, repo string)
	}{
		{"unstaged", func(t *testing.T, repo string) { writeSnapshotFile(t, repo, "tracked.txt", "changed after review\n") }},
		{"staged", func(t *testing.T, repo string) {
			writeSnapshotFile(t, repo, "tracked.txt", "changed after review\n")
			gitSnapshot(t, repo, "add", "tracked.txt")
		}},
		{"commit-a", func(t *testing.T, repo string) {
			writeSnapshotFile(t, repo, "tracked.txt", "changed after review\n")
			gitSnapshot(t, repo, "commit", "-am", "drift")
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			repo := initSnapshotRepo(t)
			writeSnapshotFile(t, repo, "tracked.txt", "candidate\n")
			state := newCompactTestState(t, repo, "invalidate-drift-"+mutate.name)
			store, _ := CompactAuthoritativeStore(context.Background(), repo, state.LineageID)
			revision, err := store.Replace("", "review/start", state)
			if err != nil {
				t.Fatal(err)
			}
			mutate.apply(t, repo)
			if err := state.Invalidate("operator abandoned"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Replace(revision, "review/invalidate", state); err == nil {
				t.Fatal("live candidate drift was accepted")
			}
			loaded, err := store.Load()
			if err != nil || loaded.Revision != revision || loaded.State.State != StateReviewing {
				t.Fatalf("drift changed compact authority: %#v, %v", loaded, err)
			}
		})
	}
}
