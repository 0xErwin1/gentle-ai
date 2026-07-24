package workprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestProductiveReconcileIsOneShotExactReplayWithoutAnotherEffect(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"reconcile-indeterminate",
		productiveAdvanceTestCASIndeterminate,
		false,
	)
	ctx := context.Background()
	advance, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if advance.Diagnostic == nil ||
		advance.Diagnostic.NextAction !=
			workrun.WorkAdvanceNextActionReconcile ||
		advance.Status.Diagnostic == nil ||
		!reflect.DeepEqual(advance.Diagnostic, advance.Status.Diagnostic) {
		t.Fatalf("advance did not expose exact reconciliation authority: %#v", advance)
	}
	callsBefore := fixture.casCalls()
	reconciled, err := fixture.runtime.ReconcileOutcome(
		ctx,
		fixture.workRunID,
		advance.Status.Revision,
		advance.Diagnostic.Ref,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Outcome != workrun.WorkReconcileManualResolution ||
		reconciled.Status.PublicState !=
			workrun.PublicStateNeedsYourDecision ||
		reconciled.Status.Diagnostic == nil ||
		reconciled.Status.Diagnostic.NextAction !=
			workrun.WorkAdvanceNextActionManual ||
		reconciled.Status.Diagnostic.Code !=
			workrun.WorkAdvanceDiagnosticManualResolutionRequired ||
		reconciled.DeliveryResultRef != "" ||
		fixture.casCalls() != callsBefore {
		t.Fatalf(
			"one-shot reconciliation = %#v; CAS %d -> %d",
			reconciled,
			callsBefore,
			fixture.casCalls(),
		)
	}

	store, _ := fixture.existingStore(t)
	resultPath := filepath.Join(
		store.root,
		store.reconcileResultName(advance.Status.Revision),
	)
	if err := os.Remove(resultPath); err != nil {
		t.Fatal(err)
	}
	replayed, err := fixture.runtime.ReconcileOutcome(
		ctx,
		fixture.workRunID,
		advance.Status.Revision,
		advance.Diagnostic.Ref,
	)
	if err != nil ||
		!reflect.DeepEqual(replayed, reconciled) ||
		fixture.casCalls() != callsBefore {
		t.Fatalf(
			"reconciliation replay = %#v, %v; CAS %d",
			replayed,
			err,
			fixture.casCalls(),
		)
	}
	if _, err := os.Lstat(resultPath); err != nil {
		t.Fatalf("post-commit replay did not restore public result: %v", err)
	}

	oldAdvance, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil ||
		!reflect.DeepEqual(oldAdvance, advance) ||
		fixture.casCalls() != callsBefore {
		t.Fatalf(
			"historical advance replay = %#v, %v; CAS %d",
			oldAdvance,
			err,
			fixture.casCalls(),
		)
	}

	_, err = fixture.runtime.ReconcileOutcome(
		ctx,
		fixture.workRunID,
		reconciled.Status.Revision,
		reconciled.Status.Diagnostic.Ref,
	)
	if err == nil {
		t.Fatal("manual terminal reconciliation was accepted as another attempt")
	}
}

func TestProductiveReconcileDerivesConfirmedAndNoDeliveryOutcomes(
	t *testing.T,
) {
	t.Run("delivery confirmed from durable PAD result", func(t *testing.T) {
		fixture := newProductiveAdvanceTestFixture(
			t,
			"reconcile-delivered",
			productiveAdvanceTestCASSucceed,
			true,
		)
		advance := blockProductiveReconciliationAfterDurableSuccess(
			t,
			fixture,
		)
		callsBefore := fixture.casCalls()
		reconciled, err := fixture.runtime.ReconcileOutcome(
			context.Background(),
			fixture.workRunID,
			advance.Status.Revision,
			advance.Diagnostic.Ref,
		)
		if err != nil {
			t.Fatal(err)
		}
		if reconciled.Outcome != workrun.WorkReconcileDeliveryConfirmed ||
			reconciled.Status.PublicState != workrun.PublicStateReady ||
			reconciled.Status.Diagnostic != nil ||
			reconciled.DeliveryResultRef == "" ||
			fixture.casCalls() != callsBefore {
			t.Fatalf(
				"confirmed reconciliation = %#v; CAS %d -> %d",
				reconciled,
				callsBefore,
				fixture.casCalls(),
			)
		}
		store, factory := fixture.existingStore(t)
		state, status := fixture.currentStateAndStatus(t, factory)
		if state.ProductiveBlockerRef != advance.Diagnostic.Ref ||
			state.ProductiveReconciliationSourceRevision !=
				advance.Status.Revision ||
			state.ProductiveReconciliationOutcome !=
				workrun.WorkReconcileDeliveryConfirmed ||
			state.DeliveryResultRef != reconciled.DeliveryResultRef ||
			!reflect.DeepEqual(status, reconciled.Status) {
			t.Fatalf(
				"confirmed WorkRun/status = %#v / %#v",
				state,
				status,
			)
		}
		cached, ok, err := store.reconcileResult(
			context.Background(),
			advance.Status.Revision,
			advance.Diagnostic.Ref,
			state,
			status,
		)
		if err != nil || !ok || !reflect.DeepEqual(cached, reconciled) {
			t.Fatalf(
				"confirmed reconciliation cache = %#v, %t, %v",
				cached,
				ok,
				err,
			)
		}
	})

	t.Run("no delivery confirmed before effect reservation", func(t *testing.T) {
		fixture := newProductiveAdvanceTestFixture(
			t,
			"reconcile-not-delivered",
			productiveAdvanceTestCASSucceed,
			false,
		)
		advance := blockProductiveReconciliationBeforeEffect(t, fixture)
		reconciled, err := fixture.runtime.ReconcileOutcome(
			context.Background(),
			fixture.workRunID,
			advance.Status.Revision,
			advance.Diagnostic.Ref,
		)
		if err != nil {
			t.Fatal(err)
		}
		if reconciled.Outcome !=
			workrun.WorkReconcileNoDeliveryConfirmed ||
			reconciled.Status.PublicState !=
				workrun.PublicStateNeedsYourDecision ||
			reconciled.Status.Diagnostic == nil ||
			reconciled.Status.Diagnostic.Code !=
				workrun.WorkAdvanceDiagnosticDeliveryNotCompleted ||
			reconciled.Status.Diagnostic.NextAction !=
				workrun.WorkAdvanceNextActionStartFresh ||
			reconciled.DeliveryResultRef != "" ||
			fixture.casCalls() != 0 ||
			fixture.remoteRevision(t) != fixture.base {
			t.Fatalf(
				"no-delivery reconciliation = %#v; CAS %d",
				reconciled,
				fixture.casCalls(),
			)
		}
		replayed, err := fixture.runtime.ReconcileOutcome(
			context.Background(),
			fixture.workRunID,
			advance.Status.Revision,
			advance.Diagnostic.Ref,
		)
		if err != nil || !reflect.DeepEqual(replayed, reconciled) ||
			fixture.casCalls() != 0 {
			t.Fatalf(
				"no-delivery replay = %#v, %v; CAS %d",
				replayed,
				err,
				fixture.casCalls(),
			)
		}
	})
}

