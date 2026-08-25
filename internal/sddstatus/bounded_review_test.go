//go:build legacy_compact_receipt

package sddstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

func TestResolveFinalVerifyWaitsForAllTasks(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n- [ ] 1.2 Pending\n")
	write(t, filepath.Join(changeRoot, "apply-progress.md"), "focused work-unit checks passed\n")
	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if status.Dependencies.Verify != DependencyBlocked || status.NextRecommended != "apply" {
		t.Fatalf("verify=%q next=%q, want blocked/apply", status.Dependencies.Verify, status.NextRecommended)
	}
}

// TestResolveMakesVerifyReadyWithNoPreVerifyReviewSupervision is Wave 4 S3's
// characterization of the new sequence (design.md decision 3, proposal.md's
// #1 success criterion, maintainer directive Engram #10123): apply -> verify
// -> offer RDD -> archive, with no pre-verify review control. Before this
// wave (see git history), the equivalent test —
// TestResolveStartsBoundedReviewBeforeFinalVerification — asserted the
// opposite: verify stayed blocked and next was "review" until an explicit
// bounded review transaction existed. That routing (applyPreVerifyReview
// Routing/applyPreVerifyCompactBridgeRouting) is removed in this slice, so
// this test proves its replacement: verify becomes ready immediately once
// apply is done, with or without any review transaction ever having
// existed, and the dispatcher no longer renders "Next Review Operation"
// guidance for this case (that guidance is now reachable only from
// resolve-review's binding-error paths, exercised elsewhere).
func TestResolveRoutesLegacyMissingReviewEvidenceToIndependentVerify(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	report := legacyMissingReviewReport()
	report = strings.Replace(report, "requirements: 0/2", "requirements: 0/1", 1)
	report = strings.Replace(report, "scenarios: 0/3", "scenarios: 0/1", 1)
	write(t, filepath.Join(changeRoot, "verify-report.md"), report)

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Dependencies.Verify != DependencyReady || status.Dependencies.Archive != DependencyBlocked || status.NextRecommended != "verify" {
		t.Fatalf("status = %#v, want incomplete evidence to re-enter independent verify", status)
	}
	for _, reason := range status.BlockedReasons {
		if strings.Contains(strings.ToLower(reason), "review") {
			t.Fatalf("legacy missing-review evidence routed through review: %v", status.BlockedReasons)
		}
	}
}

func TestResolveMakesVerifyReadyWithNoPreVerifyReviewSupervision(t *testing.T) {
	root := t.TempDir()
	seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Dependencies.Verify != DependencyReady || status.NextRecommended != "verify" {
		t.Fatalf("without any review transaction verify=%q next=%q, want ready/verify", status.Dependencies.Verify, status.NextRecommended)
	}
	if status.ReviewTransaction != nil {
		t.Fatalf("status.ReviewTransaction = %#v, want nil — no review transaction was ever created", status.ReviewTransaction)
	}
	dispatcher := RenderDispatcherMarkdown(status)
	if strings.Contains(dispatcher, "### Next Review Operation") {
		t.Fatalf("dispatcher rendered pre-verify review-operation guidance with no pre-verify gate to guide toward:\n%s", dispatcher)
	}
}

func TestCompactAuthorityPathBindingRejectsForeignAndTraversalOpenSpecPaths(t *testing.T) {
	base := reviewtransaction.CompactState{GenesisPaths: []string{"openspec/changes/thin/tasks.md"}, InitialSnapshot: reviewtransaction.Snapshot{Paths: []string{"openspec/changes/thin/tasks.md"}}}
	for _, tt := range []struct {
		name       string
		paths      []string
		wantReason bool
	}{
		{name: "foreign change is irrelevant", paths: []string{"openspec/changes/other/tasks.md"}},
		{name: "traversal", paths: []string{"openspec/changes/thin/../other/tasks.md"}, wantReason: true},
		{name: "dot segment", paths: []string{"openspec/changes/thin/./tasks.md"}, wantReason: true},
		{name: "duplicate separator", paths: []string{"openspec/changes/thin//tasks.md"}, wantReason: true},
		{name: "mixed unexpected config", paths: []string{"openspec/changes/thin/tasks.md", "openspec/config.yml"}, wantReason: true},
		{name: "mixed foreign change", paths: []string{"openspec/changes/thin/tasks.md", "openspec/changes/other/tasks.md"}, wantReason: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := base
			state.GenesisPaths, state.InitialSnapshot.Paths = tt.paths, tt.paths
			if bound, reason := compactAuthorityPathsBound(state, "thin"); bound || (reason != "") != tt.wantReason {
				t.Fatalf("paths %v = bound=%v reason=%q, wantReason=%v", tt.paths, bound, reason, tt.wantReason)
			}
		})
	}
}

func TestResolveIgnoresAmbiguousPathBoundCompactAuthoritiesPreVerify(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "compact-one")
	first, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "compact-one")
	if err != nil {
		t.Fatal(err)
	}
	record, err := first.Load()
	if err != nil {
		t.Fatal(err)
	}
	lines := record.State.OriginalChangedLines
	secondState, err := reviewtransaction.NewCompactState(reviewtransaction.Start{
		LineageID: "compact-two", Mode: reviewtransaction.ModeOrdinaryBounded, Generation: 1,
		Snapshot: record.State.InitialSnapshot, PolicyHash: record.State.PolicyHash, RiskLevel: record.State.RiskLevel,
		SelectedLenses: record.State.SelectedLenses, OriginalChangedLines: &lines,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, secondState.LineageID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := second.Replace("", "review/start", secondState)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]reviewtransaction.LensResult, len(secondState.SelectedLenses))
	for index, lens := range secondState.SelectedLenses {
		results[index] = reviewtransaction.LensResult{Lens: lens, Findings: []reviewtransaction.Finding{}, Evidence: []string{"review complete"}}
	}
	if err := secondState.CompleteReview(reviewtransaction.CompactReviewInput{LensResults: results, Classifications: []reviewtransaction.FindingEvidence{}, RefuterOutcomes: []reviewtransaction.EvidenceResult{}}); err != nil {
		t.Fatal(err)
	}
	if err := secondState.CloseCleanReviewOnLastEvent(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Replace(revision, "review/complete-review", secondState); err != nil {
		t.Fatal(err)
	}
	receipt, err := secondState.Receipt()
	if err != nil {
		t.Fatal(err)
	}
	if err := reviewtransaction.WriteCompactReceiptAtomic(second.ReceiptPath(), receipt); err != nil {
		t.Fatal(err)
	}
	// An ambiguous review context is informational. Independent verification
	// remains ready before any optional post-verify review offer.
	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Dependencies.Verify != DependencyReady || status.NextRecommended != "verify" {
		t.Fatalf("ambiguous compact bridge status = %#v, want ready/verify with no pre-verify bridge consultation", status)
	}
}

