package sddstatus

import (
	"strings"
	"testing"
)

// Issue #2498: rendering the workspace root through fmt's %q escapes every
// backslash, so a Windows path prints with doubled separators in the exact
// `gentle-ai sdd-attempt` invocation the refusal tells the operator to run.
// This test pins the correct behavior: the printed invocation contains the
// path as the filesystem knows it, quoted, with single separators.

func TestArchiveReVerifyContinuationRendersWindowsPathVerbatim(t *testing.T) {
	workspace := `C:\Users\dev\repo`
	got := archiveReVerifyContinuation(workspace, "my-change", RuntimeStatus{Revision: "abc123"})
	want := `--cwd "C:\Users\dev\repo"`
	if occurrences := strings.Count(got, want); occurrences != 2 {
		t.Fatalf("begin+finish continuation renders --cwd twice and both must carry the verbatim path:\nwant 2 occurrences of %s, got %d\ngot: %s", want, occurrences, got)
	}
}
