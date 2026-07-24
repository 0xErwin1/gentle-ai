package workprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

var errProductiveAdvanceRecoveryUnavailable = errors.New(
	"productive advance has no terminal recovery to reconcile",
)

func resolveProductiveStartAuthority(
	ctx context.Context,
	coordinator *OwnerCoordinator,
	authority productiveStartAuthority,
) (
	admitted deliveryadmission.AdmittedDecision,
	err error,
) {
	opened, err := coordinator.pad.openRepository(ctx)
	if err != nil {
		return deliveryadmission.AdmittedDecision{}, err
	}
	repository, ok := opened.(ownerPADDecisionRepository)
	if !ok {
		_ = opened.Close()
		return deliveryadmission.AdmittedDecision{}, errors.New(
			"PAD owner repository cannot resolve productive start authority",
		)
	}
	defer func() {
		if closeErr := repository.Close(); closeErr != nil {
			admitted = deliveryadmission.AdmittedDecision{}
			err = errors.Join(err, closeErr)
		}
	}()
	admitted, err = repository.ResolveAdmittedDecision(
		ctx,
		authority.DeliveryIntentRef,
	)
	if err != nil {
		return deliveryadmission.AdmittedDecision{}, err
	}
	scopeDigest, digestErr := outcomeScopeDigest(
		authority.RepositoryRef,
		authority.Outcome,
		authority.ScopeSelectors,
	)
	if digestErr != nil {
		return deliveryadmission.AdmittedDecision{}, errors.New(
			"productive start authority scope is corrupt",
		)
	}
	decision := admitted.Decision
	if decision.Disposition != deliveryadmission.AdmissionAdmitted ||
		decision.IntentRef != authority.DeliveryIntentRef ||
		decision.Intent.ScopeDigest != authority.ScopeDigest ||
		decision.Intent.ScopeDigest != scopeDigest ||
		decision.Destination.RepositoryRef != authority.RepositoryRef ||
		decision.Destination.ObservedRevision != authority.BaseRevision {
		return deliveryadmission.AdmittedDecision{}, errors.New(
			"productive start authority does not bind the admitted PAD intent",
		)
	}
	return admitted, nil
}

func observeProductiveCommittedCandidate(
	ctx context.Context,
	factory *ProductionOwnerCoordinatorFactory,
	authority productiveStartAuthority,
) (
	reviewtransaction.Target,
	reviewtransaction.Snapshot,
	workrun.WorkAdvanceDiagnosticCode,
	error,
) {
	target := reviewtransaction.Target{
		Kind:              reviewtransaction.TargetBaseDiff,
		BaseRef:           strings.TrimPrefix(authority.BaseRevision, "git:"),
		IntendedUntracked: []string{},
	}
	if !validPADGitRevision(authority.BaseRevision) {
		return target, reviewtransaction.Snapshot{},
			workrun.WorkAdvanceDiagnosticCandidateBaseNotGit,
			nil
	}
	builder := reviewtransaction.SnapshotBuilder{
		Repo: factory.repositoryRoot(),
	}
	dirtyTracked, err := builder.HasDirtyTrackedChanges(ctx)
	if err != nil {
		blocker, propagated := productiveGitObservationFailure(
			ctx,
			err,
			"",
		)
		return target, reviewtransaction.Snapshot{}, blocker, propagated
	}
	untracked, err := builder.DiscoverIntendedUntracked(ctx)
	if err != nil {
		blocker, propagated := productiveGitObservationFailure(
			ctx,
			err,
			"",
		)
		return target, reviewtransaction.Snapshot{}, blocker, propagated
	}
	if dirtyTracked || len(untracked) != 0 {
		return target, reviewtransaction.Snapshot{},
			workrun.WorkAdvanceDiagnosticCandidateNotCommitted,
			nil
	}
	if factory.candidateStore == nil ||
		factory.candidateStore.objects == nil {
		return target, reviewtransaction.Snapshot{},
			workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable,
			nil
	}
	headRevision, err := factory.candidateStore.objects.ResolveHeadRevision(
		ctx,
		authority.RepositoryRef,
	)
	if err != nil {
		blocker, propagated := productiveGitObservationFailure(
			ctx,
			err,
			workrun.WorkAdvanceDiagnosticCandidateChanged,
		)
		return target, reviewtransaction.Snapshot{}, blocker, propagated
	}
	if headRevision == authority.BaseRevision {
		return target, reviewtransaction.Snapshot{},
			workrun.WorkAdvanceDiagnosticNoMutation,
			nil
	}
	ancestry, err := factory.candidateStore.objects.ObserveAncestry(
		ctx,
		authority.RepositoryRef,
		authority.BaseRevision,
		headRevision,
	)
	if err != nil {
		blocker, propagated := productiveGitObservationFailure(
			ctx,
			err,
			workrun.WorkAdvanceDiagnosticCandidateChanged,
		)
		return target, reviewtransaction.Snapshot{}, blocker, propagated
	}
	switch ancestry.State {
	case PADGitAncestor:
	case PADGitNotAncestor:
		return target, reviewtransaction.Snapshot{},
			workrun.WorkAdvanceDiagnosticCandidateWrongBase,
			nil
	default:
		return target, reviewtransaction.Snapshot{},
			workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable,
			nil
	}
	candidate, err := builder.Build(ctx, target)
	if err != nil {
		blocker, propagated := productiveGitObservationFailure(
			ctx,
			err,
			"",
		)
		return target, reviewtransaction.Snapshot{}, blocker, propagated
	}
	if len(candidate.Paths) == 0 {
		return target, reviewtransaction.Snapshot{},
			workrun.WorkAdvanceDiagnosticNoMutation,
			nil
	}
	if !equalProductiveStrings(
		candidate.Paths,
		authority.ScopeSelectors,
	) {
		return target, reviewtransaction.Snapshot{},
			workrun.WorkAdvanceDiagnosticScopeMismatch,
			nil
	}
	if err := factory.candidateStore.objects.RequireCommitTree(
		ctx,
		authority.RepositoryRef,
		headRevision,
		candidate.CandidateTree,
	); err != nil {
		blocker, propagated := productiveGitObservationFailure(
			ctx,
			err,
			workrun.WorkAdvanceDiagnosticCandidateChanged,
		)
		return target, reviewtransaction.Snapshot{}, blocker, propagated
	}
	return target, candidate, "", nil
}

