//go:build legacy_compact_receipt

package sddstatus

import (
	"context"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// historicalFinishRemediationRequest is the canonical request payload of the
// removed atomic record. It stays test-only so this fixture remains valid when
// production no longer accepts that historical operation.
const historicalAtomicRemediationOperation = "attempt/finish-remediation"

type historicalFinishRemediationRequest struct {
	ExpectedRevision           string             `json:"expected_revision"`
	RequestID                  string             `json:"request_id"`
	Outcome                    AttemptOutcome     `json:"outcome"`
	EvidenceRevision           string             `json:"evidence_revision"`
	Diagnosis                  string             `json:"diagnosis"`
	HarnessDisposition         HarnessDisposition `json:"harness_disposition"`
	CleanupEvidence            string             `json:"cleanup_evidence"`
	ProcessEvidence            string             `json:"process_evidence"`
	RemediatesEvidenceRevision string             `json:"remediates_evidence_revision,omitempty"`
}

func TestRuntimeLedgerRejectsHistoricalAtomicRemediationRecord(t *testing.T) {
	t.Run("ordinary attempt/finish remains accepted", func(t *testing.T) {
		store := mustRuntimeStore(t, initRuntimeLedgerRepo(t), "ordinary-finish-control")
		started, err := store.Begin(context.Background(), BeginAttemptRequest{
			RequestID: "ordinary-begin", WorkUnit: "verify", EvidenceGoal: "ordinary finish acceptance control",
			MaxAttempts: 2, MaxChangedLines: 20,
		})
		if err != nil {
			t.Fatal(err)
		}
		completed, err := store.Finish(context.Background(), FinishAttemptRequest{
			ExpectedRevision: started.Revision, RequestID: "ordinary-finish", Outcome: AttemptPassed,
			EvidenceRevision: runtimeTestHash('a'), Diagnosis: "ordinary finish completed",
			HarnessDisposition: HarnessReused, CleanupEvidence: "cleanup completed", ProcessEvidence: "no descendants",
		})
		if err != nil || !completed.Complete {
			t.Fatalf("ordinary attempt/finish = %#v, err=%v", completed, err)
		}
		record, err := store.loadRecord(completed.Revision)
		if err != nil || record.Operation != runtimeOperationFinish {
			t.Fatalf("ordinary attempt/finish record = %#v, err=%v", record, err)
		}
	})

	t.Run("historical atomic remediation is unsupported", func(t *testing.T) {
		store, historical := historicalAtomicRemediationRecord(t)
		publishHistoricalRuntimeRecord(t, store, historical)

		_, err := store.Status()
		if err == nil {
			t.Fatal("historical attempt/finish-remediation record was accepted")
		}
		if !strings.Contains(err.Error(), "invalid SDD runtime record operation") {
			t.Fatalf("historical attempt/finish-remediation rejection = %v, want unsupported operation", err)
		}
	})
}

func historicalAtomicRemediationRecord(t *testing.T) (RuntimeStore, runtimeRecord) {
	t.Helper()
	repo := initRuntimeLedgerRepo(t)
	store := mustRuntimeStore(t, repo, "historical-remediation")
	if err := store.ensureDirectories(); err != nil {
		t.Fatal(err)
	}

	first, err := store.Begin(context.Background(), BeginAttemptRequest{
		RequestID: "historical-begin-1", WorkUnit: "verify",
		EvidenceGoal: "prove historical atomic remediation acceptance", MaxAttempts: 3, MaxChangedLines: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	failedEvidence := runtimeTestHash('c')
	failed, err := store.Finish(context.Background(), FinishAttemptRequest{
		ExpectedRevision: first.Revision, RequestID: "historical-finish-1", Outcome: AttemptFailed,
		EvidenceRevision: failedEvidence, Diagnosis: "recorded failed verification",
		HarnessDisposition: HarnessReused, CleanupEvidence: "cleanup completed", ProcessEvidence: "no descendants",
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Begin(context.Background(), BeginAttemptRequest{
		ExpectedRevision: failed.Revision, RequestID: "historical-begin-2", WorkUnit: "verify",
		EvidenceGoal: "prove historical atomic remediation acceptance", MaxAttempts: 3, MaxChangedLines: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	appendRuntimeLedgerFile(t, repo, "historical remediation\n")
	snapshot, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).Build(context.Background(), reviewtransaction.Target{
		Kind: reviewtransaction.TargetBaseWorkspaceOverlay, BaseRef: active.ActiveAttempt.BeginCandidateTree,
		Projection: reviewtransaction.ProjectionWorkspace, IntendedUntracked: active.ActiveAttempt.IntendedUntracked,
	})
	if err != nil {
		t.Fatal(err)
	}
	changedLines, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).ChangedLines(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	record := runtimeRecord{
		Schema: runtimeRecordSchema, Change: store.Change, PreviousRevision: active.Revision,
		Operation: historicalAtomicRemediationOperation, RequestID: "historical-finish-remediation",
		Finish: &runtimeFinishEvent{
			Ordinal: active.ActiveAttempt.Ordinal, FinishCandidateIdentity: snapshot.Identity, FinishCandidateTree: snapshot.CandidateTree,
			Outcome: AttemptPassed, ChangedLines: changedLines, EvidenceRevision: runtimeTestHash('f'),
			Diagnosis: "historical atomic remediation completed", HarnessDisposition: HarnessReused,
			CleanupEvidence: "cleanup completed", ProcessEvidence: "no descendants", RemediatesEvidenceRevision: failedEvidence,
		},
	}
	record.RequestDigest = runtimeValueHash("gentle-ai.sdd-runtime-finish-request/v1", historicalFinishRemediationRequest{
		ExpectedRevision: record.PreviousRevision, RequestID: record.RequestID, Outcome: record.Finish.Outcome,
		EvidenceRevision: record.Finish.EvidenceRevision, Diagnosis: record.Finish.Diagnosis,
		HarnessDisposition: record.Finish.HarnessDisposition, CleanupEvidence: record.Finish.CleanupEvidence,
		ProcessEvidence:            record.Finish.ProcessEvidence,
		RemediatesEvidenceRevision: record.Finish.RemediatesEvidenceRevision,
	})
	return store, record
}

func publishHistoricalRuntimeRecord(t *testing.T, store RuntimeStore, record runtimeRecord) string {
	t.Helper()
	revision, payload, err := runtimeRecordRevision(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.publishRecord(revision, payload); err != nil {
		t.Fatal(err)
	}
	if err := store.publishHead(revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestLegacyRuntimeLedgerRejectsMalformedPersistedInterruptedEvidence(t *testing.T) {
	store := mustRuntimeStore(t, initRuntimeLedgerRepo(t), "malformed-interrupted")
	started, err := store.Begin(context.Background(), BeginAttemptRequest{
		RequestID: "malformed-begin", WorkUnit: "runtime evidence", EvidenceGoal: "replay safely",
		MaxAttempts: 2, MaxChangedLines: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := store.Finish(context.Background(), FinishAttemptRequest{
		ExpectedRevision: started.Revision, RequestID: "malformed-finish", Outcome: AttemptInterrupted,
		Diagnosis: "the interrupted attempt was recorded", HarnessDisposition: HarnessInvalidated,
		CleanupEvidence: "cleanup completed", ProcessEvidence: "no surviving processes",
	})
	if err != nil {
		t.Fatal(err)
	}

	record, err := store.loadRecord(settled.Revision)
	if err != nil {
		t.Fatal(err)
	}
	record.Finish.EvidenceRevision = "sha256:" + strings.Repeat("G", 64)
	record.RequestDigest = runtimeValueHash("gentle-ai.sdd-runtime-finish-request/v1", FinishAttemptRequest{
		ExpectedRevision: record.PreviousRevision, RequestID: record.RequestID, Outcome: record.Finish.Outcome,
		EvidenceRevision: record.Finish.EvidenceRevision, Diagnosis: record.Finish.Diagnosis,
		HarnessDisposition: record.Finish.HarnessDisposition, CleanupEvidence: record.Finish.CleanupEvidence,
		ProcessEvidence: record.Finish.ProcessEvidence,
	})
	revision, payload, err := runtimeRecordRevision(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.publishRecord(revision, payload); err != nil {
		t.Fatal(err)
	}
	if err := store.publishHead(revision); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Status(); err == nil || !strings.Contains(err.Error(), "invalid SDD runtime finish event") {
		t.Fatalf("malformed persisted interrupted evidence status error = %v, want fail-closed replay refusal", err)
	}
}
