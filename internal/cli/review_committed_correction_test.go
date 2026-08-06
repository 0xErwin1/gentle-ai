package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

func TestSelectorlessCommittedCorrectionContinuesToReceipt(t *testing.T) {
	for _, amend := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "amend"}[amend], func(t *testing.T) {
			repo, base, lineage := forecastCommittedCorrection(t)
			writeCommittedCorrection(t, repo, amend)
			// The mutable ref must not influence the frozen correction boundary.
			runReviewCLIGit(t, repo, "branch", "-f", base, "HEAD")

			status := committedCorrectionStatus(t, repo)
			if status.Authority == nil || status.Authority.LineageID != lineage || status.NextTransition == nil ||
				status.NextTransition.Kind != reviewNextTransitionCollect || status.NextTransition.ReasonCode != "correction_repository_verification_required" {
				t.Fatalf("post-commit correction status = %#v", status)
			}
			request := capturePassedCorrectionEvidenceForTest(t, repo, lineage)

			status = committedCorrectionStatus(t, repo)
			if status.ValidationRequest == nil || status.ValidationRequest.CorrectionTargetIdentity != request.CorrectionTargetIdentity ||
				status.NextTransition == nil || status.NextTransition.ReasonCode != "targeted_validation_required" {
				t.Fatalf("post-evidence correction status = %#v", status)
			}
			validation := filepath.Join(t.TempDir(), "validation.json")
			writeReviewCLIJSON(t, validation, facadeValidationResult{
				TargetedValidationRequestHash: request.RequestHash,
				CorrectionTargetIdentity:      request.CorrectionTargetIdentity,
				OriginalCriteria:              facadeValidationCheck{Passed: true, Evidence: []string{"original criteria passed"}},
				CorrectionRegression:          facadeValidationCheck{Passed: true, Evidence: []string{"correction regression passed"}},
				FollowUps:                     []reviewtransaction.FollowUp{},
			})
			if err := RunReviewFacadeFinalize([]string{
				"--cwd", repo, "--contract", ReviewIntegrationContractV1, "--validation", validation, "--captured-evidence",
			}, &bytes.Buffer{}); err != nil {
				t.Fatalf("selector-less committed correction finalize: %v", err)
			}

			store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, lineage)
			if err != nil {
				t.Fatal(err)
			}
			record, err := store.Load()
			if err != nil || record.State.State != reviewtransaction.StateApproved || record.State.EvidenceTargetIdentity != request.CorrectionTargetIdentity {
				t.Fatalf("committed correction receipt state = %#v, %v", record.State, err)
			}
			if _, err := os.Stat(store.ReceiptPath()); err != nil {
				t.Fatalf("committed correction receipt = %v", err)
			}
		})
	}
}

