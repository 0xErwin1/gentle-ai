package sddstatus

import (
	"context"
	"strings"
	"testing"
)

// #4527: --remediates-evidence-revision is a POINTER to a value the ledger
// already recorded. Its only correct test is equality with the chain's
// actual unremediated failed evidence, not the pointer's own shape. Before
// this fix, every ingress checked shape before equality, so a chain whose
// own recorded evidence was itself malformed (e.g. a legacy or
// externally-written record predating today's stricter validation) could
// never be named by settle: the shape gate fired first and refused every
// caller, including one that typed the exact value the chain held.
//
// A REAL ledger can never hold a malformed EvidenceRevision today:
// validateRuntimeRecordShape enforces runtimeRevisionPattern on every Finish
// event's evidence_revision unconditionally, both when a record is first
// committed (store.mutate) and on every subsequent replay from genesis
// (loadRevision -> applyRuntimeRecordLocked), which makes the exact
// reproduction scenario unreachable through any store-backed acquire/
// finish/settle round trip in this codebase (confirmed by attempting to
// seed one through store.commitRecordLocked directly: it is rejected with
// "invalid_finish_event" before HEAD ever moves). The chain-equality
// decision itself is therefore exercised here as the pure predicate Finish
// and Acquire each consult, built from a hand-constructed chain value
// exactly as the replay layer would present it -- never through the store.
func TestRuntimeRemediationPointerRefusalAcceptsPointerEqualToChainRegardlessOfShape(t *testing.T) {
	malformedChainValue := "sha256:" + strings.Repeat("f", 65) // 65 hex chars: one too many
	if err := runtimeRemediationPointerRefusal(malformedChainValue, malformedChainValue, "finish"); err != nil {
		t.Fatalf("pointer equal to the chain's own malformed value refused: %v", err)
	}
}

func TestRuntimeRemediationPointerRefusalNamesShapeAndChainValueForMalformedMismatch(t *testing.T) {
	chainValue := runtimeTestHash('9')
	malformedPointer := "sha256:" + strings.Repeat("A", 64) // uppercase hex, wrong shape
	err := runtimeRemediationPointerRefusal(chainValue, malformedPointer, "finish")
	if err == nil {
		t.Fatal("malformed non-matching pointer = nil error, want a refusal")
	}
	message := err.Error()
	if !strings.Contains(message, "sha256:<64-lowercase-hex>") {
		t.Fatalf("refusal %q does not name the expected shape", message)
	}
	if !strings.Contains(message, "length=") {
		t.Fatalf("refusal %q does not include a redacted length observation", message)
	}
	if !strings.Contains(message, chainValue) {
		t.Fatalf("refusal %q does not name the chain's actual value %q", message, chainValue)
	}
	if !strings.Contains(message, "--remediates-evidence-revision") {
		t.Fatalf("refusal %q does not name a runnable exit", message)
	}
	if strings.Contains(message, malformedPointer) {
		t.Fatalf("refusal %q echoes the raw offending pointer verbatim", message)
	}
}

func TestRuntimeRemediationPointerRefusalKeepsWellFormedMismatchWording(t *testing.T) {
	chainValue := runtimeTestHash('9')
	wellFormedPointer := runtimeTestHash('d')
	err := runtimeRemediationPointerRefusal(chainValue, wellFormedPointer, "finish")
	if err == nil || !strings.Contains(err.Error(), "unremediated failure") {
		t.Fatalf("well-formed non-matching pointer = %v, want the plain mismatch refusal", err)
	}
	if strings.Contains(err.Error(), "sha256:<64-lowercase-hex>") {
		t.Fatalf("well-formed non-matching pointer = %q, must not gain the shape wording", err.Error())
	}
}

