package deliveryadmission

import (
	"context"
	"fmt"
)

type AuthorizationKind string

const (
	AuthorizationNormal    AuthorizationKind = "normal"
	AuthorizationException AuthorizationKind = "exception"
)

type AuthorizationBinding struct {
	Nonce                 string             `json:"nonce"`
	DecisionRef           string             `json:"decisionRef"`
	IntentRef             string             `json:"intentRef"`
	Route                 Route              `json:"route"`
	Candidate             CandidateBinding   `json:"candidate"`
	Destination           DestinationBinding `json:"destination"`
	PolicyRef             string             `json:"policyRef"`
	Policy                RoutePolicy        `json:"policy"`
	ReviewReceiptRef      string             `json:"reviewReceiptRef"`
	VerificationResultRef string             `json:"verificationResultRef"`
	AuthorityRef          string             `json:"authorityRef"`
	Authority             AuthoritySignal    `json:"authority"`
	IssuedAt              int64              `json:"issuedAt"`
	ExpiresAt             int64              `json:"expiresAt"`
	UseLimit              int                `json:"useLimit"`
}

func (binding AuthorizationBinding) Validate() error {
	if err := validateToken("authorization.nonce", binding.Nonce); err != nil {
		return err
	}
	for name, ref := range map[string]string{
		"authorization.decisionRef":           binding.DecisionRef,
		"authorization.intentRef":             binding.IntentRef,
		"authorization.reviewReceiptRef":      binding.ReviewReceiptRef,
		"authorization.verificationResultRef": binding.VerificationResultRef,
	} {
		if err := validateDigest(name, ref); err != nil {
			return err
		}
	}
	if err := exactEmbeddedRef("authorization.policyRef", binding.PolicyRef, binding.Policy.Ref); err != nil {
		return err
	}
	if err := exactEmbeddedRef("authorization.authorityRef", binding.AuthorityRef, binding.Authority.Ref); err != nil {
		return err
	}
	if !binding.Route.valid() || binding.Policy.Route != binding.Route ||
		binding.Authority.PolicyRef != binding.PolicyRef ||
		binding.Authority.Policy != binding.Policy.Binding() ||
		binding.UseLimit != 1 {
		return fmt.Errorf("%w: authorization preimages", ErrBindingMismatch)
	}
	if err := binding.Candidate.validate(); err != nil {
		return err
	}
	if err := binding.Destination.validate(); err != nil {
		return err
	}
	if binding.IssuedAt <= 0 || binding.ExpiresAt <= binding.IssuedAt ||
		binding.ExpiresAt > binding.Authority.ExpiresAt {
		return fmt.Errorf("%w: authorization window", ErrInvalid)
	}
	return validateAuthorizationIssuer(
		binding.Route, binding.Policy, binding.Authority, binding.IssuedAt,
	)
}

func validateAuthorizationIssuer(route Route, policy RoutePolicy, authority AuthoritySignal, at int64) error {
	if err := authority.Validate(); err != nil {
		return err
	}
	policyRef, _ := policy.Ref()
	if authority.PolicyRef != policyRef || authority.Policy != policy.Binding() || at >= authority.ExpiresAt {
		return fmt.Errorf("%w: authorization issuer binding/window", ErrUnauthorized)
	}
	if route == RoutePRWithIssue {
		if authority.Provenance == ProvenanceRepositoryPolicy ||
			authority.Provenance == ProvenanceMaintainerControl {
			return nil
		}
	} else if authority.Provenance == ProvenanceMaintainerControl {
		return nil
	}
	return fmt.Errorf("%w: authorization issuer provenance", ErrUnauthorized)
}

type DeliveryAuthorization struct {
	Schema  string               `json:"schema"`
	Binding AuthorizationBinding `json:"binding"`
}

