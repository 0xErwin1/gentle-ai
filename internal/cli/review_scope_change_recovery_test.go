package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// TestPrePushScopeChangeNamedRecoveryReachesAllow is the guard the existing
// suite was missing. Every earlier test asserted only that a scope-changed
// denial EMITTED a recovery continuation; none of them ever executed the
// recovery it named. Both pre-push scope-changed classes shipped a message
// that dead-ended: the receipt-binding class named a bare `review.recover`
// that freezes an empty current-changes successor (base_tree ==
// candidate_tree) and re-trips the same one-commit delivery rule, and the
// delivery-shape class named nothing at all.
//
// This test drives the real facade entry points the CLI dispatches to
// (RunReviewFacadeValidate, RunReviewRecover, RunReviewFacadeFinalize) over a
// real Git repository with a real bare publication remote. It reads the
// recovery out of the frozen diagnostics the denial itself carries -- never a
// recovery the test knows independently -- runs exactly that recovery, and
// requires the same gate to then return allow. A future scope-changed class
// whose named recovery dead-ends fails here.
func TestPrePushScopeChangeNamedRecoveryReachesAllow(t *testing.T) {
	tests := []struct {
		name       string
		wantDenial reviewtransaction.GateDenial
		stage      func(t *testing.T, repo string)
	}{
		{
			// Two commits past the reviewed base AND different bytes:
			// buildPushTarget ERRORS with ErrReviewedDeliveryNotOneCommit
			// before any actual snapshot exists, so discovery buckets this as
			// deliveryShape, where the receipt-binding derivation is not even
			// callable.
			name:       "delivery shape mismatch",
			wantDenial: reviewtransaction.GateDenial{Stage: "delivery-derivation", Code: "delivery-shape-mismatch"},
			stage: func(t *testing.T, repo string) {
				writeScopeRecoveryFile(t, repo, "alpha.txt", "alpha\n")
				writeScopeRecoveryFile(t, repo, "beta.txt", "beta\n")
				runReviewCLIGit(t, repo, "add", "-N", "alpha.txt", "beta.txt")
				finalizeFacadeReviewForRepo(t, repo)
				writeScopeRecoveryFile(t, repo, "alpha.txt", "alpha changed after approval\n")
				runReviewCLIGit(t, repo, "add", "alpha.txt")
				runReviewCLIGit(t, repo, "commit", "-qm", "feat: alpha")
				runReviewCLIGit(t, repo, "add", "beta.txt")
				runReviewCLIGit(t, repo, "commit", "-qm", "feat: beta")
			},
		},
		{
			// The second, previously untested route into the same
			// context-less bucket: a commit inside the publication range
			// reproduces the reviewed base tree exactly, so
			// reviewedDeliveryBase finds two candidate base commits and
			// raises GateDeliveryBaseResolutionError. Like the one-commit
			// rule it fails before any actual snapshot exists.
			name:       "delivery base ambiguous",
			wantDenial: reviewtransaction.GateDenial{Stage: "delivery-derivation", Code: "delivery-base-ambiguous"},
			stage: func(t *testing.T, repo string) {
				writeScopeRecoveryFile(t, repo, "alpha.txt", "alpha\n")
				runReviewCLIGit(t, repo, "add", "-N", "alpha.txt")
				finalizeFacadeReviewForRepo(t, repo)
				runReviewCLIGit(t, repo, "add", "alpha.txt")
				runReviewCLIGit(t, repo, "commit", "-qm", "feat: alpha")
				// Restore the reviewed base tree inside the range: this
				// commit and the publication base now both carry it.
				runReviewCLIGit(t, repo, "rm", "-q", "alpha.txt")
				runReviewCLIGit(t, repo, "commit", "-qm", "revert: alpha")
				writeScopeRecoveryFile(t, repo, "alpha.txt", "alpha reworked after approval\n")
				runReviewCLIGit(t, repo, "add", "alpha.txt")
				runReviewCLIGit(t, repo, "commit", "-qm", "feat: alpha again")
			},
		},
		{
			// Exactly one commit past the reviewed base, but carrying
			// different bytes: the assessment COMPLETES and reports a
			// candidate mismatch, so discovery buckets this as scopeChanged
			// with full diagnostics.
			name:       "receipt binding mismatch",
			wantDenial: reviewtransaction.GateDenial{Stage: "receipt-binding", Code: "candidate-or-paths-mismatch"},
			stage: func(t *testing.T, repo string) {
				writeScopeRecoveryFile(t, repo, "alpha.txt", "alpha\n")
				runReviewCLIGit(t, repo, "add", "-N", "alpha.txt")
				finalizeFacadeReviewForRepo(t, repo)
				writeScopeRecoveryFile(t, repo, "alpha.txt", "alpha changed after approval\n")
				runReviewCLIGit(t, repo, "add", "alpha.txt")
				runReviewCLIGit(t, repo, "commit", "-qm", "feat: alpha")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reviewModeHome(t)
			repo := initReviewCLIRepo(t)
			branch := strings.TrimSpace(runReviewCLIGit(t, repo, "symbolic-ref", "--short", "HEAD"))
			configureCLIReviewPublicationRemote(t, repo, branch)
			test.stage(t, repo)

			denial, scope := prePushScopeChangeDenial(t, repo)
			if denial.Context.Denial == nil || *denial.Context.Denial != test.wantDenial {
				t.Fatalf("pre-push denial = %#v, want %#v", denial.Context.Denial, test.wantDenial)
			}
			if scope == nil {
				t.Fatalf("scope-changed denial carries no recovery diagnostics: %s", denial.Error())
			}
			if scope.RecoveryOperation != "review.recover" {
				t.Fatalf("recovery operation = %q, want review.recover", scope.RecoveryOperation)
			}

			// Execute EXACTLY the recovery the frozen diagnostics name.
			runNamedScopeChangeRecovery(t, repo, *scope, "recovered-successor")

			var validated bytes.Buffer
			if err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", "pre-push"}, &validated); err != nil {
				t.Fatalf("gate still denied after running the recovery the message named: %v\n%s", err, validated.String())
			}
			assertReviewGateResult(t, validated.Bytes(), reviewtransaction.GateAllow)
		})
	}
}

