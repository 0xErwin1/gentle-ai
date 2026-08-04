package sddstatus

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompactSettleRemediationRefusalIsClassifiedNotAuthorityFailure is the RED
// reproduction for #2249: a compact `sdd-attempt settle --outcome passed` with
// otherwise valid inputs, issued against an attempt whose candidate drifted
// after Begin while a review binding is in place, used to collapse into
// {"state":"blocked","reason":"authority_failure"} while status kept
// reporting outcome: running, next_action: finish — a dead end. Root cause:
// compactMutationFailure's switch had no case for
// ErrRuntimeRemediationSuccessorRequired, so it fell through to the default
// branch and threw away runtimeRemediationExitRefusal's actionable message.
func TestCompactSettleRemediationRefusalIsClassifiedNotAuthorityFailure(t *testing.T) {
	change := "compact-remediation-legibility"
	fixture := newRuntimeUnchangedBindingFixture(t, change)

	// Drift the candidate after Begin: the ordinary continuation for a
	// passing finish bound to a review is now the remediation trio, not a
	// bare pass.
	write(t, filepath.Join(fixture.store.Repo, "openspec", "changes", change, "tasks.md"), "- [x] 1.1 Done\n# post-begin drift\n")

	result, err := fixture.store.Settle(context.Background(), CompactSettleRequest{
		Token: fixture.active.Revision, RequestID: change + "-settle", Outcome: AttemptPassed,
		EvidenceRevision: runtimeTestHash('e'), Diagnosis: "drifted candidate settle reproduces #2249",
		HarnessDisposition: HarnessReused, CleanupEvidence: "settle cleanup completed",
		ProcessEvidence: "settle process scan found no descendants",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != CompactStateBlocked {
		t.Fatalf("drifted compact settle result = %#v, want state=blocked", result)
	}
	if result.Reason == CompactBlockAuthorityFailure {
		t.Fatalf("drifted compact settle reason = %q, want a specific classification, not the opaque default authority_failure dead end", result.Reason)
	}
	if result.Reason != CompactBlockRemediationRequired {
		t.Fatalf("drifted compact settle reason = %q, want %q", result.Reason, CompactBlockRemediationRequired)
	}
	if result.Detail == "" || result.Exit == "" {
		t.Fatalf("drifted compact settle result = %#v, want non-empty Detail/Exit carrying the wrapped refusal instead of throwing it away", result)
	}
	for _, want := range []string{"--expected-binding-revision", "--successor-lineage", "--remediates-evidence-revision"} {
		if !strings.Contains(result.Detail, want) {
			t.Fatalf("drifted compact settle detail = %q, want it to name the remediation trio exit including %s", result.Detail, want)
		}
	}
}

// TestCompactMutationFailureClassifiesEveryReachableLedgerSentinel is the
// table-driven sentinel enumeration test required alongside the #2249 fix. It
// audits every ErrRuntime*/ErrBindingRevisionConflict sentinel declared in the
// var block at runtime_ledger.go:61-92 against whether Begin or Finish (the
// only two mutations Acquire/Settle drive through compactMutationFailure) can
// actually produce it, and fails if a sentinel marked reachable still lands on
// the opaque CompactBlockAuthorityFailure default. This is a genuine
// regression guard: deleting the ErrRuntimeRemediationSuccessorRequired or
// ErrBindingRevisionConflict case from compactMutationFailure's switch makes
// this test fail, not just the #2249 repro above.
func TestCompactMutationFailureClassifiesEveryReachableLedgerSentinel(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		reachable  bool // producible by Begin or Finish, and therefore by Acquire/Settle
		wantState  CompactAttemptState
		wantReason CompactBlockReason
	}{
		{name: "ErrRuntimeObjectiveDone", err: ErrRuntimeObjectiveDone, reachable: true, wantState: CompactStateComplete},
		{name: "ErrRuntimeBudgetExhausted", err: ErrRuntimeBudgetExhausted, reachable: true, wantState: CompactStateBlocked, wantReason: CompactBlockMaintainerDecision},
		{name: "ErrRuntimeObjectiveChange", err: ErrRuntimeObjectiveChange, reachable: true, wantState: CompactStateBlocked, wantReason: CompactBlockMaintainerDecision},
		{name: "ErrRuntimeAttemptActive", err: ErrRuntimeAttemptActive, reachable: true, wantState: CompactStateBlocked, wantReason: CompactBlockActiveAttempt},
		{name: "ErrRuntimeRevisionConflict", err: ErrRuntimeRevisionConflict, reachable: true, wantState: CompactStateBlocked, wantReason: CompactBlockInvalidContinuation},
		{name: "ErrRuntimeConcurrentUpdate", err: ErrRuntimeConcurrentUpdate, reachable: true, wantState: CompactStateBlocked, wantReason: CompactBlockInvalidContinuation},
		{name: "ErrRuntimeRequestConflict", err: ErrRuntimeRequestConflict, reachable: true, wantState: CompactStateBlocked, wantReason: CompactBlockInvalidContinuation},
		{name: "ErrRuntimeNoActiveAttempt", err: ErrRuntimeNoActiveAttempt, reachable: true, wantState: CompactStateBlocked, wantReason: CompactBlockInvalidContinuation},
		{name: "ErrRuntimeRemediationSuccessorRequired", err: ErrRuntimeRemediationSuccessorRequired, reachable: true, wantState: CompactStateBlocked, wantReason: CompactBlockRemediationRequired},
		{name: "ErrRuntimeWorktreeMismatch", err: ErrRuntimeWorktreeMismatch, reachable: true, wantState: CompactStateBlocked, wantReason: CompactBlockWorktreeMismatch},
		{name: "ErrBindingRevisionConflict", err: ErrBindingRevisionConflict, reachable: true, wantState: CompactStateBlocked, wantReason: CompactBlockInvalidContinuation},
		// Reset is the only mutation that can produce these two; Begin/Finish
		// never do, so Acquire/Settle never route them into
		// compactMutationFailure. They stay intentionally unclassified.
		{name: "ErrRuntimeNoObjective", err: ErrRuntimeNoObjective, reachable: false},
		{name: "ErrRuntimeResetNotAllowed", err: ErrRuntimeResetNotAllowed, reachable: false},
	}

	store := RuntimeStore{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := store.compactMutationFailure(tt.err, false, BeginAttemptRequest{})
			if !tt.reachable {
				return
			}
			if result.Reason == CompactBlockAuthorityFailure {
				t.Fatalf("compactMutationFailure(%v) = %#v, a Begin/Finish-reachable sentinel must not fall through to authority_failure", tt.err, result)
			}
			if result.State != tt.wantState || result.Reason != tt.wantReason {
				t.Fatalf("compactMutationFailure(%v) = %#v, want state=%q reason=%q", tt.err, result, tt.wantState, tt.wantReason)
			}
			if result.Detail == "" || result.Exit == "" {
				t.Fatalf("compactMutationFailure(%v) = %#v, want non-empty Detail/Exit", tt.err, result)
			}
		})
	}
}

// TestCompactMutationFailureLeavesUnexpectedErrorsAtAuthorityFailure proves
// the classification is not a blanket bypass: a genuinely unclassified error
// (unrelated to any declared ledger sentinel, e.g. a raw I/O failure) still
// lands on CompactBlockAuthorityFailure, and still carries Detail/Exit so it
// is visible instead of silently swallowed.
func TestCompactMutationFailureLeavesUnexpectedErrorsAtAuthorityFailure(t *testing.T) {
	store := RuntimeStore{}
	err := errors.New("simulated unexpected I/O failure")
	result := store.compactMutationFailure(err, false, BeginAttemptRequest{})
	if result.State != CompactStateBlocked || result.Reason != CompactBlockAuthorityFailure {
		t.Fatalf("compactMutationFailure(%v) = %#v, want state=blocked reason=authority_failure", err, result)
	}
	if result.Detail != err.Error() || result.Exit != err.Error() {
		t.Fatalf("compactMutationFailure(%v) = %#v, want Detail/Exit = %q", err, result, err.Error())
	}
}
