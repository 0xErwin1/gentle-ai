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
	logicalReportPath, err := filepath.Rel(repo, filepath.Join(changeRoot, "verify-report.md"))
	if err != nil {
		return postReviewVerifyReportUnproven
	}
	logicalReportPath = filepath.ToSlash(logicalReportPath)
	if logicalReportPath != path.Join("openspec", "changes", change, "verify-report.md") {
		return postReviewVerifyReportUnproven
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
	rebuilt, err := reviewtransaction.RestoreTreeBlob(ctx, repo, current.CandidateTree, receipt.FinalCandidateTree, logicalReportPath)
	if err != nil || rebuilt != receipt.FinalCandidateTree {
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
