package workprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const maximumOutcomeRequestCodePoints = 64 << 10

var ErrOutcomeNotManaged = errors.New(
	"outcome does not require a managed write lifecycle",
)

// OutcomeStartRequest is the entire caller-authored start surface. It contains
// no repository identity, hash, policy, authority, route, argv, or durable
// reference. ExplicitSDDRequested records user intent only; the coordination
// owner derives its immutable authority reference.
type OutcomeStartRequest struct {
	Outcome              string
	ExplicitSDDRequested bool
}

func (request OutcomeStartRequest) validate() error {
	if request.Outcome == "" ||
		!utf8.ValidString(request.Outcome) ||
		!hasCanonicalTextEdges(request.Outcome) ||
		utf8.RuneCountInString(request.Outcome) >
			maximumOutcomeRequestCodePoints ||
		strings.ContainsRune(request.Outcome, '\x00') {
		return errors.New("outcome start requires bounded canonical outcome text")
	}
	return nil
}

// OwnerOutcomeContext is supplied by the retained repository lease to the
// injected control-plane authority. Neither value comes from the outcome
// request.
type OwnerOutcomeContext struct {
	RepositoryRef  string
	RepositoryRoot string
}

// OwnerOutcomeIntakeAuthority is a provider-composition trust boundary. Its
// implementation may inspect protected repository policy, maintainer control
// planes, and the requested outcome. It returns typed facts, not already-issued
// content hashes. Implementations must never derive authority from candidate or
// PR-head content.
type OwnerOutcomeIntakeAuthority interface {
	ResolveOutcomeIntake(
		context.Context,
		OwnerOutcomeContext,
		OutcomeStartRequest,
	) (OwnerOutcomeIntake, error)
}

// OwnerDestinationInput omits RepositoryRef because the service derives that
// identity from its retained lease.
type OwnerDestinationInput struct {
	TargetRef        string
	ObservedRevision string
	DefaultBranch    bool
}

// OwnerAuthoritySignalInput omits policy and content references. The service
// binds the current live PAD policy and derives the immutable signal ref.
type OwnerAuthoritySignalInput struct {
	SignalID   string
	IssuerRef  string
	Provenance deliveryadmission.AuthorityProvenance
	ExpiresAt  int64
}

// OwnerResidualRiskInput is accepted only from the injected owner authority for
// an emergency governance decision. Subject, route, destination, and policy
// bindings are derived by the service.
type OwnerResidualRiskInput struct {
	RiskID      string
	Description string
	ExpiresAt   int64
}

// OwnerGovernanceInput contains route-specific owner facts without any derived
// subject, policy, authority, risk, or repository references.
type OwnerGovernanceInput struct {
	DecisionID       string
	ApprovedIssueRef string
	Reason           string
	ExpiresAt        int64
	ResidualRisk     *OwnerResidualRiskInput
}

// OwnerOutcomeIntake is returned by the trusted control-plane authority, never
// by the outcome caller. PAD policy remains separately owner-controlled in the
// live trusted repository and is resolved rather than supplied here.
type OwnerOutcomeIntake struct {
	WorkRunID string
	Nonce     string
	// Route is an owner request to PAD. Empty intentionally delegates to PAD's
	// canonical governed default; it is never normalized by workprovider.
	Route          deliveryadmission.Route
	ScopeSelectors []string
	Destination    OwnerDestinationInput

	PrimaryAuthority OwnerAuthoritySignalInput
	SecondAuthority  *OwnerAuthoritySignalInput
	Governance       *OwnerGovernanceInput
	RoutingFacts     workrun.ImplementationRouteInput
	// DeclinedSDDFallback is supplied only by the trusted intake authority and
	// only when the primary facts would propose optional SDD. The provider
	// persists the resulting direct/delegated decision before START; a route
	// consumer can decline SDD but can never author the replacement route.
	DeclinedSDDFallback *workrun.ImplementationRouteInput `json:"declinedSDDFallback,omitempty"`
}

func (intake OwnerOutcomeIntake) validate(
	repositoryRef string,
	request OutcomeStartRequest,
) error {
	return intake.validateAllowLegacyMissingFallback(
		repositoryRef,
		request,
		false,
	)
}

func (intake OwnerOutcomeIntake) validateStructure(
	repositoryRef string,
	request OutcomeStartRequest,
) error {
	return intake.validateAllowLegacyMissingFallback(
		repositoryRef,
		request,
		true,
	)
}

