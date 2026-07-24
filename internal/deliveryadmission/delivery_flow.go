package deliveryadmission

import (
	"context"
	"errors"
	"fmt"
)

const (
	LiveGateProbeRequestContractV1 = "gentle-ai.delivery-live-gate-probe-request/v1"
	LiveGateProbeResultContractV1  = "gentle-ai.delivery-live-gate-probe-result/v1"
	ExecutionCommandContractV1     = "gentle-ai.delivery-execution-command/v1"
	ExecutionResultContractV1      = "gentle-ai.delivery-execution-result/v1"
	RouteReevaluationContractV1    = "gentle-ai.delivery-route-reevaluation/v1"
	maximumDeliveryGateTTLSeconds  = int64(300)
)

var (
	ErrLiveProbeRejected = errors.New("live delivery probe rejected")
	ErrExecutionRejected = errors.New("delivery execution rejected")
	ErrRouteNotChanged   = errors.New("delivery route was not changed")
)

// IntentSelectionRequest contains only user intent. An empty RequestedRoute is
// not a bypass flag: it selects PAD's canonical community default.
type IntentSelectionRequest struct {
	Nonce          string
	RequestedRoute Route
	ScopeDigest    string
	Destination    DestinationBinding
}

// SelectDeliveryIntent binds a provisional intent to the current live
// owner-controlled route policy. PAD never falls back from a disabled or
// unavailable default to a less-governed route.
func SelectDeliveryIntent(
	ctx context.Context,
	resolver TrustedResolver,
	request IntentSelectionRequest,
) (DeliveryIntent, error) {
	if err := validateToken("intent selection nonce", request.Nonce); err != nil {
		return DeliveryIntent{}, err
	}
	if err := validateDigest("intent selection scope", request.ScopeDigest); err != nil {
		return DeliveryIntent{}, err
	}
	if err := request.Destination.validate(); err != nil {
		return DeliveryIntent{}, err
	}
	route := request.RequestedRoute
	if route == "" {
		route = RoutePRWithIssue
	} else if !route.valid() {
		return DeliveryIntent{}, fmt.Errorf("%w: requested delivery route", ErrInvalid)
	}
	policy, err := resolveLiveRoutePolicy(
		ctx,
		resolver,
		request.Destination.RepositoryRef,
		route,
	)
	if err != nil {
		return DeliveryIntent{}, err
	}
	if !policy.Enabled {
		return DeliveryIntent{}, fmt.Errorf(
			"%w: selected delivery route is disabled",
			ErrUnauthorized,
		)
	}
	now, err := trustedNow(ctx, resolver)
	if err != nil {
		return DeliveryIntent{}, err
	}
	policyRef, err := policy.Ref()
	if err != nil {
		return DeliveryIntent{}, err
	}
	return NewIntent(
		request.Nonce,
		route,
		request.ScopeDigest,
		request.Destination,
		policyRef,
		policy.Binding(),
		now,
	)
}

func resolveLiveRoutePolicy(
	ctx context.Context,
	resolver TrustedResolver,
	repositoryRef string,
	route Route,
) (RoutePolicy, error) {
	if resolver == nil {
		return RoutePolicy{}, fmt.Errorf("%w: missing resolver", ErrUntrustedPreimage)
	}
	policy, err := resolver.ResolveLiveRoutePolicy(ctx, repositoryRef, route)
	if err != nil {
		return RoutePolicy{}, fmt.Errorf("%w: live route policy: %v", ErrUntrustedPreimage, err)
	}
	if err := policy.Validate(); err != nil {
		return RoutePolicy{}, fmt.Errorf("%w: invalid live route policy: %v", ErrUntrustedPreimage, err)
	}
	if policy.Route != route {
		return RoutePolicy{}, fmt.Errorf("%w: live route policy route", ErrBindingMismatch)
	}
	return policy, nil
}

type LiveGateProbeRequest struct {
	Schema      string             `json:"schema"`
	Route       Route              `json:"route"`
	Candidate   CandidateBinding   `json:"candidate"`
	Destination DestinationBinding `json:"destination"`
	PolicyRef   string             `json:"policyRef"`
	Policy      PolicyBinding      `json:"policy"`
	Mechanism   Mechanism          `json:"mechanism"`
}

func NewLiveGateProbeRequest(
	route Route,
	candidate CandidateBinding,
	destination DestinationBinding,
	policyRef string,
	policy PolicyBinding,
	mechanism Mechanism,
) (LiveGateProbeRequest, error) {
	request := LiveGateProbeRequest{
		Schema: LiveGateProbeRequestContractV1,
		Route:  route, Candidate: candidate, Destination: destination,
		PolicyRef: policyRef, Policy: policy, Mechanism: mechanism,
	}
	return request, request.Validate()
}

