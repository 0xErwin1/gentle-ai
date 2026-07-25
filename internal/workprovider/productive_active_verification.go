package workprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

var (
	errProductiveActivePlanRequired = errors.New(
		"productive active verification requires an owner catalog",
	)
	errProductiveActiveConsentRequired = errors.New(
		"productive active verification requires explicit consent",
	)
	errProductiveActiveReviewRequired = errors.New(
		"productive active verification requires a review decision",
	)
	errProductiveVerificationApplicabilityUnknown = errors.New(
		"productive verification applicability is unknown",
	)
	errProductiveReviewRiskUnknown = errors.New(
		"productive review risk is unknown",
	)
	errProductiveReviewLensesUnknown = errors.New(
		"productive review lenses are unknown",
	)
)

type productiveVerificationPreparation struct {
	Applicability reviewtransaction.VerificationApplicability
	Registry      reviewtransaction.VerificationPlanRegistry
	Plan          reviewtransaction.VerificationPlan
	Assessment    reviewtransaction.RiskAssessment
	Lenses        []string
	Actions       []ProductiveVerificationAction
	Automatic     bool
}

func prepareProductiveVerification(
	ctx context.Context,
	factory *ProductionOwnerCoordinatorFactory,
	candidate reviewtransaction.Snapshot,
	policyHash string,
) (productiveVerificationPreparation, error) {
	builder := reviewtransaction.SnapshotBuilder{
		Repo: factory.repositoryRoot(),
	}
	assessment, err := builder.AssessSnapshotRisk(ctx, candidate)
	if err != nil {
		return productiveVerificationPreparation{}, fmt.Errorf(
			"%w: %v",
			errProductiveReviewRiskUnknown,
			err,
		)
	}
	lenses, err := reviewtransaction.SelectReviewLenses(
		assessment,
		"reliability",
	)
	if err != nil {
		return productiveVerificationPreparation{}, fmt.Errorf(
			"%w: %v",
			errProductiveReviewLensesUnknown,
			err,
		)
	}
	emptyRegistry, err := reviewtransaction.NewVerificationPlanRegistry(
		policyHash,
		[]string{},
		[]reviewtransaction.VerificationObligation{},
	)
	if err != nil {
		return productiveVerificationPreparation{}, fmt.Errorf(
			"%w: %v",
			errProductiveVerificationApplicabilityUnknown,
			err,
		)
	}
	applicability, err := builder.ClassifyVerificationApplicability(
		ctx,
		candidate,
		emptyRegistry,
		[]string{},
	)
	if err != nil {
		return productiveVerificationPreparation{}, err
	}
	preparation := productiveVerificationPreparation{
		Applicability: applicability,
		Registry:      emptyRegistry,
		Assessment:    assessment,
		Lenses:        append([]string(nil), lenses...),
		Actions:       []ProductiveVerificationAction{},
	}
	switch applicability.Decision {
	case reviewtransaction.VerificationNotApplicable:
		preparation.Plan, err = reviewtransaction.BuildVerificationPlan(
			applicability,
			emptyRegistry,
		)
		return preparation, err
	case reviewtransaction.VerificationUnknown:
		return productiveVerificationPreparation{},
			errProductiveVerificationApplicabilityUnknown
	case reviewtransaction.VerificationApplicable:
	default:
		return productiveVerificationPreparation{}, errors.New(
			"productive verification classifier returned an unsupported decision",
		)
	}
	if factory.active == nil {
		return productiveVerificationPreparation{},
			errProductiveActivePlanRequired
	}
	subject, err := reviewtransaction.VerificationSubjectFromSnapshot(
		candidate,
	)
	if err != nil {
		return productiveVerificationPreparation{}, fmt.Errorf(
			"%w: %v",
			errProductiveVerificationApplicabilityUnknown,
			err,
		)
	}
	request, err := NewProductiveVerificationCatalogRequest(
		subject,
		candidate.Paths,
		assessment,
	)
	if err != nil {
		return productiveVerificationPreparation{}, err
	}
	catalog, err := factory.active.ResolveVerificationCatalog(ctx, request)
	if err != nil {
		return productiveVerificationPreparation{}, err
	}
	if err := catalog.ValidateFor(request); err != nil {
		return productiveVerificationPreparation{}, err
	}
	if len(catalog.Actions) == 0 {
		return productiveVerificationPreparation{},
			errProductiveActivePlanRequired
	}
	obligations := make(
		[]reviewtransaction.VerificationObligation,
		0,
		len(catalog.Actions),
	)
	for _, action := range catalog.Actions {
		observation, err := evidence.ObserveSemanticRequirement(
			evidence.SemanticRequirementRequest{
				Program: action.Program,
				Args:    append([]string(nil), action.Args...),
				CWD:     action.CWD,
				Environment: cloneProductiveEnvironment(
					action.Environment,
				),
				Capability: action.Capability,
			},
		)
		if err != nil {
			return productiveVerificationPreparation{}, err
		}
		obligations = append(
			obligations,
			reviewtransaction.VerificationObligation{
				ID:             action.ID,
				RequirementRef: observation.RequirementRef,
				ArgvRef:        observation.ArgvRef,
				CapabilityRef:  action.Capability,
				Cost:           action.Cost,
			},
		)
	}
	registry, err := reviewtransaction.NewVerificationPlanRegistry(
		policyHash,
		[]string{},
		obligations,
	)
	if err != nil {
		return productiveVerificationPreparation{}, err
	}
	applicability, err = builder.ClassifyVerificationApplicability(
		ctx,
		candidate,
		registry,
		[]string{},
	)
	if err != nil {
		return productiveVerificationPreparation{}, err
	}
	if applicability.Decision != reviewtransaction.VerificationApplicable {
		return productiveVerificationPreparation{}, errors.New(
			"owner verification catalog changed native applicability",
		)
	}
	plan, err := reviewtransaction.BuildVerificationPlan(
		applicability,
		registry,
	)
	if err != nil {
		return productiveVerificationPreparation{}, err
	}
	automatic := true
	for _, action := range catalog.Actions {
		if action.Cost != reviewtransaction.VerificationCostQuick ||
			action.RequiresConsent {
			automatic = false
		}
	}
	preparation.Applicability = applicability
	preparation.Registry = registry
	preparation.Plan = plan
	preparation.Actions = append(
		[]ProductiveVerificationAction(nil),
		catalog.Actions...,
	)
	preparation.Automatic = automatic
	return preparation, nil
}

