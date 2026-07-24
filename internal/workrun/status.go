package workrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

// PublicStatus projects owner facts without copying them into WorkRun. It never
// advertises an executable SDD route before both acceptance and run binding,
// and it never infers Ready without a future PAD authorization binding.
func (store WorkRunStore) PublicStatus(ctx context.Context) (WorkStatusV1, error) {
	state, err := store.Status()
	if err != nil {
		return WorkStatusV1{}, err
	}
	var productiveExecution *ProductiveExecutionResultAuthority
	if state.ProductiveExecutionResultRef != "" {
		resolved, err := store.resolveProductiveExecutionResult(
			ctx,
			state.ProductiveExecutionResultRef,
			state,
		)
		if err != nil {
			return WorkStatusV1{}, err
		}
		productiveExecution = &resolved
	}
	var diagnostic *WorkAdvanceDiagnosticV1
	if state.ProductiveBlockerRef != "" {
		if state.ProductiveReconciliationRef != "" {
			reconciliation, err := store.resolveProductiveReconciliation(
				ctx,
				state.ProductiveReconciliationRef,
				state,
			)
			if err != nil {
				return WorkStatusV1{}, err
			}
			diagnostic = nil
			if reconciliation.Diagnostic != nil {
				value := *reconciliation.Diagnostic
				diagnostic = &value
			}
		} else {
			resolved, err := store.resolveProductiveDiagnosticAuthority(
				ctx,
				state.ProductiveBlockerRef,
				state,
			)
			if err != nil {
				return WorkStatusV1{}, err
			}
			value := resolved.Diagnostic
			diagnostic = &value
		}
	}
	var result *VerificationResultAuthority
	if state.VerificationResultRef != "" {
		resolved, err := store.resolveBoundVerificationResult(ctx, state)
		if err != nil {
			return WorkStatusV1{}, err
		}
		result = &resolved
	}
	var authorization *DeliveryAuthorizationAuthority
	var deliveryResult *DeliveryResultAuthority
	if state.DeliveryResultRef != "" {
		if store.authority.DeliveryResult == nil {
			return WorkStatusV1{}, ErrAuthorityPortUnavailable
		}
		resolved, err := store.authority.DeliveryResult.ResolveDeliveryResult(
			ctx,
			state.DeliveryResultRef,
		)
		if err != nil {
			return WorkStatusV1{}, fmt.Errorf(
				"resolve terminal PAD delivery result: %w",
				err,
			)
		}
		if err := resolved.Validate(state.DeliveryResultRef, state); err != nil {
			return WorkStatusV1{}, err
		}
		deliveryResult = &resolved
	}
	if state.DeliveryAuthorizationRef != "" &&
		deliveryResult == nil &&
		state.ProductiveBlockerRef == "" &&
		productiveExecution == nil {
		if result == nil || state.Handoff == nil || state.ReviewReceiptRef == "" {
			return WorkStatusV1{}, errors.New(
				"delivery authorization is bound without terminal WorkRun facts",
			)
		}
		if store.authority.PAD == nil {
			return WorkStatusV1{}, ErrAuthorityPortUnavailable
		}
		resolved, err := store.authority.PAD.ResolveLiveDeliveryAuthorization(
			ctx,
			state.DeliveryAuthorizationRef,
		)
		if err != nil {
			if errors.Is(err, ErrDeliveryAuthorizationInactive) {
				return projectPublicStatus(
					state,
					result,
					nil,
					nil,
					diagnostic,
				)
			}
			return WorkStatusV1{}, fmt.Errorf(
				"resolve live PAD delivery authorization: %w",
				err,
			)
		}
		if err := resolved.Validate(
			state.DeliveryAuthorizationRef,
			state,
		); err != nil {
			if errors.Is(err, ErrDeliveryAuthorizationInactive) {
				return projectPublicStatus(
					state,
					result,
					nil,
					nil,
					diagnostic,
				)
			}
			return WorkStatusV1{}, err
		}
		if err := validateAuthorizedDeliveryAggregate(
			resolved.Kind,
			result.Result.Aggregate,
		); err != nil {
			return WorkStatusV1{}, err
		}
		authorization = &resolved
	}
	return projectPublicStatus(
		state,
		result,
		authorization,
		deliveryResult,
		diagnostic,
	)
}

func (store WorkRunStore) resolveProductiveDiagnostic(
	ctx context.Context,
	ref string,
	state WorkRunState,
) error {
	_, err := store.resolveProductiveDiagnosticAuthority(ctx, ref, state)
	return err
}