func (request LiveGateProbeRequest) Validate() error {
	if request.Schema != LiveGateProbeRequestContractV1 || !request.Route.valid() {
		return fmt.Errorf("%w: live gate probe request header", ErrInvalid)
	}
	if err := request.Candidate.validate(); err != nil {
		return err
	}
	if err := request.Destination.validate(); err != nil {
		return err
	}
	if err := validateDigest("live gate probe policyRef", request.PolicyRef); err != nil {
		return err
	}
	if err := request.Policy.validate(); err != nil {
		return err
	}
	switch request.Route {
	case RoutePRWithIssue, RoutePRWithoutIssue:
		if request.Mechanism != MechanismPullRequest {
			return fmt.Errorf("%w: pull-request route probe mechanism", ErrInvalid)
		}
	case RouteDirectMain:
		if request.Mechanism != MechanismFastForwardOnly ||
			!request.Destination.DefaultBranch {
			return fmt.Errorf("%w: direct-main probe mechanism/destination", ErrUnauthorized)
		}
	case RouteEmergency:
		if request.Mechanism != MechanismPullRequest &&
			request.Mechanism != MechanismFastForwardOnly {
			return fmt.Errorf("%w: emergency probe mechanism", ErrInvalid)
		}
		if request.Mechanism == MechanismFastForwardOnly &&
			!request.Destination.DefaultBranch {
			return fmt.Errorf("%w: emergency direct destination", ErrUnauthorized)
		}
	default:
		return fmt.Errorf("%w: live gate probe route", ErrInvalid)
	}
	return nil
}

func (request LiveGateProbeRequest) Ref() (string, error) {
	return contentRef(request, request.Validate())
}

// LiveGateProbeResult is returned by a trusted runtime adapter. Freshness and
// protection are explicit observations rather than caller-authored gate refs.
type LiveGateProbeResult struct {
	Schema                  string               `json:"schema"`
	RequestRef              string               `json:"requestRef"`
	Request                 LiveGateProbeRequest `json:"request"`
	ObservedRemoteRevision  string               `json:"observedRemoteRevision"`
	RemoteFreshnessRef      string               `json:"remoteFreshnessRef"`
	ProtectionMode          ProtectionMode       `json:"protectionMode"`
	ProtectionSatisfied     bool                 `json:"protectionSatisfied"`
	ProtectionRef           string               `json:"protectionRef"`
	FastForwardSatisfied    bool                 `json:"fastForwardSatisfied"`
	FastForwardRef          string               `json:"fastForwardRef,omitempty"`
	PullRequestRef          string               `json:"pullRequestRef,omitempty"`
	RequiredChecksSatisfied bool                 `json:"requiredChecksSatisfied"`
	RequiredChecksRef       string               `json:"requiredChecksRef,omitempty"`
	ObservedAt              int64                `json:"observedAt"`
	ExpiresAt               int64                `json:"expiresAt"`
}

func (result LiveGateProbeResult) Validate() error {
	if result.Schema != LiveGateProbeResultContractV1 ||
		result.ObservedAt <= 0 || result.ExpiresAt <= result.ObservedAt ||
		result.ExpiresAt-result.ObservedAt > maximumDeliveryGateTTLSeconds {
		return fmt.Errorf("%w: live gate probe result header/window", ErrInvalid)
	}
	if err := exactEmbeddedRef(
		"live gate probe requestRef",
		result.RequestRef,
		result.Request.Ref,
	); err != nil {
		return err
	}
	if err := validateToken(
		"live gate observed remote revision",
		result.ObservedRemoteRevision,
	); err != nil {
		return err
	}
	if err := validateDigest("live gate remote freshness ref", result.RemoteFreshnessRef); err != nil {
		return err
	}
	if result.ProtectionMode != ProtectionRespect || !result.ProtectionSatisfied {
		return fmt.Errorf("%w: branch protection is not satisfied", ErrLiveProbeRejected)
	}
	if err := validateDigest("live gate protection ref", result.ProtectionRef); err != nil {
		return err
	}
	if result.ObservedRemoteRevision != result.Request.Destination.ObservedRevision {
		return fmt.Errorf("%w: remote destination is stale", ErrLiveProbeRejected)
	}
	switch result.Request.Mechanism {
	case MechanismPullRequest:
		if result.FastForwardSatisfied || result.FastForwardRef != "" {
			return fmt.Errorf("%w: pull-request probe carries direct-update facts", ErrInvalid)
		}
		if err := validateToken("live gate pull request ref", result.PullRequestRef); err != nil {
			return err
		}
		if !result.RequiredChecksSatisfied {
			return fmt.Errorf("%w: required checks are not satisfied", ErrLiveProbeRejected)
		}
		return validateDigest("live gate required checks ref", result.RequiredChecksRef)
	case MechanismFastForwardOnly:
		if result.PullRequestRef != "" || result.RequiredChecksRef != "" ||
			result.RequiredChecksSatisfied {
			return fmt.Errorf("%w: direct delivery carries pull-request facts", ErrInvalid)
		}
		if !result.FastForwardSatisfied {
			return fmt.Errorf("%w: update is not fast-forward", ErrLiveProbeRejected)
		}
		return validateDigest("live gate fast-forward ref", result.FastForwardRef)
	default:
		return fmt.Errorf("%w: live gate probe mechanism", ErrInvalid)
	}
}

