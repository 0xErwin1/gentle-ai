package sddstatus

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeSelectedUntrackedPopulationAccountsMixedCandidate(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "selected.txt"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := OpenRuntimeStore(context.Background(), repo, "selected-mixed")
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.Begin(context.Background(), BeginAttemptRequest{
		RequestID: "selected-mixed-begin", WorkUnit: "mixed candidate", EvidenceGoal: "account tracked and selected files",
		MaxAttempts: 2, MaxChangedLines: 20, IntendedUntracked: []string{"selected.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	appendRuntimeLedgerFile(t, repo, "tracked change\n")
	if err := os.WriteFile(filepath.Join(repo, "selected.txt"), []byte("initial\nselected change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	finished, err := store.Finish(context.Background(), FinishAttemptRequest{
		ExpectedRevision: started.Revision, RequestID: "selected-mixed-finish", Outcome: AttemptFailed,
		EvidenceRevision: runtimeTestHash('a'), Diagnosis: "mixed candidate needs complete accounting",
		HarnessDisposition: HarnessReused, CleanupEvidence: "cleanup completed", ProcessEvidence: "process scan clean",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(finished.Attempts) != 1 || finished.Attempts[0].ChangedLines != 2 {
		t.Fatalf("mixed candidate changed lines = %#v, want tracked plus selected untracked lines", finished.Attempts)
	}
}

func TestRuntimeSelectedUntrackedCorrectionIsNotUnchanged(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "selected.txt"), []byte("initial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := OpenRuntimeStore(context.Background(), repo, "selected-remediation")
	if err != nil {
		t.Fatal(err)
	}
	store.ReviewDisabled = true
	started, err := store.Begin(context.Background(), BeginAttemptRequest{
		RequestID: "selected-remediation-begin", WorkUnit: "selected correction", EvidenceGoal: "accept selected correction bytes",
		MaxAttempts: 2, MaxChangedLines: 20, IntendedUntracked: []string{"selected.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "selected.txt"), []byte("initial\nfailed change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	failed, err := store.Finish(context.Background(), FinishAttemptRequest{
		ExpectedRevision: started.Revision, RequestID: "selected-remediation-failed", Outcome: AttemptFailed,
		EvidenceRevision: runtimeTestHash('b'), Diagnosis: "first selected candidate failed",
		HarnessDisposition: HarnessReused, CleanupEvidence: "cleanup completed", ProcessEvidence: "process scan clean",
	})
	if err != nil {
		t.Fatal(err)
	}
	correcting, err := store.Begin(context.Background(), BeginAttemptRequest{
		ExpectedRevision: failed.Revision, RequestID: "selected-remediation-correct", WorkUnit: "selected correction", EvidenceGoal: "accept selected correction bytes",
		MaxAttempts: 2, MaxChangedLines: 20, IntendedUntracked: []string{"selected.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "selected.txt"), []byte("initial\ncorrected change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Finish(context.Background(), FinishAttemptRequest{
		ExpectedRevision: correcting.Revision, RequestID: "selected-remediation-settle", Outcome: AttemptPassed,
		EvidenceRevision: runtimeTestHash('c'), Diagnosis: "selected correction changed the candidate",
		HarnessDisposition: HarnessReused, CleanupEvidence: "cleanup completed", ProcessEvidence: "process scan clean",
		RemediatesEvidenceRevision: runtimeTestHash('b'),
	}); err != nil {
		t.Fatalf("selected correction was classified unchanged: %v", err)
	}
}

func TestRuntimeSelectedUntrackedPopulationPersistsAcrossBeginFinishReplayAndHandoff(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	worktree := filepath.Join(t.TempDir(), "selected-worktree")
	runRuntimeLedgerGit(t, repo, "worktree", "add", "-q", "-b", "selected-worktree", worktree)
	for _, root := range []string{repo, worktree} {
		if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("initial\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	storeA, err := OpenRuntimeStore(context.Background(), repo, "selected-handoff")
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := OpenRuntimeStore(context.Background(), worktree, "selected-handoff")
	if err != nil {
		t.Fatal(err)
	}
	started, err := storeA.Begin(context.Background(), BeginAttemptRequest{
		RequestID: "selected-handoff-begin", WorkUnit: "selected handoff", EvidenceGoal: "preserve selected path provenance",
		MaxAttempts: 2, MaxChangedLines: 20, IntendedUntracked: []string{"selected.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handedOff, err := storeA.Handoff(context.Background(), HandoffAttemptRequest{
		ExpectedRevision: started.Revision, RequestID: "selected-handoff", DestinationWorktree: worktree,
	})
	if err != nil {
		t.Fatal(err)
	}
	if handedOff.ActiveAttempt == nil || len(handedOff.ActiveAttempt.IntendedUntracked) != 1 || handedOff.ActiveAttempt.IntendedUntracked[0] != "selected.txt" {
		t.Fatalf("handoff lost selected path provenance: %#v", handedOff.ActiveAttempt)
	}
	replayed, err := storeA.Status()
	if err != nil || replayed.ActiveAttempt == nil || len(replayed.ActiveAttempt.IntendedUntracked) != 1 || replayed.ActiveAttempt.IntendedUntracked[0] != "selected.txt" {
		t.Fatalf("replay lost selected path provenance: status=%#v err=%v", replayed, err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "selected.txt"), []byte("initial\nchanged in handoff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	finished, err := storeB.Finish(context.Background(), FinishAttemptRequest{
		ExpectedRevision: handedOff.Revision, RequestID: "selected-handoff-finish", Outcome: AttemptFailed,
		EvidenceRevision: runtimeTestHash('d'), Diagnosis: "handoff must account selected untracked bytes",
		HarnessDisposition: HarnessReused, CleanupEvidence: "cleanup completed", ProcessEvidence: "process scan clean",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(finished.Attempts) != 1 || finished.Attempts[0].ChangedLines != 1 || len(finished.Attempts[0].IntendedUntracked) != 1 || finished.Attempts[0].IntendedUntracked[0] != "selected.txt" {
		t.Fatalf("terminal selected handoff provenance = %#v", finished.Attempts)
	}
}

func TestRuntimeLegacyEmptyPopulationStillReplays(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, "legacy-empty-selected")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := captureRuntimeCandidate(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	request := legacyBeginAttemptRequest{
		RequestID: "legacy-empty-begin", WorkUnit: "legacy candidate", EvidenceGoal: "replay empty selected paths",
		MaxAttempts: 2, MaxChangedLines: 20,
	}
	record := runtimeRecord{
		Schema: runtimeRecordSchema, Change: store.Change, Operation: runtimeOperationBegin,
		RequestID: request.RequestID, RequestDigest: runtimeValueHash("gentle-ai.sdd-runtime-begin-request/v1", request),
		Begin: &runtimeBeginEvent{
			ObjectiveID: legacyRuntimeObjectiveID(store.Change, request.EvidenceGoal), WorkUnit: request.WorkUnit,
			EvidenceGoal: request.EvidenceGoal, MaxAttempts: request.MaxAttempts, MaxChangedLines: request.MaxChangedLines,
			Ordinal: 1, BeginCandidateIdentity: snapshot.Identity, BeginCandidateTree: snapshot.CandidateTree,
		},
	}
	revision, payload, err := runtimeRecordRevision(record)
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
	status, err := store.Status()
	if err != nil || status.ActiveAttempt == nil || len(status.ActiveAttempt.IntendedUntracked) != 0 {
		t.Fatalf("legacy empty selected population did not replay: status=%#v err=%v", status, err)
	}
}