func (intake OwnerOutcomeIntake) validateAllowLegacyMissingFallback(
	repositoryRef string,
	request OutcomeStartRequest,
	allowLegacyMissingFallback bool,
) error {
	if !workRunIDPattern.MatchString(intake.WorkRunID) {
		return errors.New("owner outcome intake returned an invalid work-run identifier")
	}
	if intake.Route != "" && !validOutcomeDeliveryRoute(intake.Route) {
		return errors.New("owner outcome intake returned an invalid delivery route")
	}
	if err := validateOwnerOutcomeToken("nonce", intake.Nonce); err != nil {
		return err
	}
	if err := validateOwnerOutcomeDestinationShape(intake.Destination); err != nil {
		return err
	}
	if intake.RoutingFacts.ExplicitSDDRequestRef != "" {
		return errors.New(
			"owner outcome intake cannot provide an explicit SDD authority reference",
		)
	}
	if intake.RoutingFacts.WriteIntent == "" {
		return errors.New(
			"owner outcome intake must explicitly classify write intent",
		)
	}
	if intake.RoutingFacts.WriteIntent == workrun.WriteIntentNone {
		if intake.DeclinedSDDFallback != nil {
			return errors.New(
				"unmanaged outcome intake cannot provide an SDD decline fallback",
			)
		}
		return workrun.ValidateImplementationRouteInput(intake.RoutingFacts)
	}
	if _, err := outcomeScopeDigest(
		repositoryRef,
		request.Outcome,
		intake.ScopeSelectors,
	); err != nil {
		return err
	}
	primary, err := workrun.DecideImplementationRoute(intake.RoutingFacts)
	if err != nil {
		return fmt.Errorf("validate owner implementation routing facts: %w", err)
	}
	awaitsSDDDecision := primary.Decision == workrun.RouteDecisionProposeSDD &&
		!request.ExplicitSDDRequested
	if !awaitsSDDDecision {
		if intake.DeclinedSDDFallback != nil {
			return errors.New(
				"owner outcome intake provided an SDD decline fallback without a pending optional proposal",
			)
		}
		return nil
	}
	if intake.DeclinedSDDFallback == nil {
		if allowLegacyMissingFallback {
			return nil
		}
		return errors.New(
			"optional SDD proposal requires an owner-authored decline fallback",
		)
	}
	fallback, err := workrun.DecideImplementationRoute(
		*intake.DeclinedSDDFallback,
	)
	if err != nil {
		return fmt.Errorf("validate owner SDD decline fallback: %w", err)
	}
	if fallback.Decision != workrun.RouteDecisionDirectInline &&
		fallback.Decision != workrun.RouteDecisionDelegatedDirect {
		return errors.New(
			"owner SDD decline fallback must select direct or delegated work",
		)
	}
	return nil
}

func (intake OwnerOutcomeIntake) validateSelectedRoute(
	selectedRoute deliveryadmission.Route,
	requestedAt int64,
) (OwnerOutcomeIntake, error) {
	if !validOutcomeDeliveryRoute(selectedRoute) || requestedAt <= 0 {
		return OwnerOutcomeIntake{}, errors.New(
			"PAD selected an invalid owner delivery route",
		)
	}
	if intake.Route != "" && intake.Route != selectedRoute {
		return OwnerOutcomeIntake{}, errors.New(
			"PAD selected a delivery route different from owner intake",
		)
	}
	intake.Route = selectedRoute
	if err := validateOwnerOutcomeDestinationRoute(
		selectedRoute,
		intake.Destination,
	); err != nil {
		return OwnerOutcomeIntake{}, err
	}
	if err := validateOwnerOutcomeAuthorities(intake, requestedAt); err != nil {
		return OwnerOutcomeIntake{}, err
	}
	switch selectedRoute {
	case deliveryadmission.RoutePRWithIssue:
		if intake.Governance == nil || intake.Governance.ResidualRisk != nil {
			return OwnerOutcomeIntake{}, errors.New(
				"PR-with-issue intake requires issue governance without residual risk",
			)
		}
	case deliveryadmission.RoutePRWithoutIssue,
		deliveryadmission.RouteDirectMain:
		if intake.Governance != nil {
			return OwnerOutcomeIntake{}, errors.New(
				"issue-free intake cannot provide governance",
			)
		}
	case deliveryadmission.RouteEmergency:
		if intake.Governance == nil || intake.Governance.ResidualRisk == nil {
			return OwnerOutcomeIntake{}, errors.New(
				"emergency intake requires owner governance and residual risk",
			)
		}
	}
	return intake, nil
}

// OutcomeService is the outcome-first product use case. It performs an
// activation preflight, asks the injected owner authority for trusted facts,
// composes the existing owners, admits PAD intent, starts WorkRun, and returns
// only the existing public status contract.
type OutcomeService struct {
	factory *ProductionOwnerCoordinatorFactory
	intake  OwnerOutcomeIntakeAuthority
}

func NewProductionOutcomeService(
	ctx context.Context,
	repo string,
	agent model.AgentID,
	intake OwnerOutcomeIntakeAuthority,
	activation ActivationResolver,
) (*OutcomeService, error) {
	if intake == nil {
		return nil, errors.New("outcome service requires owner intake authority")
	}
	factory, err := NewProductionOwnerCoordinatorFactory(
		ctx,
		repo,
		agent,
		activation,
	)
	if err != nil {
		return nil, err
	}
	return &OutcomeService{factory: factory, intake: intake}, nil
}

// NewDefaultOutcomeService uses the environment kill switch while leaving the
// canonical capability manifest untouched and dormant.
func NewDefaultOutcomeService(
	ctx context.Context,
	repo string,
	agent model.AgentID,
	intake OwnerOutcomeIntakeAuthority,
) (*OutcomeService, error) {
	return NewProductionOutcomeService(
		ctx,
		repo,
		agent,
		intake,
		EnvironmentActivationResolver{},
	)
}

