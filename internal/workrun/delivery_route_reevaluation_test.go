package workrun

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

func TestBindDeliveryRouteReevaluationPreservesTerminalContentAndAcceptsTargetAuthorization(
	t *testing.T,
) {
	fixture := newTerminalWorkRunFixture(
		t,
		reviewtransaction.VerificationAggregateNotRequired,
	)
	before := fixture.state
	reevaluationRef := testSHARef("delivery-route-reevaluation")
	targetIntentRef := testSHARef("delivery-route-target-intent")
	routeAuthority := DeliveryRouteReevaluationAuthority{
		ReevaluationRef:            reevaluationRef,
		RepositoryRef:              fixture.store.RepositoryRef(),
		SourceDecisionRef:          testSHARef("delivery-route-source-decision"),
		TargetAdmissionDecisionRef: testSHARef("delivery-route-target-admission"),
		TargetDecisionRef:          testSHARef("delivery-route-target-decision"),
		SourceDeliveryIntentRef:    before.DeliveryIntentRef,
		TargetDeliveryIntentRef:    targetIntentRef,
		SourceRoute:                "pr_without_issue",
		TargetRoute:                "direct_main",
		Mechanism:                  "fast_forward_only",
		ScopeDigest:                before.Handoff.ScopeDigest,
		CandidateRef:               before.Handoff.CandidateRef,
		ReviewReceiptRef:           before.ReviewReceiptRef,
		VerificationResultRef:      before.VerificationResultRef,
	}
	fixture.authority.deliveryRoutes[reevaluationRef] = routeAuthority
	request := BindDeliveryRouteReevaluationRequest{
		ExpectedRevision: before.Revision,
		RequestID:        "bind-delivery-route-reevaluation",
		ReevaluationRef:  reevaluationRef,
	}
	bound, err := fixture.store.BindDeliveryRouteReevaluation(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.DeliveryIntentRef != targetIntentRef ||
		bound.DeliveryRouteReevaluationRef != reevaluationRef {
		t.Fatalf("bound delivery route state = %#v", bound)
	}
	if bound.ImplementationRoute != before.ImplementationRoute ||
		!reflect.DeepEqual(bound.RouteDecision, before.RouteDecision) ||
		bound.RouteAcceptanceRef != before.RouteAcceptanceRef ||
		bound.SDDRunRef != before.SDDRunRef ||
		!reflect.DeepEqual(bound.Handoff, before.Handoff) ||
		bound.VerificationResultRef != before.VerificationResultRef ||
		bound.PostVerificationSnapshotRef !=
			before.PostVerificationSnapshotRef ||
		bound.ReviewReceiptRef != before.ReviewReceiptRef ||
		bound.DeliveryAuthorizationRef != "" {
		t.Fatalf("route-only event widened terminal content: %#v", bound)
	}

	replayed, err := fixture.store.BindDeliveryRouteReevaluation(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatalf("exact route reevaluation replay: %v", err)
	}
	if !reflect.DeepEqual(replayed, bound) {
		t.Fatalf("route reevaluation replay changed state")
	}

	alternateAuthorizationRef := testSHARef(
		"alternate-target-route-authorization",
	)
	alternateAuthorization := liveDeliveryAuthorization(
		bound,
		alternateAuthorizationRef,
		DeliveryAuthorizationNormal,
	)
	alternateAuthorization.DecisionRef = testSHARef(
		"alternate-target-route-decision",
	)
	fixture.authority.authorizations[alternateAuthorizationRef] =
		alternateAuthorization
	if _, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		BindDeliveryAuthorizationRequest{
			ExpectedRevision: bound.Revision,
			RequestID:        "bind-alternate-target-route-authorization",
			AuthorizationRef: alternateAuthorizationRef,
		},
	); !errors.Is(err, ErrAuthorityBindingMismatch) {
		t.Fatalf("alternate target-route authorization error = %v", err)
	}
	afterRejected, err := fixture.store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterRejected, bound) {
		t.Fatalf(
			"alternate target-route authorization mutated WorkRun: %#v",
			afterRejected,
		)
	}

	authorizationRef := testSHARef("target-route-authorization")
	authorization := liveDeliveryAuthorization(
		bound,
		authorizationRef,
		DeliveryAuthorizationNormal,
	)
	authorization.DecisionRef = routeAuthority.TargetDecisionRef
	fixture.authority.authorizations[authorizationRef] = authorization
	delivered, err := fixture.store.BindDeliveryAuthorization(
		context.Background(),
		BindDeliveryAuthorizationRequest{
			ExpectedRevision: bound.Revision,
			RequestID:        "bind-target-route-authorization",
			AuthorizationRef: authorizationRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.DeliveryIntentRef != targetIntentRef ||
		delivered.DeliveryRouteReevaluationRef != reevaluationRef ||
		delivered.DeliveryAuthorizationRef != authorizationRef {
		t.Fatalf("target-route delivery binding = %#v", delivered)
	}
}

func TestBindDeliveryRouteReevaluationRejectsMissingOrCrossBoundAuthority(
	t *testing.T,
) {
	fixture := newTerminalWorkRunFixture(
		t,
		reviewtransaction.VerificationAggregateNotRequired,
	)
	before := fixture.state
	if _, err := fixture.store.BindDeliveryRouteReevaluation(
		context.Background(),
		BindDeliveryRouteReevaluationRequest{
			ExpectedRevision: before.Revision,
			RequestID:        "missing-route-reevaluation",
			ReevaluationRef:  testSHARef("missing-route-reevaluation"),
		},
	); err == nil {
		t.Fatal("hash-shaped but unowned route reevaluation was accepted")
	}

	ref := testSHARef("cross-bound-route-reevaluation")
	fixture.authority.deliveryRoutes[ref] =
		DeliveryRouteReevaluationAuthority{
			ReevaluationRef:            ref,
			RepositoryRef:              fixture.store.RepositoryRef(),
			SourceDecisionRef:          testSHARef("cross-source-decision"),
			TargetAdmissionDecisionRef: testSHARef("cross-target-admission"),
			TargetDecisionRef:          testSHARef("cross-target-decision"),
			SourceDeliveryIntentRef:    before.DeliveryIntentRef,
			TargetDeliveryIntentRef:    testSHARef("cross-target-intent"),
			SourceRoute:                "pr_without_issue",
			TargetRoute:                "direct_main",
			Mechanism:                  "fast_forward_only",
			ScopeDigest:                before.Handoff.ScopeDigest,
			CandidateRef:               testSHARef("different-candidate"),
			ReviewReceiptRef:           before.ReviewReceiptRef,
			VerificationResultRef:      before.VerificationResultRef,
		}
	if _, err := fixture.store.BindDeliveryRouteReevaluation(
		context.Background(),
		BindDeliveryRouteReevaluationRequest{
			ExpectedRevision: before.Revision,
			RequestID:        "cross-bound-route-reevaluation",
			ReevaluationRef:  ref,
		},
	); !errors.Is(err, ErrAuthorityBindingMismatch) {
		t.Fatalf("cross-bound route reevaluation error = %v", err)
	}
	after, err := fixture.store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected route reevaluation mutated state")
	}
}
