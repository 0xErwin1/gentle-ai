package workprovider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

// EvidenceStoreOpener keeps status polling read-only when no transition is
// advertised. Owner publication creates EPD storage before an authorization is
// issued; only resolution/apply needs to open it.
type EvidenceStoreOpener func(context.Context) (evidence.Store, error)

// ProductionRepository is the sole implementation of the public apply path.
// It accepts no argv or policy input: the transition ref resolves an immutable
// owner authorization, RAR plan, EPD ticket, and exact WorkRun revision.
type ProductionRepository struct {
	WorkRun      workrun.WorkRunStore
	Transitions  *TransitionAuthorityStore
	Plans        *reviewtransaction.RARAuthorityRepository
	Executor     *hostruntime.Executor
	OpenEvidence EvidenceStoreOpener
	Now          func() time.Time
}

func (repository *ProductionRepository) Status(
	ctx context.Context,
) (workrun.WorkStatusV1, error) {
	if err := repository.validate(); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	status, err := repository.WorkRun.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	authorization, err := repository.Transitions.CurrentAuthorization(
		ctx,
		status.Revision,
		repository.nowUnix(),
	)
	if err != nil {
		if errors.Is(err, ErrAuthorizationNotFound) {
			return status, nil
		}
		return workrun.WorkStatusV1{}, err
	}
	if authorization == nil {
		return status, nil
	}
	if _, err := repository.validatePendingAuthorization(
		ctx,
		*authorization,
	); err != nil {
		// A stale already-issued authorization is simply no longer advertised.
		// Corruption and binding mismatches remain visible errors.
		if errors.Is(err, ErrAuthorizationStale) ||
			errors.Is(err, reviewtransaction.ErrRARAuthorityStale) {
			return status, nil
		}
		return workrun.WorkStatusV1{}, err
	}
	transition, err := authorization.PublicTransition()
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	status.AuthorizedTransition = &transition
	if err := status.Validate(); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	return status, nil
}

func (repository *ProductionRepository) ResolveAuthorization(
	ctx context.Context,
	ref string,
) (ResolvedAuthorization, error) {
	if err := repository.validate(); err != nil {
		return ResolvedAuthorization{}, err
	}
	authorization, phase, err := repository.Transitions.ResolveAuthorization(
		ctx,
		ref,
		repository.nowUnix(),
	)
	if err != nil {
		return ResolvedAuthorization{}, err
	}
	// Pending authority must still bind the exact live owner preimages.
	// Claimed/completed authority resolves only so Apply can recover or replay;
	// it can never cause a second process without revalidating under the lock.
	if phase == TransitionPending {
		if _, err := repository.validatePendingAuthorization(ctx, authorization); err != nil {
			if errors.Is(err, reviewtransaction.ErrRARAuthorityStale) {
				return ResolvedAuthorization{}, ErrAuthorizationStale
			}
			return ResolvedAuthorization{}, err
		}
	}
	return authorization.Resolved()
}