func (result LiveGateProbeResult) Ref() (string, error) {
	return contentRef(result, result.Validate())
}

// LiveGateProbePort owns Git/hosting observation. Implementations may compose
// HCR, provider APIs, or another trusted runtime, but PAD never receives argv,
// shell snippets, force flags, or caller-authored success booleans.
type LiveGateProbePort interface {
	ProbeLiveGate(context.Context, LiveGateProbeRequest) (LiveGateProbeResult, error)
}

func ProbeGateEvidence(
	ctx context.Context,
	resolver TrustedResolver,
	probe LiveGateProbePort,
	request LiveGateProbeRequest,
) (GateEvidence, error) {
	if err := request.Validate(); err != nil {
		return GateEvidence{}, err
	}
	policy, err := resolvePolicy(ctx, resolver, request.PolicyRef)
	if err != nil {
		return GateEvidence{}, err
	}
	if policy.Binding() != request.Policy || policy.Route != request.Route {
		return GateEvidence{}, fmt.Errorf("%w: live gate policy", ErrBindingMismatch)
	}
	if err := requireLivePolicy(
		ctx,
		resolver,
		request.Destination.RepositoryRef,
		request.Route,
		request.PolicyRef,
		policy,
	); err != nil {
		return GateEvidence{}, err
	}
	if probe == nil {
		return GateEvidence{}, fmt.Errorf("%w: missing live gate probe", ErrInvalid)
	}
	before, err := trustedNow(ctx, resolver)
	if err != nil {
		return GateEvidence{}, err
	}
	result, err := probe.ProbeLiveGate(ctx, request)
	if err != nil {
		return GateEvidence{}, fmt.Errorf("%w: %v", ErrLiveProbeRejected, err)
	}
	after, err := trustedNow(ctx, resolver)
	if err != nil {
		return GateEvidence{}, err
	}
	if err := result.Validate(); err != nil {
		return GateEvidence{}, err
	}
	if result.Request != request ||
		result.ObservedAt < before ||
		result.ObservedAt > after ||
		after >= result.ExpiresAt {
		return GateEvidence{}, fmt.Errorf("%w: live probe binding/window", ErrBindingMismatch)
	}
	gates := GateEvidence{
		Schema: GateEvidenceContractV1,
		Route:  request.Route, Candidate: request.Candidate,
		Destination: request.Destination,
		PolicyRef:   request.PolicyRef, Policy: request.Policy,
		Mechanism: request.Mechanism, ProtectionMode: result.ProtectionMode,
		PullRequestRef: result.PullRequestRef, RequiredChecksRef: result.RequiredChecksRef,
		RemoteFreshnessRef: result.RemoteFreshnessRef,
		ProtectionRef:      result.ProtectionRef,
		ObservedAt:         result.ObservedAt, ExpiresAt: result.ExpiresAt,
	}
	return gates, gates.Validate()
}

type ExecutionOutcome string

const (
	ExecutionSucceeded     ExecutionOutcome = "succeeded"
	ExecutionFailed        ExecutionOutcome = "failed"
	ExecutionIndeterminate ExecutionOutcome = "indeterminate"
)

