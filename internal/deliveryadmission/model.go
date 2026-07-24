// Package deliveryadmission defines PAD-owned delivery contracts. It is
// intentionally dormant: nothing here mutates Git, GitHub, or workflow state.
package deliveryadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	RoutePolicyContractV1            = "gentle-ai.delivery-route-policy/v1"
	AuthoritySignalContractV1        = "gentle-ai.delivery-authority-signal/v1"
	IntentContractV1                 = "gentle-ai.delivery-intent/v1"
	AdmissionDecisionContractV1      = "gentle-ai.delivery-admission-decision/v1"
	GateEvidenceContractV1           = "gentle-ai.delivery-gate-evidence/v1"
	DeliveryDecisionContractV1       = "gentle-ai.delivery-decision/v1"
	AuthorizationContractV1          = "gentle-ai.delivery-authorization/v1"
	ExceptionAuthorizationContractV1 = "gentle-ai.delivery-exception-authorization/v1"
	AuthorizationUseContractV1       = "gentle-ai.delivery-authorization-use/v1"
)

var (
	ErrInvalid               = errors.New("invalid delivery contract")
	ErrUnauthorized          = errors.New("delivery is not authorized")
	ErrUntrustedPreimage     = errors.New("untrusted delivery preimage")
	ErrBindingMismatch       = errors.New("delivery binding mismatch")
	ErrExpired               = errors.New("delivery authorization expired")
	ErrAlreadyConsumed       = errors.New("delivery authorization already consumed")
	ErrExceptionNotPermitted = errors.New("delivery exception is not permitted")
)

type Route string

const (
	RoutePRWithIssue    Route = "pr_with_issue"
	RoutePRWithoutIssue Route = "pr_without_issue"
	RouteDirectMain     Route = "direct_main"
	RouteEmergency      Route = "emergency"
)

func (route Route) valid() bool {
	return route == RoutePRWithIssue || route == RoutePRWithoutIssue ||
		route == RouteDirectMain || route == RouteEmergency
}

type CandidateBinding struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

func (binding CandidateBinding) validate() error {
	if err := validateToken("candidate.ref", binding.Ref); err != nil {
		return err
	}
	return validateDigest("candidate.digest", binding.Digest)
}

type DestinationBinding struct {
	RepositoryRef    string `json:"repositoryRef"`
	TargetRef        string `json:"targetRef"`
	ObservedRevision string `json:"observedRevision"`
	DefaultBranch    bool   `json:"defaultBranch"`
}

func (binding DestinationBinding) validate() error {
	if err := validateToken("destination.repositoryRef", binding.RepositoryRef); err != nil {
		return err
	}
	if !strings.HasPrefix(binding.TargetRef, "refs/heads/") {
		return fmt.Errorf("%w: destination target must use refs/heads", ErrInvalid)
	}
	if err := validateToken("destination.targetRef", binding.TargetRef); err != nil {
		return err
	}
	return validateToken("destination.observedRevision", binding.ObservedRevision)
}

func sameDestinationIdentity(left, right DestinationBinding) bool {
	return left.RepositoryRef == right.RepositoryRef &&
		left.TargetRef == right.TargetRef &&
		left.DefaultBranch == right.DefaultBranch
}

type PolicyBinding struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Digest   string `json:"digest"`
}

func (binding PolicyBinding) validate() error {
	if err := validateToken("policy.id", binding.ID); err != nil {
		return err
	}
	if err := validateToken("policy.revision", binding.Revision); err != nil {
		return err
	}
	return validateDigest("policy.digest", binding.Digest)
}

type RoutePolicy struct {
	Schema                  string `json:"schema"`
	ID                      string `json:"id"`
	Revision                string `json:"revision"`
	Digest                  string `json:"digest"`
	Route                   Route  `json:"route"`
	Enabled                 bool   `json:"enabled"`
	AllowIncomplete         bool   `json:"allowIncomplete"`
	RequireSecondMaintainer bool   `json:"requireSecondMaintainer"`
	AuthorizationTTLSeconds int64  `json:"authorizationTTLSeconds"`
	ExceptionTTLSeconds     int64  `json:"exceptionTTLSeconds"`
}