func (service *OutcomeService) StartOutcome(
	ctx context.Context,
	request OutcomeStartRequest,
) (workrun.WorkStatusV1, error) {
	if err := request.validate(); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	if service == nil || service.factory == nil || service.intake == nil {
		return workrun.WorkStatusV1{}, errors.New("outcome service is not initialized")
	}

	// This owner-controlled activation + ACI snapshot intentionally precedes
	// the injected intake authority. Unknown providers and non-productive
	// activation modes cannot trigger intake work or create provider storage.
	preflight, err := service.factory.preflightProductiveOwner(ctx)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	ownerContext := OwnerOutcomeContext{
		RepositoryRef:  service.factory.RepositoryRef(),
		RepositoryRoot: service.factory.repositoryRoot(),
	}
	intake, err := service.intake.ResolveOutcomeIntake(
		ctx,
		ownerContext,
		request,
	)
	if err != nil {
		return workrun.WorkStatusV1{}, fmt.Errorf(
			"resolve owner-controlled outcome intake: %w",
			err,
		)
	}
	if err := intake.validateStructure(
		ownerContext.RepositoryRef,
		request,
	); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	if replay, found, err := service.replayLegacySDDStart(
		ctx,
		preflight,
		intake,
		request,
	); err != nil {
		return workrun.WorkStatusV1{}, err
	} else if found {
		return replay, nil
	}
	if err := intake.validate(ownerContext.RepositoryRef, request); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	selectors, err := canonicalProductiveSelectors(intake.ScopeSelectors)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	intake.ScopeSelectors = selectors
	if intake.RoutingFacts.WriteIntent == workrun.WriteIntentNone {
		return workrun.WorkStatusV1{}, ErrOutcomeNotManaged
	}
	scopeDigest, err := outcomeScopeDigest(
		ownerContext.RepositoryRef,
		request.Outcome,
		selectors,
	)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	sddDeclineFallback, err := ownerSDDDeclineFallback(intake, request)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}

	coordinator, err := service.factory.openWithProductivePreflight(
		ctx,
		intake.WorkRunID,
		preflight,
	)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	admission, err := coordinator.admitOwnerOutcome(ctx, request.Outcome, intake)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	sddDeclineFallbackRef := ""
	if sddDeclineFallback != nil {
		published, publishErr :=
			coordinator.coordination.PublishSDDDeclineFallback(
				ctx,
				SDDDeclineFallbackPublication{
					WorkRunID:             intake.WorkRunID,
					DeliveryIntentRef:     admission.IntentRef,
					PendingDecisionDigest: sddDeclineFallback.PendingDecisionDigest,
					RouteDecision:         sddDeclineFallback.RouteDecision,
				},
			)
		if publishErr != nil {
			return workrun.WorkStatusV1{}, publishErr
		}
		resolved, resolveErr :=
			coordinator.coordination.ResolveSDDDeclineFallback(
				ctx,
				published.AuthorityRef,
			)
		if resolveErr != nil {
			return workrun.WorkStatusV1{}, resolveErr
		}
		if resolved.AuthorityRef != published.AuthorityRef ||
			resolved.RepositoryIdentity !=
				service.factory.RepositoryRef() ||
			resolved.WorkRunID != intake.WorkRunID ||
			resolved.DeliveryIntentRef != admission.IntentRef ||
			resolved.PendingDecisionDigest !=
				sddDeclineFallback.PendingDecisionDigest ||
			resolved.RouteDecision.Digest !=
				sddDeclineFallback.RouteDecision.Digest {
			return workrun.WorkStatusV1{}, errors.New(
				"published SDD decline fallback authority differs from owner intake",
			)
		}
		sddDeclineFallbackRef = published.AuthorityRef
	}
	advanceStore, err := openProductiveAdvanceStore(
		ctx,
		service.factory.lease,
		intake.WorkRunID,
	)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	if err := advanceStore.publishStartAuthority(
		ctx,
		productiveStartAuthority{
			Schema:                productiveStartAuthoritySchema,
			RepositoryRef:         service.factory.RepositoryRef(),
			WorkRunID:             intake.WorkRunID,
			AgentID:               service.factory.agent,
			DeliveryIntentRef:     admission.IntentRef,
			Outcome:               request.Outcome,
			ScopeDigest:           scopeDigest,
			BaseRevision:          intake.Destination.ObservedRevision,
			ScopeSelectors:        selectors,
			SDDDeclineFallbackRef: sddDeclineFallbackRef,
		},
	); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	state, err := coordinator.StartWork(ctx, OwnerStartWorkRequest{
		DeliveryIntentRef:     admission.IntentRef,
		RouteInput:            intake.RoutingFacts,
		SDDDeclineFallbackRef: sddDeclineFallbackRef,
		ExplicitSDDRequested:  request.ExplicitSDDRequested,
	})
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	status, err := coordinator.work.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	if status.WorkRunID != state.WorkRunID ||
		status.Revision != state.Revision ||
		status.DeliveryIntentRef != admission.IntentRef {
		return workrun.WorkStatusV1{}, ErrProviderResultMismatch
	}
	return status, nil
}

// replayLegacySDDStart provides compatibility only after trusted intake has
// named the exact WorkRun and nonce. It never indexes or deduplicates by
// caller-authored outcome text, does not promise general START retry, and does
// not retrofit the immutable v1 START authority. Legacy accept remains valid;
// decline remains fail-closed because no immutable fallback ref exists.
func (service *OutcomeService) replayLegacySDDStart(
	ctx context.Context,
	preflight ownerProductivePreflight,
	intake OwnerOutcomeIntake,
	request OutcomeStartRequest,
) (workrun.WorkStatusV1, bool, error) {
	if err := preflight.validate(ctx, service.factory); err != nil {
		return workrun.WorkStatusV1{}, false, err
	}
	store, err := existingProductiveAdvanceStore(
		preflight.lease,
		intake.WorkRunID,
	)
	if err != nil {
		return workrun.WorkStatusV1{}, false, err
	}
	authority, err := store.startAuthority(ctx)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return workrun.WorkStatusV1{}, false, nil
		}
		return workrun.WorkStatusV1{}, false, err
	}
	if authority.SDDDeclineFallbackRef != "" {
		return workrun.WorkStatusV1{}, false, nil
	}
	work, err := workrun.OpenWorkRunStoreWithRepositoryIdentityLease(
		ctx,
		preflight.lease,
		intake.WorkRunID,
	)
	if err != nil {
		return workrun.WorkStatusV1{}, false, err
	}
	state, err := work.Status()
	if err != nil {
		return workrun.WorkStatusV1{}, false, err
	}
	if state.RouteDecision.Decision != workrun.RouteDecisionProposeSDD {
		return workrun.WorkStatusV1{}, false, nil
	}
	if err := service.validateLegacySDDStartBinding(
		ctx,
		authority,
		state,
		intake,
		request,
	); err != nil {
		return workrun.WorkStatusV1{}, false, err
	}
	status, err := work.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkStatusV1{}, false, err
	}
	return status, true, nil
}