type ExecutionCommand struct {
	Schema            string             `json:"schema"`
	AuthorizationRef  string             `json:"authorizationRef"`
	AuthorizationKind AuthorizationKind  `json:"authorizationKind"`
	DecisionRef       string             `json:"decisionRef"`
	IntentRef         string             `json:"intentRef"`
	Route             Route              `json:"route"`
	Candidate         CandidateBinding   `json:"candidate"`
	Destination       DestinationBinding `json:"destination"`
	PolicyRef         string             `json:"policyRef"`
	Policy            PolicyBinding      `json:"policy"`
	GateRef           string             `json:"gateRef"`
	LiveGateRef       string             `json:"liveGateRef"`
	// ExecutionExpiresAt is the earlier of the authorization and live-gate
	// deadlines. Only replay of an already durable terminal result may cross it.
	ExecutionExpiresAt     int64          `json:"executionExpiresAt"`
	Mechanism              Mechanism      `json:"mechanism"`
	ProtectionMode         ProtectionMode `json:"protectionMode"`
	PullRequestRef         string         `json:"pullRequestRef,omitempty"`
	ExpectedRemoteRevision string         `json:"expectedRemoteRevision"`
}

func (command ExecutionCommand) Validate() error {
	if command.Schema != ExecutionCommandContractV1 ||
		(command.AuthorizationKind != AuthorizationNormal &&
			command.AuthorizationKind != AuthorizationException) ||
		!command.Route.valid() || command.ExecutionExpiresAt <= 0 {
		return fmt.Errorf("%w: delivery execution command header", ErrInvalid)
	}
	for name, ref := range map[string]string{
		"execution authorizationRef": command.AuthorizationRef,
		"execution decisionRef":      command.DecisionRef,
		"execution intentRef":        command.IntentRef,
		"execution policyRef":        command.PolicyRef,
		"execution gateRef":          command.GateRef,
		"execution liveGateRef":      command.LiveGateRef,
	} {
		if err := validateDigest(name, ref); err != nil {
			return err
		}
	}
	if err := command.Candidate.validate(); err != nil {
		return err
	}
	if err := command.Destination.validate(); err != nil {
		return err
	}
	if err := command.Policy.validate(); err != nil {
		return err
	}
	if command.ExpectedRemoteRevision != command.Destination.ObservedRevision {
		return fmt.Errorf("%w: execution remote revision", ErrBindingMismatch)
	}
	if command.ProtectionMode != ProtectionRespect {
		return fmt.Errorf("%w: execution must respect branch protection", ErrUnauthorized)
	}
	switch command.Route {
	case RoutePRWithIssue, RoutePRWithoutIssue:
		if command.Mechanism != MechanismPullRequest {
			return fmt.Errorf("%w: PR execution mechanism", ErrInvalid)
		}
		return validateToken("execution pull request ref", command.PullRequestRef)
	case RouteDirectMain:
		if command.Mechanism != MechanismFastForwardOnly ||
			command.PullRequestRef != "" || !command.Destination.DefaultBranch {
			return fmt.Errorf("%w: direct-main execution must be protected fast-forward", ErrUnauthorized)
		}
	case RouteEmergency:
		if command.Mechanism != MechanismPullRequest &&
			command.Mechanism != MechanismFastForwardOnly {
			return fmt.Errorf("%w: emergency execution mechanism", ErrInvalid)
		}
		if command.Mechanism == MechanismPullRequest {
			return validateToken("execution pull request ref", command.PullRequestRef)
		}
		if command.PullRequestRef != "" || !command.Destination.DefaultBranch {
			return fmt.Errorf("%w: emergency direct execution", ErrUnauthorized)
		}
	}
	return nil
}

func (command ExecutionCommand) Ref() (string, error) {
	return contentRef(command, command.Validate())
}

type ExecutionResult struct {
	Schema           string           `json:"schema"`
	CommandRef       string           `json:"commandRef"`
	AuthorizationRef string           `json:"authorizationRef"`
	Route            Route            `json:"route"`
	Candidate        CandidateBinding `json:"candidate"`
	Outcome          ExecutionOutcome `json:"outcome"`
	DeliveryRef      string           `json:"deliveryRef,omitempty"`
	EvidenceRef      string           `json:"evidenceRef"`
	CompletedAt      int64            `json:"completedAt"`
}

func (result ExecutionResult) Validate(command ExecutionCommand) error {
	if result.Schema != ExecutionResultContractV1 || result.CompletedAt <= 0 {
		return fmt.Errorf("%w: delivery execution result header", ErrInvalid)
	}
	commandRef, err := command.Ref()
	if err != nil {
		return err
	}
	if result.CommandRef != commandRef ||
		result.AuthorizationRef != command.AuthorizationRef ||
		result.Route != command.Route || result.Candidate != command.Candidate {
		return fmt.Errorf("%w: delivery execution result binding", ErrBindingMismatch)
	}
	if err := validateDigest("execution result evidence ref", result.EvidenceRef); err != nil {
		return err
	}
	switch result.Outcome {
	case ExecutionSucceeded:
		return validateToken("execution result delivery ref", result.DeliveryRef)
	case ExecutionFailed, ExecutionIndeterminate:
		if result.DeliveryRef != "" {
			return fmt.Errorf("%w: unsuccessful execution carries delivery ref", ErrInvalid)
		}
		return nil
	default:
		return fmt.Errorf("%w: delivery execution outcome", ErrInvalid)
	}
}