type routePolicyProjection struct {
	Schema                  string `json:"schema"`
	ID                      string `json:"id"`
	Revision                string `json:"revision"`
	Route                   Route  `json:"route"`
	Enabled                 bool   `json:"enabled"`
	AllowIncomplete         bool   `json:"allowIncomplete"`
	RequireSecondMaintainer bool   `json:"requireSecondMaintainer"`
	AuthorizationTTLSeconds int64  `json:"authorizationTTLSeconds"`
	ExceptionTTLSeconds     int64  `json:"exceptionTTLSeconds"`
}

func NewRoutePolicy(
	id, revision string,
	route Route,
	enabled, allowIncomplete, requireSecondMaintainer bool,
	authorizationTTLSeconds, exceptionTTLSeconds int64,
) (RoutePolicy, error) {
	policy := RoutePolicy{
		Schema: RoutePolicyContractV1, ID: id, Revision: revision, Route: route,
		Enabled: enabled, AllowIncomplete: allowIncomplete,
		RequireSecondMaintainer: requireSecondMaintainer,
		AuthorizationTTLSeconds: authorizationTTLSeconds,
		ExceptionTTLSeconds:     exceptionTTLSeconds,
	}
	if err := policy.validateShape(); err != nil {
		return RoutePolicy{}, err
	}
	policy.Digest = hashValue(policy.projection())
	return policy, nil
}

func (policy RoutePolicy) projection() routePolicyProjection {
	return routePolicyProjection{
		Schema: policy.Schema, ID: policy.ID, Revision: policy.Revision, Route: policy.Route,
		Enabled: policy.Enabled, AllowIncomplete: policy.AllowIncomplete,
		RequireSecondMaintainer: policy.RequireSecondMaintainer,
		AuthorizationTTLSeconds: policy.AuthorizationTTLSeconds,
		ExceptionTTLSeconds:     policy.ExceptionTTLSeconds,
	}
}

func (policy RoutePolicy) validateShape() error {
	if policy.Schema != RoutePolicyContractV1 {
		return fmt.Errorf("%w: route policy schema", ErrInvalid)
	}
	if err := validateToken("policy.id", policy.ID); err != nil {
		return err
	}
	if err := validateToken("policy.revision", policy.Revision); err != nil {
		return err
	}
	if !policy.Route.valid() || policy.AuthorizationTTLSeconds <= 0 {
		return fmt.Errorf("%w: route policy route/TTL", ErrInvalid)
	}
	if policy.AllowIncomplete != (policy.ExceptionTTLSeconds > 0) {
		return fmt.Errorf("%w: exception policy/TTL", ErrInvalid)
	}
	if policy.RequireSecondMaintainer && policy.Route != RouteEmergency {
		return fmt.Errorf("%w: second maintainer is emergency-only", ErrInvalid)
	}
	return nil
}

func (policy RoutePolicy) Validate() error {
	if err := policy.validateShape(); err != nil {
		return err
	}
	if policy.Digest != hashValue(policy.projection()) {
		return fmt.Errorf("%w: route policy digest", ErrBindingMismatch)
	}
	return nil
}

func (policy RoutePolicy) Binding() PolicyBinding {
	return PolicyBinding{ID: policy.ID, Revision: policy.Revision, Digest: policy.Digest}
}

func (policy RoutePolicy) Ref() (string, error) {
	return contentRef(policy, policy.Validate())
}

type AuthorityProvenance string