// TestPreCommitScopeChangeKeepsBareCurrentChangesRecovery pins the
// gate-conditional half of the contract. A bare `review.recover` genuinely
// works at pre-commit with a dirty workspace, so pre-commit must keep naming
// it and must never acquire the committed base-diff flags it does not need.
func TestPreCommitScopeChangeKeepsBareCurrentChangesRecovery(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	branch := strings.TrimSpace(runReviewCLIGit(t, repo, "symbolic-ref", "--short", "HEAD"))
	configureCLIReviewPublicationRemote(t, repo, branch)

	writeScopeRecoveryFile(t, repo, "alpha.txt", "alpha\n")
	runReviewCLIGit(t, repo, "add", "-N", "alpha.txt")
	finalizeFacadeReviewForRepo(t, repo)
	// Dirty the workspace after approval so the receipt stops governing it.
	writeScopeRecoveryFile(t, repo, "alpha.txt", "alpha changed after approval\n")
	runReviewCLIGit(t, repo, "add", "alpha.txt")

	var denied bytes.Buffer
	err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", "pre-commit"}, &denied)
	if err == nil {
		t.Fatalf("pre-commit allowed a candidate no receipt governs: %s", denied.String())
	}
	var gateErr ReviewGateDeniedError
	if !errors.As(err, &gateErr) || gateErr.Result != reviewtransaction.GateScopeChanged {
		t.Fatalf("pre-commit denial = %T %v, want a scope-changed gate denial", err, err)
	}
	scope := gateErr.Context.ScopeChange
	if scope == nil {
		t.Fatalf("pre-commit scope-changed denial carries no diagnostics: %s", denied.String())
	}
	if scope.RecoveryBaseRef != "" || scope.RecoveryScope != "" {
		t.Fatalf("pre-commit named committed base-diff recovery inputs it does not need: base_ref=%q scope=%q",
			scope.RecoveryBaseRef, scope.RecoveryScope)
	}
	// The bare recovery pre-commit names must still reach allow.
	runNamedScopeChangeRecovery(t, repo, *scope, "precommit-successor")
	var validated bytes.Buffer
	if err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", "pre-commit"}, &validated); err != nil {
		t.Fatalf("pre-commit still denied after the bare recovery it named: %v\n%s", err, validated.String())
	}
	assertReviewGateResult(t, validated.Bytes(), reviewtransaction.GateAllow)
}

