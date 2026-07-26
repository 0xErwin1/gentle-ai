package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

// TestReviewGateDeniedErrorNamesItsContinuation is the RED-first proof that
// the human-surface ReviewGateDeniedError.Error() names a concrete
// continuation per GateResult instead of a bare "review lifecycle gate
// denied: <result>" mute block. Every case must derive its continuation from
// the same source the negotiated envelope already uses
// (reviewGateAction / GateScopeChangeDiagnostics.RecoveryOperation), never a
// second hand-written copy of the routing knowledge.
func TestReviewGateDeniedErrorNamesItsContinuation(t *testing.T) {
	bareMessage := func(result reviewtransaction.GateResult) string {
		return fmt.Sprintf("review lifecycle gate denied: %s", result)
	}

	t.Run("scope-changed with recovery diagnostics names review.recover and its required inputs", func(t *testing.T) {
		denied := ReviewGateDeniedError{
			Result: reviewtransaction.GateScopeChanged,
			Context: reviewtransaction.GateContext{
				Gate: reviewtransaction.GatePrePush,
				ScopeChange: &reviewtransaction.GateScopeChangeDiagnostics{
					RecoveryOperation: "review.recover",
					RecoveryRequiredInputs: []string{
						"predecessor_lineage_id", "expected_predecessor_revision", "successor_lineage_id",
						"disposition", "reason", "actor",
					},
				},
			},
		}
		got := denied.Error()
		if got == bareMessage(reviewtransaction.GateScopeChanged) {
			t.Fatalf("scope-changed Error() is still the bare mute block: %q", got)
		}
		if !strings.Contains(got, "review.recover") {
			t.Fatalf("scope-changed Error() = %q, want it to name review.recover", got)
		}
		for _, input := range denied.Context.ScopeChange.RecoveryRequiredInputs {
			if !strings.Contains(got, input) {
				t.Fatalf("scope-changed Error() = %q, want it to name required input %q", got, input)
			}
		}
	})

	t.Run("scope-changed without diagnostics states the terminal precondition, not a fabricated command", func(t *testing.T) {
		denied := ReviewGateDeniedError{
			Result:  reviewtransaction.GateScopeChanged,
			Context: reviewtransaction.GateContext{Gate: reviewtransaction.GatePostApply},
		}
		got := denied.Error()
		if got == bareMessage(reviewtransaction.GateScopeChanged) {
			t.Fatalf("scope-changed (no diagnostics) Error() is still the bare mute block: %q", got)
		}
		if strings.Contains(got, "review.recover") {
			t.Fatalf("scope-changed (no diagnostics) Error() = %q, must not fabricate review.recover without diagnostics", got)
		}
		if !strings.Contains(got, "explicit maintainer action") {
			t.Fatalf("scope-changed (no diagnostics) Error() = %q, want the honest terminal precondition", got)
		}
	})

	t.Run("escalated mirrors the earlier review.status routing", func(t *testing.T) {
		denied := ReviewGateDeniedError{Result: reviewtransaction.GateEscalated}
		got := denied.Error()
		want := reviewGateAction(reviewtransaction.GateEscalated)
		if want != "review.status" {
			t.Fatalf("precondition changed: reviewGateAction(escalated) = %q, want review.status", want)
		}
		if got == bareMessage(reviewtransaction.GateEscalated) {
			t.Fatalf("escalated Error() is still the bare mute block: %q", got)
		}
		if !strings.Contains(got, "review.status") {
			t.Fatalf("escalated Error() = %q, want it to name review.status", got)
		}
	})

	t.Run("invalidated names the terminal precondition instead of inventing a command", func(t *testing.T) {
		denied := ReviewGateDeniedError{Result: reviewtransaction.GateInvalidated}
		got := denied.Error()
		if got == bareMessage(reviewtransaction.GateInvalidated) {
			t.Fatalf("invalidated Error() is still the bare mute block: %q", got)
		}
		if !strings.Contains(got, "explicit maintainer action") {
			t.Fatalf("invalidated Error() = %q, want the honest terminal precondition", got)
		}
	})

	t.Run("Result and prefix are untouched: same denial, same result value", func(t *testing.T) {
		for _, result := range []reviewtransaction.GateResult{
			reviewtransaction.GateScopeChanged, reviewtransaction.GateInvalidated, reviewtransaction.GateEscalated,
		} {
			denied := ReviewGateDeniedError{Result: result}
			if !strings.HasPrefix(denied.Error(), bareMessage(result)) {
				t.Fatalf("Error() = %q, want it to still start with %q (message-only change, no condition change)", denied.Error(), bareMessage(result))
			}
		}
	})
}

// TestReviewGateDeniedNegotiatedEnvelopeUnchangedByHumanMessage is the
// byte-identical proof (non-negotiable #3): enriching the human-surface
// Error() string must never change the negotiated JSON failure envelope
// fields that already carry the routing knowledge.
func TestReviewGateDeniedNegotiatedEnvelopeUnchangedByHumanMessage(t *testing.T) {
	tree := strings.Repeat("a", 40)
	sha := "sha256:" + strings.Repeat("b", 64)
	denied := ReviewGateDeniedError{
		Result: reviewtransaction.GateScopeChanged,
		Context: reviewtransaction.GateContext{
			Gate: reviewtransaction.GatePrePush,
			ScopeChange: &reviewtransaction.GateScopeChangeDiagnostics{
				Expected:             reviewtransaction.GateTargetEvidence{CandidateTree: tree, PathsDigest: sha},
				Actual:               reviewtransaction.GateTargetEvidence{CandidateTree: tree, PathsDigest: sha},
				DifferingPathsDigest: sha,
				PredecessorLineageID: "predecessor-lineage",
				PredecessorRevision:  sha,
				RecoveryOperation:    "review.recover",
				RecoveryRequiredInputs: []string{
					"predecessor_lineage_id", "expected_predecessor_revision", "successor_lineage_id",
					"disposition", "reason", "actor",
				},
			},
		},
	}
	failure := newReviewIntegrationFailure(ReviewIntegrationOperationValidate, nil, denied)
	if failure.Message != "The review delivery gate denied the current target." {
		t.Fatalf("negotiated failure Message changed = %q, want the existing byte-identical envelope text", failure.Message)
	}
	if failure.Code != "gate_scope_changed" || failure.NextAction != reviewGateAction(reviewtransaction.GateScopeChanged) {
		t.Fatalf("negotiated failure Code/NextAction changed = code=%q next_action=%q", failure.Code, failure.NextAction)
	}
	if failure.Context == nil || failure.Context.ScopeChange == nil ||
		failure.Context.ScopeChange.RecoveryOperation != "review.recover" ||
		!strings.Contains(strings.Join(failure.Context.ScopeChange.RecoveryRequiredInputs, ","), "predecessor_lineage_id") {
		t.Fatalf("negotiated failure scope-change context changed = %#v", failure.Context)
	}
	if err := failure.Validate(); err != nil {
		t.Fatalf("negotiated failure with untouched routing fields must still validate: %v", err)
	}
}