// Validate proves canonical self-consistency. Authority-sensitive callers use
// ValidateAuthorization, which re-resolves the exact PAD and RAR preimages.
func (authorization DeliveryAuthorization) Validate() error {
	if authorization.Schema != AuthorizationContractV1 {
		return fmt.Errorf("%w: normal authorization schema", ErrInvalid)
	}
	if err := authorization.Binding.Validate(); err != nil {
		return err
	}
	if authorization.Binding.ExpiresAt-authorization.Binding.IssuedAt >
		authorization.Binding.Policy.AuthorizationTTLSeconds {
		return fmt.Errorf("%w: normal authorization TTL", ErrUnauthorized)
	}
	return nil
}

func (authorization DeliveryAuthorization) Ref() (string, error) {
	return contentRef(authorization, authorization.Validate())
}

type DeliveryExceptionAuthorization struct {
	Schema           string               `json:"schema"`
	Binding          AuthorizationBinding `json:"binding"`
	HumanDecisionRef string               `json:"humanDecisionRef"`
	ResidualRiskRef  string               `json:"residualRiskRef"`
}

// Validate is structural only. ValidateExceptionAuthorization re-resolves the
// owner-authored human decision, residual risk, and native RAR result.
func (authorization DeliveryExceptionAuthorization) Validate() error {
	if authorization.Schema != ExceptionAuthorizationContractV1 {
		return fmt.Errorf("%w: exception authorization schema", ErrInvalid)
	}
	if err := authorization.Binding.Validate(); err != nil {
		return err
	}
	if !authorization.Binding.Policy.AllowIncomplete ||
		authorization.Binding.Authority.Provenance != ProvenanceMaintainerControl ||
		authorization.Binding.ExpiresAt-authorization.Binding.IssuedAt >
			authorization.Binding.Policy.ExceptionTTLSeconds {
		return fmt.Errorf("%w: exception policy/TTL", ErrExceptionNotPermitted)
	}
	if err := validateDigest("exception.humanDecisionRef", authorization.HumanDecisionRef); err != nil {
		return err
	}
	return validateDigest("exception.residualRiskRef", authorization.ResidualRiskRef)
}

func (authorization DeliveryExceptionAuthorization) Ref() (string, error) {
	return contentRef(authorization, authorization.Validate())
}

type AuthorizationIssueRequest struct {
	DecisionRef  string
	Nonce        string
	AuthorityRef string
}

func IssueAuthorization(
	ctx context.Context,
	resolver TrustedResolver,
	rar RARAuthorityPort,
	request AuthorizationIssueRequest,
) (DeliveryAuthorization, error) {
	now, err := trustedNow(ctx, resolver)
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	decision, err := ValidateDeliveryDecision(ctx, resolver, rar, request.DecisionRef)
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	if decision.Disposition != DeliveryAuthorizeNormal {
		return DeliveryAuthorization{}, fmt.Errorf("%w: normal authorization outcome", ErrUnauthorized)
	}
	if err := requireLivePolicy(
		ctx, resolver, decision.Gates.Destination.RepositoryRef,
		decision.AdmissionDecision.Route, decision.PolicyRef, decision.Policy,
	); err != nil {
		return DeliveryAuthorization{}, err
	}
	authority, err := resolveAuthority(ctx, resolver, request.AuthorityRef)
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	governanceDeadline, err := admissionGovernanceDeadline(ctx, resolver, decision.AdmissionDecision, now)
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	decisionAuthorityDeadline, err := validateDecisionAuthorityAt(decision, now)
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	binding, err := buildAuthorizationBinding(
		decision, request, authority, now, decision.Policy.AuthorizationTTLSeconds,
		governanceDeadline, decision.Gates.ExpiresAt, decisionAuthorityDeadline,
	)
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	authorization := DeliveryAuthorization{Schema: AuthorizationContractV1, Binding: binding}
	return authorization, authorization.Validate()
}

type ExceptionAuthorizationIssueRequest struct {
	AuthorizationIssueRequest
	HumanDecisionRef string
}