const (
	// ProvenanceRepositoryPolicy must resolve from protected repository
	// policy, never files, labels, or workflow output in a candidate PR head.
	ProvenanceRepositoryPolicy AuthorityProvenance = "protected_repository_policy"
	// ProvenanceMaintainerControl is an owner-controlled signal outside the
	// candidate. PR-without-issue, direct-main, and emergency require it.
	ProvenanceMaintainerControl AuthorityProvenance = "maintainer_control_plane"
	ProvenanceSecondMaintainer  AuthorityProvenance = "independent_maintainer_control_plane"
)

type AuthoritySignal struct {
	Schema     string              `json:"schema"`
	SignalID   string              `json:"signalId"`
	IssuerRef  string              `json:"issuerRef"`
	Provenance AuthorityProvenance `json:"provenance"`
	PolicyRef  string              `json:"policyRef"`
	Policy     PolicyBinding       `json:"policy"`
	ExpiresAt  int64               `json:"expiresAt"`
}

func (signal AuthoritySignal) Validate() error {
	if signal.Schema != AuthoritySignalContractV1 || signal.ExpiresAt <= 0 {
		return fmt.Errorf("%w: authority signal header", ErrInvalid)
	}
	if err := validateToken("authority.signalId", signal.SignalID); err != nil {
		return err
	}
	if err := validateToken("authority.issuerRef", signal.IssuerRef); err != nil {
		return err
	}
	if err := validateDigest("authority.policyRef", signal.PolicyRef); err != nil {
		return err
	}
	if err := signal.Policy.validate(); err != nil {
		return err
	}
	switch signal.Provenance {
	case ProvenanceRepositoryPolicy, ProvenanceMaintainerControl, ProvenanceSecondMaintainer:
		return nil
	default:
		return fmt.Errorf("%w: authority provenance", ErrInvalid)
	}
}

func (signal AuthoritySignal) Ref() (string, error) {
	return contentRef(signal, signal.Validate())
}

type DeliveryIntent struct {
	Schema      string             `json:"schema"`
	Nonce       string             `json:"nonce"`
	Route       Route              `json:"route"`
	ScopeDigest string             `json:"scopeDigest"`
	Destination DestinationBinding `json:"destination"`
	PolicyRef   string             `json:"policyRef"`
	Policy      PolicyBinding      `json:"policy"`
	RequestedAt int64              `json:"requestedAt"`
}

func NewIntent(
	nonce string,
	route Route,
	scopeDigest string,
	destination DestinationBinding,
	policyRef string,
	policy PolicyBinding,
	at int64,
) (DeliveryIntent, error) {
	intent := DeliveryIntent{
		Schema: IntentContractV1, Nonce: nonce, Route: route, ScopeDigest: scopeDigest,
		Destination: destination, PolicyRef: policyRef, Policy: policy, RequestedAt: at,
	}
	return intent, intent.Validate()
}

func (intent DeliveryIntent) Validate() error {
	if intent.Schema != IntentContractV1 || !intent.Route.valid() || intent.RequestedAt <= 0 {
		return fmt.Errorf("%w: intent header", ErrInvalid)
	}
	if err := validateToken("intent.nonce", intent.Nonce); err != nil {
		return err
	}
	if err := validateDigest("intent.scopeDigest", intent.ScopeDigest); err != nil {
		return err
	}
	if err := intent.Destination.validate(); err != nil {
		return err
	}
	if err := validateDigest("intent.policyRef", intent.PolicyRef); err != nil {
		return err
	}
	if err := intent.Policy.validate(); err != nil {
		return err
	}
	if (intent.Route == RouteDirectMain || intent.Route == RouteEmergency) &&
		!intent.Destination.DefaultBranch {
		return fmt.Errorf("%w: direct intent destination", ErrInvalid)
	}
	return nil
}

func (intent DeliveryIntent) Ref() (string, error) {
	return contentRef(intent, intent.Validate())
}

type AdmissionDisposition string

const (
	AdmissionAdmitted AdmissionDisposition = "admitted"
	AdmissionDenied   AdmissionDisposition = "denied"
)

type IssueAdmission string

