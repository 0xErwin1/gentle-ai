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

// preparePADCandidateBinding re-resolves the reviewed Git tree exclusively
// from RAR and requires the decision's exact content-addressed candidate
// record. Productive owner composition never reconstructs that authority from
// the mutable catalog lookup after the decision exists.
func (coordinator *OwnerCoordinator) preparePADCandidateBinding(
	ctx context.Context,
	decision deliveryadmission.DeliveryDecision,
) (PADCandidateAuthority, error) {
	return coordinator.resolvePADCandidateBinding(
		ctx,
		decision,
		true,
	)
}

func (coordinator *OwnerCoordinator) preparePADCandidateBindingFor(
	ctx context.Context,
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
	receiptRef string,
	resultRef string,
) (PADCandidateAuthority, error) {
	candidateTree, binding, err :=
		coordinator.resolvePADCandidateBindingProposal(
			ctx,
			candidate,
			destination,
			mechanism,
			receiptRef,
			resultRef,
		)
	if err != nil || candidateTree == "" {
		return PADCandidateAuthority{}, err
	}
	return coordinator.publishPADCandidateBinding(
		ctx,
		candidateTree,
		binding,
	)
}

// resolvePADCandidateBindingProposal observes and validates the candidate
// binding without occupying its catalog slot. Callers that must compare a
// route transition against an existing exact authority can therefore reject a
// drifted connector response before it poisons the single-assignment index.
func (coordinator *OwnerCoordinator) resolvePADCandidateBindingProposal(
	ctx context.Context,
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
	receiptRef string,
	resultRef string,
) (string, PADGitBinding, error) {
	if coordinator.padCandidates == nil &&
		coordinator.padBindingSource == nil {
		return "", PADGitBinding{}, nil
	}
	if coordinator.padCandidates == nil ||
		coordinator.padBindingSource == nil {
		return "", PADGitBinding{}, errors.New(
			"owner coordinator PAD candidate binding authorities are incomplete",
		)
	}
	candidateTree, err := coordinator.resolveReviewedPADCandidateTree(
		ctx,
		receiptRef,
		resultRef,
	)
	if err != nil {
		return "", PADGitBinding{}, err
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
			return "", PADGitBinding{}, resolveErr
		}
		binding, err = coordinator.padBindingSource.ResolvePADGitBinding(
			ctx,
			candidate,
			destination,
			mechanism,
		)
		if err != nil {
			return "", PADGitBinding{}, err
		}
	}
	if err := binding.Validate(candidate, destination, mechanism); err != nil {
		return "", PADGitBinding{}, err
	}
	return candidateTree, binding, nil
}

func (coordinator *OwnerCoordinator) publishPADCandidateBinding(
	ctx context.Context,
	candidateTree string,
	binding PADGitBinding,
) (PADCandidateAuthority, error) {
	published, err := coordinator.padCandidates.BindCandidate(
		ctx,
		candidateTree,
		binding,
	)
	if err != nil {
		return PADCandidateAuthority{}, err
	}
	if published.Binding != binding ||
		published.CandidateTree != candidateTree {
		return PADCandidateAuthority{}, fmt.Errorf(
			"%w: PAD candidate catalog published a mismatched binding",
			ErrPADCandidateCatalogConflict,
		)
	}
	resolved, err := coordinator.padCandidates.ResolveCandidateAuthority(
		ctx,
		published.RecordRef,
		binding.Candidate,
		binding.Destination,
		binding.Mechanism,
		candidateTree,
	)
	if err != nil {
		return PADCandidateAuthority{}, err
	}
	if resolved != published {
		return PADCandidateAuthority{}, fmt.Errorf(
			"%w: PAD candidate catalog resolved a mismatched binding",
			ErrPADCandidateCatalogCorrupt,
		)
	}
	if err := coordinator.padCandidates.RequireCurrentCandidateAuthority(
		ctx,
		resolved,
	); err != nil {
		return PADCandidateAuthority{}, err
	}
	return resolved, nil
}

