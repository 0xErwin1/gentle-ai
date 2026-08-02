package releaseartifact

import "fmt"

// RequiredFloor is the hand-declared, FROZEN minimum semantic snapshot
// surface a consumer verifies is present (design D4, spec "Required-Floor
// Compatibility for Operations, Schemas, Gates, and Projections").
//
// It is NEVER recomputed from the live capability surface at generation
// time. A floor computed from "whatever operations/gates/projections/
// schemas exist right now" moves with every release and stops being a
// floor — a removal from the live surface would then go completely
// unnoticed, because the floor would have silently shrunk along with it.
// Every value below is a literal, checked-in string, not a call into
// internal/cli (which this stdlib-only package cannot import in any case).
//
// Values are frozen as of the gentle-ai.review-integration/v1 and v2
// contracts' initial capability surface (2026-08-01). Widening this floor
// in a later release is a deliberate, reviewed edit — not automatic.
var RequiredFloor = struct {
	Operations  []string
	Gates       []string
	Projections []string
	Schemas     []string
}{
	// Core negotiation operations, identical across contract v1 and v2.
	Operations: []string{
		"review.capabilities",
		"review.start",
		"review.status",
	},
	// All five delivery gates. Small and stable, but a future removal must
	// still fail this floor rather than pass silently.
	Gates: []string{
		"post-apply",
		"pre-commit",
		"pre-push",
		"pre-pr",
		"release",
	},
	// Both existing target projections.
	Projections: []string{
		"staged",
		"workspace",
	},
	// A representative subset of schema identities that are identical
	// across the v1 and v2 review integration contracts today.
	Schemas: []string{
		"gentle-ai.review-receipt/v1",
		"gentle-ai.review-receipt/v2",
		"gentle-ai.review-gate-request/v1",
		"gentle-ai.review-integration.projection/v1",
		"gentle-ai.review-result-artifact/v2",
	},
}

// VerifyRequiredFloor confirms every element of RequiredFloor is present in
// the corresponding field of snapshot. It returns an error naming the first
// missing element. An addition beyond the floor is never an error — only a
// removal is (spec "New optional operation does not break older consumer
// logic").
func VerifyRequiredFloor(snapshot SemanticSnapshot) error {
	if missing, ok := firstMissing(RequiredFloor.Operations, snapshot.Operations); !ok {
		return fmt.Errorf("required floor operation %q is missing from the snapshot", missing)
	}
	if missing, ok := firstMissing(RequiredFloor.Gates, snapshot.Gates); !ok {
		return fmt.Errorf("required floor gate %q is missing from the snapshot", missing)
	}
	if missing, ok := firstMissing(RequiredFloor.Projections, snapshot.Projections); !ok {
		return fmt.Errorf("required floor projection %q is missing from the snapshot", missing)
	}
	if missing, ok := firstMissing(RequiredFloor.Schemas, snapshot.Schemas); !ok {
		return fmt.Errorf("required floor schema %q is missing from the snapshot", missing)
	}
	return nil
}

// firstMissing reports the first element of required not present in
// actual. ok is true when every required element is present.
func firstMissing(required, actual []string) (missing string, ok bool) {
	present := make(map[string]bool, len(actual))
	for _, a := range actual {
		present[a] = true
	}
	for _, r := range required {
		if !present[r] {
			return r, false
		}
	}
	return "", true
}
