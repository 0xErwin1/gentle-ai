package workprovider

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
)

func TestPADDeliveryTerminalRejectsFailedToSucceededRewrite(t *testing.T) {
	fixture := newPADDeliveryTestFixture(
		t,
		deliveryadmission.RouteDirectMain,
		deliveryadmission.MechanismFastForwardOnly,
	)
	store, commandRef, claim := preparePADTerminalTest(t, fixture)
	failed := branchPADTerminalEvidence(
		t,
		fixture,
		commandRef,
		claim,
		HostingEffectRejected,
		fixture.base,
		"",
	)
	result, err := store.publishTerminal(fixture.command, failed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != deliveryadmission.ExecutionFailed {
		t.Fatalf("terminal result = %#v", result)
	}
	binding := readPADTerminalBinding(t, store, commandRef)
	resultPath := store.terminalResultPath(binding.ResultRef)
	payload, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var rewritten deliveryadmission.ExecutionResult
	if err := decodeStrictCoordinationJSON(payload, &rewritten); err != nil {
		t.Fatal(err)
	}
	rewritten.Outcome = deliveryadmission.ExecutionSucceeded
	rewritten.DeliveryRef = fixture.candidate
	if err := rewritten.Validate(fixture.command); err != nil {
		t.Fatalf("rewrite must remain structurally canonical: %v", err)
	}
	writePADCanonicalTestValue(t, resultPath, rewritten)

	if _, _, err := fixture.adapter.ResolveOnce(
		context.Background(),
		fixture.command,
	); !errors.Is(err, ErrPADDeliveryResultCorrupt) {
		t.Fatalf("semantic failed->succeeded rewrite = %v", err)
	}
	if fixture.hosting.casCalls != 0 {
		t.Fatalf("corrupt replay reached an effect: %d", fixture.hosting.casCalls)
	}
}

func TestPADDeliveryTerminalRejectsLegacyIndeterminateToFailedRewrite(
	t *testing.T,
) {
	fixture := newPADDeliveryTestFixture(
		t,
		deliveryadmission.RouteDirectMain,
		deliveryadmission.MechanismFastForwardOnly,
	)
	store, err := fixture.adapter.ensureStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	commandRef, err := fixture.command.Ref()
	if err != nil {
		t.Fatal(err)
	}
	legacy := deliveryadmission.ExecutionResult{
		Schema:           deliveryadmission.ExecutionResultContractV1,
		CommandRef:       commandRef,
		AuthorizationRef: fixture.command.AuthorizationRef,
		Route:            fixture.command.Route,
		Candidate:        fixture.command.Candidate,
		Outcome:          deliveryadmission.ExecutionIndeterminate,
		EvidenceRef:      padDeliveryTestRef("unanchored-legacy-evidence"),
		CompletedAt:      fixture.clock.Now().Unix(),
	}
	if err := legacy.Validate(fixture.command); err != nil {
		t.Fatal(err)
	}
	path := store.legacyResultPath(commandRef)
	writePADCanonicalTestValue(t, path, legacy)
	if _, _, err := fixture.adapter.ResolveOnce(
		context.Background(),
		fixture.command,
	); !errors.Is(err, ErrPADDeliveryResultCorrupt) {
		t.Fatalf("unanchored legacy indeterminate = %v", err)
	}

	legacy.Outcome = deliveryadmission.ExecutionFailed
	writePADCanonicalTestValue(t, path, legacy)
	if _, _, err := fixture.adapter.ResolveOnce(
		context.Background(),
		fixture.command,
	); !errors.Is(err, ErrPADDeliveryResultCorrupt) {
		t.Fatalf("legacy indeterminate->failed rewrite = %v", err)
	}
	if fixture.hosting.casCalls != 0 || fixture.hosting.observationCalls != 0 {
		t.Fatalf(
			"legacy terminal replay touched hosting: observations=%d effects=%d",
			fixture.hosting.observationCalls,
			fixture.hosting.casCalls,
		)
	}
}

func TestPADDeliveryTerminalFailsClosedOnMissingOrMismatchedAuthority(
	t *testing.T,
) {
	tests := []struct {
		name   string
		tamper func(*testing.T, *padDeliveryResultStore, string, padDeliveryTerminalBinding)
		match  error
	}{
		{
			name: "missing result object",
			tamper: func(
				t *testing.T,
				store *padDeliveryResultStore,
				_ string,
				binding padDeliveryTerminalBinding,
			) {
				t.Helper()
				if err := os.Remove(store.terminalResultPath(binding.ResultRef)); err != nil {
					t.Fatal(err)
				}
			},
			match: ErrPADDeliveryResultCorrupt,
		},
		{
			name: "missing evidence object",
			tamper: func(
				t *testing.T,
				store *padDeliveryResultStore,
				_ string,
				binding padDeliveryTerminalBinding,
			) {
				t.Helper()
				if err := os.Remove(
					store.terminalEvidencePath(binding.EvidenceRef),
				); err != nil {
					t.Fatal(err)
				}
			},
			match: ErrPADDeliveryResultCorrupt,
		},
		{
			name: "missing claim",
			tamper: func(
				t *testing.T,
				store *padDeliveryResultStore,
				commandRef string,
				_ padDeliveryTerminalBinding,
			) {
				t.Helper()
				if err := os.Remove(store.claimPath(commandRef)); err != nil {
					t.Fatal(err)
				}
			},
			match: ErrPADDeliveryResultCorrupt,
		},
		{
			name: "binding points at another result",
			tamper: func(
				t *testing.T,
				store *padDeliveryResultStore,
				commandRef string,
				binding padDeliveryTerminalBinding,
			) {
				t.Helper()
				binding.ResultRef = padDeliveryTestRef("foreign-terminal-result")
				writePADCanonicalTestValue(
					t,
					store.terminalBindingPath(commandRef),
					binding,
				)
			},
			match: ErrPADDeliveryResultCorrupt,
		},
		{
			name: "binding repository mismatch",
			tamper: func(
				t *testing.T,
				store *padDeliveryResultStore,
				commandRef string,
				binding padDeliveryTerminalBinding,
			) {
				t.Helper()
				binding.RepositoryRef = padDeliveryTestRef("foreign-repository")
				writePADCanonicalTestValue(
					t,
					store.terminalBindingPath(commandRef),
					binding,
				)
			},
			match: ErrPADDeliveryResultCorrupt,
		},
		{
			name: "missing single-assignment binding after claim",
			tamper: func(
				t *testing.T,
				store *padDeliveryResultStore,
				commandRef string,
				_ padDeliveryTerminalBinding,
			) {
				t.Helper()
				if err := os.Remove(store.terminalBindingPath(commandRef)); err != nil {
					t.Fatal(err)
				}
			},
			match: ErrPADDeliveryIndeterminate,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPADDeliveryTestFixture(
				t,
				deliveryadmission.RouteDirectMain,
				deliveryadmission.MechanismFastForwardOnly,
			)
			result, err := fixture.adapter.ExecuteOnce(
				context.Background(),
				fixture.command,
			)
			if err != nil {
				t.Fatal(err)
			}
			commandRef, err := fixture.command.Ref()
			if err != nil {
				t.Fatal(err)
			}
			store := fixture.adapter.store
			binding := readPADTerminalBinding(t, store, commandRef)
			if binding.EvidenceRef != result.EvidenceRef {
				t.Fatalf("terminal binding/result = %#v / %#v", binding, result)
			}
			fixture.hosting.mu.Lock()
			observationsBefore := fixture.hosting.observationCalls
			effectsBefore := fixture.hosting.casCalls
			fixture.hosting.mu.Unlock()
			test.tamper(t, store, commandRef, binding)

			if _, _, err := fixture.adapter.ResolveOnce(
				context.Background(),
				fixture.command,
			); !errors.Is(err, test.match) {
				t.Fatalf("tampered terminal authority error = %v", err)
			}
			fixture.hosting.mu.Lock()
			observationsAfter := fixture.hosting.observationCalls
			effectsAfter := fixture.hosting.casCalls
			fixture.hosting.mu.Unlock()
			if observationsAfter != observationsBefore ||
				effectsAfter != effectsBefore {
				t.Fatalf(
					"tampered replay touched hosting: observations %d->%d effects %d->%d",
					observationsBefore,
					observationsAfter,
					effectsBefore,
					effectsAfter,
				)
			}
		})
	}
}