func IssueExceptionAuthorization(
	ctx context.Context,
	resolver TrustedResolver,
	rar RARAuthorityPort,
	request ExceptionAuthorizationIssueRequest,
) (DeliveryExceptionAuthorization, error) {
	now, err := trustedNow(ctx, resolver)
	if err != nil {
		return DeliveryExceptionAuthorization{}, err
	}
	decision, err := ValidateDeliveryDecision(ctx, resolver, rar, request.DecisionRef)
	if err != nil {
		return DeliveryExceptionAuthorization{}, err
	}
	if decision.Disposition != DeliveryRequireException ||
		!decision.Policy.AllowIncomplete {
		return DeliveryExceptionAuthorization{}, fmt.Errorf("%w: final decision", ErrExceptionNotPermitted)
	}
	if err := requireLivePolicy(
		ctx, resolver, decision.Gates.Destination.RepositoryRef,
		decision.AdmissionDecision.Route, decision.PolicyRef, decision.Policy,
	); err != nil {
		return DeliveryExceptionAuthorization{}, err
	}
	authority, err := resolveAuthority(ctx, resolver, request.AuthorityRef)
	if err != nil {
		return DeliveryExceptionAuthorization{}, err
	}
	if authority.Provenance != ProvenanceMaintainerControl {
		return DeliveryExceptionAuthorization{}, fmt.Errorf("%w: exception issuer", ErrUnauthorized)
	}
	governance, risk, err := validateExceptionGovernance(
		ctx, resolver, request.HumanDecisionRef, request.DecisionRef,
		decision, request.AuthorityRef, now,
	)
	if err != nil {
		return DeliveryExceptionAuthorization{}, err
	}
	admissionDeadline, err := admissionGovernanceDeadline(
		ctx, resolver, decision.AdmissionDecision, now,
	)
	if err != nil {
		return DeliveryExceptionAuthorization{}, err
	}
	decisionAuthorityDeadline, err := validateDecisionAuthorityAt(decision, now)
	if err != nil {
		return DeliveryExceptionAuthorization{}, err
	}
	binding, err := buildAuthorizationBinding(
		decision, request.AuthorizationIssueRequest, authority, now,
		decision.Policy.ExceptionTTLSeconds,
		admissionDeadline, decision.Gates.ExpiresAt, decisionAuthorityDeadline,
		governance.ExpiresAt, risk.ExpiresAt,
	)
	if err != nil {
		return DeliveryExceptionAuthorization{}, err
	}
	authorization := DeliveryExceptionAuthorization{
		Schema: ExceptionAuthorizationContractV1, Binding: binding,
		HumanDecisionRef: request.HumanDecisionRef,
		ResidualRiskRef:  governance.ResidualRiskRef,
	}
	if riskRef, _ := risk.Ref(); riskRef != authorization.ResidualRiskRef {
		return DeliveryExceptionAuthorization{}, fmt.Errorf("%w: exception residual risk", ErrBindingMismatch)
	}
	return authorization, authorization.Validate()
}