func TestResolveIgnoresReceiptMismatchAndNonAllowCompactBridgePreVerify(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(t *testing.T, root string)
	}{
		{name: "receipt mismatch", mutate: func(t *testing.T, root string) {
			store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "compact-thin")
			if err != nil {
				t.Fatal(err)
			}
			write(t, store.ReceiptPath(), "{}\n")
		}},
		{name: "post apply gate denies", mutate: func(t *testing.T, root string) {
			write(t, filepath.Join(root, "openspec", "changes", "thin", "tasks.md"), "- [x] 1.1 Done\n# changed after compact approval\n")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
			writeApprovedCompactAuthorityForChange(t, root, changeRoot, "compact-thin")
			tt.mutate(t, root)
			// Wave 4 S3: same rationale as
			// TestResolveIgnoresAmbiguousPathBoundCompactAuthoritiesPreVerify
			// — bridge.Relevant no longer gates verify pre-verify, so a
			// receipt mismatch or a post-apply-gate denial no longer blocks
			// here either. Verify is ready regardless.
			status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
			if err != nil {
				t.Fatal(err)
			}
			if status.Dependencies.Verify != DependencyReady || status.NextRecommended != "verify" {
				t.Fatalf("%s status = %#v, want ready/verify with no pre-verify bridge consultation", tt.name, status)
			}
		})
	}
}

func TestResolveRoutesStaleVerifyEvidenceToIndependentVerify(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	write(t, filepath.Join(changeRoot, "specs", "auth", "spec.md"), "### Requirement: Auth\n#### Scenario: Valid login\n#### Scenario: Rejected login\n")
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("d"), "pass"))
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "compact-thin")

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if status.ReviewGate != nil {
		t.Fatalf("ReviewGate = %#v, want no pre-verify review consultation", status.ReviewGate)
	}
	if len(status.BlockedReasons) != 0 {
		t.Fatalf("BlockedReasons = %v, want empty", status.BlockedReasons)
	}
	if status.Dependencies.Verify != DependencyReady || status.Dependencies.Archive != DependencyBlocked || status.NextRecommended != "verify" {
		t.Fatalf("verify=%q archive=%q next=%q, want ready/blocked/verify", status.Dependencies.Verify, status.Dependencies.Archive, status.NextRecommended)
	}
	if status.RemediationState != (RemediationState{}) {
		t.Fatalf("RemediationState = %#v, want empty for stale evidence", status.RemediationState)
	}
}

func TestResolveRoutesChangeLocalStalePassWithoutRemediation(t *testing.T) {
	tests := []struct {
		name              string
		report            string
		withAuthority     bool
		blockingAuthority bool
		explicitAuthority bool
		reviewDisabled    bool
		wantNext          string
		wantVerify        DependencyState
		wantRemediation   bool
	}{
		{
			// Wave 4 S3 removed pre-verify review supervision: absent
			// authority is decline-by-absence, so stale PASS evidence
			// re-enters verification directly instead of demanding review.
			name:            "historical requirement PASS mismatch restarts verification without review supervision",
			report:          strings.Replace(boundedVerifyEnvelope(shaID("d"), "pass"), "requirements: 1/1", "requirements: 0/0", 1),
			wantNext:        "verify",
			wantVerify:      DependencyReady,
			wantRemediation: false,
		},
		{
			name:            "historical requirement PASS mismatch restarts verification while review is disabled",
			report:          strings.Replace(boundedVerifyEnvelope(shaID("d"), "pass"), "requirements: 1/1", "requirements: 0/0", 1),
			reviewDisabled:  true,
			wantNext:        "verify",
			wantVerify:      DependencyReady,
			wantRemediation: false,
		},
		{
			name:              "historical requirement PASS mismatch with discovered review context restarts verification",
			report:            strings.Replace(boundedVerifyEnvelope(shaID("d"), "pass"), "requirements: 1/1", "requirements: 0/0", 1),
			blockingAuthority: true,
			wantNext:          "verify",
			wantVerify:        DependencyReady,
			wantRemediation:   false,
		},
		{
			name:              "historical requirement PASS mismatch with disabled discovered blocking authority restarts verification",
			report:            strings.Replace(boundedVerifyEnvelope(shaID("d"), "pass"), "requirements: 1/1", "requirements: 0/0", 1),
			blockingAuthority: true,
			reviewDisabled:    true,
			wantNext:          "verify",
			wantVerify:        DependencyReady,
			wantRemediation:   false,
		},
		{
			// While the kill switch is off, zero review code executes:
			// even an explicit escalated receipt never blocks SDD routing.
			name:              "historical requirement PASS mismatch with disabled explicit blocking authority restarts verification",
			report:            strings.Replace(boundedVerifyEnvelope(shaID("d"), "pass"), "requirements: 1/1", "requirements: 0/0", 1),
			blockingAuthority: true,
			explicitAuthority: true,
			reviewDisabled:    true,
			wantNext:          "verify",
			wantVerify:        DependencyReady,
			wantRemediation:   false,
		},
		{
			name:            "genuine FAIL evidence keeps bounded remediation",
			report:          strings.Replace(boundedVerifyEnvelope(shaID("d"), "fail"), "test_exit_code: 0", "test_exit_code: 1", 1),
			withAuthority:   true,
			wantNext:        "remediate",
			wantVerify:      DependencyBlocked,
			wantRemediation: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
			write(t, filepath.Join(changeRoot, "specs", "auth", "spec.md"), "### REQ-1: Auth\n#### Scenario: Valid login\n")
			write(t, filepath.Join(changeRoot, "verify-report.md"), tt.report)
			if tt.withAuthority {
				writeApprovedCompactAuthorityForChange(t, root, changeRoot, "compact-thin")
			}
			if tt.blockingAuthority {
				writeApprovedReviewArtifacts(t, changeRoot)
				if !tt.explicitAuthority {
					if err := os.RemoveAll(filepath.Join(changeRoot, "reviews")); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Remove(filepath.Join(changeRoot, "reviews", "transaction.json")); err != nil {
					t.Fatal(err)
				}
				original := evaluateNativeReviewGate
				evaluateNativeReviewGate = func(context.Context, string, reviewtransaction.Receipt, reviewtransaction.GateRequest) reviewtransaction.NativeGateEvaluation {
					return reviewtransaction.NativeGateEvaluation{Result: reviewtransaction.GateEscalated}
				}
				t.Cleanup(func() { evaluateNativeReviewGate = original })
			}

			status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin", ReviewDisabled: tt.reviewDisabled})
			if err != nil {
				t.Fatal(err)
			}
			if status.NextRecommended != tt.wantNext || status.Dependencies.Verify != tt.wantVerify || status.RemediationState.Required != tt.wantRemediation {
				t.Fatalf("status = next %q verify %q remediation %#v, want %q/%q/remediation=%v", status.NextRecommended, status.Dependencies.Verify, status.RemediationState, tt.wantNext, tt.wantVerify, tt.wantRemediation)
			}
			if status.Dependencies.Verify != DependencyAllDone && status.ReviewGate != nil {
				t.Fatalf("ReviewGate = %#v, want no pre-verify review consultation", status.ReviewGate)
			}
			if tt.reviewDisabled && status.ReviewGate != nil {
				t.Fatalf("disabled ReviewGate = %#v, want structural absence while the switch is off", status.ReviewGate)
			}
			if !tt.wantRemediation {
				reasons := strings.Join(status.BlockedReasons, "\n")
				if strings.Contains(reasons, "bounded review transaction is missing") || strings.Contains(reasons, "remediation") {
					t.Fatalf("stale PASS entered failed-evidence routing: %v", status.BlockedReasons)
				}
			}
		})
	}
}

