package workprovider

import (
	"context"
	"errors"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

var ErrPADRouteReevaluationUnavailable = errors.New(
	"PAD route reevaluation live authority is unavailable",
)

// PADRouteReevaluationProbe is injected only by provider composition. The
// user-facing operation accepts no probe, gate facts, candidate, policy, or
// content authority.
type PADRouteReevaluationProbe interface {
	deliveryadmission.LiveGateProbePort
	RepositoryRef() string
}

type OwnerDeliveryRouteReevaluationRequest struct {
	ExpectedRevision           string
	SourceDecisionRef          string
	TargetAdmissionDecisionRef string
	Mechanism                  deliveryadmission.Mechanism
}

type OwnerDeliveryRouteReevaluation struct {
	ReevaluationRef string
	Reevaluation    deliveryadmission.RouteReevaluation
	State           workrun.WorkRunState
}

type ownerPADRouteReevaluationRepository interface {
	padTrustedRepository
	deliveryadmission.RouteReevaluationRepository
	deliveryadmission.RouteReevaluationResolver
	deliveryadmission.RARAuthorityPort
}

// ReevaluateDeliveryRoute changes only PAD's current delivery intent. It
// reuses the frozen implementation candidate, MMI completion, verification
// result, and review receipt, then binds PAD's persisted route lineage into
// WorkRun before a target-route authorization can be accepted.
func (coordinator *OwnerCoordinator) ReevaluateDeliveryRoute(
	ctx context.Context,
	request OwnerDeliveryRouteReevaluationRequest,
) (
	result OwnerDeliveryRouteReevaluation,
	err error,
) {
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return OwnerDeliveryRouteReevaluation{}, err
	}
	if coordinator.padRoute == nil ||
		coordinator.padRoute.RepositoryRef() !=
			coordinator.pad.authority.RepositoryRef() {
		return OwnerDeliveryRouteReevaluation{},
			ErrPADRouteReevaluationUnavailable
	}
	preflight, err := coordinator.work.Status()
	if err != nil {
		return OwnerDeliveryRouteReevaluation{}, err
	}
	if preflight.DeliveryRouteReevaluationRef == "" &&
		preflight.Revision != request.ExpectedRevision {
		return OwnerDeliveryRouteReevaluation{}, ownerRevisionConflict(
			request.ExpectedRevision,
			preflight.Revision,
		)
	}
	lock, err := acquirePADDeliveryOwnerLock(ctx, coordinator.work.Dir)
	if err != nil {
		return OwnerDeliveryRouteReevaluation{}, err
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			result = OwnerDeliveryRouteReevaluation{}
			err = errors.Join(
				err,
				fmt.Errorf(
					"release PAD route reevaluation owner lock: %w",
					releaseErr,
				),
			)
		}
	}()
	if err := coordinator.guardGeneralMutation(ctx); err != nil {
		return OwnerDeliveryRouteReevaluation{}, err
	}
	current, err := coordinator.work.Status()
	if err != nil {
		return OwnerDeliveryRouteReevaluation{}, err
	}
	if current.DeliveryRouteReevaluationRef == "" &&
		current.Revision != request.ExpectedRevision {
		return OwnerDeliveryRouteReevaluation{}, ownerRevisionConflict(
			request.ExpectedRevision,
			current.Revision,
		)
	}
	requestID, err := ownerRequestID(
		"reevaluate-delivery-route",
		request,
	)
	if err != nil {
		return OwnerDeliveryRouteReevaluation{}, err
	}
	if current.DeliveryRouteReevaluationRef == "" {
		if err := rejectPreparedPADAuthorizationForRoute(
			ctx,
			coordinator,
		); err != nil {
			return OwnerDeliveryRouteReevaluation{}, err
		}
	}
	opened, err := coordinator.pad.openRepository(ctx)
	if err != nil {
		return OwnerDeliveryRouteReevaluation{}, err
	}
	repository, ok := opened.(ownerPADRouteReevaluationRepository)
	if !ok {
		_ = opened.Close()
		return OwnerDeliveryRouteReevaluation{}, errors.New(
			"PAD owner repository does not support route reevaluation",
		)
	}
	defer func() {
		if closeErr := repository.Close(); closeErr != nil {
			result = OwnerDeliveryRouteReevaluation{}
			err = errors.Join(
				err,
				fmt.Errorf("close PAD owner repository: %w", closeErr),
			)
		}
	}()

	if current.DeliveryRouteReevaluationRef != "" {
		reevaluation, _, _, resolveErr :=
			resolveValidatedPADRouteReevaluation(
				ctx,
				repository,
				current.DeliveryRouteReevaluationRef,
			)
		if resolveErr != nil {
			return OwnerDeliveryRouteReevaluation{}, resolveErr
		}
		if reevaluation.SourceDecisionRef != request.SourceDecisionRef ||
			reevaluation.TargetAdmissionDecisionRef !=
				request.TargetAdmissionDecisionRef ||
			reevaluation.Mechanism != request.Mechanism {
			return OwnerDeliveryRouteReevaluation{}, fmt.Errorf(
				"%w: WorkRun already binds a different PAD route reevaluation",
				workrun.ErrWorkRunInvalidTransition,
			)
		}
		state, bindErr := coordinator.work.BindDeliveryRouteReevaluation(
			ctx,
			workrun.BindDeliveryRouteReevaluationRequest{
				ExpectedRevision: request.ExpectedRevision,
				RequestID:        requestID,
				ReevaluationRef:  current.DeliveryRouteReevaluationRef,
			},
		)
		if bindErr != nil {
			return OwnerDeliveryRouteReevaluation{}, bindErr
		}
		return OwnerDeliveryRouteReevaluation{
			ReevaluationRef: current.DeliveryRouteReevaluationRef,
			Reevaluation:    reevaluation,
			State:           state,
		}, nil
	}
	if err := coordinator.validatePADRouteReevaluationSource(
		ctx,
		repository,
		current,
		request.SourceDecisionRef,
	); err != nil {
		return OwnerDeliveryRouteReevaluation{}, err
	}
	reevaluation, err := deliveryadmission.ReevaluateDeliveryRoute(
		ctx,
		repository,
		coordinator.rar,
		coordinator.padRoute,
		deliveryadmission.RouteReevaluationRequest{
			SourceDecisionRef:          request.SourceDecisionRef,
			TargetAdmissionDecisionRef: request.TargetAdmissionDecisionRef,
			Mechanism:                  request.Mechanism,
		},
	)
	if err != nil {
		return OwnerDeliveryRouteReevaluation{}, err
	}
	reevaluationRef, err := reevaluation.Ref()
	if err != nil {
		return OwnerDeliveryRouteReevaluation{}, err
	}
	state, err := coordinator.work.BindDeliveryRouteReevaluation(
		ctx,
		workrun.BindDeliveryRouteReevaluationRequest{
			ExpectedRevision: request.ExpectedRevision,
			RequestID:        requestID,
			ReevaluationRef:  reevaluationRef,
		},
	)
	if err != nil {
		return OwnerDeliveryRouteReevaluation{}, err
	}
	return OwnerDeliveryRouteReevaluation{
		ReevaluationRef: reevaluationRef,
		Reevaluation:    reevaluation,
		State:           state,
	}, nil
}

