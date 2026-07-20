package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestReviewQuarantineLegacyFixScopeRequiresExactBoundAuthorization(t *testing.T) {
	var output bytes.Buffer
	err := RunReview([]string{"quarantine-legacy-fix-scope"}, &output)
	if err == nil || !strings.Contains(err.Error(), "requires --lineage, --expected-revision, --diagnostic, --disposition, --reason, --actor, and --maintainer-authorization") {
		t.Fatalf("missing quarantine input error = %v", err)
	}
}

func TestReviewQuarantineLegacyFixScopeHelpDocumentsNarrowContract(t *testing.T) {
	var help bytes.Buffer
	if err := RunReview([]string{"quarantine-legacy-fix-scope", "--help"}, &help); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"complete-fix", "scope expansion", "exact eight-line LF-only binding", "idempotently"} {
		if !strings.Contains(help.String(), want) {
			t.Fatalf("quarantine help missing %q: %s", want, help.String())
		}
	}
}