func prepareProductiveAdvanceVerification(
	ctx context.Context,
	factory *ProductionOwnerCoordinatorFactory,
	candidate reviewtransaction.Snapshot,
	policyHash string,
) (
	productiveVerificationPreparation,
	workrun.WorkAdvanceDiagnosticCode,
	error,
) {
	preparation, err := prepareProductiveVerification(
		ctx,
		factory,
		candidate,
		policyHash,
	)
	if err == nil {
		return preparation, "", nil
	}
	if ctx.Err() != nil {
		return productiveVerificationPreparation{}, "", ctx.Err()
	}
	switch {
	case errors.Is(err, errProductiveVerificationApplicabilityUnknown):
		return productiveVerificationPreparation{},
			workrun.WorkAdvanceDiagnosticVerificationApplicabilityUnknown,
			nil
	case errors.Is(err, errProductiveReviewRiskUnknown):
		return productiveVerificationPreparation{},
			workrun.WorkAdvanceDiagnosticReviewRiskUnknown,
			nil
	case errors.Is(err, errProductiveReviewLensesUnknown):
		return productiveVerificationPreparation{},
			workrun.WorkAdvanceDiagnosticReviewLensesUnknown,
			nil
	case errors.Is(err, errProductiveActivePlanRequired):
		return productiveVerificationPreparation{},
			workrun.WorkAdvanceDiagnosticCompleteVerificationPlanRequired,
			nil
	default:
		return productiveVerificationPreparation{},
			workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable,
			nil
	}
}

func productivePreparationNextAction(
	code workrun.WorkAdvanceDiagnosticCode,
) workrun.WorkAdvanceDiagnosticNextAction {
	if code == workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable {
		return workrun.WorkAdvanceNextActionReconcile
	}
	return workrun.WorkAdvanceNextActionStartFresh
}

