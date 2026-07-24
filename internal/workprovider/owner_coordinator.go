package workprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/mutationintegrity"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/sddstatus"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

// OwnerCoordinator is a private, composition-only use case. It gives the
// provider one typed path through the existing owners without becoming an
// authority, lifecycle, policy engine, command surface, or workflow engine.
//
// In particular, it accepts no argv, shell text, environment, or generic
// payload. HCR inputs can reach it only inside an already-issued EPD
// ActionTicket.
type OwnerCoordinator struct {
	pad          *PADWorkRunAdapter
	sdd          SDDWorkRunAuthority
	work         workrun.WorkRunStore
	coordination *CoordinationAuthorityStore
	rar          *reviewtransaction.RARAuthorityRepository
	transitions  *TransitionAuthorityStore
	evidence     evidence.Store
	mutations    mutationintegrity.Store
	activation   ActivationResolver
	repository   string
}

// OwnerCoordinatorDependencies are already-open owner repositories and the
// narrow owner ports needed by WorkRun. NewOwnerCoordinator only composes
// these values; it performs no publication.
type OwnerCoordinatorDependencies struct {
	WorkRun      workrun.WorkRunStore
	Coordination *CoordinationAuthorityStore
	RAR          *reviewtransaction.RARAuthorityRepository
	Transitions  *TransitionAuthorityStore
	Evidence     evidence.Store
	Mutations    mutationintegrity.Store

	PADAuthority    *PADWorkRunAdapter
	SDDAuthority    SDDWorkRunAuthority
	LaunchAuthority workrun.LaunchAuthorityPort
	Activation      ActivationResolver
}

func NewOwnerCoordinator(
	ctx context.Context,
	dependencies OwnerCoordinatorDependencies,
) (*OwnerCoordinator, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dependencies.Coordination == nil ||
		dependencies.RAR == nil ||
		dependencies.Transitions == nil ||
		dependencies.PADAuthority == nil ||
		dependencies.SDDAuthority.Repo == "" ||
		dependencies.LaunchAuthority == nil ||
		dependencies.Activation == nil {
		return nil, errors.New("owner coordinator requires every owner authority")
	}
	if dependencies.WorkRun.WorkRunID == "" ||
		dependencies.Evidence.Root() == "" ||
		dependencies.Mutations.Root() == "" {
		return nil, errors.New(
			"owner coordinator requires opened WorkRun, EPD, and MMI stores",
		)
	}
	if dependencies.Coordination.workRunID != dependencies.WorkRun.WorkRunID ||
		dependencies.Transitions.workRunID != dependencies.WorkRun.WorkRunID {
		return nil, errors.New("owner coordinator stores belong to different work runs")
	}
	identity := dependencies.Coordination.identity
	expectedWorkRunRoot := filepath.Join(
		identity.gitCommonDir,
		"gentle-ai",
		"work-runs",
		"v1",
		"repositories",
		strings.TrimPrefix(identity.repositoryIdentity, "sha256:"),
		dependencies.WorkRun.WorkRunID,
	)
	expectedEvidenceRoot := filepath.Join(
		identity.gitCommonDir,
		"gentle-ai",
		"execution-evidence",
		"v1",
	)
	expectedMutationRoot := filepath.Join(
		identity.gitCommonDir,
		"gentle-ai",
		"mutation-integrity",
		"v1",
		"repositories",
		identity.lease.StorageKey(),
		"completions",
	)
	sddIdentity, err := resolveCoordinationRepositoryIdentity(
		ctx,
		dependencies.SDDAuthority.Repo,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve SDD owner repository identity: %w", err)
	}
	if dependencies.WorkRun.Repo != identity.repositoryRoot ||
		dependencies.WorkRun.RepositoryRef() != identity.repositoryIdentity ||
		filepath.Clean(dependencies.WorkRun.Dir) != expectedWorkRunRoot ||
		dependencies.Evidence.RepositoryRef() != identity.repositoryIdentity ||
		filepath.Clean(dependencies.Evidence.Root()) != expectedEvidenceRoot ||
		filepath.Clean(dependencies.Mutations.Root()) != expectedMutationRoot ||
		dependencies.Transitions.identity.repositoryIdentity !=
			identity.repositoryIdentity ||
		dependencies.Transitions.root != dependencies.Coordination.root ||
		dependencies.Coordination.plans != dependencies.RAR ||
		dependencies.RAR.RepositoryRef() != identity.repositoryIdentity ||
		dependencies.PADAuthority.authority == nil ||
		dependencies.PADAuthority.authority.identity.repositoryIdentity !=
			identity.repositoryIdentity ||
		sddIdentity.repositoryIdentity != identity.repositoryIdentity {
		return nil, errors.New(
			"owner coordinator authorities belong to different repository identities",
		)
	}

	verification := RARWorkRunAuthority{
		Coordination: dependencies.Coordination,
		RAR:          dependencies.RAR,
	}
	configured := dependencies.WorkRun.
		WithEvidencePort(EvidenceWorkRunPort{Store: dependencies.Evidence}).
		WithAuthorityPorts(workrun.AuthorityPorts{
			PAD:                dependencies.PADAuthority,
			ExplicitSDDRequest: dependencies.Coordination,
			MutationCompletion: MutationCompletionWorkRunAuthority{
				Store: dependencies.Mutations,
			},
			Route:        dependencies.Coordination,
			SDD:          dependencies.SDDAuthority,
			Verification: verification,
			Launch:       dependencies.LaunchAuthority,
		})
	return &OwnerCoordinator{
		pad: dependencies.PADAuthority, sdd: dependencies.SDDAuthority,
		work:         configured,
		coordination: dependencies.Coordination, rar: dependencies.RAR,
		transitions: dependencies.Transitions, evidence: dependencies.Evidence,
		mutations:  dependencies.Mutations,
		activation: dependencies.Activation, repository: identity.repositoryRoot,
	}, nil
}

type OwnerDeliveryAdmissionRequest struct {
	Intent             deliveryadmission.DeliveryIntent
	GovernanceRef      string
	AuthorityRef       string
	SecondAuthorityRef string
}

type OwnerDeliveryAdmission struct {
	IntentRef            string
	AdmissionDecisionRef string
}

type ownerPADAdmissionRepository interface {
	padTrustedRepository
	deliveryadmission.TrustedResolver
	ResolveAdmittedDecision(
		context.Context,
		string,
	) (deliveryadmission.AdmittedDecision, error)
	PublishIntent(
		context.Context,
		deliveryadmission.DeliveryIntent,
	) (string, error)
	PublishAdmissionDecision(
		context.Context,
		deliveryadmission.AdmissionDecision,
	) (string, error)
}

