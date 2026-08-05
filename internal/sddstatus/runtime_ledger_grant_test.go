package sddstatus

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRuntimeLedgerGrantCommitsAndProjectsGrantedRoots is #2540 S2's core
// round-trip: a committed grant projects its canonical absolute
// symlink-evaluated roots into RuntimeStatus.GrantedRoots, and a second
// grant chains and accumulates.
func TestRuntimeLedgerGrantCommitsAndProjectsGrantedRoots(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, "grant-roots")
	if err != nil {
		t.Fatal(err)
	}

	sibling, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "sibling-link")
	if err := os.Symlink(sibling, link); err != nil {
		t.Fatal(err)
	}

	// The caller passes the SYMLINK path; the recorded and projected root must
	// be the canonical evaluated target, following BeginWorktree's precedent.
	granted, err := store.Grant(context.Background(), GrantRootsRequest{
		ExpectedRevision: "", RequestID: "grant-1", Roots: []string{link},
		Reason: "maintainer authorized sibling repository edits", Actor: "maintainer",
	})
	if err != nil {
		t.Fatalf("grant refused: %v", err)
	}
	if len(granted.GrantedRoots) != 1 || granted.GrantedRoots[0] != sibling ||
		granted.Revision == "" || countRuntimeRecords(t, store.Dir) != 1 {
		t.Fatalf("granted status = %#v records=%d, want one chained record granting %q",
			granted, countRuntimeRecords(t, store.Dir), sibling)
	}

	// Status() replays the persisted chain: the projection round-trips.
	status, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(status.GrantedRoots) != 1 || status.GrantedRoots[0] != sibling {
		t.Fatalf("replayed granted roots = %#v, want [%q]", status.GrantedRoots, sibling)
	}

	// A second grant chains PreviousRevision on the first and accumulates,
	// deduplicating the already-granted root.
	second, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	accumulated, err := store.Grant(context.Background(), GrantRootsRequest{
		ExpectedRevision: granted.Revision, RequestID: "grant-2", Roots: []string{second, sibling},
		Reason: "maintainer widened the change to a second sibling", Actor: "maintainer",
	})
	if err != nil {
		t.Fatalf("second grant refused: %v", err)
	}
	if len(accumulated.GrantedRoots) != 2 || accumulated.GrantedRoots[0] != sibling ||
		accumulated.GrantedRoots[1] != second || countRuntimeRecords(t, store.Dir) != 2 {
		t.Fatalf("accumulated granted roots = %#v records=%d, want [%q %q] over 2 records",
			accumulated.GrantedRoots, countRuntimeRecords(t, store.Dir), sibling, second)
	}
}

// TestRuntimeLedgerGrantDuplicateRequestIsIdempotent proves the sibling-event
// RequestID/RequestDigest contract: an exact replay returns the committed
// revision without a new record; reuse with different inputs is refused.
func TestRuntimeLedgerGrantDuplicateRequestIsIdempotent(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, "grant-idempotent")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := GrantRootsRequest{
		ExpectedRevision: "", RequestID: "grant-once", Roots: []string{root},
		Reason: "maintainer authorized sibling repository edits", Actor: "maintainer",
	}
	granted, err := store.Grant(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	replayed, err := store.Grant(context.Background(), request)
	if err != nil || replayed.Revision != granted.Revision || countRuntimeRecords(t, store.Dir) != 1 {
		t.Fatalf("grant replay = %#v err=%v records=%d, want committed revision %q and 1 record",
			replayed, err, countRuntimeRecords(t, store.Dir), granted.Revision)
	}

	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request.Roots = []string{other}
	if _, err := store.Grant(context.Background(), request); !errors.Is(err, ErrRuntimeRequestConflict) {
		t.Fatalf("conflicting grant reuse = %v, want ErrRuntimeRequestConflict", err)
	}
}

// TestRuntimeLedgerGrantReplayRefusesForgedWidenedRecord is the
// runtimeRescopeEvent forgery pattern applied to grants: widened Roots no
// longer match the RequestDigest the original request bound, so replay's
// digest recompute refuses the chain.
func TestRuntimeLedgerGrantReplayRefusesForgedWidenedRecord(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, "grant-forged-replay")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := GrantRootsRequest{
		ExpectedRevision: "", RequestID: "grant-forge-1", Roots: []string{root},
		Reason: "maintainer authorized one sibling repository", Actor: "maintainer",
	}
	granted, err := store.Grant(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	// Forged: the record claims the SAME request granted a widened root set,
	// keeping the digest the original single-root request produced.
	widened, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedRevision, request.RequestID = granted.Revision, "grant-forge-2"
	forgedRecord := runtimeRecord{
		Schema: runtimeRecordSchema, Change: store.Change, PreviousRevision: granted.Revision,
		Operation: runtimeOperationGrant, RequestID: request.RequestID,
		RequestDigest: runtimeValueHash("gentle-ai.sdd-runtime-grant-request/v1", request),
		Grant: &runtimeGrantEvent{
			Roots: []string{root, widened}, Actor: "attacker",
			Reason: "forged widened grant", GrantedAt: "2026-08-05T00:00:00Z",
		},
	}
	revision, payload, err := runtimeRecordRevision(forgedRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ensureDirectories(); err != nil {
		t.Fatal(err)
	}
	if err := store.publishRecord(revision, payload); err != nil {
		t.Fatal(err)
	}
	if err := store.publishHead(revision); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Status(); err == nil || !strings.Contains(err.Error(), "grant request digest does not match record") {
		t.Fatalf("replay of forged widened grant = %v, want the digest-recompute rejection", err)
	}
}

// TestRuntimeLedgerLegacyChainWithoutGrantsReplaysUnchanged pins the phase-1
// compatibility constraint: a chain recorded before grant records existed
// replays exactly as before, projects no granted roots, and serializes no
// granted_roots member (omitempty is load-bearing).
func TestRuntimeLedgerLegacyChainWithoutGrantsReplaysUnchanged(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, "grant-legacy-chain")
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.Begin(context.Background(), BeginAttemptRequest{
		ExpectedRevision: "", RequestID: "legacy-begin-1", WorkUnit: "legacy-scope",
		EvidenceGoal: "prove grant-free chains replay unchanged", MaxAttempts: 2, MaxChangedLines: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := store.Finish(context.Background(), FinishAttemptRequest{
		ExpectedRevision: started.Revision, RequestID: "legacy-finish-1", Outcome: AttemptInterrupted,
		EvidenceRevision: runtimeTestHash('7'), Diagnosis: "interrupted with the workspace unchanged",
		HarnessDisposition: HarnessInvalidated, CleanupEvidence: "no executor process was ever spawned",
		ProcessEvidence: "pre-launch process scan found no descendants",
	})
	if err != nil {
		t.Fatal(err)
	}
	if finished.GrantedRoots != nil {
		t.Fatalf("grant-free chain projected granted roots: %#v", finished.GrantedRoots)
	}
	status, err := store.Status()
	if err != nil {
		t.Fatalf("grant-free chain replay = %v, want unchanged success", err)
	}
	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "granted_roots") {
		t.Fatalf("grant-free status serialized a granted_roots member: %s", payload)
	}
}
