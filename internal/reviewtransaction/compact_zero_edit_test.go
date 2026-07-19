package reviewtransaction

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// newCompactZeroEditCorrectionState builds a medium-risk compact review that
// reached correction_required with an authorized in-budget forecast, without
// applying any candidate edit afterwards.
func newCompactZeroEditCorrectionState(t *testing.T, repo, lineage string) CompactState {
	t.Helper()
	state := newCompactTestState(t, repo, lineage)
	completeCompactZeroEditReview(t, &state)
	if err := state.BeginCorrection(1); err != nil {
		t.Fatal(err)
	}
	return state
}

func completeCompactZeroEditReview(t *testing.T, state *CompactState) {
	t.Helper()
	finding := Finding{ID: "R3-001", Lens: "reliability", Location: "tracked.txt:2", Severity: "CRITICAL", Claim: "wrong value", ProofRefs: []string{"candidate-only failure"}}
	if err := state.CompleteReview(CompactReviewInput{
		LensResults:     []LensResult{{Lens: LensReliability, Findings: []Finding{finding}, Evidence: []string{"reviewed"}}},
		Classifications: []FindingEvidence{{FindingID: finding.ID, Class: EvidenceDeterministic, Causality: CausalIntroduced, Proof: "changed hunk causes failure"}},
		RefuterOutcomes: []EvidenceResult{},
	}); err != nil {
		t.Fatal(err)
	}
}

