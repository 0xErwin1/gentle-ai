package workprovider

import (
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestVerificationTransitionAuthorizationRoundTripsPublicContract(t *testing.T) {
	authorization := validVerificationTransitionAuthorization(t)
	resolved, err := authorization.Resolved()
	if err != nil {
		t.Fatalf("Resolved() error = %v", err)
	}
	public, err := authorization.PublicTransition()
	if err != nil {
		t.Fatalf("PublicTransition() error = %v", err)
	}
	if !resolved.matches(authorization.WorkRunID, public) {
		t.Fatalf("public transition does not match owner preimage: %#v", public)
	}
	if public.Contract != workrun.WorkTransitionContractV1 ||
		public.Operation != "apply" ||
		public.AuthorizationRef != authorization.AuthorizationRef {
		t.Fatalf("public transition = %#v", public)
	}
	requestID, err := authorization.RequestID()
	if err != nil {
		t.Fatalf("RequestID() error = %v", err)
	}
	if requestID != "apply-"+strings.TrimPrefix(authorization.AuthorizationRef, "sha256:") {
		t.Fatalf("request id = %q", requestID)
	}
}

func TestVerificationTransitionAuthorizationRejectsCrossBindingAndWindowDrift(t *testing.T) {
	base := validVerificationTransitionAuthorization(t)
	tests := []struct {
		name   string
		mutate func(*VerificationTransitionAuthorization)
	}{
		{
			name: "work run",
			mutate: func(value *VerificationTransitionAuthorization) {
				value.WorkRunID = "other-work"
			},
		},
		{
			name: "revision",
			mutate: func(value *VerificationTransitionAuthorization) {
				value.ExpectedRevision = testRevision("9")
			},
		},
		{
			name: "candidate",
			mutate: func(value *VerificationTransitionAuthorization) {
				value.CandidateRef = testRevision("8")
			},
		},
		{
			name: "ticket",
			mutate: func(value *VerificationTransitionAuthorization) {
				value.ActionTicketRef = testRevision("7")
			},
		},
		{
			name: "plan authority",
			mutate: func(value *VerificationTransitionAuthorization) {
				value.ApplicableAuthorizationRef = testRevision("6")
			},
		},
		{
			name: "class",
			mutate: func(value *VerificationTransitionAuthorization) {
				value.Class = "invented"
			},
		},
		{
			name: "window",
			mutate: func(value *VerificationTransitionAuthorization) {
				value.ExpiresAt = value.IssuedAt
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.mutate(&changed)
			if err := changed.Validate(); err == nil {
				t.Fatal("Validate() accepted changed immutable authority")
			}
		})
	}
}

func TestNewVerificationTransitionAuthorizationRejectsInvalidInputs(t *testing.T) {
	request := validVerificationTransitionAuthorizationRequest()
	request.ActionTicketRef = "ticket:caller-authored"
	if _, err := NewVerificationTransitionAuthorization(request); err == nil {
		t.Fatal("NewVerificationTransitionAuthorization() accepted non-owner ticket ref")
	}
}

func validVerificationTransitionAuthorization(
	t *testing.T,
) VerificationTransitionAuthorization {
	t.Helper()
	authorization, err := NewVerificationTransitionAuthorization(
		validVerificationTransitionAuthorizationRequest(),
	)
	if err != nil {
		t.Fatalf("NewVerificationTransitionAuthorization() error = %v", err)
	}
	return authorization
}

func validVerificationTransitionAuthorizationRequest() VerificationTransitionAuthorizationRequest {
	return VerificationTransitionAuthorizationRequest{
		WorkRunID: "transition-authorization-test", ExpectedRevision: testRevision("a"),
		CandidateRef: testRevision("b"), ActionTicketRef: testRevision("c"),
		ApplicableAuthorizationRef: testRevision("d"),
		Class:                      AuthorizationGeneral, IssuedAt: 10, ExpiresAt: 20,
	}
}