// DeliveryExecutorPort performs one content-addressed effect. ExecuteOnce MUST
// first return any durable terminal result stored under command.Ref(). If no
// terminal result exists, it MUST use its owner-controlled clock to reject at
// or after ExecutionExpiresAt before its first effect. It must never infer or
// accept a force option.
type DeliveryExecutorPort interface {
	ExecuteOnce(context.Context, ExecutionCommand) (ExecutionResult, error)
}

type ExecuteDeliveryRequest struct {
	AuthorizationRef  string
	AuthorizationKind AuthorizationKind
}

func ExecuteAuthorizedDelivery(
	ctx context.Context,
	resolver TrustedResolver,
	rar RARAuthorityPort,
	probe LiveGateProbePort,
	executor DeliveryExecutorPort,
	store *DirectoryUseStore,
	request ExecuteDeliveryRequest,
) (ExecutionResult, error) {
	if executor == nil {
		return ExecutionResult{}, fmt.Errorf("%w: missing delivery executor", ErrInvalid)
	}
	binding, decision, err := resolveExecutionAuthority(
		ctx,
		resolver,
		rar,
		request,
	)
	if err != nil {
		return ExecutionResult{}, err
	}
	if store == nil {
		return ExecutionResult{}, fmt.Errorf("%w: missing atomic durable use store", ErrInvalid)
	}
	if store.repositoryRef != binding.Destination.RepositoryRef {
		return ExecutionResult{}, fmt.Errorf(
			"%w: delivery execution use repository",
			ErrBindingMismatch,
		)
	}
	priorUse, consumed, err := store.authorizationUse(ctx, request.AuthorizationRef)
	if err != nil {
		return ExecutionResult{}, err
	}
	if consumed {
		if priorUse.ExecutionCommandRef == "" || priorUse.LiveGateRef == "" {
			return ExecutionResult{}, fmt.Errorf(
				"%w: authorization was not reserved by the delivery executor",
				ErrBindingMismatch,
			)
		}
		command := executionCommand(
			request.AuthorizationRef,
			request.AuthorizationKind,
			binding,
			decision,
			priorUse.LiveGateRef,
			priorUse.ExecutionExpiresAt,
		)
		commandRef, err := command.Ref()
		if err != nil {
			return ExecutionResult{}, err
		}
		if err := validateExecutionUse(
			priorUse,
			request.AuthorizationKind,
			binding,
			commandRef,
			priorUse.LiveGateRef,
			priorUse.ExecutionExpiresAt,
		); err != nil {
			return ExecutionResult{}, err
		}
		return executeOnce(ctx, executor, command, priorUse.ConsumedAt)
	}

	probeRequest, err := NewLiveGateProbeRequest(
		binding.Route,
		binding.Candidate,
		binding.Destination,
		binding.PolicyRef,
		binding.Policy.Binding(),
		decision.Gates.Mechanism,
	)
	if err != nil {
		return ExecutionResult{}, err
	}
	liveGates, err := ProbeGateEvidence(ctx, resolver, probe, probeRequest)
	if err != nil {
		return ExecutionResult{}, err
	}
	if liveGates.PullRequestRef != decision.Gates.PullRequestRef ||
		liveGates.Mechanism != decision.Gates.Mechanism ||
		liveGates.Candidate != decision.Candidate ||
		liveGates.Destination != binding.Destination {
		return ExecutionResult{}, fmt.Errorf("%w: live execution gate changed", ErrBindingMismatch)
	}
	liveGateRef, err := liveGates.Ref()
	if err != nil {
		return ExecutionResult{}, err
	}
	executionExpiresAt := liveGates.ExpiresAt
	if binding.ExpiresAt < executionExpiresAt {
		executionExpiresAt = binding.ExpiresAt
	}
	command := executionCommand(
		request.AuthorizationRef,
		request.AuthorizationKind,
		binding,
		decision,
		liveGateRef,
		executionExpiresAt,
	)
	commandRef, err := command.Ref()
	if err != nil {
		return ExecutionResult{}, err
	}
	now, err := trustedNow(ctx, resolver)
	if err != nil {
		return ExecutionResult{}, err
	}
	if now < binding.IssuedAt || now < liveGates.ObservedAt {
		return ExecutionResult{}, fmt.Errorf(
			"%w: delivery execution clock regressed",
			ErrBindingMismatch,
		)
	}
	if now >= executionExpiresAt {
		return ExecutionResult{}, ErrExpired
	}
	if err := requireLivePolicy(
		ctx,
		resolver,
		binding.Destination.RepositoryRef,
		binding.Route,
		binding.PolicyRef,
		binding.Policy,
	); err != nil {
		return ExecutionResult{}, err
	}
	use := AuthorizationUse{
		Schema: AuthorizationUseContractV1,
		Kind:   request.AuthorizationKind, AuthorizationRef: request.AuthorizationRef,
		ExecutionCommandRef: commandRef, LiveGateRef: liveGateRef,
		ExecutionExpiresAt: executionExpiresAt,
		DecisionRef:        binding.DecisionRef, IntentRef: binding.IntentRef,
		Route: binding.Route, Candidate: binding.Candidate,
		Destination: binding.Destination, PolicyRef: binding.PolicyRef,
		Policy: binding.Policy.Binding(), ConsumedAt: now,
		AuthorizationExpiresAt: binding.ExpiresAt,
	}
	err = store.consumeOnce(ctx, use)
	if errors.Is(err, ErrAlreadyConsumed) || errors.Is(err, ErrBindingMismatch) {
		reservationErr := err
		priorUse, consumed, readErr := store.authorizationUse(ctx, request.AuthorizationRef)
		if readErr != nil {
			return ExecutionResult{}, readErr
		}
		if !consumed {
			return ExecutionResult{}, reservationErr
		}
		if priorUse.ExecutionCommandRef == "" || priorUse.LiveGateRef == "" {
			return ExecutionResult{}, fmt.Errorf(
				"%w: authorization has a non-execution reservation",
				ErrBindingMismatch,
			)
		}
		command = executionCommand(
			request.AuthorizationRef,
			request.AuthorizationKind,
			binding,
			decision,
			priorUse.LiveGateRef,
			priorUse.ExecutionExpiresAt,
		)
		commandRef, err = command.Ref()
		if err != nil {
			return ExecutionResult{}, err
		}
		if err := validateExecutionUse(
			priorUse,
			request.AuthorizationKind,
			binding,
			commandRef,
			priorUse.LiveGateRef,
			priorUse.ExecutionExpiresAt,
		); err != nil {
			return ExecutionResult{}, err
		}
		use = priorUse
	} else if err != nil {
		return ExecutionResult{}, err
	} else if err := validateExecutionUse(
		use,
		request.AuthorizationKind,
		binding,
		commandRef,
		liveGateRef,
		executionExpiresAt,
	); err != nil {
		return ExecutionResult{}, err
	}
	return executeOnce(ctx, executor, command, use.ConsumedAt)
}

