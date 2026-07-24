package workprovider

import (
	"context"
	"errors"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type ownerPADDecisionRepository interface {
	ownerPADAuthorizationRepository
	ResolveAdmittedDecision(
		context.Context,
		string,
	) (deliveryadmission.AdmittedDecision, error)
	PublishGateEvidence(
		context.Context,
		deliveryadmission.GateEvidence,
	) (string, error)
	PublishDeliveryDecision(
		context.Context,
		deliveryadmission.DeliveryDecision,
	) (string, error)
}

// DeriveAndPublishDeliveryDecision probes live Git/hosting state through the
// composed PAD adapter and publishes the exact gate and decision. The caller
// supplies no route, mechanism, candidate, destination, policy, or authority.
func (coordinator *OwnerCoordinator) DeriveAndPublishDeliveryDecision(
	ctx context.Context,
) (
	decisionRef string,
	err error,
) {
	if err := coordinator.guardTerminalMutation(ctx); err != nil {
		return "", err
	}
	state, err := coordinator.work.Status()
	if err != nil {
		return "", err
	}
	if err := validateOwnerDeliveryDecisionState(state); err != nil {
		return "", err
	}
	if state.DeliveryAuthorizationRef != "" {
		return "", fmt.Errorf(
			"%w: delivery authorization is already bound",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	lock, err := acquirePADDeliveryOwnerLock(ctx, coordinator.work.Dir)
	if err != nil {
		return "", err
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			decisionRef = ""
			err = errors.Join(err, releaseErr)
		}
	}()
	if err := coordinator.guardTerminalMutation(ctx); err != nil {
		return "", err
	}
	live, err := coordinator.work.Status()
	if err != nil {
		return "", err
	}
	if live.Revision != state.Revision {
		return "", workrun.ErrWorkRunConcurrentUpdate
	}

	opened, err := coordinator.pad.openRepository(ctx)
	if err != nil {
		return "", err
	}
	repository, ok := opened.(ownerPADDecisionRepository)
	if !ok {
		_ = opened.Close()
		return "", errors.New(
			"PAD owner repository does not support live delivery decisions",
		)
	}
	defer func() {
		if closeErr := repository.Close(); closeErr != nil {
			decisionRef = ""
			err = errors.Join(err, closeErr)
		}
	}()
	admitted, err := repository.ResolveAdmittedDecision(
		ctx,
		state.DeliveryIntentRef,
	)
	if err != nil {
		return "", err
	}
	admission := admitted.Decision
	if admission.IntentRef != state.DeliveryIntentRef ||
		admission.Disposition != deliveryadmission.AdmissionAdmitted {
		return "", deliveryadmission.ErrBindingMismatch
	}
	mechanism, err := productiveDeliveryMechanism(admission.Route)
	if err != nil {
		return "", err
	}
	candidate := deliveryadmission.CandidateBinding{
		Ref:    "work-run:" + state.WorkRunID,
		Digest: state.Handoff.CandidateRef,
	}
	candidateAuthority, err := coordinator.preparePADCandidateBindingFor(
		ctx,
		candidate,
		admission.Destination,
		mechanism,
		state.ReviewReceiptRef,
		state.VerificationResultRef,
	)
	if err != nil {
		return "", err
	}
	deliveryPort, err := coordinator.padDeliveryForCandidateAuthority(
		ctx,
		candidateAuthority,
	)
	if err != nil {
		return "", err
	}
	probeRequest, err := deliveryadmission.NewLiveGateProbeRequest(
		admission.Route,
		candidate,
		admission.Destination,
		admission.PolicyRef,
		admission.Policy.Binding(),
		mechanism,
	)
	if err != nil {
		return "", err
	}
	gates, err := deliveryadmission.ProbeGateEvidence(
		ctx,
		repository,
		deliveryPort,
		probeRequest,
	)
	if err != nil {
		return "", err
	}
	gateRef, err := repository.PublishGateEvidence(ctx, gates)
	if err != nil {
		return "", err
	}
	decision, err := deliveryadmission.Decide(
		ctx,
		repository,
		coordinator.rar,
		deliveryadmission.DeliveryRequest{
			AdmissionDecisionRef:  admitted.AdmissionDecisionRef,
			PolicyRef:             admission.PolicyRef,
			ReviewReceiptRef:      state.ReviewReceiptRef,
			VerificationResultRef: state.VerificationResultRef,
			CandidateAuthorityRef: candidateAuthority.RecordRef,
			GateRef:               gateRef,
			AuthorityRef:          admission.AuthorityRef,
			SecondAuthorityRef:    admission.SecondAuthorityRef,
		},
	)
	if err != nil {
		return "", err
	}
	if decision.Disposition != deliveryadmission.DeliveryAuthorizeNormal {
		return "", deliveryadmission.ErrUnauthorized
	}
	decisionRef, err = repository.PublishDeliveryDecision(ctx, decision)
	return decisionRef, err
}

func productiveDeliveryMechanism(
	route deliveryadmission.Route,
) (deliveryadmission.Mechanism, error) {
	switch route {
	case deliveryadmission.RoutePRWithIssue,
		deliveryadmission.RoutePRWithoutIssue:
		return deliveryadmission.MechanismPullRequest, nil
	case deliveryadmission.RouteDirectMain:
		return deliveryadmission.MechanismFastForwardOnly, nil
	default:
		return "", fmt.Errorf(
			"%w: emergency delivery requires an explicit human mechanism",
			deliveryadmission.ErrUnauthorized,
		)
	}
}
