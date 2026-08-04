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

// TestCompactBlockedNamesExitForEveryReachableReason is the enumeration-style
// sentinel guard for compactBlocked, the sibling constructor
// compactMutationFailure's #2249 fix never reached: every one of
// compactBlocked's 20 real call sites in this file emits a bare
// {"state":"blocked","reason":"<code>"} with no Exit/Detail — a token with
// nothing behind it on a surface that carries no stderr narration and no docs
// mirror at all. This test enumerates every CompactBlockReason compactBlocked
// itself is ever called with (active_attempt, maintainer_decision,
// corrupt_authority, invalid_continuation — verified against this file's own
// call sites) and fails if any of them still produces an empty Exit or
// Detail, mirroring
// TestCompactMutationFailureClassifiesEveryReachableLedgerSentinel above.
func TestCompactBlockedNamesExitForEveryReachableReason(t *testing.T) {
	const sampleToken = "sha256:" + "a1b2c3d4e5f60708091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7"
	tests := []struct {
		name   string
		reason CompactBlockReason
		token  string
	}{
		{name: "corrupt_authority", reason: CompactBlockCorruptAuthority},
		{name: "invalid_continuation", reason: CompactBlockInvalidContinuation},
		{name: "maintainer_decision", reason: CompactBlockMaintainerDecision},
		{name: "active_attempt", reason: CompactBlockActiveAttempt, token: sampleToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compactBlocked(tt.reason, tt.token)
			if result.State != CompactStateBlocked || result.Reason != tt.reason {
				t.Fatalf("compactBlocked(%q, %q) = %#v, want state=blocked reason=%q", tt.reason, tt.token, result, tt.reason)
			}
			if result.Exit == "" || result.Detail == "" {
				t.Fatalf("compactBlocked(%q, %q) = %#v, want non-empty Exit/Detail — a blocked result with nothing behind its bare reason code is exactly the class this test exists to catch", tt.reason, tt.token, result)
			}
			if result.Token != tt.token {
				t.Fatalf("compactBlocked(%q, %q) = %#v, want Token preserved unchanged", tt.reason, tt.token, result)
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
