package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// TestOccupiedSlotIsNotARepositoryContextFailure is #2411's rc.6 occurrence.
//
// A reviewer result already occupying the slot with different bytes is a
// conflict about content, not a failure to reach the repository. The capture
// path already knows that: it builds a message naming both runnable exits,
// `review dispose-result` and `review preserve-result`. It then wraps that
// message in `repository_context_capture_failed`, whose action is "retry
// capture-result with the same exact binding or refresh status" — which is
// exactly what fails again, because the slot is still occupied.
//
// So the good advice sits inside the cause while the action contradicts it,
// and a consumer following the action loops. That is what @kar0loz reported on
// `2.4.0-rc.6` after their reviewer result had already been persisted.
func TestOccupiedSlotIsNotARepositoryContextFailure(t *testing.T) {
	binding, _, _ := startedOpaqueCaptureBinding(t, "slot-conflict-classification")

	first := admissibleOpaqueReviewerResult(t, binding, "first reviewer evidence")
	if err := RunReviewCaptureResult(append(append([]string{}, binding...), "--input", first), io.Discard); err != nil {
		t.Fatalf("first capture failed: %v", err)
	}
	second := admissibleOpaqueReviewerResult(t, binding, "second reviewer evidence")
	err := RunReviewCaptureResult(append(append([]string{}, binding...), "--input", second), io.Discard)
	if err == nil {
		t.Fatal("a second reviewer result with different bytes was accepted into an occupied slot")
	}
	message := err.Error()

	if strings.Contains(message, "repository_context_capture_failed") {
		t.Fatalf("an occupied slot is still classified as a repository-context failure: %s", message)
	}
	if strings.Contains(message, "retry capture-result with the same exact binding") {
		t.Fatalf("the action still tells the caller to retry the binding that will fail again: %s", message)
	}
	// The exits the code already knew about have to reach the surface rather
	// than stay buried inside a wrapped cause.
	for _, exit := range []string{"review dispose-result", "review preserve-result"} {
		if !strings.Contains(message, exit) {
			t.Fatalf("refusal does not name %q: %s", exit, message)
		}
	}
}

// TestGenuineRepositoryContextCaptureFailureKeepsItsCode is the guard on the
// other side: a capture that really could not reach its repository context
// keeps the code and the retry action, which for that cause is correct.
func TestGenuineRepositoryContextCaptureFailureKeepsItsCode(t *testing.T) {
	binding, _, repo := startedOpaqueCaptureBinding(t, "slot-conflict-control")
	result := admissibleOpaqueReviewerResult(t, binding, "reviewer evidence")
	blocked := filepath.Join(reviewCLICompactStoreDir(repo, "slot-conflict-control"), reviewtransaction.CompactReviewerResultsDir)
	if err := os.WriteFile(blocked, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := RunReviewCaptureResult(append(append([]string{}, binding...), "--input", result), io.Discard)
	if err == nil {
		t.Fatal("capture succeeded despite an unusable results directory")
	}
	if !strings.Contains(err.Error(), "repository_context_capture_failed") {
		t.Fatalf("a real capture failure lost its code: %s", err.Error())
	}
}
