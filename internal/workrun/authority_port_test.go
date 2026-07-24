package workrun

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
)

type testAuthorityRepository struct {
	mu sync.Mutex

	intents             map[string]DeliveryIntentAuthority
	routes              map[string]RouteSelectionAuthority
	runs                map[string]SDDRunAuthority
	forecasts           map[string]VerificationForecastAuthority
	dispositions        map[string]VerificationDispositionAuthority
	results             map[string]VerificationResultAuthority
	receipts            map[string]ReviewReceiptAuthority
	authorizations      map[string]DeliveryAuthorizationAuthority
	authorizationErrors map[string]error

	sddBindings []SDDReservationBinding
	sddBindErr  error
}

func newTestAuthorityRepository() *testAuthorityRepository {
	return &testAuthorityRepository{
		intents:             map[string]DeliveryIntentAuthority{},
		routes:              map[string]RouteSelectionAuthority{},
		runs:                map[string]SDDRunAuthority{},
		forecasts:           map[string]VerificationForecastAuthority{},
		dispositions:        map[string]VerificationDispositionAuthority{},
		results:             map[string]VerificationResultAuthority{},
		receipts:            map[string]ReviewReceiptAuthority{},
		authorizations:      map[string]DeliveryAuthorizationAuthority{},
		authorizationErrors: map[string]error{},
		sddBindings:         []SDDReservationBinding{},
	}
}

func (repository *testAuthorityRepository) ResolveLiveDeliveryAuthorization(
	_ context.Context,
	ref string,
) (DeliveryAuthorizationAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if err, ok := repository.authorizationErrors[ref]; ok {
		return DeliveryAuthorizationAuthority{}, err
	}
	value, ok := repository.authorizations[ref]
	if !ok {
		return DeliveryAuthorizationAuthority{}, os.ErrNotExist
	}
	return value, nil
}

func (repository *testAuthorityRepository) ResolveDeliveryIntent(
	_ context.Context,
	ref string,
) (DeliveryIntentAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.intents[ref]
	if !ok {
		return DeliveryIntentAuthority{}, os.ErrNotExist
	}
	return value, nil
}

func (repository *testAuthorityRepository) ResolveRouteSelection(
	_ context.Context,
	ref string,
) (RouteSelectionAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.routes[ref]
	if !ok {
		return RouteSelectionAuthority{}, os.ErrNotExist
	}
	return value, nil
}

func (repository *testAuthorityRepository) ResolveRun(
	_ context.Context,
	ref string,
) (SDDRunAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.runs[ref]
	if !ok {
		return SDDRunAuthority{}, os.ErrNotExist
	}
	return value, nil
}

func (repository *testAuthorityRepository) BindVerificationReservation(
	_ context.Context,
	binding SDDReservationBinding,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.sddBindErr != nil {
		return repository.sddBindErr
	}
	repository.sddBindings = append(repository.sddBindings, binding)
	return nil
}

func (repository *testAuthorityRepository) ResolveForecast(
	_ context.Context,
	ref string,
) (VerificationForecastAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.forecasts[ref]
	if !ok {
		return VerificationForecastAuthority{}, os.ErrNotExist
	}
	value.DiagnosticRefs = cloneStrings(value.DiagnosticRefs)
	return value, nil
}

func (repository *testAuthorityRepository) ResolveDisposition(
	_ context.Context,
	ref string,
) (VerificationDispositionAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.dispositions[ref]
	if !ok {
		return VerificationDispositionAuthority{}, os.ErrNotExist
	}
	return value, nil
}

func (repository *testAuthorityRepository) ResolveResult(
	_ context.Context,
	ref string,
) (VerificationResultAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.results[ref]
	if !ok {
		return VerificationResultAuthority{}, os.ErrNotExist
	}
	return value, nil
}

func (repository *testAuthorityRepository) ResolveReviewReceipt(
	_ context.Context,
	ref string,
) (ReviewReceiptAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.receipts[ref]
	if !ok {
		return ReviewReceiptAuthority{}, os.ErrNotExist
	}
	return value, nil
}

func testAuthorityForStore(t *testing.T, store WorkRunStore) *testAuthorityRepository {
	t.Helper()
	repository, ok := store.authority.PAD.(*testAuthorityRepository)
	if !ok || repository == nil {
		t.Fatal("test store does not contain its owner authority repository")
	}
	return repository
}

func testExecutorForStore(t *testing.T, store WorkRunStore) *hostruntime.Executor {
	t.Helper()
	executor, ok := store.authority.Launch.(*hostruntime.Executor)
	if !ok || executor == nil {
		t.Fatal("test store does not contain its HCR launch authority")
	}
	return executor
}

func registerTestDeliveryIntent(t *testing.T, store WorkRunStore, ref string) {
	t.Helper()
	repository := testAuthorityForStore(t, store)
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.intents[ref] = DeliveryIntentAuthority{IntentRef: ref}
}

func registerTestForecast(
	t *testing.T,
	store WorkRunStore,
	input VerificationForecastInput,
) {
	t.Helper()
	repository := testAuthorityForStore(t, store)
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.forecasts[input.AvailabilityRef] = VerificationForecastAuthority{
		AvailabilityRef: input.AvailabilityRef,
		Applicability:   input.Applicability,
		Registry:        input.Registry,
		Plan:            input.Plan,
		PlanRevisionRef: input.PlanRevisionRef,
		Availability:    input.Availability,
		DiagnosticRefs:  cloneStrings(input.DiagnosticRefs),
	}
}

func registerTestDisposition(
	t *testing.T,
	store WorkRunStore,
	forecast VerificationForecast,
	request RecordVerificationDispositionRequest,
) {
	t.Helper()
	repository := testAuthorityForStore(t, store)
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.dispositions[request.DecisionRef] = VerificationDispositionAuthority{
		DecisionRef: request.DecisionRef, ForecastDigest: forecast.Digest,
		AssumptionsRef: request.AssumptionsRef, Kind: request.Kind,
		ActorRef: request.ActorRef, RunnerRef: request.RunnerRef,
	}
}
