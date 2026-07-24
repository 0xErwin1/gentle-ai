package workrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
)

type statusDeliveryResultPort struct {
	authority DeliveryResultAuthority
	calls     int
}

func (port *statusDeliveryResultPort) ResolveDeliveryResult(
	_ context.Context,
	ref string,
) (DeliveryResultAuthority, error) {
	port.calls++
	if ref != port.authority.ResultRef {
		return DeliveryResultAuthority{}, errors.New(
			"unexpected delivery result reference",
		)
	}
	return port.authority, nil
}

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

func TestPublicStatusProjectsProductiveReconciliationWithoutReopeningBlocker(
	t *testing.T,
) {
	tests := []struct {
		name        string
		outcome     WorkReconcileOutcome
		publicState PublicState
	}{
		{
			name:        "delivery confirmed is ready",
			outcome:     WorkReconcileDeliveryConfirmed,
			publicState: PublicStateReady,
		},
		{
			name:        "no delivery starts fresh",
			outcome:     WorkReconcileNoDeliveryConfirmed,
			publicState: PublicStateNeedsYourDecision,
		},
		{
			name:        "manual resolution remains terminal",
			outcome:     WorkReconcileManualResolution,
			publicState: PublicStateNeedsYourDecision,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, blocked, reconciliationPort :=
				newBlockedProductiveReconciliationFixtureForOutcome(
					t,
					test.outcome,
				)
			reconciliationRef := statusTestRef(
				"status-reconciliation-" + string(test.outcome),
			)
			authority := testProductiveReconciliationAuthority(
				t,
				store,
				blocked,
				reconciliationRef,
				test.outcome,
			)
			reconciliationPort.register(authority)
			reconciled, err := store.RecordProductiveReconciliation(
				context.Background(),
				RecordProductiveReconciliationRequest{
					ExpectedRevision:      blocked.Revision,
					RequestID:             "status-" + string(test.outcome),
					OriginalDiagnosticRef: blocked.ProductiveBlockerRef,
					ReconciliationRef:     reconciliationRef,
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			owner := testAuthorityForStore(t, store)
			owner.mu.Lock()
			authorizationCalls := owner.authorizationResolveCalls
			owner.productiveDiagnosticErrors[blocked.ProductiveBlockerRef] =
				errors.New(
					"historical blocker must not be resolved after reconciliation",
				)
			owner.mu.Unlock()
			deliveryPort := &statusDeliveryResultPort{
				authority: DeliveryResultAuthority{
					ResultRef:             authority.DeliveryResultRef,
					ExecutionResultRef:    reconciled.ProductiveExecutionResultRef,
					AuthorizationRef:      reconciled.DeliveryAuthorizationRef,
					DeliveryIntentRef:     reconciled.DeliveryIntentRef,
					CandidateRef:          reconciled.Handoff.CandidateRef,
					ReviewReceiptRef:      reconciled.ReviewReceiptRef,
					VerificationResultRef: reconciled.VerificationResultRef,
					DeliveryRef:           "delivery:status-reconciliation",
					CompletedAt:           1,
				},
			}
			ports := store.authority
			ports.DeliveryResult = deliveryPort
			store = store.WithAuthorityPorts(ports)

			status, err := store.PublicStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status.PublicState != test.publicState ||
				status.Revision != reconciled.Revision ||
				status.AuthorizedTransition != nil {
				t.Fatalf(
					"reconciled public status = %#v, want state %q",
					status,
					test.publicState,
				)
			}
			if calls := reconciliationPort.callCount(); calls != 3 {
				t.Fatalf(
					"reconciliation authority calls = %d, want pre, post, and status",
					calls,
				)
			}
			owner.mu.Lock()
			authorizationCallsAfter := owner.authorizationResolveCalls
			owner.mu.Unlock()
			if authorizationCallsAfter != authorizationCalls {
				t.Fatalf(
					"reconciled status resolved historical live authorization: %d -> %d",
					authorizationCalls,
					authorizationCallsAfter,
				)
			}

			switch test.outcome {
			case WorkReconcileDeliveryConfirmed:
				if status.Diagnostic != nil ||
					deliveryPort.calls != 1 ||
					reconciled.ProductiveBlockerRef !=
						blocked.ProductiveBlockerRef ||
					reconciled.DeliveryResultRef !=
						authority.DeliveryResultRef {
					t.Fatalf(
						"confirmed reconciliation projection = %#v / calls %d",
						status,
						deliveryPort.calls,
					)
				}
			case WorkReconcileNoDeliveryConfirmed,
				WorkReconcileManualResolution:
				if status.Diagnostic == nil ||
					!reflect.DeepEqual(
						status.Diagnostic,
						authority.Diagnostic,
					) ||
					status.Diagnostic.Ref ==
						blocked.ProductiveBlockerRef ||
					deliveryPort.calls != 0 ||
					reconciled.DeliveryResultRef != "" {
					t.Fatalf(
						"non-delivery reconciliation projection = %#v / authority %#v / calls %d",
						status,
						authority,
						deliveryPort.calls,
					)
				}
			default:
				t.Fatalf("unsupported test outcome %q", test.outcome)
			}
		})
	}
}

func statusTestRef(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}
