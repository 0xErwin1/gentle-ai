package sddstatus

import (
	"path/filepath"
	"testing"
)

// canonicalRuntimeTempDir hands tests the resolved spelling of t.TempDir():
// the same shape OpenRuntimeStore produces, which runs filepath.Abs followed
// by filepath.EvalSymlinks over the --cwd it is given before recording it as
// store.Workspace.
//
// A fixture that keeps the raw spelling compares unequal to the very store it
// opened, because the two are the same directory under two different names:
//
//   - On Windows a runner's t.TempDir() is the 8.3 short form
//     (C:\Users\RUNNER~1\...), while EvalSymlinks answers with the long form
//     (C:\Users\runneradmin\...).
//   - On Darwin $TMPDIR lives under /var/folders and /var is a symlink into
//     /private, so the raw spelling always has a symlinked ancestor.
//
// Either way the comparison is against the fixture's own spelling rather than
// against production behaviour, so the test reports a defect that does not
// exist. Canonicalizing first is what makes the assertion mean what it says.
func canonicalRuntimeTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
