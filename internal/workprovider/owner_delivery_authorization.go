package workprovider

import (
	"context"
	"errors"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

// OwnerIssueDeliveryAuthorizationRequest identifies the exact PAD decision
// that should become the WorkRun's one delivery authorization. The caller
// cannot choose the authorization kind, issuer, nonce, candidate, result, or
// review receipt. For an exception decision it may provide only the immutable
// owner-issued human decision reference required by PAD.
type OwnerIssueDeliveryAuthorizationRequest struct {
	ExpectedRevision string
	DecisionRef      string
	HumanDecisionRef string
}

type ownerPADAuthorizationRepository interface {
	padTrustedRepository
	deliveryadmission.TrustedResolver
	PublishAuthorization(
		context.Context,
		deliveryadmission.DeliveryAuthorization,
	) (string, error)
	PublishExceptionAuthorization(
		context.Context,
		deliveryadmission.DeliveryExceptionAuthorization,
	) (string, error)
}

// IssueAndBindDeliveryAuthorization composes PAD's existing issue/publication
// use cases with WorkRun's existing ref-only binding. PAD derives the kind from
// the independently validated delivery decision; this adapter derives the
// nonce and issuer from owner facts and never accepts them from its caller.
func (coordinator *OwnerCoordinator) IssueAndBindDeliveryAuthorization(
	ctx context.Context,
	request OwnerIssueDeliveryAuthorizationRequest,
) (
	state workrun.WorkRunState,
	err error,
) {
	if err := coordinator.guardTerminalMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	preflight, err := coordinator.work.Status()
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	if preflight.DeliveryAuthorizationRef == "" &&
		preflight.Revision != request.ExpectedRevision {
		return workrun.WorkRunState{}, ownerRevisionConflict(
			request.ExpectedRevision,
			preflight.Revision,
		)
	}
	lock, err := acquirePADDeliveryOwnerLock(ctx, coordinator.work.Dir)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			state = workrun.WorkRunState{}
			err = errors.Join(
				err,
				fmt.Errorf(
					"release PAD delivery authorization owner lock: %w",
					releaseErr,
				),
			)
		}
	}()
	if err := coordinator.guardTerminalMutation(ctx); err != nil {
		return workrun.WorkRunState{}, err
	}
	current, err := coordinator.work.Status()
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	if current.DeliveryAuthorizationRef == "" &&
		current.Revision != request.ExpectedRevision {
		return workrun.WorkRunState{}, ownerRevisionConflict(
			request.ExpectedRevision,
			current.Revision,
		)
	}
	if err := validateOwnerDeliveryDecisionState(current); err != nil {
		return workrun.WorkRunState{}, err
	}

	opened, err := coordinator.pad.openRepository(ctx)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	repository, ok := opened.(ownerPADAuthorizationRepository)
	if !ok {
		_ = opened.Close()
		return workrun.WorkRunState{}, errors.New(
			"PAD owner repository does not support delivery authorization issuance",
		)
	}
	defer func() {
		if closeErr := repository.Close(); closeErr != nil {
			state = workrun.WorkRunState{}
			err = errors.Join(
				err,
				fmt.Errorf(
					"close PAD delivery authorization repository: %w",
					closeErr,
				),
			)
		}
	}()

	decision, err := deliveryadmission.ValidateDeliveryDecision(
		ctx,
		repository,
		coordinator.rar,
		request.DecisionRef,
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	if err := matchOwnerDeliveryDecision(current, decision); err != nil {
		return workrun.WorkRunState{}, err
	}
	if err := matchOwnerDeliveryRouteDecision(
		ctx,
		repository,
		current,
		request.DecisionRef,
	); err != nil {
		return workrun.WorkRunState{}, err
	}
	if _, err := coordinator.preparePADCandidateBinding(
		ctx,
		decision,
	); err != nil {
		return workrun.WorkRunState{}, err
	}

	if current.DeliveryAuthorizationRef != "" {
		if err := validateOwnerAuthorizationReplay(
			ctx,
			repository,
			coordinator.rar,
			current,
			decision,
			request,
		); err != nil {
			return workrun.WorkRunState{}, err
		}
		return coordinator.BindDeliveryAuthorization(
			ctx,
			OwnerBindDeliveryAuthorizationRequest{
				ExpectedRevision: request.ExpectedRevision,
				AuthorizationRef: current.DeliveryAuthorizationRef,
			},
		)
	}

	nonce, err := ownerDeliveryAuthorizationNonce(
		coordinator.work.WorkRunID,
		request.DecisionRef,
		current,
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	authorizationRef, err := prepareAndPublishOwnerPADAuthorization(
		ctx,
		coordinator,
		repository,
		current,
		decision,
		request,
		nonce,
	)
	if err != nil {
		return workrun.WorkRunState{}, err
	}
	return coordinator.BindDeliveryAuthorization(
		ctx,
		OwnerBindDeliveryAuthorizationRequest{
			ExpectedRevision: request.ExpectedRevision,
			AuthorizationRef: authorizationRef,
		},
	)
}

func validateOwnerDeliveryDecisionState(
	state workrun.WorkRunState,
) error {
	if !state.Started ||
		state.Handoff == nil ||
		state.DeliveryIntentRef == "" ||
		state.Handoff.CandidateRef == "" ||
		state.VerificationResultRef == "" ||
		state.PostVerificationSnapshotRef == "" ||
		state.PostVerificationSnapshotRef != state.Handoff.CandidateRef ||
		state.VerificationStop != nil ||
		state.ReviewReceiptRef == "" {
		return fmt.Errorf(
			"%w: terminal WorkRun facts are required before PAD authorization",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	return nil
}

func matchOwnerDeliveryRouteDecision(
	ctx context.Context,
	repository ownerPADAuthorizationRepository,
	state workrun.WorkRunState,
	decisionRef string,
) error {
	if state.DeliveryRouteReevaluationRef == "" {
		return nil
	}
	resolver, ok := any(repository).(deliveryadmission.RouteReevaluationResolver)
	if !ok {
		return errors.New(
			"PAD owner repository cannot resolve bound route reevaluation",
		)
	}
	reevaluation, err := resolver.ResolveRouteReevaluation(
		ctx,
		state.DeliveryRouteReevaluationRef,
	)
	if err != nil {
		return err
	}
	resolvedRef, err := reevaluation.Ref()
	if err != nil ||
		resolvedRef != state.DeliveryRouteReevaluationRef ||
		reevaluation.DecisionRef != decisionRef {
		return fmt.Errorf(
			"%w: authorization decision is not the bound route target decision",
			deliveryadmission.ErrBindingMismatch,
		)
	}
	return nil
}

func matchOwnerDeliveryDecision(
	state workrun.WorkRunState,
	decision deliveryadmission.DeliveryDecision,
) error {
	if state.Handoff == nil ||
		decision.AdmissionDecision.IntentRef != state.DeliveryIntentRef ||
		decision.Candidate.Digest != state.Handoff.CandidateRef ||
		decision.VerificationResultRef != state.VerificationResultRef ||
		decision.ReviewReceiptRef != state.ReviewReceiptRef {
		return fmt.Errorf(
			"%w: PAD decision does not bind the exact WorkRun terminal facts",
			deliveryadmission.ErrBindingMismatch,
		)
	}
	return nil
}

func ownerDeliveryAuthorizationNonce(
	workRunID string,
	decisionRef string,
	state workrun.WorkRunState,
) (string, error) {
	return ownerRequestID("authorize-delivery", struct {
		WorkRunID             string
		DecisionRef           string
		DeliveryIntentRef     string
		CandidateRef          string
		VerificationResultRef string
		ReviewReceiptRef      string
	}{
		WorkRunID:             workRunID,
		DecisionRef:           decisionRef,
		DeliveryIntentRef:     state.DeliveryIntentRef,
		CandidateRef:          state.Handoff.CandidateRef,
		VerificationResultRef: state.VerificationResultRef,
		ReviewReceiptRef:      state.ReviewReceiptRef,
	})
}

func validateOwnerAuthorizationReplay(
	ctx context.Context,
	repository ownerPADAuthorizationRepository,
	rar deliveryadmission.RARAuthorityPort,
	state workrun.WorkRunState,
	decision deliveryadmission.DeliveryDecision,
	request OwnerIssueDeliveryAuthorizationRequest,
) error {
	var binding deliveryadmission.AuthorizationBinding
	switch decision.Disposition {
	case deliveryadmission.DeliveryAuthorizeNormal:
		if request.HumanDecisionRef != "" {
			return fmt.Errorf(
				"%w: bound normal delivery differs from the requested governance",
				deliveryadmission.ErrTrustedRepositoryConflict,
			)
		}
		authorization, err := deliveryadmission.ValidateAuthorization(
			ctx,
			repository,
			rar,
			state.DeliveryAuthorizationRef,
		)
		if err != nil {
			return err
		}
		binding = authorization.Binding
	case deliveryadmission.DeliveryRequireException:
		authorization, err :=
			deliveryadmission.ValidateExceptionAuthorization(
				ctx,
				repository,
				rar,
				state.DeliveryAuthorizationRef,
			)
		if err != nil {
			return err
		}
		if authorization.HumanDecisionRef != request.HumanDecisionRef {
			return fmt.Errorf(
				"%w: bound exception uses different human governance",
				deliveryadmission.ErrTrustedRepositoryConflict,
			)
		}
		binding = authorization.Binding
	default:
		return fmt.Errorf(
			"%w: a blocked decision cannot replay a delivery authorization",
			deliveryadmission.ErrTrustedRepositoryConflict,
		)
	}
	if binding.DecisionRef != request.DecisionRef ||
		binding.IntentRef != state.DeliveryIntentRef ||
		state.Handoff == nil ||
		binding.Candidate.Digest != state.Handoff.CandidateRef ||
		binding.VerificationResultRef != state.VerificationResultRef ||
		binding.ReviewReceiptRef != state.ReviewReceiptRef ||
		binding.AuthorityRef != decision.AuthorityRef {
		return fmt.Errorf(
			"%w: bound authorization differs from the exact owner request",
			deliveryadmission.ErrTrustedRepositoryConflict,
		)
	}
	return nil
}