func TestProductiveReconcileFreezesMissingHistoricalAdvanceBeforeCommit(
	t *testing.T,
) {
	tests := []struct {
		name        string
		wantOutcome workrun.WorkReconcileOutcome
		setup       func(*testing.T) (
			productiveAdvanceTestFixture,
			workrun.WorkAdvanceV1,
		)
	}{
		{
			name:        "manual",
			wantOutcome: workrun.WorkReconcileManualResolution,
			setup: func(t *testing.T) (
				productiveAdvanceTestFixture,
				workrun.WorkAdvanceV1,
			) {
				fixture := newProductiveAdvanceTestFixture(
					t,
					"missing-history-manual",
					productiveAdvanceTestCASIndeterminate,
					false,
				)
				advance, err := fixture.runtime.AdvanceOutcome(
					context.Background(),
					fixture.workRunID,
					fixture.start.Revision,
				)
				if err != nil {
					t.Fatal(err)
				}
				return fixture, advance
			},
		},
		{
			name:        "no delivery",
			wantOutcome: workrun.WorkReconcileNoDeliveryConfirmed,
			setup: func(t *testing.T) (
				productiveAdvanceTestFixture,
				workrun.WorkAdvanceV1,
			) {
				fixture := newProductiveAdvanceTestFixture(
					t,
					"missing-history-no-delivery",
					productiveAdvanceTestCASSucceed,
					false,
				)
				return fixture,
					blockProductiveReconciliationBeforeEffect(
						t,
						fixture,
					)
			},
		},
		{
			name:        "delivery confirmed",
			wantOutcome: workrun.WorkReconcileDeliveryConfirmed,
			setup: func(t *testing.T) (
				productiveAdvanceTestFixture,
				workrun.WorkAdvanceV1,
			) {
				fixture := newProductiveAdvanceTestFixture(
					t,
					"missing-history-delivered",
					productiveAdvanceTestCASSucceed,
					true,
				)
				return fixture,
					blockProductiveReconciliationAfterDurableSuccess(
						t,
						fixture,
					)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, original := test.setup(t)
			if original.Diagnostic == nil ||
				original.Diagnostic.NextAction !=
					workrun.WorkAdvanceNextActionReconcile ||
				original.PreviousRevision != fixture.start.Revision {
				t.Fatalf("reconciliation source advance = %#v", original)
			}
			store, factory := fixture.existingStore(t)
			resultPath := filepath.Join(
				store.root,
				store.resultName(fixture.start.Revision),
			)
			if err := os.Remove(resultPath); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(resultPath); !errors.Is(
				err,
				os.ErrNotExist,
			) {
				t.Fatalf("historical result still exists: %v", err)
			}
			callsBefore := fixture.casCalls()
			reconciled, err := fixture.runtime.ReconcileOutcome(
				context.Background(),
				fixture.workRunID,
				original.Status.Revision,
				original.Diagnostic.Ref,
			)
			if err != nil {
				t.Fatal(err)
			}
			if reconciled.Outcome != test.wantOutcome {
				t.Fatalf(
					"reconciliation outcome = %q, want %q",
					reconciled.Outcome,
					test.wantOutcome,
				)
			}
			stateAfter, statusAfter := fixture.currentStateAndStatus(
				t,
				factory,
			)
			if !reflect.DeepEqual(reconciled.Status, statusAfter) ||
				fixture.casCalls() != callsBefore {
				t.Fatalf(
					"reconciliation state/effect = %#v / CAS %d -> %d",
					reconciled,
					callsBefore,
					fixture.casCalls(),
				)
			}
			remoteAfter := fixture.remoteRevision(t)
			want, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			for retry := 1; retry <= 2; retry++ {
				replayed, err := fixture.runtime.AdvanceOutcome(
					context.Background(),
					fixture.workRunID,
					fixture.start.Revision,
				)
				if err != nil {
					t.Fatalf("Advance retry %d: %v", retry, err)
				}
				got, err := json.Marshal(replayed)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf(
						"Advance retry %d differs:\nwant %s\ngot  %s",
						retry,
						want,
						got,
					)
				}
				state, status := fixture.currentStateAndStatus(
					t,
					factory,
				)
				if !reflect.DeepEqual(state, stateAfter) ||
					!reflect.DeepEqual(status, statusAfter) ||
					fixture.casCalls() != callsBefore ||
					fixture.remoteRevision(t) != remoteAfter {
					t.Fatalf(
						"Advance retry %d mutated state/effect: state=%#v status=%#v CAS=%d",
						retry,
						state,
						status,
						fixture.casCalls(),
					)
				}
			}
			frozen, err := os.ReadFile(resultPath)
			if err != nil {
				t.Fatalf(
					"Reconcile did not restore historical result: %v",
					err,
				)
			}
			wantFile := append(append([]byte{}, want...), '\n')
			if !bytes.Equal(frozen, wantFile) {
				t.Fatalf(
					"frozen historical result differs:\nwant %s\ngot  %s",
					wantFile,
					frozen,
				)
			}
		})
	}
}

