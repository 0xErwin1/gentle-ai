package sddstatus

import (
	"context"
	"testing"
)

// Issue #4542: runtimeChainFailedAttempt walked newest to oldest and stopped
// at the first AttemptPassed, treating every pass as the objective's
// completing settlement. A pass that also carries
// ChangedLineBudgetExceeded=true never completed the objective -- it forces
// DecisionRequired/reset exactly like a failure does -- so it must not hide
// an earlier failure, or itself, from the chain a correction remediates.
// runtimeChainFailedAttempt's real question is "what is the chain's newest
// unremediated attempt", answered as failed unless the newest AttemptPassed
// actually completed the objective.
func TestRuntimeChainFailedAttempt(t *testing.T) {
	cases := []struct {
		name         string
		attempts     []RuntimeAttempt
		wantFound    bool
		wantOutcome  AttemptOutcome
		wantEvidence string
	}{
		{
			name: "newest completing pass returns none",
			attempts: []RuntimeAttempt{
				{Outcome: AttemptFailed, EvidenceRevision: runtimeTestHash('a')},
				{Outcome: AttemptPassed, EvidenceRevision: runtimeTestHash('b')},
			},
			wantFound: false,
		},
		{
			name: "newest budget exceeded pass is returned",
			attempts: []RuntimeAttempt{
				{Outcome: AttemptFailed, EvidenceRevision: runtimeTestHash('a')},
				{Outcome: AttemptPassed, EvidenceRevision: runtimeTestHash('b'), ChangedLineBudgetExceeded: true},
			},
			wantFound:    true,
			wantOutcome:  AttemptPassed,
			wantEvidence: runtimeTestHash('b'),
		},
		{
			name: "failed after a completing pass is the newest failure",
			attempts: []RuntimeAttempt{
				{Outcome: AttemptPassed, EvidenceRevision: runtimeTestHash('a')},
				{Outcome: AttemptFailed, EvidenceRevision: runtimeTestHash('b')},
			},
			wantFound:    true,
			wantOutcome:  AttemptFailed,
			wantEvidence: runtimeTestHash('b'),
		},
		{
			name: "running attempt on top of a failed attempt is the failed one",
			attempts: []RuntimeAttempt{
				{Outcome: AttemptFailed, EvidenceRevision: runtimeTestHash('a')},
				{Outcome: AttemptRunning},
			},
			wantFound:    true,
			wantOutcome:  AttemptFailed,
			wantEvidence: runtimeTestHash('a'),
		},
		{
			name: "budget exceeded pass after a completing pass is returned over the older pass",
			attempts: []RuntimeAttempt{
				{Outcome: AttemptPassed, EvidenceRevision: runtimeTestHash('a')},
				{Outcome: AttemptPassed, EvidenceRevision: runtimeTestHash('b'), ChangedLineBudgetExceeded: true},
			},
			wantFound:    true,
			wantOutcome:  AttemptPassed,
			wantEvidence: runtimeTestHash('b'),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := runtimeChainFailedAttempt(testCase.attempts)
			if ok != testCase.wantFound {
				t.Fatalf("runtimeChainFailedAttempt(%q) found = %v, want %v", testCase.name, ok, testCase.wantFound)
			}
			if !ok {
				return
			}
			if got.Outcome != testCase.wantOutcome || got.EvidenceRevision != testCase.wantEvidence {
				t.Fatalf("runtimeChainFailedAttempt(%q) = %#v, want outcome=%s evidence=%s",
					testCase.name, got, testCase.wantOutcome, testCase.wantEvidence)
			}
		})
	}
}

