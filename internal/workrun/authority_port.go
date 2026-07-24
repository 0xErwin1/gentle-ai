package workrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

var (
	ErrAuthorityPortUnavailable      = errors.New("work run owner authority port is unavailable")
	ErrAuthorityBindingMismatch      = errors.New("work run owner authority binding does not match")
	ErrDeliveryAuthorizationInactive = errors.New("delivery authorization is not live")
)

// PADAuthorityPort resolves delivery facts only from PAD-owned storage. The
// Live method must re-evaluate policy and expiry with PAD's trusted clock on
// every call and return ErrDeliveryAuthorizationInactive when no longer live.
// A hash-shaped caller value is never sufficient authority.
type PADAuthorityPort interface {
	ResolveDeliveryIntent(context.Context, string) (DeliveryIntentAuthority, error)
	ResolveLiveDeliveryAuthorization(
		context.Context,
		string,
	) (DeliveryAuthorizationAuthority, error)
}

type DeliveryIntentAuthority struct {
	IntentRef string
}

func (authority DeliveryIntentAuthority) Validate(ref string) error {
	if !validSHA256Ref(authority.IntentRef) || authority.IntentRef != ref {
		return fmt.Errorf("%w: delivery intent", ErrAuthorityBindingMismatch)
	}
	return nil
}

type DeliveryAuthorizationKind string

const (
	DeliveryAuthorizationNormal    DeliveryAuthorizationKind = "normal"
	DeliveryAuthorizationException DeliveryAuthorizationKind = "exception"
)

// DeliveryAuthorizationInactiveError represents an owner-confirmed or locally
// observed loss of liveness. Binding mismatches and malformed immutable facts
// use different errors so callers never hide corruption as a normal expiry.
type DeliveryAuthorizationInactiveError struct {
	AuthorizationRef string
	Reason           string
}

func (err *DeliveryAuthorizationInactiveError) Error() string {
	if err == nil {
		return ErrDeliveryAuthorizationInactive.Error()
	}
	if err.Reason == "" {
		return fmt.Sprintf(
			"%v: %s",
			ErrDeliveryAuthorizationInactive,
			err.AuthorizationRef,
		)
	}
	return fmt.Sprintf(
		"%v: %s: %s",
		ErrDeliveryAuthorizationInactive,
		err.AuthorizationRef,
		err.Reason,
	)
}

func (err *DeliveryAuthorizationInactiveError) Unwrap() error {
	return ErrDeliveryAuthorizationInactive
}

// DeliveryAuthorizationAuthority is the minimum WorkRun-owned projection of a
// PAD authorization. Its immutable ref remains the only persisted value.
type DeliveryAuthorizationAuthority struct {
	AuthorizationRef      string                    `json:"authorization_ref"`
	Kind                  DeliveryAuthorizationKind `json:"kind"`
	DeliveryIntentRef     string                    `json:"delivery_intent_ref"`
	CandidateRef          string                    `json:"candidate_ref"`
	ReviewReceiptRef      string                    `json:"review_receipt_ref"`
	VerificationResultRef string                    `json:"verification_result_ref"`
	ValidatedAt           int64                     `json:"validated_at"`
	ObservedAt            int64                     `json:"observed_at"`
	ExpiresAt             int64                     `json:"expires_at"`
}

