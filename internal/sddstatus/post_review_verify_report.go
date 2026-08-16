package sddstatus

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"reflect"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

type postReviewVerifyReportAttestation int

const (
	postReviewVerifyReportUnproven postReviewVerifyReportAttestation = iota
	postReviewVerifyReportRequired
	postReviewVerifyReportBound
)

// classifyPostReviewVerifyReportAttestation proves the exact final verify
// settlement, report bytes, current candidate tree, and one-path receipt delta.
// A missing digest is recoverable only after every structural check succeeds;
// the legacy caller-owned work-unit label does not govern the recovery offer.
func classifyPostReviewVerifyReportAttestation(
	ctx context.Context,
	repo, workspace, change string,
	ref reviewtransaction.SDDReceiptRef,
	runtime *RuntimeStatus,
	specCounts SpecCounts,
) postReviewVerifyReportAttestation {
	if runtime == nil || runtime.ActiveAttempt != nil || !runtime.Complete || len(runtime.Attempts) == 0 {
		return postReviewVerifyReportUnproven
	}
	settlement := runtime.Attempts[len(runtime.Attempts)-1]
	if settlement.Outcome != AttemptPassed || settlement.FinishCandidateTree == "" || settlement.EvidenceRevision == "" {
		return postReviewVerifyReportUnproven
	}

	store, err := reviewtransaction.CompactAuthoritativeStore(ctx, repo, ref.Lineage)
	if err != nil {
		return postReviewVerifyReportUnproven
	}
	record, err := store.Load()
	if err != nil || record.State.State != reviewtransaction.StateApproved || record.State.Validate() != nil {
		return postReviewVerifyReportUnproven
	}
	receiptPayload, err := os.ReadFile(store.ReceiptPath())
	if err != nil || verifyReportDigest(receiptPayload) != ref.ReceiptHash {
		return postReviewVerifyReportUnproven
	}
	receipt, err := reviewtransaction.ParseCompactReceipt(receiptPayload)
	if err != nil || receipt.LineageID != ref.Lineage {
		return postReviewVerifyReportUnproven
	}
	authoritativeReceipt, err := record.State.Receipt()
	if err != nil || !reflect.DeepEqual(receipt, authoritativeReceipt) ||
		record.State.CurrentSnapshot.CandidateTree != receipt.FinalCandidateTree {
		return postReviewVerifyReportUnproven
	}

	changeRoot, err := resolveBindingChangeRoot(ctx, repo, workspace, change)
	if err != nil {
		return postReviewVerifyReportUnproven
	}
	reportPath := filepath.Join(changeRoot, "verify-report.md")
	// Mirror captureFinalVerifyReport: the canonical path is anchored at the
	// planning workspace, tree reads at the repository root (which may differ).
	workspaceReportPath, err := filepath.Rel(workspace, reportPath)
	if err != nil || filepath.ToSlash(workspaceReportPath) != path.Join("openspec", "changes", change, "verify-report.md") {
		return postReviewVerifyReportUnproven
	}
	logicalReportPath, err := filepath.Rel(repo, reportPath)
	if err != nil {
		return postReviewVerifyReportUnproven
	}
	logicalReportPath = filepath.ToSlash(logicalReportPath)

	// Status classification never writes to the Git object database: this cheap
	// byte gate rejects a worktree report that cannot match the attested digest
	// before any snapshot build hashes drifted bytes; filters only fail closed.
	if settlement.AttestedVerifyReportDigest != "" {
		worktreeReport, err := os.ReadFile(reportPath)
		if err != nil || len(worktreeReport) > MaxVerifyReportBytes ||
			verifyReportDigest(worktreeReport) != settlement.AttestedVerifyReportDigest {
			return postReviewVerifyReportUnproven
		}
	}

	current, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).Build(ctx, reviewtransaction.Target{
		Kind: reviewtransaction.TargetBaseWorkspaceOverlay, BaseRef: receipt.FinalCandidateTree,
		Projection: reviewtransaction.ProjectionWorkspace, IntendedUntracked: []string{},
	})
	if err != nil || current.CandidateTree != settlement.FinishCandidateTree ||
		!reflect.DeepEqual(current.Paths, []string{logicalReportPath}) {
		return postReviewVerifyReportUnproven
	}
	payload, err := reviewtransaction.ReadTreeBlob(ctx, repo, current.CandidateTree, logicalReportPath, MaxVerifyReportBytes)
	if err != nil {
		return postReviewVerifyReportUnproven
	}
	admission := ValidateVerifyReportAdmission(string(payload), specCounts)
	if !admission.Valid || admission.Verdict != "pass" || admission.EvidenceRevision != settlement.EvidenceRevision {
		return postReviewVerifyReportUnproven
	}
	// Read-only single-blob-delta proof: current.Paths proved only the report
	// path differs and both ReadTreeBlob calls prove a canonical 100644 blob on
	// each side, so swapping that one blob reproduces the receipt tree exactly
	// without RestoreTreeBlob's object-writing write-tree round-trip.
	if _, err := reviewtransaction.ReadTreeBlob(ctx, repo, receipt.FinalCandidateTree, logicalReportPath, MaxVerifyReportBytes); err != nil {
		return postReviewVerifyReportUnproven
	}
	// Legacy records had no native digest and work-unit labels are caller-owned.
	// Their label cannot grant archive authority, but it cannot suppress the safe
	// recovery offer once every structural report check above has succeeded.
	if settlement.AttestedVerifyReportDigest == "" {
		return postReviewVerifyReportRequired
	}
	// Only the explicit current-binary final verification labels can carry the
	// native digest that grants the archive-status exception.
	if !isFinalVerifyWorkUnit(settlement.WorkUnit) || !runtimeRevisionPattern.MatchString(settlement.AttestedVerifyReportDigest) ||
		verifyReportDigest(payload) != settlement.AttestedVerifyReportDigest {
		return postReviewVerifyReportUnproven
	}
	return postReviewVerifyReportBound
}
