package workprovider

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

// productiveRouteAuthority is a read-only proof that a route mutation targets
// one existing managed WorkRun and its immutable START authority. Resolving it
// must precede factory.Open, whose owner composition may create coordination
// directories.
type productiveRouteAuthority struct {
	factory      *ProductionOwnerCoordinatorFactory
	coordination *CoordinationAuthorityStore
	state        workrun.WorkRunState
	start        productiveStartAuthority
}

func (runtimeOutcome *productiveRuntimeOutcome) DecideRoute(
	ctx context.Context,
	workRunID string,
	expectedRevision string,
	choice workrun.WorkRouteChoice,
) (workrun.WorkRouteV1, error) {
	if !workRunIDPattern.MatchString(workRunID) ||
		!revisionPattern.MatchString(expectedRevision) {
		return workrun.WorkRouteV1{}, errors.New(
			"productive route decision requires exact WorkRun authority",
		)
	}
	if choice != workrun.WorkRouteChoiceAcceptSDD &&
		choice != workrun.WorkRouteChoiceDeclineSDD {
		return workrun.WorkRouteV1{}, errors.New(
			"productive route decision requires accept_sdd or decline_sdd",
		)
	}
	authority, err := runtimeOutcome.resolveProductiveRouteAuthority(
		ctx,
		workRunID,
	)
	if err != nil {
		return workrun.WorkRouteV1{}, err
	}
	var fallback SDDDeclineFallbackRecord
	if choice == workrun.WorkRouteChoiceDeclineSDD {
		fallback, err = authority.resolveSDDDeclineFallback(ctx)
		if err != nil {
			return workrun.WorkRouteV1{}, err
		}
	}
	coordinator, err := authority.factory.Open(ctx, workRunID)
	if err != nil {
		return workrun.WorkRouteV1{}, err
	}
	var (
		selected workrun.ImplementationRoute
		state    workrun.WorkRunState
	)
	switch choice {
	case workrun.WorkRouteChoiceAcceptSDD:
		state, err = coordinator.AcceptProposedSDD(
			ctx,
			OwnerAcceptSDDRequest{
				ExpectedRevision:      expectedRevision,
				PendingDecisionDigest: authority.state.RouteDecision.Digest,
			},
		)
		selected = workrun.ImplementationRouteSDD
	case workrun.WorkRouteChoiceDeclineSDD:
		state, err = coordinator.rerouteProposedSDD(
			ctx,
			expectedRevision,
			fallback.PendingDecisionDigest,
			fallback.RouteDecision,
		)
		selected = state.ImplementationRoute
	}
	if err != nil {
		return workrun.WorkRouteV1{}, err
	}
	status, err := projectRouteMutationStatus(state)
	if err != nil {
		return workrun.WorkRouteV1{}, err
	}
	result := workrun.WorkRouteV1{
		Schema:           workrun.WorkRouteContractV1,
		Contract:         workrun.WorkRouteContractV1,
		Operation:        workrun.WorkRouteOperationDecideSDD,
		Choice:           choice,
		PreviousRevision: expectedRevision,
		SelectedRoute:    selected,
		Status:           status,
	}
	if err := result.Validate(); err != nil {
		return workrun.WorkRouteV1{}, err
	}
	return result, nil
}