func (store WorkRunStore) resolveProductiveDiagnosticAuthority(
	ctx context.Context,
	ref string,
	state WorkRunState,
) (ProductiveDiagnosticAuthority, error) {
	if !validSHA256Ref(ref) {
		return ProductiveDiagnosticAuthority{}, errors.New(
			"WorkRun productive diagnostic reference is invalid",
		)
	}
	if store.authority.ProductiveDiagnostic == nil {
		return ProductiveDiagnosticAuthority{}, ErrAuthorityPortUnavailable
	}
	resolved, err := store.authority.ProductiveDiagnostic.
		ResolveProductiveDiagnostic(ctx, ref)
	if err != nil {
		return ProductiveDiagnosticAuthority{}, fmt.Errorf(
			"resolve owner productive diagnostic: %w",
			err,
		)
	}
	repositoryRef := store.RepositoryRef()
	if repositoryRef == "" {
		return ProductiveDiagnosticAuthority{}, errors.New(
			"WorkRun repository authority is unavailable",
		)
	}
	if err := resolved.Validate(ref, repositoryRef, state); err != nil {
		return ProductiveDiagnosticAuthority{}, err
	}
	return resolved, nil
}

func projectPublicStatus(
	state WorkRunState,
	result *VerificationResultAuthority,
	authorization *DeliveryAuthorizationAuthority,
	deliveryResult *DeliveryResultAuthority,
	diagnostic *WorkAdvanceDiagnosticV1,
) (WorkStatusV1, error) {
	if !state.Started {
		return WorkStatusV1{}, ErrWorkRunNotStarted
	}
	route := state.ImplementationRoute
	sddRunRef := state.SDDRunRef
	routePhase := RoutePhaseImplementationSelected
	if route == ImplementationRouteSDD && sddRunRef == "" {
		route = ""
		routePhase = RoutePhaseSDDRuntimePending
	} else if state.RouteDecision.Decision == RouteDecisionProposeSDD &&
		route == "" {
		routePhase = RoutePhaseDecisionPending
	}
	status := WorkStatusV1{
		Schema: WorkStatusContractV1, Contract: WorkStatusContractV1,
		WorkRunID: state.WorkRunID, Revision: publicWorkRunRevision(state),
		PublicState:         publicStateForWorkRun(state, result),
		RouteDecision:       state.RouteDecision.Decision,
		RoutePhase:          routePhase,
		ImplementationRoute: route, SDDRunRef: sddRunRef,
		Verification: VerificationSummaryV1{
			Outcome: VerificationPending, ResultRefs: []string{},
		},
		DeliveryIntentRef: state.DeliveryIntentRef,
		ReviewReceiptRef:  state.ReviewReceiptRef,
		Diagnostic:        diagnostic,
	}
	if result != nil {
		status.Verification.Outcome = publicVerificationOutcome(result.Result.Aggregate)
		status.Verification.ResultRefs = []string{result.Result.ResultRef}
	}
	if (state.ProductiveBlockerRef == "" ||
		state.ProductiveReconciliationOutcome ==
			WorkReconcileDeliveryConfirmed) &&
		(authorization != nil || deliveryResult != nil) {
		status.PublicState = PublicStateReady
		status.Diagnostic = nil
	}
	if err := status.Validate(); err != nil {
		return WorkStatusV1{}, err
	}
	return status, nil
}

// publicWorkRunRevision keeps the productive Advance CAS stable while the owner
// progresses autonomously. A durable consent checkpoint yields control, so its
// current ledger revision becomes observable through both work-status and the
// owner prompt. A durable decision then advances that revision once more and
// records that successor as the stable bounded resume token.
func publicWorkRunRevision(state WorkRunState) string {
	if state.Forecast != nil &&
		state.Forecast.Availability == ForecastAvailable &&
		state.Disposition == nil &&
		forecastRequiresExplicitConsent(*state.Forecast) {
		return state.Revision
	}
	if validSHA256Ref(state.ProductiveResumeRevision) &&
		state.ProductiveBlockerRef == "" &&
		state.DeliveryResultRef == "" &&
		state.ProductiveReconciliationRef == "" {
		return state.ProductiveResumeRevision
	}
	if state.ProductiveAdvanceSourceRevision != "" &&
		state.ProductiveBlockerRef == "" &&
		state.DeliveryResultRef == "" &&
		state.ProductiveReconciliationRef == "" {
		return state.ProductiveAdvanceSourceRevision
	}
	return state.Revision
}

