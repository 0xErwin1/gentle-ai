package reviewtransaction

import (
	"strings"
	"testing"
)

// Issue #2498: rendering a repository path through fmt's %q escapes every
// backslash, so a Windows path prints with doubled separators in the exact
// invocation the user is told to copy-paste. These tests pin the correct
// behavior: the printed invocation contains the path as the filesystem knows
// it, quoted, with single separators, so strings.Contains(message, repo)
// holds on every platform.

func TestCompactAbandonCommandTextRendersWindowsPathVerbatim(t *testing.T) {
	repo := `C:\Users\dev\repo`
	text := compactAbandonCommandText(repo, "lineage-1", CompactAbandonEligibility{
		Eligible:         true,
		Revision:         "rev-1",
		SnapshotIdentity: "snap-1",
	})
	want := `--cwd "C:\Users\dev\repo"`
	if !strings.Contains(text, want) {
		t.Fatalf("abandon invocation does not contain the path as the filesystem knows it:\nwant substring: %s\ngot: %s", want, text)
	}
}

func TestCompactRepairCommandTextRendersWindowsPathVerbatim(t *testing.T) {
	repo := `C:\Users\dev\repo`
	text := compactRepairCommandText(repo, AuthorityDispositionPlan{
		AuthorityInventoryRevision: "inv-rev-1",
		PlanDigest:                 "digest-1",
	})
	want := `--cwd "C:\Users\dev\repo"`
	if got := strings.Count(text, want); got != 2 {
		t.Fatalf("repair invocation renders --cwd twice and both must carry the verbatim path:\nwant 2 occurrences of %s, got %d\ngot: %s", want, got, text)
	}
}