func buildAuthorizationBinding(
	decision DeliveryDecision,
	request AuthorizationIssueRequest,
	authority AuthoritySignal,
	issuedAt int64,
	maximumTTL int64,
	deadlines ...int64,
) (AuthorizationBinding, error) {
	if err := validateToken("authorization.nonce", request.Nonce); err != nil {
		return AuthorizationBinding{}, err
	}
	if issuedAt < decision.DecidedAt || maximumTTL <= 0 {
		return AuthorizationBinding{}, fmt.Errorf("%w: authorization window", ErrUnauthorized)
	}
	expiresAt := issuedAt + maximumTTL
	if expiresAt <= issuedAt {
		return AuthorizationBinding{}, fmt.Errorf("%w: authorization TTL overflow", ErrInvalid)
	}
	if expiresAt > authority.ExpiresAt {
		expiresAt = authority.ExpiresAt
	}
	for _, deadline := range deadlines {
		if deadline > 0 && expiresAt > deadline {
			expiresAt = deadline
		}
	}
	if expiresAt <= issuedAt {
		return AuthorizationBinding{}, fmt.Errorf("%w: expired authorization authority", ErrExpired)
	}
	if err := validateAuthorizationIssuer(
		decision.AdmissionDecision.Route, decision.Policy, authority, issuedAt,
	); err != nil {
		return AuthorizationBinding{}, err
	}
	return AuthorizationBinding{
		Nonce: request.Nonce, DecisionRef: request.DecisionRef,
		IntentRef: decision.AdmissionDecision.IntentRef,
		Route:     decision.AdmissionDecision.Route, Candidate: decision.Candidate,
		Destination:           decision.Gates.Destination,
		PolicyRef:             decision.PolicyRef,
		Policy:                decision.Policy,
		ReviewReceiptRef:      decision.ReviewReceiptRef,
		VerificationResultRef: decision.VerificationResultRef,
		AuthorityRef:          request.AuthorityRef,
		Authority:             authority,
		IssuedAt:              issuedAt,
		ExpiresAt:             expiresAt,
		UseLimit:              1,
	}, nil
}

