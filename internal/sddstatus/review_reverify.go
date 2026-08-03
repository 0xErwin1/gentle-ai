package sddstatus

import (
	"context"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// review_reverify.go is Wave 4 S6 (design.md's "Amendment (coordinator-
// resolved): targeted re-verify call site", 2026-08-03): targeted
// re-verify is a routing decision owned by internal/sddstatus's Resolve()/
// resolveEngramStatus(), mirroring the post-verify offer's own call-site
// resolution (decision 3's amendment) -- the routing surface owns
// integration, the orchestrator consumes it, RunSDDVerifyValidate stays
// context-free. It fires only when the change's governing receipt (S5c''s
// resolveGoverningReceiptRef path) records an applied review correction,
// and is purely additive/informational: it never mutates Dependencies or
// NextRecommended itself, the same non-invasive shape Status.ReviewOffer
// already established.
//
// Data-source note (recorded explicitly, matching the amendment's own
// permission): the terminal CompactReceipt SDDReceiptRef points at carries
// no correction changed-path data at all -- only an opaque FixDeltaHash.
// Extending that schema is explicitly out of scope for this amendment. The
// real, already-existing source used instead is the full CompactState
// loaded from the same lineage's compact authoritative store (the same
// store resolveCompactRemediationAuthority's sibling code path already
// loads for remediation purposes):
// CorrectionAttempts[last].Snapshot.Paths. Whenever that is empty or
// absent, branch 7.2 (not reliably derivable) is what fires -- expected to
// be the common case in production until a future wave (Wave 5 or 7, per
// the amendment's Open Questions) revisits the receipt shape to carry
// correction-path data directly.
//
// "Verify evidence scope" is approximated by the compact authority's own
// GenesisPaths narrowed to the openspec/changes/<changeName>/ prefix -- the
// same prefix check compactAuthorityPathsBound (review_gate.go) already
// uses to prove a compact authority is bound to this SDD change. This is a
// deliberate, investigated choice, not an arbitrary one: reviewtransaction's
// own state machine validates every correction attempt's changed paths as a
// SUBSET of the full GenesisPaths (confirmed empirically while building
// this slice's own test fixture -- store.Replace refused a correction path
// outside GenesisPaths with "compact correction attempt is outside frozen
// scope"), so using the FULL GenesisPaths as the evidence-scope side would
// make branch 7.1's empty intersection structurally unreachable (correction
// paths ⊆ GenesisPaths always, so their intersection with GenesisPaths
// itself is never empty unless the correction paths are themselves empty --
// already branch 7.2's territory). Narrowing to the OpenSpec planning-
// artifact slice of GenesisPaths keeps a genuine, meaningful three-way
// split: a correction confined to ordinary source files (the common case)
// leaves nothing about WHAT verify checks changed -- empty intersection,
// branch 7.1; a correction that also touches specs/tasks/design/proposal
// under this change's own openspec path might have changed what verify
// needs to prove -- non-empty intersection, full re-verify. No already-
// exported path-diff primitive exists from RuntimeObjective's candidate-
// tree pair without new reviewtransaction plumbing, which is out of scope
// for this slice.

const (
	// ReVerifyModeTargeted names a cheap re-run of the objective's existing
	// evidence goal: the correction's changed paths did not intersect the
	// verify evidence scope, so nothing new needs proving -- the re-run
	// only refreshes the evidence-revision binding to the corrected
	// candidate.
	ReVerifyModeTargeted = "targeted"
	// ReVerifyModeFull names a full re-verify of the objective's evidence
	// goal: either the correction's changed-path set could not be reliably
	// derived, or it genuinely intersects the verify evidence scope, so the
	// full goal must be re-proven rather than assumed still valid.
	ReVerifyModeFull = "full"
)

// ReVerifyBlock is the orchestrator-facing routing decision task 8.6's
// prose instructs acting on: run sdd-verify with the stated Scope before
// archive.
type ReVerifyBlock struct {
	Mode   string   `json:"mode"`
	Scope  []string `json:"scope,omitempty"`
	Reason string   `json:"reason"`
}

// correctionEvidence is the intermediate shape deriveCorrectionEvidence
// produces from a loaded CompactState, isolated from classifyTargetedReVerify
// so each of tasks 7.1-7.3's branches is independently, genuinely testable
// with synthetic inputs regardless of what today's schema can supply in
// production.
type correctionEvidence struct {
	applied    bool
	paths      []string
	derivable  bool
	failClosed bool
}

// deriveCorrectionEvidence inspects the compact authority's last recorded
// correction attempt, if any. failClosed reports task 7.3's empty-index/
// unborn-HEAD case: the correction's own captured commit state cannot be
// trusted for a path diff at all, so the caller must fail closed (emit no
// block) rather than guess a scope. derivable=false with failClosed=false
// reports task 7.2's case: a correction was recorded but carries no usable
// path data -- the schema-limited common case this amendment anticipates.
func deriveCorrectionEvidence(compact *reviewtransaction.CompactState) correctionEvidence {
	if compact == nil || len(compact.CorrectionAttempts) == 0 {
		return correctionEvidence{}
	}
	last := compact.CorrectionAttempts[len(compact.CorrectionAttempts)-1]
	if last.Snapshot.UnbornHead {
		return correctionEvidence{applied: true, failClosed: true}
	}
	if len(last.Snapshot.Paths) == 0 {
		return correctionEvidence{applied: true}
	}
	return correctionEvidence{applied: true, derivable: true, paths: append([]string(nil), last.Snapshot.Paths...)}
}

// intersectPaths returns the paths present in both sets, order-preserving
// on a, duplicate-free.
func intersectPaths(a, b []string) []string {
	inB := make(map[string]struct{}, len(b))
	for _, path := range b {
		inB[path] = struct{}{}
	}
	seen := make(map[string]struct{}, len(a))
	var overlap []string
	for _, path := range a {
		if _, ok := inB[path]; !ok {
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		overlap = append(overlap, path)
	}
	return overlap
}

const (
	reVerifyNotDerivableReason      = "the correction's changed-path set is not reliably derivable from the governing compact authority; re-verifying the objective's full evidence goal"
	reVerifyEmptyIntersectionReason = "the correction's changed paths do not intersect the verify evidence scope; re-running the objective's evidence goal against the unaffected scope"
	reVerifyIntersectingReason      = "the correction's changed paths intersect the verify evidence scope; re-verifying the objective's full evidence goal"
)

// classifyTargetedReVerify implements tasks 7.1-7.3's three distinct
// branches as a pure function. emit reports whether a routing block should
// be surfaced at all: task 7.3's fail-closed case emits nothing (the
// pre-existing native runtime error routing already owns fail-closed
// reporting for unreadable commit state elsewhere; a routing block here
// would only duplicate or contradict it), and "no correction applied at
// all" is structural absence, the same guard pattern the offer block uses.
func classifyTargetedReVerify(evidence correctionEvidence, scope []string) (ReVerifyBlock, bool) {
	if !evidence.applied || evidence.failClosed {
		return ReVerifyBlock{}, false
	}
	if !evidence.derivable {
		return ReVerifyBlock{Mode: ReVerifyModeFull, Reason: reVerifyNotDerivableReason}, true
	}
	overlap := intersectPaths(evidence.paths, scope)
	if len(overlap) == 0 {
		return ReVerifyBlock{Mode: ReVerifyModeTargeted, Scope: append([]string(nil), scope...), Reason: reVerifyEmptyIntersectionReason}, true
	}
	return ReVerifyBlock{Mode: ReVerifyModeFull, Scope: overlap, Reason: reVerifyIntersectingReason}, true
}

// verifyEvidenceScope narrows the compact authority's GenesisPaths down to
// the OpenSpec planning-artifact paths (specs/tasks/design/proposal) that
// define what SDD's own verify checks -- see this file's top-level doc
// comment for the full investigation behind this choice.
func verifyEvidenceScope(genesisPaths []string, changeName string) []string {
	prefix := "openspec/changes/" + changeName + "/"
	var scope []string
	for _, path := range genesisPaths {
		if strings.HasPrefix(path, prefix) {
			scope = append(scope, path)
		}
	}
	return scope
}

// applyTargetedReVerifyRouting is the one call site (design.md's
// amendment): Resolve() and resolveEngramStatus() both call it
// symmetrically, exactly mirroring applyReviewOfferRouting's own shape. It
// is purely additive: it never mutates Dependencies or NextRecommended,
// only Status.ReVerify. It only fires in the same window the offer already
// requires -- SDD's own verify already passed -- since a correction with
// no completed SDD verify to potentially invalidate has nothing to route.
func applyTargetedReVerifyRouting(ctx context.Context, status *Status, repo, changeName string, governingRef *reviewtransaction.SDDReceiptRef, reviewDisabled bool) {
	if reviewDisabled || governingRef == nil || status.Dependencies.Verify != DependencyAllDone {
		return
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(ctx, repo, governingRef.Lineage)
	if err != nil {
		return
	}
	record, err := store.Load()
	if err != nil {
		return
	}
	evidence := deriveCorrectionEvidence(&record.State)
	scope := verifyEvidenceScope(record.State.GenesisPaths, changeName)
	block, emit := classifyTargetedReVerify(evidence, scope)
	if !emit {
		return
	}
	status.ReVerify = &block
}
