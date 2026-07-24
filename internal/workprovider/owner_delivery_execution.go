package workprovider

import (
	"context"
	"errors"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type ownerPADDeliveryExecutionRepository interface {
	ownerPADAuthorizationRepository
	deliveryadmission.RARAuthorityPort
}

type ownerBoundDeliveryAuthorization struct {
	kind    deliveryadmission.AuthorizationKind
	binding deliveryadmission.AuthorizationBinding
}

// preparePADCandidateBinding derives the reviewed Git tree exclusively from
// RAR's exact terminal receipt and occupies the local candidate-catalog slot
// with the connector-issued Git/hosting binding. Legacy composition has
// neither optional authority and intentionally remains unchanged.
//
// The catalog is checked first so a crash after catalog publication but before
// PAD authorization publication can replay without asking mutable transport
// state to reconstruct an already-occupied append-only slot.
func (coordinator *OwnerCoordinator) preparePADCandidateBinding(
	ctx context.Context,
	decision deliveryadmission.DeliveryDecision,
) error {
	return coordinator.preparePADCandidateBindingFor(
		ctx,
		decision.Candidate,
		decision.Gates.Destination,
		decision.Gates.Mechanism,
		decision.ReviewReceiptRef,
		decision.VerificationResultRef,
	)
}

func (coordinator *OwnerCoordinator) preparePADCandidateBindingFor(
	ctx context.Context,
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
	receiptRef string,
	resultRef string,
) error {
	if coordinator.padCandidates == nil &&
		coordinator.padBindingSource == nil {
		return nil
	}
	if coordinator.padCandidates == nil ||
		coordinator.padBindingSource == nil {
		return errors.New(
			"owner coordinator PAD candidate binding authorities are incomplete",
		)
	}
	authority, err := coordinator.rar.ResolveReceiptResult(
		ctx,
		receiptRef,
		resultRef,
	)
	if err != nil {
		return fmt.Errorf("resolve reviewed PAD candidate tree: %w", err)
	}
	candidateTree := authority.Receipt.CandidateTree()
	if candidateTree == "" ||
		authority.Receipt.ReceiptRef != receiptRef ||
		authority.Result.ResultRef != resultRef ||
		authority.Result.Subject.CandidateTree != candidateTree {
		return fmt.Errorf(
			"%w: RAR returned a mismatched reviewed candidate tree",
			deliveryadmission.ErrBindingMismatch,
		)
	}

	binding, resolveErr := coordinator.padCandidates.ResolvePADGitBinding(
		ctx,
		candidate,
		destination,
		mechanism,
	)
	if resolveErr != nil {
		if !errors.Is(
			resolveErr,
			ErrPADCandidateCatalogUnavailable,
		) {
			return resolveErr
		}
		binding, err = coordinator.padBindingSource.ResolvePADGitBinding(
			ctx,
			candidate,
			destination,
			mechanism,
		)
		if err != nil {
			return err
		}
	}
	if err := binding.Validate(candidate, destination, mechanism); err != nil {
		return err
	}
	published, err := coordinator.padCandidates.BindCandidate(
		ctx,
		candidateTree,
		binding,
	)
	if err != nil {
		return err
	}
	if published != binding {
		return fmt.Errorf(
			"%w: PAD candidate catalog published a mismatched binding",
			ErrPADCandidateCatalogConflict,
		)
	}
	resolved, err := coordinator.padCandidates.ResolvePADGitBinding(
		ctx,
		candidate,
		destination,
		mechanism,
	)
	if err != nil {
		return err
	}
	if resolved != binding {
		return fmt.Errorf(
			"%w: PAD candidate catalog resolved a mismatched binding",
			ErrPADCandidateCatalogCorrupt,
		)
	}
	return nil
}

// ExecuteBoundDelivery performs the one delivery effect already authorized by
// the terminal WorkRun. It accepts no authority reference, authorization kind,
// repository, route, candidate, destination, probe, executor, or effect mode
// from its caller.
//
// The owner re-resolves the exact immutable PAD authorization, derives normal
// versus exception internally, and uses the same composed PADDeliveryAdapter
// for both the live gate and the one-shot effect. DirectoryUseStore and the
// adapter's content-addressed result store make retries and concurrent
// invocations replay-safe.
func (coordinator *OwnerCoordinator) ExecuteBoundDelivery(
	ctx context.Context,
) (
	result deliveryadmission.ExecutionResult,
	err error,
) {
	if err := coordinator.guardTerminalMutation(ctx); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	state, err := coordinator.work.Status()
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if err := validateOwnerBoundDeliveryState(state); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}

	opened, err := coordinator.pad.openRepository(ctx)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	repository, ok := opened.(ownerPADDeliveryExecutionRepository)
	if !ok {
		_ = opened.Close()
		return deliveryadmission.ExecutionResult{}, errors.New(
			"PAD owner repository does not support bound delivery execution",
		)
	}
	defer func() {
		if closeErr := repository.Close(); closeErr != nil {
			result = deliveryadmission.ExecutionResult{}
			err = errors.Join(
				err,
				fmt.Errorf(
					"close PAD bound-delivery repository: %w",
					closeErr,
				),
			)
		}
	}()

	authorization, err := resolveOwnerBoundDeliveryAuthorization(
		ctx,
		repository,
		state.DeliveryAuthorizationRef,
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if err := validateOwnerBoundDeliveryAuthorization(
		ctx,
		repository,
		state,
		authorization.binding,
		coordinator.padDelivery.RepositoryRef(),
	); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}

	useStore, err := deliveryadmission.OpenDirectoryUseStore(
		ctx,
		repository,
		coordinator.padDelivery.RepositoryRef(),
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	defer func() {
		if closeErr := useStore.Close(); closeErr != nil {
			result = deliveryadmission.ExecutionResult{}
			err = errors.Join(
				err,
				fmt.Errorf(
					"close PAD bound-delivery use store: %w",
					closeErr,
				),
			)
		}
	}()

	// Resolve immutable authority outside the shared owner lock, then serialize
	// only the live probe/reservation/effect window. This lets concurrent
	// callers do expensive validation in parallel while ensuring that every
	// loser observes the winning durable reservation before probing mutable
	// hosting state.
	select {
	case coordinator.deliveryExecutionGate <- struct{}{}:
		defer func() { <-coordinator.deliveryExecutionGate }()
	case <-ctx.Done():
		return deliveryadmission.ExecutionResult{}, ctx.Err()
	}
	lock, err := acquirePADDeliveryOwnerLock(ctx, coordinator.work.Dir)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			result = deliveryadmission.ExecutionResult{}
			err = errors.Join(
				err,
				fmt.Errorf(
					"release PAD bound-delivery owner lock: %w",
					releaseErr,
				),
			)
		}
	}()

	// Store and authority resolution can involve blocking filesystem work.
	// Recheck once more at the last owner boundary before live probing or an
	// external effect.
	if err := coordinator.guardTerminalMutation(ctx); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	live, err := coordinator.work.Status()
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if live.Revision != state.Revision ||
		live.DeliveryAuthorizationRef != state.DeliveryAuthorizationRef {
		return deliveryadmission.ExecutionResult{}, fmt.Errorf(
			"%w: terminal WorkRun changed before bound delivery",
			workrun.ErrWorkRunConcurrentUpdate,
		)
	}
	return deliveryadmission.ExecuteAuthorizedDelivery(
		ctx,
		repository,
		repository,
		coordinator.padDelivery,
		coordinator.padDelivery,
		useStore,
		deliveryadmission.ExecuteDeliveryRequest{
			AuthorizationRef:  state.DeliveryAuthorizationRef,
			AuthorizationKind: authorization.kind,
		},
	)
}