func TestProductiveAdvanceRestoresReconciledHistoryBeforePublicStatus(
	t *testing.T,
) {
	outcomes := []struct {
		name        string
		wantOutcome workrun.WorkReconcileOutcome
		setup       func(*testing.T, string) (
			productiveAdvanceTestFixture,
			workrun.WorkAdvanceV1,
		)
	}{
		{
			name:        "manual",
			wantOutcome: workrun.WorkReconcileManualResolution,
			setup: func(
				t *testing.T,
				seed string,
			) (productiveAdvanceTestFixture, workrun.WorkAdvanceV1) {
				fixture := newProductiveAdvanceTestFixture(
					t,
					seed,
					productiveAdvanceTestCASIndeterminate,
					false,
				)
				advance, err := fixture.runtime.AdvanceOutcome(
					context.Background(),
					fixture.workRunID,
					fixture.start.Revision,
				)
				if err != nil {
					t.Fatal(err)
				}
				return fixture, advance
			},
		},
		{
			name:        "no delivery",
			wantOutcome: workrun.WorkReconcileNoDeliveryConfirmed,
			setup: func(
				t *testing.T,
				seed string,
			) (productiveAdvanceTestFixture, workrun.WorkAdvanceV1) {
				fixture := newProductiveAdvanceTestFixture(
					t,
					seed,
					productiveAdvanceTestCASSucceed,
					false,
				)
				return fixture,
					blockProductiveReconciliationBeforeEffect(t, fixture)
			},
		},
		{
			name:        "delivery confirmed",
			wantOutcome: workrun.WorkReconcileDeliveryConfirmed,
			setup: func(
				t *testing.T,
				seed string,
			) (productiveAdvanceTestFixture, workrun.WorkAdvanceV1) {
				fixture := newProductiveAdvanceTestFixture(
					t,
					seed,
					productiveAdvanceTestCASSucceed,
					true,
				)
				return fixture,
					blockProductiveReconciliationAfterDurableSuccess(
						t,
						fixture,
					)
			},
		},
	}
	modes := []struct {
		name string
		mode ActivationMode
	}{
		{name: "advertised enabled", mode: ActivationEnabled},
		{name: "dormant disabled", mode: ActivationDisabled},
	}
	for _, outcome := range outcomes {
		for _, mode := range modes {
			t.Run(outcome.name+"/"+mode.name, func(t *testing.T) {
				seed := "post-reconcile-history-" +
					strings.ReplaceAll(outcome.name, " ", "-") + "-" +
					strings.ReplaceAll(mode.name, " ", "-")
				fixture, original := outcome.setup(t, seed)
				if original.Diagnostic == nil ||
					original.Diagnostic.NextAction !=
						workrun.WorkAdvanceNextActionReconcile {
					t.Fatalf(
						"historical reconciliation source = %#v",
						original,
					)
				}
				reconciled, err := fixture.runtime.ReconcileOutcome(
					context.Background(),
					fixture.workRunID,
					original.Status.Revision,
					original.Diagnostic.Ref,
				)
				if err != nil {
					t.Fatal(err)
				}
				if reconciled.Outcome != outcome.wantOutcome {
					t.Fatalf(
						"reconciliation outcome = %q, want %q",
						reconciled.Outcome,
						outcome.wantOutcome,
					)
				}

				store, factory := fixture.existingStore(t)
				stateBefore, statusBefore :=
					fixture.currentStateAndStatus(t, factory)
				callsBefore := fixture.casCalls()
				remoteBefore := fixture.remoteRevision(t)
				resultPath := filepath.Join(
					store.root,
					store.resultName(fixture.start.Revision),
				)
				if err := os.Remove(resultPath); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(resultPath); !errors.Is(
					err,
					os.ErrNotExist,
				) {
					t.Fatalf("historical cache still exists: %v", err)
				}
				providerEntriesBefore := productiveProviderEntries(
					t,
					store.root,
					filepath.Base(resultPath),
				)

				restoreVerification := hideProductiveVerificationResult(
					t,
					factory,
					stateBefore.VerificationResultRef,
				)
				fixture.runtime.activation = StaticActivationResolver{
					Mode: mode.mode,
				}
				want, err := json.Marshal(original)
				if err != nil {
					t.Fatal(err)
				}
				for retry := 1; retry <= 2; retry++ {
					replayed, err := fixture.runtime.AdvanceOutcome(
						context.Background(),
						fixture.workRunID,
						fixture.start.Revision,
					)
					if err != nil {
						t.Fatalf(
							"historical retry %d: %v",
							retry,
							err,
						)
					}
					got, err := json.Marshal(replayed)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, want) {
						t.Fatalf(
							"historical retry %d differs:\nwant %s\ngot  %s",
							retry,
							want,
							got,
						)
					}
					workStore, err := workrun.OpenWorkRunStore(
						context.Background(),
						fixture.repo,
						fixture.workRunID,
					)
					if err != nil {
						t.Fatal(err)
					}
					state, err := workStore.Status()
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(state, stateBefore) ||
						fixture.casCalls() != callsBefore ||
						fixture.remoteRevision(t) != remoteBefore {
						t.Fatalf(
							"historical retry %d mutated state/effect: state=%#v CAS=%d remote=%q",
							retry,
							state,
							fixture.casCalls(),
							fixture.remoteRevision(t),
						)
					}
				}
				frozen, err := os.ReadFile(resultPath)
				if err != nil {
					t.Fatalf("historical cache was not restored: %v", err)
				}
				wantFile := append(append([]byte{}, want...), '\n')
				if !bytes.Equal(frozen, wantFile) {
					t.Fatalf(
						"restored historical cache differs:\nwant %s\ngot  %s",
						wantFile,
						frozen,
					)
				}
				providerEntriesAfter := productiveProviderEntries(
					t,
					store.root,
					filepath.Base(resultPath),
				)
				if !reflect.DeepEqual(
					providerEntriesAfter,
					providerEntriesBefore,
				) {
					t.Fatalf(
						"historical retries published unrelated provider records:\nbefore %v\nafter  %v",
						providerEntriesBefore,
						providerEntriesAfter,
					)
				}

				restoreVerification()
				stateAfter, statusAfter :=
					fixture.currentStateAndStatus(t, factory)
				if !reflect.DeepEqual(stateAfter, stateBefore) ||
					!reflect.DeepEqual(statusAfter, statusBefore) ||
					fixture.casCalls() != callsBefore ||
					fixture.remoteRevision(t) != remoteBefore {
					t.Fatalf(
						"historical retries changed terminal projection/effect:\nstate %#v -> %#v\nstatus %#v -> %#v\nCAS %d -> %d\nremote %q -> %q",
						stateBefore,
						stateAfter,
						statusBefore,
						statusAfter,
						callsBefore,
						fixture.casCalls(),
						remoteBefore,
						fixture.remoteRevision(t),
					)
				}
			})
		}
	}
}

