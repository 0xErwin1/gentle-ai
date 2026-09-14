package sddstatus

import (
	"context"
	"strings"
	"testing"
)

// #4481: status and settle must derive the same failed-evidence revision.
// resolveBoundedRemediation used to source FailedEvidenceRevision only from
// the verify-report file, while the runtime ledger (runtime_ledger.go
// runtimeChainFailedAttempt / Finish's --remediates-evidence-revision check)
// tracks the newest unremediated failed attempt independently. Once a
// remediation attempt against the verify-report's failure itself fails and
// records a new evidence revision, the two surfaces disagreed forever: status
// kept naming the stale file revision, but settle only accepted the chain's
// newer one.

// TestStatusReportsChainRevisionWhenNewerRemediationFailed pins the fix:
// after a failed remediation attempt records a new evidence revision, status
// reports that chain revision, matching what Finish will actually accept.
func TestStatusReportsChainRevisionWhenNewerRemediationFailed(t *testing.T) {
	const change = "chain-revision-advice"
	repo := initRuntimeLedgerRepo(t)
	store, failedEvidence, failed := seedUnmanagedFailedVerification(t, repo, change, 2)
	if failed.DecisionRequired {
		t.Fatalf("fixture unexpectedly required a decision: %#v", failed)
	}

	remediationEvidence := runtimeTestHash('b')
	started, err := store.Begin(context.Background(), BeginAttemptRequest{
		ExpectedRevision: failed.Revision, RequestID: change + "-begin-remediation",
		WorkUnit: "verify", EvidenceGoal: "independent verification", MaxAttempts: 2, MaxChangedLines: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := store.Finish(context.Background(), FinishAttemptRequest{
		ExpectedRevision: started.Revision, RequestID: change + "-finish-remediation", Outcome: AttemptFailed,
		EvidenceRevision: remediationEvidence, Diagnosis: "correction did not converge",
		HarnessDisposition: HarnessInvalidated, CleanupEvidence: "workspace cleanup completed",
		ProcessEvidence:            "process scan found no descendants",
		RemediatesEvidenceRevision: failedEvidence,
	})
	if err != nil {
		t.Fatalf("failed remediation settle refused: %v", err)
	}
	last := settled.Attempts[len(settled.Attempts)-1]
	if last.EvidenceRevision != remediationEvidence || last.RemediatesEvidenceRevision != failedEvidence {
		t.Fatalf("remediation record = %#v", last)
	}

	status, joined := resolveDisabledRemediationInstructions(t, repo, change)
	if status.NextRecommended != "remediate" {
		t.Fatalf("NextRecommended = %q, want remediate", status.NextRecommended)
	}
	if !status.RemediationState.Required {
		t.Fatal("RemediationState.Required = false, want true")
	}
	if status.RemediationState.FailedEvidenceRevision != remediationEvidence {
		t.Fatalf("RemediationState.FailedEvidenceRevision = %q, want the chain's newer failure %q",
			status.RemediationState.FailedEvidenceRevision, remediationEvidence)
	}
	if status.RuntimeStatus == nil {
		t.Fatal("RuntimeStatus is nil")
	}
	chainEvidence, found := runtimeChainFailedEvidence(status.RuntimeStatus.Attempts)
	if !found || chainEvidence != remediationEvidence {
		t.Fatalf("runtimeChainFailedEvidence = (%q, %v), want (%q, true)", chainEvidence, found, remediationEvidence)
	}
	if status.RemediationState.FailedEvidenceRevision != chainEvidence {
		t.Fatalf("status disagrees with the ledger: RemediationState names %q, chain names %q",
			status.RemediationState.FailedEvidenceRevision, chainEvidence)
	}
	if !strings.Contains(joined, remediationEvidence) {
		t.Fatalf("remediate instructions do not name the chain's failed evidence %q:\n%s", remediationEvidence, joined)
	}
	if strings.Contains(joined, failedEvidence) {
		t.Fatalf("remediate instructions still name the stale verify-report evidence %q:\n%s", failedEvidence, joined)
	}
}

// TestStatusKeepsFileBackedRevisionWithoutRuntimeLedger is the fallback
// regression guard: with no Git repository (so no native runtime ledger can
// exist), status must keep naming the verify-report's own evidence revision
// exactly as before.
func TestStatusKeepsFileBackedRevisionWithoutRuntimeLedger(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "no-ledger-advice", "- [x] 1.1 Work\n")
	write(t, changeRoot+"/verify-report.md", testVerifyEnvelope("fail", 0, 0, "1/1", "1/1", 0, 0))

	fileEvidence := "sha256:" + strings.Repeat("a", 64)
	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "no-ledger-advice", ReviewDisabled: true, IncludeInstructions: true})
	if err != nil {
		t.Fatal(err)
	}
	if status.RuntimeStatus != nil {
		t.Fatalf("expected no runtime ledger without Git, got %#v", status.RuntimeStatus)
	}
	if !status.RemediationState.Required {
		t.Fatal("RemediationState.Required = false, want true")
	}
	if status.RemediationState.FailedEvidenceRevision != fileEvidence {
		t.Fatalf("RemediationState.FailedEvidenceRevision = %q, want the file-backed evidence %q",
			status.RemediationState.FailedEvidenceRevision, fileEvidence)
	}
}
