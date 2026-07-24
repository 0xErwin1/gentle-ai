package deliveryadmission

import (
	"context"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

// TrustedResolver is the PAD trust boundary. Implementations resolve immutable
// preimages only from provider-owned repositories/control planes; candidate or
// PR-head content must never satisfy these lookups. CurrentUnix is supplied by
// that same control plane so callers cannot revive expired authority.
type TrustedResolver interface {
	CurrentUnix(context.Context) (int64, error)
	// ResolveDeliveryRepository returns the unique exact Git repository root
	// owned by the control plane for repositoryRef. The mapping is immutable
	// while any authorization for that repository can remain live; callers
	// cannot select a filesystem root for the durable one-shot store.
	ResolveDeliveryRepository(context.Context, string) (string, error)
	ResolveIntent(context.Context, string) (DeliveryIntent, error)
	ResolveRoutePolicy(context.Context, string) (RoutePolicy, error)
	ResolveLiveRoutePolicy(context.Context, string, Route) (RoutePolicy, error)
	ResolveAuthoritySignal(context.Context, string) (AuthoritySignal, error)
	ResolveGovernanceDecision(context.Context, string) (GovernanceDecision, error)
	ResolveResidualRisk(context.Context, string) (ResidualRisk, error)
	ResolveAdmissionDecision(context.Context, string) (AdmissionDecision, error)
	ResolveGateEvidence(context.Context, string) (GateEvidence, error)
	ResolveDeliveryDecision(context.Context, string) (DeliveryDecision, error)
	ResolveAuthorization(context.Context, string) (DeliveryAuthorization, error)
	ResolveExceptionAuthorization(context.Context, string) (DeliveryExceptionAuthorization, error)
}

// RARAuthorityPort is owned by reviewtransaction. Its implementation must
// resolve the exact native terminal receipt and the exact, natively validated
// verification-result preimage (including applicability, registry, and plan).
// PAD consumes those facts and never accepts a caller-authored outcome enum.
type RARAuthorityPort interface {
	ResolveTerminalReview(
		context.Context,
		string,
		string,
	) (reviewtransaction.RARVerificationAuthority, error)
}

type resolvedRARReview struct {
	authority reviewtransaction.RARVerificationAuthority
	result    reviewtransaction.VerificationResultRef
}

type AdmissionRequest struct {
	IntentRef          string
	PolicyRef          string
	GovernanceRef      string
	AuthorityRef       string
	SecondAuthorityRef string
}

// Admit resolves every owner claim by immutable reference before issuing the
// initial decision. Callers request a route; they do not submit authority,
// governance, policy, or wall-clock facts.
func Admit(
	ctx context.Context,
	resolver TrustedResolver,
	request AdmissionRequest,
) (AdmissionDecision, error) {
	now, err := trustedNow(ctx, resolver)
	if err != nil {
		return AdmissionDecision{}, err
	}
	intent, err := resolveIntent(ctx, resolver, request.IntentRef)
	if err != nil {
		return AdmissionDecision{}, err
	}
	policy, err := resolvePolicy(ctx, resolver, request.PolicyRef)
	if err != nil {
		return AdmissionDecision{}, err
	}
	if err := requireLivePolicy(
		ctx, resolver, intent.Destination.RepositoryRef, intent.Route,
		request.PolicyRef, policy,
	); err != nil {
		return AdmissionDecision{}, err
	}
	authority, err := resolveAuthority(ctx, resolver, request.AuthorityRef)
	if err != nil {
		return AdmissionDecision{}, err
	}
	second, err := resolveOptionalAuthority(ctx, resolver, request.SecondAuthorityRef)
	if err != nil {
		return AdmissionDecision{}, err
	}
	if intent.PolicyRef != request.PolicyRef || intent.Policy != policy.Binding() ||
		intent.Route != policy.Route {
		return AdmissionDecision{}, fmt.Errorf("%w: intent policy", ErrBindingMismatch)
	}
	if now < intent.RequestedAt {
		return AdmissionDecision{}, fmt.Errorf("%w: admission predates intent", ErrInvalid)
	}
	if err := validateAuthorityRoute(intent.Route, policy, authority, second, now); err != nil {
		return AdmissionDecision{}, err
	}
	decision := AdmissionDecision{
		Schema: AdmissionDecisionContractV1, IntentRef: request.IntentRef, Intent: intent,
		Route: intent.Route, Destination: intent.Destination,
		PolicyRef: request.PolicyRef, Policy: policy,
		AuthorityRef: request.AuthorityRef, Authority: authority,
		SecondAuthorityRef: request.SecondAuthorityRef, SecondAuthority: second,
		DecidedAt: now,
	}
	if !policy.Enabled {
		decision.Disposition, decision.IssueAdmission = AdmissionDenied, IssueNotEvaluated
		return decision, decision.Validate()
	}
	if _, err := validateAdmissionGovernance(
		ctx, resolver, request.GovernanceRef, request.IntentRef, intent,
		request.PolicyRef, policy, request.AuthorityRef, request.SecondAuthorityRef, now,
	); err != nil {
		return AdmissionDecision{}, err
	}
	decision.Disposition = AdmissionAdmitted
	decision.GovernanceRef = request.GovernanceRef
	switch intent.Route {
	case RoutePRWithIssue:
		decision.IssueAdmission = IssueApproved
	case RoutePRWithoutIssue, RouteDirectMain, RouteEmergency:
		decision.IssueAdmission = IssueNotApplicable
	}
	return decision, decision.Validate()
}

type DeliveryRequest struct {
	AdmissionDecisionRef  string
	PolicyRef             string
	ReviewReceiptRef      string
	VerificationResultRef string
	CandidateAuthorityRef string
	GateRef               string
	AuthorityRef          string
	SecondAuthorityRef    string
}

// Decide accepts only native RAR receipt/result identities plus trusted PAD
// gate evidence. A caller-authored "complete" string is not part of this API.
func Decide(
	ctx context.Context,
	resolver TrustedResolver,
	rar RARAuthorityPort,
	request DeliveryRequest,
) (DeliveryDecision, error) {
	if request.CandidateAuthorityRef != "" {
		if err := validateDigest(
			"delivery candidate authority ref",
			request.CandidateAuthorityRef,
		); err != nil {
			return DeliveryDecision{}, err
		}
	}
	now, err := trustedNow(ctx, resolver)
	if err != nil {
		return DeliveryDecision{}, err
	}
	admission, err := ValidateAdmissionDecision(ctx, resolver, request.AdmissionDecisionRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	if admission.Disposition != AdmissionAdmitted {
		return DeliveryDecision{}, fmt.Errorf("%w: initial admission denied", ErrUnauthorized)
	}
	if _, err := admissionGovernanceDeadline(ctx, resolver, admission, now); err != nil {
		return DeliveryDecision{}, err
	}
	policy, err := resolvePolicy(ctx, resolver, request.PolicyRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	if err := requireLivePolicy(
		ctx, resolver, admission.Destination.RepositoryRef, admission.Route,
		request.PolicyRef, policy,
	); err != nil {
		return DeliveryDecision{}, err
	}
	review, err := resolveRARReview(
		ctx, rar, request.ReviewReceiptRef, request.VerificationResultRef,
	)
	if err != nil {
		return DeliveryDecision{}, err
	}
	gates, err := resolveGates(ctx, resolver, request.GateRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	authority, err := resolveAuthority(ctx, resolver, request.AuthorityRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	second, err := resolveOptionalAuthority(ctx, resolver, request.SecondAuthorityRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	if policy.Route != admission.Route ||
		gates.PolicyRef != request.PolicyRef || gates.Policy != policy.Binding() ||
		gates.Route != admission.Route ||
		now < gates.ObservedAt || now >= gates.ExpiresAt ||
		gates.Candidate.Digest != review.result.Subject.SnapshotIdentity ||
		!sameDestinationIdentity(admission.Destination, gates.Destination) {
		return DeliveryDecision{}, fmt.Errorf("%w: live delivery evidence", ErrBindingMismatch)
	}
	if err := validateAuthorityRoute(admission.Route, policy, authority, second, now); err != nil {
		return DeliveryDecision{}, err
	}
	decision := DeliveryDecision{
		Schema:                DeliveryDecisionContractV1,
		AdmissionDecisionRef:  request.AdmissionDecisionRef,
		AdmissionDecision:     admission,
		PolicyRef:             request.PolicyRef,
		Policy:                policy,
		ReviewReceiptRef:      request.ReviewReceiptRef,
		VerificationResultRef: request.VerificationResultRef,
		Candidate:             gates.Candidate,
		CandidateAuthorityRef: request.CandidateAuthorityRef,
		GateRef:               request.GateRef,
		Gates:                 gates,
		Disposition:           deliveryDisposition(policy, review.result.Aggregate),
		AuthorityRef:          request.AuthorityRef,
		Authority:             authority,
		SecondAuthorityRef:    request.SecondAuthorityRef,
		SecondAuthority:       second,
		DecidedAt:             now,
	}
	return decision, decision.Validate()
}

func ValidateAdmissionDecision(
	ctx context.Context,
	resolver TrustedResolver,
	ref string,
) (AdmissionDecision, error) {
	decision, err := resolveAdmission(ctx, resolver, ref)
	if err != nil {
		return AdmissionDecision{}, err
	}
	intent, err := resolveIntent(ctx, resolver, decision.IntentRef)
	if err != nil {
		return AdmissionDecision{}, err
	}
	policy, err := resolvePolicy(ctx, resolver, decision.PolicyRef)
	if err != nil {
		return AdmissionDecision{}, err
	}
	authority, err := resolveAuthority(ctx, resolver, decision.AuthorityRef)
	if err != nil {
		return AdmissionDecision{}, err
	}
	second, err := resolveOptionalAuthority(ctx, resolver, decision.SecondAuthorityRef)
	if err != nil {
		return AdmissionDecision{}, err
	}
	if decision.Intent != intent || decision.Policy != policy ||
		decision.Authority != authority || !sameOptionalAuthority(decision.SecondAuthority, second) ||
		decision.DecidedAt < intent.RequestedAt {
		return AdmissionDecision{}, fmt.Errorf("%w: admission trusted preimages", ErrBindingMismatch)
	}
	if decision.Disposition != AdmissionAdmitted {
		if decision.Disposition != AdmissionDenied {
			return AdmissionDecision{}, fmt.Errorf("%w: admission disposition", ErrInvalid)
		}
		return decision, nil
	}
	if _, err := validateAdmissionGovernance(
		ctx, resolver, decision.GovernanceRef, decision.IntentRef, intent,
		decision.PolicyRef, policy, decision.AuthorityRef, decision.SecondAuthorityRef,
		decision.DecidedAt,
	); err != nil {
		return AdmissionDecision{}, err
	}
	return decision, nil
}

func ValidateDeliveryDecision(
	ctx context.Context,
	resolver TrustedResolver,
	rar RARAuthorityPort,
	ref string,
) (DeliveryDecision, error) {
	decision, err := resolveDecision(ctx, resolver, ref)
	if err != nil {
		return DeliveryDecision{}, err
	}
	admission, err := ValidateAdmissionDecision(ctx, resolver, decision.AdmissionDecisionRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	if admission.Disposition != AdmissionAdmitted ||
		decision.DecidedAt < admission.DecidedAt {
		return DeliveryDecision{}, fmt.Errorf("%w: delivery admission/chronology", ErrUnauthorized)
	}
	policy, err := resolvePolicy(ctx, resolver, decision.PolicyRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	review, err := resolveRARReview(
		ctx, rar, decision.ReviewReceiptRef, decision.VerificationResultRef,
	)
	if err != nil {
		return DeliveryDecision{}, err
	}
	gates, err := resolveGates(ctx, resolver, decision.GateRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	authority, err := resolveAuthority(ctx, resolver, decision.AuthorityRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	second, err := resolveOptionalAuthority(ctx, resolver, decision.SecondAuthorityRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	if decision.AdmissionDecision != admission || decision.Policy != policy ||
		decision.Gates != gates || decision.Authority != authority ||
		!sameOptionalAuthority(decision.SecondAuthority, second) ||
		decision.Candidate != gates.Candidate ||
		decision.Candidate.Digest != review.result.Subject.SnapshotIdentity ||
		decision.Disposition != deliveryDisposition(policy, review.result.Aggregate) {
		return DeliveryDecision{}, fmt.Errorf("%w: decision trusted preimages", ErrBindingMismatch)
	}
	return decision, nil
}

func trustedNow(ctx context.Context, resolver TrustedResolver) (int64, error) {
	if resolver == nil {
		return 0, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	now, err := resolver.CurrentUnix(ctx)
	if err != nil {
		return 0, fmt.Errorf("%w: trusted clock: %v", ErrUntrustedPreimage, err)
	}
	if now <= 0 {
		return 0, fmt.Errorf("%w: trusted clock returned non-positive time", ErrInvalid)
	}
	return now, nil
}

func requireLivePolicy(
	ctx context.Context,
	resolver TrustedResolver,
	repositoryRef string,
	route Route,
	policyRef string,
	policy RoutePolicy,
) error {
	if resolver == nil {
		return fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	live, err := resolver.ResolveLiveRoutePolicy(ctx, repositoryRef, route)
	if err != nil {
		return fmt.Errorf("%w: live route policy: %v", ErrUntrustedPreimage, err)
	}
	if err := live.Validate(); err != nil {
		return fmt.Errorf("%w: invalid live route policy: %v", ErrUntrustedPreimage, err)
	}
	liveRef, _ := live.Ref()
	if liveRef != policyRef || live != policy ||
		live.Route != route {
		return fmt.Errorf("%w: stale route policy", ErrBindingMismatch)
	}
	return nil
}

func validateAdmissionGovernance(
	ctx context.Context,
	resolver TrustedResolver,
	governanceRef string,
	intentRef string,
	intent DeliveryIntent,
	policyRef string,
	policy RoutePolicy,
	authorityRef string,
	secondAuthorityRef string,
	at int64,
) (int64, error) {
	if intent.Route == RoutePRWithoutIssue || intent.Route == RouteDirectMain {
		if governanceRef != "" {
			return 0, fmt.Errorf("%w: issue-free route governance", ErrInvalid)
		}
		return 0, nil
	}
	governance, err := resolveGovernance(ctx, resolver, governanceRef)
	if err != nil {
		return 0, err
	}
	expectedKind := GovernanceApprovedIssue
	if intent.Route == RouteEmergency {
		expectedKind = GovernanceEmergency
	}
	if governance.Kind != expectedKind || governance.SubjectRef != intentRef ||
		governance.Route != intent.Route ||
		governance.RepositoryRef != intent.Destination.RepositoryRef ||
		governance.Candidate != nil || governance.Destination != intent.Destination ||
		governance.PolicyRef != policyRef || governance.Policy != policy.Binding() ||
		governance.AuthorityRef != authorityRef ||
		governance.SecondAuthorityRef != secondAuthorityRef ||
		at >= governance.ExpiresAt {
		return 0, fmt.Errorf("%w: admission governance binding/window", ErrBindingMismatch)
	}
	deadline := governance.ExpiresAt
	if governance.Kind == GovernanceEmergency {
		risk, err := validateGovernanceRisk(ctx, resolver, governance, at)
		if err != nil {
			return 0, err
		}
		if risk.ExpiresAt < deadline {
			deadline = risk.ExpiresAt
		}
	}
	return deadline, nil
}

func admissionGovernanceDeadline(
	ctx context.Context,
	resolver TrustedResolver,
	admission AdmissionDecision,
	at int64,
) (int64, error) {
	return validateAdmissionGovernance(
		ctx, resolver, admission.GovernanceRef, admission.IntentRef, admission.Intent,
		admission.PolicyRef, admission.Policy, admission.AuthorityRef,
		admission.SecondAuthorityRef, at,
	)
}

func resolveRARReview(
	ctx context.Context,
	rar RARAuthorityPort,
	receiptRef string,
	resultRef string,
) (resolvedRARReview, error) {
	if err := validateDigest("RAR receipt ref", receiptRef); err != nil {
		return resolvedRARReview{}, err
	}
	if err := validateDigest("RAR verification-result ref", resultRef); err != nil {
		return resolvedRARReview{}, err
	}
	if rar == nil {
		return resolvedRARReview{}, fmt.Errorf("%w: missing RAR authority port", ErrUntrustedPreimage)
	}
	authority, err := rar.ResolveTerminalReview(ctx, receiptRef, resultRef)
	if err != nil {
		return resolvedRARReview{}, fmt.Errorf("%w: RAR authority: %v", ErrUntrustedPreimage, err)
	}
	if err := authority.Validate(); err != nil {
		return resolvedRARReview{}, fmt.Errorf(
			"%w: invalid RAR verification authority: %v",
			ErrUntrustedPreimage,
			err,
		)
	}
	if authority.Receipt.ReceiptRef != receiptRef ||
		authority.Result.ResultRef != resultRef {
		return resolvedRARReview{}, fmt.Errorf(
			"%w: RAR receipt/result owner identity",
			ErrBindingMismatch,
		)
	}
	return resolvedRARReview{authority: authority, result: authority.Result}, nil
}

func resolveIntent(ctx context.Context, resolver TrustedResolver, ref string) (DeliveryIntent, error) {
	if resolver == nil {
		return DeliveryIntent{}, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	return resolveExact(ref, resolver.ResolveIntent, ctx, func(value DeliveryIntent) (string, error) { return value.Ref() })
}

func resolvePolicy(ctx context.Context, resolver TrustedResolver, ref string) (RoutePolicy, error) {
	if resolver == nil {
		return RoutePolicy{}, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	return resolveExact(ref, resolver.ResolveRoutePolicy, ctx, func(value RoutePolicy) (string, error) { return value.Ref() })
}

func resolveAuthority(ctx context.Context, resolver TrustedResolver, ref string) (AuthoritySignal, error) {
	if resolver == nil {
		return AuthoritySignal{}, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	return resolveExact(ref, resolver.ResolveAuthoritySignal, ctx, func(value AuthoritySignal) (string, error) { return value.Ref() })
}

func resolveOptionalAuthority(ctx context.Context, resolver TrustedResolver, ref string) (*AuthoritySignal, error) {
	if ref == "" {
		return nil, nil
	}
	value, err := resolveAuthority(ctx, resolver, ref)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func resolveAdmission(ctx context.Context, resolver TrustedResolver, ref string) (AdmissionDecision, error) {
	if resolver == nil {
		return AdmissionDecision{}, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	return resolveExact(ref, resolver.ResolveAdmissionDecision, ctx, func(value AdmissionDecision) (string, error) { return value.Ref() })
}

func resolveGates(ctx context.Context, resolver TrustedResolver, ref string) (GateEvidence, error) {
	if resolver == nil {
		return GateEvidence{}, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	return resolveExact(ref, resolver.ResolveGateEvidence, ctx, func(value GateEvidence) (string, error) { return value.Ref() })
}

func resolveDecision(ctx context.Context, resolver TrustedResolver, ref string) (DeliveryDecision, error) {
	if resolver == nil {
		return DeliveryDecision{}, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	return resolveExact(ref, resolver.ResolveDeliveryDecision, ctx, func(value DeliveryDecision) (string, error) { return value.Ref() })
}

func resolveExact[T any](
	ref string,
	load func(context.Context, string) (T, error),
	ctx context.Context,
	contentRef func(T) (string, error),
) (T, error) {
	var zero T
	if err := validateDigest("trusted preimage ref", ref); err != nil {
		return zero, err
	}
	value, err := load(ctx, ref)
	if err != nil {
		return zero, fmt.Errorf("%w: %v", ErrUntrustedPreimage, err)
	}
	actual, err := contentRef(value)
	if err != nil {
		return zero, fmt.Errorf("%w: invalid preimage: %v", ErrUntrustedPreimage, err)
	}
	if actual != ref {
		return zero, fmt.Errorf("%w: requested %s, resolved %s", ErrUntrustedPreimage, ref, actual)
	}
	return value, nil
}

func sameOptionalAuthority(left, right *AuthoritySignal) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