// TestBudgetExceededPassRemediationSettlesThroughCompactAcquire is the exact
// #4542 shape end to end at the compact surface: attempt 1 settles failed
// with evidence F1, attempt 2 corrects the candidate and its verification
// passes, but the settle exceeds max_changed_lines, so the ledger records it
// as passed-with-budget-exceeded and forces a decision instead of completing.
// The maintainer resets with relation remediation, then re-acquires and
// settles a correction over the SAME (unchanged) candidate naming
// --remediates-evidence-revision against F2 (the budget-exceeded pass), the
// evidence the maintainer was actually told to reset over. Before the fix,
// runtimeChainFailedAttempt stopped at F2's AttemptPassed outcome and
// reported no failed evidence at all, so compact Acquire refused with
// remediation_unsatisfiable even though the reset explicitly authorized this
// retry.
func TestBudgetExceededPassRemediationSettlesThroughCompactAcquire(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	store := mustRuntimeStore(t, repo, "budget-exceeded-remediation")
	store.ReviewDisabled = true
	ctx := context.Background()

	// 1. Objective with a small max-changed-lines ceiling.
	first, err := store.Begin(ctx, BeginAttemptRequest{
		ExpectedRevision: "", RequestID: "budget-begin-1", WorkUnit: "verify",
		EvidenceGoal: "independent verification", MaxAttempts: 2, MaxChangedLines: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Attempt 1 settles failed with evidence F1.
	f1 := runtimeTestHash('a')
	failed, err := store.Finish(ctx, FinishAttemptRequest{
		ExpectedRevision: first.Revision, RequestID: "budget-finish-1", Outcome: AttemptFailed,
		EvidenceRevision: f1, Diagnosis: "independent verification found a correctable defect",
		HarnessDisposition: HarnessReused, CleanupEvidence: "verification cleanup completed",
		ProcessEvidence: "verification process scan completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed.DecisionRequired || failed.NextAction != RuntimeActionBegin {
		t.Fatalf("attempt 1 failed settlement = %#v, want unused capacity remaining", failed)
	}

	// 3. Attempt 2 corrects the candidate: verification passes, but the
	// correction exceeds max_changed_lines (1), so the settle is recorded
	// passed-with-budget-exceeded instead of completing the objective.
	second, err := store.Begin(ctx, BeginAttemptRequest{
		ExpectedRevision: failed.Revision, RequestID: "budget-begin-2", WorkUnit: "verify",
		EvidenceGoal: "independent verification", MaxAttempts: 2, MaxChangedLines: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	appendRuntimeLedgerFile(t, repo, "line one\nline two\n")
	f2 := runtimeTestHash('b')
	exceeded, err := store.Finish(ctx, FinishAttemptRequest{
		ExpectedRevision: second.Revision, RequestID: "budget-finish-2", Outcome: AttemptPassed,
		EvidenceRevision: f2, Diagnosis: "correction converged and verification passed",
		HarnessDisposition: HarnessReused, CleanupEvidence: "correction cleanup completed",
		ProcessEvidence: "correction process scan completed", RemediatesEvidenceRevision: f1,
	})
	if err != nil {
		t.Fatal(err)
	}
	last := exceeded.Attempts[len(exceeded.Attempts)-1]
	if !last.ChangedLineBudgetExceeded {
		t.Fatalf("attempt 2 settlement did not exceed the changed-line budget: %#v", last)
	}
	if !exceeded.DecisionRequired || exceeded.NextAction != RuntimeActionReset || exceeded.Complete {
		t.Fatalf("budget-exceeded passed settlement = %#v, want DecisionRequired reset and not complete", exceeded)
	}

	// 4. Maintainer reset with relation remediation.
	reset, err := store.Reset(ctx, ResetObjectiveRequest{
		ExpectedRevision: exceeded.Revision, RequestID: "budget-reset",
		Reason: "maintainer decision: correction exceeded the changed-line budget, reset for a bounded retry",
		Actor:  "maintainer", Relation: RuntimeObjectiveRelationRemediation,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reset.NextAction != RuntimeActionBegin || reset.Objective != nil {
		t.Fatalf("post-reset status = %#v, want next_action begin and no objective", reset)
	}
	if reset.LastReset == nil || reset.LastReset.Relation != RuntimeObjectiveRelationRemediation {
		t.Fatalf("post-reset LastReset = %#v, want relation remediation", reset.LastReset)
	}

	// 5. Acquire naming --remediates-evidence-revision F2 (the budget-exceeded
	// pass) over the unchanged candidate must proceed, not block as
	// remediation_unsatisfiable.
	acquired, err := store.Acquire(ctx, CompactAcquireRequest{
		BeginAttemptRequest: BeginAttemptRequest{
			ExpectedRevision: reset.Revision, RequestID: "budget-acquire-retry", WorkUnit: "verify",
			EvidenceGoal: "independent verification", MaxAttempts: 2, MaxChangedLines: 1,
		},
		RemediatesEvidenceRevision: f2,
	})
	if err != nil {
		t.Fatalf("acquire after audited reset returned an error: %v", err)
	}
	if acquired.State != CompactStateProceed || acquired.Token == "" {
		t.Fatalf("acquire after audited reset = %#v, want proceed with a token", acquired)
	}

	// 6. Settling that attempt passed within budget, naming
	// --remediates-evidence-revision F2, must complete the objective.
	completed, err := store.Finish(ctx, FinishAttemptRequest{
		ExpectedRevision: acquired.Token, RequestID: "budget-finish-retry", Outcome: AttemptPassed,
		EvidenceRevision: runtimeTestHash('c'), Diagnosis: "unchanged candidate reverified within budget",
		HarnessDisposition: HarnessReused, CleanupEvidence: "retry cleanup completed",
		ProcessEvidence: "retry process scan completed", RemediatesEvidenceRevision: f2,
	})
	if err != nil {
		t.Fatalf("settling the remediation retry was refused: %v", err)
	}
	if !completed.Complete || completed.ActiveAttempt != nil {
		t.Fatalf("remediation retry settlement = %#v, want complete", completed)
	}
	finalAttempt := completed.Attempts[len(completed.Attempts)-1]
	if finalAttempt.RemediatesEvidenceRevision != f2 || finalAttempt.ChangedLineBudgetExceeded {
		t.Fatalf("final attempt = %#v, want it to remediate %s within budget", finalAttempt, f2)
	}
}