func (service *OutcomeService) validateLegacySDDStartBinding(
	ctx context.Context,
	authority productiveStartAuthority,
	state workrun.WorkRunState,
	intake OwnerOutcomeIntake,
	request OutcomeStartRequest,
) (resultErr error) {
	selectors, err := canonicalProductiveSelectors(intake.ScopeSelectors)
	if err != nil {
		return err
	}
	scopeDigest, err := outcomeScopeDigest(
		service.factory.RepositoryRef(),
		request.Outcome,
		selectors,
	)
	if err != nil {
		return err
	}
	if !state.Started ||
		authority.Outcome != request.Outcome ||
		authority.WorkRunID != intake.WorkRunID ||
		state.WorkRunID != authority.WorkRunID ||
		state.DeliveryIntentRef != authority.DeliveryIntentRef ||
		authority.ScopeDigest != scopeDigest ||
		!equalProductiveStrings(authority.ScopeSelectors, selectors) ||
		authority.BaseRevision != intake.Destination.ObservedRevision ||
		state.RouteDecision.Decision != workrun.RouteDecisionProposeSDD ||
		state.Handoff != nil ||
		state.ProductiveBlockerRef != "" ||
		state.VerificationResultRef != "" ||
		state.ReviewReceiptRef != "" ||
		state.DeliveryAuthorizationRef != "" ||
		state.DeliveryResultRef != "" {
		return ErrProviderResultMismatch
	}
	explicit := state.RouteDecision.ExplicitSDDRequestRef != ""
	if explicit != request.ExplicitSDDRequested {
		return ErrProviderResultMismatch
	}
	if explicit {
		if state.ImplementationRoute != workrun.ImplementationRouteSDD ||
			state.RouteAcceptanceRef !=
				state.RouteDecision.ExplicitSDDRequestRef {
			return ErrProviderResultMismatch
		}
	} else {
		primary, err := workrun.DecideImplementationRoute(
			intake.RoutingFacts,
		)
		if err != nil ||
			primary.Decision != workrun.RouteDecisionProposeSDD ||
			primary.Digest != state.RouteDecision.Digest ||
			(state.ImplementationRoute != "" &&
				state.ImplementationRoute !=
					workrun.ImplementationRouteSDD) {
			return ErrProviderResultMismatch
		}
	}

	opened, err := service.factory.pad.openRepository(ctx)
	if err != nil {
		return err
	}
	pad, ok := opened.(ownerPADOutcomeRepository)
	if !ok {
		_ = opened.Close()
		return errors.New(
			"PAD owner repository does not support legacy START replay",
		)
	}
	defer func() {
		if closeErr := pad.Close(); resultErr == nil && closeErr != nil {
			resultErr = fmt.Errorf(
				"close PAD legacy START replay repository: %w",
				closeErr,
			)
		}
	}()
	admitted, err := pad.ResolveAdmittedDecision(
		ctx,
		authority.DeliveryIntentRef,
	)
	if err != nil {
		return err
	}
	intent := admitted.Decision.Intent
	if intent.Nonce != intake.Nonce ||
		intent.ScopeDigest != authority.ScopeDigest ||
		intent.Destination.RepositoryRef != service.factory.RepositoryRef() ||
		intent.Destination.TargetRef != intake.Destination.TargetRef ||
		intent.Destination.ObservedRevision !=
			intake.Destination.ObservedRevision ||
		intent.Destination.DefaultBranch != intake.Destination.DefaultBranch ||
		(intake.Route != "" && intent.Route != intake.Route) {
		return ErrProviderResultMismatch
	}
	replayIntake, err := intake.validateSelectedRoute(
		intent.Route,
		intent.RequestedAt,
	)
	if err != nil {
		return err
	}
	preimages, err := buildOwnerOutcomePreimages(
		service.factory.RepositoryRef(),
		admitted.Decision.Policy,
		intent,
		replayIntake,
	)
	if err != nil {
		return err
	}
	if _, err := ownerResolvedAdmission(
		OwnerDeliveryAdmissionRequest{
			Intent:             preimages.intent,
			GovernanceRef:      preimages.governanceRef,
			AuthorityRef:       preimages.authorityRef,
			SecondAuthorityRef: preimages.secondAuthorityRef,
		},
		admitted,
	); err != nil {
		return ErrProviderResultMismatch
	}
	return nil
}