func ValidateAuthorization(
	ctx context.Context,
	resolver TrustedResolver,
	rar RARAuthorityPort,
	ref string,
) (DeliveryAuthorization, error) {
	if resolver == nil {
		return DeliveryAuthorization{}, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	authorization, err := resolveExact(
		ref, resolver.ResolveAuthorization, ctx,
		func(value DeliveryAuthorization) (string, error) { return value.Ref() },
	)
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	decision, err := validateTrustedAuthorizationBinding(ctx, resolver, rar, authorization.Binding)
	if err != nil {
		return DeliveryAuthorization{}, err
	}
	if decision.Disposition != DeliveryAuthorizeNormal {
		return DeliveryAuthorization{}, fmt.Errorf("%w: normal authorization decision", ErrUnauthorized)
	}
	return authorization, nil
}

func ValidateExceptionAuthorization(
	ctx context.Context,
	resolver TrustedResolver,
	rar RARAuthorityPort,
	ref string,
) (DeliveryExceptionAuthorization, error) {
	if resolver == nil {
		return DeliveryExceptionAuthorization{}, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	authorization, err := resolveExact(
		ref, resolver.ResolveExceptionAuthorization, ctx,
		func(value DeliveryExceptionAuthorization) (string, error) { return value.Ref() },
	)
	if err != nil {
		return DeliveryExceptionAuthorization{}, err
	}
	decision, err := validateTrustedAuthorizationBinding(ctx, resolver, rar, authorization.Binding)
	if err != nil {
		return DeliveryExceptionAuthorization{}, err
	}
	if decision.Disposition != DeliveryRequireException {
		return DeliveryExceptionAuthorization{}, fmt.Errorf("%w: exception authorization decision", ErrExceptionNotPermitted)
	}
	governance, risk, err := validateExceptionGovernance(
		ctx, resolver, authorization.HumanDecisionRef, authorization.Binding.DecisionRef,
		decision, authorization.Binding.AuthorityRef, authorization.Binding.IssuedAt,
	)
	if err != nil {
		return DeliveryExceptionAuthorization{}, err
	}
	riskRef, _ := risk.Ref()
	if governance.ResidualRiskRef != authorization.ResidualRiskRef ||
		riskRef != authorization.ResidualRiskRef ||
		authorization.Binding.ExpiresAt > governance.ExpiresAt ||
		authorization.Binding.ExpiresAt > risk.ExpiresAt {
		return DeliveryExceptionAuthorization{}, fmt.Errorf("%w: exception trusted residual risk", ErrBindingMismatch)
	}
	return authorization, nil
}

func validateTrustedAuthorizationBinding(
	ctx context.Context,
	resolver TrustedResolver,
	rar RARAuthorityPort,
	binding AuthorizationBinding,
) (DeliveryDecision, error) {
	decision, err := ValidateDeliveryDecision(ctx, resolver, rar, binding.DecisionRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	policy, err := resolvePolicy(ctx, resolver, binding.PolicyRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	authority, err := resolveAuthority(ctx, resolver, binding.AuthorityRef)
	if err != nil {
		return DeliveryDecision{}, err
	}
	if decision.Policy != binding.Policy || policy != binding.Policy ||
		authority != binding.Authority ||
		decision.AdmissionDecision.IntentRef != binding.IntentRef ||
		decision.AdmissionDecision.Route != binding.Route ||
		decision.Candidate != binding.Candidate ||
		decision.Gates.Destination != binding.Destination ||
		decision.ReviewReceiptRef != binding.ReviewReceiptRef ||
		decision.VerificationResultRef != binding.VerificationResultRef ||
		binding.IssuedAt < decision.DecidedAt ||
		binding.ExpiresAt > decision.Gates.ExpiresAt {
		return DeliveryDecision{}, fmt.Errorf("%w: authorization trusted preimages", ErrBindingMismatch)
	}
	governanceDeadline, err := admissionGovernanceDeadline(
		ctx, resolver, decision.AdmissionDecision, binding.IssuedAt,
	)
	if err != nil {
		return DeliveryDecision{}, err
	}
	if governanceDeadline > 0 && binding.ExpiresAt > governanceDeadline {
		return DeliveryDecision{}, fmt.Errorf("%w: authorization exceeds route governance", ErrBindingMismatch)
	}
	decisionAuthorityDeadline, err := validateDecisionAuthorityAt(decision, binding.IssuedAt)
	if err != nil {
		return DeliveryDecision{}, err
	}
	if binding.ExpiresAt > decisionAuthorityDeadline {
		return DeliveryDecision{}, fmt.Errorf("%w: authorization exceeds decision authority", ErrBindingMismatch)
	}
	return decision, nil
}

func validateExceptionGovernance(
	ctx context.Context,
	resolver TrustedResolver,
	governanceRef string,
	decisionRef string,
	decision DeliveryDecision,
	authorityRef string,
	at int64,
) (GovernanceDecision, ResidualRisk, error) {
	governance, err := resolveGovernance(ctx, resolver, governanceRef)
	if err != nil {
		return GovernanceDecision{}, ResidualRisk{}, err
	}
	candidate := decision.Candidate
	if governance.Kind != GovernanceException ||
		governance.SubjectRef != decisionRef ||
		governance.Route != decision.AdmissionDecision.Route ||
		governance.RepositoryRef != decision.Gates.Destination.RepositoryRef ||
		governance.Candidate == nil || *governance.Candidate != candidate ||
		governance.Destination != decision.Gates.Destination ||
		governance.PolicyRef != decision.PolicyRef ||
		governance.Policy != decision.Policy.Binding() ||
		governance.AuthorityRef != authorityRef ||
		governance.SecondAuthorityRef != decision.SecondAuthorityRef ||
		at >= governance.ExpiresAt {
		return GovernanceDecision{}, ResidualRisk{},
			fmt.Errorf("%w: exception governance binding/window", ErrBindingMismatch)
	}
	risk, err := validateGovernanceRisk(ctx, resolver, governance, at)
	if err != nil {
		return GovernanceDecision{}, ResidualRisk{}, err
	}
	return governance, risk, nil
}

func validateDecisionAuthorityAt(decision DeliveryDecision, at int64) (int64, error) {
	if err := validateAuthorityRoute(
		decision.AdmissionDecision.Route, decision.Policy, decision.Authority,
		decision.SecondAuthority, at,
	); err != nil {
		return 0, err
	}
	deadline := decision.Authority.ExpiresAt
	if decision.SecondAuthority != nil && decision.SecondAuthority.ExpiresAt < deadline {
		deadline = decision.SecondAuthority.ExpiresAt
	}
	return deadline, nil
}