func (authority DeliveryAuthorizationAuthority) Validate(
	requestedRef string,
	state WorkRunState,
) error {
	for _, ref := range []string{
		authority.AuthorizationRef,
		authority.DeliveryIntentRef,
		authority.CandidateRef,
		authority.ReviewReceiptRef,
		authority.VerificationResultRef,
	} {
		if !validSHA256Ref(ref) {
			return errors.New(
				"delivery authorization authority has invalid immutable references",
			)
		}
	}
	if authority.AuthorizationRef != requestedRef {
		return fmt.Errorf(
			"%w: delivery authorization reference",
			ErrAuthorityBindingMismatch,
		)
	}
	switch authority.Kind {
	case DeliveryAuthorizationNormal, DeliveryAuthorizationException:
	default:
		return fmt.Errorf(
			"unsupported delivery authorization kind %q",
			authority.Kind,
		)
	}
	if authority.ValidatedAt <= 0 ||
		authority.ExpiresAt <= authority.ValidatedAt ||
		authority.ObservedAt < authority.ValidatedAt ||
		authority.ObservedAt >= authority.ExpiresAt {
		return errors.New("delivery authorization has an invalid validity window")
	}
	if state.Handoff == nil ||
		authority.DeliveryIntentRef != state.DeliveryIntentRef ||
		authority.CandidateRef != state.Handoff.CandidateRef ||
		authority.VerificationResultRef != state.VerificationResultRef ||
		authority.ReviewReceiptRef != state.ReviewReceiptRef {
		return fmt.Errorf(
			"%w: delivery authorization terminal facts",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

// ExplicitSDDRequestAuthorityPort resolves the owner-authored request that
// selects SDD before an implementation route decision exists. The authority
// must be durable and independently content-addressed; it cannot depend on the
// digest of the decision that later references it.
type ExplicitSDDRequestAuthorityPort interface {
	ResolveExplicitSDDRequest(
		context.Context,
		string,
	) (ExplicitSDDRequestAuthority, error)
}

type ExplicitSDDRequestAuthority struct {
	AuthorityRef      string
	WorkRunID         string
	DeliveryIntentRef string
}

func (authority ExplicitSDDRequestAuthority) Validate(
	ref string,
	workRunID string,
	deliveryIntentRef string,
) error {
	if !validSHA256Ref(authority.AuthorityRef) ||
		authority.AuthorityRef != ref ||
		authority.WorkRunID != workRunID ||
		!validSHA256Ref(authority.DeliveryIntentRef) ||
		authority.DeliveryIntentRef != deliveryIntentRef {
		return fmt.Errorf(
			"%w: explicit SDD request",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

// RouteAuthorityPort resolves accepted proposals and safe reroutes from the
// provider-owned decision repository. Explicit pre-route requests use the
// separate ExplicitSDDRequestAuthorityPort.
type RouteAuthorityPort interface {
	ResolveRouteSelection(context.Context, string) (RouteSelectionAuthority, error)
}

type RouteSelectionAuthority struct {
	DecisionRef            string
	PendingDecisionDigest  string
	SelectedRoute          ImplementationRoute
	SelectedDecisionDigest string
}

func (authority RouteSelectionAuthority) Validate(
	ref string,
	pendingDigest string,
	selectedRoute ImplementationRoute,
	selectedDecisionDigest string,
) error {
	if !validSHA256Ref(authority.DecisionRef) || authority.DecisionRef != ref ||
		!validSHA256Ref(authority.PendingDecisionDigest) ||
		authority.PendingDecisionDigest != pendingDigest ||
		authority.SelectedRoute != selectedRoute {
		return fmt.Errorf("%w: implementation route selection", ErrAuthorityBindingMismatch)
	}
	if selectedDecisionDigest == "" {
		if authority.SelectedDecisionDigest != "" {
			return fmt.Errorf("%w: unexpected selected route decision", ErrAuthorityBindingMismatch)
		}
		return nil
	}
	if !validSHA256Ref(authority.SelectedDecisionDigest) ||
		authority.SelectedDecisionDigest != selectedDecisionDigest {
		return fmt.Errorf("%w: selected route decision", ErrAuthorityBindingMismatch)
	}
	return nil
}

// SDDAuthorityPort resolves an accepted SDD run and idempotently binds the
// common verification reservation into the exact SDD phase attempt. WorkRun
// never copies the SDD attempt ledger.
type SDDAuthorityPort interface {
	ResolveRun(context.Context, string) (SDDRunAuthority, error)
	BindVerificationReservation(context.Context, SDDReservationBinding) error
}

type SDDRunAuthority struct {
	RunRef             string
	WorkRunID          string
	RouteAcceptanceRef string
}

func (authority SDDRunAuthority) Validate(
	runRef string,
	workRunID string,
	acceptanceRef string,
) error {
	if !validOpaqueRef(authority.RunRef) || authority.RunRef != runRef ||
		authority.WorkRunID != workRunID ||
		!validSHA256Ref(authority.RouteAcceptanceRef) ||
		authority.RouteAcceptanceRef != acceptanceRef {
		return fmt.Errorf("%w: SDD run", ErrAuthorityBindingMismatch)
	}
	return nil
}

type SDDReservationBinding struct {
	WorkRunID       string
	WorkRunRevision string
	SDDRunRef       string
	ReservationRef  string
	ActionTicketRef string
}

func (binding SDDReservationBinding) Validate() error {
	if !workRunIDPattern.MatchString(binding.WorkRunID) ||
		!validSHA256Ref(binding.WorkRunRevision) ||
		!validOpaqueRef(binding.SDDRunRef) ||
		!validSHA256Ref(binding.ReservationRef) ||
		!validSHA256Ref(binding.ActionTicketRef) {
		return errors.New("invalid SDD verification reservation binding")
	}
	return nil
}

// VerificationAuthorityPort resolves every RAR-owned plan, result, and receipt
// preimage plus provider-owned forecast/consent observations. A review receipt
// is resolved by its exact receipt/result pair and then revalidated against the
// WorkRun candidate. WorkRun persists only immutable references.
type VerificationAuthorityPort interface {
	ResolveForecast(context.Context, string) (VerificationForecastAuthority, error)
	ResolveDisposition(context.Context, string) (VerificationDispositionAuthority, error)
	ResolveResult(context.Context, string) (VerificationResultAuthority, error)
	ResolveReviewReceipt(
		ctx context.Context,
		receiptRef string,
		resultRef string,
	) (ReviewReceiptAuthority, error)
}

type VerificationForecastAuthority struct {
	AvailabilityRef string
	Applicability   reviewtransaction.VerificationApplicability
	Registry        reviewtransaction.VerificationPlanRegistry
	Plan            reviewtransaction.VerificationPlan
	PlanRevisionRef string
	Availability    ForecastAvailability
	DiagnosticRefs  []string
}

func (authority VerificationForecastAuthority) Validate() error {
	if !validSHA256Ref(authority.AvailabilityRef) ||
		!validSHA256Ref(authority.PlanRevisionRef) {
		return errors.New("verification forecast authority has invalid immutable references")
	}
	if err := reviewtransaction.ValidateVerificationPlan(
		authority.Applicability,
		authority.Registry,
		authority.Plan,
	); err != nil {
		return err
	}
	if !isCanonicalSHA256Refs(authority.DiagnosticRefs) {
		return errors.New("verification forecast authority diagnostics must be canonical")
	}
	switch authority.Availability {
	case ForecastAvailable, ForecastNotRequired, ForecastPartial,
		ForecastUnavailable, ForecastUnknown:
	default:
		return fmt.Errorf("unsupported owner forecast availability %q", authority.Availability)
	}
	return nil
}

func (authority VerificationForecastAuthority) MatchesInput(
	input VerificationForecastInput,
) error {
	if err := authority.Validate(); err != nil {
		return err
	}
	if authority.AvailabilityRef != input.AvailabilityRef ||
		authority.Applicability.Digest != input.Applicability.Digest ||
		authority.Registry.Digest != input.Registry.Digest ||
		authority.Plan.Digest != input.Plan.Digest ||
		authority.PlanRevisionRef != input.PlanRevisionRef ||
		authority.Availability != input.Availability ||
		!equalStrings(authority.DiagnosticRefs, input.DiagnosticRefs) {
		return fmt.Errorf("%w: verification forecast", ErrAuthorityBindingMismatch)
	}
	return nil
}

type VerificationDispositionAuthority struct {
	DecisionRef    string
	ForecastDigest string
	AssumptionsRef string
	Kind           VerificationDispositionKind
	ActorRef       string
	RunnerRef      string
}

func (authority VerificationDispositionAuthority) Validate(
	forecast VerificationForecast,
) error {
	for _, ref := range []string{
		authority.DecisionRef,
		authority.ForecastDigest,
		authority.AssumptionsRef,
		authority.ActorRef,
	} {
		if !validSHA256Ref(ref) {
			return errors.New("verification disposition authority has invalid immutable references")
		}
	}
	if authority.ForecastDigest != forecast.Digest {
		return fmt.Errorf("%w: verification disposition forecast", ErrAuthorityBindingMismatch)
	}
	disposition, err := newVerificationDisposition(
		forecast,
		authority.Kind,
		authority.AssumptionsRef,
		authority.ActorRef,
		authority.DecisionRef,
		authority.RunnerRef,
	)
	if err != nil {
		return err
	}
	return disposition.ValidateFor(forecast)
}

type VerificationResultAuthority struct {
	Applicability reviewtransaction.VerificationApplicability
	Registry      reviewtransaction.VerificationPlanRegistry
	Plan          reviewtransaction.VerificationPlan
	Result        reviewtransaction.VerificationResultRef
}

func (authority VerificationResultAuthority) Validate(ref string) error {
	if authority.Result.ResultRef != ref {
		return fmt.Errorf("%w: verification result", ErrAuthorityBindingMismatch)
	}
	return reviewtransaction.ValidateVerificationResultRef(
		authority.Applicability,
		authority.Registry,
		authority.Plan,
		authority.Result,
	)
}

type ReviewReceiptAuthority struct {
	ReceiptRef            string
	CandidateRef          string
	VerificationResultRef string
}

func (authority ReviewReceiptAuthority) Validate(
	receiptRef string,
	candidateRef string,
	resultRef string,
) error {
	for _, ref := range []string{
		receiptRef,
		candidateRef,
		resultRef,
		authority.ReceiptRef,
		authority.CandidateRef,
		authority.VerificationResultRef,
	} {
		if !validSHA256Ref(ref) {
			return fmt.Errorf(
				"%w: terminal review receipt contains an invalid immutable reference",
				ErrAuthorityBindingMismatch,
			)
		}
	}
	if authority.ReceiptRef != receiptRef ||
		authority.CandidateRef != candidateRef ||
		authority.VerificationResultRef != resultRef {
		return fmt.Errorf("%w: terminal review receipt", ErrAuthorityBindingMismatch)
	}
	return nil
}

// LaunchAuthorityPort is the HCR-owned activation seam. It creates an opaque
// capability only after WorkRun has durably reserved the exact action. The
// claim callback is invoked by HCR immediately before process creation.
type LaunchAuthorityPort interface {
	ActivateLaunch(
		context.Context,
		hostruntime.LaunchBinding,
		hostruntime.LaunchClaim,
	) (*hostruntime.LaunchCapability, error)
}

type AuthorityPorts struct {
	PAD                PADAuthorityPort
	ExplicitSDDRequest ExplicitSDDRequestAuthorityPort
	Route              RouteAuthorityPort
	SDD                SDDAuthorityPort
	Verification       VerificationAuthorityPort
	Launch             LaunchAuthorityPort
}
