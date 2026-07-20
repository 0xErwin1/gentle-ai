package reviewtransaction

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// legacyFixScopeFixture reproduces the three shipped Pi complete-fix shapes:
// two terminal events and one event followed by its validation successor.
func legacyFixScopeFixture(t *testing.T, repo, lineage string, addedPaths []string, successor bool, overrides ...func(*Transaction)) (Store, string, string) {
	t.Helper()
	store, err := AuthoritativeStore(context.Background(), repo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	tx := newTestTransaction(t, ModeOrdinary4R)
	tx.LineageID = lineage
	// v1 records predate the persisted genesis_paths field. The immutable
	// frozen scope is therefore the genesis snapshot's path list.
	tx.GenesisPaths = nil
	if err := tx.StartReview(); err != nil {
		t.Fatal(err)
	}
	head := writeStoreEvent(t, store, Record{Operation: "review/start", Transaction: *tx})
	if err := freezeTestFindings(tx, []Finding{{ID: "R1-SCOPE", Severity: "CRITICAL"}}); err != nil {
		t.Fatal(err)
	}
	head = writeStoreEvent(t, store, Record{Operation: "review/freeze-findings", PreviousRevision: head, Transaction: *tx})
	if _, err := tx.ClassifyEvidence([]FindingEvidence{{FindingID: "R1-SCOPE", Class: EvidenceDeterministic, Causality: CausalIntroduced, Proof: "historical proof"}}); err != nil {
		t.Fatal(err)
	}
	head = writeStoreEvent(t, store, Record{Operation: "review/classify-evidence", PreviousRevision: head, Transaction: *tx})
	if err := tx.BeginFix(hash("a")); err != nil {
		t.Fatal(err)
	}
	head = writeStoreEvent(t, store, Record{Operation: "review/begin-fix", PreviousRevision: head, Transaction: *tx})

	fix := tx.Snapshot
	fix.Kind = TargetFixDiff
	fix.BaseTree = tx.FinalCandidateTree
	fix.CandidateTree = tree("c")
	fix.LedgerIDs = []string{"R1-SCOPE"}
	fix.Paths, err = canonicalPaths(append(append([]string{}, fix.Paths...), addedPaths...))
	if err != nil {
		t.Fatal(err)
	}
	fix.PathsDigest = digestPaths(fix.Paths)
	fix.Identity = hash("c")
	// Persist the historical transition exactly as v1 did: its content is
	// individually decodable and hash-valid, but the new paths exceed genesis.
	completed := *tx
	completed.Snapshot = fix
	completed.FinalCandidateTree = fix.CandidateTree
	completed.FixDeltaHash = FixDeltaHashForSnapshot(fix)
	completed.State = StateFixValidating
	completed.GenesisPaths = nil
	for _, override := range overrides {
		if override != nil {
			override(&completed)
		}
	}
	completeEvent := writeStoreEvent(t, store, Record{Operation: "review/complete-fix", PreviousRevision: head, Transaction: completed})
	head = completeEvent
	if successor {
		validated := historicalValidationTransition(completed)
		head = writeStoreEvent(t, store, Record{Operation: "review/validate-fix", PreviousRevision: head, Transaction: validated})
		if err := validated.BeginFinalVerification(); err != nil {
			t.Fatal(err)
		}
		head = writeStoreEvent(t, store, Record{Operation: "review/begin-final-verification", PreviousRevision: head, Transaction: validated})
		if err := validated.CompleteFinalVerification(hash("b"), true); err != nil {
			t.Fatal(err)
		}
		head = writeStoreEvent(t, store, Record{Operation: "review/complete-final-verification", PreviousRevision: head, Transaction: validated})
	}
	return store, head, completeEvent
}

func legacyFixScopeQuarantineRequest(t *testing.T, repo, lineage, revision string) LegacyFixScopeQuarantineRequest {
	t.Helper()
	_, repository, err := reviewAuthorityRoot(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	request := LegacyFixScopeQuarantineRequest{
		LineageID: lineage, ExpectedRevision: revision,
		ExpectedDiagnostic: "historical complete-fix expanded correction paths beyond frozen genesis scope",
		Disposition:        LegacyFixScopeQuarantineDisposition,
		Reason:             "quarantine exact historical complete-fix scope expansion", Actor: "maintainer@example.com",
	}
	request.MaintainerAuthorization = legacyFixScopeQuarantineAuthorizationBinding(
		repository, request.LineageID, request.ExpectedRevision, request.ExpectedDiagnostic,
		request.Disposition, request.Actor, request.Reason,
	)
	return request
}

func TestQuarantineHistoricalLegacyFixScopeReproducesEveryPiShape(t *testing.T) {
	tests := []struct {
		name       string
		lineage    string
		paths      []string
		successor  bool
		wantExpand []string
	}{
		{name: "complete-target alias at HEAD", lineage: "issue-1099-complete-target-review-20260710", paths: []string{"lib/sdd-preflight.ts"}, wantExpand: []string{"lib/sdd-preflight.ts"}},
		{name: "final-complete alias at HEAD", lineage: "issue-1099-final-complete-review-20260710", paths: []string{"assets/agents/gentle-ai-worker.md"}, wantExpand: []string{"assets/agents/gentle-ai-worker.md"}},
		{name: "corrected-candidate alias with terminal successor", lineage: "issue-1099-corrected-candidate-review-20260710", paths: []string{"extensions/gentle-ai.ts", "tests/gentle-ai.test.ts"}, successor: true, wantExpand: []string{"extensions/gentle-ai.ts", "tests/gentle-ai.test.ts"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := initSnapshotRepo(t)
			approved, approvedStore := approvedCompactFixture(t, repo, "unrelated-approved")
			store, head, event := legacyFixScopeFixture(t, repo, tt.lineage, tt.paths, tt.successor)
			request := legacyFixScopeQuarantineRequest(t, repo, tt.lineage, head)
			eventPath := filepath.Join(store.Dir, "events", strings.TrimPrefix(event, "sha256:")+".json")
			eventBytes, err := os.ReadFile(eventPath)
			if err != nil {
				t.Fatal(err)
			}

			committed, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, request)
			if err != nil {
				t.Fatalf("quarantine historical complete-fix scope: %v", err)
			}
			proof := committed.LegacyFixScopeQuarantine
			if committed.Status != CompactReclaimCommitted || proof == nil || proof.EventRevision != event ||
				proof.HeadRevision != head || proof.Operation != "review/complete-fix" ||
				proof.FixDeltaHash == EmptyFixDeltaHash || !equalStrings(proof.ExpandedCorrectionPaths, tt.wantExpand) ||
				proof.Diagnostic != request.ExpectedDiagnostic || proof.Disposition != request.Disposition {
				t.Fatalf("quarantine record = %#v", committed)
			}
			moved, err := os.ReadFile(filepath.Join(committed.QuarantinePath, "residue", "events", filepath.Base(eventPath)))
			if err != nil || !bytes.Equal(moved, eventBytes) {
				t.Fatalf("quarantined event = %q, %v", moved, err)
			}
			if _, err := os.Stat(store.Dir); !os.IsNotExist(err) {
				t.Fatalf("legacy source remains: %v", err)
			}
			if _, err := os.Stat(approvedStore.StatePath()); err != nil {
				t.Fatalf("unrelated approved authority changed: %v", err)
			}
			report, err := InventoryAuthority(context.Background(), repo)
			if err != nil || !report.Complete || !report.Authoritative || !hasAuthorityInventoryStatus(report.Entries, approved.State.LineageID, AuthorityStatusApproved) {
				t.Fatalf("post-quarantine inventory = %#v, %v", report, err)
			}

			replayed, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, request)
			if err != nil || replayed.QuarantinePath != committed.QuarantinePath {
				t.Fatalf("idempotent replay = %#v, %v", replayed, err)
			}
		})
	}
}

