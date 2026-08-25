//go:build legacy_compact_receipt

package sddstatus

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The enabled-side counterpart of the test above is gone with its subject:
// review no longer demands a successor when implementation finishes, because
// it acts after implementation and verification. Its replacement lives in
// runtime_review_acts_after_verify_test.go.

func TestRuntimeDisabledUnmanagedRemediationConsumesTheOnlyRemainingAttempt(t *testing.T) {
	// The subject is what re-enabling does to a disabled/unmanaged
	// correction, so this fixture needs a user who has receipt-driven
	// development switched on -- otherwise the "re-enabled" half of the test
	// never re-enables anything.
	reviewEnabledHome(t)
	repo := initRuntimeLedgerRepo(t)
	changeRoot := seedReadyChange(t, repo, "unmanaged-remediation", "- [x] 1.1 Work\n")
	store := mustRuntimeStore(t, repo, "unmanaged-remediation")
	store.ReviewDisabled = true
	first, err := store.Begin(context.Background(), BeginAttemptRequest{
		ExpectedRevision: "", RequestID: "unmanaged-begin-verification", WorkUnit: "verify",
		EvidenceGoal: "independent verification", MaxAttempts: 2, MaxChangedLines: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	failedEvidence := runtimeTestHash('a')
	failed, err := store.Finish(context.Background(), FinishAttemptRequest{
		ExpectedRevision: first.Revision, RequestID: "unmanaged-finish-verification", Outcome: AttemptFailed,
		EvidenceRevision: failedEvidence, Diagnosis: "independent verification found a correctable defect",
		HarnessDisposition: HarnessReused, CleanupEvidence: "verification cleanup completed",
		ProcessEvidence: "verification process scan completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(failedEvidence, "fail"))
	active, err := store.Begin(context.Background(), BeginAttemptRequest{
		ExpectedRevision: failed.Revision, RequestID: "unmanaged-begin-correction", WorkUnit: "verify",
		EvidenceGoal: "independent verification", MaxAttempts: 2, MaxChangedLines: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := FinishAttemptRequest{
		ExpectedRevision: active.Revision, RequestID: "unmanaged-finish-correction", Outcome: AttemptPassed,
		EvidenceRevision: runtimeTestHash('b'), Diagnosis: "bounded correction passed focused checks",
		HarnessDisposition: HarnessReused, CleanupEvidence: "correction cleanup completed",
		ProcessEvidence: "correction process scan completed", RemediatesEvidenceRevision: failedEvidence,
	}
	before := countRuntimeRecords(t, store.Dir)
	if _, err := store.Finish(context.Background(), request); err == nil {
		t.Fatal("unchanged candidate satisfied unmanaged remediation")
	}
	if status, err := store.Status(); err != nil || status.Revision != active.Revision || status.ActiveAttempt == nil || countRuntimeRecords(t, store.Dir) != before {
		t.Fatalf("unchanged remediation refusal mutated runtime: status=%#v err=%v records=%d", status, err, countRuntimeRecords(t, store.Dir))
	}
	appendRuntimeLedgerFile(t, repo, "bounded unmanaged correction\n")
	withoutBinding := request
	withoutBinding.RequestID = "unmanaged-finish-unbound"
	withoutBinding.RemediatesEvidenceRevision = ""
	if _, err := store.Finish(context.Background(), withoutBinding); err == nil {
		t.Fatal("generic passing finish satisfied disabled remediation")
	}
	wrongEvidence := request
	wrongEvidence.RequestID = "unmanaged-finish-wrong-evidence"
	wrongEvidence.RemediatesEvidenceRevision = runtimeTestHash('c')
	if _, err := store.Finish(context.Background(), wrongEvidence); err == nil {
		t.Fatal("wrong failed evidence satisfied unmanaged remediation")
	}
	completed, err := store.Finish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !completed.Complete || completed.ActiveAttempt != nil || completed.Binding != nil || len(completed.Attempts) != 2 {
		t.Fatalf("unmanaged remediation completion = %#v", completed)
	}
	last := completed.Attempts[len(completed.Attempts)-1]
	if last.RemediatesEvidenceRevision != failedEvidence || last.FinishCandidateIdentity == last.BeginCandidateIdentity || last.FinishCandidateTree == last.BeginCandidateTree {
		t.Fatalf("unmanaged remediation did not bind the failed evidence to a changed candidate: %#v", last)
	}
	if replay, err := store.Finish(context.Background(), request); err != nil || replay.Revision != completed.Revision {
		t.Fatalf("exact remediation replay = %#v err=%v", replay, err)
	}
	result, err := store.Acquire(context.Background(), CompactAcquireRequest{BeginAttemptRequest: BeginAttemptRequest{
		RequestID: "unmanaged-replay-correction", WorkUnit: "verify", EvidenceGoal: "independent verification", MaxAttempts: 2, MaxChangedLines: 20,
	}})
	if err != nil || result.State != CompactStateComplete {
		t.Fatalf("second unmanaged correction = %#v err=%v", result, err)
	}

	// Re-enabling receipt-driven delivery must not turn a valid disabled
	// correction into archive authority. The fresh PASS is preserved, but the
	// existing bounded-review route must own archive admission from here.
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(request.EvidenceRevision, "pass"))
	reenabled, err := Resolve(ResolveOptions{CWD: repo, ChangeName: "unmanaged-remediation"})
	if err != nil {
		t.Fatal(err)
	}
	if reenabled.Dependencies.Verify != DependencyAllDone || reenabled.Dependencies.Archive != DependencyReady || reenabled.NextRecommended != "archive" {
		t.Fatalf("re-enabled unmanaged correction routed verify=%q archive=%q next=%q", reenabled.Dependencies.Verify, reenabled.Dependencies.Archive, reenabled.NextRecommended)
	}
	if reenabled.ReviewGate == nil || !strings.Contains(reenabled.ReviewGate.Reason, "disabled/unmanaged correction") ||
		strings.Contains(reenabled.ReviewGate.Reason, reviewGateFreshReviewContinuation) {
		t.Fatalf("re-enabled unmanaged correction context = %#v, want informational ordinary-policy wording", reenabled.ReviewGate)
	}
	if reenabled.ReviewOffer == nil || !reenabled.ReviewOffer.Available || !strings.Contains(reenabled.ReviewOffer.Invocation, "review start") {
		t.Fatalf("re-enabled unmanaged correction omitted the executable review offer: %#v", reenabled.ReviewOffer)
	}
}
