package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// ReviewFacadeFinalizeNewLineageResult is finalize's v3 activation-branch
// response shape (task C1). It is deliberately not ReviewFacadeFinalizeResult:
// that type is keyed to legacy CompactState's Mode/State vocabulary and a
// compact receipt shape, neither of which NewLineageAuthority/NewLineageReceipt
// use — mirroring ReviewFacadeStartNewLineageResult's own precedent
// (review_facade_new_lineage.go) of not reusing a legacy-shaped type.
type ReviewFacadeFinalizeNewLineageResult struct {
	Operation          string                            `json:"operation"`
	LineageID          string                            `json:"lineage_id"`
	State              reviewtransaction.NewLineageState `json:"state"`
	AdmittedFindingIDs []string                          `json:"admitted_finding_ids,omitempty"`
	Receipt            *reviewtransaction.ReceiptRef     `json:"receipt,omitempty"`
}

// runReviewFacadeFinalizeNewLineage is the missing caller CRITICAL C1
// identified: design decision 5 says finalize routes on discovered lineage
// kind, but no production code ever called ReviewCore.finalize +
// AuthorityStore.WriteReceipt from the CLI — a v3 lineage the shipped
// `review start` creates could never be finalized through the product. This
// supplies that caller, reusing the exact receipt-issuance path S2/S3 already
// built and tested (TestReviewCoreFinalizeApprovedIssuesReceiptRef,
// authority_store_test.go's receipt-immutability suite) rather than adding
// new receipt logic.
//
// It is the tier-0 minimum-viable finalize path (no reviewer-lens execution,
// no correction plan — those are Wave 4+ territory per this wave's own
// documented scope, apply-progress task 6.7): a lineage with no admitted
// candidate-causal blocker (task C2, reviewtransaction.AdmitCandidateCausalFindings)
// and no --failed evidence advances reviewing/validating straight through
// validating to approved; an admitted blocker or --failed advances it to
// escalated instead. Both routes issue the terminal receipt through the
// identical ReviewCore.Next(finalize) + AuthorityStore.WriteReceipt call the
// rollback-safety test (review_new_lineage_rollback_safety_test.go) already
// drives directly against the Go API — this function is the CLI wiring that
// test's own comment noted was still missing.
//
// A lineage already `correcting` is refused rather than silently folded into
// the terminal decision: resolving an in-flight bounded correction is
// `review validate`'s job (ReviewCore.validate's ErrOneCorrectionOnly
// boundary) — finalize must never race ahead of that open question.
func runReviewFacadeFinalizeNewLineage(
	ctx context.Context, stdout io.Writer, root, lineage string, record reviewtransaction.NewLineageRecord,
	findings []reviewtransaction.FindingEvidence, failed bool,
) error {
	store, err := reviewtransaction.NewLineageAuthorityStore(ctx, root, lineage)
	if err != nil {
		return err
	}
	authority := record.Authority
	revision := record.Revision

	if authority.State == reviewtransaction.NewLineageStateCorrecting {
		return fmt.Errorf("new-lineage finalize requires the in-flight bounded correction to resolve through review validate first: lineage %q is correcting", lineage) // refusal:by-design world-action: resolving a bounded correction is review validate's job (ReviewCore.validate's changed/correcting boundary and AuthorityStore's ErrOneCorrectionOnly); finalize is never the place to force one, so the fix is calling review validate again, not this command
	}

	if authority.State != reviewtransaction.NewLineageStateApproved && authority.State != reviewtransaction.NewLineageStateEscalated {
		admitted, _ := reviewtransaction.AdmitCandidateCausalFindings(findings)
		terminalState := reviewtransaction.NewLineageStateApproved
		if failed || len(admitted) > 0 {
			terminalState = reviewtransaction.NewLineageStateEscalated
		}
		// design's own finalize data flow ("no-blocker -> validating ->
		// approved"): advance through validating explicitly rather than
		// jumping reviewing -> terminal directly, so review-state.json's
		// state field genuinely visits the intermediate persisted state
		// (task C4's five-persisted-states lifecycle evidence) instead of
		// only ever proving the two endpoints.
		revision, err = store.Mutate(ctx, revision, func(next *reviewtransaction.NewLineageAuthority) error {
			next.State = reviewtransaction.NewLineageStateValidating
			return nil
		})
		if err != nil {
			return fmt.Errorf("advance new-lineage authority to validating: %w", err)
		}
		revision, err = store.Mutate(ctx, revision, func(next *reviewtransaction.NewLineageAuthority) error {
			next.State = terminalState
			if len(admitted) > 0 {
				next.AdmittedFindingIDs = append(append([]string{}, next.AdmittedFindingIDs...), admitted...)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("advance new-lineage authority to %s: %w", terminalState, err)
		}
	}

	record, err = store.Load()
	if err != nil {
		return err
	}
	transition, err := (reviewtransaction.ReviewCore{}).Next(ctx, record.Authority, reviewtransaction.CoreRequest{Kind: reviewtransaction.CoreRequestFinalize})
	if err != nil {
		return fmt.Errorf("review core finalize: %w", err)
	}
	if transition.Receipt == nil {
		return fmt.Errorf("review core finalize returned no receipt for terminal state %q", record.Authority.State) // refusal:by-design world-action: ReviewCore.finalize always returns a non-nil Receipt for an approved/escalated authority (review_core.go); anything else reaching here is a bug in that code, fixed by editing it, not an operator command
	}
	if err := store.WriteReceipt(ctx, reviewtransaction.NewLineageReceipt{
		Schema: reviewtransaction.NewLineageReceiptSchema, LineageID: record.Authority.LineageID,
		TerminalState: record.Authority.State, AuthorityRevision: transition.Receipt.AuthorityRevision,
		CandidateIdentity: record.Authority.CandidateIdentity,
	}); err != nil {
		return fmt.Errorf("write new-lineage terminal receipt: %w", err)
	}
	result := ReviewFacadeFinalizeNewLineageResult{
		Operation: "review/finalize", LineageID: lineage, State: record.Authority.State,
		AdmittedFindingIDs: append([]string{}, record.Authority.AdmittedFindingIDs...), Receipt: transition.Receipt,
	}
	return encodeReviewJSON(stdout, result)
}