func (runtimeOutcome *productiveRuntimeOutcome) BindSDDRoute(
	ctx context.Context,
	workRunID string,
	expectedRevision string,
	runRef string,
) (workrun.WorkRouteV1, error) {
	if !workRunIDPattern.MatchString(workRunID) ||
		!revisionPattern.MatchString(expectedRevision) {
		return workrun.WorkRouteV1{}, errors.New(
			"productive SDD binding requires exact WorkRun authority",
		)
	}
	if !validNativeSDDRunRef(runRef) {
		return workrun.WorkRouteV1{}, errors.New(
			"productive SDD binding requires a canonical SDD run reference",
		)
	}
	authority, err := runtimeOutcome.resolveProductiveRouteAuthority(
		ctx,
		workRunID,
	)
	if err != nil {
		return workrun.WorkRouteV1{}, err
	}
	if authority.state.RouteDecision.Decision !=
		workrun.RouteDecisionProposeSDD ||
		authority.state.ImplementationRoute != workrun.ImplementationRouteSDD ||
		!validPADImmutableRef(authority.state.RouteAcceptanceRef) {
		return workrun.WorkRouteV1{}, fmt.Errorf(
			"%w: WorkRun has no accepted SDD route",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	coordinator, err := authority.factory.Open(ctx, workRunID)
	if err != nil {
		return workrun.WorkRouteV1{}, err
	}
	state, err := coordinator.BindAcceptedSDDRun(
		ctx,
		OwnerBindSDDRunRequest{
			ExpectedRevision:   expectedRevision,
			RouteAcceptanceRef: authority.state.RouteAcceptanceRef,
			RunRef:             runRef,
		},
	)
	if err != nil {
		return workrun.WorkRouteV1{}, err
	}
	status, err := projectRouteMutationStatus(state)
	if err != nil {
		return workrun.WorkRouteV1{}, err
	}
	result := workrun.WorkRouteV1{
		Schema:           workrun.WorkRouteContractV1,
		Contract:         workrun.WorkRouteContractV1,
		Operation:        workrun.WorkRouteOperationBindSDD,
		PreviousRevision: expectedRevision,
		SelectedRoute:    workrun.ImplementationRouteSDD,
		Status:           status,
	}
	if err := result.Validate(); err != nil {
		return workrun.WorkRouteV1{}, err
	}
	return result, nil
}

func (runtimeOutcome *productiveRuntimeOutcome) resolveProductiveRouteAuthority(
	ctx context.Context,
	workRunID string,
) (productiveRouteAuthority, error) {
	if err := runtimeOutcome.validate(ctx); err != nil {
		return productiveRouteAuthority{}, err
	}
	if !workRunIDPattern.MatchString(workRunID) {
		return productiveRouteAuthority{}, errors.New(
			"productive route requires a canonical WorkRun identifier",
		)
	}
	capabilities, err := runtimeOutcome.Capabilities(ctx)
	if err != nil {
		return productiveRouteAuthority{}, err
	}
	if capabilities.WorkRouting.Exposure != WorkRoutingAdvertised ||
		runtimeOutcome.connector == nil {
		return productiveRouteAuthority{}, ErrCapabilityReadOnly
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(
		ctx,
		runtimeOutcome.repo,
	)
	if err != nil {
		return productiveRouteAuthority{}, err
	}
	if err := lease.Validate(ctx); err != nil {
		return productiveRouteAuthority{}, err
	}
	if lease.Identity().RepositoryRef != runtimeOutcome.repositoryRef {
		return productiveRouteAuthority{}, ErrProductiveRuntimeBindingMismatch
	}
	store, err := workrun.OpenWorkRunStoreWithRepositoryIdentityLease(
		ctx,
		lease,
		workRunID,
	)
	if err != nil {
		return productiveRouteAuthority{}, err
	}
	state, err := store.Status()
	if err != nil {
		return productiveRouteAuthority{}, err
	}
	startStore, err := existingProductiveAdvanceStore(
		lease,
		workRunID,
	)
	if err != nil {
		return productiveRouteAuthority{}, err
	}
	start, err := startStore.startAuthority(ctx)
	if err != nil {
		return productiveRouteAuthority{}, err
	}
	if start.RepositoryRef != lease.Identity().RepositoryRef ||
		start.WorkRunID != state.WorkRunID ||
		start.DeliveryIntentRef != state.DeliveryIntentRef {
		return productiveRouteAuthority{}, errors.New(
			"productive route START authority does not bind the existing WorkRun",
		)
	}
	coordination, err := openCoordinationAuthorityStoreWithLease(
		ctx,
		lease,
		workRunID,
		nil,
	)
	if err != nil {
		return productiveRouteAuthority{}, err
	}
	factory, err := NewProductiveOwnerCoordinatorFactory(
		ctx,
		runtimeOutcome.repo,
		runtimeOutcome.agent,
		runtimeOutcome.activation,
		runtimeOutcome.connector,
	)
	if err != nil {
		return productiveRouteAuthority{}, err
	}
	if factory.RepositoryRef() != lease.Identity().RepositoryRef {
		return productiveRouteAuthority{}, ErrProductiveRuntimeBindingMismatch
	}
	return productiveRouteAuthority{
		factory:      factory,
		coordination: coordination,
		state:        state,
		start:        start,
	}, nil
}

func (authority productiveRouteAuthority) resolveSDDDeclineFallback(
	ctx context.Context,
) (SDDDeclineFallbackRecord, error) {
	if authority.coordination == nil ||
		!validPADImmutableRef(authority.start.SDDDeclineFallbackRef) {
		return SDDDeclineFallbackRecord{}, fmt.Errorf(
			"%w: WorkRun has no immutable owner-authored SDD decline fallback",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	fallback, err := authority.coordination.ResolveSDDDeclineFallback(
		ctx,
		authority.start.SDDDeclineFallbackRef,
	)
	if err != nil {
		return SDDDeclineFallbackRecord{}, fmt.Errorf(
			"resolve SDD decline fallback authority: %w",
			err,
		)
	}
	if fallback.AuthorityRef != authority.start.SDDDeclineFallbackRef ||
		authority.state.SDDDeclineFallbackRef !=
			authority.start.SDDDeclineFallbackRef ||
		fallback.RepositoryIdentity != authority.start.RepositoryRef ||
		fallback.WorkRunID != authority.start.WorkRunID ||
		fallback.WorkRunID != authority.state.WorkRunID ||
		fallback.DeliveryIntentRef != authority.start.DeliveryIntentRef ||
		fallback.DeliveryIntentRef != authority.state.DeliveryIntentRef {
		return SDDDeclineFallbackRecord{}, fmt.Errorf(
			"%w: SDD decline fallback does not bind START and WorkRun",
			ErrCoordinationAuthorityCorrupt,
		)
	}

	if authority.state.RouteDecision.Decision ==
		workrun.RouteDecisionProposeSDD &&
		authority.state.RouteDecision.Digest ==
			fallback.PendingDecisionDigest &&
		authority.state.ImplementationRoute == "" &&
		authority.state.RouteAcceptanceRef == "" {
		return fallback, nil
	}

	selectedRoute := workrun.ImplementationRouteDirectInline
	if fallback.RouteDecision.Decision ==
		workrun.RouteDecisionDelegatedDirect {
		selectedRoute = workrun.ImplementationRouteDelegatedDirect
	}
	if !reflect.DeepEqual(
		authority.state.RouteDecision,
		fallback.RouteDecision,
	) ||
		authority.state.ImplementationRoute != selectedRoute ||
		!validPADImmutableRef(authority.state.RouteAcceptanceRef) {
		return SDDDeclineFallbackRecord{}, fmt.Errorf(
			"%w: SDD decline fallback does not bind the pending proposal or its exact replay",
			workrun.ErrWorkRunInvalidTransition,
		)
	}
	selection, err := authority.coordination.ResolveRouteSelection(
		ctx,
		authority.state.RouteAcceptanceRef,
	)
	if err != nil {
		return SDDDeclineFallbackRecord{}, fmt.Errorf(
			"resolve replayed SDD decline route selection: %w",
			err,
		)
	}
	if err := selection.Validate(
		authority.state.RouteAcceptanceRef,
		fallback.PendingDecisionDigest,
		selectedRoute,
		fallback.RouteDecision.Digest,
	); err != nil {
		return SDDDeclineFallbackRecord{}, fmt.Errorf(
			"validate replayed SDD decline route selection: %w",
			err,
		)
	}
	return fallback, nil
}

func validNativeSDDRunRef(runRef string) bool {
	return utf8.ValidString(runRef) &&
		utf8.RuneCountInString(runRef) <= 96 &&
		sddRunRefPattern.MatchString(runRef)
}

// projectRouteMutationStatus projects only exact route-mutation successors.
// It never rereads mutable current state after the owner lock is released, so
// lost-response replay remains bound to the historical WorkRun revision even
// if another process has already advanced the run.
func projectRouteMutationStatus(
	state workrun.WorkRunState,
) (workrun.WorkStatusV1, error) {
	if !state.Started || state.Handoff != nil ||
		state.VerificationResultRef != "" ||
		state.ReviewReceiptRef != "" ||
		state.DeliveryAuthorizationRef != "" ||
		state.DeliveryResultRef != "" ||
		state.ProductiveBlockerRef != "" {
		return workrun.WorkStatusV1{}, ErrProviderResultMismatch
	}
	phase := workrun.RoutePhaseImplementationSelected
	route := state.ImplementationRoute
	sddRunRef := state.SDDRunRef
	publicState := workrun.PublicStateWorking
	if state.RouteDecision.Decision == workrun.RouteDecisionProposeSDD {
		switch {
		case state.ImplementationRoute == "":
			phase = workrun.RoutePhaseDecisionPending
			publicState = workrun.PublicStateNeedsYourDecision
		case state.ImplementationRoute == workrun.ImplementationRouteSDD &&
			state.SDDRunRef == "":
			phase = workrun.RoutePhaseSDDRuntimePending
			route = ""
		}
	}
	status := workrun.WorkStatusV1{
		Schema:              workrun.WorkStatusContractV1,
		Contract:            workrun.WorkStatusContractV1,
		WorkRunID:           state.WorkRunID,
		Revision:            state.Revision,
		PublicState:         publicState,
		RouteDecision:       state.RouteDecision.Decision,
		RoutePhase:          phase,
		ImplementationRoute: route,
		SDDRunRef:           sddRunRef,
		Verification: workrun.VerificationSummaryV1{
			Outcome:    workrun.VerificationPending,
			ResultRefs: []string{},
		},
		DeliveryIntentRef: state.DeliveryIntentRef,
	}
	if err := status.Validate(); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	return status, nil
}

var _ RuntimeOutcome = (*productiveRuntimeOutcome)(nil)