// TestRuntimeFinishRemediationPointerMalformedNonMatchingRefusesLegibly is
// the store-backed, end-to-end reproduction that IS reachable: the chain's
// own recorded evidence stays well-formed (as it always must), and the
// caller's --remediates-evidence-revision pointer is malformed and does not
// match it. Before the fix this refused with the bare shape message and no
// mention of the chain's actual value; after the fix the refusal names both.
func TestRuntimeFinishRemediationPointerMalformedNonMatchingRefusesLegibly(t *testing.T) {
	repo, store, failed := newTruthfulRemediationChain(t)
	started := beginTruthfulRemediation(t, store, failed, "pointer-legibility-begin")
	appendRuntimeLedgerFile(t, repo, "correction attempt with a malformed pointer\n")

	malformedPointer := "sha256:" + strings.Repeat("A", 64)
	_, err := store.Finish(context.Background(), FinishAttemptRequest{
		ExpectedRevision: started.Revision, RequestID: "pointer-legibility-finish", Outcome: AttemptPassed,
		EvidenceRevision: runtimeTestHash('c'), Diagnosis: "claims a correction",
		HarnessDisposition: HarnessReused, CleanupEvidence: "workspace cleanup completed",
		ProcessEvidence:            "process scan found no descendants",
		RemediatesEvidenceRevision: malformedPointer,
	})
	if err == nil {
		t.Fatal("malformed non-matching remediates_evidence_revision = nil error, want a refusal")
	}
	message := err.Error()
	if !strings.Contains(message, "sha256:<64-lowercase-hex>") || !strings.Contains(message, "length=") {
		t.Fatalf("refusal %q does not carry the shape observation", message)
	}
	if !strings.Contains(message, runtimeTestHash('f')) {
		t.Fatalf("refusal %q does not name the chain's actual unremediated failure %q", message, runtimeTestHash('f'))
	}
	if strings.Contains(message, malformedPointer) {
		t.Fatalf("refusal %q echoes the raw offending pointer verbatim", message)
	}
}

// TestCompactAcquireRemediationPointerMalformedNonMatchingNamesShapeError
// proves Acquire's ordering fix for the reachable case: a malformed pointer
// that does not match the chain's (well-formed) recorded failure must
// surface the legible shape error, not the opaque remediation_unsatisfiable
// block a caller cannot self-diagnose.
func TestCompactAcquireRemediationPointerMalformedNonMatchingNamesShapeError(t *testing.T) {
	_, store, _ := newTruthfulRemediationChain(t)
	malformedPointer := "sha256:" + strings.Repeat("A", 64)
	_, err := store.Acquire(context.Background(), CompactAcquireRequest{
		BeginAttemptRequest: BeginAttemptRequest{
			RequestID: "pointer-acquire-begin", WorkUnit: truthfulWorkUnit,
			EvidenceGoal: truthfulGoal, MaxAttempts: truthfulMaxAttempts, MaxChangedLines: truthfulMaxLines,
		},
		RemediatesEvidenceRevision: malformedPointer,
	})
	if err == nil {
		t.Fatal("acquire with a malformed non-matching pointer = nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "must be sha256:<64-lowercase-hex>") {
		t.Fatalf("acquire refusal = %v, want the shape error", err)
	}
	if !strings.Contains(err.Error(), runtimeTestHash('f')) {
		t.Fatalf("acquire refusal = %v, want the chain's actual unremediated failure named", err)
	}
	if !strings.Contains(err.Error(), "sdd-attempt acquire") {
		t.Fatalf("acquire refusal = %v, want the acquire exit named", err)
	}
}

// TestFailedEvidenceRemediationSettleableIsEqualityOnly is the unit-level
// predicate proof for Acquire's admission gate (#4527): it already judges
// equality alone, with no shape check of its own, so it accepts a pointer
// that matches the chain's recorded evidence even when that recorded value
// is itself malformed -- built here directly, the way the replay layer
// would present it, never through the store (see the package-level
// unreachability note above).
func TestFailedEvidenceRemediationSettleableIsEqualityOnly(t *testing.T) {
	malformedChainValue := "sha256:" + strings.Repeat("f", 65)
	status := RuntimeStatus{Attempts: []RuntimeAttempt{
		{Ordinal: 1, Outcome: AttemptFailed, EvidenceRevision: malformedChainValue},
	}}
	if !failedEvidenceRemediationSettleable(status, malformedChainValue) {
		t.Fatal("pointer equal to the chain's own malformed evidence was not settleable")
	}
	if failedEvidenceRemediationSettleable(status, "sha256:"+strings.Repeat("A", 64)) {
		t.Fatal("a malformed pointer that does not match the chain's evidence was settleable")
	}
}
