package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// finalizeFacadeReviewForRepo runs one complete reviewed flow over the live
// candidate: start with the given extra arguments, submit one clean result per
// selected lens, and finalize to a terminal receipt.
func finalizeFacadeReviewForRepo(t *testing.T, repo string, startExtra ...string) {
	t.Helper()
	var output bytes.Buffer
	if err := RunReviewFacadeStart(append([]string{"--cwd", repo}, startExtra...), &output); err != nil {
		t.Fatalf("start facade review: %v\n%s", err, output.String())
	}
	var started ReviewFacadeStartResult
	if err := json.Unmarshal(output.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if len(started.SelectedLenses) == 0 {
		if err := RunReviewFacadeFinalize([]string{"--cwd", repo, "--lineage", started.LineageID}, io.Discard); err != nil {
			t.Fatal(err)
		}
		return
	}
	evidencePath := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(evidencePath, []byte("focused tests pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	finalizeArgs := append([]string{"--cwd", repo, "--lineage", started.LineageID}, facadeReviewerResultArgs(t, repo, started)...)
	if err := RunReviewFacadeFinalize(append(finalizeArgs, "--evidence", evidencePath), io.Discard); err != nil {
		t.Fatal(err)
	}
}

func disableReviewForClone(t *testing.T, repo string) {
	t.Helper()
	var output bytes.Buffer
	if err := RunReviewMode([]string{"disable", "--cwd", repo, "--scope", "clone", "--json"}, &output); err != nil {
		t.Fatalf("disable receipt-driven development: %v\n%s", err, output.String())
	}
	if status := decodeReviewModeResult(t, output.Bytes()).Status; status.Effective != reviewtransaction.RDDModeOff {
		t.Fatalf("kill switch did not take effect: %#v", status)
	}
}