func TestQuarantineHistoricalLegacyFixScopeRefusesOutsideExactClass(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, store Store, request *LegacyFixScopeQuarantineRequest)
		want    string
	}{
		{name: "stale CAS", prepare: func(_ *testing.T, _ Store, request *LegacyFixScopeQuarantineRequest) {
			request.ExpectedRevision = hash("f")
		}, want: ErrConcurrentUpdate.Error()},
		{name: "malformed LF authorization", prepare: func(_ *testing.T, _ Store, request *LegacyFixScopeQuarantineRequest) {
			request.MaintainerAuthorization += "\n"
		}, want: "exact maintainer authorization binding"},
		{name: "multiline authorization field", prepare: func(_ *testing.T, _ Store, request *LegacyFixScopeQuarantineRequest) {
			request.Reason += "\nextra=field"
			request.MaintainerAuthorization = legacyFixScopeQuarantineAuthorizationBinding("repository", request.LineageID, request.ExpectedRevision, request.ExpectedDiagnostic, request.Disposition, request.Actor, request.Reason)
		}, want: "LF-only single-line actor and reason"},
		{name: "wrong diagnostic", prepare: func(_ *testing.T, _ Store, request *LegacyFixScopeQuarantineRequest) {
			request.ExpectedDiagnostic = "scope bypass"
		}, want: "supports only"},
		{name: "wrong disposition", prepare: func(_ *testing.T, _ Store, request *LegacyFixScopeQuarantineRequest) {
			request.Disposition = "rewrite-history"
		}, want: "supports only"},
		{name: "mixed compact collision", prepare: func(t *testing.T, _ Store, request *LegacyFixScopeQuarantineRequest) {
			compact, err := CompactAuthoritativeStore(context.Background(), t.TempDir(), request.LineageID)
			if err == nil {
				_ = compact
			}
		}, want: "also exists in compact-v2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := initSnapshotRepo(t)
			store, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-refusal", []string{"lib/scope.go"}, false)
			request := legacyFixScopeQuarantineRequest(t, repo, "legacy-fix-scope-refusal", head)
			if tt.name == "mixed compact collision" {
				compact, err := CompactAuthoritativeStore(context.Background(), repo, request.LineageID)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(compact.Dir, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				tt.prepare(t, store, &request)
			}
			_, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, request)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("refusal error = %v, want %q", err, tt.want)
			}
			if _, err := os.Stat(store.Dir); err != nil {
				t.Fatalf("refused request moved source: %v", err)
			}
		})
	}

	t.Run("repair legacy alias refuses scope expansion", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		_, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-alias", []string{"lib/scope.go"}, false)
		request := legacyAliasRepairRequest(t, repo, "legacy-fix-scope-alias", head)
		if _, err := RepairHistoricalLegacyAlias(context.Background(), repo, request); err == nil || !strings.Contains(err.Error(), "unsupported successor anomaly") {
			t.Fatalf("alias-only repair error = %v", err)
		}
	})

	for _, tt := range []struct {
		name     string
		paths    []string
		override func(*Transaction)
		want     string
	}{
		{name: "no scope expansion", want: "no supported historical complete-fix scope expansion"},
		{name: "malformed expansion path", paths: []string{"lib/scope.go"}, override: func(next *Transaction) {
			next.Snapshot.Paths = []string{"../escape"}
			next.FixDeltaHash = FixDeltaHashForSnapshot(next.Snapshot)
		}, want: "unsupported successor anomaly"},
		{name: "other profile", paths: []string{"lib/scope.go"}, override: func(next *Transaction) { next.Mode = ModeJudgmentDay }, want: "load revision"},
		{name: "other state", paths: []string{"lib/scope.go"}, override: func(next *Transaction) { next.State = StateReadyFinalVerification }, want: "load revision"},
		{name: "corrupted fix delta", paths: []string{"lib/scope.go"}, override: func(next *Transaction) { next.FixDeltaHash = hash("f") }, want: "unsupported successor anomaly"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := initSnapshotRepo(t)
			store, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-"+strings.ReplaceAll(tt.name, " ", "-"), tt.paths, false, tt.override)
			request := legacyFixScopeQuarantineRequest(t, repo, store.lineageID, head)
			if _, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, request); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("refusal error = %v, want %q", err, tt.want)
			}
			if _, err := os.Stat(store.Dir); err != nil {
				t.Fatalf("refused request moved source: %v", err)
			}
		})
	}

	t.Run("unknown terminal successor", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		store, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-unknown", []string{"lib/scope.go"}, false)
		current, _, err := store.loadRevision(head)
		if err != nil {
			t.Fatal(err)
		}
		terminal := historicalValidationTransition(current.Transaction)
		head = writeStoreEvent(t, store, Record{Operation: "review/unknown-fix", PreviousRevision: head, Transaction: terminal})
		if _, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, legacyFixScopeQuarantineRequest(t, repo, "legacy-fix-scope-unknown", head)); err == nil || !strings.Contains(err.Error(), "unsupported successor anomaly") {
			t.Fatalf("unknown successor error = %v", err)
		}
	})
}