func TestResolveIgnoresForeignReviewContextBeforeVerify(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	foreignRoot := seedReadyChange(t, root, "other", "- [x] 1.1 Done\n")
	write(t, filepath.Join(changeRoot, "specs", "auth", "spec.md"), "### Requirement: Auth\n#### Scenario: Valid login\n#### Scenario: Rejected login\n")
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("d"), "pass"))
	writeApprovedCompactAuthorityForChange(t, root, foreignRoot, "compact-other")

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if status.Dependencies.Verify != DependencyReady || status.NextRecommended != "verify" || status.ReviewGate != nil {
		t.Fatalf("status = %#v, want independent verify without review consultation", status)
	}
	if len(status.BlockedReasons) != 0 {
		t.Fatalf("BlockedReasons = %v, want no review-derived blocker", status.BlockedReasons)
	}
}

func TestResolveEngramRoutesStaleVerifyEvidenceToVerifyUnderApprovedCompactAuthority(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "compact-thin")
	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "compact-thin")
	if err != nil {
		t.Fatal(err)
	}
	receiptPayload := readText(store.ReceiptPath())
	if receiptPayload == "" {
		t.Fatal("compact receipt payload is empty")
	}
	mkdir(t, filepath.Join(root, ".engram"))
	project := strings.ToLower(filepath.Base(root))
	restore := stubEngramExport(t, []engramObservation{
		{Title: "sdd/thin/proposal", Content: "# Proposal\n", Project: project, Scope: "project"},
		{Title: "sdd/thin/spec", Content: "### Requirement: Auth\n#### Scenario: Valid login\n#### Scenario: Rejected login\n", Project: project, Scope: "project"},
		{Title: "sdd/thin/design", Content: "# Design\n", Project: project, Scope: "project"},
		{Title: "sdd/thin/tasks", Content: "- [x] 1.1 Done\n", Project: project, Scope: "project"},
		{Title: "sdd/thin/verify-report", Content: boundedVerifyEnvelope(shaID("d"), "pass"), Project: project, Scope: "project"},
		{Title: "sdd/thin/review/receipt", Content: receiptPayload, Project: project, Scope: "project"},
	})
	defer restore()

	status, ok, err := resolveEngramStatus(root, "thin", false, false)
	if err != nil {
		t.Fatalf("resolveEngramStatus() error = %v", err)
	}
	if !ok {
		t.Fatal("resolveEngramStatus() did not retain the Engram change")
	}
	if status.ReviewGate != nil {
		t.Fatalf("ReviewGate = %#v, want no pre-verify review consultation", status.ReviewGate)
	}
	if len(status.BlockedReasons) != 0 {
		t.Fatalf("BlockedReasons = %v, want empty", status.BlockedReasons)
	}
	if status.Dependencies.Verify != DependencyReady || status.Dependencies.Archive != DependencyBlocked || status.NextRecommended != "verify" {
		t.Fatalf("verify=%q archive=%q next=%q, want ready/blocked/verify", status.Dependencies.Verify, status.Dependencies.Archive, status.NextRecommended)
	}
	if status.RemediationState != (RemediationState{}) {
		t.Fatalf("RemediationState = %#v, want empty for stale evidence", status.RemediationState)
	}
}

func TestResolveEngramRoutesStalePassWithDisabledBlockingAuthority(t *testing.T) {
	// While the kill switch is off, zero review code executes: neither
	// discovered nor explicit review artifacts may block SDD routing or
	// project a review gate, and stale PASS evidence re-enters verification.
	tests := []struct {
		name              string
		explicitAuthority bool
		wantNext          string
		wantVerify        DependencyState
	}{
		{
			name:       "discovered authority is unmanaged",
			wantNext:   "verify",
			wantVerify: DependencyReady,
		},
		{
			name:              "explicit authority is unmanaged while disabled",
			explicitAuthority: true,
			wantNext:          "verify",
			wantVerify:        DependencyReady,
		},
	}

	original := evaluateNativeReviewGate
	t.Cleanup(func() { evaluateNativeReviewGate = original })
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
			write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("d"), "pass"))
			writeApprovedReviewArtifacts(t, changeRoot)
			receipt := readText(filepath.Join(changeRoot, "reviews", "receipt.json"))
			if !tt.explicitAuthority {
				if err := os.RemoveAll(filepath.Join(changeRoot, "reviews")); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(filepath.Join(changeRoot, "reviews", "transaction.json")); err != nil {
				t.Fatal(err)
			}
			evaluateNativeReviewGate = func(context.Context, string, reviewtransaction.Receipt, reviewtransaction.GateRequest) reviewtransaction.NativeGateEvaluation {
				return reviewtransaction.NativeGateEvaluation{Result: reviewtransaction.GateEscalated}
			}

			mkdir(t, filepath.Join(root, ".engram"))
			project := strings.ToLower(filepath.Base(root))
			observations := []engramObservation{
				{Title: "sdd/thin/proposal", Content: "# Proposal\n", Project: project, Scope: "project"},
				{Title: "sdd/thin/spec", Content: "### REQ-1: Auth\n#### Scenario: Valid login\n", Project: project, Scope: "project"},
				{Title: "sdd/thin/design", Content: "# Design\n", Project: project, Scope: "project"},
				{Title: "sdd/thin/tasks", Content: "- [x] 1.1 Done\n", Project: project, Scope: "project"},
				{Title: "sdd/thin/verify-report", Content: strings.Replace(boundedVerifyEnvelope(shaID("d"), "pass"), "requirements: 1/1", "requirements: 0/0", 1), Project: project, Scope: "project"},
			}
			if tt.explicitAuthority {
				observations = append(observations, engramObservation{Title: "sdd/thin/review/receipt", Content: receipt, Project: project, Scope: "project"})
			}
			restore := stubEngramExport(t, observations)
			defer restore()

			status, ok, err := resolveEngramStatus(root, "thin", false, true)
			if err != nil || !ok {
				t.Fatalf("resolveEngramStatus() = ok %v, error %v", ok, err)
			}
			if status.NextRecommended != tt.wantNext || status.Dependencies.Verify != tt.wantVerify || status.Dependencies.Archive != DependencyBlocked {
				t.Fatalf("status = next %q verify %q archive %q, want %q/%q/blocked", status.NextRecommended, status.Dependencies.Verify, status.Dependencies.Archive, tt.wantNext, tt.wantVerify)
			}
			if status.ReviewGate != nil {
				t.Fatalf("disabled ReviewGate = %#v, want structural absence while the switch is off", status.ReviewGate)
			}
			if reasons := strings.Join(status.BlockedReasons, "\n"); strings.Contains(reasons, "escalated the receipt") || strings.Contains(reasons, "remediation") {
				t.Fatalf("disabled BlockedReasons = %v, want no review or remediation blocking", status.BlockedReasons)
			}
		})
	}
}