func productiveVerificationRequirementRefs(
	plan reviewtransaction.VerificationPlan,
) []string {
	refs := make([]string, 0, len(plan.Obligations))
	for _, obligation := range plan.Obligations {
		refs = append(refs, obligation.RequirementRef)
	}
	sort.Strings(refs)
	return refs
}

func validateProductivePreparedHandoff(
	handoff *workrun.ImplementationHandoff,
	candidate reviewtransaction.Snapshot,
	plan reviewtransaction.VerificationPlan,
) error {
	subject, err := reviewtransaction.VerificationSubjectFromSnapshot(
		candidate,
	)
	if err != nil {
		return err
	}
	if handoff == nil ||
		handoff.Subject != subject ||
		handoff.CandidateRef != candidate.Identity ||
		!equalProductiveStrings(
			handoff.DeclaredObligationRefs,
			productiveVerificationRequirementRefs(plan),
		) {
		return errors.New(
			"productive handoff does not bind the prepared verification plan",
		)
	}
	return nil
}

func productiveForecastAvailability(
	preparation productiveVerificationPreparation,
) workrun.ForecastAvailability {
	if preparation.Applicability.Decision ==
		reviewtransaction.VerificationNotApplicable {
		return workrun.ForecastNotRequired
	}
	return workrun.ForecastAvailable
}

func productiveVerificationExplicitConsent(
	preparation productiveVerificationPreparation,
) bool {
	if preparation.Applicability.Decision !=
		reviewtransaction.VerificationApplicable {
		return false
	}
	for _, action := range preparation.Actions {
		if action.RequiresConsent {
			return true
		}
	}
	return false
}

func productiveVerificationNeedsDecision(
	preparation productiveVerificationPreparation,
) bool {
	return preparation.Applicability.Decision ==
		reviewtransaction.VerificationApplicable &&
		!preparation.Automatic
}

func validateProductivePreparedForecast(
	ctx context.Context,
	coordinator *OwnerCoordinator,
	state workrun.WorkRunState,
	planAuthority reviewtransaction.RARPlanAuthority,
	preparation productiveVerificationPreparation,
) error {
	if state.Handoff == nil || state.Forecast == nil {
		return errors.New(
			"productive verification forecast is missing its handoff",
		)
	}
	availability := productiveForecastAvailability(preparation)
	forecast := state.Forecast
	if forecast.HandoffDigest != state.Handoff.Digest ||
		forecast.CandidateRef != state.Handoff.CandidateRef ||
		forecast.ApplicabilityDigest != preparation.Applicability.Digest ||
		forecast.RegistryDigest != preparation.Registry.Digest ||
		forecast.PlanDigest != preparation.Plan.Digest ||
		forecast.PlanRevisionRef != planAuthority.AuthorityRef ||
		forecast.Availability != availability ||
		forecast.RequiresConsent !=
			productiveVerificationExplicitConsent(preparation) ||
		len(forecast.DiagnosticRefs) != 0 {
		return errors.New(
			"productive verification forecast differs from its prepared authority",
		)
	}
	authority, err := coordinator.coordination.ResolveForecast(
		ctx,
		forecast.AvailabilityRef,
	)
	if err != nil {
		return err
	}
	return authority.MatchesInput(workrun.VerificationForecastInput{
		WorkRunID:       state.WorkRunID,
		Handoff:         *state.Handoff,
		Applicability:   preparation.Applicability,
		Registry:        preparation.Registry,
		Plan:            preparation.Plan,
		PlanRevisionRef: planAuthority.AuthorityRef,
		Availability:    availability,
		RequiresConsent: productiveVerificationExplicitConsent(preparation),
		AvailabilityRef: forecast.AvailabilityRef,
		DiagnosticRefs:  []string{},
	})
}