func TestQuarantineHistoricalLegacyFixScopeRejectsLockingCorruptionAndPartialFailure(t *testing.T) {
	t.Run("refuses symlinked lineage without touching external target", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		store, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-symlink", []string{"lib/scope.go"}, false)
		external := filepath.Join(t.TempDir(), "external-lineage")
		if err := os.Rename(store.Dir, external); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, store.Dir); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		record, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, legacyFixScopeQuarantineRequest(t, repo, "legacy-fix-scope-symlink", head))
		if err == nil || !strings.Contains(err.Error(), "is symlinked") || record.Status == CompactReclaimCommitted {
			t.Fatalf("symlink lineage result = %#v, %v", record, err)
		}
		if _, err := os.Stat(external); err != nil {
			t.Fatalf("external lineage was touched: %v", err)
		}
	})

	t.Run("stale invocation cannot recreate committed source", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		store, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-interleave", []string{"lib/scope.go"}, false)
		request := legacyFixScopeQuarantineRequest(t, repo, "legacy-fix-scope-interleave", head)
		entered, release := make(chan struct{}), make(chan struct{})
		var gate sync.Mutex
		blocked := false
		original := legacyFixScopeBeforeMaintenance
		legacyFixScopeBeforeMaintenance = func() {
			gate.Lock()
			wait := !blocked
			blocked = true
			gate.Unlock()
			if wait {
				close(entered)
				<-release
			}
		}
		t.Cleanup(func() { legacyFixScopeBeforeMaintenance = original })
		first := make(chan error, 1)
		go func() {
			_, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, request)
			first <- err
		}()
		<-entered
		if _, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, request); err != nil {
			t.Fatalf("committing invocation: %v", err)
		}
		close(release)
		if err := <-first; err != nil {
			t.Fatalf("stale invocation: %v", err)
		}
		if _, err := os.Lstat(store.Dir); !os.IsNotExist(err) {
			t.Fatalf("stale invocation recreated source: %v", err)
		}
	})

	t.Run("active lineage lock", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		store, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-local-lock", []string{"lib/scope.go"}, false)
		lock, err := acquireStoreLock(filepath.Join(store.Dir, "LOCK"))
		if err != nil {
			t.Fatal(err)
		}
		defer lock.release()
		if _, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, legacyFixScopeQuarantineRequest(t, repo, "legacy-fix-scope-local-lock", head)); !errors.Is(err, ErrAuthorityLockTimeout) {
			t.Fatalf("active lock error = %v", err)
		}
	})

	t.Run("shared maintenance lock", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		_, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-maintenance-lock", []string{"lib/scope.go"}, false)
		lockContext, cancelLock := context.WithTimeout(context.Background(), time.Second)
		defer cancelLock()
		lock, err := AcquireReviewMaintenanceExclusive(lockContext, repo)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Release()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := QuarantineHistoricalLegacyFixScope(ctx, repo, legacyFixScopeQuarantineRequest(t, repo, "legacy-fix-scope-maintenance-lock", head)); !errors.Is(err, ErrAuthorityLockCancelled) {
			t.Fatalf("maintenance contention error = %v", err)
		}
	})

	t.Run("corrupted event hash", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		store, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-hash", []string{"lib/scope.go"}, false)
		if err := os.WriteFile(filepath.Join(store.Dir, "events", strings.TrimPrefix(head, "sha256:")+".json"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, legacyFixScopeQuarantineRequest(t, repo, "legacy-fix-scope-hash", head)); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
			t.Fatalf("corrupt hash error = %v", err)
		}
	})

	t.Run("discontinuous predecessor chain", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		store, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-chain", []string{"lib/scope.go"}, false)
		current, _, err := store.loadRevision(head)
		if err != nil {
			t.Fatal(err)
		}
		head = writeStoreEvent(t, store, Record{Operation: "review/validate-fix-delta", PreviousRevision: hash("f"), Transaction: historicalValidationTransition(current.Transaction)})
		if _, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, legacyFixScopeQuarantineRequest(t, repo, "legacy-fix-scope-chain", head)); err == nil || !strings.Contains(err.Error(), "load revision") {
			t.Fatalf("discontinuous chain error = %v", err)
		}
	})

	t.Run("partial rename leaves prepared audit", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		store, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-partial", []string{"lib/scope.go"}, false)
		original := reclaimQuarantineResidue
		reclaimQuarantineResidue = func(_, _ string) error { return errors.New("rename failed") }
		t.Cleanup(func() { reclaimQuarantineResidue = original })
		record, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, legacyFixScopeQuarantineRequest(t, repo, "legacy-fix-scope-partial", head))
		if err == nil || record.Status != CompactReclaimPrepared || record.QuarantinePath == "" {
			t.Fatalf("partial quarantine = %#v, %v", record, err)
		}
		if _, err := os.Stat(store.Dir); err != nil {
			t.Fatalf("partial failure moved source: %v", err)
		}
	})

	t.Run("concurrent calls preserve authority", func(t *testing.T) {
		repo := initSnapshotRepo(t)
		_, head, _ := legacyFixScopeFixture(t, repo, "legacy-fix-scope-concurrent", []string{"lib/scope.go"}, false)
		request := legacyFixScopeQuarantineRequest(t, repo, "legacy-fix-scope-concurrent", head)
		start := make(chan struct{})
		results := make(chan error, 2)
		for range 2 {
			go func() {
				<-start
				_, err := QuarantineHistoricalLegacyFixScope(context.Background(), repo, request)
				results <- err
			}()
		}
		close(start)
		successes := 0
		for range 2 {
			if err := <-results; err == nil {
				successes++
			}
		}
		if successes == 0 {
			t.Fatal("concurrent quarantine did not commit or replay")
		}
		report, err := InventoryAuthority(context.Background(), repo)
		if err != nil || !report.Complete || !report.Authoritative {
			t.Fatalf("inventory after concurrent quarantine = %#v, %v", report, err)
		}
	})
}
