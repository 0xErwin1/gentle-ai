// Package reviewtransaction — the Wave-4 offer API (design decision 8, Wave
// 3 Slice 5, task 6.5; wired Wave 4 S3b/S5b). OfferReviewAfterVerify is
// design decision 8's literal signature, now called from internal/sddstatus
// (S3b's applyReviewOfferRouting, through review_door.go's
// reviewOfferForVerify).
package reviewtransaction

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
)

// OfferRequest names the one candidate an offer decision would be made for.
// Receipt is Wave 4 S5b's closed loop: the caller's own-looked-up terminal
// receipt for this candidate, read from SDD's own runtime ledger
// (sddstatus.RuntimeStatus.Receipt) and passed in. reviewtransaction never
// looks this up itself — internal/sddstatus already imports
// internal/reviewtransaction, so the reverse import would cycle; the
// package boundary is why the caller resolves it, not this function.
type OfferRequest struct {
	LineageID string
	Receipt   *SDDReceiptRef
}

// Offer is OfferReviewAfterVerify's exact result shape. Available false
// never means "denied" — it means no offer is made for this call; Wave 4's
// wiring is what turns this into a genuinely actionable choice.
type Offer struct {
	Available bool
	LineageID string
}

// OfferReviewAfterVerify is Wave 4's post-verify offer point (design decision
// 8's literal signature): after a candidate's independent verification
// evidence exists, this asks whether receipt-driven development should now
// offer a review for it.
//
// The kill switch, when recorded off at the GLOBAL scope, returns
// Offer{Available: false}, nil BEFORE any repository read. That ordering is
// provable here specifically because the clone-local override is off-only
// (SetCloneLocalRDDMode's own ErrRDDModeRepositoryForcedOn invariant) — a
// clone can never override a global-off switch back to on — so a
// global-only reading is on its own a complete, honest answer for the
// disabled case, with nothing left for a repository read to add.
//
// Wave 4 S5b closes the loop design decision 8 left open: when the switch is
// on, the real decision is receipt discovery, not a fabricated fixed
// answer. request.Receipt (the caller's own-looked-up terminal receipt, if
// the runtime ledger has one for this lineage) is re-validated through
// ValidateSDDReceiptRef — the same one native validation entry point every
// other consumer uses, never a re-derivation. A receipt that still resolves
// GateAllow already governs this exact candidate: nothing to offer.
// Anything else (no receipt recorded yet, or one that no longer resolves
// allow) is a genuine invitation to start a review.
func OfferReviewAfterVerify(ctx context.Context, repo string, request OfferRequest) (Offer, error) {
	if err := ctx.Err(); err != nil {
		return Offer{}, err
	}
	// repo is deliberately unread on the disabled path: that ordering proof
	// stays intact regardless of the widened on-path below.
	global, err := readGlobalRDDModeForOffer()
	if err != nil {
		return Offer{}, err
	}
	if mode, modeErr := normalizeRDDMode(global.Value); modeErr == nil && mode == RDDModeOff {
		return Offer{Available: false, LineageID: request.LineageID}, nil
	}
	available := true
	if request.Receipt != nil {
		result, _, validateErr := ValidateSDDReceiptRef(ctx, repo, *request.Receipt)
		if validateErr == nil && result == GateAllow {
			available = false
		}
	}
	return Offer{Available: available, LineageID: request.LineageID}, nil
}

// readGlobalRDDModeForOffer reads only the uncommitted global kill-switch
// value, never the repository. It intentionally duplicates
// internal/cli's readGlobalRDDMode rather than calling it: reviewtransaction
// is the lower layer in this wave's package boundary (design's own note on
// FreezeCandidateIdentity/authorizeReviewStart — cli-owned reuse is called
// FROM cli INTO reviewtransaction, never the reverse), so this package
// cannot import internal/cli without a cycle. Both read exactly
// internal/state's persisted RDDMode field the same way.
func readGlobalRDDModeForOffer() (RDDGlobalMode, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return RDDGlobalMode{}, err
	}
	persisted, err := state.Read(home)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return RDDGlobalMode{}, nil
		}
		return RDDGlobalMode{}, err
	}
	global := RDDGlobalMode{Value: persisted.RDDMode}
	if persisted.RDDModeRecordedAt != nil {
		global.RecordedAt = persisted.RDDModeRecordedAt.UTC()
	}
	return global, nil
}
