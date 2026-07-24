package workrun

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

type reviewReceiptLookup struct {
	receiptRef string
	resultRef  string
}

type testAuthorityRepository struct {
	mu sync.Mutex

	intents                    map[string]DeliveryIntentAuthority
	deliveryRoutes             map[string]DeliveryRouteReevaluationAuthority
	explicitSDDRequests        map[string]ExplicitSDDRequestAuthority
	routes                     map[string]RouteSelectionAuthority
	runs                       map[string]SDDRunAuthority
	completions                map[string]MutationCompletionAuthority
	forecasts                  map[string]VerificationForecastAuthority
	dispositions               map[string]VerificationDispositionAuthority
	results                    map[string]VerificationResultAuthority
	receipts                   map[reviewReceiptLookup]ReviewReceiptAuthority
	authorizations             map[string]DeliveryAuthorizationAuthority
	authorizationErrors        map[string]error
	productiveDiagnostics      map[string]ProductiveDiagnosticAuthority
	productiveDiagnosticErrors map[string]error
	authorizationResolveCalls  int

	sddBindings []SDDReservationBinding
	sddBindErr  error
}

func newTestAuthorityRepository() *testAuthorityRepository {
	return &testAuthorityRepository{
		intents:                    map[string]DeliveryIntentAuthority{},
		deliveryRoutes:             map[string]DeliveryRouteReevaluationAuthority{},
		explicitSDDRequests:        map[string]ExplicitSDDRequestAuthority{},
		routes:                     map[string]RouteSelectionAuthority{},
		runs:                       map[string]SDDRunAuthority{},
		completions:                map[string]MutationCompletionAuthority{},
		forecasts:                  map[string]VerificationForecastAuthority{},
		dispositions:               map[string]VerificationDispositionAuthority{},
		results:                    map[string]VerificationResultAuthority{},
		receipts:                   map[reviewReceiptLookup]ReviewReceiptAuthority{},
		authorizations:             map[string]DeliveryAuthorizationAuthority{},
		authorizationErrors:        map[string]error{},
		productiveDiagnostics:      map[string]ProductiveDiagnosticAuthority{},
		productiveDiagnosticErrors: map[string]error{},
		sddBindings:                []SDDReservationBinding{},
	}
}

func (repository *testAuthorityRepository) ResolveDeliveryRouteReevaluation(
	_ context.Context,
	ref string,
) (DeliveryRouteReevaluationAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.deliveryRoutes[ref]
	if !ok {
		return DeliveryRouteReevaluationAuthority{}, os.ErrNotExist
	}
	return value, nil
}

func (repository *testAuthorityRepository) ResolveMutationCompletion(
	_ context.Context,
	ref string,
) (MutationCompletionAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.completions[ref]
	if !ok {
		return MutationCompletionAuthority{}, os.ErrNotExist
	}
	value.Snapshot = cloneTestSnapshot(value.Snapshot)
	return value, nil
}

func cloneTestSnapshot(
	value reviewtransaction.Snapshot,
) reviewtransaction.Snapshot {
	value.IntendedUntracked = append([]string{}, value.IntendedUntracked...)
	value.LedgerIDs = append([]string{}, value.LedgerIDs...)
	value.Paths = append([]string{}, value.Paths...)
	return value
}

func (repository *testAuthorityRepository) ResolveLiveDeliveryAuthorization(
	_ context.Context,
	ref string,
) (DeliveryAuthorizationAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.authorizationResolveCalls++
	if err, ok := repository.authorizationErrors[ref]; ok {
		return DeliveryAuthorizationAuthority{}, err
	}
	value, ok := repository.authorizations[ref]
	if !ok {
		return DeliveryAuthorizationAuthority{}, os.ErrNotExist
	}
	return value, nil
}