func TestResolveEngramRejectsForeignCompactAuthorityForStaleVerifyEvidence(t *testing.T) {
	root := t.TempDir()
	foreignRoot := seedReadyChange(t, root, "other", "- [x] 1.1 Done\n")
	writeApprovedCompactAuthorityForChange(t, root, foreignRoot, "compact-other")
	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "compact-other")
	if err != nil {
		t.Fatal(err)
	}
	receiptPayload := readText(store.ReceiptPath())
	mkdir(t, filepath.Join(root, ".engram"))
	project := strings.ToLower(filepath.Base(root))
	restore := stubEngramExport(t, []engramObservation{
		{Title: "sdd/thin/proposal", Content: "# Proposal\n", Project: project, Scope: "project"},
		{Title: "sdd/thin/spec", Content: "### Requirement: Auth\n#### Scenario: Valid login\n#### Scenario: Rejected login\n", Project: project, Scope: "project"},
		{Title: "sdd/thin/design", Content: "# Design\n", Project: project, Scope: "project"},
		{Title: "sdd/thin/tasks", Content: "- [x] 1.1 Done\n", Project: project, Scope: "project"},
		{Title: "sdd/thin/verify-report", Content: boundedVerifyEnvelope(shaID("d"), "pass"), Project: project, Scope: "project"},
		{Title: "sdd/thin/review/receipt", Content: receiptPayload, Project: project, Scope: "project"},
	})
	defer restore()

	status, ok, err := resolveEngramStatus(root, "thin", false, false)
	if err != nil || !ok {
		t.Fatalf("resolveEngramStatus() = ok %v, error %v", ok, err)
	}
	if status.Dependencies.Verify != DependencyReady || status.NextRecommended != "verify" {
		t.Fatalf("verify=%q next=%q, want ready/verify", status.Dependencies.Verify, status.NextRecommended)
	}
	if status.ReviewGate != nil {
		t.Fatalf("ReviewGate = %#v, want no pre-verify review consultation", status.ReviewGate)
	}
	if len(status.BlockedReasons) != 0 {
		t.Fatalf("BlockedReasons = %v, want no review-derived blocker", status.BlockedReasons)
	}
}

func TestResolveGrantsCompactRemediationBudgetForFailedVerdictWithIncompleteScenarios(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	write(t, filepath.Join(changeRoot, "specs", "auth", "spec.md"), "### Requirement: Auth\n#### Scenario: Valid login\n#### Scenario: Added after verification\n")
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("d"), "fail"))
	writeApprovedCompactAuthorityForChange(t, root, changeRoot, "compact-thin")
	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), root, "compact-thin")
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if status.Dependencies.Verify != DependencyBlocked || status.NextRecommended != "remediate" {
		t.Fatalf("verify=%q next=%q, want blocked/remediate for failed verdict", status.Dependencies.Verify, status.NextRecommended)
	}
	wantBudget := before.State.CorrectionBudget - before.State.CumulativeCorrectionLines
	if !status.RemediationState.Required || status.RemediationState.CorrectionBudgetRemaining != wantBudget || wantBudget <= 0 || status.RemediationState.LineageID != "compact-thin" || status.RemediationState.FailedEvidenceRevision != shaID("d") {
		t.Fatalf("RemediationState = %#v, want transaction-bound nonzero compact budget", status.RemediationState)
	}
	if status.RemediationState.CorrectionBudgetTotal != before.State.CorrectionBudget {
		t.Fatalf("CorrectionBudgetTotal = %d, want frozen total %d", status.RemediationState.CorrectionBudgetTotal, before.State.CorrectionBudget)
	}
	after, err := store.Load()
	if err != nil || after.Revision != before.Revision {
		t.Fatalf("compact authority changed during status resolution: before=%q after=%q err=%v", before.Revision, after.Revision, err)
	}
}

func TestResolveRejectsCompactRemediationWhenFrozenBudgetIsZero(t *testing.T) {
	compact := reviewtransaction.CompactState{
		LineageID: "compact-thin", Generation: 1, State: reviewtransaction.StateApproved,
		CorrectionBudget: 2, CumulativeCorrectionLines: 2,
	}
	state := resolveBoundedRemediation(true, false, verifyResultEvaluation{
		EvidenceRevision: shaID("d"), Reason: "scenarios are incomplete",
	}, nil, &compact, "bounded review transaction is missing", "")

	if state.Required || !strings.Contains(state.Reason, "compact review authority has no correction budget") {
		t.Fatalf("RemediationState = %#v, want fail-closed exhausted budget", state)
	}
}

func TestResolveRejectsCompactRemediationAfterSingleConsumedAttempt(t *testing.T) {
	compact := reviewtransaction.CompactState{
		LineageID: "compact-thin", Generation: 1, State: reviewtransaction.StateApproved,
		CorrectionBudget: 10, CumulativeCorrectionLines: 1,
		CorrectionAttempts: []reviewtransaction.CompactCorrectionAttempt{{ActualLines: 1}},
	}
	state := resolveBoundedRemediation(true, false, verifyResultEvaluation{
		EvidenceRevision: shaID("d"), Reason: "scenarios are incomplete",
	}, nil, &compact, "bounded review transaction is missing", "")

	if state.Required || state.Reason != "compact review authority has exhausted its correction attempts" {
		t.Fatalf("RemediationState = %#v, want exhausted attempts with remaining line budget", state)
	}
}