// AdmitDeliveryIntent publishes the typed PAD intent, resolves its complete
// owner-controlled admission preimages, and publishes the resulting decision.
// A failed admission can leave only a harmless immutable intent orphan.
func (coordinator *OwnerCoordinator) AdmitDeliveryIntent(
	ctx context.Context,
	request OwnerDeliveryAdmissionRequest,
) (
	result OwnerDeliveryAdmission,
	err error,
) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return OwnerDeliveryAdmission{}, err
	}
	if err := request.Intent.Validate(); err != nil {
		return OwnerDeliveryAdmission{}, err
	}
	opened, err := coordinator.pad.openRepository(ctx)
	if err != nil {
		return OwnerDeliveryAdmission{}, err
	}
	pad, ok := opened.(ownerPADAdmissionRepository)
	if !ok {
		_ = opened.Close()
		return OwnerDeliveryAdmission{}, errors.New(
			"PAD owner repository does not support delivery admission",
		)
	}
	defer func() {
		if closeErr := pad.Close(); err == nil && closeErr != nil {
			result = OwnerDeliveryAdmission{}
			err = fmt.Errorf("close PAD owner repository: %w", closeErr)
		}
	}()
	intentRef, err := request.Intent.Ref()
	if err != nil {
		return OwnerDeliveryAdmission{}, err
	}
	if resolved, resolveErr := pad.ResolveAdmittedDecision(
		ctx,
		intentRef,
	); resolveErr == nil {
		return ownerResolvedAdmission(request, resolved)
	} else if !errors.Is(
		resolveErr,
		deliveryadmission.ErrAdmittedDecisionNotFound,
	) {
		return OwnerDeliveryAdmission{}, resolveErr
	}
	publishedIntentRef, err := pad.PublishIntent(ctx, request.Intent)
	if err != nil {
		return OwnerDeliveryAdmission{}, err
	}
	if publishedIntentRef != intentRef {
		return OwnerDeliveryAdmission{}, errors.New(
			"PAD published a mismatched delivery intent reference",
		)
	}
	decision, err := deliveryadmission.Admit(
		ctx,
		pad,
		deliveryadmission.AdmissionRequest{
			IntentRef:          intentRef,
			PolicyRef:          request.Intent.PolicyRef,
			GovernanceRef:      request.GovernanceRef,
			AuthorityRef:       request.AuthorityRef,
			SecondAuthorityRef: request.SecondAuthorityRef,
		},
	)
	if err != nil {
		return OwnerDeliveryAdmission{}, err
	}
	decisionRef, err := pad.PublishAdmissionDecision(ctx, decision)
	if err != nil {
		if resolved, resolveErr := pad.ResolveAdmittedDecision(
			ctx,
			intentRef,
		); resolveErr == nil {
			return ownerResolvedAdmission(request, resolved)
		} else {
			return OwnerDeliveryAdmission{}, errors.Join(err, resolveErr)
		}
	}
	if decision.Disposition != deliveryadmission.AdmissionAdmitted {
		return OwnerDeliveryAdmission{}, errors.New("PAD denied the delivery intent")
	}
	return OwnerDeliveryAdmission{
		IntentRef:            intentRef,
		AdmissionDecisionRef: decisionRef,
	}, nil
}

func ownerResolvedAdmission(
	request OwnerDeliveryAdmissionRequest,
	resolved deliveryadmission.AdmittedDecision,
) (OwnerDeliveryAdmission, error) {
	intentRef, err := request.Intent.Ref()
	if err != nil {
		return OwnerDeliveryAdmission{}, err
	}
	decision := resolved.Decision
	if resolved.AdmissionDecisionRef == "" ||
		decision.Disposition != deliveryadmission.AdmissionAdmitted ||
		decision.IntentRef != intentRef ||
		decision.Intent != request.Intent ||
		decision.PolicyRef != request.Intent.PolicyRef ||
		decision.GovernanceRef != request.GovernanceRef ||
		decision.AuthorityRef != request.AuthorityRef ||
		decision.SecondAuthorityRef != request.SecondAuthorityRef {
		return OwnerDeliveryAdmission{}, fmt.Errorf(
			"%w: delivery intent already admitted with different owner inputs",
			deliveryadmission.ErrTrustedRepositoryConflict,
		)
	}
	return OwnerDeliveryAdmission{
		IntentRef:            intentRef,
		AdmissionDecisionRef: resolved.AdmissionDecisionRef,
	}, nil
}

type OwnerStartWorkRequest struct {
	DeliveryIntentRef string
	RouteInput        workrun.ImplementationRouteInput
	// ExplicitSDDRequested is the typed user intent. The coordinator, not its
	// caller, issues the immutable authority reference consumed by the router.
	ExplicitSDDRequested bool
}