func ownerSDDDeclineFallback(
	intake OwnerOutcomeIntake,
	request OutcomeStartRequest,
) (*productiveSDDDeclineFallback, error) {
	if intake.DeclinedSDDFallback == nil {
		return nil, nil
	}
	if request.ExplicitSDDRequested {
		return nil, errors.New(
			"explicit SDD work cannot carry a decline fallback",
		)
	}
	primary, err := workrun.DecideImplementationRoute(intake.RoutingFacts)
	if err != nil {
		return nil, fmt.Errorf(
			"derive pending SDD proposal for decline fallback: %w",
			err,
		)
	}
	if primary.Decision != workrun.RouteDecisionProposeSDD {
		return nil, errors.New(
			"SDD decline fallback does not bind a pending proposal",
		)
	}
	fallback, err := workrun.DecideImplementationRoute(
		*intake.DeclinedSDDFallback,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"derive owner SDD decline fallback decision: %w",
			err,
		)
	}
	binding := &productiveSDDDeclineFallback{
		PendingDecisionDigest: primary.Digest,
		RouteDecision:         fallback,
	}
	if err := validateProductiveSDDDeclineFallback(binding); err != nil {
		return nil, err
	}
	return binding, nil
}

type ownerPADOutcomeRepository interface {
	ownerPADAdmissionRepository
	PublishAuthoritySignal(
		context.Context,
		deliveryadmission.AuthoritySignal,
	) (string, error)
	PublishGovernanceDecision(
		context.Context,
		deliveryadmission.GovernanceDecision,
	) (string, error)
	PublishResidualRisk(
		context.Context,
		deliveryadmission.ResidualRisk,
	) (string, error)
}

type ownerOutcomePreimages struct {
	intent             deliveryadmission.DeliveryIntent
	authorityRef       string
	secondAuthorityRef string
	governanceRef      string
}

func (coordinator *OwnerCoordinator) admitOwnerOutcome(
	ctx context.Context,
	outcome string,
	intake OwnerOutcomeIntake,
) (OwnerDeliveryAdmission, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return OwnerDeliveryAdmission{}, err
	}
	preimages, err := coordinator.publishOwnerOutcomePreimages(
		ctx,
		outcome,
		intake,
	)
	if err != nil {
		return OwnerDeliveryAdmission{}, err
	}
	return coordinator.AdmitDeliveryIntent(ctx, OwnerDeliveryAdmissionRequest{
		Intent:             preimages.intent,
		GovernanceRef:      preimages.governanceRef,
		AuthorityRef:       preimages.authorityRef,
		SecondAuthorityRef: preimages.secondAuthorityRef,
	})
}

func (coordinator *OwnerCoordinator) publishOwnerOutcomePreimages(
	ctx context.Context,
	outcome string,
	intake OwnerOutcomeIntake,
) (
	result ownerOutcomePreimages,
	err error,
) {
	opened, err := coordinator.pad.openRepository(ctx)
	if err != nil {
		return ownerOutcomePreimages{}, err
	}
	pad, ok := opened.(ownerPADOutcomeRepository)
	if !ok {
		_ = opened.Close()
		return ownerOutcomePreimages{}, errors.New(
			"PAD owner repository does not support outcome intake",
		)
	}
	defer func() {
		if closeErr := pad.Close(); err == nil && closeErr != nil {
			result = ownerOutcomePreimages{}
			err = fmt.Errorf("close PAD outcome repository: %w", closeErr)
		}
	}()

	current, statusErr := coordinator.work.Status()
	switch {
	case statusErr != nil &&
		!errors.Is(statusErr, workrun.ErrWorkRunNotStarted):
		return ownerOutcomePreimages{}, statusErr
	}

	repositoryRef := coordinator.pad.authority.RepositoryRef()
	destination := deliveryadmission.DestinationBinding{
		RepositoryRef:    repositoryRef,
		TargetRef:        intake.Destination.TargetRef,
		ObservedRevision: intake.Destination.ObservedRevision,
		DefaultBranch:    intake.Destination.DefaultBranch,
	}
	scopeDigest, err := outcomeScopeDigest(
		repositoryRef,
		outcome,
		intake.ScopeSelectors,
	)
	if err != nil {
		return ownerOutcomePreimages{}, err
	}
	selectedIntent, err := deliveryadmission.SelectDeliveryIntent(
		ctx,
		pad,
		deliveryadmission.IntentSelectionRequest{
			Nonce:          intake.Nonce,
			RequestedRoute: intake.Route,
			ScopeDigest:    scopeDigest,
			Destination:    destination,
		},
	)
	if err != nil {
		return ownerOutcomePreimages{}, fmt.Errorf(
			"select owner delivery intent: %w",
			err,
		)
	}
	policy, err := pad.ResolveRoutePolicy(ctx, selectedIntent.PolicyRef)
	if err != nil {
		return ownerOutcomePreimages{}, fmt.Errorf(
			"resolve selected owner delivery policy: %w",
			err,
		)
	}

	intent := selectedIntent
	var resolved deliveryadmission.AdmittedDecision
	if statusErr == nil {
		resolved, err = pad.ResolveAdmittedDecision(
			ctx,
			current.DeliveryIntentRef,
		)
		if err != nil {
			return ownerOutcomePreimages{}, err
		}
		intent = resolved.Decision.Intent
		reselected := selectedIntent
		reselected.RequestedAt = intent.RequestedAt
		if reselected != intent {
			return ownerOutcomePreimages{}, workrun.ErrWorkRunAlreadyStarted
		}
	}
	intake, err = intake.validateSelectedRoute(
		intent.Route,
		intent.RequestedAt,
	)
	if err != nil {
		return ownerOutcomePreimages{}, err
	}
	preimages, err := buildOwnerOutcomePreimages(
		repositoryRef,
		policy,
		intent,
		intake,
	)
	if err != nil {
		return ownerOutcomePreimages{}, err
	}
	intentRef, err := preimages.intent.Ref()
	if err != nil {
		return ownerOutcomePreimages{}, err
	}
	if statusErr == nil && current.DeliveryIntentRef != intentRef {
		return ownerOutcomePreimages{}, workrun.ErrWorkRunAlreadyStarted
	}
	ownerPreimages := ownerOutcomePreimages{
		intent:             preimages.intent,
		authorityRef:       preimages.authorityRef,
		secondAuthorityRef: preimages.secondAuthorityRef,
		governanceRef:      preimages.governanceRef,
	}
	if statusErr == nil {
		if _, err := ownerResolvedAdmission(
			OwnerDeliveryAdmissionRequest{
				Intent:             preimages.intent,
				GovernanceRef:      preimages.governanceRef,
				AuthorityRef:       preimages.authorityRef,
				SecondAuthorityRef: preimages.secondAuthorityRef,
			},
			resolved,
		); err != nil {
			return ownerOutcomePreimages{}, err
		}
		return ownerPreimages, nil
	}

	authorityRef, err := pad.PublishAuthoritySignal(
		ctx,
		preimages.primaryAuthority,
	)
	if err != nil {
		return ownerOutcomePreimages{}, err
	}
	if authorityRef != preimages.authorityRef {
		return ownerOutcomePreimages{}, errors.New(
			"PAD published a mismatched primary outcome authority",
		)
	}
	if preimages.secondAuthority != nil {
		secondRef, err := pad.PublishAuthoritySignal(
			ctx,
			*preimages.secondAuthority,
		)
		if err != nil {
			return ownerOutcomePreimages{}, err
		}
		if secondRef != preimages.secondAuthorityRef {
			return ownerOutcomePreimages{}, errors.New(
				"PAD published a mismatched secondary outcome authority",
			)
		}
	}
	if preimages.residualRisk != nil {
		riskRef, err := pad.PublishResidualRisk(ctx, *preimages.residualRisk)
		if err != nil {
			return ownerOutcomePreimages{}, err
		}
		if riskRef != preimages.residualRiskRef {
			return ownerOutcomePreimages{}, errors.New(
				"PAD published a mismatched outcome residual risk",
			)
		}
	}
	if preimages.governance != nil {
		governanceRef, err := pad.PublishGovernanceDecision(
			ctx,
			*preimages.governance,
		)
		if err != nil {
			return ownerOutcomePreimages{}, err
		}
		if governanceRef != preimages.governanceRef {
			return ownerOutcomePreimages{}, errors.New(
				"PAD published mismatched outcome governance",
			)
		}
	}
	return ownerPreimages, nil
}

