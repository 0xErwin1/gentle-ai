package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

// wantEnabledReviewGateFields is the exact shipped field set of a gate result
// produced while review-driven development is on. It guards the regression that
// matters most here: the delivery disposition must stay invisible on every path
// that already worked, so no consumer of the current projection changes.
var wantEnabledReviewGateFields = []string{"action", "allowed", "context", "reason", "result", "schema"}

// TestReviewValidateReportsDisabledUnmanagedDeliveryWithoutReceipt closes the
// contract breach: the guidance installed on every agent promises that delivery
// under a disabled switch reports `disabled/unmanaged`, and until now nothing
// emitted that token on the wire.
func TestReviewValidateReportsDisabledUnmanagedDeliveryWithoutReceipt(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("candidate authored while disabled\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	disableReviewForClone(t, repo)

	var output bytes.Buffer
	err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}, &output)
	// The gate reports; it does not veto. Ordinary repository policy governs
	// delivery once the user has switched review-driven development off.
	if err != nil {
		t.Fatalf("disabled delivery gate vetoed delivery instead of reporting it: %v\n%s", err, output.String())
	}
	var result ReviewValidateResult
	decodeStrictReviewJSON(t, output.Bytes(), &result)
	if result.Schema != ReviewValidateSchema {
		t.Fatalf("disabled delivery left the typed gate schema = %q", result.Schema)
	}
	if result.Delivery != reviewtransaction.RDDDeliveryDisabledUnmanaged {
		t.Fatalf("disabled receiptless delivery = %q, want %q", result.Delivery, reviewtransaction.RDDDeliveryDisabledUnmanaged)
	}
	// Unmanaged by choice is neither an approval nor a fault.
	if result.Allowed || result.Result == reviewtransaction.GateAllow {
		t.Fatalf("disabled delivery fabricated an approval: %#v", result)
	}
	var denied ReviewGateDeniedError
	if errors.As(err, &denied) {
		t.Fatalf("disabled delivery was reported as a denial: %#v", denied)
	}
	// The reason the candidate is unmanaged stays discoverable.
	if result.Context.Denial == nil || result.Context.Denial.Stage != "receipt-discovery" {
		t.Fatalf("disabled delivery hid why no receipt governs: %#v", result.Context.Denial)
	}

	// The report is an observation, so replaying the same request must return
	// the same bytes and must not create review authority.
	var replay bytes.Buffer
	if err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}, &replay); err != nil {
		t.Fatalf("replayed disabled delivery gate: %v\n%s", err, replay.String())
	}
	if !bytes.Equal(replay.Bytes(), output.Bytes()) {
		t.Fatalf("disabled delivery report is not replay stable:\nfirst:\n%s\nreplay:\n%s", output.String(), replay.String())
	}
	// The clone-local kill-switch override shares the review-transactions root,
	// so the assertion targets the authority generation directory itself.
	if _, err := os.Stat(filepath.Join(repo, ".git", "gentle-ai", "review-transactions", "v2")); !os.IsNotExist(err) {
		t.Fatalf("a disabled delivery report created review authority: %v", err)
	}
}

// TestReviewValidateKeepsGoverningReceiptAuthoritativeWhileDisabled proves the
// asymmetry the disposition already encodes: disabling freezes authority
// read-only, it never unmakes an approval that was content-bound to exactly
// these bytes.
func TestReviewValidateKeepsGoverningReceiptAuthoritativeWhileDisabled(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("reviewed candidate behavior\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	started := startFacadeReview(t, repo)
	evidencePath := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(evidencePath, []byte("focused tests pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	finalizeArgs := append([]string{"--cwd", repo, "--lineage", started.LineageID}, facadeReviewerResultArgs(t, started)...)
	if err := RunReviewFacadeFinalize(append(finalizeArgs, "--evidence", evidencePath), io.Discard); err != nil {
		t.Fatal(err)
	}
	runReviewCLIGit(t, repo, "add", "tracked.txt")

	var enabled bytes.Buffer
	if err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}, &enabled); err != nil {
		t.Fatalf("receipt-governed gate before disabling: %v\n%s", err, enabled.String())
	}
	assertReviewGateResult(t, enabled.Bytes(), reviewtransaction.GateAllow)

	disableReviewForClone(t, repo)

	var disabled bytes.Buffer
	if err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}, &disabled); err != nil {
		t.Fatalf("disabling revoked a receipt that governs these exact bytes: %v\n%s", err, disabled.String())
	}
	assertReviewGateResult(t, disabled.Bytes(), reviewtransaction.GateAllow)
	var result ReviewValidateResult
	decodeStrictReviewJSON(t, disabled.Bytes(), &result)
	if result.Delivery == reviewtransaction.RDDDeliveryDisabledUnmanaged {
		t.Fatalf("a governing receipt was reported as unmanaged: %#v", result)
	}
	if !bytes.Equal(disabled.Bytes(), enabled.Bytes()) {
		t.Fatalf("the receipt-governed projection changed after disabling:\nenabled:\n%s\ndisabled:\n%s", enabled.String(), disabled.String())
	}
}

// TestReviewValidateWithoutReceiptStillDeniesWhileReviewIsEnabled is the
// regression guard: with the switch on, a receiptless candidate keeps today's
// denial, today's exit status, and today's exact field set.
func TestReviewValidateWithoutReceiptStillDeniesWhileReviewIsEnabled(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("unreviewed candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}, &output)
	var denied ReviewGateDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("enabled receiptless delivery error = %T %v", err, err)
	}
	if fields := strictReviewJSONFields(t, output.Bytes()); !reflect.DeepEqual(fields, wantEnabledReviewGateFields) {
		t.Fatalf("enabled gate fields = %v, want %v", fields, wantEnabledReviewGateFields)
	}
	var result ReviewValidateResult
	decodeStrictReviewJSON(t, output.Bytes(), &result)
	if result.Delivery != "" {
		t.Fatalf("an enabled switch reported a delivery disposition: %#v", result)
	}
	if result.Allowed || result.Context.Denial == nil || result.Context.Denial.Stage != "receipt-discovery" ||
		result.Context.Denial.Code != string(ReviewReceiptMissing) {
		t.Fatalf("enabled receiptless denial = %#v", result)
	}
}

func disableReviewForClone(t *testing.T, repo string) {
	t.Helper()
	var output bytes.Buffer
	if err := RunReviewMode([]string{"disable", "--cwd", repo, "--scope", "clone", "--json"}, &output); err != nil {
		t.Fatalf("disable review-driven development: %v\n%s", err, output.String())
	}
	if status := decodeReviewModeResult(t, output.Bytes()).Status; status.Effective != reviewtransaction.RDDModeOff {
		t.Fatalf("kill switch did not take effect: %#v", status)
	}
}