func executeProductiveActiveVerification(
	ctx context.Context,
	factory *ProductionOwnerCoordinatorFactory,
	coordinator *OwnerCoordinator,
	state workrun.WorkRunState,
	planAuthority reviewtransaction.RARPlanAuthority,
	preparation productiveVerificationPreparation,
) (
	workrun.WorkRunState,
	[]evidence.ExecutionEvidence,
	reviewtransaction.VerificationAggregate,
	error,
) {
	if !preparation.Automatic && state.Disposition == nil {
		return state, nil, "", errProductiveActiveConsentRequired
	}
	if factory.semantic == nil {
		return state, nil, "", errProductiveActivePlanRequired
	}
	if state.Disposition == nil {
		assumptionsRef, err := productiveVerificationAssumptionsRef(
			preparation,
		)
		if err != nil {
			return workrun.WorkRunState{}, nil, "", err
		}
		actorRef := productiveAdvanceSHA256(
			[]byte(
				"gentle-ai.productive-quick-actor/v1\x00" +
					factory.RepositoryRef(),
			),
		)
		state, err = coordinator.PublishVerificationDisposition(
			ctx,
			OwnerDispositionRequest{
				ExpectedRevision: state.Revision,
				Handoff:          *state.Handoff,
				Forecast:         *state.Forecast,
				Kind:             workrun.DispositionRun,
				AssumptionsRef:   assumptionsRef,
				ActorRef:         actorRef,
			},
		)
		if err != nil {
			return workrun.WorkRunState{}, nil, "", err
		}
	}
	if state.Disposition.Kind != workrun.DispositionRun {
		return state, nil, "", errProductiveActiveConsentRequired
	}
	if !preparation.Automatic {
		assumptionsRef, err := productiveVerificationAssumptionsRef(
			preparation,
		)
		if err != nil {
			return workrun.WorkRunState{}, nil, "", err
		}
		if state.Disposition.AssumptionsRef != assumptionsRef {
			return workrun.WorkRunState{}, nil, "",
				ErrProviderResultMismatch
		}
	}
	actionByID := make(
		map[string]ProductiveVerificationAction,
		len(preparation.Actions),
	)
	for _, action := range preparation.Actions {
		actionByID[action.ID] = action
	}
	executions := make(
		[]evidence.ExecutionEvidence,
		0,
		len(preparation.Plan.Obligations),
	)
	outcomes := make(
		[]evidence.ObligationOutcome,
		0,
		len(preparation.Plan.Obligations),
	)
	controller := NewController(
		ProductionRepositoryOpener{
			SemanticEvaluator: factory.semantic,
		},
		factory.activation,
	)
	for _, obligation := range preparation.Plan.Obligations {
		action, ok := actionByID[obligation.ID]
		if !ok {
			return workrun.WorkRunState{}, nil, "", errors.New(
				"productive verification plan lost its owner action",
			)
		}
		ticket, err := coordinator.IssueVerificationActionTicket(
			ctx,
			OwnerVerificationActionRequest{
				ExpectedRevision: state.Revision,
				Ticket: evidence.SemanticActionTicketRequest{
					SubjectRef:             state.Forecast.SubjectRef,
					CandidateRef:           state.Handoff.CandidateRef,
					VerificationContextRef: state.Forecast.Digest,
					ExpectedRevision:       planAuthority.AuthorityRef,
					Slot:                   obligation.ID,
					Program:                action.Program,
					Args: append(
						[]string(nil),
						action.Args...,
					),
					CWD: action.CWD,
					Environment: cloneProductiveEnvironment(
						action.Environment,
					),
					Deadline:     action.deadline(),
					OutputLimits: action.OutputLimits,
					Capability:   action.Capability,
					RedactionLiterals: append(
						[]string(nil),
						action.RedactionLiterals...,
					),
				},
			},
		)
		if err != nil {
			return workrun.WorkRunState{}, nil, "", err
		}
		admitted, evidenceErr :=
			coordinator.evidence.ReadExecutionForTicket(
				ticket.TicketRef,
			)
		if errors.Is(evidenceErr, evidence.ErrEvidenceNotFound) {
			now := time.Now().UTC().Unix()
			expiresAt := now +
				int64(action.deadline()/time.Second) +
				300
			transition, err :=
				coordinator.IssueVerificationTransition(
					ctx,
					OwnerVerificationTransitionRequest{
						ExpectedRevision: state.Revision,
						ActionTicketRef:  ticket.TicketRef,
						IssuedAt:         now,
						ExpiresAt:        expiresAt,
					},
				)
			if err != nil {
				return workrun.WorkRunState{}, nil, "", err
			}
			if _, err := controller.Apply(
				ctx,
				ApplyRequest{
					Repo:      factory.repositoryRoot(),
					WorkRunID: state.WorkRunID,
					Contract:  workrun.WorkTransitionContractV1,
					AuthorizationRef: transition.Authorization.
						AuthorizationRef,
					ExpectedRevision: transition.Authorization.
						ExpectedRevision,
				},
			); err != nil {
				return workrun.WorkRunState{}, nil, "", err
			}
			admitted, evidenceErr =
				coordinator.evidence.ReadExecutionForTicket(
					ticket.TicketRef,
				)
		}
		if evidenceErr != nil {
			return workrun.WorkRunState{}, nil, "", evidenceErr
		}
		if admitted.TicketRef != ticket.TicketRef ||
			admitted.Slot != obligation.ID ||
			admitted.CandidateRef != state.Handoff.CandidateRef {
			return workrun.WorkRunState{}, nil, "", errors.New(
				"productive execution evidence does not bind its planned obligation",
			)
		}
		executions = append(executions, admitted)
		switch admitted.Outcome {
		case evidence.ExecutionComplete:
			outcomes = append(outcomes, evidence.ObligationPassed)
		case evidence.ExecutionFailed:
			outcomes = append(outcomes, evidence.ObligationFailed)
		default:
			outcomes = append(outcomes, evidence.ObligationUnavailable)
		}
		state, err = coordinator.Status(ctx)
		if err != nil {
			return workrun.WorkRunState{}, nil, "", err
		}
	}
	return state, executions, evidence.AggregateObligations(outcomes), nil
}

