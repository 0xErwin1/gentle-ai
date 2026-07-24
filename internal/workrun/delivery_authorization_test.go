package workrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

type terminalWorkRunFixture struct {
	store         WorkRunStore
	state         WorkRunState
	authority     *testAuthorityRepository
	applicability reviewtransaction.VerificationApplicability
	registry      reviewtransaction.VerificationPlanRegistry
	plan          reviewtransaction.VerificationPlan
	result        reviewtransaction.VerificationResultRef
}

func TestDeliveryAuthorizationNormalReadyAndLiveRevalidation(t *testing.T) {
	fixture := newTerminalWorkRunFixture(
		t,
		reviewtransaction.VerificationAggregateNotRequired,
	)
	before, err := fixture.store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.PublicState != PublicStateChecking {
		t.Fatalf("terminal work without PAD authorization = %q", before.PublicState)
	}

	authorizationRef := testSHARef("normal-delivery-authorization")
	authorization := liveDeliveryAuthorization(
		fixture.state,
		authorizationRef,
		DeliveryAuthorizationNormal,
	)
	fixture.authority.authorizations[authorizationRef] = authorization
	request := BindDeliveryAuthorizationRequest{
		ExpectedRevision: fixture.state.Revision,
		RequestID:        "bind-normal-delivery",
		AuthorizationRef: authorizationRef,
	}
	bound, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.DeliveryAuthorizationRef != authorizationRef {
		t.Fatalf("bound authorization ref = %q", bound.DeliveryAuthorizationRef)
	}
	payload, err := json.Marshal(bound)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		`"validated_at"`,
		`"observed_at"`,
		`"expires_at"`,
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("WorkRun copied PAD authorization preimage %q: %s", forbidden, payload)
		}
	}
	status, err := fixture.store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicState != PublicStateReady ||
		status.Verification.Outcome != VerificationNotRequired {
		t.Fatalf("normal delivery status = %#v", status)
	}

	replayed, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatalf("exact delivery authorization replay failed: %v", err)
	}
	if replayed.Revision != bound.Revision ||
		replayed.DeliveryAuthorizationRef != authorizationRef {
		t.Fatalf("exact delivery replay = %#v, want %#v", replayed, bound)
	}
	conflicting := request
	conflicting.AuthorizationRef = testSHARef("different-authorization")
	if _, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		conflicting,
	); !errors.Is(err, ErrWorkRunRequestConflict) {
		t.Fatalf("delivery request-ID conflict error = %v", err)
	}

	fixture.authority.authorizationErrors[authorizationRef] =
		&DeliveryAuthorizationInactiveError{
			AuthorizationRef: authorizationRef,
			Reason:           "owner revoked authorization",
		}
	status, err = fixture.store.PublicStatus(context.Background())
	if err != nil {
		t.Fatalf("inactive authorization should project safely: %v", err)
	}
	if status.PublicState != PublicStateChecking {
		t.Fatalf("inactive normal authorization state = %q", status.PublicState)
	}

	fixture.authority.authorizationErrors[authorizationRef] = os.ErrPermission
	if _, err := fixture.store.PublicStatus(context.Background()); err == nil {
		t.Fatal("PAD owner outage was hidden as a normal non-ready state")
	}
	delete(fixture.authority.authorizationErrors, authorizationRef)

	skewedOwnerClock := authorization
	skewedOwnerClock.ValidatedAt = time.Now().Unix() + int64((24*time.Hour)/time.Second)
	skewedOwnerClock.ObservedAt = skewedOwnerClock.ValidatedAt + 60
	skewedOwnerClock.ExpiresAt = skewedOwnerClock.ValidatedAt + 3_600
	fixture.authority.authorizations[authorizationRef] = skewedOwnerClock
	status, err = fixture.store.PublicStatus(context.Background())
	if err != nil {
		t.Fatalf("ambient clock skew overrode PAD trusted observation: %v", err)
	}
	if status.PublicState != PublicStateReady {
		t.Fatalf("PAD-live authorization under clock skew state = %q", status.PublicState)
	}

	fixture.authority.authorizationErrors[authorizationRef] =
		&DeliveryAuthorizationInactiveError{
			AuthorizationRef: authorizationRef,
			Reason:           "owner-observed expiry",
		}
	status, err = fixture.store.PublicStatus(context.Background())
	if err != nil {
		t.Fatalf("owner-expired authorization should project safely: %v", err)
	}
	if status.PublicState != PublicStateChecking {
		t.Fatalf("owner-expired normal authorization state = %q", status.PublicState)
	}
	delete(fixture.authority.authorizationErrors, authorizationRef)

	tampered := authorization
	tampered.CandidateRef = testSHARef("other-candidate")
	fixture.authority.authorizations[authorizationRef] = tampered
	if _, err := fixture.store.PublicStatus(context.Background()); !errors.Is(
		err,
		ErrAuthorityBindingMismatch,
	) {
		t.Fatalf("tampered live authorization status error = %v", err)
	}
}

