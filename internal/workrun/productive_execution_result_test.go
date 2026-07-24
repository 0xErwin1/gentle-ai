package workrun

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

func TestRecordProductiveExecutionResultAllOutcomesExactReplay(t *testing.T) {
	for _, outcome := range []deliveryadmission.ExecutionOutcome{
		deliveryadmission.ExecutionSucceeded,
		deliveryadmission.ExecutionFailed,
		deliveryadmission.ExecutionIndeterminate,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			store, source, owner := newProductiveExecutionResultFixture(t)
			authority := testProductiveExecutionResultAuthority(
				t,
				store,
				source,
				outcome,
				"exact-"+string(outcome),
			)
			owner.productiveExecutionResults[authority.ResultRef] = authority
			request := RecordProductiveExecutionResultRequest{
				ExpectedRevision: source.Revision,
				RequestID:        "record-execution-" + string(outcome),
				ResultRef:        authority.ResultRef,
			}

			anchored, err := store.RecordProductiveExecutionResult(
				context.Background(),
				request,
			)
			if err != nil {
				t.Fatal(err)
			}
			if anchored.ProductiveExecutionResultRef != authority.ResultRef ||
				anchored.ProductiveExecutionResultSourceRevision != source.Revision ||
				anchored.Revision == source.Revision {
				t.Fatalf("anchored execution state = %#v", anchored)
			}
			replayed, err := store.RecordProductiveExecutionResult(
				context.Background(),
				request,
			)
			if err != nil || !reflect.DeepEqual(replayed, anchored) {
				t.Fatalf(
					"exact execution replay = %#v, %v; want %#v",
					replayed,
					err,
					anchored,
				)
			}
			resolved, err := store.ResolveProductiveExecutionResult(
				context.Background(),
				authority.ResultRef,
			)
			if err != nil || !reflect.DeepEqual(resolved, authority) {
				t.Fatalf(
					"resolved execution authority = %#v, %v; want %#v",
					resolved,
					err,
					authority,
				)
			}

			authority.Execution.EvidenceRef = testSHARef(
				"tampered-execution-evidence-" + string(outcome),
			)
			owner.productiveExecutionResults[authority.ResultRef] = authority
			_, err = store.RecordProductiveExecutionResult(
				context.Background(),
				request,
			)
			var publication *PublicationError
			if !errors.As(err, &publication) ||
				!publication.Committed ||
				publication.Revision != anchored.Revision {
				t.Fatalf("tampered exact replay = %#v", err)
			}
			unchanged, statusErr := store.Status()
			if statusErr != nil || !reflect.DeepEqual(unchanged, anchored) {
				t.Fatalf(
					"tampered replay changed state = %#v, %v",
					unchanged,
					statusErr,
				)
			}
			if _, err := store.PublicStatus(
				context.Background(),
			); !errors.Is(err, ErrAuthorityBindingMismatch) {
				t.Fatalf("tampered execution projected public status: %v", err)
			}
		})
	}
}

func TestRecordProductiveExecutionResultRejectsMissingOrMismatchedAuthorityBeforeMutation(
	t *testing.T,
) {
	t.Run("missing", func(t *testing.T) {
		store, source, _ := newProductiveExecutionResultFixture(t)
		_, err := store.RecordProductiveExecutionResult(
			context.Background(),
			RecordProductiveExecutionResultRequest{
				ExpectedRevision: source.Revision,
				RequestID:        "missing-execution-authority",
				ResultRef:        testSHARef("missing-execution-authority"),
			},
		)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing execution authority = %v", err)
		}
		assertProductiveExecutionSourceUnchanged(t, store, source)
	})

	t.Run("command-mismatch", func(t *testing.T) {
		store, source, owner := newProductiveExecutionResultFixture(t)
		authority := testProductiveExecutionResultAuthority(
			t,
			store,
			source,
			deliveryadmission.ExecutionFailed,
			"command-mismatch",
		)
		authority.CommandRef = testSHARef("another-execution-command")
		owner.productiveExecutionResults[authority.ResultRef] = authority
		_, err := store.RecordProductiveExecutionResult(
			context.Background(),
			RecordProductiveExecutionResultRequest{
				ExpectedRevision: source.Revision,
				RequestID:        "mismatched-execution-authority",
				ResultRef:        authority.ResultRef,
			},
		)
		if !errors.Is(err, ErrAuthorityBindingMismatch) {
			t.Fatalf("mismatched execution authority = %v", err)
		}
		assertProductiveExecutionSourceUnchanged(t, store, source)
	})
}