const (
	IssueApproved      IssueAdmission = "approved"
	IssueNotApplicable IssueAdmission = "not_applicable"
	IssueNotEvaluated  IssueAdmission = "not_evaluated"
)

type AdmissionDecision struct {
	Schema             string               `json:"schema"`
	IntentRef          string               `json:"intentRef"`
	Intent             DeliveryIntent       `json:"intent"`
	Route              Route                `json:"route"`
	Destination        DestinationBinding   `json:"destination"`
	PolicyRef          string               `json:"policyRef"`
	Policy             RoutePolicy          `json:"policy"`
	Disposition        AdmissionDisposition `json:"disposition"`
	IssueAdmission     IssueAdmission       `json:"issueAdmission"`
	GovernanceRef      string               `json:"governanceRef,omitempty"`
	AuthorityRef       string               `json:"authorityRef"`
	Authority          AuthoritySignal      `json:"authority"`
	SecondAuthorityRef string               `json:"secondAuthorityRef,omitempty"`
	SecondAuthority    *AuthoritySignal     `json:"secondAuthority,omitempty"`
	DecidedAt          int64                `json:"decidedAt"`
}

func (decision AdmissionDecision) Validate() error {
	if decision.Schema != AdmissionDecisionContractV1 || !decision.Route.valid() || decision.DecidedAt <= 0 {
		return fmt.Errorf("%w: admission header", ErrInvalid)
	}
	if err := exactEmbeddedRef("admission.intentRef", decision.IntentRef, decision.Intent.Ref); err != nil {
		return err
	}
	if err := exactEmbeddedRef("admission.policyRef", decision.PolicyRef, decision.Policy.Ref); err != nil {
		return err
	}
	if err := exactEmbeddedRef("admission.authorityRef", decision.AuthorityRef, decision.Authority.Ref); err != nil {
		return err
	}
	if decision.Intent.Route != decision.Route || decision.Policy.Route != decision.Route ||
		decision.Intent.PolicyRef != decision.PolicyRef ||
		decision.Intent.Policy != decision.Policy.Binding() ||
		decision.Destination != decision.Intent.Destination ||
		decision.DecidedAt < decision.Intent.RequestedAt ||
		decision.Authority.PolicyRef != decision.PolicyRef ||
		decision.Authority.Policy != decision.Policy.Binding() {
		return fmt.Errorf("%w: admission preimages", ErrBindingMismatch)
	}
	if decision.SecondAuthority == nil {
		if decision.SecondAuthorityRef != "" {
			return fmt.Errorf("%w: absent second authority", ErrBindingMismatch)
		}
	} else if err := exactEmbeddedRef("admission.secondAuthorityRef", decision.SecondAuthorityRef, decision.SecondAuthority.Ref); err != nil {
		return err
	} else if decision.SecondAuthority.PolicyRef != decision.PolicyRef ||
		decision.SecondAuthority.Policy != decision.Policy.Binding() {
		return fmt.Errorf("%w: second authority policy", ErrBindingMismatch)
	}
	if err := validateAuthorityRoute(
		decision.Route, decision.Policy, decision.Authority,
		decision.SecondAuthority, decision.DecidedAt,
	); err != nil {
		return err
	}
	if decision.Disposition == AdmissionDenied {
		if decision.Policy.Enabled || decision.IssueAdmission != IssueNotEvaluated ||
			decision.GovernanceRef != "" {
			return fmt.Errorf("%w: malformed admission denial", ErrInvalid)
		}
		return nil
	}
	if decision.Disposition != AdmissionAdmitted || !decision.Policy.Enabled {
		return fmt.Errorf("%w: malformed admission", ErrInvalid)
	}
	switch decision.Route {
	case RoutePRWithIssue:
		if decision.IssueAdmission != IssueApproved {
			return fmt.Errorf("%w: PR-with-issue governance", ErrInvalid)
		}
		return validateDigest("admission.governanceRef", decision.GovernanceRef)
	case RoutePRWithoutIssue, RouteDirectMain:
		if decision.IssueAdmission != IssueNotApplicable ||
			decision.GovernanceRef != "" {
			return fmt.Errorf("%w: issue-free governance", ErrInvalid)
		}
	case RouteEmergency:
		if decision.IssueAdmission != IssueNotApplicable {
			return fmt.Errorf("%w: emergency issue governance", ErrInvalid)
		}
		return validateDigest("admission.governanceRef", decision.GovernanceRef)
	}
	return nil
}