func TestResolveBoundedRemediationExposesUnambiguousRemainingAndTotalBudget(t *testing.T) {
	compact := reviewtransaction.CompactState{
		LineageID: "compact-thin", Generation: 1, State: reviewtransaction.StateApproved,
		CorrectionBudget: 10, CumulativeCorrectionLines: 3,
	}
	state := resolveBoundedRemediation(true, false, verifyResultEvaluation{
		EvidenceRevision: shaID("d"), Reason: "scenarios are incomplete",
	}, nil, &compact, "bounded review transaction is missing", "")

	if !state.Required {
		t.Fatalf("RemediationState = %#v, want required", state)
	}
	if state.CorrectionBudgetRemaining != 7 {
		t.Fatalf("CorrectionBudgetRemaining = %d, want 7", state.CorrectionBudgetRemaining)
	}
	if state.CorrectionBudgetTotal != 10 {
		t.Fatalf("CorrectionBudgetTotal = %d, want 10", state.CorrectionBudgetTotal)
	}
	if state.CorrectionBudgetRemaining == state.CorrectionBudgetTotal {
		t.Fatalf("remaining and total must diverge once a correction is already charged, got both %d", state.CorrectionBudgetRemaining)
	}

	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, exists := document["correctionBudget"]; exists {
		t.Fatal("RemediationState JSON still emits the ambiguous shared correctionBudget label")
	}
	var remaining int
	if err := json.Unmarshal(document["correctionBudgetRemaining"], &remaining); err != nil || remaining != 7 {
		t.Fatalf("correctionBudgetRemaining JSON = %s (err=%v), want 7", document["correctionBudgetRemaining"], err)
	}
	var total int
	if err := json.Unmarshal(document["correctionBudgetTotal"], &total); err != nil || total != 10 {
		t.Fatalf("correctionBudgetTotal JSON = %s (err=%v), want 10", document["correctionBudgetTotal"], err)
	}
}

// TestResolveBoundedRemediationSurfacesEscalationAccountingForAlreadyEscalatedCompactAuthority
// pins that a compact authority already in StateEscalated never offers a
// fresh remediation attempt, and that its Reason names spent, remaining, and
// total as distinct labeled values instead of silently stopping (design
// Duty 6: the compact.go:1171 escalation seam records none of its own by
// default).
func TestResolveBoundedRemediationSurfacesEscalationAccountingForAlreadyEscalatedCompactAuthority(t *testing.T) {
	t.Run("budget exceeded", func(t *testing.T) {
		compact := reviewtransaction.CompactState{
			LineageID: "compact-thin", Generation: 1, State: reviewtransaction.StateEscalated,
			CorrectionBudget: 10, CumulativeCorrectionLines: 12,
		}
		state := resolveBoundedRemediation(true, false, verifyResultEvaluation{
			EvidenceRevision: shaID("d"), Reason: "scenarios are incomplete",
		}, nil, &compact, "bounded review transaction is missing", "")

		if state.Required {
			t.Fatalf("RemediationState = %#v, want no remediation offered for an already-escalated authority", state)
		}
		for _, want := range []string{"spent 12", "remaining 0", "total 10"} {
			if !strings.Contains(state.Reason, want) {
				t.Fatalf("Reason = %q, want it to contain %q", state.Reason, want)
			}
		}
	})

	t.Run("original criteria failed within budget", func(t *testing.T) {
		originalFailed := reviewtransaction.ValidationCheck{Passed: false}
		regressionPassed := reviewtransaction.ValidationCheck{Passed: true}
		compact := reviewtransaction.CompactState{
			LineageID: "compact-thin", Generation: 1, State: reviewtransaction.StateEscalated,
			CorrectionBudget: 10, CumulativeCorrectionLines: 3,
			OriginalCriteria: &originalFailed, CorrectionRegression: &regressionPassed,
		}
		state := resolveBoundedRemediation(true, false, verifyResultEvaluation{
			EvidenceRevision: shaID("d"), Reason: "scenarios are incomplete",
		}, nil, &compact, "bounded review transaction is missing", "")

		if state.Required {
			t.Fatalf("RemediationState = %#v, want no remediation offered for an already-escalated authority", state)
		}
		for _, want := range []string{"original_criteria_failed", "spent 3", "remaining 7", "total 10"} {
			if !strings.Contains(state.Reason, want) {
				t.Fatalf("Reason = %q, want it to contain %q", state.Reason, want)
			}
		}
	})
}

// TestEscalationAccountingReasonTemplateKeepsSDDBoundOutputByteIdentical pins
// the exact bytes the SDD-bound surface emits. Making the organic gate surface
// render the same accounting moved the template's definition into
// reviewtransaction so both layers can reach it; this proves the move changed
// neither the template nor a single byte resolveBoundedRemediation produces.
func TestEscalationAccountingReasonTemplateKeepsSDDBoundOutputByteIdentical(t *testing.T) {
	const wantTemplate = "compact review authority is escalated (%s): spent %d, remaining %d, total %d correction lines"
	if EscalationAccountingReasonTemplate != wantTemplate {
		t.Fatalf("EscalationAccountingReasonTemplate = %q, want %q", EscalationAccountingReasonTemplate, wantTemplate)
	}
	compact := reviewtransaction.CompactState{
		LineageID: "compact-thin", Generation: 1, State: reviewtransaction.StateEscalated,
		CorrectionBudget: 10, CumulativeCorrectionLines: 12,
	}
	state := resolveBoundedRemediation(true, false, verifyResultEvaluation{
		EvidenceRevision: shaID("d"), Reason: "scenarios are incomplete",
	}, nil, &compact, "bounded review transaction is missing", "")
	const want = "compact review authority is escalated (budget_exceeded): spent 12, remaining 0, total 10 correction lines"
	if state.Reason != want {
		t.Fatalf("SDD-bound escalation reason = %q, want %q", state.Reason, want)
	}
}

