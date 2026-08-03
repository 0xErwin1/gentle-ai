package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// resolveGoverningAuthority is Amendment C's single shared branch (design
// decision 4): every one of the five gates traverses it through
// runReviewFacadeValidate, immediately before discoverCompactFacadeGateReview
// runs — so legacy discovery stays byte-identical whenever nothing v3
// governs this candidate, which is every call that supplies no explicit
// --lineage marker (the ordinary hook-invoked shape) or names a lineage id
// with no v3/ directory at all.
//
// governs=true means the caller MUST NOT fall through to legacy discovery
// for this candidate, even when a legacy receipt exists (Amendment C: "a
// new-lineage candidate is never authorized by legacy"). evaluation is only
// meaningful when governs is true and discoveryErr is nil.
//
// discoveryErr is non-nil for exactly two Amendment-C denial shapes, both of
// which MUST NEVER fall through to legacy authorization: the
// discovery-integrity corruption path (a v3/<lineage> marker exists but its
// record could not be read — reviewtransaction.NewLineageMarkerCorruptedError,
// task 5.2) and the matrix's own "new present but unrelated, legacy present"
// deny cell. Task 5.3's decision: both reuse existing
// ReviewReceiptDiscoveryKind constants rather than minting a new one —
// ReviewAuthorityCorrupted for the corruption path (identical semantics to
// every other "the inventory could not be trusted" denial this file already
// emits) and ReviewReceiptUnrelated for the deny cell (identical semantics
// to the legacy "a receipt exists but does not govern this candidate"
// denial). No silent default: every reachable *ReviewReceiptDiscoveryError
// this function returns has an explicit Kind assigned at its construction
// site below.
func resolveGoverningAuthority(ctx context.Context, root, lineage string, gateInput reviewtransaction.NativeGateRequestInput) (governs bool, evaluation reviewtransaction.NativeGateEvaluation, discoveryErr *ReviewReceiptDiscoveryError) {
	if strings.TrimSpace(lineage) == "" {
		// Discovery rule (design's corrected Amendment C clause): lineage
		// kind is established SOLELY by v3/ record presence. No explicit
		// lineage marker means no v3 authority is discoverable for this
		// candidate at all — "new absent" by construction — so the
		// ordinary hook-invoked gate call costs no extra Git subprocess
		// here, matching decision 5's zero-cost-by-default guarantee.
		return false, reviewtransaction.NativeGateEvaluation{}, nil
	}
	record, found, err := reviewtransaction.DiscoverNewLineage(ctx, root, lineage)
	if err != nil {
		var corrupted *reviewtransaction.NewLineageMarkerCorruptedError
		if errors.As(err, &corrupted) {
			return true, reviewtransaction.NativeGateEvaluation{}, &ReviewReceiptDiscoveryError{Kind: ReviewAuthorityCorrupted, Detail: corrupted.Error()}
		}
		return false, reviewtransaction.NativeGateEvaluation{}, nil
	}
	if !found {
		return false, reviewtransaction.NativeGateEvaluation{}, nil
	}
	live, evidence, err := governingAuthorityLiveEvidence(ctx, root, gateInput)
	if err != nil {
		// No live candidate could be resolved at all — legacy discovery
		// below reports its own target-resolution failure; nothing new
		// governs a candidate identity that could not even be built.
		return false, reviewtransaction.NativeGateEvaluation{}, nil
	}
	observation := reviewtransaction.DeriveObservation(record.Authority, live, evidence)
	legacyPresent := false
	if observation.Relation == reviewtransaction.ShadowRelationUnrelated {
		if _, _, _, legacyErr := discoverFacadeReview(ctx, root, lineage, true); legacyErr == nil {
			legacyPresent = true
		}
	}
	switch reviewtransaction.ResolveGoverningAuthority(true, observation.Relation, legacyPresent) {
	case reviewtransaction.GoverningAuthorityKindDeny:
		return true, reviewtransaction.NativeGateEvaluation{}, &ReviewReceiptDiscoveryError{
			Kind: ReviewReceiptUnrelated, Detail: "a new-lineage candidate is never authorized by a legacy receipt",
		}
	case reviewtransaction.GoverningAuthorityKindLegacy:
		return false, reviewtransaction.NativeGateEvaluation{}, nil
	default: // reviewtransaction.GoverningAuthorityKindNew
		transition, err := (reviewtransaction.ReviewCore{}).Next(ctx, record.Authority, reviewtransaction.CoreRequest{
			Kind: reviewtransaction.CoreRequestValidate, LiveCandidateIdentity: live, Evidence: evidence,
		})
		if err != nil {
			return true, reviewtransaction.NativeGateEvaluation{}, &ReviewReceiptDiscoveryError{Kind: ReviewAuthorityCorrupted, Detail: err.Error()}
		}
		return true, newLineageGateEvaluation(gateInput.Gate, record, transition), nil
	}
}