func (decision AdmissionDecision) Ref() (string, error) {
	return contentRef(decision, decision.Validate())
}

type Mechanism string

const (
	MechanismPullRequest     Mechanism = "pull_request"
	MechanismFastForwardOnly Mechanism = "fast_forward_only"
)

type ProtectionMode string

const ProtectionRespect ProtectionMode = "respect"

type GateEvidence struct {
	Schema             string             `json:"schema"`
	Route              Route              `json:"route"`
	Candidate          CandidateBinding   `json:"candidate"`
	Destination        DestinationBinding `json:"destination"`
	PolicyRef          string             `json:"policyRef"`
	Policy             PolicyBinding      `json:"policy"`
	Mechanism          Mechanism          `json:"mechanism"`
	ProtectionMode     ProtectionMode     `json:"protectionMode"`
	PullRequestRef     string             `json:"pullRequestRef,omitempty"`
	RequiredChecksRef  string             `json:"requiredChecksRef,omitempty"`
	RemoteFreshnessRef string             `json:"remoteFreshnessRef"`
	ProtectionRef      string             `json:"protectionRef"`
	ObservedAt         int64              `json:"observedAt"`
	ExpiresAt          int64              `json:"expiresAt"`
}

func (evidence GateEvidence) Validate() error {
	if evidence.Schema != GateEvidenceContractV1 || !evidence.Route.valid() ||
		evidence.ObservedAt <= 0 || evidence.ExpiresAt <= evidence.ObservedAt {
		return fmt.Errorf("%w: gate evidence header", ErrInvalid)
	}
	if err := evidence.Candidate.validate(); err != nil {
		return err
	}
	if err := evidence.Destination.validate(); err != nil {
		return err
	}
	if err := validateDigest("gate.policyRef", evidence.PolicyRef); err != nil {
		return err
	}
	if err := evidence.Policy.validate(); err != nil {
		return err
	}
	if evidence.ProtectionMode != ProtectionRespect {
		return fmt.Errorf("%w: branch protection must be respected", ErrUnauthorized)
	}
	if err := validateDigest("gate.remoteFreshnessRef", evidence.RemoteFreshnessRef); err != nil {
		return err
	}
	if err := validateDigest("gate.protectionRef", evidence.ProtectionRef); err != nil {
		return err
	}
	if evidence.Route == RoutePRWithIssue || evidence.Route == RoutePRWithoutIssue ||
		(evidence.Route == RouteEmergency && evidence.Mechanism == MechanismPullRequest) {
		if evidence.Mechanism != MechanismPullRequest {
			return fmt.Errorf("%w: route requires pull request", ErrInvalid)
		}
		if err := validateToken("gate.pullRequestRef", evidence.PullRequestRef); err != nil {
			return err
		}
		return validateDigest("gate.requiredChecksRef", evidence.RequiredChecksRef)
	}
	if evidence.Mechanism != MechanismFastForwardOnly ||
		evidence.PullRequestRef != "" || evidence.RequiredChecksRef != "" {
		return fmt.Errorf("%w: direct delivery must be fast-forward-only", ErrInvalid)
	}
	return nil
}

func (evidence GateEvidence) Ref() (string, error) {
	return contentRef(evidence, evidence.Validate())
}

type DeliveryDisposition string

const (
	DeliveryAuthorizeNormal  DeliveryDisposition = "authorize_normal"
	DeliveryRequireException DeliveryDisposition = "require_exception"
	DeliveryBlocked          DeliveryDisposition = "blocked"
)

