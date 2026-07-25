package workprovider

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func (runtimeOutcome *productiveRuntimeOutcome) ReconcileOutcome(
	ctx context.Context,
	workRunID string,
	expectedRevision string,
	diagnosticRef string,
) (workrun.WorkReconcileV1, error) {
	if !workRunIDPattern.MatchString(workRunID) ||
		!revisionPattern.MatchString(expectedRevision) ||
		!revisionPattern.MatchString(diagnosticRef) {
		return workrun.WorkReconcileV1{}, errors.New(
			"productive reconciliation requires exact WorkRun authority",
		)
	}
	if _, err := runtimeOutcome.bindWorkRunAgent(
		ctx,
		workRunID,
	); err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	factory, err := NewProductionOwnerCoordinatorFactory(
		ctx,
		runtimeOutcome.repo,
		runtimeOutcome.agent,
		runtimeOutcome.activation,
	)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	store, err := existingProductiveAdvanceStore(factory.lease, workRunID)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	store.pad = factory.pad
	if err := store.validate(ctx); err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	if _, err := store.startAuthority(ctx); err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	advanceLock, err := acquireProductiveAdvanceOwnerLock(ctx, store)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	defer advanceLock.Release()
	if _, err := store.startAuthority(ctx); err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	coordinator, err := factory.openForProductiveRecovery(ctx, workRunID)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	state, err := coordinator.Status(ctx)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	status, err := coordinator.work.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	if replay, ok, err := store.reconcileResult(
		ctx,
		expectedRevision,
		diagnosticRef,
		state,
		status,
	); err != nil {
		return workrun.WorkReconcileV1{}, err
	} else if ok {
		return replay, nil
	}
	if state.ProductiveReconciliationRef != "" {
		if state.ProductiveBlockerRef != diagnosticRef ||
			state.ProductiveReconciliationSourceRevision != expectedRevision {
			return workrun.WorkReconcileV1{}, fmt.Errorf(
				"%w: productive reconciliation CAS does not match",
				workrun.ErrWorkRunRevisionConflict,
			)
		}
		return finishProductiveReconciliation(
			ctx,
			store,
			state,
			status,
			expectedRevision,
			diagnosticRef,
		)
	}
	if state.Revision != expectedRevision {
		return workrun.WorkReconcileV1{}, ownerRevisionConflict(
			expectedRevision,
			state.Revision,
		)
	}
	if state.ProductiveBlockerRef != diagnosticRef ||
		state.DeliveryAuthorizationRef == "" ||
		state.DeliveryResultRef != "" {
		return workrun.WorkReconcileV1{}, fmt.Errorf(
			"%w: WorkRun is not eligible for productive reconciliation",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	blockedState := state
	blockedStatus := status
	resolved, err := store.resolveDiagnosticForState(
		ctx,
		diagnosticRef,
		state,
	)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	if resolved.NextAction != workrun.WorkAdvanceNextActionReconcile {
		return workrun.WorkReconcileV1{}, fmt.Errorf(
			"%w: diagnostic does not authorize reconciliation",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	blockedAdvance, err := ensureBlockedProductiveAdvanceResult(
		ctx,
		store,
		blockedState,
		blockedStatus,
		diagnosticRef,
	)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}

	execution, found, recoveryErr := coordinator.RecoverBoundDelivery(ctx)
	if errors.Is(recoveryErr, context.Canceled) ||
		errors.Is(recoveryErr, context.DeadlineExceeded) {
		return workrun.WorkReconcileV1{}, recoveryErr
	}
	if errors.Is(recoveryErr, ErrPADDeliveryResultCorrupt) ||
		errors.Is(recoveryErr, deliveryadmission.ErrExecutionResultCorrupt) {
		// Never record a reconciliation outcome from corrupt terminal
		// authority. The WorkRun and its delivery fence remain byte-for-byte
		// unchanged for operator repair.
		return workrun.WorkReconcileV1{}, recoveryErr
	}
	if state.ProductiveExecutionResultRef != "" && recoveryErr != nil {
		// Once WorkRun has anchored an exact terminal result, recovery errors
		// mean its PAD/use corroboration is unavailable. They must not be
		// normalized into a fresh manual outcome: doing so would contradict
		// the independently anchored succeeded/failed/indeterminate fact.
		return workrun.WorkReconcileV1{}, recoveryErr
	}
	outcome := workrun.WorkReconcileManualResolution
	deliveryResultRef := ""
	switch {
	case recoveryErr != nil:
		// Owner evidence is still unavailable or inconclusive. Persist a manual
		// terminal action rather than inviting another automatic reconciliation.
	case !found:
		outcome = workrun.WorkReconcileNoDeliveryConfirmed
	case execution.Outcome == deliveryadmission.ExecutionSucceeded:
		deliveryResultRef, err = store.publishDeliveryResult(
			ctx,
			state,
			execution,
		)
		if err != nil {
			return workrun.WorkReconcileV1{}, err
		}
		outcome = workrun.WorkReconcileDeliveryConfirmed
	case execution.Outcome == deliveryadmission.ExecutionFailed:
		outcome = workrun.WorkReconcileNoDeliveryConfirmed
	case execution.Outcome == deliveryadmission.ExecutionIndeterminate:
	default:
		return workrun.WorkReconcileV1{}, errors.New(
			"PAD reconciliation returned an unsupported execution outcome",
		)
	}
	// Close the delete/crash gap immediately before publishing the immutable
	// reconciliation authority. The exact blocked result comes only from the
	// still-blocked pre-state, never from the later live projection.
	durableBlockedAdvance, err := ensureBlockedProductiveAdvanceResult(
		ctx,
		store,
		blockedState,
		blockedStatus,
		diagnosticRef,
	)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	if !reflect.DeepEqual(durableBlockedAdvance, blockedAdvance) {
		return workrun.WorkReconcileV1{}, errors.New(
			"historical productive advance changed during reconciliation",
		)
	}
	reconciliationRef, _, err := store.publishReconciliation(
		ctx,
		state,
		diagnosticRef,
		durableBlockedAdvance,
		outcome,
		deliveryResultRef,
	)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	state, err = coordinator.recordProductiveReconciliation(
		ctx,
		expectedRevision,
		diagnosticRef,
		reconciliationRef,
	)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	status, err = coordinator.work.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	return finishProductiveReconciliation(
		ctx,
		store,
		state,
		status,
		expectedRevision,
		diagnosticRef,
	)
}

func ensureBlockedProductiveAdvanceResult(
	ctx context.Context,
	store productiveAdvanceStore,
	state workrun.WorkRunState,
	status workrun.WorkStatusV1,
	diagnosticRef string,
) (workrun.WorkAdvanceV1, error) {
	if state.ProductiveReconciliationRef != "" ||
		state.DeliveryResultRef != "" ||
		state.ProductiveBlockerRef != diagnosticRef ||
		status.WorkRunID != state.WorkRunID ||
		status.Revision != state.Revision ||
		status.PublicState != workrun.PublicStateNeedsYourDecision ||
		status.AuthorizedTransition != nil {
		return workrun.WorkAdvanceV1{}, errors.New(
			"historical productive advance is not a blocked pre-reconciliation state",
		)
	}
	resolved, err := store.resolveDiagnosticForState(
		ctx,
		diagnosticRef,
		state,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	sourceRevision := resolved.AdvanceExpectedRevision
	if !revisionPattern.MatchString(sourceRevision) ||
		state.ProductiveAdvanceSourceRevision != sourceRevision {
		return workrun.WorkAdvanceV1{}, errors.New(
			"productive diagnostic has no WorkRun-anchored advance source",
		)
	}
	diagnostic := workrun.WorkAdvanceDiagnosticV1{
		Ref:        diagnosticRef,
		Code:       resolved.Code,
		Message:    resolved.Message,
		NextAction: resolved.NextAction,
	}
	if err := diagnostic.Validate(); err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if status.Diagnostic == nil ||
		*status.Diagnostic != diagnostic {
		return workrun.WorkAdvanceV1{}, errors.New(
			"blocked public status does not carry the exact productive diagnostic",
		)
	}
	if replayed, ok, err := store.result(
		ctx,
		sourceRevision,
		state,
		status,
	); err != nil {
		return workrun.WorkAdvanceV1{}, err
	} else if ok {
		return replayed, nil
	}
	result := workrun.WorkAdvanceV1{
		Schema:           workrun.WorkAdvanceContractV1,
		Contract:         workrun.WorkAdvanceContractV1,
		PreviousRevision: sourceRevision,
		Status:           status,
		Diagnostic:       &diagnostic,
	}
	if err := result.Validate(); err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if err := store.publishResult(ctx, result); err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	replayed, ok, err := store.result(
		ctx,
		sourceRevision,
		state,
		status,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if !ok || !reflect.DeepEqual(replayed, result) {
		return workrun.WorkAdvanceV1{}, errors.New(
			"historical productive advance durability check failed",
		)
	}
	return replayed, nil
}

func finishProductiveReconciliation(
	ctx context.Context,
	store productiveAdvanceStore,
	state workrun.WorkRunState,
	status workrun.WorkStatusV1,
	expectedRevision string,
	diagnosticRef string,
) (workrun.WorkReconcileV1, error) {
	if state.ProductiveBlockerRef != diagnosticRef ||
		state.ProductiveReconciliationRef == "" ||
		state.ProductiveReconciliationSourceRevision != expectedRevision {
		return workrun.WorkReconcileV1{}, ErrProviderResultMismatch
	}
	result := workrun.WorkReconcileV1{
		Schema:            workrun.WorkReconcileContractV1,
		Contract:          workrun.WorkReconcileContractV1,
		PreviousRevision:  expectedRevision,
		DiagnosticRef:     diagnosticRef,
		ReconciliationRef: state.ProductiveReconciliationRef,
		Outcome:           state.ProductiveReconciliationOutcome,
		Status:            status,
		DeliveryResultRef: state.DeliveryResultRef,
	}
	if err := result.Validate(); err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	if err := store.publishReconcileResult(ctx, result); err != nil {
		return workrun.WorkReconcileV1{}, err
	}
	return result, nil
}

var _ RuntimeOutcome = (*productiveRuntimeOutcome)(nil)