func TestHistoricalAdvanceRejectsCoherentRouteRewriteAfterReconciliation(
	t *testing.T,
) {
	fixture, original, _ := reconcileManualFixture(
		t,
		"historical-route-rewrite",
	)
	store, factory := fixture.existingStore(t)
	beforeState, beforeStatus := fixture.currentStateAndStatus(t, factory)
	callsBefore := fixture.casCalls()
	remoteBefore := fixture.remoteRevision(t)
	resultPath := filepath.Join(
		store.root,
		store.resultName(fixture.start.Revision),
	)
	payload, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var rewritten workrun.WorkAdvanceV1
	if err := json.Unmarshal(payload, &rewritten); err != nil {
		t.Fatal(err)
	}
	if rewritten.Status.RouteDecision != workrun.RouteDecisionDirectInline ||
		rewritten.Status.ImplementationRoute !=
			workrun.ImplementationRouteDirectInline {
		t.Fatalf("test requires a direct cached route: %#v", rewritten.Status)
	}
	rewritten.Status.RouteDecision = workrun.RouteDecisionDelegatedDirect
	rewritten.Status.ImplementationRoute =
		workrun.ImplementationRouteDelegatedDirect
	if err := rewritten.Validate(); err != nil {
		t.Fatalf("coherent direct-to-delegated rewrite is not valid: %v", err)
	}
	rewrittenPayload, err := json.Marshal(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	rewrittenPayload = append(rewrittenPayload, '\n')
	if err := os.WriteFile(resultPath, rewrittenPayload, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.runtime.AdvanceOutcome(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
	); err == nil {
		t.Fatal("coherent historical route rewrite was replayed")
	}
	afterState, afterStatus := fixture.currentStateAndStatus(t, factory)
	if !reflect.DeepEqual(afterState, beforeState) ||
		!reflect.DeepEqual(afterStatus, beforeStatus) ||
		fixture.casCalls() != callsBefore ||
		fixture.remoteRevision(t) != remoteBefore {
		t.Fatalf(
			"historical route rewrite mutated state/effect:\noriginal=%#v\nbefore=%#v / %#v\nafter=%#v / %#v\nCAS=%d->%d",
			original,
			beforeState,
			beforeStatus,
			afterState,
			afterStatus,
			callsBefore,
			fixture.casCalls(),
		)
	}
}

func TestProductiveReconcileRejectsCorruptPADTerminalWithoutMutationOrEffect(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"reconcile-corrupt-pad-terminal",
		productiveAdvanceTestCASSucceed,
		true,
	)
	advance := blockProductiveReconciliationAfterDurableSuccess(t, fixture)
	store, factory := fixture.existingStore(t)
	beforeState, beforeStatus := fixture.currentStateAndStatus(t, factory)
	callsBefore := fixture.casCalls()

	padStore, err := existingPADDeliveryResultStore(
		context.Background(),
		factory.padAuthority,
	)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := os.ReadDir(filepath.Join(padStore.root, "terminal-bindings"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].IsDir() {
		t.Fatalf("terminal binding entries = %#v", bindings)
	}
	bindingPayload, err := os.ReadFile(
		filepath.Join(padStore.root, "terminal-bindings", bindings[0].Name()),
	)
	if err != nil {
		t.Fatal(err)
	}
	var binding padDeliveryTerminalBinding
	if err := decodeStrictCoordinationJSON(bindingPayload, &binding); err != nil {
		t.Fatal(err)
	}
	rewritten := coherentlyRewritePADTerminal(
		t,
		padStore,
		binding,
		deliveryadmission.ExecutionFailed,
	)
	coherentlyRewriteAuthorizationUseTerminal(
		t,
		factory,
		beforeState.DeliveryAuthorizationRef,
		rewritten,
	)

	if _, err := fixture.runtime.ReconcileOutcome(
		context.Background(),
		fixture.workRunID,
		advance.Status.Revision,
		advance.Diagnostic.Ref,
	); !errors.Is(err, deliveryadmission.ErrExecutionResultCorrupt) ||
		!strings.Contains(err.Error(), "differs from WorkRun authority") {
		t.Fatalf("corrupt PAD reconciliation = %v", err)
	}
	afterState, afterStatus := fixture.currentStateAndStatus(t, factory)
	if !reflect.DeepEqual(afterState, beforeState) ||
		!reflect.DeepEqual(afterStatus, beforeStatus) {
		t.Fatalf(
			"corrupt reconciliation mutated WorkRun/status:\nbefore %#v / %#v\nafter  %#v / %#v",
			beforeState,
			beforeStatus,
			afterState,
			afterStatus,
		)
	}
	if afterStatus.PublicState == workrun.PublicStateReady ||
		afterState.ProductiveReconciliationRef != "" ||
		fixture.casCalls() != callsBefore {
		t.Fatalf(
			"corrupt reconciliation escaped fence: state=%#v status=%#v CAS=%d->%d",
			afterState,
			afterStatus,
			callsBefore,
			fixture.casCalls(),
		)
	}
	if _, ok, err := store.reconcileResult(
		context.Background(),
		advance.Status.Revision,
		advance.Diagnostic.Ref,
		afterState,
		afterStatus,
	); err != nil || ok {
		t.Fatalf("corrupt reconciliation published a public result: %t, %v", ok, err)
	}
}

func TestProductiveRecoveryRejectsCoherentPADFailedToSucceededRewrite(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"recover-coherent-failed-success",
		productiveAdvanceTestCASFailed,
		false,
	)
	first, err := fixture.runtime.AdvanceOutcome(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Diagnostic == nil ||
		first.Diagnostic.Code != workrun.WorkAdvanceDiagnosticDeliveryEffectFailed {
		t.Fatalf("failed source result = %#v", first)
	}
	_, factory := fixture.existingStore(t)
	beforeState, beforeStatus := fixture.currentStateAndStatus(t, factory)
	callsBefore := fixture.casCalls()
	padStore, err := existingPADDeliveryResultStore(
		context.Background(),
		factory.padAuthority,
	)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(padStore.root, "terminal-bindings"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].IsDir() {
		t.Fatalf("terminal binding entries = %#v", entries)
	}
	payload, err := os.ReadFile(
		filepath.Join(padStore.root, "terminal-bindings", entries[0].Name()),
	)
	if err != nil {
		t.Fatal(err)
	}
	var binding padDeliveryTerminalBinding
	if err := decodeStrictCoordinationJSON(payload, &binding); err != nil {
		t.Fatal(err)
	}
	rewritten := coherentlyRewritePADTerminal(
		t,
		padStore,
		binding,
		deliveryadmission.ExecutionSucceeded,
	)
	coherentlyRewriteAuthorizationUseTerminal(
		t,
		factory,
		beforeState.DeliveryAuthorizationRef,
		rewritten,
	)

	coordinator, err := factory.openForProductiveRecovery(
		context.Background(),
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := coordinator.RecoverBoundDelivery(
		context.Background(),
	); !errors.Is(err, deliveryadmission.ErrExecutionResultCorrupt) ||
		!strings.Contains(err.Error(), "differs from WorkRun authority") {
		t.Fatalf("coherent failed->succeeded recovery = %v", err)
	}
	afterState, afterStatus := fixture.currentStateAndStatus(t, factory)
	if !reflect.DeepEqual(afterState, beforeState) ||
		!reflect.DeepEqual(afterStatus, beforeStatus) ||
		afterStatus.PublicState == workrun.PublicStateReady ||
		fixture.casCalls() != callsBefore {
		t.Fatalf(
			"coherent rewrite escaped terminal fence: state %#v -> %#v status %#v -> %#v CAS %d -> %d",
			beforeState,
			afterState,
			beforeStatus,
			afterStatus,
			callsBefore,
			fixture.casCalls(),
		)
	}
}

func TestProductiveReconcileRejectsStaleOrForeignTerminalCASBeforeRecovery(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"reconcile-cas-rejection",
		productiveAdvanceTestCASIndeterminate,
		false,
	)
	advance, err := fixture.runtime.AdvanceOutcome(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	callsBefore := fixture.casCalls()
	tests := []struct {
		name               string
		expectedRevision   string
		diagnosticRef      string
		expectedErrorMatch error
	}{
		{
			name:               "stale revision",
			expectedRevision:   fixture.start.Revision,
			diagnosticRef:      advance.Diagnostic.Ref,
			expectedErrorMatch: workrun.ErrWorkRunRevisionConflict,
		},
		{
			name:             "foreign diagnostic",
			expectedRevision: advance.Status.Revision,
			diagnosticRef: productiveAdvanceSHA256(
				[]byte("foreign-productive-reconcile-diagnostic"),
			),
			expectedErrorMatch: workrun.ErrWorkRunInvalidTransition,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.runtime.ReconcileOutcome(
				context.Background(),
				fixture.workRunID,
				test.expectedRevision,
				test.diagnosticRef,
			)
			if !errors.Is(err, test.expectedErrorMatch) {
				t.Fatalf("reconciliation CAS error = %v", err)
			}
			if fixture.casCalls() != callsBefore {
				t.Fatalf(
					"rejected reconciliation changed effects: %d -> %d",
					callsBefore,
					fixture.casCalls(),
				)
			}
		})
	}
	store, factory := fixture.existingStore(t)
	state, _ := fixture.currentStateAndStatus(t, factory)
	if state.ProductiveReconciliationRef != "" ||
		state.ProductiveReconciliationSourceRevision != "" ||
		state.ProductiveReconciliationOutcome != "" {
		t.Fatalf("rejected reconciliation mutated WorkRun: %#v", state)
	}
	if _, err := store.resolveDiagnosticForState(
		context.Background(),
		advance.Diagnostic.Ref,
		state,
	); err != nil {
		t.Fatalf("original terminal authority changed: %v", err)
	}
}

func TestProductiveReconcileRejectsTamperedAndCrossRunArtifacts(
	t *testing.T,
) {
	for _, target := range []string{
		"reconciliation authority",
		"public result",
	} {
		t.Run(target, func(t *testing.T) {
			fixture, advance, reconciled := reconcileManualFixture(
				t,
				"reconcile-tamper-"+target[:6],
			)
			store, _ := fixture.existingStore(t)
			path := filepath.Join(
				store.root,
				store.reconcileResultName(advance.Status.Revision),
			)
			if target == "reconciliation authority" {
				path = filepath.Join(
					store.root,
					"reconciliation-"+
						productiveAdvanceRevisionKey(
							reconciled.ReconciliationRef,
						)+
						".json",
				)
			}
			tamperProductiveReconciliationFile(t, path)
			if _, err := fixture.runtime.ReconcileOutcome(
				context.Background(),
				fixture.workRunID,
				advance.Status.Revision,
				advance.Diagnostic.Ref,
			); err == nil {
				t.Fatalf("tampered %s produced a replay", target)
			}
			if fixture.casCalls() != 1 {
				t.Fatalf(
					"tampered %s retried effect: CAS %d",
					target,
					fixture.casCalls(),
				)
			}
		})
	}

	t.Run("reconciliation authority cannot cross WorkRuns", func(t *testing.T) {
		fixture, _, reconciled := reconcileManualFixture(
			t,
			"reconcile-cross-run",
		)
		store, factory := fixture.existingStore(t)
		source := filepath.Join(
			store.root,
			"reconciliation-"+
				productiveAdvanceRevisionKey(reconciled.ReconciliationRef)+
				".json",
		)
		payload, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := openProductiveAdvanceStore(
			context.Background(),
			factory.lease,
			"productive-reconcile-foreign-run",
		)
		if err != nil {
			t.Fatal(err)
		}
		foreign.pad = factory.pad
		target := filepath.Join(
			foreign.root,
			"reconciliation-"+
				productiveAdvanceRevisionKey(reconciled.ReconciliationRef)+
				".json",
		)
		if err := os.WriteFile(target, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := foreign.ResolveProductiveReconciliation(
			context.Background(),
			reconciled.ReconciliationRef,
		); err == nil {
			t.Fatal("cross-WorkRun reconciliation authority was accepted")
		}
	})
}

func TestProductiveReconcileUnknownWorkRunCreatesNoProviderStorage(
	t *testing.T,
) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	authority, err := NewPADRepositoryAuthority(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &productiveRuntimeOutcome{
		repo:          repo,
		repositoryRef: authority.RepositoryRef(),
		agent:         model.AgentCodex,
		activation: StaticActivationResolver{
			Mode: ActivationDisabled,
		},
	}
	root := defaultProviderRoot(t, repo)
	if _, statErr := os.Lstat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("fresh runtime already has provider storage: %v", statErr)
	}
	_, err = runtime.ReconcileOutcome(
		ctx,
		"productive-reconcile-unknown",
		productiveAdvanceSHA256([]byte("unknown-reconcile-revision")),
		productiveAdvanceSHA256([]byte("unknown-reconcile-diagnostic")),
	)
	if err == nil {
		t.Fatal("unknown WorkRun reconciliation unexpectedly succeeded")
	}
	if _, statErr := os.Lstat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf(
			"unknown WorkRun reconciliation created provider storage: %v",
			statErr,
		)
	}
}

type productiveReconcileDisableAfterAuthorization struct {
	store workrun.WorkRunStore
}

func (resolver productiveReconcileDisableAfterAuthorization) ResolveActivation(
	ctx context.Context,
	_ string,
) (ActivationMode, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	state, err := resolver.store.Status()
	if err != nil {
		return "", err
	}
	if state.DeliveryAuthorizationRef != "" {
		return ActivationDisabled, nil
	}
	return ActivationEnabled, nil
}

type productiveReconcileDisableAfterExecutionAnchor struct {
	store workrun.WorkRunStore
}

func (resolver productiveReconcileDisableAfterExecutionAnchor) ResolveActivation(
	ctx context.Context,
	_ string,
) (ActivationMode, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	state, err := resolver.store.Status()
	if err != nil {
		return "", err
	}
	if state.ProductiveExecutionResultRef != "" &&
		state.DeliveryResultRef == "" {
		return ActivationDisabled, nil
	}
	return ActivationEnabled, nil
}

func blockProductiveReconciliationAfterDurableSuccess(
	t *testing.T,
	fixture productiveAdvanceTestFixture,
) workrun.WorkAdvanceV1 {
	t.Helper()
	ctx := context.Background()
	workStore, err := workrun.OpenWorkRunStore(
		ctx,
		fixture.repo,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime.activation =
		productiveReconcileDisableAfterExecutionAnchor{store: workStore}
	if _, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	); !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("post-effect stop = %v", err)
	}
	if fixture.casCalls() != 1 ||
		fixture.remoteRevision(t) != fixture.connector.candidateRevision {
		t.Fatalf(
			"durable delivery was not reached: CAS %d remote %q",
			fixture.casCalls(),
			fixture.remoteRevision(t),
		)
	}
	store, factory := fixture.existingStore(t)
	coordinator, err := factory.openForProductiveRecovery(
		ctx,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.DeliveryAuthorizationRef == "" ||
		state.ProductiveExecutionResultRef == "" ||
		state.DeliveryResultRef != "" ||
		state.ProductiveBlockerRef != "" {
		t.Fatalf("post-effect recovery source = %#v", state)
	}
	advance, err := blockRecoveredProductiveAdvance(
		ctx,
		store,
		coordinator,
		fixture.start.Revision,
		state,
		workrun.WorkAdvanceDiagnosticDeliveryOutcomeIndeterminate,
		workrun.WorkAdvanceNextActionReconcile,
	)
	if err != nil {
		t.Fatal(err)
	}
	return advance
}

func blockProductiveReconciliationBeforeEffect(
	t *testing.T,
	fixture productiveAdvanceTestFixture,
) workrun.WorkAdvanceV1 {
	t.Helper()
	ctx := context.Background()
	store, err := workrun.OpenWorkRunStore(
		ctx,
		fixture.repo,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime.activation =
		productiveReconcileDisableAfterAuthorization{store: store}
	if _, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	); !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("pre-effect stop = %v", err)
	}
	if fixture.casCalls() != 0 ||
		fixture.remoteRevision(t) != fixture.base {
		t.Fatalf(
			"pre-effect stop reached delivery: CAS %d remote %q",
			fixture.casCalls(),
			fixture.remoteRevision(t),
		)
	}
	advance, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if advance.Diagnostic == nil ||
		advance.Diagnostic.NextAction != workrun.WorkAdvanceNextActionReconcile {
		t.Fatalf("pre-effect recovery did not bind reconciliation: %#v", advance)
	}
	return advance
}

