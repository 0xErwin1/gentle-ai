package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

func TestReviewStatusLineageInventoryContinuationAndSelectorValidation(t *testing.T) {
	repo := initReviewCLIRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var startOutput bytes.Buffer
	if err := runLegacyFacadeStartForTest(t, []string{"--cwd", repo}, &startOutput); err != nil {
		t.Fatal(err)
	}
	var started ReviewFacadeStartResult
	if err := json.Unmarshal(startOutput.Bytes(), &started); err != nil {
		t.Fatal(err)
	}

	// 1. Uncontracted status with existing valid lineage exits 0 and returns scoped inventory
	var out bytes.Buffer
	err := RunReviewStatus([]string{"--cwd", repo, "--lineage", started.LineageID}, &out)
	if err != nil {
		t.Fatalf("RunReviewStatus with valid lineage failed: %v", err)
	}
	var report reviewtransaction.AuthorityStatusReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("failed to decode status report: %v, raw: %s", err, out.String())
	}
	if len(report.Entries) != 1 || report.Entries[0].LineageID != started.LineageID {
		t.Fatalf("unexpected inventory entries: %#v", report.Entries)
	}
	if report.Status != reviewtransaction.AuthorityStatusActive {
		t.Fatalf("unexpected report status: %v", report.Status)
	}

	// 2. Uncontracted status with nonexistent lineage exits with error naming the lineage
	out.Reset()
	err = RunReviewStatus([]string{"--cwd", repo, "--lineage", "review-0000000000000000"}, &out)
	if err == nil || !strings.Contains(err.Error(), "review-0000000000000000") {
		t.Fatalf("expected error naming nonexistent lineage, got: %v", err)
	}

	// 3. Uncontracted status with other selectors (e.g. --base-ref) requires contract
	out.Reset()
	err = RunReviewStatus([]string{"--cwd", repo, "--base-ref", "HEAD"}, &out)
	if err == nil || !strings.Contains(err.Error(), reviewStatusTargetSelectorsRequireContractReason) {
		t.Fatalf("expected contract required error, got: %v", err)
	}
	if !strings.Contains(reviewStatusTargetSelectorsRequireContractReason, ReviewIntegrationContractV1) ||
		!strings.Contains(reviewStatusTargetSelectorsRequireContractReason, ReviewIntegrationContractV2) {
		t.Fatalf("selector contract reason does not name both v1 and v2: %q", reviewStatusTargetSelectorsRequireContractReason)
	}
}