func productiveGitObservationFailure(
	ctx context.Context,
	err error,
	objectMismatch workrun.WorkAdvanceDiagnosticCode,
) (workrun.WorkAdvanceDiagnosticCode, error) {
	if err == nil {
		return "", nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if objectMismatch != "" &&
		errors.Is(err, ErrPADGitObjectMismatch) {
		return objectMismatch, nil
	}
	return workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable, nil
}

func recheckProductiveCommittedCandidate(
	ctx context.Context,
	factory *ProductionOwnerCoordinatorFactory,
	authority productiveStartAuthority,
	expected reviewtransaction.Snapshot,
) (
	reviewtransaction.Snapshot,
	workrun.WorkAdvanceDiagnosticCode,
	error,
) {
	_, observed, blocker, err := observeProductiveCommittedCandidate(
		ctx,
		factory,
		authority,
	)
	if err != nil || blocker != "" {
		return reviewtransaction.Snapshot{}, blocker, err
	}
	if observed.Identity != expected.Identity ||
		observed.CandidateTree != expected.CandidateTree ||
		!equalProductiveStrings(observed.Paths, expected.Paths) {
		return reviewtransaction.Snapshot{},
			workrun.WorkAdvanceDiagnosticCandidateChanged,
			nil
	}
	return observed, "", nil
}

func (runtimeOutcome *productiveRuntimeOutcome) AdvanceOutcome(
	ctx context.Context,
	workRunID string,
	expectedRevision string,
) (workrun.WorkAdvanceV1, error) {
	if !workRunIDPattern.MatchString(workRunID) ||
		!revisionPattern.MatchString(expectedRevision) {
		return workrun.WorkAdvanceV1{}, errors.New(
			"productive advance requires exact WorkRun authority",
		)
	}
	capabilities, err := runtimeOutcome.Capabilities(ctx)
	if err != nil {
		return runtimeOutcome.failProductiveAdvance(
			ctx,
			workRunID,
			expectedRevision,
			err,
		)
	}
	if capabilities.WorkRouting.Exposure != WorkRoutingAdvertised ||
		runtimeOutcome.connector == nil {
		recoveryFactory, recoveryErr := NewProductionOwnerCoordinatorFactory(
			ctx,
			runtimeOutcome.repo,
			runtimeOutcome.agent,
			runtimeOutcome.activation,
		)
		if recoveryErr != nil {
			return workrun.WorkAdvanceV1{}, recoveryErr
		}
		recovered, recoveryErr := recoverProductiveWork(
			ctx,
			recoveryFactory,
			workRunID,
			expectedRevision,
		)
		if recoveryErr == nil {
			return recovered, nil
		}
		if !errors.Is(
			recoveryErr,
			errProductiveAdvanceRecoveryUnavailable,
		) {
			return workrun.WorkAdvanceV1{}, recoveryErr
		}
		return runtimeOutcome.failProductiveAdvance(
			ctx,
			workRunID,
			expectedRevision,
			ErrCapabilityReadOnly,
		)
	}
	factory, err := NewProductiveOwnerCoordinatorFactory(
		ctx,
		runtimeOutcome.repo,
		runtimeOutcome.agent,
		runtimeOutcome.activation,
		runtimeOutcome.connector,
	)
	if err != nil {
		return runtimeOutcome.failProductiveAdvance(
			ctx,
			workRunID,
			expectedRevision,
			err,
		)
	}
	provisioner, err := NewProductivePolicyProvisioner(
		ctx,
		factory.padAuthority,
	)
	if err != nil {
		return runtimeOutcome.failProductiveAdvance(
			ctx,
			workRunID,
			expectedRevision,
			err,
		)
	}
	snapshot, err := runtimeOutcome.connector.ResolvePolicySnapshot(ctx)
	if err != nil {
		return runtimeOutcome.failProductiveAdvance(
			ctx,
			workRunID,
			expectedRevision,
			err,
		)
	}
	if err := provisioner.Provision(ctx, snapshot); err != nil {
		return runtimeOutcome.failProductiveAdvance(
			ctx,
			workRunID,
			expectedRevision,
			err,
		)
	}
	return advanceProductiveWork(
		ctx,
		factory,
		workRunID,
		expectedRevision,
	)
}

func (runtimeOutcome *productiveRuntimeOutcome) failProductiveAdvance(
	ctx context.Context,
	workRunID string,
	expectedRevision string,
	cause error,
) (workrun.WorkAdvanceV1, error) {
	if errors.Is(cause, context.Canceled) ||
		errors.Is(cause, context.DeadlineExceeded) {
		return workrun.WorkAdvanceV1{}, cause
	}
	factory, err := NewProductionOwnerCoordinatorFactory(
		ctx,
		runtimeOutcome.repo,
		runtimeOutcome.agent,
		runtimeOutcome.activation,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, cause
	}
	store, err := existingProductiveAdvanceStore(factory.lease, workRunID)
	if err != nil {
		return workrun.WorkAdvanceV1{}, cause
	}
	store.pad = factory.pad
	if err := store.validate(ctx); err != nil {
		if os.IsNotExist(err) {
			return workrun.WorkAdvanceV1{}, cause
		}
		return workrun.WorkAdvanceV1{}, err
	}
	if _, err := store.startAuthority(ctx); err != nil {
		if os.IsNotExist(err) {
			return workrun.WorkAdvanceV1{}, cause
		}
		return workrun.WorkAdvanceV1{}, err
	}
	advanceLock, err := acquireProductiveAdvanceOwnerLock(ctx, store)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	defer advanceLock.Release()
	if _, err := store.startAuthority(ctx); err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	coordinator, err := factory.openForProductiveRecovery(ctx, workRunID)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	state, err := coordinator.Status(ctx)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	status, err := coordinator.work.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if replay, ok, err := store.result(
		ctx,
		expectedRevision,
		state,
		status,
	); err != nil {
		return workrun.WorkAdvanceV1{}, err
	} else if ok {
		return replay, nil
	}
	if err := gateProductiveAdvanceAttempt(
		ctx,
		store,
		state,
		expectedRevision,
	); err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if state.DeliveryResultRef != "" {
		return finishProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state.DeliveryResultRef,
			"",
		)
	}
	if state.ProductiveBlockerRef != "" {
		return finishProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			"",
			state.ProductiveBlockerRef,
		)
	}
	return blockRecoveredProductiveAdvance(
		ctx,
		store,
		coordinator,
		expectedRevision,
		state,
		workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable,
	)
}