type builtOwnerOutcomePreimages struct {
	intent             deliveryadmission.DeliveryIntent
	primaryAuthority   deliveryadmission.AuthoritySignal
	authorityRef       string
	secondAuthority    *deliveryadmission.AuthoritySignal
	secondAuthorityRef string
	residualRisk       *deliveryadmission.ResidualRisk
	residualRiskRef    string
	governance         *deliveryadmission.GovernanceDecision
	governanceRef      string
}

func buildOwnerOutcomePreimages(
	repositoryRef string,
	policy deliveryadmission.RoutePolicy,
	intent deliveryadmission.DeliveryIntent,
	intake OwnerOutcomeIntake,
) (builtOwnerOutcomePreimages, error) {
	if err := intent.Validate(); err != nil {
		return builtOwnerOutcomePreimages{}, fmt.Errorf(
			"validate selected owner delivery intent: %w",
			err,
		)
	}
	if err := policy.Validate(); err != nil {
		return builtOwnerOutcomePreimages{}, fmt.Errorf(
			"validate owner live delivery policy: %w",
			err,
		)
	}
	policyRef, err := policy.Ref()
	if err != nil {
		return builtOwnerOutcomePreimages{}, err
	}
	if policy.Route != intake.Route ||
		intent.Route != intake.Route ||
		intent.Destination.RepositoryRef != repositoryRef ||
		intent.PolicyRef != policyRef ||
		intent.Policy != policy.Binding() {
		return builtOwnerOutcomePreimages{}, errors.New(
			"selected owner intent, policy, and intake do not match",
		)
	}
	if !policy.Enabled {
		return builtOwnerOutcomePreimages{}, errors.New(
			"owner live delivery policy does not enable the requested route",
		)
	}
	if policy.RequireSecondMaintainer != (intake.SecondAuthority != nil) {
		return builtOwnerOutcomePreimages{}, errors.New(
			"owner authority count does not match live delivery policy",
		)
	}
	destination := intent.Destination
	intentRef, err := intent.Ref()
	if err != nil {
		return builtOwnerOutcomePreimages{}, err
	}

	primary := deliveryadmission.AuthoritySignal{
		Schema:     deliveryadmission.AuthoritySignalContractV1,
		SignalID:   intake.PrimaryAuthority.SignalID,
		IssuerRef:  intake.PrimaryAuthority.IssuerRef,
		Provenance: intake.PrimaryAuthority.Provenance,
		PolicyRef:  policyRef,
		Policy:     policy.Binding(),
		ExpiresAt:  intake.PrimaryAuthority.ExpiresAt,
	}
	primaryRef, err := primary.Ref()
	if err != nil {
		return builtOwnerOutcomePreimages{}, fmt.Errorf(
			"build primary owner authority: %w",
			err,
		)
	}
	result := builtOwnerOutcomePreimages{
		intent:           intent,
		primaryAuthority: primary,
		authorityRef:     primaryRef,
	}
	if intake.SecondAuthority != nil {
		second := deliveryadmission.AuthoritySignal{
			Schema:     deliveryadmission.AuthoritySignalContractV1,
			SignalID:   intake.SecondAuthority.SignalID,
			IssuerRef:  intake.SecondAuthority.IssuerRef,
			Provenance: intake.SecondAuthority.Provenance,
			PolicyRef:  policyRef,
			Policy:     policy.Binding(),
			ExpiresAt:  intake.SecondAuthority.ExpiresAt,
		}
		secondRef, err := second.Ref()
		if err != nil {
			return builtOwnerOutcomePreimages{}, fmt.Errorf(
				"build secondary owner authority: %w",
				err,
			)
		}
		result.secondAuthority = &second
		result.secondAuthorityRef = secondRef
	}

	switch intake.Route {
	case deliveryadmission.RoutePRWithIssue:
		governance := deliveryadmission.GovernanceDecision{
			Schema:             deliveryadmission.GovernanceDecisionContractV1,
			Kind:               deliveryadmission.GovernanceApprovedIssue,
			DecisionID:         intake.Governance.DecisionID,
			SubjectRef:         intentRef,
			Route:              intake.Route,
			RepositoryRef:      repositoryRef,
			Destination:        destination,
			PolicyRef:          policyRef,
			Policy:             policy.Binding(),
			AuthorityRef:       primaryRef,
			SecondAuthorityRef: result.secondAuthorityRef,
			ApprovedIssueRef:   intake.Governance.ApprovedIssueRef,
			ExpiresAt:          intake.Governance.ExpiresAt,
		}
		governanceRef, err := governance.Ref()
		if err != nil {
			return builtOwnerOutcomePreimages{}, fmt.Errorf(
				"build approved-issue governance: %w",
				err,
			)
		}
		result.governance = &governance
		result.governanceRef = governanceRef
	case deliveryadmission.RouteEmergency:
		risk := deliveryadmission.ResidualRisk{
			Schema:      deliveryadmission.ResidualRiskContractV1,
			RiskID:      intake.Governance.ResidualRisk.RiskID,
			SubjectRef:  intentRef,
			Route:       intake.Route,
			Destination: destination,
			PolicyRef:   policyRef,
			Policy:      policy.Binding(),
			Description: intake.Governance.ResidualRisk.Description,
			ExpiresAt:   intake.Governance.ResidualRisk.ExpiresAt,
		}
		riskRef, err := risk.Ref()
		if err != nil {
			return builtOwnerOutcomePreimages{}, fmt.Errorf(
				"build emergency residual risk: %w",
				err,
			)
		}
		governance := deliveryadmission.GovernanceDecision{
			Schema:             deliveryadmission.GovernanceDecisionContractV1,
			Kind:               deliveryadmission.GovernanceEmergency,
			DecisionID:         intake.Governance.DecisionID,
			SubjectRef:         intentRef,
			Route:              intake.Route,
			RepositoryRef:      repositoryRef,
			Destination:        destination,
			PolicyRef:          policyRef,
			Policy:             policy.Binding(),
			AuthorityRef:       primaryRef,
			SecondAuthorityRef: result.secondAuthorityRef,
			Reason:             intake.Governance.Reason,
			ResidualRiskRef:    riskRef,
			ExpiresAt:          intake.Governance.ExpiresAt,
		}
		governanceRef, err := governance.Ref()
		if err != nil {
			return builtOwnerOutcomePreimages{}, fmt.Errorf(
				"build emergency governance: %w",
				err,
			)
		}
		result.residualRisk = &risk
		result.residualRiskRef = riskRef
		result.governance = &governance
		result.governanceRef = governanceRef
	}
	return result, nil
}