func (coordinator *OwnerCoordinator) resolvePADCandidateBinding(
	ctx context.Context,
	decision deliveryadmission.DeliveryDecision,
	requireCurrent bool,
) (PADCandidateAuthority, error) {
	if coordinator.padCandidates == nil {
		if decision.CandidateAuthorityRef != "" {
			return PADCandidateAuthority{}, errors.New(
				"owner coordinator cannot resolve the decision candidate authority",
			)
		}
		return PADCandidateAuthority{}, nil
	}
	if decision.CandidateAuthorityRef == "" {
		if coordinator.padBindingSource == nil {
			// Historical generic decisions predate the exact candidate anchor.
			// Recovery may read them, but a productive composition capable of
			// selecting new bindings must never downgrade to that shape.
			return PADCandidateAuthority{}, nil
		}
		return PADCandidateAuthority{}, fmt.Errorf(
			"%w: productive delivery decision lacks exact candidate authority",
			deliveryadmission.ErrBindingMismatch,
		)
	}
	candidateTree, err := coordinator.resolveReviewedPADCandidateTree(
		ctx,
		decision.ReviewReceiptRef,
		decision.VerificationResultRef,
	)
	if err != nil {
		return PADCandidateAuthority{}, err
	}
	var resolved PADCandidateAuthority
	if requireCurrent {
		resolved, err = coordinator.padCandidates.ResolveCandidateAuthority(
			ctx,
			decision.CandidateAuthorityRef,
			decision.Candidate,
			decision.Gates.Destination,
			decision.Gates.Mechanism,
			candidateTree,
		)
	} else {
		resolved, err =
			coordinator.padCandidates.resolveCandidateAuthorityReadOnly(
				ctx,
				decision.CandidateAuthorityRef,
				decision.Candidate,
				decision.Gates.Destination,
				decision.Gates.Mechanism,
				candidateTree,
			)
	}
	if err != nil {
		return PADCandidateAuthority{}, err
	}
	if requireCurrent {
		if err := coordinator.padCandidates.RequireCurrentCandidateAuthority(
			ctx,
			resolved,
		); err != nil {
			return PADCandidateAuthority{}, err
		}
	}
	return resolved, nil
}

func (coordinator *OwnerCoordinator) resolveReviewedPADCandidateTree(
	ctx context.Context,
	receiptRef string,
	resultRef string,
) (string, error) {
	authority, err := coordinator.rar.ResolveReceiptResult(
		ctx,
		receiptRef,
		resultRef,
	)
	if err != nil {
		return "", fmt.Errorf("resolve reviewed PAD candidate tree: %w", err)
	}
	candidateTree := authority.Receipt.CandidateTree()
	if candidateTree == "" ||
		authority.Receipt.ReceiptRef != receiptRef ||
		authority.Result.ResultRef != resultRef ||
		authority.Result.Subject.CandidateTree != candidateTree {
		return "", fmt.Errorf(
			"%w: RAR returned a mismatched reviewed candidate tree",
			deliveryadmission.ErrBindingMismatch,
		)
	}
	return candidateTree, nil
}