func reconcileManualFixture(
	t *testing.T,
	seed string,
) (
	productiveAdvanceTestFixture,
	workrun.WorkAdvanceV1,
	workrun.WorkReconcileV1,
) {
	t.Helper()
	fixture := newProductiveAdvanceTestFixture(
		t,
		seed,
		productiveAdvanceTestCASIndeterminate,
		false,
	)
	advance, err := fixture.runtime.AdvanceOutcome(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := fixture.runtime.ReconcileOutcome(
		context.Background(),
		fixture.workRunID,
		advance.Status.Revision,
		advance.Diagnostic.Ref,
	)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, advance, reconciled
}

func hideProductiveVerificationResult(
	t *testing.T,
	factory *ProductionOwnerCoordinatorFactory,
	resultRef string,
) func() {
	t.Helper()
	if factory == nil || factory.lease == nil ||
		!validPADImmutableRef(resultRef) {
		t.Fatal("verification result hiding requires exact owner authority")
	}
	path := filepath.Join(
		factory.lease.Identity().GitCommonDir,
		"gentle-ai",
		"review-transactions",
		"rar-authority",
		"v1",
		"by-result",
		strings.TrimPrefix(resultRef, "sha256:")+".json",
	)
	hidden := path + ".historical-preflight-hidden"
	if err := os.Rename(path, hidden); err != nil {
		t.Fatalf("hide live verification authority: %v", err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			_ = os.Rename(hidden, path)
		}
	})
	return func() {
		t.Helper()
		if restored {
			return
		}
		if err := os.Rename(hidden, path); err != nil {
			t.Fatalf("restore live verification authority: %v", err)
		}
		restored = true
	}
}