func deliveryDisposition(
	policy RoutePolicy,
	outcome reviewtransaction.VerificationAggregate,
) DeliveryDisposition {
	switch {
	case !policy.Enabled || outcome == reviewtransaction.VerificationAggregateFailed:
		return DeliveryBlocked
	case outcome == reviewtransaction.VerificationAggregateComplete ||
		outcome == reviewtransaction.VerificationAggregateNotRequired:
		return DeliveryAuthorizeNormal
	case policy.AllowIncomplete &&
		(outcome == reviewtransaction.VerificationAggregatePartial ||
			outcome == reviewtransaction.VerificationAggregateUnavailable):
		return DeliveryRequireException
	default:
		return DeliveryBlocked
	}
}

type DeliveryDecision struct {
	Schema                string              `json:"schema"`
	AdmissionDecisionRef  string              `json:"admissionDecisionRef"`
	AdmissionDecision     AdmissionDecision   `json:"admissionDecision"`
	PolicyRef             string              `json:"policyRef"`
	Policy                RoutePolicy         `json:"policy"`
	ReviewReceiptRef      string              `json:"reviewReceiptRef"`
	VerificationResultRef string              `json:"verificationResultRef"`
	Candidate             CandidateBinding    `json:"candidate"`
	GateRef               string              `json:"gateRef"`
	Gates                 GateEvidence        `json:"gates"`
	Disposition           DeliveryDisposition `json:"disposition"`
	AuthorityRef          string              `json:"authorityRef"`
	Authority             AuthoritySignal     `json:"authority"`
	SecondAuthorityRef    string              `json:"secondAuthorityRef,omitempty"`
	SecondAuthority       *AuthoritySignal    `json:"secondAuthority,omitempty"`
	DecidedAt             int64               `json:"decidedAt"`
}

func (decision DeliveryDecision) Validate() error {
	if decision.Schema != DeliveryDecisionContractV1 || decision.DecidedAt <= 0 {
		return fmt.Errorf("%w: delivery decision header", ErrInvalid)
	}
	for _, exact := range []struct {
		name string
		ref  string
		load func() (string, error)
	}{
		{"decision.admissionDecisionRef", decision.AdmissionDecisionRef, decision.AdmissionDecision.Ref},
		{"decision.policyRef", decision.PolicyRef, decision.Policy.Ref},
		{"decision.gateRef", decision.GateRef, decision.Gates.Ref},
		{"decision.authorityRef", decision.AuthorityRef, decision.Authority.Ref},
	} {
		if err := exactEmbeddedRef(exact.name, exact.ref, exact.load); err != nil {
			return err
		}
	}
	if err := validateDigest("decision.reviewReceiptRef", decision.ReviewReceiptRef); err != nil {
		return err
	}
	if err := validateDigest("decision.verificationResultRef", decision.VerificationResultRef); err != nil {
		return err
	}
	if err := decision.Candidate.validate(); err != nil {
		return err
	}
	route := decision.AdmissionDecision.Route
	if decision.Policy.Route != route || decision.Gates.Route != route ||
		decision.AdmissionDecision.Disposition != AdmissionAdmitted ||
		decision.DecidedAt < decision.AdmissionDecision.DecidedAt ||
		decision.DecidedAt < decision.Gates.ObservedAt ||
		decision.DecidedAt >= decision.Gates.ExpiresAt ||
		decision.Candidate != decision.Gates.Candidate ||
		decision.Gates.PolicyRef != decision.PolicyRef ||
		decision.Gates.Policy != decision.Policy.Binding() ||
		!sameDestinationIdentity(decision.AdmissionDecision.Destination, decision.Gates.Destination) ||
		decision.Authority.PolicyRef != decision.PolicyRef ||
		decision.Authority.Policy != decision.Policy.Binding() {
		return fmt.Errorf("%w: delivery decision preimages", ErrBindingMismatch)
	}
	if decision.SecondAuthority == nil {
		if decision.SecondAuthorityRef != "" {
			return fmt.Errorf("%w: absent delivery second authority", ErrBindingMismatch)
		}
	} else if err := exactEmbeddedRef("decision.secondAuthorityRef", decision.SecondAuthorityRef, decision.SecondAuthority.Ref); err != nil {
		return err
	}
	if err := validateAuthorityRoute(
		route, decision.Policy, decision.Authority,
		decision.SecondAuthority, decision.DecidedAt,
	); err != nil {
		return err
	}
	if (route == RouteDirectMain || route == RouteEmergency) &&
		!decision.Gates.Destination.DefaultBranch {
		return fmt.Errorf("%w: direct delivery destination", ErrInvalid)
	}
	switch decision.Disposition {
	case DeliveryAuthorizeNormal, DeliveryRequireException, DeliveryBlocked:
	default:
		return fmt.Errorf("%w: delivery disposition", ErrInvalid)
	}
	return nil
}

