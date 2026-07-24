package workprovider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/sddstatus"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const ownerRouteConvergenceHelper = "GENTLE_AI_OWNER_ROUTE_CONVERGENCE_HELPER"

type ownerRouteExactSemanticEvaluator struct {
	t                      *testing.T
	expectedTicketRef      string
	expectedRequirementRef string
	expectedCandidateRef   string
	calls                  int
}

func (evaluator *ownerRouteExactSemanticEvaluator) EvaluateSemanticExecution(
	_ context.Context,
	ticket evidence.ActionTicket,
	process hostruntime.ProcessEvidence,
) (SemanticEvaluation, error) {
	evaluator.t.Helper()
	evaluator.calls++
	if ticket.TicketRef != evaluator.expectedTicketRef ||
		ticket.SemanticRequirementRef != evaluator.expectedRequirementRef ||
		ticket.CandidateRef != evaluator.expectedCandidateRef ||
		process.RequestDigest != ticket.RequestBinding.RequestDigest ||
		process.SemanticRequirementRef != ticket.SemanticRequirementRef {
		return SemanticEvaluation{}, fmt.Errorf(
			"semantic evaluator received a different owner ticket/process binding",
		)
	}
	return SemanticEvaluation{
		Schema:               SemanticEvaluationSchemaV1,
		RequirementRef:       ticket.SemanticRequirementRef,
		CandidateRef:         ticket.CandidateRef,
		RequestDigest:        process.RequestDigest,
		ToolchainIdentityRef: process.ToolchainIdentityRef,
		StdoutRawDigest:      process.Stdout.RawDigest,
		StderrRawDigest:      process.Stderr.RawDigest,
		Outcome:              SemanticEvaluationPassed,
	}, nil
}

type ownerRouteSafetyGateShape struct {
	handoffSchema       string
	mutationSchema      string
	applicabilitySchema string
	registrySchema      string
	planSchema          string
	executionSchema     string
	resultSchema        string
	receiptVersion      reviewtransaction.RARNativeReceiptVersion
	resultAggregate     reviewtransaction.VerificationAggregate
	authorizationKind   workrun.DeliveryAuthorizationKind
	publicState         workrun.PublicState
	verification        workrun.VerificationOutcome
}

func TestOwnerCoordinatorImplementationRoutesConvergeThroughCommonSafetyPipeline(
	t *testing.T,
) {
	if os.Getenv(ownerRouteConvergenceHelper) == "1" {
		return
	}

	tests := []struct {
		name          string
		routeInput    workrun.ImplementationRouteInput
		acceptSDD     bool
		expectedRoute workrun.ImplementationRoute
	}{
		{
			name: "direct_inline",
			routeInput: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
			expectedRoute: workrun.ImplementationRouteDirectInline,
		},
		{
			name: "delegated_direct",
			routeInput: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAnalytical,
				WriteFileCount: 2,
			},
			expectedRoute: workrun.ImplementationRouteDelegatedDirect,
		},
		{
			name: "accepted_sdd",
			routeInput: workrun.ImplementationRouteInput{
				DurablePlanning: []workrun.DurablePlanningReason{
					workrun.PlanningArchitecture,
				},
			},
			acceptSDD:     true,
			expectedRoute: workrun.ImplementationRouteSDD,
		},
	}

	var commonShape *ownerRouteSafetyGateShape
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			shape := runOwnerRouteConvergenceJourney(
				t,
				test.name,
				test.routeInput,
				test.acceptSDD,
				test.expectedRoute,
			)
			if commonShape == nil {
				commonShape = &shape
				return
			}
			if shape != *commonShape {
				t.Fatalf(
					"safety gate shape differs by implementation route:\ngot  %#v\nwant %#v",
					shape,
					*commonShape,
				)
			}
		})
	}
}