func recoverProductiveWork(
	ctx context.Context,
	factory *ProductionOwnerCoordinatorFactory,
	workRunID string,
	expectedRevision string,
) (workrun.WorkAdvanceV1, error) {
	store, err := existingProductiveAdvanceStore(factory.lease, workRunID)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	store.pad = factory.pad
	if err := store.validate(ctx); err != nil {
		if os.IsNotExist(err) {
			return workrun.WorkAdvanceV1{},
				errProductiveAdvanceRecoveryUnavailable
		}
		return workrun.WorkAdvanceV1{}, err
	}
	if _, err := store.startAuthority(ctx); err != nil {
		if os.IsNotExist(err) {
			return workrun.WorkAdvanceV1{},
				errProductiveAdvanceRecoveryUnavailable
		}
		return workrun.WorkAdvanceV1{}, err
	}
	advanceLock, err := acquireProductiveAdvanceOwnerLock(ctx, store)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	defer advanceLock.Release()
	if _, err := store.startAuthority(ctx); err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	coordinator, err := factory.openForProductiveRecovery(ctx, workRunID)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	state, err := coordinator.Status(ctx)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	status, err := coordinator.work.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if replay, ok, err := store.result(
		ctx,
		expectedRevision,
		state,
		status,
	); err != nil {
		return workrun.WorkAdvanceV1{}, err
	} else if ok {
		return replay, nil
	}
	if err := gateProductiveAdvanceAttempt(
		ctx,
		store,
		state,
		expectedRevision,
	); err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if state.DeliveryResultRef != "" {
		return finishProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state.DeliveryResultRef,
			"",
		)
	}
	if state.ProductiveBlockerRef != "" {
		return finishProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			"",
			state.ProductiveBlockerRef,
		)
	}
	if state.DeliveryAuthorizationRef == "" {
		return workrun.WorkAdvanceV1{},
			errProductiveAdvanceRecoveryUnavailable
	}
	execution, found, recoveryErr := coordinator.RecoverBoundDelivery(ctx)
	if errors.Is(
		recoveryErr,
		deliveryadmission.ErrExecutionResultUnavailable,
	) {
		return blockRecoveredProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			workrun.WorkAdvanceDiagnosticDeliveryOutcomeIndeterminate,
		)
	}
	if recoveryErr != nil || !found {
		return blockRecoveredProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable,
		)
	}
	switch execution.Outcome {
	case deliveryadmission.ExecutionSucceeded:
		resultRef, err := store.publishDeliveryResult(ctx, state, execution)
		if err != nil {
			return workrun.WorkAdvanceV1{}, err
		}
		bound, err := coordinator.bindRecoveredDeliveryResult(
			ctx,
			state.Revision,
			resultRef,
		)
		if err != nil {
			return workrun.WorkAdvanceV1{}, err
		}
		if bound.DeliveryResultRef != resultRef {
			return workrun.WorkAdvanceV1{}, ErrProviderResultMismatch
		}
		return finishProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			resultRef,
			"",
		)
	case deliveryadmission.ExecutionFailed:
		return blockRecoveredProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			workrun.WorkAdvanceDiagnosticDeliveryEffectFailed,
		)
	default:
		return blockRecoveredProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			workrun.WorkAdvanceDiagnosticDeliveryOutcomeIndeterminate,
		)
	}
}