func TestDeliveryAuthorizationExceptionKeepsUnavailableReasonWhileReady(t *testing.T) {
	fixture := newTerminalWorkRunFixture(
		t,
		reviewtransaction.VerificationAggregateUnavailable,
	)
	authorizationRef := testSHARef("exception-delivery-authorization")
	fixture.authority.authorizations[authorizationRef] = liveDeliveryAuthorization(
		fixture.state,
		authorizationRef,
		DeliveryAuthorizationException,
	)
	bound, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		BindDeliveryAuthorizationRequest{
			ExpectedRevision: fixture.state.Revision,
			RequestID:        "bind-exception-delivery",
			AuthorizationRef: authorizationRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.DeliveryAuthorizationRef != authorizationRef {
		t.Fatalf("exception authorization was not persisted by ref: %#v", bound)
	}
	status, err := fixture.store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicState != PublicStateReady ||
		status.Verification.Outcome != VerificationUnavailable {
		t.Fatalf("accepted-risk delivery status = %#v", status)
	}
}

func TestBindDeliveryAuthorizationRejectsFabricatedStaleAndCrossBoundFacts(t *testing.T) {
	fixture := newTerminalWorkRunFixture(
		t,
		reviewtransaction.VerificationAggregateNotRequired,
	)
	if _, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		BindDeliveryAuthorizationRequest{
			ExpectedRevision: fixture.state.Revision,
			RequestID:        "fabricated-authorization",
			AuthorizationRef: testSHARef("fabricated-authorization"),
		},
	); err == nil {
		t.Fatal("hash-shaped but unowned delivery authorization was accepted")
	}

	tests := []struct {
		name   string
		mutate func(*DeliveryAuthorizationAuthority)
	}{
		{
			name: "authorization ref",
			mutate: func(value *DeliveryAuthorizationAuthority) {
				value.AuthorizationRef = testSHARef("cross-authorization-ref")
			},
		},
		{
			name: "delivery intent",
			mutate: func(value *DeliveryAuthorizationAuthority) {
				value.DeliveryIntentRef = testSHARef("cross-intent")
			},
		},
		{
			name: "candidate",
			mutate: func(value *DeliveryAuthorizationAuthority) {
				value.CandidateRef = testSHARef("cross-candidate")
			},
		},
		{
			name: "verification result",
			mutate: func(value *DeliveryAuthorizationAuthority) {
				value.VerificationResultRef = testSHARef("cross-result")
			},
		},
		{
			name: "review receipt",
			mutate: func(value *DeliveryAuthorizationAuthority) {
				value.ReviewReceiptRef = testSHARef("cross-receipt")
			},
		},
		{
			name: "observation after expiry",
			mutate: func(value *DeliveryAuthorizationAuthority) {
				value.ObservedAt = value.ExpiresAt
			},
		},
		{
			name: "observation before validation",
			mutate: func(value *DeliveryAuthorizationAuthority) {
				value.ObservedAt = value.ValidatedAt - 1
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref := testSHARef("invalid-authorization-" + test.name)
			authorization := liveDeliveryAuthorization(
				fixture.state,
				ref,
				DeliveryAuthorizationNormal,
			)
			test.mutate(&authorization)
			fixture.authority.authorizations[ref] = authorization
			if _, err := fixture.store.BindDeliveryAuthorization(
				context.Background(),
				BindDeliveryAuthorizationRequest{
					ExpectedRevision: fixture.state.Revision,
					RequestID:        "invalid-authorization-" + string(rune('a'+index)),
					AuthorizationRef: ref,
				},
			); err == nil {
				t.Fatalf("invalid %s authorization was accepted", test.name)
			}
			current, err := fixture.store.Status()
			if err != nil {
				t.Fatal(err)
			}
			if current.Revision != fixture.state.Revision ||
				current.DeliveryAuthorizationRef != "" {
				t.Fatalf("rejected authorization mutated state: %#v", current)
			}
		})
	}

	validRef := testSHARef("valid-but-stale-authorization")
	fixture.authority.authorizations[validRef] = liveDeliveryAuthorization(
		fixture.state,
		validRef,
		DeliveryAuthorizationNormal,
	)
	if _, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		BindDeliveryAuthorizationRequest{
			ExpectedRevision: testSHARef("stale-revision"),
			RequestID:        "stale-authorization",
			AuthorizationRef: validRef,
		},
	); !errors.Is(err, ErrWorkRunRevisionConflict) {
		t.Fatalf("stale delivery authorization error = %v", err)
	}

	fixture.authority.authorizationErrors[validRef] = os.ErrPermission
	if _, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		BindDeliveryAuthorizationRequest{
			ExpectedRevision: fixture.state.Revision,
			RequestID:        "owner-down-authorization",
			AuthorizationRef: validRef,
		},
	); err == nil {
		t.Fatal("PAD owner failure was ignored during delivery binding")
	}
	inactiveRef := testSHARef("owner-inactive-authorization")
	fixture.authority.authorizationErrors[inactiveRef] =
		&DeliveryAuthorizationInactiveError{
			AuthorizationRef: inactiveRef,
			Reason:           "expired according to PAD trusted clock",
		}
	if _, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		BindDeliveryAuthorizationRequest{
			ExpectedRevision: fixture.state.Revision,
			RequestID:        "inactive-authorization",
			AuthorizationRef: inactiveRef,
		},
	); !errors.Is(err, ErrDeliveryAuthorizationInactive) {
		t.Fatalf("inactive PAD authorization binding error = %v", err)
	}
}