func TestRecordProductiveExecutionResultConcurrentReferencesAreSingleAssignment(
	t *testing.T,
) {
	store, source, owner := newProductiveExecutionResultFixture(t)
	authorities := []ProductiveExecutionResultAuthority{
		testProductiveExecutionResultAuthority(
			t,
			store,
			source,
			deliveryadmission.ExecutionFailed,
			"concurrent-failed",
		),
		testProductiveExecutionResultAuthority(
			t,
			store,
			source,
			deliveryadmission.ExecutionIndeterminate,
			"concurrent-indeterminate",
		),
	}
	for _, authority := range authorities {
		owner.productiveExecutionResults[authority.ResultRef] = authority
	}

	results := make([]WorkRunState, len(authorities))
	errs := make([]error, len(authorities))
	var wait sync.WaitGroup
	for index := range authorities {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index], errs[index] = store.RecordProductiveExecutionResult(
				context.Background(),
				RecordProductiveExecutionResultRequest{
					ExpectedRevision: source.Revision,
					RequestID: "concurrent-execution-" +
						string(authorities[index].Execution.Outcome),
					ResultRef: authorities[index].ResultRef,
				},
			)
		}()
	}
	wait.Wait()

	winner := -1
	for index := range authorities {
		if errs[index] == nil {
			if winner != -1 {
				t.Fatalf("multiple execution authorities committed: %#v", results)
			}
			winner = index
		}
	}
	if winner == -1 {
		t.Fatalf("no execution authority committed: %#v", errs)
	}
	loser := 1 - winner
	if errs[loser] == nil {
		t.Fatal("losing execution authority unexpectedly committed")
	}
	state, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state, results[winner]) ||
		state.ProductiveExecutionResultRef != authorities[winner].ResultRef ||
		state.ProductiveExecutionResultSourceRevision != source.Revision {
		t.Fatalf(
			"single-assignment state = %#v; winner %#v",
			state,
			results[winner],
		)
	}
	resolved, err := store.ResolveProductiveExecutionResult(
		context.Background(),
		state.ProductiveExecutionResultRef,
	)
	if err != nil || !reflect.DeepEqual(resolved, authorities[winner]) {
		t.Fatalf("winning execution authority = %#v, %v", resolved, err)
	}
}

func newProductiveExecutionResultFixture(
	t *testing.T,
) (WorkRunStore, WorkRunState, *testAuthorityRepository) {
	t.Helper()
	fixture := newTerminalWorkRunFixture(
		t,
		reviewtransaction.VerificationAggregateNotRequired,
	)
	authorizationRef := testSHARef(
		"productive-execution-result-authorization",
	)
	fixture.authority.authorizations[authorizationRef] =
		liveDeliveryAuthorization(
			fixture.state,
			authorizationRef,
			DeliveryAuthorizationNormal,
		)
	state, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		BindDeliveryAuthorizationRequest{
			ExpectedRevision: fixture.state.Revision,
			RequestID:        "productive-execution-result-authorization",
			AuthorizationRef: authorizationRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err = fixture.store.BeginProductiveAdvance(
		context.Background(),
		BeginProductiveAdvanceRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "productive-execution-result-begin",
			SourceRevision:   state.Revision,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return fixture.store, state, fixture.authority
}

func testProductiveExecutionResultAuthority(
	t *testing.T,
	store WorkRunStore,
	state WorkRunState,
	outcome deliveryadmission.ExecutionOutcome,
	seed string,
) ProductiveExecutionResultAuthority {
	t.Helper()
	execution := deliveryadmission.ExecutionResult{
		Schema:           deliveryadmission.ExecutionResultContractV1,
		CommandRef:       testSHARef("productive-execution-command-" + seed),
		AuthorizationRef: state.DeliveryAuthorizationRef,
		Route:            deliveryadmission.RouteDirectMain,
		Candidate: deliveryadmission.CandidateBinding{
			Ref:    "work-run:" + state.WorkRunID,
			Digest: state.Handoff.CandidateRef,
		},
		Outcome:     outcome,
		EvidenceRef: testSHARef("productive-execution-evidence-" + seed),
		CompletedAt: 1,
	}
	if outcome == deliveryadmission.ExecutionSucceeded {
		execution.DeliveryRef = "delivery:" + seed
	}
	resultRef, err := productiveExecutionResultRef(execution)
	if err != nil {
		t.Fatal(err)
	}
	return ProductiveExecutionResultAuthority{
		ResultRef:                resultRef,
		RepositoryRef:            store.RepositoryRef(),
		WorkRunID:                state.WorkRunID,
		SourceRevision:           state.Revision,
		DeliveryIntentRef:        state.DeliveryIntentRef,
		Handoff:                  cloneHandoff(state.Handoff),
		VerificationResultRef:    state.VerificationResultRef,
		ReviewReceiptRef:         state.ReviewReceiptRef,
		DeliveryAuthorizationRef: state.DeliveryAuthorizationRef,
		CommandRef:               execution.CommandRef,
		Execution:                execution,
	}
}

func assertProductiveExecutionSourceUnchanged(
	t *testing.T,
	store WorkRunStore,
	source WorkRunState,
) {
	t.Helper()
	unchanged, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unchanged, source) {
		t.Fatalf(
			"rejected execution authority mutated WorkRun:\nbefore %#v\nafter  %#v",
			source,
			unchanged,
		)
	}
}