// governingAuthorityLiveEvidence resolves the live candidate identity and
// validate evidence ReviewCore.Next(validate) needs, reusing the same
// workspace-selector resolution ObserveShadowRelation already uses at every
// one of its five gate call sites (shadow_observer.go) rather than
// rebuilding a gate-specific target a second time.
//
// Scope note (S4/task 5.4): this is a workspace-only resolution, not the
// gate-target-accurate selector each of the five gates' own legacy discovery
// already composes (staged for pre-commit, committed-range for pre-push/
// pre-pr/release). Wave 3 Slice 5's bench journeys (task 6.7) are the
// declared place to widen this to per-gate accuracy; S4's own task list
// requires only the governance decision itself (Amendment C) and the
// continue/escalate shapes the switch-off-equivalence goldens exercise
// without ever reaching this function (no explicit --lineage marker means
// resolveGoverningAuthority returns before this is ever called).
func governingAuthorityLiveEvidence(ctx context.Context, repo string, gateInput reviewtransaction.NativeGateRequestInput) (reviewtransaction.CandidateIdentity, reviewtransaction.CoreValidateEvidence, error) {
	builder := reviewtransaction.SnapshotBuilder{Repo: repo}
	intendedUntracked := gateInput.IntendedUntracked
	if intendedUntracked == nil {
		intendedUntracked = []string{}
	}
	snapshot, err := builder.Build(ctx, reviewtransaction.Target{
		Kind: reviewtransaction.TargetCurrentChanges, Projection: reviewtransaction.ProjectionWorkspace,
		IntendedUntracked: intendedUntracked,
	})
	if err != nil {
		return reviewtransaction.CandidateIdentity{}, reviewtransaction.CoreValidateEvidence{}, err
	}
	live, err := reviewtransaction.FreezeCandidateIdentity(ctx, repo, snapshot, "")
	if err != nil {
		return reviewtransaction.CandidateIdentity{}, reviewtransaction.CoreValidateEvidence{}, err
	}
	return live, reviewtransaction.CoreValidateEvidence{LiveSnapshot: snapshot, ApplicableAuthorities: 1}, nil
}

// newLineageGateEvaluation translates a ReviewCore validate CoreTransition
// into gate JSON. It covers exactly the two shapes reachable from S4's own
// task list: continue (allow) and escalate. Every other CoreTransitionKind
// (collect/approve/repair/stop) is denied with its transition kind named as
// the reason code rather than silently mapped to allow — the full
// CoreTransition-to-reason-taxonomy mapping for those kinds is Wave 3 Slice
// 5's task 6.2, reused rather than duplicated here (design decision 7).
func newLineageGateEvaluation(gate reviewtransaction.GateKind, record reviewtransaction.NewLineageRecord, transition reviewtransaction.CoreTransition) reviewtransaction.NativeGateEvaluation {
	context := reviewtransaction.GateContext{
		Gate: gate, LineageID: record.Authority.LineageID, StoreRevision: record.Revision,
		BaseTree: record.Authority.CandidateIdentity.BaseTree, CandidateTree: record.Authority.CandidateIdentity.CandidateTree,
		PolicyHash: record.Authority.CandidateIdentity.PolicyHash,
	}
	switch transition.Kind {
	case reviewtransaction.CoreTransitionContinue:
		return reviewtransaction.NativeGateEvaluation{Result: reviewtransaction.GateAllow, Reason: transition.ReasonCode, Context: context}
	case reviewtransaction.CoreTransitionEscalate:
		context.Denial = &reviewtransaction.GateDenial{Stage: "new-lineage-validate", Code: transition.ReasonCode}
		return reviewtransaction.NativeGateEvaluation{Result: reviewtransaction.GateEscalated, Reason: transition.ReasonCode, Context: context}
	default:
		context.Denial = &reviewtransaction.GateDenial{Stage: "new-lineage-validate", Code: string(transition.Kind)}
		return reviewtransaction.NativeGateEvaluation{Result: reviewtransaction.GateInvalidated, Reason: transition.ReasonCode, Context: context}
	}
}