func advanceProductiveWork(
	ctx context.Context,
	factory *ProductionOwnerCoordinatorFactory,
	workRunID string,
	expectedRevision string,
) (workrun.WorkAdvanceV1, error) {
	store, err := existingProductiveAdvanceStore(factory.lease, workRunID)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	store.pad = factory.pad
	if err := store.validate(ctx); err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	authority, err := store.startAuthority(ctx)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	advanceLock, err := acquireProductiveAdvanceOwnerLock(ctx, store)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	defer advanceLock.Release()
	authority, err = store.startAuthority(ctx)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	coordinator, err := factory.Open(ctx, workRunID)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	state, err := coordinator.Status(ctx)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if state.DeliveryIntentRef != authority.DeliveryIntentRef {
		return workrun.WorkAdvanceV1{}, ErrProviderResultMismatch
	}
	publicStatus, err := coordinator.work.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if replay, ok, err := store.result(
		ctx,
		expectedRevision,
		state,
		publicStatus,
	); err != nil {
		return workrun.WorkAdvanceV1{}, err
	} else if ok {
		return replay, nil
	}
	admitted, err := resolveProductiveStartAuthority(
		ctx,
		coordinator,
		authority,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if err := gateProductiveAdvanceAttempt(
		ctx,
		store,
		state,
		expectedRevision,
	); err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if state.ProductiveBlockerRef != "" {
		return finishProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			"",
			state.ProductiveBlockerRef,
		)
	}

	target, candidate, blockerCode, err :=
		observeProductiveCommittedCandidate(
			ctx,
			factory,
			authority,
		)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if blockerCode != "" {
		return blockProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			blockerCode,
		)
	}
	if state.Handoff == nil {
		state, err = coordinator.BindImplementationHandoff(
			ctx,
			OwnerBindHandoffRequest{
				ExpectedRevision:       state.Revision,
				Target:                 target,
				IntendedPaths:          authority.ScopeSelectors,
				DeclaredObligationRefs: []string{},
				EvidenceRefs:           []string{},
			},
		)
		if err != nil {
			return workrun.WorkAdvanceV1{}, err
		}
	}
	candidate, blockerCode, err = recheckProductiveCommittedCandidate(
		ctx,
		factory,
		authority,
		candidate,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if blockerCode != "" {
		return blockProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			blockerCode,
		)
	}
	if state.DeliveryAuthorizationRef != "" {
		resultRef, deliveryBlocker, err := executeAndBindProductiveDelivery(
			ctx,
			store,
			coordinator,
			state,
		)
		if err != nil {
			return workrun.WorkAdvanceV1{}, err
		}
		if deliveryBlocker != "" {
			return blockProductiveAdvance(
				ctx,
				store,
				coordinator,
				expectedRevision,
				state,
				deliveryBlocker,
			)
		}
		return finishProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			resultRef,
			"",
		)
	}

	builder := reviewtransaction.SnapshotBuilder{Repo: factory.repositoryRoot()}
	if state.Handoff == nil ||
		state.Handoff.CandidateRef != candidate.Identity {
		return workrun.WorkAdvanceV1{}, ErrProviderResultMismatch
	}
	policyHash := admitted.Decision.PolicyRef
	registry, err := reviewtransaction.NewVerificationPlanRegistry(
		policyHash,
		[]string{},
		[]reviewtransaction.VerificationObligation{},
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	applicability, err := builder.ClassifyVerificationApplicability(
		ctx,
		candidate,
		registry,
		[]string{},
	)
	if err != nil {
		return blockProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			workrun.WorkAdvanceDiagnosticVerificationApplicabilityUnknown,
		)
	}
	if applicability.Decision !=
		reviewtransaction.VerificationNotApplicable {
		return blockProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			workrun.WorkAdvanceDiagnosticCompleteVerificationPlanRequired,
		)
	}
	assessment, err := builder.AssessSnapshotRisk(ctx, candidate)
	if err != nil {
		return blockProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			workrun.WorkAdvanceDiagnosticReviewRiskUnknown,
		)
	}
	lenses, err := reviewtransaction.SelectReviewLenses(
		assessment,
		"reliability",
	)
	if err != nil {
		return blockProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			workrun.WorkAdvanceDiagnosticReviewLensesUnknown,
		)
	}
	if assessment.Level != reviewtransaction.RiskLow ||
		len(lenses) != 0 {
		return blockProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			workrun.WorkAdvanceDiagnosticCompleteReviewRequired,
		)
	}
	plan, err := reviewtransaction.BuildVerificationPlan(
		applicability,
		registry,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	planAuthority, err := coordinator.PublishVerificationPlan(
		ctx,
		reviewtransaction.RARPlanPublication{
			Snapshot:      candidate,
			Applicability: applicability,
			Registry:      registry,
			Plan:          plan,
		},
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if state.Forecast == nil {
		state, err = coordinator.PublishVerificationForecast(
			ctx,
			OwnerForecastRequest{
				ExpectedRevision: state.Revision,
				Handoff:          *state.Handoff,
				PlanRevisionRef:  planAuthority.AuthorityRef,
				Availability:     workrun.ForecastNotRequired,
				DiagnosticRefs:   []string{},
			},
		)
		if err != nil {
			return workrun.WorkAdvanceV1{}, err
		}
	}

	lineageID := productiveAdvanceLineage(factory.RepositoryRef(), workRunID)
	receiptRef, resultRef, err := publishNativePassiveRAR(
		ctx,
		coordinator,
		factory.repositoryRoot(),
		lineageID,
		candidate,
		applicability,
		registry,
		plan,
		assessment,
		lenses,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if state.VerificationResultRef == "" {
		state, err = coordinator.BindVerificationResult(
			ctx,
			OwnerBindResultRequest{
				ExpectedRevision: state.Revision,
				ResultRef:        resultRef,
			},
		)
		if err != nil {
			return workrun.WorkAdvanceV1{}, err
		}
	}
	if state.ReviewReceiptRef == "" {
		state, err = coordinator.BindReviewReceipt(
			ctx,
			OwnerBindReviewRequest{
				ExpectedRevision: state.Revision,
				ReviewReceiptRef: receiptRef,
			},
		)
		if err != nil {
			return workrun.WorkAdvanceV1{}, err
		}
	}
	candidate, blockerCode, err = recheckProductiveCommittedCandidate(
		ctx,
		factory,
		authority,
		candidate,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if blockerCode != "" {
		return blockProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			blockerCode,
		)
	}
	decisionRef, ok, err := store.decision(ctx, expectedRevision)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if !ok {
		decisionRef, err = coordinator.DeriveAndPublishDeliveryDecision(ctx)
		if err != nil {
			return blockProductiveAdvance(
				ctx,
				store,
				coordinator,
				expectedRevision,
				state,
				productiveDeliveryDecisionBlocker(err),
			)
		}
		if err := store.publishDecision(
			ctx,
			expectedRevision,
			decisionRef,
		); err != nil {
			return workrun.WorkAdvanceV1{}, err
		}
	}
	state, err = coordinator.IssueAndBindDeliveryAuthorization(
		ctx,
		OwnerIssueDeliveryAuthorizationRequest{
			ExpectedRevision: state.Revision,
			DecisionRef:      decisionRef,
		},
	)
	if err != nil {
		return blockProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			productiveDeliveryAuthorizationBlocker(err),
		)
	}
	_, blockerCode, err = recheckProductiveCommittedCandidate(
		ctx,
		factory,
		authority,
		candidate,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if blockerCode != "" {
		return blockProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			blockerCode,
		)
	}
	deliveryResultRef, deliveryBlocker, err := executeAndBindProductiveDelivery(
		ctx,
		store,
		coordinator,
		state,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if deliveryBlocker != "" {
		return blockProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state,
			deliveryBlocker,
		)
	}
	return finishProductiveAdvance(
		ctx,
		store,
		coordinator,
		expectedRevision,
		deliveryResultRef,
		"",
	)
}

func gateProductiveAdvanceAttempt(
	ctx context.Context,
	store productiveAdvanceStore,
	state workrun.WorkRunState,
	expectedRevision string,
) error {
	attempted, err := store.attemptExists(ctx, expectedRevision)
	if err != nil {
		return err
	}
	if attempted {
		return nil
	}
	if state.ProductiveBlockerRef != "" ||
		state.DeliveryResultRef != "" {
		return fmt.Errorf(
			"%w: work advance is already terminal",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	if state.Revision != expectedRevision {
		return ownerRevisionConflict(expectedRevision, state.Revision)
	}
	return store.beginAttempt(ctx, expectedRevision)
}

func executeAndBindProductiveDelivery(
	ctx context.Context,
	store productiveAdvanceStore,
	coordinator *OwnerCoordinator,
	state workrun.WorkRunState,
) (string, workrun.WorkAdvanceDiagnosticCode, error) {
	if state.DeliveryResultRef != "" {
		return state.DeliveryResultRef, "", nil
	}
	execution, err := coordinator.ExecuteBoundDelivery(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return "", "", err
		}
		return "", productiveDeliveryExecutionBlocker(err), nil
	}
	switch execution.Outcome {
	case deliveryadmission.ExecutionFailed:
		return "", workrun.WorkAdvanceDiagnosticDeliveryEffectFailed, nil
	case deliveryadmission.ExecutionIndeterminate:
		return "",
			workrun.WorkAdvanceDiagnosticDeliveryOutcomeIndeterminate,
			nil
	case deliveryadmission.ExecutionSucceeded:
	default:
		return "", "", errors.New(
			"PAD execution returned an unsupported terminal outcome",
		)
	}
	resultRef, err := store.publishDeliveryResult(ctx, state, execution)
	if err != nil {
		return "", "", err
	}
	bound, err := coordinator.BindDeliveryResult(
		ctx,
		state.Revision,
		resultRef,
	)
	if err != nil {
		return "", "", err
	}
	if bound.DeliveryResultRef != resultRef {
		return "", "", ErrProviderResultMismatch
	}
	return resultRef, "", nil
}

func productiveDeliveryDecisionBlocker(
	err error,
) workrun.WorkAdvanceDiagnosticCode {
	if errors.Is(err, deliveryadmission.ErrLiveProbeRejected) ||
		errors.Is(err, ErrPADDeliveryConflict) {
		return workrun.WorkAdvanceDiagnosticDeliveryConflict
	}
	if errors.Is(err, deliveryadmission.ErrUnauthorized) {
		return workrun.WorkAdvanceDiagnosticDeliveryDecisionUnavailable
	}
	return workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable
}

func productiveDeliveryAuthorizationBlocker(
	err error,
) workrun.WorkAdvanceDiagnosticCode {
	if errors.Is(err, deliveryadmission.ErrExpired) {
		return workrun.WorkAdvanceDiagnosticDeliveryAuthorizationExpired
	}
	if errors.Is(err, deliveryadmission.ErrUnauthorized) ||
		errors.Is(err, deliveryadmission.ErrBindingMismatch) {
		return workrun.WorkAdvanceDiagnosticDeliveryAuthorizationUnavailable
	}
	return workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable
}

func productiveDeliveryExecutionBlocker(
	err error,
) workrun.WorkAdvanceDiagnosticCode {
	switch {
	case errors.Is(err, ErrPADDeliveryIndeterminate),
		errors.Is(err, deliveryadmission.ErrExecutionResultUnavailable),
		errors.Is(err, deliveryadmission.ErrExecutionRejected):
		return workrun.WorkAdvanceDiagnosticDeliveryOutcomeIndeterminate
	case errors.Is(err, deliveryadmission.ErrExpired):
		return workrun.WorkAdvanceDiagnosticDeliveryAuthorizationExpired
	case errors.Is(err, ErrPADDeliveryConflict),
		errors.Is(err, deliveryadmission.ErrLiveProbeRejected):
		return workrun.WorkAdvanceDiagnosticDeliveryConflict
	case errors.Is(err, deliveryadmission.ErrUnauthorized),
		errors.Is(err, deliveryadmission.ErrBindingMismatch):
		return workrun.WorkAdvanceDiagnosticDeliveryAuthorizationUnavailable
	default:
		return workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable
	}
}

func (coordinator *OwnerCoordinator) productiveVerificationPolicyHash(
	ctx context.Context,
) (policyHash string, err error) {
	state, err := coordinator.work.Status()
	if err != nil {
		return "", err
	}
	opened, err := coordinator.pad.openRepository(ctx)
	if err != nil {
		return "", err
	}
	repository, ok := opened.(interface {
		ResolveAdmittedDecision(
			context.Context,
			string,
		) (deliveryadmission.AdmittedDecision, error)
	})
	if !ok {
		_ = opened.Close()
		return "", errors.New(
			"PAD owner repository cannot resolve admitted policy",
		)
	}
	defer func() {
		if closeErr := opened.Close(); closeErr != nil {
			policyHash = ""
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
	return admitted.Decision.PolicyRef, nil
}

func blockProductiveAdvance(
	ctx context.Context,
	store productiveAdvanceStore,
	coordinator *OwnerCoordinator,
	expectedRevision string,
	state workrun.WorkRunState,
	code workrun.WorkAdvanceDiagnosticCode,
) (workrun.WorkAdvanceV1, error) {
	current, err := coordinator.Status(ctx)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	state = current
	if state.DeliveryResultRef != "" {
		return finishProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			state.DeliveryResultRef,
			"",
		)
	}
	if state.ProductiveBlockerRef != "" {
		return finishProductiveAdvance(
			ctx,
			store,
			coordinator,
			expectedRevision,
			"",
			state.ProductiveBlockerRef,
		)
	}
	diagnostic, err := store.publishDiagnostic(
		ctx,
		state,
		code,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	state, err = coordinator.RecordProductiveBlocker(
		ctx,
		state.Revision,
		diagnostic.Ref,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	return finishProductiveAdvance(
		ctx,
		store,
		coordinator,
		expectedRevision,
		"",
		state.ProductiveBlockerRef,
	)
}

func blockRecoveredProductiveAdvance(
	ctx context.Context,
	store productiveAdvanceStore,
	coordinator *OwnerCoordinator,
	expectedRevision string,
	state workrun.WorkRunState,
	code workrun.WorkAdvanceDiagnosticCode,
) (workrun.WorkAdvanceV1, error) {
	diagnostic, err := store.publishDiagnostic(ctx, state, code)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	state, err = coordinator.recordRecoveredProductiveBlocker(
		ctx,
		state.Revision,
		diagnostic.Ref,
	)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	return finishProductiveAdvance(
		ctx,
		store,
		coordinator,
		expectedRevision,
		"",
		state.ProductiveBlockerRef,
	)
}

func finishProductiveAdvance(
	ctx context.Context,
	store productiveAdvanceStore,
	coordinator *OwnerCoordinator,
	expectedRevision string,
	deliveryResultRef string,
	diagnosticRef string,
) (workrun.WorkAdvanceV1, error) {
	status, err := coordinator.work.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	var diagnostic *workrun.WorkAdvanceDiagnosticV1
	if diagnosticRef != "" {
		state, stateErr := coordinator.Status(ctx)
		if stateErr != nil {
			return workrun.WorkAdvanceV1{}, stateErr
		}
		resolved, resolveErr := store.resolveDiagnosticForState(
			ctx,
			diagnosticRef,
			state,
		)
		if resolveErr != nil {
			return workrun.WorkAdvanceV1{}, resolveErr
		}
		value := workrun.WorkAdvanceDiagnosticV1{
			Ref:     diagnosticRef,
			Code:    resolved.Code,
			Message: resolved.Message,
		}
		if err := value.Validate(); err != nil {
			return workrun.WorkAdvanceV1{}, err
		}
		diagnostic = &value
	}
	result := workrun.WorkAdvanceV1{
		Schema:            workrun.WorkAdvanceContractV1,
		Contract:          workrun.WorkAdvanceContractV1,
		PreviousRevision:  expectedRevision,
		Status:            status,
		DeliveryResultRef: deliveryResultRef,
		Diagnostic:        diagnostic,
	}
	if err := result.Validate(); err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	if err := store.publishResult(ctx, result); err != nil {
		return workrun.WorkAdvanceV1{}, err
	}
	return result, nil
}

func productiveAdvanceLineage(repositoryRef, workRunID string) string {
	sum := sha256.Sum256(
		[]byte("gentle-ai.productive-advance-lineage/v1\x00" +
			repositoryRef + "\x00" + workRunID),
	)
	return "work-advance-" + hex.EncodeToString(sum[:16])
}

func productiveAdvanceSHA256(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func publishNativePassiveRAR(
	ctx context.Context,
	coordinator *OwnerCoordinator,
	repo string,
	lineageID string,
	snapshot reviewtransaction.Snapshot,
	applicability reviewtransaction.VerificationApplicability,
	registry reviewtransaction.VerificationPlanRegistry,
	plan reviewtransaction.VerificationPlan,
	assessment reviewtransaction.RiskAssessment,
	lenses []string,
) (string, string, error) {
	if assessment.Level != reviewtransaction.RiskLow ||
		len(lenses) != 0 {
		return "", "", errors.New(
			"native passive review is not genuine zero-lens low risk",
		)
	}
	state, err := reviewtransaction.NewCompactState(reviewtransaction.Start{
		LineageID: lineageID, Mode: reviewtransaction.ModeOrdinaryBounded,
		Generation: 1, Snapshot: snapshot, PolicyHash: plan.PolicyHash,
		RiskLevel: assessment.Level, SelectedLenses: lenses,
		OriginalChangedLines: &assessment.ChangedLines,
	})
	if err != nil {
		return "", "", err
	}
	start, err := reviewtransaction.StartCompactAuthority(
		ctx,
		repo,
		reviewtransaction.CompactStartRequest{
			State:           state,
			ExplicitLineage: true,
		},
	)
	if err != nil {
		return "", "", err
	}
	if start.Action == reviewtransaction.CompactStartBlocked ||
		start.Action == reviewtransaction.CompactStartRecover {
		return "", "", errors.New(
			"native passive review requires an explicit review decision",
		)
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(
		ctx,
		repo,
		lineageID,
	)
	if err != nil {
		return "", "", err
	}
	record := start.Record
	if record.State.LineageID != state.LineageID ||
		record.State.Generation != state.Generation ||
		record.State.PolicyHash != state.PolicyHash ||
		record.State.RiskLevel != state.RiskLevel ||
		record.State.OriginalChangedLines !=
			state.OriginalChangedLines ||
		!reflect.DeepEqual(
			record.State.SelectedLenses,
			state.SelectedLenses,
		) ||
		!reviewtransaction.SnapshotsEqualExact(
			record.State.InitialSnapshot,
			state.InitialSnapshot,
		) ||
		!reflect.DeepEqual(record.State.GenesisPaths, state.GenesisPaths) ||
		!reviewtransaction.SnapshotsEqualExact(
			record.State.CurrentSnapshot,
			snapshot,
		) ||
		len(record.State.CorrectionAttempts) != 0 {
		return "", "", errors.New(
			"native passive review authority does not match the requested candidate",
		)
	}
	if record.State.State == reviewtransaction.StateReviewing {
		next := record.State
		if err := next.CompleteReview(
			reviewtransaction.CompactReviewInput{
				LensResults:     []reviewtransaction.LensResult{},
				Classifications: []reviewtransaction.FindingEvidence{},
				RefuterOutcomes: []reviewtransaction.EvidenceResult{},
			},
		); err != nil {
			return "", "", err
		}
		if _, err := store.ReplaceContext(
			ctx,
			record.Revision,
			"review/complete-review",
			next,
		); err != nil {
			return "", "", err
		}
		record, err = store.LoadContext(ctx)
		if err != nil {
			return "", "", err
		}
	}
	if record.State.State == reviewtransaction.StateValidating {
		evidence, err := reviewtransaction.NativeLowRiskVerificationEvidence(
			record.State,
			assessment,
		)
		if err != nil {
			return "", "", err
		}
		next := record.State
		if err := next.CompleteVerification(evidence, true); err != nil {
			return "", "", err
		}
		if _, err := store.ReplaceContext(
			ctx,
			record.Revision,
			"review/complete-verification",
			next,
		); err != nil {
			return "", "", err
		}
		record, err = store.LoadContext(ctx)
		if err != nil {
			return "", "", err
		}
	}
	if record.State.State != reviewtransaction.StateApproved {
		return "", "", errors.New("native passive review did not approve")
	}
	receipt, err := record.State.Receipt()
	if err != nil {
		return "", "", err
	}
	if err := store.WriteReceipt(ctx, receipt); err != nil {
		return "", "", err
	}
	receiptPayload, err := readProductiveRegularFile(
		store.ReceiptPath(),
		maximumProductiveAdvanceRecord,
	)
	if err != nil {
		return "", "", err
	}
	receiptRef := productiveAdvanceSHA256(receiptPayload)
	resultRef := productiveAdvanceResultRef(
		lineageID,
		receiptRef,
		plan,
	)
	result := reviewtransaction.VerificationResultRef{
		Schema:    reviewtransaction.VerificationResultRefSchema,
		ResultRef: resultRef, Subject: plan.Subject,
		PolicyHash: plan.PolicyHash, PlanDigest: plan.Digest,
		ApplicabilityDigest:  plan.ApplicabilityDigest,
		Aggregate:            reviewtransaction.VerificationAggregateNotRequired,
		CompletedObligations: []string{}, EvidenceRefs: []string{},
	}
	authority, err := coordinator.rar.Publish(
		ctx,
		reviewtransaction.RARAuthorityPublication{
			LineageID: lineageID, ReceiptRef: receiptRef,
			Applicability: applicability, Registry: registry,
			Plan: plan, Result: result,
		},
	)
	if err != nil {
		return "", "", err
	}
	if authority.Receipt.ReceiptRef != receiptRef ||
		authority.Result.ResultRef != resultRef {
		return "", "", ErrProviderResultMismatch
	}
	return receiptRef, resultRef, nil
}

func productiveAdvanceResultRef(
	lineageID string,
	receiptRef string,
	plan reviewtransaction.VerificationPlan,
) string {
	value := struct {
		Schema     string `json:"schema"`
		LineageID  string `json:"lineageId"`
		ReceiptRef string `json:"receiptRef"`
		PlanDigest string `json:"planDigest"`
	}{
		Schema:    "gentle-ai.native-not-required-result/v1",
		LineageID: lineageID, ReceiptRef: receiptRef,
		PlanDigest: plan.Digest,
	}
	payload, _ := json.Marshal(value)
	return productiveAdvanceSHA256(payload)
}