func resolveExecutionAuthority(
	ctx context.Context,
	resolver TrustedResolver,
	rar RARAuthorityPort,
	request ExecuteDeliveryRequest,
) (AuthorizationBinding, DeliveryDecision, error) {
	if err := validateDigest("execution authorization ref", request.AuthorizationRef); err != nil {
		return AuthorizationBinding{}, DeliveryDecision{}, err
	}
	var binding AuthorizationBinding
	switch request.AuthorizationKind {
	case AuthorizationNormal:
		authorization, err := ValidateAuthorization(
			ctx,
			resolver,
			rar,
			request.AuthorizationRef,
		)
		if err != nil {
			return AuthorizationBinding{}, DeliveryDecision{}, err
		}
		binding = authorization.Binding
	case AuthorizationException:
		authorization, err := ValidateExceptionAuthorization(
			ctx,
			resolver,
			rar,
			request.AuthorizationRef,
		)
		if err != nil {
			return AuthorizationBinding{}, DeliveryDecision{}, err
		}
		binding = authorization.Binding
	default:
		return AuthorizationBinding{}, DeliveryDecision{},
			fmt.Errorf("%w: execution authorization kind", ErrInvalid)
	}
	decision, err := ValidateDeliveryDecision(ctx, resolver, rar, binding.DecisionRef)
	if err != nil {
		return AuthorizationBinding{}, DeliveryDecision{}, err
	}
	return binding, decision, nil
}

