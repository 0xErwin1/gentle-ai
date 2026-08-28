package sddstatus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #3816 / #2833: the store used to commit before it verified. publishRecord
// wrote the record, publishHead ADVANCED HEAD, and only then did the chain
// replay -- so a record the store's own validator rejects was already on the
// chain, and every later read walked into it. That is the wedge class: a drift
// bug became a dead end rather than a refusal.
//
// The record must now replay as a candidate BEFORE HEAD moves.

// corruptNewestRecordOnSync corrupts the record that was just written, at the
// moment its directory is synced, so the candidate replay must reject it. This
// uses the seam the ledger already exposes rather than adding one.
func corruptNewestRecordOnSync(t *testing.T, store RuntimeStore) {
	t.Helper()
	recordsDir := filepath.Join(store.Dir, "records")
	original := runtimeSyncDirectory
	runtimeSyncDirectory = func(path string) error {
		if filepath.Clean(path) == filepath.Clean(recordsDir) {
			entries, err := os.ReadDir(recordsDir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if !strings.HasSuffix(entry.Name(), ".json") {
					continue
				}
				full := filepath.Join(recordsDir, entry.Name())
				info, statErr := os.Stat(full)
				if statErr != nil || info.Size() == 0 {
					continue
				}
				if writeErr := os.WriteFile(full, []byte("{}\n"), 0o600); writeErr != nil {
					return writeErr
				}
			}
		}
		return original(path)
	}
	t.Cleanup(func() { runtimeSyncDirectory = original })
}

func readRuntimeHeadRevision(t *testing.T, store RuntimeStore) string {
	t.Helper()
	head, exists, err := readRuntimeHead(filepath.Join(store.Dir, "HEAD"))
	if err != nil {
		t.Fatalf("read HEAD: %v", err)
	}
	if !exists {
		return ""
	}
	return head
}

// TestRejectedRecordNeverReachesHead pins the reordering: when the candidate
// chain does not replay, HEAD stays exactly where it was and the failure is
// not reported as committed.
func TestRejectedRecordNeverReachesHead(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, "verify-before-commit")
	if err != nil {
		t.Fatal(err)
	}

	before := readRuntimeHeadRevision(t, store)
	corruptNewestRecordOnSync(t, store)

	_, err = store.Begin(context.Background(), BeginAttemptRequest{
		ExpectedRevision: before, RequestID: "verify-first-begin", WorkUnit: "work",
		EvidenceGoal: "prove the candidate is verified before HEAD moves",
		MaxAttempts:  2, MaxChangedLines: 90,
	})
	if err == nil {
		t.Fatal("a record that cannot replay was committed")
	}

	var publication *RuntimePublicationError
	if errors.As(err, &publication) && publication.Committed {
		t.Errorf("rejected record reported as committed: %v", err)
	}
	if after := readRuntimeHeadRevision(t, store); after != before {
		t.Errorf("HEAD advanced to %q despite a rejected record (was %q)", after, before)
	}
}