func validateOwnerOutcomeDestinationShape(
	destination OwnerDestinationInput,
) error {
	if !strings.HasPrefix(destination.TargetRef, "refs/heads/") {
		return errors.New(
			"owner outcome destination must use a branch reference",
		)
	}
	if err := validateOwnerOutcomeToken(
		"destination target",
		destination.TargetRef,
	); err != nil {
		return err
	}
	if err := validateOwnerOutcomeToken(
		"destination observed revision",
		destination.ObservedRevision,
	); err != nil {
		return err
	}
	return nil
}

func validateOwnerOutcomeDestinationRoute(
	route deliveryadmission.Route,
	destination OwnerDestinationInput,
) error {
	if (route == deliveryadmission.RouteDirectMain ||
		route == deliveryadmission.RouteEmergency) &&
		!destination.DefaultBranch {
		return errors.New(
			"owner direct outcome destination must be the default branch",
		)
	}
	return nil
}

func validateOwnerOutcomeAuthorities(
	intake OwnerOutcomeIntake,
	requestedAt int64,
) error {
	primary := intake.PrimaryAuthority
	if err := validateOwnerOutcomeToken(
		"primary authority signal",
		primary.SignalID,
	); err != nil {
		return err
	}
	if err := validateOwnerOutcomeToken(
		"primary authority issuer",
		primary.IssuerRef,
	); err != nil {
		return err
	}
	if primary.ExpiresAt <= requestedAt {
		return errors.New(
			"owner primary authority must outlive the outcome request",
		)
	}
	if intake.Route == deliveryadmission.RoutePRWithIssue {
		if primary.Provenance !=
			deliveryadmission.ProvenanceRepositoryPolicy &&
			primary.Provenance !=
				deliveryadmission.ProvenanceMaintainerControl {
			return errors.New(
				"owner PR authority has invalid provenance",
			)
		}
	} else if primary.Provenance !=
		deliveryadmission.ProvenanceMaintainerControl {
		return errors.New(
			"owner issue-free authority must come from maintainer control",
		)
	}

	if intake.SecondAuthority != nil {
		second := *intake.SecondAuthority
		if intake.Route != deliveryadmission.RouteEmergency ||
			second.Provenance !=
				deliveryadmission.ProvenanceSecondMaintainer {
			return errors.New(
				"owner secondary authority must be independent emergency control",
			)
		}
		if err := validateOwnerOutcomeToken(
			"secondary authority signal",
			second.SignalID,
		); err != nil {
			return err
		}
		if err := validateOwnerOutcomeToken(
			"secondary authority issuer",
			second.IssuerRef,
		); err != nil {
			return err
		}
		if second.ExpiresAt <= requestedAt {
			return errors.New(
				"owner secondary authority must outlive the outcome request",
			)
		}
		if second.SignalID == primary.SignalID ||
			second.IssuerRef == primary.IssuerRef {
			return errors.New(
				"owner secondary authority is not independent",
			)
		}
	}

	switch intake.Route {
	case deliveryadmission.RoutePRWithIssue:
		return validateOwnerIssueGovernance(intake, requestedAt)
	case deliveryadmission.RouteEmergency:
		return validateOwnerEmergencyGovernance(intake, requestedAt)
	default:
		return nil
	}
}

