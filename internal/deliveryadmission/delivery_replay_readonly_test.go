package deliveryadmission

import (
	"context"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
)

type replayReadOnlyResultResolver struct {
	result  ExecutionResult
	found   bool
	err     error
	calls   atomic.Int32
	command ExecutionCommand
}

func (resolver *replayReadOnlyResultResolver) ResolveOnce(
	_ context.Context,
	command ExecutionCommand,
) (ExecutionResult, bool, error) {
	resolver.calls.Add(1)
	resolver.command = command
	return resolver.result, resolver.found, resolver.err
}

func TestReplayAuthorizedDeliveryResultNeverProbesReservesOrExecutes(
	t *testing.T,
) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t,
		current.repository,
		authorization.Binding.Destination.RepositoryRef,
	)
	probe := &testLiveGateProbe{now: current.repository.now}
	executor := &testDeliveryExecutor{now: current.repository.now}
	request := ExecuteDeliveryRequest{
		AuthorizationRef:  authorizationRef,
		AuthorizationKind: AuthorizationNormal,
	}
	first, err := ExecuteAuthorizedDelivery(
		context.Background(),
		current.repository,
		current.repository,
		probe,
		executor,
		store,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	useBefore, consumed, err := store.authorizationUse(
		context.Background(),
		authorizationRef,
	)
	if err != nil || !consumed {
		t.Fatalf("initial durable execution reservation = %t, %v", consumed, err)
	}
	entriesBefore, err := os.ReadDir(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	probesBefore := probe.calls.Load()
	executor.mu.Lock()
	callsBefore := executor.calls
	effectsBefore := executor.effects
	command := executor.last
	executor.mu.Unlock()
	var publicationCalls atomic.Int32
	store.beforePublication = func() {
		publicationCalls.Add(1)
	}
	resolver := &replayReadOnlyResultResolver{
		result: first,
		found:  true,
	}

	current.repository.now = authorization.Binding.ExpiresAt + 1
	replayed, found, err := ReplayAuthorizedDeliveryResult(
		context.Background(),
		current.repository,
		current.repository,
		resolver,
		store,
		request,
	)
	if err != nil || !found {
		t.Fatalf("read-only terminal replay = %t, %v", found, err)
	}
	if replayed != first || resolver.command != command ||
		resolver.calls.Load() != 1 {
		t.Fatalf(
			"read-only replay/result calls changed: %#v / %#v / %d",
			replayed,
			resolver.command,
			resolver.calls.Load(),
		)
	}
	if publicationCalls.Load() != 0 {
		t.Fatalf("read-only replay attempted %d use publications", publicationCalls.Load())
	}
	if probe.calls.Load() != probesBefore {
		t.Fatalf("read-only replay probed live gates: %d -> %d", probesBefore, probe.calls.Load())
	}
	executor.mu.Lock()
	callsAfter := executor.calls
	effectsAfter := executor.effects
	executor.mu.Unlock()
	if callsAfter != callsBefore || effectsAfter != effectsBefore {
		t.Fatalf(
			"read-only replay called executor/effect: %d/%d -> %d/%d",
			callsBefore,
			effectsBefore,
			callsAfter,
			effectsAfter,
		)
	}
	useAfter, consumed, err := store.authorizationUse(
		context.Background(),
		authorizationRef,
	)
	if err != nil || !consumed || !reflect.DeepEqual(useAfter, useBefore) {
		t.Fatalf("read-only replay changed durable use: %#v / %t / %v", useAfter, consumed, err)
	}
	entriesAfter, err := os.ReadDir(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entriesAfter, entriesBefore) {
		t.Fatal("read-only replay changed the authorization-use directory")
	}
}

func TestReplayAuthorizedDeliveryResultDoesNotReserveUnusedAuthorization(
	t *testing.T,
) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t,
		current.repository,
		authorization.Binding.Destination.RepositoryRef,
	)
	request := ExecuteDeliveryRequest{
		AuthorizationRef:  authorizationRef,
		AuthorizationKind: AuthorizationNormal,
	}
	resolver := &replayReadOnlyResultResolver{}
	var publicationCalls atomic.Int32
	store.beforePublication = func() {
		publicationCalls.Add(1)
	}

	result, found, err := ReplayAuthorizedDeliveryResult(
		context.Background(),
		current.repository,
		current.repository,
		resolver,
		store,
		request,
	)
	if err != nil || found || result != (ExecutionResult{}) {
		t.Fatalf("unused authorization replay = %#v, %t, %v", result, found, err)
	}
	if resolver.calls.Load() != 0 || publicationCalls.Load() != 0 {
		t.Fatalf(
			"unused replay reached result/publication = %d/%d",
			resolver.calls.Load(),
			publicationCalls.Load(),
		)
	}
	if _, consumed, err := store.authorizationUse(
		context.Background(),
		authorizationRef,
	); err != nil || consumed {
		t.Fatalf("read-only replay reserved unused authorization: %t, %v", consumed, err)
	}
}