// TestResolveBoundedRemediationNoCorrectionBudgetGuardNeverPopulatesRemaining
// pins the `remainingBudget <= 0` early-return guard in
// resolveBoundedRemediation: it fires for every `spent >= total` shape
// (exactly exhausted and over-exhausted) and returns before
// RemediationState.CorrectionBudgetRemaining/CorrectionBudgetTotal are ever
// populated, so those fields stay their int zero value on this path.
//
// Refuted claim: a prior report alleged sddstatus/review_gate.go could emit
// a negative CorrectionBudgetRemaining. Verified false for this call site —
// the guard above returns RemediationState{Reason: ...} strictly before the
// struct literal that sets CorrectionBudgetRemaining is reached, so a
// negative value can never leave resolveBoundedRemediation through this
// path. This test pins that guard; it changes no production code.
func TestResolveBoundedRemediationNoCorrectionBudgetGuardNeverPopulatesRemaining(t *testing.T) {
	tests := []struct {
		name                      string
		correctionBudget          int
		cumulativeCorrectionLines int
	}{
		{name: "spent exactly equals total", correctionBudget: 10, cumulativeCorrectionLines: 10},
		{name: "spent exceeds total", correctionBudget: 10, cumulativeCorrectionLines: 11},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compact := reviewtransaction.CompactState{
				LineageID: "compact-thin", Generation: 1, State: reviewtransaction.StateApproved,
				CorrectionBudget: test.correctionBudget, CumulativeCorrectionLines: test.cumulativeCorrectionLines,
			}
			state := resolveBoundedRemediation(true, false, verifyResultEvaluation{
				EvidenceRevision: shaID("d"), Reason: "scenarios are incomplete",
			}, nil, &compact, "bounded review transaction is missing", "")

			if state.Required {
				t.Fatalf("RemediationState = %#v, want the exhausted-budget guard to refuse remediation", state)
			}
			if state.Reason != "compact review authority has no correction budget remaining" {
				t.Fatalf("Reason = %q, want the exhausted-budget guard reason", state.Reason)
			}
			if state.CorrectionBudgetRemaining != 0 || state.CorrectionBudgetTotal != 0 {
				t.Fatalf("RemediationState = %#v, want CorrectionBudgetRemaining/Total to stay unset (zero value) on the early-return path", state)
			}
		})
	}
}

func TestResolveRemediationIsBoundToBudgetAndFailedEvidenceRevision(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	write(t, filepath.Join(changeRoot, "specs", "auth", "spec.md"), "### Requirement: Auth\n#### Scenario: Valid login\n")
	revision := shaID("d")
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(revision, "fail"))

	tx := remediationTransaction(t, revision, false)
	writeJSON(t, filepath.Join(changeRoot, "reviews", "transaction.json"), tx)
	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if status.NextRecommended != "remediate" || !status.RemediationState.Required {
		t.Fatalf("next=%q remediation=%#v, want bounded remediation", status.NextRecommended, status.RemediationState)
	}

	stale := remediationTransaction(t, shaID("e"), false)
	writeJSON(t, filepath.Join(changeRoot, "reviews", "transaction.json"), stale)
	status, err = Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatalf("Resolve(stale) error = %v", err)
	}
	if status.NextRecommended != "verify" || status.RemediationState.Required {
		t.Fatalf("stale next=%q remediation=%#v, want independent verification retry", status.NextRecommended, status.RemediationState)
	}
	if !strings.Contains(strings.Join(status.BlockedReasons, "\n"), "does not match failed evidence revision") {
		t.Fatalf("BlockedReasons = %v", status.BlockedReasons)
	}
}

func TestResolveMalformedFailedEvidenceKeepsMatchingRevisionBound(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	revision := shaID("d")
	report := strings.Replace(boundedVerifyEnvelope(revision, "fail"), "blockers: 0", "blockers: 1", 1)
	report = strings.Replace(report, "test_output_hash: "+shaID("2"), "test_output_hash: sha256:invalid", 1)
	admission := ValidateVerifyReportAdmission(report, SpecCounts{Requirements: 1, Scenarios: 1})
	if admission.Valid || !strings.Contains(admission.Reason, "invalid test_output_hash") {
		t.Fatalf("admission = %#v, want denied malformed hash", admission)
	}
	write(t, filepath.Join(changeRoot, "verify-report.md"), report)
	writeJSON(t, filepath.Join(changeRoot, "reviews", "transaction.json"), remediationTransaction(t, revision, false))

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.NextRecommended != "verify" || status.Dependencies.Verify != DependencyReady || status.RemediationState.Required {
		t.Fatalf("next=%q verify=%q remediation=%#v, want malformed evidence to re-enter independent verification", status.NextRecommended, status.Dependencies.Verify, status.RemediationState)
	}
	if status.Dependencies.Archive != DependencyBlocked {
		t.Fatalf("archive=%q, malformed report must not become archive-ready", status.Dependencies.Archive)
	}
}

func seedBoundedReadyChange(t *testing.T, root string) string {
	t.Helper()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Work\n")
	write(t, filepath.Join(changeRoot, "specs", "auth", "spec.md"), "### Requirement: Auth\n#### Scenario: Valid login\n")
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("1"), "pass"))
	return changeRoot
}