func TestPADDeliveryTerminalBindingHasOneConcurrentWinner(t *testing.T) {
	fixture := newPADDeliveryTestFixture(
		t,
		deliveryadmission.RouteDirectMain,
		deliveryadmission.MechanismFastForwardOnly,
	)
	store, commandRef, claim := preparePADTerminalTest(t, fixture)
	failed := branchPADTerminalEvidence(
		t,
		fixture,
		commandRef,
		claim,
		HostingEffectRejected,
		fixture.base,
		"",
	)
	fixture.clock.SetUnix(fixture.clock.Now().Unix() + 1)
	succeeded := branchPADTerminalEvidence(
		t,
		fixture,
		commandRef,
		claim,
		HostingEffectApplied,
		fixture.candidate,
		fixture.candidate,
	)
	evidences := []padDeliveryTerminalEvidence{failed, succeeded}
	results := make([]deliveryadmission.ExecutionResult, len(evidences))
	errs := make([]error, len(evidences))
	var wait sync.WaitGroup
	for index := range evidences {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index], errs[index] = store.publishTerminal(
				fixture.command,
				evidences[index],
			)
		}()
	}
	wait.Wait()

	successes := 0
	conflicts := 0
	var winner deliveryadmission.ExecutionResult
	for index, err := range errs {
		switch {
		case err == nil:
			successes++
			winner = results[index]
		case errors.Is(err, ErrCoordinationAuthorityConflict):
			conflicts++
		default:
			t.Fatalf("contender %d = %v", index, err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("single-assignment winners/conflicts = %d/%d", successes, conflicts)
	}
	replayed, exists, err := store.readTerminal(fixture.command, commandRef)
	if err != nil || !exists || replayed != winner {
		t.Fatalf("winner replay = %#v, %t, %v; want %#v", replayed, exists, err, winner)
	}
}

func preparePADTerminalTest(
	t *testing.T,
	fixture padDeliveryTestFixture,
) (*padDeliveryResultStore, string, padDeliveryClaim) {
	t.Helper()
	store, err := fixture.adapter.ensureStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	commandRef, err := fixture.command.Ref()
	if err != nil {
		t.Fatal(err)
	}
	claim, err := newPADDeliveryClaim(
		fixture.command,
		commandRef,
		fixture.hosting.binding.HostingRepositoryRef,
		fixture.candidate,
		fixture.base,
		fixture.clock.Now().Unix(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.publishClaim(claim); err != nil {
		t.Fatal(err)
	}
	return store, commandRef, claim
}

func branchPADTerminalEvidence(
	t *testing.T,
	fixture padDeliveryTestFixture,
	commandRef string,
	claim padDeliveryClaim,
	outcome HostingEffectOutcome,
	remoteRevision string,
	deliveryRef string,
) padDeliveryTerminalEvidence {
	t.Helper()
	request := HostingBranchCASRequest{
		CommandRef:           commandRef,
		RepositoryRef:        fixture.command.Destination.RepositoryRef,
		HostingRepositoryRef: claim.HostingRepositoryRef,
		TargetRef:            fixture.command.Destination.TargetRef,
		ExpectedRevision:     claim.ExpectedRevision,
		CandidateRevision:    claim.CandidateRevision,
		ExecutionExpiresAt:   fixture.command.ExecutionExpiresAt,
	}
	receipt := fixture.hosting.branchReceipt(
		request,
		outcome,
		remoteRevision,
		deliveryRef,
	)
	evidence, err := fixture.adapter.newTerminalEvidence(
		fixture.command,
		commandRef,
		claim,
		padDeliveryTerminalEvidenceBranchCAS,
		&receipt,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func readPADTerminalBinding(
	t *testing.T,
	store *padDeliveryResultStore,
	commandRef string,
) padDeliveryTerminalBinding {
	t.Helper()
	payload, err := os.ReadFile(store.terminalBindingPath(commandRef))
	if err != nil {
		t.Fatal(err)
	}
	var binding padDeliveryTerminalBinding
	if err := decodeStrictCoordinationJSON(payload, &binding); err != nil {
		t.Fatal(err)
	}
	return binding
}

func writePADCanonicalTestValue(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := canonicalCoordinationPayload(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}
