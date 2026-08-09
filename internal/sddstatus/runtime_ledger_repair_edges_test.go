package sddstatus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type consecutiveRescopeRepairFixture struct {
	store       RuntimeStore
	predecessor RuntimeStatus
	poison      runtimeRecord
	path        string
}

func newConsecutiveRescopeRepairFixture(t *testing.T) consecutiveRescopeRepairFixture {
	t.Helper()
	store := mustRuntimeStore(t, initRuntimeLedgerRepo(t), "repair-edges")
	started, err := store.Begin(context.Background(), BeginAttemptRequest{RequestID: "begin-a", WorkUnit: "objective-a", EvidenceGoal: "prove A", MaxAttempts: 2, MaxChangedLines: 20})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := store.Finish(context.Background(), FinishAttemptRequest{ExpectedRevision: started.Revision, RequestID: "finish-a", Outcome: AttemptFailed, EvidenceRevision: runtimeTestHash('a'), Diagnosis: "failed without drift", HarnessDisposition: HarnessReused, CleanupEvidence: "clean", ProcessEvidence: "reproduced"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Rescope(context.Background(), RescopeObjectiveRequest{ExpectedRevision: failed.Revision, RequestID: "rescope-a-b", WorkUnit: "objective-b", EvidenceGoal: "prove B", MaxAttempts: 2, MaxChangedLines: 10, Reason: "narrow A to B", Actor: "maintainer"})
	if err != nil {
		t.Fatal(err)
	}
	last, generation := b.Attempts[0], b.ObjectiveGeneration+1
	request := RescopeObjectiveRequest{ExpectedRevision: b.Revision, RequestID: "rescope-b-c", WorkUnit: "objective-c", EvidenceGoal: "prove C", MaxAttempts: 1, MaxChangedLines: 5, Reason: "narrow B to C", Actor: "maintainer"}
	poison := runtimeRecord{Schema: runtimeRecordSchema, Change: store.Change, PreviousRevision: b.Revision, Operation: runtimeOperationRescope, RequestID: request.RequestID, RequestDigest: runtimeValueHash("gentle-ai.sdd-runtime-rescope-request/v1", request), Rescope: &runtimeRescopeEvent{PreviousObjectiveID: b.Objective.ID, PreviousGeneration: b.Objective.Generation, PreviousMaxAttempts: b.Objective.MaxAttempts, PreviousMaxChangedLines: b.Objective.MaxChangedLines, RescopeCandidateIdentity: b.Objective.InitialCandidateIdentity, RescopeCandidateTree: last.FinishCandidateTree, ObjectiveID: runtimeObjectiveID(store.Change, request.WorkUnit, request.EvidenceGoal, b.Objective.InitialCandidateIdentity, generation), ObjectiveGeneration: generation, WorkUnit: request.WorkUnit, EvidenceGoal: request.EvidenceGoal, MaxAttempts: request.MaxAttempts, MaxChangedLines: request.MaxChangedLines, Reason: request.Reason, Actor: request.Actor}}
	revision, payload, err := runtimeRecordRevision(poison)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.publishRecord(revision, payload); err != nil {
		t.Fatal(err)
	}
	if err := store.publishHead(revision); err != nil {
		t.Fatal(err)
	}
	return consecutiveRescopeRepairFixture{store: store, predecessor: b, poison: poison, path: filepath.Join(store.Dir, "records", revision[len("sha256:"):]+".json")}
}

func (fixture consecutiveRescopeRepairFixture) rewrite(t *testing.T, record runtimeRecord) string {
	t.Helper()
	revision, payload, err := runtimeRecordRevision(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.publishRecord(revision, payload); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.publishHead(revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestRuntimeConsecutiveRescopeRepairIdempotenceAndPreservation(t *testing.T) {
	fixture := newConsecutiveRescopeRepairFixture(t)
	head, exists, err := readRuntimeHead(filepath.Join(fixture.store.Dir, "HEAD"))
	if err != nil || !exists {
		t.Fatal(err)
	}
	request := RepairConsecutiveRescopeRequest{ExpectedRevision: head, RequestID: "repair-exact", Reason: "repair exact historical defect", Actor: "maintainer"}
	if _, err := fixture.store.Reset(context.Background(), ResetObjectiveRequest{ExpectedRevision: head, RequestID: "not-repair", Reason: "not a repair", Actor: "maintainer"}); err == nil || countRuntimeRecords(t, fixture.store.Dir) != 4 {
		t.Fatalf("reset masqueraded as repair: err=%v records=%d", err, countRuntimeRecords(t, fixture.store.Dir))
	}
	if _, err := fixture.store.RepairConsecutiveRescope(context.Background(), RepairConsecutiveRescopeRequest{ExpectedRevision: fixture.predecessor.Revision, RequestID: "wrong-head", Reason: "wrong", Actor: "maintainer"}); err == nil {
		t.Fatal("repair accepted a wrong expected revision")
	}
	before, err := os.ReadFile(fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := fixture.store.RepairConsecutiveRescope(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := fixture.store.RepairConsecutiveRescope(context.Background(), request)
	if err != nil || replayed.Revision != repaired.Revision {
		t.Fatalf("idempotent repair = %#v, err=%v", replayed, err)
	}
	if _, err := fixture.store.RepairConsecutiveRescope(context.Background(), RepairConsecutiveRescopeRequest{ExpectedRevision: head, RequestID: request.RequestID, Reason: "changed digest", Actor: "maintainer"}); err == nil {
		t.Fatal("repair accepted a changed digest for the same request id")
	}
	after, err := os.ReadFile(fixture.path)
	if err != nil || string(after) != string(before) || repaired.LastReset != nil || repaired.LastRescope == nil || repaired.LastRepair == nil {
		t.Fatalf("repair altered preserved authority: err=%v status=%#v", err, repaired)
	}
	if err := os.Remove(fixture.path); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Status(); err == nil {
		t.Fatal("replay accepted a repair whose preserved record is missing")
	}
}

func TestRuntimeConsecutiveRescopeRepairRefusesNonExactDamage(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*runtimeRecord)
	}{
		{"wrong operation", func(record *runtimeRecord) {
			record.Operation = runtimeOperationReset
			record.Reset = &runtimeResetEvent{PreviousObjectiveID: record.Rescope.PreviousObjectiveID, PreviousGeneration: record.Rescope.PreviousGeneration, ResetCandidateIdentity: record.Rescope.RescopeCandidateIdentity, ResetCandidateTree: record.Rescope.RescopeCandidateTree, Reason: "forged", Actor: "attacker"}
			record.Rescope = nil
		}},
		{"wrong predecessor", func(record *runtimeRecord) { record.PreviousRevision = runtimeTestHash('f') }},
		{"widened limits", func(record *runtimeRecord) {
			record.Rescope.PreviousMaxChangedLines = 12
			record.Rescope.MaxChangedLines = 11
		}},
		{"candidate mismatch", func(record *runtimeRecord) { record.Rescope.RescopeCandidateTree = strings.Repeat("1", 40) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newConsecutiveRescopeRepairFixture(t)
			record := fixture.poison
			test.mutate(&record)
			if record.Operation == runtimeOperationRescope {
				request := RescopeObjectiveRequest{ExpectedRevision: record.PreviousRevision, RequestID: record.RequestID, WorkUnit: record.Rescope.WorkUnit, EvidenceGoal: record.Rescope.EvidenceGoal, MaxAttempts: record.Rescope.MaxAttempts, MaxChangedLines: record.Rescope.MaxChangedLines, Reason: record.Rescope.Reason, Actor: record.Rescope.Actor}
				record.RequestDigest = runtimeValueHash("gentle-ai.sdd-runtime-rescope-request/v1", request)
			}
			head := fixture.rewrite(t, record)
			if _, err := fixture.store.RepairConsecutiveRescope(context.Background(), RepairConsecutiveRescopeRequest{ExpectedRevision: head, RequestID: "repair-refusal", Reason: "must refuse", Actor: "maintainer"}); err == nil {
				t.Fatal("repair accepted non-exact authority")
			}
		})
	}
}