func TestDeliveryAuthorizationAggregatePolicyMatrix(t *testing.T) {
	tests := []struct {
		name      string
		kind      DeliveryAuthorizationKind
		aggregate reviewtransaction.VerificationAggregate
		wantError bool
	}{
		{
			name:      "normal complete",
			kind:      DeliveryAuthorizationNormal,
			aggregate: reviewtransaction.VerificationAggregateComplete,
		},
		{
			name:      "normal not required",
			kind:      DeliveryAuthorizationNormal,
			aggregate: reviewtransaction.VerificationAggregateNotRequired,
		},
		{
			name:      "normal partial rejected",
			kind:      DeliveryAuthorizationNormal,
			aggregate: reviewtransaction.VerificationAggregatePartial,
			wantError: true,
		},
		{
			name:      "normal unavailable rejected",
			kind:      DeliveryAuthorizationNormal,
			aggregate: reviewtransaction.VerificationAggregateUnavailable,
			wantError: true,
		},
		{
			name:      "exception partial",
			kind:      DeliveryAuthorizationException,
			aggregate: reviewtransaction.VerificationAggregatePartial,
		},
		{
			name:      "exception unavailable",
			kind:      DeliveryAuthorizationException,
			aggregate: reviewtransaction.VerificationAggregateUnavailable,
		},
		{
			name:      "exception complete rejected",
			kind:      DeliveryAuthorizationException,
			aggregate: reviewtransaction.VerificationAggregateComplete,
			wantError: true,
		},
		{
			name:      "exception not required rejected",
			kind:      DeliveryAuthorizationException,
			aggregate: reviewtransaction.VerificationAggregateNotRequired,
			wantError: true,
		},
		{
			name:      "normal failed always rejected",
			kind:      DeliveryAuthorizationNormal,
			aggregate: reviewtransaction.VerificationAggregateFailed,
			wantError: true,
		},
		{
			name:      "exception failed always rejected",
			kind:      DeliveryAuthorizationException,
			aggregate: reviewtransaction.VerificationAggregateFailed,
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAuthorizedDeliveryAggregate(test.kind, test.aggregate)
			if test.wantError && err == nil {
				t.Fatal("aggregate policy accepted forbidden authorization")
			}
			if !test.wantError && err != nil {
				t.Fatalf("aggregate policy rejected valid authorization: %v", err)
			}
		})
	}
}

