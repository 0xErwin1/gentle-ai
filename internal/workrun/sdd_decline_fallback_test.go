package workrun

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWorkRunStartAnchorsFallbackOnlyForOptionalSDDProposal(
	t *testing.T,
) {
	fallbackRef := testSHARef("optional-sdd-fallback")
	tests := []struct {
		name     string
		decision func(*testing.T) ImplementationRouteDecision
		wantOK   bool
	}{
		{
			name: "optional SDD proposal",
			decision: func(t *testing.T) ImplementationRouteDecision {
				t.Helper()
				decision, err := DecideImplementationRoute(
					ImplementationRouteInput{
						DurablePlanning: []DurablePlanningReason{
							PlanningArchitecture,
						},
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				return decision
			},
			wantOK: true,
		},
		{
			name: "direct",
			decision: func(t *testing.T) ImplementationRouteDecision {
				return directRouteDecision(t)
			},
		},
		{
			name: "delegated",
			decision: func(t *testing.T) ImplementationRouteDecision {
				t.Helper()
				decision, err := DecideImplementationRoute(
					ImplementationRouteInput{
						WriteIntent:    WriteIntentAnalytical,
						WriteFileCount: 2,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				return decision
			},
		},
		{
			name: "explicit SDD proposal",
			decision: func(t *testing.T) ImplementationRouteDecision {
				t.Helper()
				decision, err := DecideImplementationRoute(
					ImplementationRouteInput{
						ExplicitSDDRequestRef: testSHARef(
							"explicit-sdd-with-fallback",
						),
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				return decision
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestWorkRunStore(
				t,
				initWorkRunRepository(t),
				"fallback-"+strings.ReplaceAll(
					strings.ToLower(test.name),
					" ",
					"-",
				),
			)
			deliveryIntentRef := testSHARef(
				"fallback-delivery-" + test.name,
			)
			registerTestDeliveryIntent(t, store, deliveryIntentRef)
			state, err := store.Start(
				context.Background(),
				StartRequest{
					RequestID:             "start-fallback",
					RouteDecision:         test.decision(t),
					DeliveryIntentRef:     deliveryIntentRef,
					SDDDeclineFallbackRef: fallbackRef,
				},
			)
			if test.wantOK {
				if err != nil {
					t.Fatal(err)
				}
				if state.SDDDeclineFallbackRef != fallbackRef {
					t.Fatalf(
						"WorkRun fallback anchor = %q, want %q",
						state.SDDDeclineFallbackRef,
						fallbackRef,
					)
				}
				return
			}
			if err == nil || !strings.Contains(
				err.Error(),
				"optional SDD proposal",
			) {
				t.Fatalf("invalid fallback binding error = %v", err)
			}
			if _, statusErr := store.Status(); !errors.Is(
				statusErr,
				ErrWorkRunNotStarted,
			) {
				t.Fatalf(
					"invalid fallback binding mutated WorkRun: %v",
					statusErr,
				)
			}
		})
	}
}

func TestWorkRunReplayRejectsInvalidFallbackBinding(t *testing.T) {
	state := WorkRunState{
		Schema:                          WorkRunStateSchemaV1,
		WorkRunID:                       "fallback-replay-invariant",
		Reservations:                    []VerificationReservation{},
		LaunchClaims:                    []VerificationLaunchClaim{},
		NextOrdinal:                     1,
		ReusableVerificationObligations: []string{},
	}
	record := workRunRecord{
		Schema:        workRunRecordSchemaV1,
		WorkRunID:     state.WorkRunID,
		Operation:     workOperationStart,
		RequestID:     "start-invalid-fallback",
		RequestDigest: testSHARef("invalid-fallback-request"),
		Start: &workStartEvent{
			RouteDecision:         directRouteDecision(t),
			DeliveryIntentRef:     testSHARef("invalid-fallback-delivery"),
			SDDDeclineFallbackRef: testSHARef("invalid-fallback-anchor"),
		},
	}
	if err := applyWorkRunRecord(
		&state,
		record,
	); err == nil || !strings.Contains(err.Error(), "optional SDD proposal") {
		t.Fatalf("invalid replay fallback binding error = %v", err)
	}
	if state.Started || state.SDDDeclineFallbackRef != "" {
		t.Fatalf("invalid replay fallback mutated state = %#v", state)
	}
}