func (repository *ProductionRepository) ApplyAuthorized(
	ctx context.Context,
	authorizationRef string,
	expectedRevision string,
) (workrun.WorkTransitionV1, error) {
	if err := repository.validate(); err != nil {
		return workrun.WorkTransitionV1{}, err
	}
	var result workrun.WorkTransitionV1
	err := repository.Transitions.withAuthorizationLock(
		ctx,
		authorizationRef,
		func() error {
			authorization, phase, err := repository.Transitions.ResolveAuthorization(
				ctx,
				authorizationRef,
				repository.nowUnix(),
			)
			if err != nil {
				return err
			}
			if authorization.ExpectedRevision != expectedRevision {
				return ErrAuthorizationMismatch
			}
			if phase == TransitionCompleted {
				completed, _, err := repository.Transitions.completion(authorization)
				if err != nil {
					return err
				}
				if completed == nil {
					return ErrTransitionAuthorityCorrupt
				}
				result = *completed
				return nil
			}

			evidenceStore, err := repository.OpenEvidence(ctx)
			if err != nil {
				return fmt.Errorf("open execution evidence: %w", err)
			}
			ticket, err := evidenceStore.ReadTicket(authorization.ActionTicketRef)
			if err != nil {
				return fmt.Errorf("read authorized action ticket: %w", err)
			}

			// EPD is the first recovery oracle. Once admitted evidence exists,
			// no live plan or launch retry is needed to reconstruct the receipt.
			admitted, evidenceErr := evidenceStore.ReadExecutionForTicket(
				authorization.ActionTicketRef,
			)
			if evidenceErr == nil {
				transition, err := repository.complete(
					ctx,
					authorization,
					admitted.EvidenceRef,
				)
				if err != nil {
					return err
				}
				result = transition
				return nil
			}
			if !errors.Is(evidenceErr, evidence.ErrEvidenceNotFound) {
				return fmt.Errorf("read admitted execution: %w", evidenceErr)
			}

			state, err := repository.WorkRun.Status()
			if err != nil {
				return err
			}
			reservation, reserved := reservationForActionTicket(
				state,
				authorization.ActionTicketRef,
			)
			if reserved && launchClaimForReservation(state, reservation.ReservationRef) {
				if phase == TransitionPending {
					if err := repository.Transitions.markClaimed(
						authorization,
						repository.nowUnix(),
					); err != nil {
						return err
					}
				}
				if err := repository.Transitions.markIndeterminate(authorization); err != nil {
					return err
				}
				return ErrTransitionIndeterminate
			}

			plan, err := repository.validatePendingAuthorization(
				ctx,
				authorization,
			)
			if err != nil {
				if errors.Is(err, reviewtransaction.ErrRARAuthorityStale) {
					return ErrAuthorizationStale
				}
				return err
			}
			if phase == TransitionPending {
				if err := repository.Transitions.markClaimed(
					authorization,
					repository.nowUnix(),
				); err != nil {
					return err
				}
			}

			requestID, err := authorization.RequestID()
			if err != nil {
				return err
			}
			executableStore := repository.WorkRun.WithEvidencePort(
				EvidenceWorkRunPort{Store: evidenceStore},
			)
			outcome, err := executableStore.Begin(ctx, workrun.BeginRequest{
				ExpectedRevision: authorization.ExpectedRevision,
				RequestID:        requestID,
				Applicability:    plan.Applicability,
				Registry:         plan.Registry,
				Plan:             plan.Plan,
				ActionTicketRef:  ticket.TicketRef,
			})
			if err != nil {
				return err
			}
			if outcome.Capability == nil {
				return errors.New("WorkRun begin returned no host launch capability")
			}
			step, err := ticket.ExecStep()
			if err != nil {
				return err
			}
			process, err := repository.Executor.ExecuteAuthorized(
				ctx,
				step,
				outcome.Capability,
			)
			if err != nil {
				// If HCR durably claimed before returning an unexpected error,
				// the only safe recovery is an explicit human decision.
				live, statusErr := executableStore.Status()
				if statusErr == nil {
					reservation, ok := reservationForActionTicket(
						live,
						authorization.ActionTicketRef,
					)
					if ok && launchClaimForReservation(
						live,
						reservation.ReservationRef,
					) {
						_ = repository.Transitions.markIndeterminate(authorization)
					}
				}
				return err
			}
			admitted, err = evidenceStore.AdmitAndStore(ticket, process)
			if err != nil {
				_ = repository.Transitions.markIndeterminate(authorization)
				return fmt.Errorf("admit execution evidence: %w", err)
			}
			transition, err := repository.complete(
				ctx,
				authorization,
				admitted.EvidenceRef,
			)
			if err != nil {
				return err
			}
			result = transition
			return nil
		},
	)
	if err != nil {
		return workrun.WorkTransitionV1{}, err
	}
	return result, nil
}