func TestStagedCorrectionContinuesToReceipt(t *testing.T) {
	repo := initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "tracked.txt", "base\none\ntwo\nthree\nwrong\n", 0o644)
	runReviewCLIGit(t, repo, "add", "tracked.txt")
	var output bytes.Buffer
	if err := runLegacyFacadeStartForTest(t, []string{"--cwd", repo, "--lineage", "staged-correction", "--projection", "staged"}, &output); err != nil {
		t.Fatal(err)
	}
	var started ReviewFacadeStartResult
	if err := json.Unmarshal(output.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(t.TempDir(), "reviewer.json")
	writeReviewCLIJSON(t, result, facadeReviewerResult{Lens: started.SelectedLenses[0], Findings: []facadeFinding{{
		Location: "tracked.txt:5", Severity: "CRITICAL", Claim: "candidate is wrong",
		ProofRefs: []string{"tracked.txt:5 changed hunk"}, EvidenceClass: reviewtransaction.EvidenceDeterministic,
		CausalDisposition: reviewtransaction.CausalIntroduced,
	}}, Evidence: []string{"reviewed frozen staged candidate"}})
	if err := finalizeReviewCLIArgs(t, repo, []string{"--cwd", repo, "--lineage", started.LineageID, "--result", result}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := RunReviewFacadeFinalize([]string{"--cwd", repo, "--lineage", started.LineageID, "--correction-lines", "2"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	writeReviewStartCandidate(t, repo, "tracked.txt", "base\none\ntwo\nthree\nfixed\n", 0o644)
	runReviewCLIGit(t, repo, "add", "tracked.txt")
	status := readCorrectionEvidenceStatus(t, []string{"status", "--cwd", repo, "--contract", ReviewIntegrationContractV1, "--next-transition", "--lineage", started.LineageID, "--projection", "staged"})
	if status.NextTransition == nil || status.NextTransition.ReasonCode != "correction_repository_verification_required" {
		t.Fatalf("staged correction status = %#v", status)
	}
	request := capturePassedCorrectionEvidenceForTest(t, repo, started.LineageID)
	validation := filepath.Join(t.TempDir(), "validation.json")
	writeReviewCLIJSON(t, validation, facadeValidationResult{
		TargetedValidationRequestHash: request.RequestHash, CorrectionTargetIdentity: request.CorrectionTargetIdentity,
		OriginalCriteria:     facadeValidationCheck{Passed: true, Evidence: []string{"original criteria passed"}},
		CorrectionRegression: facadeValidationCheck{Passed: true, Evidence: []string{"correction regression passed"}}, FollowUps: []reviewtransaction.FollowUp{},
	})
	if err := RunReviewFacadeFinalize([]string{"--cwd", repo, "--lineage", started.LineageID, "--validation", validation, "--captured-evidence"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, started.LineageID)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Load()
	if err != nil || record.State.State != reviewtransaction.StateApproved || record.State.EvidenceTargetIdentity != request.CorrectionTargetIdentity {
		t.Fatalf("staged correction receipt state = %#v, %v", record.State, err)
	}
}

func TestSelectorlessCommittedCorrectionFailsClosedForUnreadableAuthority(t *testing.T) {
	repo, base, _ := forecastCommittedCorrection(t)
	writeCommittedCorrection(t, repo, false)
	runReviewCLIGit(t, repo, "branch", "-f", base, "HEAD")

	statePath := filepath.Join(reviewCLICompactStoreDir(repo, "unreadable-authority"), "review-state.json")
	if err := os.MkdirAll(statePath, 0o755); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := RunReview([]string{
		"status", "--cwd", repo, "--contract", ReviewIntegrationContractV1, "--next-transition",
	}, &output)
	if err == nil {
		t.Fatalf("selector-less status selected a recoverable lineage despite unreadable authority: %s", output.String())
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("selector-less status error = %T %[1]v, want operational path error", err)
	}
}

func forecastCommittedCorrection(t *testing.T) (string, string, string) {
	t.Helper()
	repo := initReviewCLIRepo(t)
	base := "frozen-base"
	runReviewCLIGit(t, repo, "branch", base, "HEAD")
	writeReviewStartCandidate(t, repo, "tracked.txt", "base\none\ntwo\nthree\nwrong\n", 0o644)
	runReviewCLIGit(t, repo, "add", "tracked.txt")
	runReviewCLIGit(t, repo, "commit", "-qm", "wrong candidate")

	const lineage = "committed-correction"
	status := selectorTransitionStatus(t, repo, "--lineage", lineage, "--base-ref", base)
	var started ReviewFacadeStartResult
	if err := json.Unmarshal(executeSelectorTransition(t, repo, status), &started); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(t.TempDir(), "reviewer.json")
	writeReviewCLIJSON(t, result, facadeReviewerResult{
		Lens: started.SelectedLenses[0], Findings: []facadeFinding{{
			Location: "tracked.txt:5", Severity: "CRITICAL", Claim: "candidate is wrong",
			ProofRefs: []string{"tracked.txt:5 changed hunk"}, EvidenceClass: reviewtransaction.EvidenceDeterministic,
			CausalDisposition: reviewtransaction.CausalIntroduced,
		}}, Evidence: []string{"reviewed frozen committed candidate"},
	})
	if err := finalizeReviewCLIArgs(t, repo, []string{"--cwd", repo, "--lineage", lineage, "--result", result}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := RunReviewFacadeFinalize([]string{"--cwd", repo, "--lineage", lineage, "--correction-lines", "2"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	return repo, base, lineage
}

func writeCommittedCorrection(t *testing.T, repo string, amend bool) {
	t.Helper()
	writeReviewStartCandidate(t, repo, "tracked.txt", "base\none\ntwo\nthree\nfixed\n", 0o644)
	runReviewCLIGit(t, repo, "add", "tracked.txt")
	if amend {
		runReviewCLIGit(t, repo, "commit", "--amend", "--no-edit")
		return
	}
	runReviewCLIGit(t, repo, "commit", "-qm", "correct candidate")
}

func committedCorrectionStatus(t *testing.T, repo string) ReviewTargetStatusResult {
	t.Helper()
	var output bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--contract", ReviewIntegrationContractV1, "--next-transition",
	}, &output); err != nil {
		t.Fatal(err)
	}
	var status ReviewTargetStatusResult
	decodeStrictReviewJSON(t, output.Bytes(), &status)
	return status
}
