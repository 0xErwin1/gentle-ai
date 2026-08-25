package sddstatus

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCurrentBehaviorCoverageFilesAreUntagged(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", ".."))

	for _, relativePath := range []string{
		"internal/sddstatus/review_binding_alias_identity_test.go",
		"internal/sddstatus/status_remediation_instructions_test.go",
		"internal/sddstatus/status_test.go",
		"internal/reviewtransaction/reviewer_context_level_test.go",
		"internal/reviewtransaction/invalidated_status_test.go",
		"internal/reviewtransaction/invalidation_test.go",
	} {
		contents, err := os.ReadFile(filepath.Join(repoRoot, relativePath))
		if err != nil {
			t.Fatalf("read current-behavior file %q: %v", relativePath, err)
		}
		if strings.Contains(string(contents), "legacy_compact_receipt") {
			t.Fatalf("current-behavior file %q retains legacy_compact_receipt", relativePath)
		}
	}
}
