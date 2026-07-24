package workrun

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestProjectPublicStatusKeepsPostAuthorizationBlockerTerminal(t *testing.T) {
	decision, err := DecideImplementationRoute(ImplementationRouteInput{
		ReadIntent:     ReadIntentNone,
		WriteIntent:    WriteIntentAtomicMechanical,
		WriteFileCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := WorkRunState{
		Schema:               WorkRunStateSchemaV1,
		WorkRunID:            "work-status-post-authorization-blocker",
		Revision:             statusTestRef("revision"),
		Started:              true,
		RouteDecision:        decision,
		ImplementationRoute:  ImplementationRouteDirectInline,
		DeliveryIntentRef:    statusTestRef("intent"),
		ProductiveBlockerRef: statusTestRef("blocker"),
	}
	status, err := projectPublicStatus(
		state,
		nil,
		&DeliveryAuthorizationAuthority{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicState != PublicStateNeedsYourDecision {
		t.Fatalf(
			"post-authorization blocker state = %q, want %q",
			status.PublicState,
			PublicStateNeedsYourDecision,
		)
	}
}

func statusTestRef(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}