func (coordinator *OwnerCoordinator) validatePADRouteReevaluationSource(
	ctx context.Context,
	repository ownerPADRouteReevaluationRepository,
	state workrun.WorkRunState,
	sourceDecisionRef string,
) error {
	if state.Handoff == nil ||
		state.VerificationResultRef == "" ||
		state.PostVerificationSnapshotRef == "" ||
		state.ReviewReceiptRef == "" ||
		state.VerificationStop != nil ||
		state.DeliveryAuthorizationRef != "" {
		return fmt.Errorf(
			"%w: exact terminal content is required before PAD route reevaluation",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	completion, err := (MutationCompletionWorkRunAuthority{
		Store: coordinator.mutations,
	}).ResolveMutationCompletion(
		ctx,
		state.Handoff.MutationCompletionRef,
	)
	if err != nil {
		return err
	}
	if err := completion.Validate(
		state.Handoff.MutationCompletionRef,
		state.WorkRunID,
		*state.Handoff,
	); err != nil {
		return err
	}
	if completion.RepositoryRef != coordinator.work.RepositoryRef() {
		return fmt.Errorf(
			"%w: PAD route reevaluation MMI repository",
			workrun.ErrAuthorityBindingMismatch,
		)
	}
	source, err := deliveryadmission.ValidateDeliveryDecision(
		ctx,
		repository,
		coordinator.rar,
		sourceDecisionRef,
	)
	if err != nil {
		return err
	}
	if source.AdmissionDecision.IntentRef != state.DeliveryIntentRef ||
		source.AdmissionDecision.Intent.ScopeDigest !=
			state.Handoff.ScopeDigest ||
		source.Candidate.Digest != state.Handoff.CandidateRef ||
		source.Candidate.Digest != state.PostVerificationSnapshotRef ||
		source.VerificationResultRef != state.VerificationResultRef ||
		source.ReviewReceiptRef != state.ReviewReceiptRef {
		return fmt.Errorf(
			"%w: PAD source decision does not bind the current WorkRun",
			workrun.ErrAuthorityBindingMismatch,
		)
	}
	return nil
}