// TestPrePushIdenticalContentDeliveryKeepsHonestFallback pins the one pre-push
// sub-case where no single `review recover` invocation resolves the block, so
// naming one would be a lie.
//
// The delivery carries byte-identical content to the approved candidate and
// differs only in commit topology (two commits instead of one). The committed
// base-diff successor a recovery would freeze is therefore the SAME scope the
// predecessor already approved, and the recovery authority refuses it with
// "approved predecessor scope has not changed" (validateCompactRecoveryEdge ->
// compactRecoveryScopeChanged). The denial must keep the terminal
// "requires explicit maintainer action" wording rather than send the operator
// at a command that exits non-zero.
func TestPrePushIdenticalContentDeliveryKeepsHonestFallback(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	branch := strings.TrimSpace(runReviewCLIGit(t, repo, "symbolic-ref", "--short", "HEAD"))
	configureCLIReviewPublicationRemote(t, repo, branch)

	writeScopeRecoveryFile(t, repo, "alpha.txt", "alpha\n")
	writeScopeRecoveryFile(t, repo, "beta.txt", "beta\n")
	runReviewCLIGit(t, repo, "add", "-N", "alpha.txt", "beta.txt")
	finalizeFacadeReviewForRepo(t, repo)
	// Deliver exactly the approved bytes, split across two commits.
	runReviewCLIGit(t, repo, "add", "alpha.txt")
	runReviewCLIGit(t, repo, "commit", "-qm", "feat: alpha")
	runReviewCLIGit(t, repo, "add", "beta.txt")
	runReviewCLIGit(t, repo, "commit", "-qm", "feat: beta")

	denial, scope := prePushScopeChangeDenial(t, repo)
	if scope != nil {
		t.Fatalf("named a recovery for a delivery whose scope is unchanged: %#v", scope)
	}
	const want = "review lifecycle gate denied: scope-changed: requires explicit maintainer action"
	if denial.Error() != want {
		t.Fatalf("denial message = %q, want %q", denial.Error(), want)
	}
}

// prePushScopeChangeDenial runs the pre-push gate, requires a scope-changed
// denial, and returns it with the frozen recovery diagnostics it carries.
func prePushScopeChangeDenial(t *testing.T, repo string) (ReviewGateDeniedError, *reviewtransaction.GateScopeChangeDiagnostics) {
	t.Helper()
	var denied bytes.Buffer
	err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", "pre-push"}, &denied)
	if err == nil {
		t.Fatalf("pre-push allowed a candidate no receipt governs: %s", denied.String())
	}
	var gateErr ReviewGateDeniedError
	if !errors.As(err, &gateErr) {
		t.Fatalf("pre-push denial = %T %v, want ReviewGateDeniedError", err, err)
	}
	if gateErr.Result != reviewtransaction.GateScopeChanged {
		t.Fatalf("pre-push gate result = %q, want scope-changed\n%s", gateErr.Result, denied.String())
	}
	return gateErr, gateErr.Context.ScopeChange
}

// runNamedScopeChangeRecovery executes the recovery the diagnostics name and
// drives the successor to a terminal approved receipt. Every argument is read
// out of the diagnostics, so the test can only pass when the recommendation
// itself is the one that works.
func runNamedScopeChangeRecovery(t *testing.T, repo string, scope reviewtransaction.GateScopeChangeDiagnostics, successor string) {
	t.Helper()
	args := []string{
		"--cwd", repo,
		"--predecessor-lineage", scope.PredecessorLineageID,
		"--expected-predecessor-revision", scope.PredecessorRevision,
		"--successor-lineage", successor,
		"--disposition", "scope_changed",
	}
	if scope.RecoveryBaseRef != "" {
		args = append(args, "--base-ref", scope.RecoveryBaseRef)
	}
	if scope.RecoveryScope == reviewtransaction.RecoveryScopeCommittedBaseDiff {
		args = append(args, "--committed-only")
	}
	var recovered bytes.Buffer
	if err := RunReviewRecover(args, &recovered); err != nil {
		t.Fatalf("named recovery %v failed: %v\n%s", args, err, recovered.String())
	}
	// The successor is a fresh review: drive whatever proportional plan its
	// own frozen state selected, exactly as a real operator would.
	store, err := reviewtransaction.CompactAuthoritativeStore(t.Context(), repo, successor)
	if err != nil {
		t.Fatalf("open recovered successor: %v", err)
	}
	record, err := store.Load()
	if err != nil {
		t.Fatalf("load recovered successor: %v", err)
	}
	started := ReviewFacadeStartResult{SelectedLenses: record.State.SelectedLenses}
	finalizeArgs := append([]string{"--cwd", repo, "--lineage", successor}, facadeReviewerResultArgs(t, started)...)
	if len(started.SelectedLenses) > 0 {
		evidencePath := filepath.Join(t.TempDir(), "evidence.txt")
		if err := os.WriteFile(evidencePath, []byte("focused tests pass\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		finalizeArgs = append(finalizeArgs, "--evidence", evidencePath)
	}
	if err := RunReviewFacadeFinalize(finalizeArgs, io.Discard); err != nil {
		t.Fatalf("finalize recovery successor: %v", err)
	}
}

func writeScopeRecoveryFile(t *testing.T, repo, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(fmt.Errorf("write %s: %w", name, err))
	}
}
