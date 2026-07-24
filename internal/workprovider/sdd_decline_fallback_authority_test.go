package workprovider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestCoordinationSDDDeclineFallbackIsContentAddressedAndSingleAssignment(
	t *testing.T,
) {
	ctx := context.Background()
	repo := initCoordinationRepository(t)
	const workRunID = "sdd-fallback-authority"
	store, err := OpenCoordinationAuthorityStore(
		ctx,
		repo,
		workRunID,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	pending := sddFallbackPendingDecision(t)
	delegated := sddFallbackDelegatedDecision(t)
	publication := SDDDeclineFallbackPublication{
		WorkRunID:             workRunID,
		DeliveryIntentRef:     coordinationTestRef("sdd-fallback-delivery"),
		PendingDecisionDigest: pending.Digest,
		RouteDecision:         delegated,
	}
	record, err := store.PublishSDDDeclineFallback(ctx, publication)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.PublishSDDDeclineFallback(ctx, publication)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, record) {
		t.Fatalf("fallback replay = %#v, want %#v", replayed, record)
	}
	resolved, err := store.ResolveSDDDeclineFallback(
		ctx,
		record.AuthorityRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved, record) {
		t.Fatalf("resolved fallback = %#v, want %#v", resolved, record)
	}
	if _, err := store.PublishSDDDeclineFallback(
		ctx,
		SDDDeclineFallbackPublication{
			WorkRunID:             publication.WorkRunID,
			DeliveryIntentRef:     publication.DeliveryIntentRef,
			PendingDecisionDigest: publication.PendingDecisionDigest,
			RouteDecision:         sddFallbackDirectDecision(t),
		},
	); !errors.Is(err, ErrCoordinationAuthorityConflict) {
		t.Fatalf("conflicting fallback publication error = %v", err)
	}

	other, err := OpenCoordinationAuthorityStore(
		ctx,
		repo,
		"sdd-fallback-other-run",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.ResolveSDDDeclineFallback(
		ctx,
		record.AuthorityRef,
	); !errors.Is(err, ErrCoordinationAuthorityCrossRun) {
		t.Fatalf("cross-run fallback resolution error = %v", err)
	}
}

func TestProductiveDeclineRejectsFallbackAuthorityTamperingBeforeMutation(
	t *testing.T,
) {
	tests := []struct {
		name   string
		tamper func(*testing.T, productiveRouteTestFixture)
	}{
		{
			name: "missing start ref",
			tamper: func(t *testing.T, fixture productiveRouteTestFixture) {
				store, authority := productiveRouteStartAuthority(t, fixture)
				authority.SDDDeclineFallbackRef = ""
				writeProductiveRouteStartAuthority(t, store, authority)
			},
		},
		{
			name: "legacy inline delegated changed to direct",
			tamper: func(t *testing.T, fixture productiveRouteTestFixture) {
				store, authority := productiveRouteStartAuthority(t, fixture)
				record := productiveRouteFallbackRecord(t, fixture, authority)
				authority.SDDDeclineFallbackRef = ""
				authority.SDDDeclineFallback = &productiveSDDDeclineFallback{
					PendingDecisionDigest: record.PendingDecisionDigest,
					RouteDecision:         sddFallbackDirectDecision(t),
				}
				writeProductiveRouteStartAuthority(t, store, authority)
			},
		},
		{
			name: "missing object",
			tamper: func(t *testing.T, fixture productiveRouteTestFixture) {
				_, authority := productiveRouteStartAuthority(t, fixture)
				coordination := productiveRouteCoordination(t, fixture)
				if err := os.Remove(coordination.objectPath(
					coordinationKindSDDFallback,
					authority.SDDDeclineFallbackRef,
				)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "corrupt object",
			tamper: func(t *testing.T, fixture productiveRouteTestFixture) {
				_, authority := productiveRouteStartAuthority(t, fixture)
				coordination := productiveRouteCoordination(t, fixture)
				if err := os.WriteFile(
					coordination.objectPath(
						coordinationKindSDDFallback,
						authority.SDDDeclineFallbackRef,
					),
					[]byte("{}\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing index",
			tamper: func(t *testing.T, fixture productiveRouteTestFixture) {
				_, authority := productiveRouteStartAuthority(t, fixture)
				coordination := productiveRouteCoordination(t, fixture)
				record := productiveRouteFallbackRecord(
					t,
					fixture,
					authority,
				)
				slotRef, err := sddDeclineFallbackSlotRef(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(coordination.slotPath(
					coordinationKindSDDFallback,
					slotRef,
				)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "corrupt index",
			tamper: func(t *testing.T, fixture productiveRouteTestFixture) {
				_, authority := productiveRouteStartAuthority(t, fixture)
				coordination := productiveRouteCoordination(t, fixture)
				record := productiveRouteFallbackRecord(
					t,
					fixture,
					authority,
				)
				slotRef, err := sddDeclineFallbackSlotRef(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					coordination.slotPath(
						coordinationKindSDDFallback,
						slotRef,
					),
					[]byte("{}\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "cross run ref",
			tamper: func(t *testing.T, fixture productiveRouteTestFixture) {
				store, authority := productiveRouteStartAuthority(t, fixture)
				current := productiveRouteFallbackRecord(
					t,
					fixture,
					authority,
				)
				other, err := OpenCoordinationAuthorityStore(
					context.Background(),
					fixture.repo,
					"sdd-fallback-cross-run",
					nil,
				)
				if err != nil {
					t.Fatal(err)
				}
				foreign, err := other.PublishSDDDeclineFallback(
					context.Background(),
					SDDDeclineFallbackPublication{
						WorkRunID:             other.workRunID,
						DeliveryIntentRef:     current.DeliveryIntentRef,
						PendingDecisionDigest: current.PendingDecisionDigest,
						RouteDecision:         current.RouteDecision,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				authority.SDDDeclineFallbackRef = foreign.AuthorityRef
				writeProductiveRouteStartAuthority(t, store, authority)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProductiveRouteTestFixture(
				t,
				"sdd-fallback-tamper-"+testWorkRunSuffix(test.name),
				false,
			)
			test.tamper(t, fixture)
			assertProductiveDeclineFailsWithoutMutation(t, fixture)
		})
	}
}

func TestProductiveDeclineRejectsCoherentFallbackRewriteUsingWorkRunAnchor(
	t *testing.T,
) {
	fixture := newProductiveRouteTestFixture(
		t,
		"sdd-fallback-coherent-rewrite",
		false,
	)
	startStore, start := productiveRouteStartAuthority(t, fixture)
	coordination := productiveRouteCoordination(t, fixture)
	original := productiveRouteFallbackRecord(t, fixture, start)
	replacement, err := newSDDDeclineFallbackRecord(
		original.RepositoryIdentity,
		SDDDeclineFallbackPublication{
			WorkRunID:             original.WorkRunID,
			DeliveryIntentRef:     original.DeliveryIntentRef,
			PendingDecisionDigest: original.PendingDecisionDigest,
			RouteDecision:         sddFallbackDirectDecision(t),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	objectPayload, err := canonicalCoordinationPayload(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		coordination.objectPath(
			coordinationKindSDDFallback,
			replacement.AuthorityRef,
		),
		objectPayload,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	slotRef, err := sddDeclineFallbackSlotRef(replacement)
	if err != nil {
		t.Fatal(err)
	}
	indexPayload, err := canonicalCoordinationPayload(coordinationIndex{
		Schema:             coordinationIndexSchema,
		Kind:               coordinationKindSDDFallback,
		SlotRef:            slotRef,
		AuthorityRef:       replacement.AuthorityRef,
		RepositoryIdentity: replacement.RepositoryIdentity,
		WorkRunID:          replacement.WorkRunID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		coordination.slotPath(coordinationKindSDDFallback, slotRef),
		indexPayload,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	start.SDDDeclineFallbackRef = replacement.AuthorityRef
	writeProductiveRouteStartAuthority(t, startStore, start)
	if _, err := coordination.ResolveSDDDeclineFallback(
		context.Background(),
		replacement.AuthorityRef,
	); err != nil {
		t.Fatalf("coherently rewritten coordination authority = %v", err)
	}

	work, err := workrun.OpenWorkRunStore(
		context.Background(),
		fixture.repo,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err := work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.SDDDeclineFallbackRef != original.AuthorityRef {
		t.Fatalf(
			"WorkRun fallback anchor = %q, want original %q",
			state.SDDDeclineFallbackRef,
			original.AuthorityRef,
		)
	}
	assertProductiveDeclineFailsWithoutMutation(t, fixture)
}

func TestProductiveLegacyFallbackCompatibilityIsReadOnlyAndFailClosed(
	t *testing.T,
) {
	t.Run("status and accept remain available", func(t *testing.T) {
		fixture := newProductiveRouteTestFixture(
			t,
			"sdd-fallback-legacy-accept",
			false,
		)
		store, authority := productiveRouteStartAuthority(t, fixture)
		authority.SDDDeclineFallbackRef = ""
		authority.SDDDeclineFallback = nil
		writeProductiveRouteStartAuthority(t, store, authority)

		work, err := workrun.OpenWorkRunStore(
			context.Background(),
			fixture.repo,
			fixture.workRunID,
		)
		if err != nil {
			t.Fatal(err)
		}
		status, err := work.PublicStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.RoutePhase != workrun.RoutePhaseDecisionPending {
			t.Fatalf("legacy status = %#v", status)
		}
		accepted, err := fixture.runtime.DecideRoute(
			context.Background(),
			fixture.workRunID,
			fixture.start.Revision,
			workrun.WorkRouteChoiceAcceptSDD,
		)
		if err != nil {
			t.Fatal(err)
		}
		if accepted.SelectedRoute != workrun.ImplementationRouteSDD {
			t.Fatalf("legacy accept = %#v", accepted)
		}
	})

	t.Run("decline remains fail closed", func(t *testing.T) {
		fixture := newProductiveRouteTestFixture(
			t,
			"sdd-fallback-legacy-decline",
			false,
		)
		store, authority := productiveRouteStartAuthority(t, fixture)
		authority.SDDDeclineFallbackRef = ""
		authority.SDDDeclineFallback = &productiveSDDDeclineFallback{
			PendingDecisionDigest: fixture.start.Revision,
			RouteDecision:         sddFallbackDirectDecision(t),
		}
		// Use the real pending digest; legacy inline validity is intentionally
		// independent from its lack of mutation authority.
		state := productiveRouteWorkState(t, fixture)
		authority.SDDDeclineFallback.PendingDecisionDigest =
			state.RouteDecision.Digest
		writeProductiveRouteStartAuthority(t, store, authority)
		assertProductiveDeclineFailsWithoutMutation(t, fixture)
	})
}

func TestProductiveConcurrentDeclineReplaysOneImmutableFallback(
	t *testing.T,
) {
	fixture := newProductiveRouteTestFixture(
		t,
		"sdd-fallback-concurrent-decline",
		false,
	)
	const calls = 2
	start := make(chan struct{})
	results := make(chan struct {
		route workrun.WorkRouteV1
		err   error
	}, calls)
	var ready sync.WaitGroup
	ready.Add(calls)
	for range calls {
		go func() {
			ready.Done()
			<-start
			route, err := fixture.runtime.DecideRoute(
				context.Background(),
				fixture.workRunID,
				fixture.start.Revision,
				workrun.WorkRouteChoiceDeclineSDD,
			)
			results <- struct {
				route workrun.WorkRouteV1
				err   error
			}{route: route, err: err}
		}()
	}
	ready.Wait()
	close(start)
	var want workrun.WorkRouteV1
	successes := 0
	for index := 0; index < calls; index++ {
		result := <-results
		if result.err != nil {
			if !errors.Is(result.err, workrun.ErrWorkRunConcurrentUpdate) {
				t.Fatal(result.err)
			}
			continue
		}
		successes++
		if want.Contract == "" {
			want = result.route
			continue
		}
		if !reflect.DeepEqual(result.route, want) {
			t.Fatalf("concurrent decline = %#v, want %#v", result.route, want)
		}
	}
	if successes == 0 {
		t.Fatal("concurrent decline produced no successful owner mutation")
	}
	replayed, err := fixture.runtime.DecideRoute(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
		workrun.WorkRouteChoiceDeclineSDD,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, want) {
		t.Fatalf("post-contention replay = %#v, want %#v", replayed, want)
	}
}

func productiveRouteStartAuthority(
	t *testing.T,
	fixture productiveRouteTestFixture,
) (productiveAdvanceStore, productiveStartAuthority) {
	t.Helper()
	route, err := fixture.runtime.resolveProductiveRouteAuthority(
		context.Background(),
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := existingProductiveAdvanceStore(
		route.factory.lease,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := store.startAuthority(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return store, authority
}

func writeProductiveRouteStartAuthority(
	t *testing.T,
	store productiveAdvanceStore,
	authority productiveStartAuthority,
) {
	t.Helper()
	payload, err := json.Marshal(authority)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		store.root+"/start-authority.json",
		append(payload, '\n'),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func productiveRouteCoordination(
	t *testing.T,
	fixture productiveRouteTestFixture,
) *CoordinationAuthorityStore {
	t.Helper()
	route, err := fixture.runtime.resolveProductiveRouteAuthority(
		context.Background(),
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return route.coordination
}

func productiveRouteFallbackRecord(
	t *testing.T,
	fixture productiveRouteTestFixture,
	authority productiveStartAuthority,
) SDDDeclineFallbackRecord {
	t.Helper()
	coordination := productiveRouteCoordination(t, fixture)
	record, err := coordination.ResolveSDDDeclineFallback(
		context.Background(),
		authority.SDDDeclineFallbackRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func productiveRouteWorkState(
	t *testing.T,
	fixture productiveRouteTestFixture,
) workrun.WorkRunState {
	t.Helper()
	store, err := workrun.OpenWorkRunStore(
		context.Background(),
		fixture.repo,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertProductiveDeclineFailsWithoutMutation(
	t *testing.T,
	fixture productiveRouteTestFixture,
) {
	t.Helper()
	beforeState := productiveRouteWorkState(t, fixture)
	beforeTree := ownerTreeContentFingerprint(
		t,
		defaultProviderRoot(t, fixture.repo),
	)
	if _, err := fixture.runtime.DecideRoute(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
		workrun.WorkRouteChoiceDeclineSDD,
	); err == nil {
		t.Fatal("tampered SDD decline fallback unexpectedly authorized mutation")
	}
	afterState := productiveRouteWorkState(t, fixture)
	if !reflect.DeepEqual(afterState, beforeState) {
		t.Fatalf(
			"rejected decline changed WorkRun:\nbefore %#v\nafter  %#v",
			beforeState,
			afterState,
		)
	}
	afterTree := ownerTreeContentFingerprint(
		t,
		defaultProviderRoot(t, fixture.repo),
	)
	if afterTree != beforeTree {
		t.Fatalf(
			"rejected decline changed owner authority:\nbefore %s\nafter  %s",
			beforeTree,
			afterTree,
		)
	}
}

func sddFallbackPendingDecision(
	t *testing.T,
) workrun.ImplementationRouteDecision {
	t.Helper()
	decision, err := workrun.DecideImplementationRoute(
		workrun.ImplementationRouteInput{
			WriteIntent:    workrun.WriteIntentAnalytical,
			WriteFileCount: 2,
			DurablePlanning: []workrun.DurablePlanningReason{
				workrun.PlanningArchitecture,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func sddFallbackDelegatedDecision(
	t *testing.T,
) workrun.ImplementationRouteDecision {
	t.Helper()
	decision, err := workrun.DecideImplementationRoute(
		workrun.ImplementationRouteInput{
			WriteIntent:    workrun.WriteIntentAnalytical,
			WriteFileCount: 2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func sddFallbackDirectDecision(
	t *testing.T,
) workrun.ImplementationRouteDecision {
	t.Helper()
	decision, err := workrun.DecideImplementationRoute(
		workrun.ImplementationRouteInput{
			WriteIntent:    workrun.WriteIntentAtomicMechanical,
			WriteFileCount: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func testWorkRunSuffix(value string) string {
	result := make([]byte, 0, len(value))
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' {
			result = append(result, character)
			continue
		}
		result = append(result, '-')
	}
	return string(result)
}