func validateOwnerIssueGovernance(
	intake OwnerOutcomeIntake,
	requestedAt int64,
) error {
	governance := intake.Governance
	if governance == nil {
		return errors.New("owner PR-with-issue governance is missing")
	}
	if err := validateOwnerOutcomeToken(
		"governance decision",
		governance.DecisionID,
	); err != nil {
		return err
	}
	if err := validateOwnerOutcomeToken(
		"governance approved issue",
		governance.ApprovedIssueRef,
	); err != nil {
		return err
	}
	if governance.ExpiresAt <= requestedAt {
		return errors.New(
			"owner issue governance must outlive the outcome request",
		)
	}
	if governance.Reason != "" || governance.ResidualRisk != nil {
		return errors.New(
			"owner issue governance contains emergency-only facts",
		)
	}
	return nil
}

func validateOwnerEmergencyGovernance(
	intake OwnerOutcomeIntake,
	requestedAt int64,
) error {
	governance := intake.Governance
	if governance == nil || governance.ResidualRisk == nil {
		return errors.New("owner emergency governance is incomplete")
	}
	if err := validateOwnerOutcomeToken(
		"governance decision",
		governance.DecisionID,
	); err != nil {
		return err
	}
	if governance.ApprovedIssueRef != "" {
		return errors.New(
			"owner emergency governance cannot approve an issue",
		)
	}
	if err := validateOwnerOutcomeText(
		"governance reason",
		governance.Reason,
	); err != nil {
		return err
	}
	if governance.ExpiresAt <= requestedAt {
		return errors.New(
			"owner emergency governance must outlive the outcome request",
		)
	}
	risk := governance.ResidualRisk
	if err := validateOwnerOutcomeToken(
		"residual risk",
		risk.RiskID,
	); err != nil {
		return err
	}
	if err := validateOwnerOutcomeText(
		"residual risk description",
		risk.Description,
	); err != nil {
		return err
	}
	if risk.ExpiresAt <= requestedAt {
		return errors.New(
			"owner residual risk must outlive the outcome request",
		)
	}
	return nil
}

func validateOwnerOutcomeToken(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 512 {
		return fmt.Errorf("owner outcome %s is not canonical", name)
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return fmt.Errorf("owner outcome %s is not canonical", name)
		}
	}
	return nil
}

func validateOwnerOutcomeText(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) ||
		len(value) > 2_048 || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("owner outcome %s is not canonical", name)
	}
	return nil
}

func outcomeScopeDigest(
	repositoryRef string,
	outcome string,
	selectors []string,
) (string, error) {
	if !validPADImmutableRef(repositoryRef) {
		return "", errors.New("owner outcome scope requires repository identity")
	}
	if err := (OutcomeStartRequest{Outcome: outcome}).validate(); err != nil {
		return "", err
	}
	if len(selectors) == 0 || len(selectors) > 10_000 {
		return "", errors.New("owner outcome scope must contain bounded selectors")
	}
	canonical := append([]string(nil), selectors...)
	sort.Strings(canonical)
	for index, selector := range canonical {
		if selector == "" || len(selector) > 4_096 ||
			strings.ContainsAny(selector, "\x00\r\n") {
			return "", errors.New("owner outcome scope contains an invalid selector")
		}
		if index > 0 && canonical[index-1] == selector {
			return "", errors.New("owner outcome scope contains duplicate selectors")
		}
	}
	payload, err := json.Marshal(struct {
		RepositoryRef string   `json:"repositoryRef"`
		Outcome       string   `json:"outcome"`
		Selectors     []string `json:"selectors"`
	}{
		RepositoryRef: repositoryRef,
		Outcome:       outcome,
		Selectors:     canonical,
	})
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("gentle-ai.owner-outcome-scope/v1"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func validOutcomeDeliveryRoute(route deliveryadmission.Route) bool {
	switch route {
	case deliveryadmission.RoutePRWithIssue,
		deliveryadmission.RoutePRWithoutIssue,
		deliveryadmission.RouteDirectMain,
		deliveryadmission.RouteEmergency:
		return true
	default:
		return false
	}
}