func (coordinator *OwnerCoordinator) padDeliveryForCandidateAuthority(
	ctx context.Context,
	authority PADCandidateAuthority,
) (*PADDeliveryAdapter, error) {
	if authority.RecordRef == "" {
		return coordinator.padDelivery, nil
	}
	pinned, err := newPADPinnedCandidateBindingAuthority(
		ctx,
		coordinator.padCandidates,
		authority,
	)
	if err != nil {
		return nil, err
	}
	adapter, err := NewPADDeliveryAdapter(
		ctx,
		coordinator.padDelivery.authority,
		pinned,
		coordinator.padDelivery.git,
		coordinator.padDelivery.hosting,
	)
	if err != nil {
		return nil, err
	}
	adapter.now = coordinator.padDelivery.now
	return adapter, nil
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
	if err := validateOwnerBoundDeliveryState(state, false); err != nil {
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
	decision, err := deliveryadmission.ValidateDeliveryDecision(
		ctx,
		repository,
		repository,
		authorization.binding.DecisionRef,
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	candidateAuthority, err := coordinator.resolvePADCandidateBinding(
		ctx,
		decision,
		true,
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	deliveryPort, err := coordinator.padDeliveryForCandidateAuthority(
		ctx,
		candidateAuthority,
	)
	if err != nil {
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
	if err := validateOwnerBoundDeliveryState(live, false); err != nil {
		return deliveryadmission.ExecutionResult{}, err
	}
	if live.Revision != state.Revision ||
		live.DeliveryAuthorizationRef != state.DeliveryAuthorizationRef {
		return deliveryadmission.ExecutionResult{}, fmt.Errorf(
			"%w: terminal WorkRun changed before bound delivery",
			workrun.ErrWorkRunConcurrentUpdate,
		)
	}
	replayRequest := deliveryadmission.ExecuteDeliveryRequest{
		AuthorizationRef:  state.DeliveryAuthorizationRef,
		AuthorizationKind: authorization.kind,
	}
	if _, found, replayErr :=
		deliveryadmission.ReplayAuthorizedDeliveryResult(
			ctx,
			repository,
			repository,
			coordinator.padDelivery,
			useStore,
			replayRequest,
		); replayErr != nil {
		// A consumed authorization is recovery-only. In particular, a
		// post-effect/pre-WorkRun crash must never be reclassified as a fresh
		// effect and promoted into the ledger.
		return deliveryadmission.ExecutionResult{}, replayErr
	} else if found {
		return deliveryadmission.ExecutionResult{},
			deliveryadmission.ErrExecutionResultUnavailable
	}
	return deliveryadmission.ExecuteAuthorizedDelivery(
		ctx,
		repository,
		repository,
		deliveryPort,
		deliveryPort,
		useStore,
		replayRequest,
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
	if err := validateOwnerBoundDeliveryState(state, true); err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	var anchored workrun.ProductiveExecutionResultAuthority
	if state.ProductiveExecutionResultRef != "" {
		anchored, err = coordinator.work.ResolveProductiveExecutionResult(
			ctx,
			state.ProductiveExecutionResultRef,
		)
		if err != nil {
			return deliveryadmission.ExecutionResult{}, false,
				fmt.Errorf(
					"%w: resolve WorkRun execution result authority: %v",
					deliveryadmission.ErrExecutionResultCorrupt,
					err,
				)
		}
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
	decision, err := deliveryadmission.ValidateDeliveryDecision(
		ctx,
		repository,
		repository,
		authorization.binding.DecisionRef,
	)
	if err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	if _, err := coordinator.resolvePADCandidateBinding(
		ctx,
		decision,
		false,
	); err != nil {
		return deliveryadmission.ExecutionResult{}, false, err
	}
	// Recovery consumes only the exact terminal result store. The candidate
	// authority was proved above; no mutable connector, candidate index, Git
	// probe, or hosting effect is needed to corroborate the stored command.
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
	replayed, found, replayErr :=
		deliveryadmission.ReplayAuthorizedDeliveryResult(
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
	if replayErr != nil {
		if state.ProductiveExecutionResultRef != "" &&
			!errors.Is(
				replayErr,
				deliveryadmission.ErrExecutionResultCorrupt,
			) {
			return deliveryadmission.ExecutionResult{}, false,
				fmt.Errorf(
					"%w: PAD result behind WorkRun anchor is unavailable: %v",
					deliveryadmission.ErrExecutionResultCorrupt,
					replayErr,
				)
		}
		return deliveryadmission.ExecutionResult{}, false, replayErr
	}
	if state.ProductiveExecutionResultRef == "" {
		if found {
			return deliveryadmission.ExecutionResult{}, false,
				deliveryadmission.ErrExecutionResultUnavailable
		}
		return deliveryadmission.ExecutionResult{}, false, nil
	}
	if !found ||
		anchored.ResultRef != state.ProductiveExecutionResultRef ||
		anchored.CommandRef != replayed.CommandRef ||
		anchored.Execution != replayed {
		return deliveryadmission.ExecutionResult{}, false,
			fmt.Errorf(
				"%w: PAD terminal result differs from WorkRun authority",
				deliveryadmission.ErrExecutionResultCorrupt,
			)
	}
	// WorkRun is the semantic source. PAD/use replay is consulted only as an
	// exact byte-for-byte corroboration of that independently anchored fact.
	return anchored.Execution, true, nil
}

func validateOwnerBoundDeliveryState(
	state workrun.WorkRunState,
	recoveryOnly bool,
) error {
	if state.ProductiveBlockerRef != "" && !recoveryOnly {
		return fmt.Errorf(
			"%w: productive blocker fences bound delivery execution",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	if state.DeliveryAuthorizationRef == "" {
		return fmt.Errorf(
			"%w: WorkRun has no bound delivery authorization",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	if !recoveryOnly && state.ProductiveExecutionResultRef != "" {
		return fmt.Errorf(
			"%w: productive execution result is already anchored",
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