func (repository *ProductionRepository) validatePendingAuthorization(
	ctx context.Context,
	authorization VerificationTransitionAuthorization,
) (reviewtransaction.RARPlanAuthority, error) {
	if err := authorization.Validate(); err != nil {
		return reviewtransaction.RARPlanAuthority{}, ErrAuthorizationMismatch
	}
	state, err := repository.WorkRun.Status()
	if err != nil {
		return reviewtransaction.RARPlanAuthority{}, err
	}
	if state.WorkRunID != authorization.WorkRunID ||
		state.Revision != authorization.ExpectedRevision ||
		state.Handoff == nil ||
		state.Forecast == nil ||
		state.Disposition == nil ||
		state.Disposition.Kind != workrun.DispositionRun ||
		state.Handoff.CandidateRef != authorization.CandidateRef ||
		state.Forecast.CandidateRef != authorization.CandidateRef ||
		state.Forecast.PlanRevisionRef != authorization.ApplicableAuthorizationRef {
		return reviewtransaction.RARPlanAuthority{}, ErrAuthorizationStale
	}
	plan, err := repository.Plans.ResolvePlan(
		ctx,
		authorization.ApplicableAuthorizationRef,
	)
	if err != nil {
		return reviewtransaction.RARPlanAuthority{}, err
	}
	if plan.Subject.SnapshotIdentity != authorization.CandidateRef ||
		plan.AuthorityRef != state.Forecast.PlanRevisionRef ||
		plan.Applicability.Digest != state.Forecast.ApplicabilityDigest ||
		plan.Registry.Digest != state.Forecast.RegistryDigest ||
		plan.Plan.Digest != state.Forecast.PlanDigest {
		return reviewtransaction.RARPlanAuthority{}, ErrAuthorizationMismatch
	}
	evidenceStore, err := repository.OpenEvidence(ctx)
	if err != nil {
		return reviewtransaction.RARPlanAuthority{}, err
	}
	ticket, err := evidenceStore.ReadTicket(authorization.ActionTicketRef)
	if err != nil {
		return reviewtransaction.RARPlanAuthority{}, err
	}
	if ticket.CandidateRef != authorization.CandidateRef ||
		ticket.SubjectRef != state.Forecast.SubjectRef ||
		ticket.VerificationContextRef != state.Forecast.Digest ||
		ticket.ExpectedRevision != plan.AuthorityRef {
		return reviewtransaction.RARPlanAuthority{}, ErrAuthorizationMismatch
	}
	if !planContainsTicket(plan.Plan, ticket) {
		return reviewtransaction.RARPlanAuthority{}, ErrAuthorizationMismatch
	}
	return plan, nil
}

func (repository *ProductionRepository) complete(
	ctx context.Context,
	authorization VerificationTransitionAuthorization,
	evidenceRef string,
) (workrun.WorkTransitionV1, error) {
	status, err := repository.WorkRun.PublicStatus(ctx)
	if err != nil {
		return workrun.WorkTransitionV1{}, err
	}
	transition := workrun.WorkTransitionV1{
		Schema:                  workrun.WorkTransitionContractV1,
		Contract:                workrun.WorkTransitionContractV1,
		WorkRunID:               authorization.WorkRunID,
		PreviousRevision:        authorization.ExpectedRevision,
		Revision:                status.Revision,
		AppliedAuthorizationRef: authorization.AuthorizationRef,
		PublicState:             status.PublicState,
	}
	if err := transition.Validate(); err != nil {
		return workrun.WorkTransitionV1{}, err
	}
	if err := repository.Transitions.markCompleted(
		authorization,
		evidenceRef,
		transition,
	); err != nil {
		return workrun.WorkTransitionV1{}, err
	}
	return transition, nil
}

func (repository *ProductionRepository) validate() error {
	if repository == nil ||
		repository.Transitions == nil ||
		repository.Plans == nil ||
		repository.Executor == nil ||
		repository.OpenEvidence == nil {
		return ErrRepositoryUnavailable
	}
	return nil
}

func (repository *ProductionRepository) nowUnix() int64 {
	if repository.Now == nil {
		return time.Now().UTC().Unix()
	}
	return repository.Now().UTC().Unix()
}

func reservationForActionTicket(
	state workrun.WorkRunState,
	ticketRef string,
) (workrun.VerificationReservation, bool) {
	for _, reservation := range state.Reservations {
		if reservation.ActionTicketRef == ticketRef {
			return reservation, true
		}
	}
	return workrun.VerificationReservation{}, false
}

func launchClaimForReservation(
	state workrun.WorkRunState,
	reservationRef string,
) bool {
	for _, claim := range state.LaunchClaims {
		if claim.ReservationRef == reservationRef {
			return true
		}
	}
	return false
}

func planContainsTicket(
	plan reviewtransaction.VerificationPlan,
	ticket evidence.ActionTicket,
) bool {
	for _, obligation := range plan.Obligations {
		if obligation.ID == ticket.Slot &&
			obligation.CapabilityRef == ticket.Capability {
			return true
		}
	}
	return false
}

var _ Repository = (*ProductionRepository)(nil)
