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