func executionCommand(
	authorizationRef string,
	kind AuthorizationKind,
	binding AuthorizationBinding,
	decision DeliveryDecision,
	liveGateRef string,
	executionExpiresAt int64,
) ExecutionCommand {
	return ExecutionCommand{
		Schema:           ExecutionCommandContractV1,
		AuthorizationRef: authorizationRef, AuthorizationKind: kind,
		DecisionRef: binding.DecisionRef, IntentRef: binding.IntentRef,
		Route: binding.Route, Candidate: binding.Candidate,
		Destination: binding.Destination, PolicyRef: binding.PolicyRef,
		Policy: binding.Policy.Binding(), GateRef: decision.GateRef,
		LiveGateRef:            liveGateRef,
		ExecutionExpiresAt:     executionExpiresAt,
		Mechanism:              decision.Gates.Mechanism,
		ProtectionMode:         decision.Gates.ProtectionMode,
		PullRequestRef:         decision.Gates.PullRequestRef,
		ExpectedRemoteRevision: binding.Destination.ObservedRevision,
	}
}

func validateExecutionUse(
	use AuthorizationUse,
	kind AuthorizationKind,
	binding AuthorizationBinding,
	commandRef string,
	liveGateRef string,
	executionExpiresAt int64,
) error {
	if err := use.Validate(); err != nil {
		return err
	}
	if use.Kind != kind ||
		use.ExecutionCommandRef != commandRef || use.LiveGateRef != liveGateRef ||
		use.ExecutionExpiresAt != executionExpiresAt ||
		use.DecisionRef != binding.DecisionRef ||
		use.IntentRef != binding.IntentRef || use.Route != binding.Route ||
		use.Candidate != binding.Candidate || use.Destination != binding.Destination ||
		use.PolicyRef != binding.PolicyRef || use.Policy != binding.Policy.Binding() ||
		use.AuthorizationExpiresAt != binding.ExpiresAt ||
		use.ConsumedAt < binding.IssuedAt {
		return fmt.Errorf("%w: delivery execution authorization use", ErrBindingMismatch)
	}
	return nil
}

func executeOnce(
	ctx context.Context,
	executor DeliveryExecutorPort,
	command ExecutionCommand,
	notBefore int64,
) (ExecutionResult, error) {
	result, err := executor.ExecuteOnce(ctx, command)
	if err != nil {
		return ExecutionResult{}, fmt.Errorf("%w: %w", ErrExecutionRejected, err)
	}
	if err := result.Validate(command); err != nil {
		return ExecutionResult{}, err
	}
	if result.CompletedAt < notBefore {
		return ExecutionResult{}, fmt.Errorf(
			"%w: execution result predates durable reservation",
			ErrBindingMismatch,
		)
	}
	return result, nil
}

// RouteReevaluationRepository is the existing PAD owner repository, narrowed to
// the two immutable objects emitted by a route-only reevaluation. It is not a
// second ledger or store.
type RouteReevaluationRepository interface {
	TrustedResolver
	PublishGateEvidence(context.Context, GateEvidence) (string, error)
	PublishDeliveryDecision(context.Context, DeliveryDecision) (string, error)
}

type RouteReevaluationRequest struct {
	SourceDecisionRef          string
	TargetAdmissionDecisionRef string
	Mechanism                  Mechanism
}

type RouteReevaluation struct {
	Schema                     string             `json:"schema"`
	SourceDecisionRef          string             `json:"sourceDecisionRef"`
	TargetAdmissionDecisionRef string             `json:"targetAdmissionDecisionRef"`
	SourceRoute                Route              `json:"sourceRoute"`
	TargetRoute                Route              `json:"targetRoute"`
	Candidate                  CandidateBinding   `json:"candidate"`
	ReviewReceiptRef           string             `json:"reviewReceiptRef"`
	VerificationResultRef      string             `json:"verificationResultRef"`
	GateRef                    string             `json:"gateRef"`
	DecisionRef                string             `json:"decisionRef"`
	Destination                DestinationBinding `json:"destination"`
	EvaluatedAt                int64              `json:"evaluatedAt"`
}

func (result RouteReevaluation) Validate() error {
	if result.Schema != RouteReevaluationContractV1 ||
		!result.SourceRoute.valid() || !result.TargetRoute.valid() ||
		result.SourceRoute == result.TargetRoute || result.EvaluatedAt <= 0 {
		return fmt.Errorf("%w: route reevaluation header", ErrInvalid)
	}
	for name, ref := range map[string]string{
		"route reevaluation source decision":  result.SourceDecisionRef,
		"route reevaluation target admission": result.TargetAdmissionDecisionRef,
		"route reevaluation receipt":          result.ReviewReceiptRef,
		"route reevaluation verification":     result.VerificationResultRef,
		"route reevaluation gate":             result.GateRef,
		"route reevaluation decision":         result.DecisionRef,
	} {
		if err := validateDigest(name, ref); err != nil {
			return err
		}
	}
	if err := result.Candidate.validate(); err != nil {
		return err
	}
	return result.Destination.validate()
}