func (repository *testAuthorityRepository) ResolveProductiveDiagnostic(
	_ context.Context,
	ref string,
) (ProductiveDiagnosticAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if err, ok := repository.productiveDiagnosticErrors[ref]; ok {
		return ProductiveDiagnosticAuthority{}, err
	}
	value, ok := repository.productiveDiagnostics[ref]
	if !ok {
		return ProductiveDiagnosticAuthority{}, os.ErrNotExist
	}
	value.Handoff = cloneHandoff(value.Handoff)
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

func (repository *testAuthorityRepository) ResolveExplicitSDDRequest(
	_ context.Context,
	ref string,
) (ExplicitSDDRequestAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.explicitSDDRequests[ref]
	if !ok {
		return ExplicitSDDRequestAuthority{}, os.ErrNotExist
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
	receiptRef string,
	resultRef string,
) (ReviewReceiptAuthority, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	value, ok := repository.receipts[reviewReceiptLookup{
		receiptRef: receiptRef,
		resultRef:  resultRef,
	}]
	if !ok {
		return ReviewReceiptAuthority{}, os.ErrNotExist
	}
	return value, nil
}

func TestExplicitSDDRequestAuthorityRequiresExactStartBinding(t *testing.T) {
	authorityRef := testSHARef("explicit-sdd-authority")
	deliveryIntentRef := testSHARef("explicit-sdd-delivery")
	authority := ExplicitSDDRequestAuthority{
		AuthorityRef:      authorityRef,
		WorkRunID:         "explicit-run",
		DeliveryIntentRef: deliveryIntentRef,
	}
	tests := []struct {
		name              string
		authorityRef      string
		workRunID         string
		deliveryIntentRef string
		wantErr           bool
	}{
		{
			name:              "exact binding",
			authorityRef:      authorityRef,
			workRunID:         "explicit-run",
			deliveryIntentRef: deliveryIntentRef,
		},
		{
			name:              "different authority",
			authorityRef:      testSHARef("other-explicit-authority"),
			workRunID:         "explicit-run",
			deliveryIntentRef: deliveryIntentRef,
			wantErr:           true,
		},
		{
			name:              "different work run",
			authorityRef:      authorityRef,
			workRunID:         "other-run",
			deliveryIntentRef: deliveryIntentRef,
			wantErr:           true,
		},
		{
			name:              "different delivery intent",
			authorityRef:      authorityRef,
			workRunID:         "explicit-run",
			deliveryIntentRef: testSHARef("other-explicit-delivery"),
			wantErr:           true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := authority.Validate(
				test.authorityRef,
				test.workRunID,
				test.deliveryIntentRef,
			)
			if test.wantErr {
				if !errors.Is(err, ErrAuthorityBindingMismatch) {
					t.Fatalf(
						"Validate error = %v, want authority binding mismatch",
						err,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate exact binding: %v", err)
			}
		})
	}
}

func TestReviewReceiptAuthorityRequiresExactTerminalTuple(t *testing.T) {
	receiptRef := testSHARef("exact-review-receipt")
	candidateRef := testSHARef("exact-review-candidate")
	resultRef := testSHARef("exact-review-result")
	authority := ReviewReceiptAuthority{
		ReceiptRef:            receiptRef,
		CandidateRef:          candidateRef,
		VerificationResultRef: resultRef,
	}

	tests := []struct {
		name         string
		receiptRef   string
		candidateRef string
		resultRef    string
		wantErr      bool
	}{
		{
			name:         "exact tuple",
			receiptRef:   receiptRef,
			candidateRef: candidateRef,
			resultRef:    resultRef,
		},
		{
			name:         "different receipt",
			receiptRef:   testSHARef("other-review-receipt"),
			candidateRef: candidateRef,
			resultRef:    resultRef,
			wantErr:      true,
		},
		{
			name:         "different candidate",
			receiptRef:   receiptRef,
			candidateRef: testSHARef("other-review-candidate"),
			resultRef:    resultRef,
			wantErr:      true,
		},
		{
			name:         "different result",
			receiptRef:   receiptRef,
			candidateRef: candidateRef,
			resultRef:    testSHARef("other-review-result"),
			wantErr:      true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := authority.Validate(
				test.receiptRef,
				test.candidateRef,
				test.resultRef,
			)
			if test.wantErr {
				if !errors.Is(err, ErrAuthorityBindingMismatch) {
					t.Fatalf("Validate error = %v, want authority binding mismatch", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate exact tuple: %v", err)
			}
		})
	}
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