func (store WorkRunStore) resolveBoundVerificationResult(
	ctx context.Context,
	state WorkRunState,
) (VerificationResultAuthority, error) {
	if !validSHA256Ref(state.VerificationResultRef) {
		return VerificationResultAuthority{}, errors.New(
			"WorkRun verification result reference is invalid",
		)
	}
	if store.authority.Verification == nil {
		return VerificationResultAuthority{}, ErrAuthorityPortUnavailable
	}
	resolved, err := store.authority.Verification.ResolveResult(
		ctx,
		state.VerificationResultRef,
	)
	if err != nil {
		return VerificationResultAuthority{}, fmt.Errorf(
			"resolve owner verification result: %w",
			err,
		)
	}
	if err := resolved.Validate(state.VerificationResultRef); err != nil {
		return VerificationResultAuthority{}, err
	}
	if state.Forecast == nil ||
		resolved.Plan.Digest != state.Forecast.PlanDigest ||
		resolved.Applicability.Digest != state.Forecast.ApplicabilityDigest {
		return VerificationResultAuthority{}, errors.New(
			"owner verification result does not bind WorkRun forecast",
		)
	}
	if state.PostVerificationSnapshotRef != "" &&
		resolved.Result.Subject.SnapshotIdentity !=
			state.PostVerificationSnapshotRef {
		return VerificationResultAuthority{}, errors.New(
			"owner verification result does not bind the exact post-verification snapshot",
		)
	}
	if state.CorrectionImpactClosureRef != "" {
		if state.VerificationReplan == nil {
			return VerificationResultAuthority{}, errors.New(
				"correction closure is bound without a correction replan",
			)
		}
		closure, err := store.buildCorrectionImpactClosure(
			ctx,
			*state.VerificationReplan,
			resolved.Applicability,
			resolved.Registry,
			resolved.Plan,
			resolved.Result,
		)
		if err != nil {
			return VerificationResultAuthority{}, err
		}
		if closure.Digest != state.CorrectionImpactClosureRef ||
			!equalStrings(
				reusableCorrectionObligations(closure),
				state.ReusableVerificationObligations,
			) {
			return VerificationResultAuthority{}, errors.New(
				"owner correction closure does not bind WorkRun convergence",
			)
		}
	}
	return resolved, nil
}

func validateAuthorizedDeliveryAggregate(
	kind DeliveryAuthorizationKind,
	aggregate reviewtransaction.VerificationAggregate,
) error {
	if aggregate == reviewtransaction.VerificationAggregateFailed {
		return errors.New(
			"failed verification can never authorize product delivery",
		)
	}
	switch kind {
	case DeliveryAuthorizationNormal:
		if aggregate == reviewtransaction.VerificationAggregateComplete ||
			aggregate == reviewtransaction.VerificationAggregateNotRequired {
			return nil
		}
		return errors.New(
			"normal delivery authorization requires complete or not-required verification",
		)
	case DeliveryAuthorizationException:
		if aggregate == reviewtransaction.VerificationAggregatePartial ||
			aggregate == reviewtransaction.VerificationAggregateUnavailable {
			return nil
		}
		return errors.New(
			"exception delivery authorization requires partial or unavailable verification",
		)
	default:
		return fmt.Errorf("unsupported delivery authorization kind %q", kind)
	}
}

func publicStateForWorkRun(
	state WorkRunState,
	result *VerificationResultAuthority,
) PublicState {
	if state.RouteDecision.Decision == RouteDecisionProposeSDD &&
		state.ImplementationRoute == "" {
		return PublicStateNeedsYourDecision
	}
	if state.VerificationStop != nil {
		return PublicStateNeedsYourDecision
	}
	if state.ProductiveBlockerRef != "" {
		return PublicStateNeedsYourDecision
	}
	if result != nil {
		switch result.Result.Aggregate {
		case reviewtransaction.VerificationAggregateFailed,
			reviewtransaction.VerificationAggregatePartial,
			reviewtransaction.VerificationAggregateUnavailable:
			return PublicStateNeedsYourDecision
		default:
			return PublicStateChecking
		}
	}
	if state.Forecast != nil {
		switch state.Forecast.Availability {
		case ForecastPartial, ForecastUnavailable, ForecastUnknown:
			return PublicStateNeedsYourDecision
		case ForecastAvailable:
			if state.Disposition == nil &&
				forecastRequiresExplicitConsent(*state.Forecast) {
				return PublicStateNeedsYourDecision
			}
		}
	}
	if state.Disposition != nil {
		switch state.Disposition.Kind {
		case DispositionDefer, DispositionReduceScope, DispositionDeferredRunner:
			return PublicStateNeedsYourDecision
		}
	}
	if state.Handoff != nil {
		return PublicStateChecking
	}
	return PublicStateWorking
}

func publicVerificationOutcome(
	aggregate reviewtransaction.VerificationAggregate,
) VerificationOutcome {
	switch aggregate {
	case reviewtransaction.VerificationAggregateNotRequired:
		return VerificationNotRequired
	case reviewtransaction.VerificationAggregateComplete:
		return VerificationComplete
	case reviewtransaction.VerificationAggregateFailed:
		return VerificationFailed
	case reviewtransaction.VerificationAggregatePartial:
		return VerificationPartial
	default:
		return VerificationUnavailable
	}
}
