package reviewtransaction

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func acquisitionTestStore(t *testing.T, lineage string) (CompactStore, CompactState) {
	t.Helper()
	repo := initSnapshotRepo(t)
	writeSnapshotFile(t, repo, "review.go", "package review\n")
	state := newCompactTestStateWithIntended(t, repo, lineage, []string{"review.go"})
	if len(state.SelectedLenses) == 0 {
		t.Fatal("acquisition fixture requires a selected lens")
	}
	return storeCompactStartAuthority(t, repo, state), state
}

func TestAcquireReviewerResult(t *testing.T) {
	t.Run("publishes an immutable binding-bound unknown outcome", func(t *testing.T) {
		store, state := acquisitionTestStore(t, "acquire-first")
		got, err := store.AcquireReviewerResult(context.Background(), state.InitialSnapshot.Identity, state.SelectedLenses[0], 0)
		if err != nil {
			t.Fatal(err)
		}
		if got.Schema != CompactResultAcquisitionSchema || got.ID == "" || got.LineageID != state.LineageID ||
			got.TargetIdentity != state.InitialSnapshot.Identity || got.Lens != state.SelectedLenses[0] ||
			got.SelectedOrder != 0 || got.State != CompactResultAcquisitionUnknownOutcome {
			t.Fatalf("acquisition = %#v", got)
		}
		payload, err := os.ReadFile(filepath.Join(store.Dir, CompactReviewerAcquisitionsDir, "00-"+state.SelectedLenses[0]+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(payload), got.ID) {
			t.Fatalf("durable acquisition does not contain generated ID %q", got.ID)
		}
	})

	t.Run("rejects stale binding without writing", func(t *testing.T) {
		store, state := acquisitionTestStore(t, "acquire-stale")
		if _, err := store.AcquireReviewerResult(context.Background(), "stale-target", state.SelectedLenses[0], 0); err == nil {
			t.Fatal("stale acquisition succeeded")
		}
		if _, err := os.Stat(filepath.Join(store.Dir, CompactReviewerAcquisitionsDir)); !os.IsNotExist(err) {
			t.Fatalf("stale acquisition wrote durable state: %v", err)
		}
	})

	t.Run("allows exactly one concurrent acquisition", func(t *testing.T) {
		store, state := acquisitionTestStore(t, "acquire-concurrent")
		start := make(chan struct{})
		results := make(chan error, 2)
		var group sync.WaitGroup
		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				_, err := store.AcquireReviewerResult(context.Background(), state.InitialSnapshot.Identity, state.SelectedLenses[0], 0)
				results <- err
			}()
		}
		close(start)
		group.Wait()
		close(results)
		successes := 0
		for err := range results {
			if err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("concurrent acquisition successes = %d, want 1", successes)
		}
	})

	t.Run("rejects a captured slot without writing", func(t *testing.T) {
		store, state := acquisitionTestStore(t, "acquire-captured")
		resultsDir := filepath.Join(store.Dir, CompactReviewerResultsDir)
		if err := os.MkdirAll(resultsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		captured := filepath.Join(resultsDir, "00-"+state.SelectedLenses[0]+".json")
		if err := os.WriteFile(captured, []byte(`{"findings":[],"evidence":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcquireReviewerResult(context.Background(), state.InitialSnapshot.Identity, state.SelectedLenses[0], 0); err == nil {
			t.Fatal("acquisition over an already-captured slot succeeded")
		}
		if _, err := os.Stat(filepath.Join(store.Dir, CompactReviewerAcquisitionsDir)); !os.IsNotExist(err) {
			t.Fatalf("captured-slot rejection wrote durable acquisition state: %v", err)
		}
	})

	t.Run("rejects a repeated acquisition and preserves the original crash-safe record", func(t *testing.T) {
		store, state := acquisitionTestStore(t, "acquire-repeat")
		target, lens := state.InitialSnapshot.Identity, state.SelectedLenses[0]
		first, err := store.AcquireReviewerResult(context.Background(), target, lens, 0)
		if err != nil {
			t.Fatal(err)
		}
		// A crash after launch but before capture leaves exactly this record; an
		// operator retry of the identical binding must observe it and refuse to
		// launch a second task rather than silently reacquiring.
		if _, err := store.AcquireReviewerResult(context.Background(), target, lens, 0); err == nil {
			t.Fatal("repeated acquisition after the first succeeded")
		}
		again, err := store.ReadResultAcquisition(state.LineageID, target, lens, 0, first.ID)
		if err != nil {
			t.Fatalf("original acquisition unreadable after rejected repeat: %v", err)
		}
		if again.ID != first.ID || again.State != CompactResultAcquisitionUnknownOutcome || !again.AcquiredAt.Equal(first.AcquiredAt) {
			t.Fatalf("original acquisition changed after a rejected repeat: got %#v, want %#v", again, first)
		}
	})
}

// TestReadResultAcquisition pins the terminal-operation binding contract:
// capture-result and dispose-result must present the exact acquisition ID
// bound to the exact lineage, target, lens, and order — any mismatch, a
// missing acquisition, or cross-slot reuse is refused identically.
func TestReadResultAcquisition(t *testing.T) {
	store, state := acquisitionTestStore(t, "acquire-read")
	target, lens := state.InitialSnapshot.Identity, state.SelectedLenses[0]
	acquisition, err := store.AcquireReviewerResult(context.Background(), target, lens, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadResultAcquisition(state.LineageID, target, lens, 0, acquisition.ID)
	if err != nil || got.ID != acquisition.ID || got.State != CompactResultAcquisitionUnknownOutcome {
		t.Fatalf("ReadResultAcquisition(exact binding) = %#v, %v", got, err)
	}
	staleID := "acq-" + strings.Repeat("0", 64)
	cases := []struct {
		name    string
		lineage string
		target  string
		lens    string
		order   int
		id      string
	}{
		{"empty id", state.LineageID, target, lens, 0, ""},
		{"stale id", state.LineageID, target, lens, 0, staleID},
		{"wrong lineage", "other-lineage", target, lens, 0, acquisition.ID},
		{"wrong target", state.LineageID, "sha256:" + strings.Repeat("0", 64), lens, 0, acquisition.ID},
		{"wrong lens", state.LineageID, target, "review-risk", 0, acquisition.ID},
		{"wrong order (no acquisition on that slot)", state.LineageID, target, lens, 1, acquisition.ID},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := store.ReadResultAcquisition(tt.lineage, tt.target, tt.lens, tt.order, tt.id); err == nil {
				t.Fatalf("ReadResultAcquisition(%q) accepted a mismatched binding", tt.name)
			}
		})
	}
}