func writeApprovedReviewArtifacts(t *testing.T, changeRoot string) {
	t.Helper()
	repo := filepath.Dir(filepath.Dir(filepath.Dir(changeRoot)))
	runSDDStatusGit(t, repo, "init", "-q")
	runSDDStatusGit(t, repo, "config", "user.email", "status@example.com")
	runSDDStatusGit(t, repo, "config", "user.name", "Status Test")
	runSDDStatusGit(t, repo, "add", ".")
	runSDDStatusGit(t, repo, "commit", "-qm", "base")
	tasksPath := filepath.Join(changeRoot, "tasks.md")
	tasks, err := os.ReadFile(tasksPath)
	if err != nil {
		t.Fatal(err)
	}
	write(t, tasksPath, string(tasks)+"\n# Reviewed candidate\n")

	snapshot, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).Build(context.Background(), reviewtransaction.Target{
		Kind: reviewtransaction.TargetCurrentChanges, IntendedUntracked: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewsDir := filepath.Join(changeRoot, "reviews")
	policyPath := filepath.Join(reviewsDir, "policy.md")
	ledgerPath := filepath.Join(reviewsDir, "ledger.json")
	verifyPath := filepath.Join(changeRoot, "verify-report.md")
	write(t, policyPath, "bounded archive policy\n")
	write(t, ledgerPath, reviewtransaction.CanonicalEmptyLedger)
	policyHash, err := reviewtransaction.HashArtifact(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	ledgerHash, err := reviewtransaction.HashArtifact(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	evidenceHash, err := reviewtransaction.HashArtifact(verifyPath)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := reviewtransaction.NewTransaction(reviewtransaction.Start{
		LineageID: "thin-lineage", Mode: reviewtransaction.ModeOrdinary4R, Generation: 1,
		Snapshot: snapshot, PolicyHash: policyHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewtransaction.AuthoritativeStore(context.Background(), repo, "thin-lineage")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.StartReview(); err != nil {
		t.Fatal(err)
	}
	revision, err := store.Append("", reviewtransaction.Record{Operation: "review/start", Transaction: *tx})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := reviewtransaction.CanonicalLedger([]reviewtransaction.Finding{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.FreezeFindings([]reviewtransaction.Finding{}, ledger, ledgerHash); err != nil {
		t.Fatal(err)
	}
	revision, err = store.Append(revision, reviewtransaction.Record{Operation: "review/freeze-findings", Transaction: *tx})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ClassifyEvidence([]reviewtransaction.FindingEvidence{}); err != nil {
		t.Fatal(err)
	}
	revision, err = store.Append(revision, reviewtransaction.Record{Operation: "review/classify", Transaction: *tx})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.BeginFinalVerification(); err != nil {
		t.Fatal(err)
	}
	revision, err = store.Append(revision, reviewtransaction.Record{Operation: "review/begin-final-verification", Transaction: *tx})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CompleteFinalVerification(evidenceHash, true); err != nil {
		t.Fatal(err)
	}
	revision, err = store.Append(revision, reviewtransaction.Record{Operation: "review/complete-final-verification", Transaction: *tx})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := tx.Receipt()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := store.ExportBundle()
	if err != nil {
		t.Fatal(err)
	}
	request := reviewtransaction.GateRequest{
		Schema: reviewtransaction.GateRequestSchema, Gate: reviewtransaction.GatePostApply,
		Target:          reviewtransaction.Target{Kind: reviewtransaction.TargetCurrentChanges, IntendedUntracked: []string{}},
		StoreRevision:   revision,
		GenesisRevision: bundle.GenesisRevision, ChainIdentity: bundle.ChainIdentity, BundleDigest: bundle.BundleDigest,
		PolicyArtifact: policyPath, LedgerArtifact: ledgerPath, EvidenceArtifact: verifyPath,
	}
	writeJSON(t, filepath.Join(reviewsDir, "transaction.json"), tx)
	writeJSON(t, filepath.Join(reviewsDir, "receipt.json"), receipt)
	if err := reviewtransaction.WriteReceiptAtomic(filepath.Join(store.Dir, "artifacts", "receipt.json"), receipt); err != nil {
		t.Fatal(err)
	}
	if err := reviewtransaction.WriteChainBundleAtomic(filepath.Join(reviewsDir, "chain-bundle.json"), bundle); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(reviewsDir, "gate-context.json"), request)
}

func writeApprovedCompactAuthorityForChange(t *testing.T, repo, changeRoot, lineage string) {
	writeApprovedCompactAuthorityForChangeWithTasks(t, repo, changeRoot, lineage, "- [x] 1.1 Done\n# approved compact scope\n")
}

func writeApprovedCompactAuthorityForChangeWithTasks(t *testing.T, repo, changeRoot, lineage, tasks string) {
	writeApprovedCompactAuthorityForChangeWithCandidate(t, repo, changeRoot, lineage, tasks, nil)
}

func writeApprovedCompactAuthorityForChangeWithCandidate(t *testing.T, repo, changeRoot, lineage, tasks string, prepareCandidate func()) {
	t.Helper()
	runSDDStatusGit(t, repo, "init", "-q")
	runSDDStatusGit(t, repo, "config", "user.email", "status@example.com")
	runSDDStatusGit(t, repo, "config", "user.name", "Status Test")
	runSDDStatusGit(t, repo, "add", ".")
	runSDDStatusGit(t, repo, "commit", "-qm", "base")
	write(t, filepath.Join(changeRoot, "tasks.md"), tasks)
	if prepareCandidate != nil {
		prepareCandidate()
	}
	snapshot, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).Build(context.Background(), reviewtransaction.Target{Kind: reviewtransaction.TargetCurrentChanges, IntendedUntracked: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	risk, lines, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).ClassifySnapshotRisk(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	lenses := compactStatusLenses(risk)
	state, err := reviewtransaction.NewCompactState(reviewtransaction.Start{LineageID: lineage, Mode: reviewtransaction.ModeOrdinaryBounded, Generation: 1, Snapshot: snapshot, PolicyHash: shaID("c"), RiskLevel: risk, SelectedLenses: lenses, OriginalChangedLines: &lines})
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := store.Replace("", "review/start", state)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]reviewtransaction.LensResult, len(lenses))
	for index, lens := range lenses {
		results[index] = reviewtransaction.LensResult{Lens: lens, Findings: []reviewtransaction.Finding{}, Evidence: []string{"review complete"}}
	}
	if err := state.CompleteReview(reviewtransaction.CompactReviewInput{LensResults: results, Classifications: []reviewtransaction.FindingEvidence{}, RefuterOutcomes: []reviewtransaction.EvidenceResult{}}); err != nil {
		t.Fatal(err)
	}
	if err := state.CloseCleanReviewOnLastEvent(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Replace(revision, "review/complete-review", state); err != nil {
		t.Fatal(err)
	}
	receipt, err := state.Receipt()
	if err != nil {
		t.Fatal(err)
	}
	if err := reviewtransaction.WriteCompactReceiptAtomic(store.ReceiptPath(), receipt); err != nil {
		t.Fatal(err)
	}
}

func writeAdditionalApprovedNativeReceipt(t *testing.T, repo, lineage string) {
	t.Helper()
	sourceStore, err := reviewtransaction.AuthoritativeStore(context.Background(), repo, "thin-lineage")
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := sourceStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := reviewtransaction.NewTransaction(reviewtransaction.Start{
		LineageID: lineage, Mode: source.Transaction.Mode, Generation: source.Transaction.Generation,
		Snapshot: source.Transaction.Snapshot, PolicyHash: source.Transaction.PolicyHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewtransaction.AuthoritativeStore(context.Background(), repo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.StartReview(); err != nil {
		t.Fatal(err)
	}
	revision, err := store.Append("", reviewtransaction.Record{Operation: "review/start", Transaction: *tx})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := reviewtransaction.CanonicalLedger([]reviewtransaction.Finding{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.FreezeFindings([]reviewtransaction.Finding{}, ledger, source.Transaction.LedgerHash); err != nil {
		t.Fatal(err)
	}
	revision, err = store.Append(revision, reviewtransaction.Record{Operation: "review/freeze-findings", Transaction: *tx})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ClassifyEvidence([]reviewtransaction.FindingEvidence{}); err != nil {
		t.Fatal(err)
	}
	revision, err = store.Append(revision, reviewtransaction.Record{Operation: "review/classify", Transaction: *tx})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.BeginFinalVerification(); err != nil {
		t.Fatal(err)
	}
	revision, err = store.Append(revision, reviewtransaction.Record{Operation: "review/begin-final-verification", Transaction: *tx})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CompleteFinalVerification(source.Transaction.EvidenceHash, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(revision, reviewtransaction.Record{Operation: "review/complete-final-verification", Transaction: *tx}); err != nil {
		t.Fatal(err)
	}
	receipt, err := tx.Receipt()
	if err != nil {
		t.Fatal(err)
	}
	if err := reviewtransaction.WriteReceiptAtomic(filepath.Join(store.Dir, "artifacts", "receipt.json"), receipt); err != nil {
		t.Fatal(err)
	}
}

func TestApplyReviewGateDiscoversCompactStateAndReceiptWithoutMirrors(t *testing.T) {
	repo := t.TempDir()
	runSDDStatusGit(t, repo, "init", "-q")
	runSDDStatusGit(t, repo, "config", "user.email", "test@example.com")
	runSDDStatusGit(t, repo, "config", "user.name", "Test")
	write(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runSDDStatusGit(t, repo, "add", "tracked.txt")
	runSDDStatusGit(t, repo, "commit", "-qm", "base")
	write(t, filepath.Join(repo, "tracked.txt"), "candidate\n")
	snapshot, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).Build(context.Background(), reviewtransaction.Target{Kind: reviewtransaction.TargetCurrentChanges, IntendedUntracked: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	risk, lines, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).ClassifySnapshotRisk(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	lenses := []string{}
	if risk == reviewtransaction.RiskMedium {
		lenses = []string{reviewtransaction.LensReliability}
	} else if risk == reviewtransaction.RiskHigh {
		lenses = []string{reviewtransaction.LensRisk, reviewtransaction.LensResilience, reviewtransaction.LensReadability, reviewtransaction.LensReliability}
	}
	state, err := reviewtransaction.NewCompactState(reviewtransaction.Start{
		LineageID: "compact-sdd", Mode: reviewtransaction.ModeOrdinaryBounded, Generation: 1,
		Snapshot: snapshot, PolicyHash: shaID("1"), RiskLevel: risk, SelectedLenses: lenses, OriginalChangedLines: &lines,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, _ := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, state.LineageID)
	revision, err := store.Replace("", "review/start", state)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]reviewtransaction.LensResult, len(lenses))
	for index, lens := range lenses {
		results[index] = reviewtransaction.LensResult{Lens: lens, Findings: []reviewtransaction.Finding{}, Evidence: []string{"independent causal review completed"}}
	}
	if err := state.CompleteReview(reviewtransaction.CompactReviewInput{LensResults: results, Classifications: []reviewtransaction.FindingEvidence{}, RefuterOutcomes: []reviewtransaction.EvidenceResult{}}); err != nil {
		t.Fatal(err)
	}
	if err := state.CloseCleanReviewOnLastEvent(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Replace(revision, "review/complete-review", state); err != nil {
		t.Fatal(err)
	}
	receipt, _ := state.Receipt()
	if err := reviewtransaction.WriteCompactReceiptAtomic(store.ReceiptPath(), receipt); err != nil {
		t.Fatal(err)
	}
	status := Status{Dependencies: Dependencies{Verify: DependencyAllDone, Archive: DependencyReady}, TaskProgress: TaskProgress{AllComplete: true}}
	applyReviewGate(&status, repo, "", "", false)
	if status.ReviewGate == nil || status.ReviewGate.Result != reviewtransaction.GateAllow || status.Dependencies.Archive != DependencyReady {
		t.Fatalf("compact SDD gate = %#v", status)
	}
}

func boundedVerifyEnvelope(revision, verdict string) string {
	return strings.Join([]string{
		"```yaml",
		"schema: gentle-ai.verify-result/v1",
		"evidence_revision: " + revision,
		"verdict: " + verdict,
		"blockers: 0",
		"critical_findings: 0",
		"requirements: 1/1",
		"scenarios: 1/1",
		"test_command: go test ./internal/example",
		"test_exit_code: 0",
		"test_output_hash: " + shaID("2"),
		"build_command: go test ./cmd/gentle-ai",
		"build_exit_code: 0",
		"build_output_hash: " + shaID("3"),
		"```",
	}, "\n")
}

func remediationTransaction(t *testing.T, revision string, ready bool) reviewtransaction.Transaction {
	t.Helper()
	tx, err := reviewtransaction.NewTransaction(reviewtransaction.Start{
		LineageID: "thin-lineage", Mode: reviewtransaction.ModeOrdinary4R, Generation: 1,
		Snapshot: reviewtransaction.Snapshot{
			Kind: reviewtransaction.TargetCurrentChanges, BaseTree: strings.Repeat("a", 40), CandidateTree: strings.Repeat("b", 40),
			PathsDigest: shaID("4"), IntendedUntracked: []string{}, IntendedUntrackedProof: shaID("8"),
			Paths: []string{"internal/example.go"}, Identity: shaID("9"),
		},
		PolicyHash: shaID("5"),
	})
	if err != nil {
		t.Fatalf("NewTransaction() error = %v", err)
	}
	_ = tx.StartReview()
	_ = freezeStatusFindings(tx, []reviewtransaction.Finding{{ID: "R1-001", Severity: "CRITICAL"}})
	_, _ = tx.ClassifyEvidence([]reviewtransaction.FindingEvidence{{FindingID: "R1-001", Class: reviewtransaction.EvidenceDeterministic, Causality: reviewtransaction.CausalIntroduced, Proof: "failing focused test"}})
	if err := tx.BeginFix(revision); err != nil {
		t.Fatalf("BeginFix() error = %v", err)
	}
	if ready {
		fix := tx.Snapshot
		fix.Kind = reviewtransaction.TargetFixDiff
		fix.BaseTree = tx.FinalCandidateTree
		fix.CandidateTree = strings.Repeat("c", 40)
		fix.LedgerIDs = []string{"R1-001"}
		fix.Identity = shaID("a")
		if err := tx.CompleteFix(fix, shaID("b"), []string{"R1-001"}); err != nil {
			t.Fatalf("CompleteFix() error = %v", err)
		}
		if err := tx.ValidateFixDelta([]string{"R1-001"}, true); err != nil {
			t.Fatalf("ValidateFixDelta() error = %v", err)
		}
	}
	return *tx
}

func readJSON(t *testing.T, path string, destination any) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, destination); err != nil {
		t.Fatal(err)
	}
}

func runSDDStatusGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("Marshal(%T): %v", value, err)
	}
	write(t, path, string(payload)+"\n")
}

func shaID(char string) string {
	return fmt.Sprintf("sha256:%s", strings.Repeat(char, 64))
}

func compactStatusLenses(risk reviewtransaction.RiskLevel) []string {
	switch risk {
	case reviewtransaction.RiskMedium:
		return []string{reviewtransaction.LensReliability}
	case reviewtransaction.RiskHigh:
		return []string{reviewtransaction.LensRisk, reviewtransaction.LensResilience, reviewtransaction.LensReadability, reviewtransaction.LensReliability}
	default:
		return []string{}
	}
}

func freezeStatusFindings(tx *reviewtransaction.Transaction, findings []reviewtransaction.Finding) error {
	ledger, err := reviewtransaction.CanonicalLedger(findings)
	if err != nil {
		return err
	}
	return tx.FreezeFindings(findings, ledger, "")
}
