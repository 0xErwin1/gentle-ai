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
	var result *VerificationResultAuthority
	if state.VerificationResultRef != "" {
		resolved, err := store.resolveBoundVerificationResult(ctx, state)
		if err != nil {
			return WorkStatusV1{}, err
		}
		result = &resolved
	}
	var authorization *DeliveryAuthorizationAuthority
	if state.DeliveryAuthorizationRef != "" {
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
				return projectPublicStatus(state, result, nil)
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
				return projectPublicStatus(state, result, nil)
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
	return projectPublicStatus(state, result, authorization)
}

func projectPublicStatus(
	state WorkRunState,
	result *VerificationResultAuthority,
	authorization *DeliveryAuthorizationAuthority,
) (WorkStatusV1, error) {
	if !state.Started {
		return WorkStatusV1{}, ErrWorkRunNotStarted
	}
	route := state.ImplementationRoute
	sddRunRef := state.SDDRunRef
	if route == ImplementationRouteSDD && sddRunRef == "" {
		route = ""
	}
	status := WorkStatusV1{
		Schema: WorkStatusContractV1, Contract: WorkStatusContractV1,
		WorkRunID: state.WorkRunID, Revision: state.Revision,
		PublicState:         publicStateForWorkRun(state, result),
		RouteDecision:       state.RouteDecision.Decision,
		ImplementationRoute: route, SDDRunRef: sddRunRef,
		Verification: VerificationSummaryV1{
			Outcome: VerificationPending, ResultRefs: []string{},
		},
		DeliveryIntentRef: state.DeliveryIntentRef,
		ReviewReceiptRef:  state.ReviewReceiptRef,
	}
	if result != nil {
		status.Verification.Outcome = publicVerificationOutcome(result.Result.Aggregate)
		status.Verification.ResultRefs = []string{result.Result.ResultRef}
	}
	if authorization != nil {
		status.PublicState = PublicStateReady
	}
	if err := status.Validate(); err != nil {
		return WorkStatusV1{}, err
	}
	return status, nil
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
			if state.Disposition == nil && state.Forecast.MaximumCost != nil {
				switch *state.Forecast.MaximumCost {
				case reviewtransaction.VerificationCostLong,
					reviewtransaction.VerificationCostVeryLong,
					reviewtransaction.VerificationCostUnknown:
					return PublicStateNeedsYourDecision
				}
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