func buildZeroEditFixSnapshot(t *testing.T, repo string, state CompactState) (Snapshot, int) {
	t.Helper()
	fix, err := (SnapshotBuilder{Repo: repo}).Build(context.Background(), Target{
		Kind: TargetFixDiff, Projection: state.InitialSnapshot.Projection,
		BaseRef: state.CurrentSnapshot.CandidateTree, IntendedUntracked: state.InitialSnapshot.IntendedUntracked,
		LedgerIDs: state.FixFindingIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fix.CandidateTree != fix.BaseTree {
		t.Fatalf("expected repository-derived zero-edit fix snapshot, got base %s candidate %s", fix.BaseTree, fix.CandidateTree)
	}
	actual, err := (SnapshotBuilder{Repo: repo}).ChangedLines(context.Background(), fix)
	if err != nil {
		t.Fatal(err)
	}
	if actual != 0 {
		t.Fatalf("zero-edit fix snapshot changed lines = %d, want 0", actual)
	}
	return fix, actual
}

func zeroEditFailedValidation(fixHash string, findingIDs []string) ScopedValidationResult {
	return ScopedValidationResult{
		LedgerIDs: findingIDs, FixCausedFindings: []Finding{},
		FollowUps:            []FollowUp{{Observation: "original criteria still failing without an in-scope fix", ProofRefs: []string{"targeted validation output"}}},
		OriginalCriteria:     ValidationCheck{Passed: false, EvidenceHash: hash("2"), FixDeltaHash: fixHash},
		CorrectionRegression: ValidationCheck{Passed: true, EvidenceHash: hash("3"), FixDeltaHash: fixHash},
	}
}

func TestCompactZeroEditFailedCorrectionEscalatesTerminally(t *testing.T) {
	repo := initSnapshotRepo(t)
	writeSnapshotFile(t, repo, "tracked.txt", "base\nwrong\n")
	state := newCompactTestState(t, repo, "compact-zero-edit-escalate")
	store := storeCompactStartAuthority(t, repo, state)
	record, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	revision := record.Revision
	completeCompactZeroEditReview(t, &state)
	revision, err = store.Replace(revision, "review/complete-review", state)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.BeginCorrection(1); err != nil {
		t.Fatal(err)
	}
	revision, err = store.Replace(revision, "review/begin-fix", state)
	if err != nil {
		t.Fatal(err)
	}

	fix, actual := buildZeroEditFixSnapshot(t, repo, state)
	fixHash := FixDeltaHashForSnapshot(fix)
	validation := zeroEditFailedValidation(fixHash, state.FixFindingIDs)
	if err := state.CompleteCorrection(fix, actual, validation); err != nil {
		t.Fatalf("zero-edit failed correction error = %v, want terminal escalation", err)
	}
	if state.State != StateEscalated {
		t.Fatalf("zero-edit failed correction state = %q, want %q", state.State, StateEscalated)
	}
	if state.ActualCorrectionLines == nil || *state.ActualCorrectionLines != 0 || state.CumulativeCorrectionLines != 0 ||
		len(state.CorrectionAttempts) != 1 || state.CorrectionAttempts[0].ActualLines != 0 ||
		state.CorrectionAttempts[0].Snapshot.CandidateTree != state.CorrectionAttempts[0].Snapshot.BaseTree {
		t.Fatalf("zero-edit escalation accounting = %#v", state)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("zero-edit escalated state validate error = %v", err)
	}

	revision, err = store.Replace(revision, "review/complete-fix", state)
	if err != nil {
		t.Fatalf("persist zero-edit escalation: %v", err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Revision != revision || loaded.State.State != StateEscalated {
		t.Fatalf("reload zero-edit escalation = %#v, %v", loaded, err)
	}

	receipt, err := state.Receipt()
	if err != nil || receipt.TerminalState != TerminalEscalated || receipt.FinalCandidateTree != state.InitialSnapshot.CandidateTree {
		t.Fatalf("zero-edit escalation receipt = %#v, %v", receipt, err)
	}
	if err := WriteCompactReceiptAtomic(store.ReceiptPath(), receipt); err != nil {
		t.Fatal(err)
	}
	report, err := InventoryAuthority(context.Background(), repo)
	if err != nil || !report.Complete || !report.Authoritative ||
		!hasAuthorityInventoryStatus(report.Entries, state.LineageID, AuthorityStatusEscalated) {
		t.Fatalf("zero-edit escalated inventory = %#v, %v", report, err)
	}
}

func TestCompactZeroEditCorrectionWithoutProvenFailedOriginalRemainsRejected(t *testing.T) {
	for _, tt := range []struct {
		name             string
		originalPassed   bool
		regressionPassed bool
		actual           int
	}{
		{name: "passing original and regression", originalPassed: true, regressionPassed: true},
		{name: "passing original failing regression", originalPassed: true, regressionPassed: false},
		{name: "failing original failing regression", originalPassed: false, regressionPassed: false},
		{name: "nonzero asserted lines", originalPassed: false, regressionPassed: true, actual: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := initSnapshotRepo(t)
			writeSnapshotFile(t, repo, "tracked.txt", "base\nwrong\n")
			state := newCompactZeroEditCorrectionState(t, repo, "compact-zero-edit-guard")
			fix, _ := buildZeroEditFixSnapshot(t, repo, state)
			fixHash := FixDeltaHashForSnapshot(fix)
			validation := zeroEditFailedValidation(fixHash, state.FixFindingIDs)
			validation.OriginalCriteria.Passed = tt.originalPassed
			validation.CorrectionRegression.Passed = tt.regressionPassed
			before := state
			err := state.CompleteCorrection(fix, tt.actual, validation)
			if err == nil || !strings.Contains(err.Error(), "unchanged candidate") {
				t.Fatalf("unproven zero-edit correction error = %v, want unchanged-candidate rejection", err)
			}
			if !reflect.DeepEqual(state, before) {
				t.Fatalf("rejected zero-edit correction mutated state:\nbefore=%#v\nafter=%#v", before, state)
			}
		})
	}
}

func TestCompactValidateFailsClosedForCraftedZeroEditAttempts(t *testing.T) {
	repo := initSnapshotRepo(t)
	writeSnapshotFile(t, repo, "tracked.txt", "base\nwrong\n")
	state := newCompactZeroEditCorrectionState(t, repo, "compact-zero-edit-crafted")
	fix, actual := buildZeroEditFixSnapshot(t, repo, state)
	fixHash := FixDeltaHashForSnapshot(fix)
	if err := state.CompleteCorrection(fix, actual, zeroEditFailedValidation(fixHash, state.FixFindingIDs)); err != nil {
		t.Fatal(err)
	}

	passingOriginal := state
	passingOriginal.CorrectionAttempts = append([]CompactCorrectionAttempt{}, state.CorrectionAttempts...)
	passingOriginal.CorrectionAttempts[0].OriginalCriteria.Passed = true
	original := *state.OriginalCriteria
	original.Passed = true
	passingOriginal.OriginalCriteria = &original
	if err := passingOriginal.Validate(); err == nil {
		t.Fatal("crafted zero-edit attempt with passing original criteria validated")
	}

	nonzeroLines := state
	nonzeroLines.CorrectionAttempts = append([]CompactCorrectionAttempt{}, state.CorrectionAttempts...)
	nonzeroLines.CorrectionAttempts[0].ActualLines = 1
	one := 1
	nonzeroLines.ActualCorrectionLines = &one
	nonzeroLines.CumulativeCorrectionLines = 1
	if err := nonzeroLines.Validate(); err == nil {
		t.Fatal("crafted zero-edit attempt with nonzero actual lines validated")
	}

	nonTerminal := state
	nonTerminal.State = StateValidating
	if err := nonTerminal.Validate(); err == nil {
		t.Fatal("crafted zero-edit correction outside terminal escalation validated")
	}
}