func productiveProviderEntries(
	t *testing.T,
	root string,
	ignored string,
) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() != ignored {
			names = append(names, entry.Name())
		}
	}
	return names
}

func tamperProductiveReconciliationFile(t *testing.T, path string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)/2] ^= 1
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func coherentlyRewritePADTerminal(
	t *testing.T,
	store *padDeliveryResultStore,
	binding padDeliveryTerminalBinding,
	target deliveryadmission.ExecutionOutcome,
) deliveryadmission.ExecutionResult {
	t.Helper()
	claimPayload, err := os.ReadFile(store.claimPath(binding.CommandRef))
	if err != nil {
		t.Fatal(err)
	}
	var claim padDeliveryClaim
	if err := decodeStrictCoordinationJSON(claimPayload, &claim); err != nil {
		t.Fatal(err)
	}
	// Rewrite the fixed claim too, so the regression covers a coherent
	// same-owner rewire rather than an isolated content-address mismatch.
	claim.ClaimedAt--
	if claim.ClaimedAt <= 0 {
		t.Fatal("test claim cannot be safely rewritten")
	}
	rewrittenClaimPayload, err := canonicalCoordinationPayload(claim)
	if err != nil {
		t.Fatal(err)
	}
	claimRef := padDeliveryContentRef(rewrittenClaimPayload)

	evidencePayload, err := os.ReadFile(
		store.terminalEvidencePath(binding.EvidenceRef),
	)
	if err != nil {
		t.Fatal(err)
	}
	var evidence padDeliveryTerminalEvidence
	if err := decodeStrictCoordinationJSON(evidencePayload, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.BranchCASReceipt == nil ||
		evidence.PullRequestMergeReceipt != nil {
		t.Fatalf("test requires direct terminal evidence: %#v", evidence)
	}
	receipt := *evidence.BranchCASReceipt
	switch target {
	case deliveryadmission.ExecutionSucceeded:
		receipt.Outcome = HostingEffectApplied
		receipt.RemoteRevision = receipt.Request.CandidateRevision
		receipt.DeliveryRef = receipt.Request.CandidateRevision
	case deliveryadmission.ExecutionFailed:
		receipt.Outcome = HostingEffectRejected
		receipt.RemoteRevision = receipt.Request.ExpectedRevision
		receipt.DeliveryRef = ""
	default:
		t.Fatalf("unsupported coherent rewrite target %q", target)
	}
	evidence.ClaimRef = claimRef
	evidence.BranchCASReceipt = &receipt
	rewrittenEvidencePayload, err := canonicalCoordinationPayload(evidence)
	if err != nil {
		t.Fatal(err)
	}
	evidenceRef := padDeliveryContentRef(rewrittenEvidencePayload)

	resultPayload, err := os.ReadFile(
		store.terminalResultPath(binding.ResultRef),
	)
	if err != nil {
		t.Fatal(err)
	}
	var result deliveryadmission.ExecutionResult
	if err := decodeStrictCoordinationJSON(resultPayload, &result); err != nil {
		t.Fatal(err)
	}
	result.Outcome = target
	result.EvidenceRef = evidenceRef
	if target == deliveryadmission.ExecutionSucceeded {
		result.DeliveryRef = receipt.DeliveryRef
	} else {
		result.DeliveryRef = ""
	}
	resultRef, err := padDeliveryCanonicalValueRef(result)
	if err != nil {
		t.Fatal(err)
	}

	rewrittenBinding := binding
	rewrittenBinding.ClaimRef = claimRef
	rewrittenBinding.EvidenceRef = evidenceRef
	rewrittenBinding.ResultRef = resultRef
	writePADCanonicalTestValue(t, store.claimPath(binding.CommandRef), claim)
	writePADCanonicalTestValue(
		t,
		store.terminalEvidencePath(evidenceRef),
		evidence,
	)
	writePADCanonicalTestValue(
		t,
		store.terminalResultPath(resultRef),
		result,
	)
	writePADCanonicalTestValue(
		t,
		store.terminalBindingPath(binding.CommandRef),
		rewrittenBinding,
	)
	return result
}

type rewrittenExecutionResultAnchor struct {
	Schema           string                            `json:"schema"`
	AuthorizationRef string                            `json:"authorizationRef"`
	CommandRef       string                            `json:"commandRef"`
	RepositoryRef    string                            `json:"repositoryRef"`
	ResultRef        string                            `json:"resultRef"`
	Result           deliveryadmission.ExecutionResult `json:"result"`
}

func coherentlyRewriteAuthorizationUseTerminal(
	t *testing.T,
	factory *ProductionOwnerCoordinatorFactory,
	authorizationRef string,
	result deliveryadmission.ExecutionResult,
) {
	t.Helper()
	identity := factory.lease.Identity()
	path := filepath.Join(
		identity.GitCommonDir,
		"gentle-ai",
		"delivery-authorization-uses",
		"v1",
		"repositories",
		factory.lease.StorageKey(),
		strings.TrimPrefix(authorizationRef, "sha256:")+"-terminal.json",
	)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var anchor rewrittenExecutionResultAnchor
	if err := json.Unmarshal(payload, &anchor); err != nil {
		t.Fatal(err)
	}
	resultRef, err := padDeliveryCanonicalValueRef(result)
	if err != nil {
		t.Fatal(err)
	}
	if anchor.AuthorizationRef != authorizationRef ||
		anchor.CommandRef != result.CommandRef ||
		anchor.RepositoryRef != factory.RepositoryRef() {
		t.Fatalf("unexpected execution-result anchor binding: %#v", anchor)
	}
	anchor.ResultRef = resultRef
	anchor.Result = result
	rewritten, err := json.Marshal(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatal(err)
	}
}
