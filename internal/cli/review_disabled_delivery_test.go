package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// TestReviewValidateReportsDisabledUnmanagedDeliveryWithPriorReceipt closes the
// community-reported gap (Andiveli, PR #1801): a repository that already holds
// receipts from earlier reviewed flows must still report `disabled/unmanaged`
// for work authored after the switch went off. A stale receipt that does not
// govern the current candidate is the expected state of a disabled clone — no
// new receipt could have been created while disabled — so it must not turn
// "unmanaged by choice" into a mismatch denial.
func TestReviewValidateReportsDisabledUnmanagedDeliveryWithPriorReceipt(t *testing.T) {
	shapes := []struct {
		name string
		// review earns the terminal receipt for the earlier candidate and
		// leaves the repository exactly as the reviewed flow delivered it.
		review func(t *testing.T, repo string)
		gate   reviewtransaction.GateKind
		// wantDenialCode is the exact receipt-discovery outcome today's gate
		// turns into a fail-closed denial and the fix must keep discoverable.
		wantDenialCode string
	}{
		{
			// Andiveli's shape: a committed candidate reviewed against its
			// base, delivered, then a new commit on top while disabled. The
			// stale receipt binds a different candidate tree, so pre-push
			// discovery classifies it receipt_scope_changed and denies with
			// candidate-or-paths-mismatch.
			name: "scope-changed receipt at pre-push",
			review: func(t *testing.T, repo string) {
				base := strings.TrimSpace(runReviewCLIGit(t, repo, "rev-parse", "HEAD"))
				if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("reviewed candidate behavior\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				runReviewCLIGit(t, repo, "add", "tracked.txt")
				runReviewCLIGit(t, repo, "commit", "-qm", "reviewed candidate")
				finalizeFacadeReviewForRepo(t, repo, "--base-ref", base, "--committed-only")
			},
			gate:           reviewtransaction.GatePrePush,
			wantDenialCode: "candidate-or-paths-mismatch",
		},
		{
			// The sibling shape: a workspace review delivered exactly as
			// reviewed, then a new commit while disabled. Discovery classifies
			// the stale receipt receipt_unrelated at pre-commit.
			name: "unrelated receipt at pre-commit",
			review: func(t *testing.T, repo string) {
				if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("reviewed candidate behavior\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				finalizeFacadeReviewForRepo(t, repo)
				runReviewCLIGit(t, repo, "add", "tracked.txt")
				runReviewCLIGit(t, repo, "commit", "-qm", "reviewed candidate")
			},
			gate:           reviewtransaction.GatePreCommit,
			wantDenialCode: string(ReviewReceiptUnrelated),
		},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			reviewModeHome(t)
			repo := initReviewCLIRepo(t)
			branch := strings.TrimSpace(runReviewCLIGit(t, repo, "symbolic-ref", "--short", "HEAD"))
			shape.review(t, repo)
			configureCLIReviewPublicationRemote(t, repo, branch)

			disableReviewForClone(t, repo)

			// New work authored and committed while disabled: no receipt can
			// exist for it, so the stale receipt must not turn "unmanaged by
			// choice" into a fault.
			if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("work authored while disabled\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runReviewCLIGit(t, repo, "add", "tracked.txt")
			runReviewCLIGit(t, repo, "commit", "-qm", "authored while disabled")

			var output bytes.Buffer
			err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(shape.gate)}, &output)
			// The gate reports; it does not veto: ordinary repository policy
			// governs delivery once review-driven development is off, and the
			// prior receipt governs only the bytes it approved.
			if err != nil {
				t.Fatalf("disabled delivery with a prior receipt was denied instead of reported: %v\n%s", err, output.String())
			}
			var result ReviewValidateResult
			decodeStrictReviewJSON(t, output.Bytes(), &result)
			if result.Schema != ReviewValidateSchema {
				t.Fatalf("disabled delivery left the typed gate schema = %q", result.Schema)
			}
			if result.Delivery != reviewtransaction.RDDDeliveryDisabledUnmanaged {
				t.Fatalf("disabled delivery with a prior receipt = %q, want %q", result.Delivery, reviewtransaction.RDDDeliveryDisabledUnmanaged)
			}
			// Unmanaged by choice is neither an approval nor a fault.
			if result.Allowed || result.Result == reviewtransaction.GateAllow {
				t.Fatalf("disabled delivery fabricated an approval: %#v", result)
			}
			var denied ReviewGateDeniedError
			if errors.As(err, &denied) {
				t.Fatalf("disabled delivery was reported as a denial: %#v", denied)
			}
			// The reason the prior receipt does not govern stays discoverable.
			if result.Context.Denial == nil || result.Context.Denial.Code != shape.wantDenialCode {
				t.Fatalf("disabled delivery hid why no receipt governs: %#v, want code %q", result.Context.Denial, shape.wantDenialCode)
			}

			// The report is an observation: replaying returns the same bytes.
			var replay bytes.Buffer
			if err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(shape.gate)}, &replay); err != nil {
				t.Fatalf("replayed disabled delivery gate: %v\n%s", err, replay.String())
			}
			if !bytes.Equal(replay.Bytes(), output.Bytes()) {
				t.Fatalf("disabled delivery report is not replay stable:\nfirst:\n%s\nreplay:\n%s", output.String(), replay.String())
			}
		})
	}
}

// TestReviewValidateKeepsFailingClosedOnCorruptedAuthorityWhileDisabled holds
// the line the disposition must never cross: corrupted review authority is
// genuine damage, not "unmanaged by choice", so it keeps failing closed with
// the kill switch off exactly as it does with the switch on.
func TestReviewValidateKeepsFailingClosedOnCorruptedAuthorityWhileDisabled(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("reviewed candidate behavior\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	finalizeFacadeReviewForRepo(t, repo)
	runReviewCLIGit(t, repo, "add", "tracked.txt")
	runReviewCLIGit(t, repo, "commit", "-qm", "reviewed candidate")

	disableReviewForClone(t, repo)

	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("work authored while disabled\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runReviewCLIGit(t, repo, "add", "tracked.txt")
	runReviewCLIGit(t, repo, "commit", "-qm", "authored while disabled")

	// Damage the authority inventory: a truncated compact record is corruption,
	// not a stale-but-healthy receipt.
	broken := filepath.Join(repo, ".git", "gentle-ai", "review-transactions", "v2", "corrupt-while-disabled")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "review-state.json"), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}, &output)
	var denied ReviewGateDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("corrupted authority while disabled did not fail closed: %T %v\n%s", err, err, output.String())
	}
	var result ReviewValidateResult
	decodeStrictReviewJSON(t, output.Bytes(), &result)
	if result.Delivery == reviewtransaction.RDDDeliveryDisabledUnmanaged {
		t.Fatalf("corrupted authority was reported as unmanaged by choice: %#v", result)
	}
	if result.Allowed || result.Context.Denial == nil || result.Context.Denial.Code != string(ReviewAuthorityCorrupted) {
		t.Fatalf("corrupted-authority denial while disabled = %#v", result)
	}
}

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
	finalizeArgs := append([]string{"--cwd", repo, "--lineage", started.LineageID}, facadeReviewerResultArgs(t, started)...)
	if err := RunReviewFacadeFinalize(append(finalizeArgs, "--evidence", evidencePath), io.Discard); err != nil {
		t.Fatal(err)
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