func publishNativeActiveRAR(
	ctx context.Context,
	factory *ProductionOwnerCoordinatorFactory,
	coordinator *OwnerCoordinator,
	lineageID string,
	snapshot reviewtransaction.Snapshot,
	preparation productiveVerificationPreparation,
	executions []evidence.ExecutionEvidence,
	aggregate reviewtransaction.VerificationAggregate,
) (string, string, error) {
	evidenceRefs := make([]string, 0, len(executions))
	completed := make([]string, 0, len(executions))
	for _, execution := range executions {
		evidenceRefs = append(evidenceRefs, execution.EvidenceRef)
		if execution.Outcome == evidence.ExecutionComplete {
			completed = append(completed, execution.Slot)
		}
	}
	sort.Strings(evidenceRefs)
	sort.Strings(completed)
	result := reviewtransaction.VerificationResultRef{
		Schema: reviewtransaction.VerificationResultRefSchema,
		ResultRef: productiveActiveResultRef(
			lineageID,
			preparation.Plan,
			aggregate,
			evidenceRefs,
		),
		Subject:              preparation.Plan.Subject,
		PolicyHash:           preparation.Plan.PolicyHash,
		PlanDigest:           preparation.Plan.Digest,
		ApplicabilityDigest:  preparation.Plan.ApplicabilityDigest,
		Aggregate:            aggregate,
		CompletedObligations: completed,
		EvidenceRefs:         evidenceRefs,
	}
	if err := reviewtransaction.ValidateVerificationResultRef(
		preparation.Applicability,
		preparation.Registry,
		preparation.Plan,
		result,
	); err != nil {
		return "", "", err
	}
	state, err := reviewtransaction.NewCompactState(
		reviewtransaction.Start{
			LineageID:  lineageID,
			Mode:       reviewtransaction.ModeOrdinaryBounded,
			Generation: 1,
			Snapshot:   snapshot,
			PolicyHash: preparation.Plan.PolicyHash,
			RiskLevel:  preparation.Assessment.Level,
			SelectedLenses: append(
				[]string(nil),
				preparation.Lenses...,
			),
			OriginalChangedLines: &preparation.Assessment.ChangedLines,
		},
	)
	if err != nil {
		return "", "", err
	}
	start, err := reviewtransaction.StartCompactAuthority(
		ctx,
		factory.repositoryRoot(),
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
		return "", "", errProductiveActiveReviewRequired
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(
		ctx,
		factory.repositoryRoot(),
		lineageID,
	)
	if err != nil {
		return "", "", err
	}
	record := start.Record
	if !reviewtransaction.SnapshotsEqualExact(
		record.State.InitialSnapshot,
		snapshot,
	) ||
		record.State.PolicyHash != preparation.Plan.PolicyHash ||
		record.State.RiskLevel != preparation.Assessment.Level ||
		!equalProductiveStrings(
			record.State.SelectedLenses,
			preparation.Lenses,
		) {
		return "", "", errors.New(
			"productive active review authority differs from the frozen candidate",
		)
	}
	if record.State.State == reviewtransaction.StateReviewing {
		frozen, err := (reviewtransaction.SnapshotBuilder{
			Repo: factory.repositoryRoot(),
		}).FrozenCandidateContext(ctx, snapshot)
		if err != nil {
			return "", "", err
		}
		results := make(
			[]reviewtransaction.LensResult,
			0,
			len(record.State.SelectedLenses),
		)
		for order, lens := range record.State.SelectedLenses {
			subject, err := reviewtransaction.NewArtifactSubject(
				record.State,
				record.Revision,
				frozen,
				lens,
				order,
				"",
			)
			if err != nil {
				return "", "", err
			}
			admitted, found, err :=
				store.ResolveAdmittedReviewerResult(
					ctx,
					record.Revision,
					snapshot.Identity,
					frozen,
					subject,
				)
			if err != nil {
				return "", "", err
			}
			if found {
				results = append(results, admitted)
				continue
			}
			request, err := NewProductiveReviewRequest(
				subject,
				frozen,
			)
			if err != nil {
				return "", "", err
			}
			reviewed, err := factory.active.ReviewCandidate(
				ctx,
				request,
			)
			if err != nil {
				return "", "", err
			}
			if err := reviewed.ValidateFor(request); err != nil {
				return "", "", err
			}
			raw, err := json.Marshal(reviewed)
			if err != nil {
				return "", "", err
			}
			raw = append(raw, '\n')
			native := reviewtransaction.LensResult{
				Lens: lens,
				Findings: append(
					[]reviewtransaction.Finding{},
					reviewed.Findings...,
				),
				Evidence: append(
					[]string{},
					reviewed.Evidence...,
				),
			}
			causal, err := productiveCandidateCausalFindingIDs(
				ctx,
				factory.repositoryRoot(),
				snapshot,
				native,
			)
			if err != nil {
				return "", "", err
			}
			admitted, err =
				store.CaptureAdmittedReviewerResult(
					ctx,
					reviewtransaction.CompactAdmittedReviewerResultRequest{
						ExpectedRevision:          record.Revision,
						TargetIdentity:            snapshot.Identity,
						FrozenContext:             frozen,
						ArtifactSubject:           subject,
						Inspection:                reviewed.Inspection,
						Result:                    native,
						CandidateCausalFindingIDs: causal,
						RawPayload:                raw,
					},
				)
			if err != nil {
				return "", "", err
			}
			results = append(results, admitted)
		}
		next := record.State
		if err := next.CompleteReview(
			reviewtransaction.CompactReviewInput{
				LensResults: results,
				Classifications: productiveReviewClassifications(
					results,
				),
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
	switch record.State.State {
	case reviewtransaction.StateCorrectionRequired,
		reviewtransaction.StateEscalated:
		return "", "", errProductiveActiveReviewRequired
	case reviewtransaction.StateValidating:
		finalEvidence, err := productiveActiveFinalEvidence(
			snapshot,
			result,
		)
		if err != nil {
			return "", "", err
		}
		next := record.State
		if err := next.CompleteVerification(
			finalEvidence,
			aggregate == reviewtransaction.VerificationAggregateComplete,
		); err != nil {
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
		return "", "", errProductiveActiveReviewRequired
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
	authority, err := coordinator.rar.Publish(
		ctx,
		reviewtransaction.RARAuthorityPublication{
			LineageID:     lineageID,
			ReceiptRef:    receiptRef,
			Applicability: preparation.Applicability,
			Registry:      preparation.Registry,
			Plan:          preparation.Plan,
			Result:        result,
		},
	)
	if err != nil {
		return "", "", err
	}
	if authority.Receipt.ReceiptRef != receiptRef ||
		authority.Result.ResultRef != result.ResultRef {
		return "", "", ErrProviderResultMismatch
	}
	return receiptRef, result.ResultRef, nil
}

func productiveCandidateCausalFindingIDs(
	ctx context.Context,
	repo string,
	snapshot reviewtransaction.Snapshot,
	result reviewtransaction.LensResult,
) ([]string, error) {
	ids := make([]string, 0)
	builder := reviewtransaction.SnapshotBuilder{Repo: repo}
	for _, finding := range result.Findings {
		switch finding.CausalDisposition {
		case reviewtransaction.CausalIntroduced,
			reviewtransaction.CausalBehaviorActivated,
			reviewtransaction.CausalWorsened:
			supported, err := builder.CandidateLocationSupportsCausality(
				ctx,
				snapshot,
				finding.Location,
				finding.CausalDisposition,
			)
			if err != nil {
				return nil, err
			}
			if !supported {
				return nil, errors.New(
					"productive reviewer claimed candidate causality outside changed evidence",
				)
			}
			ids = append(ids, finding.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func productiveReviewClassifications(
	results []reviewtransaction.LensResult,
) []reviewtransaction.FindingEvidence {
	classifications := make(
		[]reviewtransaction.FindingEvidence,
		0,
	)
	for _, result := range results {
		for _, finding := range result.Findings {
			if finding.Severity != "BLOCKER" &&
				finding.Severity != "CRITICAL" {
				continue
			}
			classifications = append(
				classifications,
				reviewtransaction.FindingEvidence{
					FindingID: finding.ID,
					Class:     finding.EvidenceClass,
					Causality: finding.CausalDisposition,
					Proof: strings.Join(
						finding.ProofRefs,
						"; ",
					),
				},
			)
		}
	}
	sort.Slice(
		classifications,
		func(left, right int) bool {
			return classifications[left].FindingID <
				classifications[right].FindingID
		},
	)
	return classifications
}

func productiveActiveFinalEvidence(
	snapshot reviewtransaction.Snapshot,
	result reviewtransaction.VerificationResultRef,
) ([]byte, error) {
	value := struct {
		Schema           string   `json:"schema"`
		SnapshotIdentity string   `json:"snapshotIdentity"`
		PlanDigest       string   `json:"planDigest"`
		ResultRef        string   `json:"resultRef"`
		Aggregate        string   `json:"aggregate"`
		EvidenceRefs     []string `json:"evidenceRefs"`
	}{
		Schema:           "gentle-ai.productive-active-final-evidence/v1",
		SnapshotIdentity: snapshot.Identity,
		PlanDigest:       result.PlanDigest,
		ResultRef:        result.ResultRef,
		Aggregate:        string(result.Aggregate),
		EvidenceRefs: append(
			[]string(nil),
			result.EvidenceRefs...,
		),
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func productiveActiveResultRef(
	lineageID string,
	plan reviewtransaction.VerificationPlan,
	aggregate reviewtransaction.VerificationAggregate,
	evidenceRefs []string,
) string {
	value := struct {
		Schema       string   `json:"schema"`
		LineageID    string   `json:"lineageId"`
		PlanDigest   string   `json:"planDigest"`
		Aggregate    string   `json:"aggregate"`
		EvidenceRefs []string `json:"evidenceRefs"`
	}{
		Schema:       "gentle-ai.productive-active-result/v1",
		LineageID:    lineageID,
		PlanDigest:   plan.Digest,
		Aggregate:    string(aggregate),
		EvidenceRefs: append([]string(nil), evidenceRefs...),
	}
	payload, _ := json.Marshal(value)
	return productiveAdvanceSHA256(payload)
}

func cloneProductiveEnvironment(
	source map[string]string,
) map[string]string {
	cloned := make(map[string]string, len(source))
	for name, value := range source {
		cloned[name] = value
	}
	return cloned
}
