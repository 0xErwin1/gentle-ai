package cli

import (
	"bytes"
	"testing"
)

// This file accumulates the threat-matrix "PR commands" RED tests Wave 7's
// consumer-first deletion slices each require (design.md Threat Matrix:
// "CLI verb dispatch loses cases... An unknown verb must return the
// existing `unknown review command %q`, never a panic or silent no-op").
// One test per retired verb, added in the same slice that retires it.

// TestReviewRetiredVerbReconcileAuthorityIsUnknownCommand is WU7's (S3a)
// threat-matrix proof: once RunReviewReconcileAuthority and its dispatch
// case are gone, "review reconcile-authority" must refuse with the exact
// unknown-command message, never a panic or a silent no-op.
func TestReviewRetiredVerbReconcileAuthorityIsUnknownCommand(t *testing.T) {
	var output bytes.Buffer
	err := RunReview([]string{"reconcile-authority", "--cwd", "."}, &output)
	if err == nil {
		t.Fatal("retired verb reconcile-authority was accepted, want unknown-command refusal")
	}
	if want := `unknown review command "reconcile-authority"`; err.Error() != want {
		t.Fatalf("retired verb reconcile-authority error = %q, want %q", err.Error(), want)
	}
	if output.Len() != 0 {
		t.Fatalf("retired verb reconcile-authority wrote output before refusing: %q", output.String())
	}
}
