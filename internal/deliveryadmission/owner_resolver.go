package deliveryadmission

import (
	"context"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

func (repository *TrustedRepository) PublishRoutePolicy(
	ctx context.Context,
	value RoutePolicy,
) (string, error) {
	return publishTrustedValue(
		ctx, repository, "route-policy", value,
		func(value RoutePolicy) error { return value.Validate() },
		func(value RoutePolicy) (string, error) { return value.Ref() },
	)
}

func (repository *TrustedRepository) PublishIntent(
	ctx context.Context,
	value DeliveryIntent,
) (string, error) {
	if err := repository.requireRepositoryBinding(value.Destination.RepositoryRef); err != nil {
		return "", err
	}
	return publishTrustedValue(
		ctx, repository, "intent", value,
		func(value DeliveryIntent) error { return value.Validate() },
		func(value DeliveryIntent) (string, error) { return value.Ref() },
	)
}

func (repository *TrustedRepository) PublishAuthoritySignal(
	ctx context.Context,
	value AuthoritySignal,
) (string, error) {
	return publishTrustedValue(
		ctx, repository, "authority-signal", value,
		func(value AuthoritySignal) error { return value.Validate() },
		func(value AuthoritySignal) (string, error) { return value.Ref() },
	)
}

func (repository *TrustedRepository) PublishGovernanceDecision(
	ctx context.Context,
	value GovernanceDecision,
) (string, error) {
	if err := repository.requireRepositoryBinding(value.RepositoryRef); err != nil {
		return "", err
	}
	if err := repository.requireRepositoryBinding(value.Destination.RepositoryRef); err != nil {
		return "", err
	}
	return publishTrustedValue(
		ctx, repository, "governance-decision", value,
		func(value GovernanceDecision) error { return value.Validate() },
		func(value GovernanceDecision) (string, error) { return value.Ref() },
	)
}

func (repository *TrustedRepository) PublishResidualRisk(
	ctx context.Context,
	value ResidualRisk,
) (string, error) {
	if err := repository.requireRepositoryBinding(value.Destination.RepositoryRef); err != nil {
		return "", err
	}
	return publishTrustedValue(
		ctx, repository, "residual-risk", value,
		func(value ResidualRisk) error { return value.Validate() },
		func(value ResidualRisk) (string, error) { return value.Ref() },
	)
}

func (repository *TrustedRepository) PublishAdmissionDecision(
	ctx context.Context,
	value AdmissionDecision,
) (string, error) {
	if err := repository.requireRepositoryBinding(value.Destination.RepositoryRef); err != nil {
		return "", err
	}
	ref, err := publishTrustedValue(
		ctx, repository, "admission-decision", value,
		func(value AdmissionDecision) error { return value.Validate() },
		func(value AdmissionDecision) (string, error) { return value.Ref() },
	)
	if err != nil || value.Disposition != AdmissionAdmitted {
		return ref, err
	}
	index := admittedIntentIndex{
		Schema:       admittedIntentIndexSchema,
		IntentRef:    value.IntentRef,
		AdmissionRef: ref,
	}
	if err := repository.publishIndex(
		ctx,
		indexRecordName(value.IntentRef, "admitted-intent"),
		index,
		index.validate,
	); err != nil {
		return "", err
	}
	return ref, nil
}

func (repository *TrustedRepository) PublishGateEvidence(
	ctx context.Context,
	value GateEvidence,
) (string, error) {
	if err := repository.requireRepositoryBinding(value.Destination.RepositoryRef); err != nil {
		return "", err
	}
	return publishTrustedValue(
		ctx, repository, "gate-evidence", value,
		func(value GateEvidence) error { return value.Validate() },
		func(value GateEvidence) (string, error) { return value.Ref() },
	)
}

func (repository *TrustedRepository) PublishDeliveryDecision(
	ctx context.Context,
	value DeliveryDecision,
) (string, error) {
	if err := repository.requireRepositoryBinding(
		value.Gates.Destination.RepositoryRef,
	); err != nil {
		return "", err
	}
	return publishTrustedValue(
		ctx, repository, "delivery-decision", value,
		func(value DeliveryDecision) error { return value.Validate() },
		func(value DeliveryDecision) (string, error) { return value.Ref() },
	)
}

func (repository *TrustedRepository) PublishRouteReevaluation(
	ctx context.Context,
	value RouteReevaluation,
) (string, error) {
	if err := repository.requireRepositoryBinding(
		value.Destination.RepositoryRef,
	); err != nil {
		return "", err
	}
	return publishTrustedValue(
		ctx, repository, "route-reevaluation", value,
		func(value RouteReevaluation) error { return value.Validate() },
		func(value RouteReevaluation) (string, error) { return value.Ref() },
	)
}

func (repository *TrustedRepository) PublishAuthorization(
	ctx context.Context,
	value DeliveryAuthorization,
) (string, error) {
	if err := repository.requireRepositoryBinding(
		value.Binding.Destination.RepositoryRef,
	); err != nil {
		return "", err
	}
	ref, err := publishTrustedValue(
		ctx, repository, "authorization", value,
		func(value DeliveryAuthorization) error { return value.Validate() },
		func(value DeliveryAuthorization) (string, error) { return value.Ref() },
	)
	if err != nil {
		return "", err
	}
	index := authorizationKindIndex{
		Schema:           authorizationKindIndexSchema,
		AuthorizationRef: ref,
		Kind:             AuthorizationNormal,
	}
	if err := repository.publishIndex(
		ctx,
		indexRecordName(ref, "authorization-kind"),
		index,
		index.validate,
	); err != nil {
		return "", err
	}
	return ref, nil
}

func (repository *TrustedRepository) PublishExceptionAuthorization(
	ctx context.Context,
	value DeliveryExceptionAuthorization,
) (string, error) {
	if err := repository.requireRepositoryBinding(
		value.Binding.Destination.RepositoryRef,
	); err != nil {
		return "", err
	}
	ref, err := publishTrustedValue(
		ctx, repository, "exception-authorization", value,
		func(value DeliveryExceptionAuthorization) error { return value.Validate() },
		func(value DeliveryExceptionAuthorization) (string, error) { return value.Ref() },
	)
	if err != nil {
		return "", err
	}
	index := authorizationKindIndex{
		Schema:           authorizationKindIndexSchema,
		AuthorizationRef: ref,
		Kind:             AuthorizationException,
	}
	if err := repository.publishIndex(
		ctx,
		indexRecordName(ref, "authorization-kind"),
		index,
		index.validate,
	); err != nil {
		return "", err
	}
	return ref, nil
}

func (repository *TrustedRepository) ResolveIntent(
	ctx context.Context,
	ref string,
) (DeliveryIntent, error) {
	value, err := resolveTrustedValue(
		ctx, repository, ref, "intent",
		func(value DeliveryIntent) error { return value.Validate() },
		func(value DeliveryIntent) (string, error) { return value.Ref() },
	)
	if err == nil {
		err = repository.requireRepositoryBinding(value.Destination.RepositoryRef)
	}
	return value, err
}

func (repository *TrustedRepository) ResolveRoutePolicy(
	ctx context.Context,
	ref string,
) (RoutePolicy, error) {
	return resolveTrustedValue(
		ctx, repository, ref, "route-policy",
		func(value RoutePolicy) error { return value.Validate() },
		func(value RoutePolicy) (string, error) { return value.Ref() },
	)
}

func (repository *TrustedRepository) ResolveAuthoritySignal(
	ctx context.Context,
	ref string,
) (AuthoritySignal, error) {
	return resolveTrustedValue(
		ctx, repository, ref, "authority-signal",
		func(value AuthoritySignal) error { return value.Validate() },
		func(value AuthoritySignal) (string, error) { return value.Ref() },
	)
}

func (repository *TrustedRepository) ResolveGovernanceDecision(
	ctx context.Context,
	ref string,
) (GovernanceDecision, error) {
	value, err := resolveTrustedValue(
		ctx, repository, ref, "governance-decision",
		func(value GovernanceDecision) error { return value.Validate() },
		func(value GovernanceDecision) (string, error) { return value.Ref() },
	)
	if err == nil {
		err = repository.requireRepositoryBinding(value.RepositoryRef)
	}
	if err == nil {
		err = repository.requireRepositoryBinding(value.Destination.RepositoryRef)
	}
	return value, err
}

func (repository *TrustedRepository) ResolveResidualRisk(
	ctx context.Context,
	ref string,
) (ResidualRisk, error) {
	value, err := resolveTrustedValue(
		ctx, repository, ref, "residual-risk",
		func(value ResidualRisk) error { return value.Validate() },
		func(value ResidualRisk) (string, error) { return value.Ref() },
	)
	if err == nil {
		err = repository.requireRepositoryBinding(value.Destination.RepositoryRef)
	}
	return value, err
}

func (repository *TrustedRepository) ResolveAdmissionDecision(
	ctx context.Context,
	ref string,
) (AdmissionDecision, error) {
	value, err := resolveTrustedValue(
		ctx, repository, ref, "admission-decision",
		func(value AdmissionDecision) error { return value.Validate() },
		func(value AdmissionDecision) (string, error) { return value.Ref() },
	)
	if err == nil {
		err = repository.requireRepositoryBinding(value.Destination.RepositoryRef)
	}
	return value, err
}

func (repository *TrustedRepository) ResolveGateEvidence(
	ctx context.Context,
	ref string,
) (GateEvidence, error) {
	value, err := resolveTrustedValue(
		ctx, repository, ref, "gate-evidence",
		func(value GateEvidence) error { return value.Validate() },
		func(value GateEvidence) (string, error) { return value.Ref() },
	)
	if err == nil {
		err = repository.requireRepositoryBinding(value.Destination.RepositoryRef)
	}
	return value, err
}

func (repository *TrustedRepository) ResolveDeliveryDecision(
	ctx context.Context,
	ref string,
) (DeliveryDecision, error) {
	value, err := resolveTrustedValue(
		ctx, repository, ref, "delivery-decision",
		func(value DeliveryDecision) error { return value.Validate() },
		func(value DeliveryDecision) (string, error) { return value.Ref() },
	)
	if err == nil {
		err = repository.requireRepositoryBinding(value.Gates.Destination.RepositoryRef)
	}
	return value, err
}

func (repository *TrustedRepository) ResolveRouteReevaluation(
	ctx context.Context,
	ref string,
) (RouteReevaluation, error) {
	value, err := resolveTrustedValue(
		ctx, repository, ref, "route-reevaluation",
		func(value RouteReevaluation) error { return value.Validate() },
		func(value RouteReevaluation) (string, error) { return value.Ref() },
	)
	if err == nil {
		err = repository.requireRepositoryBinding(value.Destination.RepositoryRef)
	}
	return value, err
}

func (repository *TrustedRepository) ResolveAuthorization(
	ctx context.Context,
	ref string,
) (DeliveryAuthorization, error) {
	value, err := resolveTrustedValue(
		ctx, repository, ref, "authorization",
		func(value DeliveryAuthorization) error { return value.Validate() },
		func(value DeliveryAuthorization) (string, error) { return value.Ref() },
	)
	if err == nil {
		err = repository.requireRepositoryBinding(value.Binding.Destination.RepositoryRef)
	}
	return value, err
}

func (repository *TrustedRepository) ResolveExceptionAuthorization(
	ctx context.Context,
	ref string,
) (DeliveryExceptionAuthorization, error) {
	value, err := resolveTrustedValue(
		ctx, repository, ref, "exception-authorization",
		func(value DeliveryExceptionAuthorization) error { return value.Validate() },
		func(value DeliveryExceptionAuthorization) (string, error) { return value.Ref() },
	)
	if err == nil {
		err = repository.requireRepositoryBinding(value.Binding.Destination.RepositoryRef)
	}
	return value, err
}

func (repository *TrustedRepository) requireRepositoryBinding(repositoryRef string) error {
	if repository == nil || repositoryRef != repository.repositoryRef {
		return fmt.Errorf("%w: cross-repository PAD preimage", ErrBindingMismatch)
	}
	return nil
}

func resolveTrustedValue[T any](
	ctx context.Context,
	repository *TrustedRepository,
	ref string,
	kind string,
	validate func(T) error,
	contentRef func(T) (string, error),
) (T, error) {
	var zero T
	if err := validateDigest("trusted repository ref", ref); err != nil {
		return zero, err
	}
	value, err := readTrustedValue(
		ctx,
		repository,
		objectRecordName(ref, kind),
		validate,
	)
	if err != nil {
		return zero, err
	}
	actual, err := contentRef(value)
	if err != nil {
		return zero, fmt.Errorf("%w: invalid %s object: %v", ErrTrustedRepositoryCorrupt, kind, err)
	}
	if actual != ref {
		return zero, fmt.Errorf("%w: %s content identity mismatch", ErrTrustedRepositoryCorrupt, kind)
	}
	return value, nil
}

var _ TrustedResolver = (*TrustedRepository)(nil)
var _ RARAuthorityPort = (*TrustedRepository)(nil)
var _ RARAuthorityPort = (*reviewtransaction.RARAuthorityRepository)(nil)
