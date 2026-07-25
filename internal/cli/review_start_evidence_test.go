package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

// TestReviewFacadeStartHighRiskCarriesConsentEvidencePhrases proves the
// non-interactive START result relays the same human evidence phrases the
// interactive consent prompt speaks, derived from the one shared helper, so a
// headless agent can explain WHY the deeper review was selected.
func TestReviewFacadeStartHighRiskCarriesConsentEvidencePhrases(t *testing.T) {
	repo := initReviewCLIRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "service-token.ts"), []byte("export const token = 'candidate'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RunReviewFacadeStart([]string{"--cwd", repo, "--lineage", "evidence-high"}, &output); err != nil {
		t.Fatal(err)
	}
	var started ReviewFacadeStartResult
	if err := json.Unmarshal(output.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.RiskLevel != reviewtransaction.RiskHigh {
		t.Fatalf("service-token start risk = %q, want high", started.RiskLevel)
	}
	want := []string{"service credentials in service-token.ts"}
	if !reflect.DeepEqual(started.RiskEvidence, want) {
		t.Fatalf("start risk_evidence = %#v, want %#v", started.RiskEvidence, want)
	}
	// Prompt parity: the phrases must be exactly what the interactive consent
	// prompt would say for the same assessed candidate, from the same helper.
	builder := reviewtransaction.SnapshotBuilder{Repo: repo}
	intended, err := builder.DiscoverIntendedUntracked(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := builder.Build(context.Background(), reviewtransaction.Target{
		Kind: reviewtransaction.TargetCurrentChanges, Projection: reviewtransaction.ProjectionWorkspace, IntendedUntracked: intended,
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := builder.AssessSnapshotRisk(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(started.RiskEvidence, reviewConsentEvidencePhrases(assessment.Reasons)) {
		t.Fatalf("start risk_evidence %#v does not derive from the shared consent helper %#v",
			started.RiskEvidence, reviewConsentEvidencePhrases(assessment.Reasons))
	}
	prompt := reviewConsentPrompt(assessment)
	for _, phrase := range started.RiskEvidence {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("interactive consent prompt %q does not speak phrase %q", prompt, phrase)
		}
	}
}

// TestReviewFacadeStartDocsOnlyOmitsRiskEvidence proves a tier-0 start carries
// no risk_evidence key at all: absence, not empty-string noise.
func TestReviewFacadeStartDocsOnlyOmitsRiskEvidence(t *testing.T) {
	repo := initReviewCLIRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "guide.md"), []byte("passive documentation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RunReviewFacadeStart([]string{"--cwd", repo, "--lineage", "evidence-docs"}, &output); err != nil {
		t.Fatal(err)
	}
	var started ReviewFacadeStartResult
	if err := json.Unmarshal(output.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.RiskLevel != reviewtransaction.RiskLow {
		t.Fatalf("docs-only start risk = %q, want low", started.RiskLevel)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["risk_evidence"]; ok {
		t.Fatalf("tier-0 start leaked risk_evidence: %s", output.String())
	}
}

// TestReviewFacadeStartResultOmitsAdditiveFieldsWhenAbsent guards the omitempty
// contract: consumers of the previous shape see byte-identical output when no
// evidence and no hint apply.
func TestReviewFacadeStartResultOmitsAdditiveFieldsWhenAbsent(t *testing.T) {
	payload, err := json.Marshal(ReviewFacadeStartResult{
		Operation: "review/start", Action: "created", LineageID: "shape-guard",
		SelectedLenses: []string{}, LensBindings: []ReviewFacadeLensBinding{},
		Projection: reviewtransaction.ProjectionWorkspace, ChangedFiles: 1, ChangedLines: 1, CorrectionBudget: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"risk_evidence", "hint"} {
		if bytes.Contains(payload, []byte(field)) {
			t.Fatalf("absent additive field %q still serialized: %s", field, payload)
		}
	}
}

// TestReviewFacadeStartEmptyCandidateHintsBaseRef proves the committed-work
// recovery path is discoverable: a clean worktree yields an empty candidate,
// and the result itself names --base-ref as the way to review committed work.
func TestReviewFacadeStartEmptyCandidateHintsBaseRef(t *testing.T) {
	repo := initReviewCLIRepo(t)
	var output bytes.Buffer
	if err := RunReviewFacadeStart([]string{"--cwd", repo, "--lineage", "evidence-empty"}, &output); err != nil {
		t.Fatal(err)
	}
	var started ReviewFacadeStartResult
	if err := json.Unmarshal(output.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.ChangedFiles != 0 {
		t.Fatalf("clean-worktree start changed_files = %d, want 0", started.ChangedFiles)
	}
	if started.Hint != reviewStartEmptyCandidateHint || !strings.Contains(started.Hint, "--base-ref") {
		t.Fatalf("empty-candidate start hint = %q, want %q", started.Hint, reviewStartEmptyCandidateHint)
	}
}

// TestReviewFacadeStartNonEmptyCandidateHasNoHint proves the hint is scoped to
// the empty-candidate case and never becomes ambient noise.
func TestReviewFacadeStartNonEmptyCandidateHasNoHint(t *testing.T) {
	repo := initReviewCLIRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RunReviewFacadeStart([]string{"--cwd", repo, "--lineage", "evidence-nonempty"}, &output); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["hint"]; ok {
		t.Fatalf("non-empty start leaked hint: %s", output.String())
	}
}