// RecoverBoundDelivery resolves only an already-consumed, already-durable PAD
// terminal result. It deliberately bypasses the activation mutation guard
// because it cannot reserve, probe, or execute an effect; this is the
// reconciliation path used after a kill switch changes during a lost response.
func (coordinator *OwnerCoordinator) RecoverBoundDelivery(
	ctx context.Context,
) (
	result deliveryadmission.ExecutionResult,
	found bool,
	err error,
) {
	if err := coordinator.validate(ctx); err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	state, err := coordinator.work.Status()
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	if err := validateOwnerBoundDeliveryState(state); err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	opened, err := coordinator.pad.openRepository(ctx)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	repository, ok := opened.(ownerPADDeliveryExecutionRepository)
	if !ok {
		_ = opened.Close()
		return deliveryadmission.ExecutionResult{}, false, errors.New(
			"PAD owner repository does not support bound delivery recovery",
		)
	}
	defer func() {
		if closeErr := repository.Close(); closeErr != nil {
			result = deliveryadmission.ExecutionResult{}
			found = false
			err = errors.Join(err, closeErr)
		}
	}()
	authorization, err := resolveOwnerBoundDeliveryAuthorization(
		ctx,
		repository,
		state.DeliveryAuthorizationRef,
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	if err := validateOwnerBoundDeliveryAuthorization(
		ctx,
		repository,
		state,
		authorization.binding,
		coordinator.padDelivery.RepositoryRef(),
	); err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	useStore, err := deliveryadmission.OpenDirectoryUseStore(
		ctx,
		repository,
		coordinator.padDelivery.RepositoryRef(),
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	defer func() {
		if closeErr := useStore.Close(); closeErr != nil {
			result = deliveryadmission.ExecutionResult{}
			found = false
			err = errors.Join(err, closeErr)
		}
	}()
	return deliveryadmission.ReplayAuthorizedDeliveryResult(
		ctx,
		repository,
		repository,
		coordinator.padDelivery,
		useStore,
		deliveryadmission.ExecuteDeliveryRequest{
			AuthorizationRef:  state.DeliveryAuthorizationRef,
			AuthorizationKind: authorization.kind,
		},
	)
}

func validateOwnerBoundDeliveryState(state workrun.WorkRunState) error {
	if state.DeliveryAuthorizationRef == "" {
		return fmt.Errorf(
			"%w: WorkRun has no bound delivery authorization",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	return validateOwnerDeliveryDecisionState(state)
}

func resolveOwnerBoundDeliveryAuthorization(
	ctx context.Context,
	repository ownerPADDeliveryExecutionRepository,
	authorizationRef string,
) (ownerBoundDeliveryAuthorization, error) {
	normal, normalErr := deliveryadmission.ValidateAuthorization(
		ctx,
		repository,
		repository,
		authorizationRef,
	)
	exception, exceptionErr :=
		deliveryadmission.ValidateExceptionAuthorization(
			ctx,
			repository,
			repository,
			authorizationRef,
		)
	switch {
	case normalErr == nil && exceptionErr == nil:
		return ownerBoundDeliveryAuthorization{}, fmt.Errorf(
			"%w: delivery authorization resolves as both normal and exception",
			deliveryadmission.ErrTrustedRepositoryCorrupt,
		)
	case normalErr == nil:
		return ownerBoundDeliveryAuthorization{
			kind:    deliveryadmission.AuthorizationNormal,
			binding: normal.Binding,
		}, nil
	case exceptionErr == nil:
		return ownerBoundDeliveryAuthorization{
			kind:    deliveryadmission.AuthorizationException,
			binding: exception.Binding,
		}, nil
	default:
		return ownerBoundDeliveryAuthorization{}, errors.Join(
			fmt.Errorf("resolve normal delivery authorization: %w", normalErr),
			fmt.Errorf(
				"resolve exception delivery authorization: %w",
				exceptionErr,
			),
		)
	}
}

func validateOwnerBoundDeliveryAuthorization(
	ctx context.Context,
	repository ownerPADDeliveryExecutionRepository,
	state workrun.WorkRunState,
	binding deliveryadmission.AuthorizationBinding,
	repositoryRef string,
) error {
	if state.Handoff == nil ||
		binding.IntentRef != state.DeliveryIntentRef ||
		binding.Candidate.Digest != state.Handoff.CandidateRef ||
		binding.VerificationResultRef != state.VerificationResultRef ||
		binding.ReviewReceiptRef != state.ReviewReceiptRef ||
		binding.Destination.RepositoryRef != repositoryRef {
		return fmt.Errorf(
			"%w: PAD authorization does not bind the exact terminal WorkRun",
			deliveryadmission.ErrBindingMismatch,
		)
	}
	return matchOwnerDeliveryRouteDecision(
		ctx,
		repository,
		state,
		binding.DecisionRef,
	)
}