func (result RouteReevaluation) Ref() (string, error) {
	return contentRef(result, result.Validate())
}

func ReevaluateDeliveryRoute(
	ctx context.Context,
	repository RouteReevaluationRepository,
	rar RARAuthorityPort,
	probe LiveGateProbePort,
	request RouteReevaluationRequest,
) (RouteReevaluation, error) {
	if repository == nil {
		return RouteReevaluation{}, fmt.Errorf("%w: missing PAD owner repository", ErrInvalid)
	}
	source, err := ValidateDeliveryDecision(
		ctx,
		repository,
		rar,
		request.SourceDecisionRef,
	)
	if err != nil {
		return RouteReevaluation{}, err
	}
	targetAdmission, err := ValidateAdmissionDecision(
		ctx,
		repository,
		request.TargetAdmissionDecisionRef,
	)
	if err != nil {
		return RouteReevaluation{}, err
	}
	if targetAdmission.Disposition != AdmissionAdmitted {
		return RouteReevaluation{}, fmt.Errorf("%w: target route admission denied", ErrUnauthorized)
	}
	if source.AdmissionDecision.Route == targetAdmission.Route {
		return RouteReevaluation{}, ErrRouteNotChanged
	}
	if source.AdmissionDecision.Intent.ScopeDigest != targetAdmission.Intent.ScopeDigest ||
		source.Gates.Destination != targetAdmission.Destination {
		return RouteReevaluation{}, fmt.Errorf(
			"%w: route-only reevaluation changed scope or destination",
			ErrBindingMismatch,
		)
	}
	probeRequest, err := NewLiveGateProbeRequest(
		targetAdmission.Route,
		source.Candidate,
		targetAdmission.Destination,
		targetAdmission.PolicyRef,
		targetAdmission.Policy.Binding(),
		request.Mechanism,
	)
	if err != nil {
		return RouteReevaluation{}, err
	}
	gates, err := ProbeGateEvidence(ctx, repository, probe, probeRequest)
	if err != nil {
		return RouteReevaluation{}, err
	}
	gateRef, err := repository.PublishGateEvidence(ctx, gates)
	if err != nil {
		return RouteReevaluation{}, err
	}
	decision, err := Decide(
		ctx,
		repository,
		rar,
		DeliveryRequest{
			AdmissionDecisionRef:  request.TargetAdmissionDecisionRef,
			PolicyRef:             targetAdmission.PolicyRef,
			ReviewReceiptRef:      source.ReviewReceiptRef,
			VerificationResultRef: source.VerificationResultRef,
			GateRef:               gateRef,
			AuthorityRef:          targetAdmission.AuthorityRef,
			SecondAuthorityRef:    targetAdmission.SecondAuthorityRef,
		},
	)
	if err != nil {
		return RouteReevaluation{}, err
	}
	if decision.Candidate != source.Candidate ||
		decision.ReviewReceiptRef != source.ReviewReceiptRef ||
		decision.VerificationResultRef != source.VerificationResultRef {
		return RouteReevaluation{}, fmt.Errorf(
			"%w: route reevaluation changed content evidence",
			ErrBindingMismatch,
		)
	}
	decisionRef, err := repository.PublishDeliveryDecision(ctx, decision)
	if err != nil {
		return RouteReevaluation{}, err
	}
	result := RouteReevaluation{
		Schema:                     RouteReevaluationContractV1,
		SourceDecisionRef:          request.SourceDecisionRef,
		TargetAdmissionDecisionRef: request.TargetAdmissionDecisionRef,
		SourceRoute:                source.AdmissionDecision.Route,
		TargetRoute:                targetAdmission.Route,
		Candidate:                  source.Candidate,
		ReviewReceiptRef:           source.ReviewReceiptRef,
		VerificationResultRef:      source.VerificationResultRef,
		GateRef:                    gateRef,
		DecisionRef:                decisionRef,
		Destination:                targetAdmission.Destination,
		EvaluatedAt:                decision.DecidedAt,
	}
	return result, result.Validate()
}

var _ RouteReevaluationRepository = (*TrustedRepository)(nil)