func (decision DeliveryDecision) Ref() (string, error) {
	return contentRef(decision, decision.Validate())
}

func validateAuthorityRoute(
	route Route,
	policy RoutePolicy,
	primary AuthoritySignal,
	second *AuthoritySignal,
	at int64,
) error {
	if err := primary.Validate(); err != nil {
		return err
	}
	policyRef, _ := policy.Ref()
	if primary.PolicyRef != policyRef || primary.Policy != policy.Binding() || at >= primary.ExpiresAt {
		return fmt.Errorf("%w: primary authority binding/window", ErrUnauthorized)
	}
	if route == RoutePRWithIssue {
		if primary.Provenance != ProvenanceRepositoryPolicy &&
			primary.Provenance != ProvenanceMaintainerControl {
			return fmt.Errorf("%w: PR authority provenance", ErrUnauthorized)
		}
	} else if primary.Provenance != ProvenanceMaintainerControl {
		return fmt.Errorf("%w: maintainer-controlled route authority", ErrUnauthorized)
	}
	if policy.RequireSecondMaintainer {
		if second == nil {
			return fmt.Errorf("%w: second maintainer required", ErrUnauthorized)
		}
		if err := second.Validate(); err != nil {
			return err
		}
		if second.PolicyRef != policyRef || second.Policy != policy.Binding() ||
			second.Provenance != ProvenanceSecondMaintainer ||
			second.IssuerRef == primary.IssuerRef || second.SignalID == primary.SignalID ||
			at >= second.ExpiresAt {
			return fmt.Errorf("%w: second maintainer is not independent", ErrUnauthorized)
		}
	} else if second != nil {
		return fmt.Errorf("%w: unexpected second authority", ErrInvalid)
	}
	return nil
}

func exactEmbeddedRef(name, ref string, preimageRef func() (string, error)) error {
	if err := validateDigest(name, ref); err != nil {
		return err
	}
	actual, err := preimageRef()
	if err != nil {
		return err
	}
	if actual != ref {
		return fmt.Errorf("%w: %s", ErrBindingMismatch, name)
	}
	return nil
}

func validateToken(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 512 {
		return fmt.Errorf("%w: %s", ErrInvalid, name)
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("%w: %s", ErrInvalid, name)
		}
	}
	return nil
}

func validateText(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 2048 ||
		strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%w: %s", ErrInvalid, name)
	}
	return nil
}

func validateDigest(name, value string) error {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") ||
		strings.ToLower(value) != value {
		return fmt.Errorf("%w: %s must be canonical sha256", ErrInvalid, name)
	}
	if _, err := hex.DecodeString(value[7:]); err != nil {
		return fmt.Errorf("%w: %s must be canonical sha256", ErrInvalid, name)
	}
	return nil
}

func contentRef(value any, validationErr error) (string, error) {
	if validationErr != nil {
		return "", validationErr
	}
	return hashValue(value), nil
}

func hashValue(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("deliveryadmission: canonical JSON failed: %v", err))
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}
