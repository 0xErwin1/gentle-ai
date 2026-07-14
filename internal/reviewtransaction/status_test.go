package reviewtransaction

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInventoryAuthorityReportsActiveMalformedAndMixedCollisionWithoutMutation(t *testing.T) {
	repo := initSnapshotRepo(t)
	state := newCompactTestState(t, repo, "active-lineage")
	store, err := CompactAuthoritativeStore(context.Background(), repo, state.LineageID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Replace("", "review/start", state); err != nil {
		t.Fatal(err)
	}
	root, _, err := reviewAuthorityRoot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "v1", "active-lineage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "v1", "active-lineage", "HEAD"), []byte("not-a-revision\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "v2", "broken-lineage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "v2", "broken-lineage", "review-state.json"), []byte("{broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := authorityBytes(t, root)

	report, err := InventoryAuthority(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Schema != ReviewAuthorityStatusSchema || report.Complete {
		t.Fatalf("report header = %#v", report)
	}
	if !hasAuthorityInventoryStatus(report.Entries, "active-lineage", AuthorityStatusCollision) ||
		!hasAuthorityInventoryStatus(report.Entries, "broken-lineage", AuthorityStatusInvalid) {
		t.Fatalf("inventory entries = %#v", report.Entries)
	}
	if after := authorityBytes(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("read-only authority inventory changed authority bytes")
	}
}

func TestInventoryAuthorityReportsResetResidueAndOwnedLock(t *testing.T) {
	repo := initSnapshotRepo(t)
	root, _, err := reviewAuthorityRoot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	lineage := filepath.Join(root, "v2", "reset-lineage")
	if err := os.MkdirAll(lineage, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lineage, ".atomic-interrupted"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock := `{"schema":"gentle-ai.review-store-lock/v1","owner_id":"owner","pid":42,"host":"test-host","acquired_at":"2026-07-14T00:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "v2", "LOCK"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "v1", "ambiguous-lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "v1", "ambiguous-lock", "LOCK"), []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := InventoryAuthority(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Complete || report.Authoritative || !hasAuthorityInventoryStatus(report.Entries, "reset-lineage", AuthorityStatusReset) {
		t.Fatalf("reset report = %#v", report)
	}
	if len(report.Locks) != 2 || report.Locks[1].Status != AuthorityLockOwned || report.Locks[1].Owner == nil || report.Locks[1].Owner.OwnerID != "owner" ||
		report.Locks[0].Status != AuthorityLockAmbiguous || report.Locks[0].Problem == "" {
		t.Fatalf("lock evidence = %#v", report.Locks)
	}
}

func TestInventoryAuthorityRejectsStructurallyValidReceiptsThatMismatchTerminalAuthority(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		lineage string
		write   func(t *testing.T, repo string)
	}{
		{
			name:    "compact",
			lineage: "compact-stale-receipt",
			write: func(t *testing.T, repo string) {
				t.Helper()
				_, store, receipt := approvedCompactCurrentChangesFixture(t, repo, "compact-stale-receipt", []string{})
				receipt.PolicyHash = "sha256:" + strings.Repeat("a", 64)
				if err := WriteCompactReceiptAtomic(store.ReceiptPath(), receipt); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "legacy",
			lineage: "legacy-stale-receipt",
			write: func(t *testing.T, repo string) {
				t.Helper()
				transaction, receipt, _ := nativeGateFixture(t, repo, "legacy-stale-receipt")
				store, err := AuthoritativeStore(context.Background(), repo, transaction.LineageID)
				if err != nil {
					t.Fatal(err)
				}
				appendApprovedStoreChain(t, store, transaction)
				receipt.PolicyHash = "sha256:" + strings.Repeat("a", 64)
				if err := WriteReceiptAtomic(filepath.Join(store.Dir, "artifacts", "receipt.json"), receipt); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			repo := initSnapshotRepo(t)
			fixture.write(t, repo)

			report, err := InventoryAuthority(context.Background(), repo)
			if err != nil {
				t.Fatal(err)
			}
			if report.Complete || report.Authoritative || report.Status != AuthorityStatusInvalid || !hasAuthorityInventoryStatus(report.Entries, fixture.lineage, AuthorityStatusInvalid) {
				t.Fatalf("mismatched receipt report = %#v", report)
			}
		})
	}
}

func TestInventoryAuthorityRejectsAmbiguousLockEvidence(t *testing.T) {
	repo := initSnapshotRepo(t)
	root, _, err := reviewAuthorityRoot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, "v2", "LOCK")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := InventoryAuthority(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Complete || report.Authoritative || report.Status != AuthorityStatusInvalid || len(report.Locks) != 1 || report.Locks[0].Status != AuthorityLockAmbiguous {
		t.Fatalf("ambiguous lock report = %#v", report)
	}
}

func TestInventoryAuthorityReportsRecoveredSuccessorAndSupersededPredecessor(t *testing.T) {
	repo := initSnapshotRepo(t)
	writeSnapshotFile(t, repo, "tracked.txt", "candidate\n")
	predecessor, store, _ := approvedCompactCurrentChangesFixture(t, repo, "recovery-predecessor", []string{})
	record, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	writeSnapshotFile(t, repo, "tracked.txt", "successor candidate\n")
	successor := newCompactTestState(t, repo, "recovery-successor")
	successor.Generation = predecessor.Generation + 1
	recovered, err := RecoverCompactAuthority(context.Background(), repo, CompactRecoveryRequest{
		PredecessorLineageID: predecessor.LineageID, ExpectedPredecessorRevision: record.Revision,
		Successor: successor, Disposition: RecoveryScopeChanged, Reason: "scope changed", Actor: "maintainer",
	})
	if err != nil {
		t.Fatal(err)
	}

	report, err := InventoryAuthority(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || !hasAuthorityInventoryStatus(report.Entries, predecessor.LineageID, AuthorityStatusSuperseded) ||
		!hasAuthorityInventoryStatus(report.Entries, successor.LineageID, AuthorityStatusRecovered) {
		t.Fatalf("recovery report = %#v", report)
	}
	recovered.State.Recovery.Disposition = RecoveryInvalidated
	_, payload, err := makeCompactRecord(recovered.State)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(store.Dir), successor.LineageID, "review-state.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	report, err = InventoryAuthority(context.Background(), repo)
	if err != nil || report.Complete || !hasAuthorityInventoryStatus(report.Entries, successor.LineageID, AuthorityStatusInvalid) {
		t.Fatalf("invalidated disposition report = %#v, %v", report, err)
	}
}

func TestInventoryAuthorityReportsRecoveredInvalidatedSuccessorAndSupersededPredecessor(t *testing.T) {
	repo := initSnapshotRepo(t)
	predecessor := newCompactTestState(t, repo, "invalidated-recovery-predecessor")
	store, err := CompactAuthoritativeStore(context.Background(), repo, predecessor.LineageID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := store.Replace("", "review/start", predecessor)
	if err != nil {
		t.Fatal(err)
	}
	if err := predecessor.Invalidate("candidate no longer applies"); err != nil {
		t.Fatal(err)
	}
	revision, err = store.Replace(revision, "review/invalidate", predecessor)
	if err != nil {
		t.Fatal(err)
	}

	successor := newCompactTestState(t, repo, "invalidated-recovery-successor")
	successor.Generation = predecessor.Generation + 1
	if _, err := RecoverCompactAuthority(context.Background(), repo, CompactRecoveryRequest{
		PredecessorLineageID: predecessor.LineageID, ExpectedPredecessorRevision: revision,
		Successor: successor, Disposition: RecoveryInvalidated, Reason: "verification invalidated authority", Actor: "maintainer",
	}); err != nil {
		t.Fatal(err)
	}

	report, err := InventoryAuthority(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || !report.Authoritative || !hasAuthorityInventoryStatus(report.Entries, predecessor.LineageID, AuthorityStatusSuperseded) ||
		!hasAuthorityInventoryStatus(report.Entries, successor.LineageID, AuthorityStatusRecovered) {
		t.Fatalf("invalidated recovery report = %#v", report)
	}
}

func authorityBytes(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[rel] = payload
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func hasAuthorityInventoryStatus(entries []AuthorityInventoryEntry, lineage string, status AuthorityStatus) bool {
	for _, entry := range entries {
		if entry.LineageID == lineage && entry.Status == status {
			return true
		}
	}
	return false
}