func runOwnerRouteConvergenceJourney(
	t *testing.T,
	name string,
	routeInput workrun.ImplementationRouteInput,
	acceptSDD bool,
	expectedRoute workrun.ImplementationRoute,
) ownerRouteSafetyGateShape {
	t.Helper()
	ctx := context.Background()
	caseID := strings.ReplaceAll(name, "_", "-")
	fixture := newOwnerCoordinatorFixture(t, "route-convergence-"+caseID)
	admission := fixture.admitDefaultDelivery(t, "route-convergence-"+caseID)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"-test.run=^TestOwnerCoordinatorImplementationRoutesConvergeThroughCommonSafetyPipeline$",
	}
	environment := map[string]string{ownerRouteConvergenceHelper: "1"}
	semanticRequirement, err := evidence.ObserveSemanticRequirement(
		evidence.SemanticRequirementRequest{
			Program: executable, Args: args, CWD: fixture.repo,
			Environment: environment, Capability: "go.test",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	applicability, registry, plan, snapshot := ownerVerificationPlanWithObligation(
		t,
		fixture.repo,
		"route-convergence-"+caseID,
		reviewtransaction.VerificationObligation{
			ID:             "unit.test",
			RequirementRef: semanticRequirement.RequirementRef,
			ArgvRef:        semanticRequirement.ArgvRef,
			CapabilityRef:  "go.test",
			Cost:           reviewtransaction.VerificationCostQuick,
		},
	)
	planAuthority, err := fixture.coordinator.PublishVerificationPlan(
		ctx,
		reviewtransaction.RARPlanPublication{
			Snapshot: snapshot, Applicability: applicability,
			Registry: registry, Plan: plan,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	state, err := fixture.coordinator.StartWork(
		ctx,
		OwnerStartWorkRequest{
			DeliveryIntentRef: admission.Admission.IntentRef,
			RouteInput:        routeInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var (
		runRef     string
		sddRuntime sddstatus.RuntimeStore
	)
	if acceptSDD {
		state, err = fixture.coordinator.AcceptProposedSDD(
			ctx,
			OwnerAcceptSDDRequest{
				ExpectedRevision:      state.Revision,
				PendingDecisionDigest: state.RouteDecision.Digest,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		runRef = "route-convergence-" + caseID
		if err := os.MkdirAll(
			filepath.Join(fixture.repo, "openspec", "changes", runRef),
			0o755,
		); err != nil {
			t.Fatal(err)
		}
		sddRuntime, err = sddstatus.OpenRuntimeStore(ctx, fixture.repo, runRef)
		if err != nil {
			t.Fatal(err)
		}
		state, err = fixture.coordinator.BindAcceptedSDDRun(
			ctx,
			OwnerBindSDDRunRequest{
				ExpectedRevision:   state.Revision,
				RouteAcceptanceRef: state.RouteAcceptanceRef,
				RunRef:             runRef,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		runtimeStatus, statusErr := sddRuntime.Status()
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if _, err := sddRuntime.Begin(
			ctx,
			sddstatus.BeginAttemptRequest{
				ExpectedRevision: runtimeStatus.Revision,
				RequestID:        "begin-common-verification",
				WorkUnit:         "common-verification",
				EvidenceGoal:     "bind common WorkRun verification evidence",
				MaxAttempts:      1,
				MaxChangedLines:  100,
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	if state.ImplementationRoute != expectedRoute ||
		state.SDDRunRef != runRef {
		t.Fatalf("selected implementation route = %#v", state)
	}

	obligation := plan.Obligations[0]
	state, err = fixture.coordinator.BindImplementationHandoff(
		ctx,
		OwnerBindHandoffRequest{
			ExpectedRevision: state.Revision,
			Target: reviewtransaction.Target{
				Kind: reviewtransaction.TargetCurrentChanges,
				IntendedUntracked: append(
					[]string(nil),
					snapshot.IntendedUntracked...,
				),
			},
			IntendedPaths:          append([]string(nil), snapshot.Paths...),
			DeclaredObligationRefs: []string{obligation.RequirementRef},
			EvidenceRefs:           []string{},
			SDDRunRef:              runRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Handoff == nil ||
		state.Handoff.Schema != workrun.ImplementationHandoffSchemaV2 ||
		state.Handoff.Subject != plan.Subject ||
		state.Handoff.ScopeDigest != admission.Intent.ScopeDigest ||
		state.Handoff.SDDRunRef != runRef {
		t.Fatalf("MMI-derived handoff = %#v", state.Handoff)
	}
	handoff := *state.Handoff
	completion, err := fixture.coordinator.mutations.Read(
		ctx,
		handoff.MutationCompletionRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if completion.WorkRunID != fixture.workRunID ||
		completion.Route != string(expectedRoute) ||
		completion.Snapshot.Identity != handoff.CandidateRef {
		t.Fatalf("MMI completion = %#v", completion)
	}

	state, err = fixture.coordinator.PublishVerificationForecast(
		ctx,
		OwnerForecastRequest{
			ExpectedRevision: state.Revision,
			Handoff:          handoff,
			PlanRevisionRef:  planAuthority.AuthorityRef,
			Availability:     workrun.ForecastAvailable,
			DiagnosticRefs:   []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err = fixture.coordinator.PublishVerificationDisposition(
		ctx,
		OwnerDispositionRequest{
			ExpectedRevision: state.Revision,
			Handoff:          handoff,
			Forecast:         *state.Forecast,
			Kind:             workrun.DispositionRun,
			AssumptionsRef:   ownerTestRef("route-convergence-assumptions-" + caseID),
			ActorRef:         ownerTestRef("route-convergence-actor-" + caseID),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := fixture.coordinator.IssueVerificationActionTicket(
		ctx,
		OwnerVerificationActionRequest{
			ExpectedRevision: state.Revision,
			Ticket: evidence.SemanticActionTicketRequest{
				SubjectRef:             state.Forecast.SubjectRef,
				CandidateRef:           handoff.CandidateRef,
				VerificationContextRef: state.Forecast.Digest,
				ExpectedRevision:       planAuthority.AuthorityRef,
				Slot:                   obligation.ID,
				Program:                executable,
				Args:                   args,
				CWD:                    fixture.repo,
				Environment:            environment,
				Deadline:               5 * time.Second,
				OutputLimits: hostruntime.StreamLimits{
					StdoutBytes: 1024,
					StderrBytes: 1024,
				},
				Capability:        obligation.CapabilityRef,
				RedactionLiterals: []string{},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	transition, err := fixture.coordinator.IssueVerificationTransition(
		ctx,
		OwnerVerificationTransitionRequest{
			ExpectedRevision: state.Revision,
			ActionTicketRef:  ticket.TicketRef,
			IssuedAt:         fixture.now,
			ExpiresAt:        fixture.now + 300,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	evaluator := &ownerRouteExactSemanticEvaluator{
		t: t, expectedTicketRef: ticket.TicketRef,
		expectedRequirementRef: obligation.RequirementRef,
		expectedCandidateRef:   handoff.CandidateRef,
	}
	controller := NewController(
		ProductionRepositoryOpener{SemanticEvaluator: evaluator},
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	applied, err := controller.Apply(
		ctx,
		ApplyRequest{
			Repo: fixture.repo, WorkRunID: fixture.workRunID,
			Contract:         workrun.WorkTransitionContractV1,
			AuthorizationRef: transition.Authorization.AuthorizationRef,
			ExpectedRevision: transition.Authorization.ExpectedRevision,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Transition == nil ||
		applied.Transition.AppliedAuthorizationRef !=
			transition.Authorization.AuthorizationRef {
		t.Fatalf("production transition = %#v", applied)
	}
	admitted, err := fixture.coordinator.evidence.ReadExecutionForTicket(
		ticket.TicketRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.Outcome != evidence.ExecutionComplete ||
		admitted.SemanticProof == nil ||
		evaluator.calls != 1 {
		t.Fatalf(
			"EPD execution = outcome:%q proof:%#v evaluator calls:%d",
			admitted.Outcome,
			admitted.SemanticProof,
			evaluator.calls,
		)
	}

	if acceptSDD {
		runtimeStatus, err := sddRuntime.Status()
		if err != nil {
			t.Fatal(err)
		}
		if runtimeStatus.WorkRunBinding == nil ||
			runtimeStatus.WorkRunBinding.RunRef != runRef ||
			len(runtimeStatus.VerificationReservations) != 1 ||
			runtimeStatus.VerificationReservations[0].WorkRunID != fixture.workRunID ||
			runtimeStatus.VerificationReservations[0].ActionTicketRef != ticket.TicketRef {
			t.Fatalf("SDD common reservation binding = %#v", runtimeStatus)
		}
	} else if _, err := os.Stat(filepath.Join(
		fixture.commonDir,
		"gentle-ai",
		"sdd-runtime",
	)); !os.IsNotExist(err) {
		t.Fatalf("%s route created SDD runtime: %v", expectedRoute, err)
	}

	receiptRef := ownerApprovedCompactReceiptRef(
		t,
		fixture.repo,
		"route-convergence-"+caseID,
		snapshot,
		plan.PolicyHash,
	)
	aggregate := evidence.AggregateObligations(
		[]evidence.ObligationOutcome{evidence.ObligationPassed},
	)
	if aggregate != reviewtransaction.VerificationAggregateComplete {
		t.Fatalf("EPD aggregate = %q", aggregate)
	}
	result := reviewtransaction.VerificationResultRef{
		Schema:    reviewtransaction.VerificationResultRefSchema,
		ResultRef: ownerTestRef("route-convergence-result-" + caseID + admitted.EvidenceRef),
		Subject:   plan.Subject, PolicyHash: plan.PolicyHash,
		PlanDigest: plan.Digest, ApplicabilityDigest: plan.ApplicabilityDigest,
		Aggregate: aggregate,
		CompletedObligations: []string{
			obligation.ID,
		},
		EvidenceRefs: []string{admitted.EvidenceRef},
	}
	rarAuthority, err := fixture.coordinator.rar.Publish(
		ctx,
		reviewtransaction.RARAuthorityPublication{
			LineageID:  "route-convergence-" + caseID,
			ReceiptRef: receiptRef, Applicability: applicability,
			Registry: registry, Plan: plan, Result: result,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorizationRef := ownerPublishPADDeliveryAuthorization(
		t,
		fixture,
		admission,
		rarAuthority,
	)

	state, err = fixture.coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state, err = fixture.coordinator.BindVerificationResult(
		ctx,
		OwnerBindResultRequest{
			ExpectedRevision: state.Revision,
			ResultRef:        result.ResultRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err = fixture.coordinator.BindReviewReceipt(
		ctx,
		OwnerBindReviewRequest{
			ExpectedRevision: state.Revision,
			ReviewReceiptRef: receiptRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err = fixture.coordinator.BindDeliveryAuthorization(
		ctx,
		OwnerBindDeliveryAuthorizationRequest{
			ExpectedRevision: state.Revision,
			AuthorizationRef: authorizationRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.DeliveryAuthorizationRef != authorizationRef ||
		state.VerificationResultRef != result.ResultRef ||
		state.ReviewReceiptRef != receiptRef {
		t.Fatalf("terminal WorkRun authority = %#v", state)
	}
	deliveryAuthority, err := fixture.coordinator.pad.ResolveLiveDeliveryAuthorization(
		ctx,
		authorizationRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	public, err := controller.Status(
		ctx,
		StatusRequest{
			Repo: fixture.repo, WorkRunID: fixture.workRunID,
			Contract: workrun.WorkStatusContractV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if public.Status == nil ||
		public.Status.PublicState != workrun.PublicStateReady ||
		public.Status.Verification.Outcome != workrun.VerificationComplete ||
		public.Status.ImplementationRoute != expectedRoute ||
		public.Status.SDDRunRef != runRef ||
		public.Status.ReviewReceiptRef != receiptRef {
		t.Fatalf("public terminal status = %#v", public)
	}

	return ownerRouteSafetyGateShape{
		handoffSchema:       handoff.Schema,
		mutationSchema:      completion.Schema,
		applicabilitySchema: applicability.Schema,
		registrySchema:      registry.Schema,
		planSchema:          plan.Schema,
		executionSchema:     admitted.Schema,
		resultSchema:        result.Schema,
		receiptVersion:      rarAuthority.Receipt.Version,
		resultAggregate:     result.Aggregate,
		authorizationKind:   deliveryAuthority.Kind,
		publicState:         public.Status.PublicState,
		verification:        public.Status.Verification.Outcome,
	}
}