// StartWork applies the canonical router once and records its route-neutral
// decision. Direct and delegated decisions never create SDD state.
func (coordinator *OwnerCoordinator) StartWork(
	ctx context.Context,
	request OwnerStartWorkRequest,
) (workrun.WorkRunState, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	if request.RouteInput.ExplicitSDDRequestRef != "" {
		return workrun.WorkRunState{}, errors.New(
			"owner start request cannot provide an explicit SDD authority reference",
		)
	}
	routeInput := request.RouteInput
	if request.ExplicitSDDRequested {
		intent, err := coordinator.pad.ResolveDeliveryIntent(
			ctx,
			request.DeliveryIntentRef,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if err := intent.Validate(request.DeliveryIntentRef); err != nil {
			return workrun.WorkRunState{}, err
		}
		publication := ExplicitSDDRequestPublication{
			WorkRunID:         coordinator.work.WorkRunID,
			DeliveryIntentRef: request.DeliveryIntentRef,
		}
		candidate, err := newExplicitSDDRequestRecord(
			coordinator.coordination.identity.repositoryIdentity,
			publication,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		routeInput.ExplicitSDDRequestRef = candidate.AuthorityRef
		decision, err := workrun.DecideImplementationRoute(routeInput)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		current, statusErr := coordinator.work.Status()
		switch {
		case statusErr == nil:
			if current.DeliveryIntentRef != request.DeliveryIntentRef ||
				current.RouteDecision.Digest != decision.Digest ||
				current.RouteDecision.ExplicitSDDRequestRef !=
					candidate.AuthorityRef {
				return workrun.WorkRunState{}, workrun.ErrWorkRunAlreadyStarted
			}
			authority, err :=
				coordinator.coordination.ResolveExplicitSDDRequest(
					ctx,
					candidate.AuthorityRef,
				)
			if err != nil {
				return workrun.WorkRunState{}, err
			}
			if err := authority.Validate(
				candidate.AuthorityRef,
				coordinator.work.WorkRunID,
				request.DeliveryIntentRef,
			); err != nil {
				return workrun.WorkRunState{}, err
			}
			return current, nil
		case !errors.Is(statusErr, workrun.ErrWorkRunNotStarted):
			return workrun.WorkRunState{}, statusErr
		default:
			published, err := coordinator.coordination.PublishExplicitSDDRequest(
				ctx,
				publication,
			)
			if err != nil {
				return workrun.WorkRunState{}, err
			}
			if published.AuthorityRef != candidate.AuthorityRef {
				return workrun.WorkRunState{}, errors.New(
					"coordination owner published a mismatched explicit SDD request",
				)
			}
		}
		return coordinator.startWork(ctx, request.DeliveryIntentRef, decision)
	}
	decision, err := workrun.DecideImplementationRoute(routeInput)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	current, statusErr := coordinator.work.Status()
	switch {
	case statusErr == nil:
		if current.DeliveryIntentRef != request.DeliveryIntentRef ||
			current.RouteDecision.Digest != decision.Digest ||
			current.RouteDecision.ExplicitSDDRequestRef != "" {
			return workrun.WorkRunState{}, workrun.ErrWorkRunAlreadyStarted
		}
		intent, err := coordinator.pad.ResolveDeliveryIntent(
			ctx,
			request.DeliveryIntentRef,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if err := intent.Validate(request.DeliveryIntentRef); err != nil {
			return workrun.WorkRunState{}, err
		}
		return current, nil
	case !errors.Is(statusErr, workrun.ErrWorkRunNotStarted):
		return workrun.WorkRunState{}, statusErr
	}
	return coordinator.startWork(ctx, request.DeliveryIntentRef, decision)
}

func (coordinator *OwnerCoordinator) startWork(
	ctx context.Context,
	deliveryIntentRef string,
	decision workrun.ImplementationRouteDecision,
) (workrun.WorkRunState, error) {
	requestID, err := ownerRequestID("start", struct {
		DeliveryIntentRef string
		DecisionDigest    string
	}{deliveryIntentRef, decision.Digest})
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	return coordinator.work.Start(ctx, workrun.StartRequest{
		RequestID:         requestID,
		RouteDecision:     decision,
		DeliveryIntentRef: deliveryIntentRef,
	})
}

type OwnerAcceptSDDRequest struct {
	ExpectedRevision      string
	PendingDecisionDigest string
}

// AcceptProposedSDD is the explicit opt-in boundary. The route-selection owner
// publishes acceptance first; WorkRun then binds only its immutable ref.
func (coordinator *OwnerCoordinator) AcceptProposedSDD(
	ctx context.Context,
	request OwnerAcceptSDDRequest,
) (workrun.WorkRunState, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	publication := RouteSelectionPublication{
		WorkRunID:             coordinator.work.WorkRunID,
		PendingDecisionDigest: request.PendingDecisionDigest,
		SelectedRoute:         workrun.ImplementationRouteSDD,
	}
	candidate, err := newRouteSelectionRecord(
		coordinator.coordination.identity.repositoryIdentity,
		publication,
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	current, err := coordinator.work.Status()
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	exactReplay := current.RouteDecision.Decision ==
		workrun.RouteDecisionProposeSDD &&
		current.RouteDecision.Digest == request.PendingDecisionDigest &&
		current.ImplementationRoute == workrun.ImplementationRouteSDD &&
		current.RouteAcceptanceRef == candidate.AuthorityRef
	if current.Revision != request.ExpectedRevision {
		if !exactReplay {
			return workrun.WorkRunState{}, ownerRevisionConflict(
				request.ExpectedRevision,
				current.Revision,
			)
		}
	} else if !ownerPendingSDDProposal(current, request.PendingDecisionDigest) {
		return workrun.WorkRunState{}, fmt.Errorf(
			"%w: SDD proposal is not awaiting acceptance",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	if exactReplay {
		authority, err := coordinator.coordination.ResolveRouteSelection(
			ctx,
			candidate.AuthorityRef,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if err := authority.Validate(
			candidate.AuthorityRef,
			request.PendingDecisionDigest,
			workrun.ImplementationRouteSDD,
			"",
		); err != nil {
			return workrun.WorkRunState{}, err
		}
		return current, nil
	}
	if !exactReplay {
		published, err := coordinator.coordination.PublishRouteSelection(
			ctx,
			publication,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if published.AuthorityRef != candidate.AuthorityRef {
			return workrun.WorkRunState{}, errors.New(
				"route owner published a mismatched SDD acceptance",
			)
		}
	}
	requestID, err := ownerRequestID("accept-sdd", struct {
		ExpectedRevision string
		SelectionRef     string
	}{request.ExpectedRevision, candidate.AuthorityRef})
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	return coordinator.work.AcceptSDD(ctx, workrun.AcceptSDDRequest{
		ExpectedRevision: request.ExpectedRevision,
		RequestID:        requestID,
		AcceptanceRef:    candidate.AuthorityRef,
	})
}

type OwnerRerouteRequest struct {
	ExpectedRevision      string
	PendingDecisionDigest string
	RouteInput            workrun.ImplementationRouteInput
}

// RerouteProposedSDD records a fresh direct/delegated router decision after a
// proposal is declined. It cannot silently turn a proposal into inline work.
func (coordinator *OwnerCoordinator) RerouteProposedSDD(
	ctx context.Context,
	request OwnerRerouteRequest,
) (workrun.WorkRunState, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	decision, err := workrun.DecideImplementationRoute(request.RouteInput)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	selectedRoute := workrun.ImplementationRouteDirectInline
	switch decision.Decision {
	case workrun.RouteDecisionDirectInline:
	case workrun.RouteDecisionDelegatedDirect:
		selectedRoute = workrun.ImplementationRouteDelegatedDirect
	default:
		return workrun.WorkRunState{}, errors.New(
			"owner reroute must resolve to direct or delegated work",
		)
	}
	publication := RouteSelectionPublication{
		WorkRunID:              coordinator.work.WorkRunID,
		PendingDecisionDigest:  request.PendingDecisionDigest,
		SelectedRoute:          selectedRoute,
		SelectedDecisionDigest: decision.Digest,
	}
	candidate, err := newRouteSelectionRecord(
		coordinator.coordination.identity.repositoryIdentity,
		publication,
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	current, err := coordinator.work.Status()
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	exactReplay := current.RouteDecision.Digest == decision.Digest &&
		current.ImplementationRoute == selectedRoute &&
		current.RouteAcceptanceRef == candidate.AuthorityRef
	if current.Revision != request.ExpectedRevision {
		if !exactReplay {
			return workrun.WorkRunState{}, ownerRevisionConflict(
				request.ExpectedRevision,
				current.Revision,
			)
		}
	} else if !ownerPendingSDDProposal(current, request.PendingDecisionDigest) {
		return workrun.WorkRunState{}, fmt.Errorf(
			"%w: SDD proposal is not eligible for reroute",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	if exactReplay {
		authority, err := coordinator.coordination.ResolveRouteSelection(
			ctx,
			candidate.AuthorityRef,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if err := authority.Validate(
			candidate.AuthorityRef,
			request.PendingDecisionDigest,
			selectedRoute,
			decision.Digest,
		); err != nil {
			return workrun.WorkRunState{}, err
		}
		return current, nil
	}
	if !exactReplay {
		published, err := coordinator.coordination.PublishRouteSelection(
			ctx,
			publication,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if published.AuthorityRef != candidate.AuthorityRef {
			return workrun.WorkRunState{}, errors.New(
				"route owner published a mismatched direct reroute",
			)
		}
	}
	requestID, err := ownerRequestID("reroute", struct {
		ExpectedRevision string
		SelectionRef     string
		DecisionDigest   string
	}{request.ExpectedRevision, candidate.AuthorityRef, decision.Digest})
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	return coordinator.work.Reroute(ctx, workrun.RerouteRequest{
		ExpectedRevision: request.ExpectedRevision,
		RequestID:        requestID,
		OwnerDecisionRef: candidate.AuthorityRef,
		RouteDecision:    decision,
	})
}

type OwnerBindSDDRunRequest struct {
	ExpectedRevision   string
	RouteAcceptanceRef string
	RunRef             string
}

// BindAcceptedSDDRun single-assigns a real SDD runtime only after WorkRun has
// recorded explicit acceptance. The preflight prevents rejected proposals from
// publishing synthetic SDD state; exact retries remain valid after binding.
func (coordinator *OwnerCoordinator) BindAcceptedSDDRun(
	ctx context.Context,
	request OwnerBindSDDRunRequest,
) (workrun.WorkRunState, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	current, err := coordinator.work.Status()
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	runtime, err := sddstatus.OpenRuntimeStore(
		ctx,
		coordinator.sdd.Repo,
		request.RunRef,
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	accepted := current.ImplementationRoute == workrun.ImplementationRouteSDD &&
		current.RouteAcceptanceRef == request.RouteAcceptanceRef
	exactReplay := accepted &&
		current.SDDRunRef != "" &&
		current.SDDRunRef == request.RunRef
	if !accepted ||
		(current.Revision != request.ExpectedRevision && !exactReplay) ||
		(current.SDDRunRef != "" && !exactReplay) {
		return workrun.WorkRunState{}, fmt.Errorf(
			"%w: SDD runtime has no accepted WorkRun route",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	if exactReplay {
		binding, err := runtime.ResolveWorkRunBinding(ctx)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if binding.RunRef != request.RunRef ||
			binding.WorkRunID != coordinator.work.WorkRunID ||
			binding.RouteAcceptanceRef != request.RouteAcceptanceRef {
			return workrun.WorkRunState{}, errors.New(
				"SDD owner returned a mismatched WorkRun binding",
			)
		}
		return current, nil
	}
	binding, err := runtime.BindWorkRun(
		ctx,
		coordinator.work.WorkRunID,
		request.RouteAcceptanceRef,
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	if binding.RunRef != request.RunRef ||
		binding.WorkRunID != coordinator.work.WorkRunID ||
		binding.RouteAcceptanceRef != request.RouteAcceptanceRef {
		return workrun.WorkRunState{}, errors.New(
			"SDD owner returned a mismatched WorkRun binding",
		)
	}
	requestID, err := ownerRequestID("bind-sdd", struct {
		ExpectedRevision string
		BindingRef       string
	}{request.ExpectedRevision, binding.BindingRef})
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	return coordinator.work.BindSDDRun(ctx, workrun.BindSDDRunRequest{
		ExpectedRevision: request.ExpectedRevision,
		RequestID:        requestID,
		SDDRunRef:        binding.RunRef,
	})
}

type OwnerBindHandoffRequest struct {
	ExpectedRevision       string
	Target                 reviewtransaction.Target
	IntendedPaths          []string
	DeclaredObligationRefs []string
	EvidenceRefs           []string
	SDDRunRef              string
}

// BindImplementationHandoff observes implementation completion through MMI
// and derives handoff v2 exclusively from owner state plus the resulting live
// snapshot. The caller cannot author route, scope, candidate, or completion
// identity.
func (coordinator *OwnerCoordinator) BindImplementationHandoff(
	ctx context.Context,
	request OwnerBindHandoffRequest,
) (workrun.WorkRunState, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	current, err := coordinator.work.Status()
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	if current.Revision != request.ExpectedRevision &&
		current.Handoff == nil {
		return workrun.WorkRunState{}, ownerRevisionConflict(
			request.ExpectedRevision,
			current.Revision,
		)
	}
	completion, handoff, err := coordinator.observeImplementationCompletion(
		ctx,
		current,
		ownerImplementationCompletionRequest{
			Target:                 request.Target,
			IntendedPaths:          request.IntendedPaths,
			DeclaredObligationRefs: request.DeclaredObligationRefs,
			EvidenceRefs:           request.EvidenceRefs,
			SDDRunRef:              request.SDDRunRef,
		},
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	if current.Handoff != nil {
		if current.Handoff.Digest != handoff.Digest {
			if current.Revision != request.ExpectedRevision {
				return workrun.WorkRunState{}, ownerRevisionConflict(
					request.ExpectedRevision,
					current.Revision,
				)
			}
			return workrun.WorkRunState{}, fmt.Errorf(
				"%w: implementation handoff is already bound",
				workrun.ErrWorkRunInvalidTransition,
			)
		}
		if err := coordinator.resolvePersistedCompletion(
			ctx,
			completion.CompletionRef,
		); err != nil {
			return workrun.WorkRunState{}, err
		}
	} else {
		completionRef, err := coordinator.mutations.Put(ctx, completion)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if completionRef != completion.CompletionRef ||
			completionRef != handoff.MutationCompletionRef {
			return workrun.WorkRunState{}, errors.New(
				"MMI owner persisted a mismatched mutation completion",
			)
		}
	}
	requestID, err := ownerRequestID("bind-handoff", struct {
		ExpectedRevision string
		HandoffDigest    string
		CompletionRef    string
	}{
		request.ExpectedRevision,
		handoff.Digest,
		completion.CompletionRef,
	})
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	applied, err := coordinator.work.BindImplementationHandoff(
		ctx,
		workrun.BindImplementationHandoffRequest{
			ExpectedRevision: request.ExpectedRevision,
			RequestID:        requestID,
			Handoff:          handoff,
		},
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	if current.Handoff != nil {
		// WorkRun returns the historical state associated with an idempotency
		// receipt. The owner already validated that receipt above, but callers
		// must still observe any later progress in the live WorkRun.
		return current, nil
	}
	return applied, nil
}

type OwnerCorrectionReplanRequest struct {
	ExpectedRevision       string
	Target                 reviewtransaction.Target
	IntendedPaths          []string
	DeclaredObligationRefs []string
	EvidenceRefs           []string
	SDDRunRef              string
}

// ReplanVerificationAfterCorrection consumes the native single correction
// budget without starting a second workflow. Route, scope, and accepted SDD
// identity come from the live WorkRun; only the corrected repository
// observation and its typed implementation references enter from the caller.
func (coordinator *OwnerCoordinator) ReplanVerificationAfterCorrection(
	ctx context.Context,
	request OwnerCorrectionReplanRequest,
) (workrun.WorkRunState, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	current, err := coordinator.work.Status()
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	replayCandidate := current.VerificationReplan != nil
	if current.Revision != request.ExpectedRevision && !replayCandidate {
		return workrun.WorkRunState{}, ownerRevisionConflict(
			request.ExpectedRevision,
			current.Revision,
		)
	}
	if !replayCandidate &&
		(current.Handoff == nil ||
			current.Forecast == nil ||
			current.VerificationResultRef != "" ||
			current.PostVerificationSnapshotRef != "" ||
			current.VerificationStop != nil ||
			current.ReviewReceiptRef != "" ||
			current.DeliveryAuthorizationRef != "") {
		return workrun.WorkRunState{}, fmt.Errorf(
			"%w: verification is not eligible for correction replanning",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	completion, handoff, err := coordinator.observeImplementationCompletion(
		ctx,
		current,
		ownerImplementationCompletionRequest{
			Target:                 request.Target,
			IntendedPaths:          request.IntendedPaths,
			DeclaredObligationRefs: request.DeclaredObligationRefs,
			EvidenceRefs:           request.EvidenceRefs,
			SDDRunRef:              request.SDDRunRef,
		},
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	if replayCandidate {
		if current.Handoff == nil ||
			current.Handoff.Digest != handoff.Digest ||
			current.VerificationReplan.MutationCompletionRef !=
				completion.CompletionRef {
			if current.Revision != request.ExpectedRevision {
				return workrun.WorkRunState{}, ownerRevisionConflict(
					request.ExpectedRevision,
					current.Revision,
				)
			}
			return workrun.WorkRunState{}, fmt.Errorf(
				"%w: correction replan is already consumed",
				workrun.ErrWorkRunInvalidTransition,
			)
		}
		if err := coordinator.resolvePersistedCompletion(
			ctx,
			completion.CompletionRef,
		); err != nil {
			return workrun.WorkRunState{}, err
		}
	} else {
		completionRef, err := coordinator.mutations.Put(ctx, completion)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if completionRef != completion.CompletionRef ||
			completionRef != handoff.MutationCompletionRef {
			return workrun.WorkRunState{}, errors.New(
				"MMI owner persisted a mismatched corrected completion",
			)
		}
	}
	requestID, err := ownerRequestID("replan-correction", struct {
		ExpectedRevision string
		HandoffDigest    string
		CompletionRef    string
	}{
		request.ExpectedRevision,
		handoff.Digest,
		completion.CompletionRef,
	})
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	applied, err := coordinator.work.ReplanVerificationAfterCorrection(
		ctx,
		workrun.ReplanVerificationAfterCorrectionRequest{
			ExpectedRevision: request.ExpectedRevision,
			RequestID:        requestID,
			CorrectedHandoff: handoff,
		},
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	if replayCandidate {
		return current, nil
	}
	return applied, nil
}

type ownerImplementationCompletionRequest struct {
	Target                 reviewtransaction.Target
	IntendedPaths          []string
	DeclaredObligationRefs []string
	EvidenceRefs           []string
	SDDRunRef              string
}

func (coordinator *OwnerCoordinator) observeImplementationCompletion(
	ctx context.Context,
	state workrun.WorkRunState,
	request ownerImplementationCompletionRequest,
) (mutationintegrity.Completion, workrun.ImplementationHandoff, error) {
	if !state.Started ||
		state.WorkRunID != coordinator.work.WorkRunID ||
		state.DeliveryIntentRef == "" ||
		state.ImplementationRoute == "" {
		return mutationintegrity.Completion{},
			workrun.ImplementationHandoff{},
			workrun.ErrWorkRunRoutePending
	}
	switch state.ImplementationRoute {
	case workrun.ImplementationRouteDirectInline,
		workrun.ImplementationRouteDelegatedDirect:
		if state.SDDRunRef != "" || request.SDDRunRef != "" {
			return mutationintegrity.Completion{},
				workrun.ImplementationHandoff{},
				errors.New("direct implementation completion cannot bind an SDD run")
		}
	case workrun.ImplementationRouteSDD:
		if state.SDDRunRef == "" || request.SDDRunRef != state.SDDRunRef {
			return mutationintegrity.Completion{},
				workrun.ImplementationHandoff{},
				errors.New(
					"SDD implementation completion must bind the accepted SDD run",
				)
		}
	default:
		return mutationintegrity.Completion{},
			workrun.ImplementationHandoff{},
			fmt.Errorf(
				"unsupported implementation route %q",
				state.ImplementationRoute,
			)
	}
	scopeDigest, err := coordinator.resolveImplementationScope(
		ctx,
		state.DeliveryIntentRef,
	)
	if err != nil {
		return mutationintegrity.Completion{},
			workrun.ImplementationHandoff{},
			err
	}
	completion, err := mutationintegrity.Complete(
		ctx,
		coordinator.coordination.identity.lease,
		mutationintegrity.CompleteRequest{
			WorkRunID:   coordinator.work.WorkRunID,
			Route:       string(state.ImplementationRoute),
			ScopeDigest: scopeDigest,
			Target:      request.Target,
			IntendedPaths: append(
				[]string(nil),
				request.IntendedPaths...,
			),
		},
	)
	if err != nil {
		return mutationintegrity.Completion{},
			workrun.ImplementationHandoff{},
			err
	}
	subject, err := reviewtransaction.VerificationSubjectFromSnapshot(
		completion.Snapshot,
	)
	if err != nil {
		return mutationintegrity.Completion{},
			workrun.ImplementationHandoff{},
			err
	}
	handoff, err := workrun.NewImplementationHandoff(
		state.ImplementationRoute,
		completion.ScopeDigest,
		subject,
		request.DeclaredObligationRefs,
		request.EvidenceRefs,
		request.SDDRunRef,
		completion.CompletionRef,
	)
	if err != nil {
		return mutationintegrity.Completion{},
			workrun.ImplementationHandoff{},
			err
	}
	return completion, handoff, nil
}

func (coordinator *OwnerCoordinator) resolveImplementationScope(
	ctx context.Context,
	intentRef string,
) (scopeDigest string, err error) {
	opened, err := coordinator.pad.openRepository(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := opened.Close(); closeErr != nil {
			scopeDigest = ""
			err = errors.Join(
				err,
				fmt.Errorf("close PAD implementation scope repository: %w", closeErr),
			)
		}
	}()
	intent, err := opened.ResolveAdmittedIntent(ctx, intentRef)
	if err != nil {
		return "", fmt.Errorf(
			"resolve admitted implementation scope: %w",
			err,
		)
	}
	resolvedRef, err := intent.Ref()
	if err != nil {
		return "", err
	}
	if resolvedRef != intentRef ||
		intent.Destination.RepositoryRef !=
			coordinator.pad.authority.RepositoryRef() {
		return "", errors.New(
			"admitted implementation scope does not bind the owner repository",
		)
	}
	return intent.ScopeDigest, nil
}

func (coordinator *OwnerCoordinator) resolvePersistedCompletion(
	ctx context.Context,
	completionRef string,
) error {
	completion, err := coordinator.mutations.Read(ctx, completionRef)
	if err != nil {
		return err
	}
	if completion.CompletionRef != completionRef ||
		completion.RepositoryRef != coordinator.work.RepositoryRef() ||
		completion.WorkRunID != coordinator.work.WorkRunID {
		return errors.New("MMI owner returned a mismatched mutation completion")
	}
	return nil
}

func (coordinator *OwnerCoordinator) PublishVerificationPlan(
	ctx context.Context,
	publication reviewtransaction.RARPlanPublication,
) (reviewtransaction.RARPlanAuthority, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return reviewtransaction.RARPlanAuthority{}, err
	}
	return coordinator.rar.PublishPlan(ctx, publication)
}

type OwnerForecastRequest struct {
	ExpectedRevision string
	Handoff          workrun.ImplementationHandoff
	PlanRevisionRef  string
	Availability     workrun.ForecastAvailability
	DiagnosticRefs   []string
}

// PublishVerificationForecast lets RAR retain plan authority, coordination
// retain the availability observation, and WorkRun retain only their refs and
// ordering.
func (coordinator *OwnerCoordinator) PublishVerificationForecast(
	ctx context.Context,
	request OwnerForecastRequest,
) (workrun.WorkRunState, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	plan, err := coordinator.rar.ResolvePlan(ctx, request.PlanRevisionRef)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	publication := ForecastAuthorityPublication{
		WorkRunID:       coordinator.work.WorkRunID,
		Handoff:         request.Handoff,
		PlanRevisionRef: request.PlanRevisionRef,
		Availability:    request.Availability,
		DiagnosticRefs:  append([]string{}, request.DiagnosticRefs...),
	}
	candidate, err := newForecastAuthorityRecord(
		coordinator.coordination.identity.repositoryIdentity,
		publication,
		plan,
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	current, err := coordinator.work.Status()
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	exactReplay := current.Forecast != nil &&
		current.Forecast.AvailabilityRef == candidate.AuthorityRef
	if current.Revision != request.ExpectedRevision {
		if !exactReplay {
			return workrun.WorkRunState{}, ownerRevisionConflict(
				request.ExpectedRevision,
				current.Revision,
			)
		}
	} else if current.Handoff == nil ||
		current.Handoff.Digest != request.Handoff.Digest ||
		current.Forecast != nil {
		return workrun.WorkRunState{}, fmt.Errorf(
			"%w: WorkRun is not ready for this verification forecast",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	if exactReplay {
		authority, err := coordinator.coordination.ResolveForecast(
			ctx,
			candidate.AuthorityRef,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if err := authority.MatchesInput(workrun.VerificationForecastInput{
			WorkRunID:       coordinator.work.WorkRunID,
			Handoff:         request.Handoff,
			Applicability:   plan.Applicability,
			Registry:        plan.Registry,
			Plan:            plan.Plan,
			PlanRevisionRef: plan.AuthorityRef,
			Availability:    candidate.Availability,
			AvailabilityRef: candidate.AuthorityRef,
			DiagnosticRefs:  append([]string{}, candidate.DiagnosticRefs...),
		}); err != nil {
			return workrun.WorkRunState{}, err
		}
		return current, nil
	}
	if !exactReplay {
		published, err := coordinator.coordination.PublishForecast(
			ctx,
			publication,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if published.AuthorityRef != candidate.AuthorityRef {
			return workrun.WorkRunState{}, errors.New(
				"coordination owner published a mismatched verification forecast",
			)
		}
	}
	requestID, err := ownerRequestID("forecast", struct {
		ExpectedRevision string
		AuthorityRef     string
	}{request.ExpectedRevision, candidate.AuthorityRef})
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	return coordinator.work.RecordVerificationForecast(
		ctx,
		workrun.RecordVerificationForecastRequest{
			ExpectedRevision: request.ExpectedRevision,
			RequestID:        requestID,
			Input: workrun.VerificationForecastInput{
				WorkRunID:       coordinator.work.WorkRunID,
				Handoff:         request.Handoff,
				Applicability:   plan.Applicability,
				Registry:        plan.Registry,
				Plan:            plan.Plan,
				PlanRevisionRef: plan.AuthorityRef,
				Availability:    candidate.Availability,
				AvailabilityRef: candidate.AuthorityRef,
				DiagnosticRefs:  append([]string{}, candidate.DiagnosticRefs...),
			},
		},
	)
}

type OwnerDispositionRequest struct {
	ExpectedRevision string
	Handoff          workrun.ImplementationHandoff
	Forecast         workrun.VerificationForecast
	Kind             workrun.VerificationDispositionKind
	AssumptionsRef   string
	ActorRef         string
	RunnerRef        string
}

func (coordinator *OwnerCoordinator) PublishVerificationDisposition(
	ctx context.Context,
	request OwnerDispositionRequest,
) (workrun.WorkRunState, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	publication := DispositionAuthorityPublication{
		WorkRunID: coordinator.work.WorkRunID, Handoff: request.Handoff,
		Forecast: request.Forecast, Kind: request.Kind,
		AssumptionsRef: request.AssumptionsRef, ActorRef: request.ActorRef,
		RunnerRef: request.RunnerRef,
	}
	forecastRecord, forecastAuthority, err :=
		coordinator.coordination.resolveForecastRecord(
			ctx,
			request.Forecast.AvailabilityRef,
		)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	candidate, err := newDispositionAuthorityRecord(
		coordinator.coordination.identity.repositoryIdentity,
		publication,
		forecastRecord,
		forecastAuthority,
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	current, err := coordinator.work.Status()
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	exactReplay := current.Disposition != nil &&
		current.Disposition.DecisionRef == candidate.AuthorityRef
	if current.Revision != request.ExpectedRevision {
		if !exactReplay {
			return workrun.WorkRunState{}, ownerRevisionConflict(
				request.ExpectedRevision,
				current.Revision,
			)
		}
	} else if current.Handoff == nil ||
		current.Handoff.Digest != request.Handoff.Digest ||
		current.Forecast == nil ||
		current.Forecast.Digest != request.Forecast.Digest ||
		current.Disposition != nil {
		return workrun.WorkRunState{}, fmt.Errorf(
			"%w: WorkRun is not ready for this verification disposition",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	if exactReplay {
		authority, err := coordinator.coordination.ResolveDisposition(
			ctx,
			candidate.AuthorityRef,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if err := authority.Validate(request.Forecast); err != nil {
			return workrun.WorkRunState{}, err
		}
		return current, nil
	}
	if !exactReplay {
		published, err := coordinator.coordination.PublishDisposition(
			ctx,
			publication,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if published.AuthorityRef != candidate.AuthorityRef {
			return workrun.WorkRunState{}, errors.New(
				"coordination owner published a mismatched verification disposition",
			)
		}
	}
	requestID, err := ownerRequestID("disposition", struct {
		ExpectedRevision string
		AuthorityRef     string
	}{request.ExpectedRevision, candidate.AuthorityRef})
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	return coordinator.work.RecordVerificationDisposition(
		ctx,
		workrun.RecordVerificationDispositionRequest{
			ExpectedRevision: request.ExpectedRevision,
			RequestID:        requestID,
			Kind:             candidate.Kind,
			AssumptionsRef:   candidate.AssumptionsRef,
			ActorRef:         candidate.ActorRef,
			DecisionRef:      candidate.AuthorityRef,
			RunnerRef:        candidate.RunnerRef,
		},
	)
}

type OwnerVerificationActionRequest struct {
	ExpectedRevision string
	Ticket           evidence.SemanticActionTicketRequest
}

// IssueVerificationActionTicket observes the exact command/toolchain against
// the live RAR obligation, then lets EPD derive and persist the repository
// issuer, semantic requirement, and signing identity. Callers never provide a
// signer or an already-sealed ticket.
func (coordinator *OwnerCoordinator) IssueVerificationActionTicket(
	ctx context.Context,
	request OwnerVerificationActionRequest,
) (evidence.ActionTicket, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return evidence.ActionTicket{}, err
	}
	state, err := coordinator.work.Status()
	if err != nil {
		return evidence.ActionTicket{}, err
	}
	if state.Revision != request.ExpectedRevision {
		return evidence.ActionTicket{}, ownerRevisionConflict(
			request.ExpectedRevision,
			state.Revision,
		)
	}
	if state.Handoff == nil || state.Forecast == nil ||
		state.Disposition == nil ||
		state.Disposition.Kind != workrun.DispositionRun ||
		state.VerificationResultRef != "" ||
		(state.ImplementationRoute == workrun.ImplementationRouteSDD &&
			state.SDDRunRef == "") ||
		request.Ticket.CandidateRef != state.Handoff.CandidateRef ||
		request.Ticket.SubjectRef != state.Forecast.SubjectRef ||
		request.Ticket.VerificationContextRef != state.Forecast.Digest ||
		request.Ticket.ExpectedRevision != state.Forecast.PlanRevisionRef {
		return evidence.ActionTicket{}, errors.New(
			"semantic action request does not bind the current WorkRun verification authority",
		)
	}
	plan, err := coordinator.rar.ResolvePlan(
		ctx,
		request.Ticket.ExpectedRevision,
	)
	if err != nil {
		return evidence.ActionTicket{}, err
	}
	observation, err := evidence.ObserveSemanticRequirement(
		evidence.SemanticRequirementRequest{
			Program:     request.Ticket.Program,
			Args:        request.Ticket.Args,
			CWD:         request.Ticket.CWD,
			Environment: request.Ticket.Environment,
			Capability:  request.Ticket.Capability,
		},
	)
	if err != nil {
		return evidence.ActionTicket{}, err
	}
	if plan.Subject.SnapshotIdentity != request.Ticket.CandidateRef ||
		plan.Applicability.Digest != state.Forecast.ApplicabilityDigest ||
		plan.Registry.Digest != state.Forecast.RegistryDigest ||
		plan.Plan.Digest != state.Forecast.PlanDigest ||
		!planContainsSemanticRequest(
			plan.Plan,
			request.Ticket,
			observation,
		) {
		return evidence.ActionTicket{}, errors.New(
			"semantic action request does not bind the exact live RAR plan",
		)
	}
	ticket, err := coordinator.evidence.IssueSemanticActionTicket(
		ctx,
		request.Ticket,
	)
	if err != nil {
		return evidence.ActionTicket{}, err
	}
	if ticket.IssuerRef != coordinator.evidence.RepositoryRef() ||
		!planContainsTicket(plan.Plan, ticket) {
		return evidence.ActionTicket{}, errors.New(
			"EPD semantic action ticket does not bind its repository owner and RAR plan",
		)
	}
	live, err := coordinator.work.Status()
	if err != nil {
		return evidence.ActionTicket{}, err
	}
	if live.Revision != request.ExpectedRevision {
		return evidence.ActionTicket{}, ownerRevisionConflict(
			request.ExpectedRevision,
			live.Revision,
		)
	}
	return ticket, nil
}

type OwnerVerificationTransitionRequest struct {
	ExpectedRevision string
	ActionTicketRef  string
	IssuedAt         int64
	ExpiresAt        int64
}

type OwnerVerificationTransition struct {
	TicketRef     string
	Authorization VerificationTransitionAuthorization
}

// IssueVerificationTransition resolves one owner-issued EPD ticket by ref and
// publishes one opaque apply authorization. It never accepts a caller-sealed
// ticket, signer, argv, or semantic requirement.
func (coordinator *OwnerCoordinator) IssueVerificationTransition(
	ctx context.Context,
	request OwnerVerificationTransitionRequest,
) (OwnerVerificationTransition, error) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return OwnerVerificationTransition{}, err
	}
	ticket, err := coordinator.evidence.ReadTicket(request.ActionTicketRef)
	if err != nil {
		return OwnerVerificationTransition{}, err
	}
	if ticket.TicketRef != request.ActionTicketRef ||
		ticket.IssuerRef != coordinator.evidence.RepositoryRef() {
		return OwnerVerificationTransition{}, errors.New(
			"EPD action ticket does not belong to the repository owner",
		)
	}
	state, err := coordinator.work.Status()
	if err != nil {
		return OwnerVerificationTransition{}, err
	}
	replay, reservationConflict := ownerTicketReservationState(
		state,
		request.ExpectedRevision,
		ticket.TicketRef,
		ticket.Slot,
	)
	if reservationConflict {
		return OwnerVerificationTransition{}, errors.New(
			"EPD action ticket reuses an already-reserved WorkRun verification slot",
		)
	}
	if state.Revision != request.ExpectedRevision && !replay {
		return OwnerVerificationTransition{}, &workrun.RevisionConflictError{
			Expected: request.ExpectedRevision,
			Current:  state.Revision,
		}
	}
	if state.Handoff == nil || state.Forecast == nil ||
		state.Disposition == nil ||
		state.Disposition.Kind != workrun.DispositionRun ||
		(state.VerificationResultRef != "" && !replay) ||
		(state.ImplementationRoute == workrun.ImplementationRouteSDD &&
			state.SDDRunRef == "") ||
		ticket.CandidateRef != state.Handoff.CandidateRef ||
		ticket.SubjectRef != state.Forecast.SubjectRef ||
		ticket.VerificationContextRef != state.Forecast.Digest ||
		ticket.ExpectedRevision != state.Forecast.PlanRevisionRef {
		return OwnerVerificationTransition{}, errors.New(
			"EPD action ticket does not bind the current WorkRun verification authority",
		)
	}
	plan, err := coordinator.rar.ResolvePlan(
		ctx,
		ticket.ExpectedRevision,
	)
	if err != nil {
		return OwnerVerificationTransition{}, err
	}
	if plan.Subject.SnapshotIdentity != ticket.CandidateRef ||
		plan.Applicability.Digest != state.Forecast.ApplicabilityDigest ||
		plan.Registry.Digest != state.Forecast.RegistryDigest ||
		plan.Plan.Digest != state.Forecast.PlanDigest ||
		!planContainsTicket(plan.Plan, ticket) {
		return OwnerVerificationTransition{}, errors.New(
			"EPD action ticket does not bind the exact live RAR plan",
		)
	}
	authorization, err := NewVerificationTransitionAuthorization(
		VerificationTransitionAuthorizationRequest{
			WorkRunID:                  coordinator.work.WorkRunID,
			ExpectedRevision:           request.ExpectedRevision,
			CandidateRef:               ticket.CandidateRef,
			ActionTicketRef:            ticket.TicketRef,
			ApplicableAuthorizationRef: plan.AuthorityRef,
			Class:                      AuthorizationGeneral, IssuedAt: request.IssuedAt,
			ExpiresAt: request.ExpiresAt,
		},
	)
	if err != nil {
		return OwnerVerificationTransition{}, err
	}
	existing, _, resolveErr := coordinator.transitions.ResolveAuthorization(
		ctx,
		authorization.AuthorizationRef,
		request.IssuedAt,
	)
	if resolveErr == nil {
		if existing != authorization {
			return OwnerVerificationTransition{}, errors.New(
				"transition owner returned mismatched replay authority",
			)
		}
		return OwnerVerificationTransition{
			TicketRef:     ticket.TicketRef,
			Authorization: existing,
		}, nil
	}
	if !errors.Is(resolveErr, ErrAuthorizationNotFound) {
		return OwnerVerificationTransition{}, resolveErr
	}
	if err := coordinator.transitions.PublishAuthorization(
		ctx,
		authorization,
	); err != nil {
		return OwnerVerificationTransition{}, err
	}
	live, err := coordinator.work.Status()
	if err != nil {
		return OwnerVerificationTransition{}, err
	}
	liveReplay, _ := ownerTicketReservationState(
		live,
		request.ExpectedRevision,
		ticket.TicketRef,
		ticket.Slot,
	)
	if live.Revision != request.ExpectedRevision && !liveReplay {
		return OwnerVerificationTransition{}, ownerRevisionConflict(
			request.ExpectedRevision,
			live.Revision,
		)
	}
	return OwnerVerificationTransition{
		TicketRef: ticket.TicketRef, Authorization: authorization,
	}, nil
}

type OwnerBindResultRequest struct {
	ExpectedRevision string
	ResultRef        string
}

func (coordinator *OwnerCoordinator) BindVerificationResult(
	ctx context.Context,
	request OwnerBindResultRequest,
) (workrun.WorkRunState, error) {
	if err := coordinator.guardTerminalMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	authority, err := coordinator.rar.ResolveResult(ctx, request.ResultRef)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	if err := authority.Validate(); err != nil {
		return workrun.WorkRunState{}, err
	}
	current, statusErr := coordinator.work.Status()
	if statusErr == nil &&
		current.VerificationResultRef == request.ResultRef {
		if current.Forecast == nil {
			return workrun.WorkRunState{}, fmt.Errorf(
				"%w: verification result replay has no forecast",
				workrun.ErrWorkRunInvalidTransition,
			)
		}
		if err := current.Forecast.MatchesPlan(
			authority.Applicability,
			authority.Registry,
			authority.Plan,
		); err != nil {
			return workrun.WorkRunState{}, err
		}
		return current, nil
	}
	if statusErr != nil &&
		!errors.Is(statusErr, workrun.ErrWorkRunNotStarted) {
		return workrun.WorkRunState{}, statusErr
	}
	requestID, err := ownerRequestID("bind-result", struct {
		ExpectedRevision string
		ResultRef        string
	}{request.ExpectedRevision, request.ResultRef})
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	return coordinator.work.BindVerificationResult(
		ctx,
		workrun.BindVerificationResultRequest{
			ExpectedRevision: request.ExpectedRevision,
			RequestID:        requestID,
			Applicability:    authority.Applicability,
			Registry:         authority.Registry,
			Plan:             authority.Plan,
			Result:           authority.Result,
		},
	)
}

type OwnerBindReviewRequest struct {
	ExpectedRevision string
	ReviewReceiptRef string
}

func (coordinator *OwnerCoordinator) BindReviewReceipt(
	ctx context.Context,
	request OwnerBindReviewRequest,
) (workrun.WorkRunState, error) {
	if err := coordinator.guardTerminalMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	current, statusErr := coordinator.work.Status()
	if statusErr == nil &&
		current.ReviewReceiptRef == request.ReviewReceiptRef {
		if current.Handoff == nil ||
			current.VerificationResultRef == "" {
			return workrun.WorkRunState{}, fmt.Errorf(
				"%w: review replay has no terminal result",
				workrun.ErrWorkRunInvalidTransition,
			)
		}
		authority, err := coordinator.rar.ResolveReceiptResult(
			ctx,
			request.ReviewReceiptRef,
			current.VerificationResultRef,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if err := authority.Validate(); err != nil {
			return workrun.WorkRunState{}, err
		}
		if authority.Receipt.ReceiptRef != request.ReviewReceiptRef ||
			authority.Result.ResultRef != current.VerificationResultRef ||
			authority.Result.Subject.SnapshotIdentity !=
				current.Handoff.CandidateRef {
			return workrun.WorkRunState{}, errors.New(
				"RAR review replay authority does not bind the current WorkRun",
			)
		}
		return current, nil
	}
	if statusErr != nil &&
		!errors.Is(statusErr, workrun.ErrWorkRunNotStarted) {
		return workrun.WorkRunState{}, statusErr
	}
	requestID, err := ownerRequestID("bind-review", struct {
		ExpectedRevision string
		ReviewReceiptRef string
	}{request.ExpectedRevision, request.ReviewReceiptRef})
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	return coordinator.work.BindReviewReceipt(
		ctx,
		workrun.BindReviewReceiptRequest{
			ExpectedRevision: request.ExpectedRevision,
			RequestID:        requestID,
			ReviewReceiptRef: request.ReviewReceiptRef,
		},
	)
}

type OwnerBindDeliveryAuthorizationRequest struct {
	ExpectedRevision string
	AuthorizationRef string
}

func (coordinator *OwnerCoordinator) BindDeliveryAuthorization(
	ctx context.Context,
	request OwnerBindDeliveryAuthorizationRequest,
) (workrun.WorkRunState, error) {
	if err := coordinator.guardTerminalMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	current, statusErr := coordinator.work.Status()
	if statusErr == nil &&
		current.DeliveryAuthorizationRef == request.AuthorizationRef {
		authority, err := coordinator.pad.ResolveLiveDeliveryAuthorization(
			ctx,
			request.AuthorizationRef,
		)
		if err != nil {
			return workrun.WorkRunState{}, err
		}
		if err := authority.Validate(request.AuthorizationRef, current); err != nil {
			return workrun.WorkRunState{}, err
		}
		return current, nil
	}
	if statusErr != nil &&
		!errors.Is(statusErr, workrun.ErrWorkRunNotStarted) {
		return workrun.WorkRunState{}, statusErr
	}
	requestID, err := ownerRequestID("bind-delivery", struct {
		ExpectedRevision string
		AuthorizationRef string
	}{request.ExpectedRevision, request.AuthorizationRef})
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	return coordinator.work.BindDeliveryAuthorization(
		ctx,
		workrun.BindDeliveryAuthorizationRequest{
			ExpectedRevision: request.ExpectedRevision,
			RequestID:        requestID,
			AuthorizationRef: request.AuthorizationRef,
		},
	)
}

// Status is deliberately read-only. It does not issue PAD, route, forecast,
// disposition, ticket, or transition authority while being polled.
func (coordinator *OwnerCoordinator) Status(
	ctx context.Context,
) (workrun.WorkRunState, error) {
	if err := coordinator.validate(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	return coordinator.work.Status()
}

func (coordinator *OwnerCoordinator) guardGeneralMutation(
	ctx context.Context,
) error {
	return coordinator.guardMutation(ctx, false)
}

func (coordinator *OwnerCoordinator) guardTerminalMutation(
	ctx context.Context,
) error {
	return coordinator.guardMutation(ctx, true)
}

func (coordinator *OwnerCoordinator) guardMutation(
	ctx context.Context,
	terminal bool,
) error {
	if err := coordinator.validate(ctx); err != nil {
		return err
	}
	mode, err := coordinator.activation.ResolveActivation(
		ctx,
		coordinator.repository,
	)
	if err != nil {
		return err
	}
	if err := mode.Validate(); err != nil {
		return err
	}
	return activationMutationError(mode, terminal)
}

func activationMutationError(mode ActivationMode, terminal bool) error {
	switch mode {
	case ActivationEnabled:
		return nil
	case ActivationRecoveryOnly:
		if terminal {
			return nil
		}
		return ErrRecoveryOnly
	case ActivationReadOnly:
		return ErrCapabilityReadOnly
	case ActivationDisabled:
		return ErrCapabilityDisabled
	default:
		return &InvalidActivationModeError{Value: string(mode)}
	}
}

func (coordinator *OwnerCoordinator) validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if coordinator == nil ||
		coordinator.pad == nil ||
		coordinator.pad.authority == nil ||
		coordinator.sdd.Repo == "" ||
		coordinator.coordination == nil ||
		coordinator.rar == nil ||
		coordinator.transitions == nil ||
		coordinator.work.WorkRunID == "" ||
		coordinator.evidence.Root() == "" ||
		coordinator.mutations.Root() == "" ||
		coordinator.activation == nil ||
		coordinator.repository == "" {
		return errors.New("owner coordinator is not initialized")
	}
	return nil
}

func ownerTicketReservationState(
	state workrun.WorkRunState,
	expectedRevision string,
	ticketRef string,
	slot string,
) (exact bool, conflict bool) {
	for _, reservation := range state.Reservations {
		if reservation.ExpectedWorkRevision == expectedRevision &&
			reservation.ActionTicketRef == ticketRef {
			exact = true
			continue
		}
		if reservation.ActionTicketRef == ticketRef ||
			reservation.Slot == slot {
			conflict = true
		}
	}
	return exact, conflict
}

func ownerPendingSDDProposal(
	state workrun.WorkRunState,
	pendingDecisionDigest string,
) bool {
	return state.Started &&
		state.RouteDecision.Decision == workrun.RouteDecisionProposeSDD &&
		state.RouteDecision.Digest == pendingDecisionDigest &&
		state.ImplementationRoute == "" &&
		state.RouteAcceptanceRef == "" &&
		state.Handoff == nil
}

func ownerRevisionConflict(expected, current string) error {
	return &workrun.RevisionConflictError{
		Expected: expected,
		Current:  current,
	}
}

func ownerRequestID(operation string, value any) (string, error) {
	if operation == "" || strings.ContainsAny(operation, "_/ \t\r\n") {
		return "", errors.New("owner operation identifier is invalid")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("gentle-ai.owner-coordinator-request/v1"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(operation))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	digest := hex.EncodeToString(hash.Sum(nil))
	return "owner-" + operation + "-" + digest[:32], nil
}