func newTerminalWorkRunFixture(
	t *testing.T,
	aggregate reviewtransaction.VerificationAggregate,
) terminalWorkRunFixture {
	t.Helper()
	active := aggregate != reviewtransaction.VerificationAggregateNotRequired
	repo := initWorkRunRepository(t)
	applicability, registry, plan := verificationFixture(t, repo, active)
	store := openTestWorkRunStore(t, repo, "terminal-delivery")
	state := startDirectWorkRun(t, store)
	requirements := []string{}
	availability := ForecastNotRequired
	diagnostics := []string{}
	if active {
		requirements = verificationRequirementRefs(plan)
		availability = ForecastUnavailable
		diagnostics = []string{testSHARef("terminal-unavailable-diagnostic")}
	}
	handoff, err := NewImplementationHandoff(
		ImplementationRouteDirectInline,
		testSHARef("terminal-scope"),
		plan.Subject,
		requirements,
		[]string{},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err = store.BindImplementationHandoff(
		context.Background(),
		BindImplementationHandoffRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "terminal-handoff",
			Handoff:          handoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	forecastInput := VerificationForecastInput{
		WorkRunID: store.WorkRunID, Handoff: handoff,
		Applicability: applicability, Registry: registry, Plan: plan,
		PlanRevisionRef: testSHARef("terminal-plan-revision"),
		Availability:    availability,
		AvailabilityRef: testSHARef("terminal-availability"),
		DiagnosticRefs:  diagnostics,
	}
	registerTestForecast(t, store, forecastInput)
	state, err = store.RecordVerificationForecast(
		context.Background(),
		RecordVerificationForecastRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "terminal-forecast",
			Input:            forecastInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result := reviewtransaction.VerificationResultRef{
		Schema:    reviewtransaction.VerificationResultRefSchema,
		ResultRef: testSHARef("terminal-result-" + string(aggregate)),
		Subject:   plan.Subject, PolicyHash: plan.PolicyHash,
		PlanDigest: plan.Digest, ApplicabilityDigest: plan.ApplicabilityDigest,
		Aggregate: aggregate, CompletedObligations: []string{},
		EvidenceRefs: []string{},
	}
	authority := testAuthorityForStore(t, store)
	authority.results[result.ResultRef] = VerificationResultAuthority{
		Applicability: applicability, Registry: registry, Plan: plan, Result: result,
	}
	state, err = store.BindVerificationResult(
		context.Background(),
		BindVerificationResultRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "terminal-result",
			Applicability:    applicability, Registry: registry, Plan: plan,
			Result: result,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	receiptRef := testSHARef("terminal-review")
	authority.receipts[receiptRef] = ReviewReceiptAuthority{
		ReceiptRef: receiptRef, CandidateRef: handoff.CandidateRef,
		VerificationResultRef: result.ResultRef,
	}
	state, err = store.BindReviewReceipt(
		context.Background(),
		BindReviewReceiptRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "terminal-review",
			ReviewReceiptRef: receiptRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return terminalWorkRunFixture{
		store: store, state: state, authority: authority,
		applicability: applicability, registry: registry, plan: plan, result: result,
	}
}

func liveDeliveryAuthorization(
	state WorkRunState,
	ref string,
	kind DeliveryAuthorizationKind,
) DeliveryAuthorizationAuthority {
	observedAt := time.Now().Unix()
	return DeliveryAuthorizationAuthority{
		AuthorizationRef: ref, Kind: kind,
		DeliveryIntentRef:     state.DeliveryIntentRef,
		CandidateRef:          state.Handoff.CandidateRef,
		ReviewReceiptRef:      state.ReviewReceiptRef,
		VerificationResultRef: state.VerificationResultRef,
		ValidatedAt:           observedAt - 60,
		ObservedAt:            observedAt,
		ExpiresAt:             observedAt + 3_600,
	}
}
