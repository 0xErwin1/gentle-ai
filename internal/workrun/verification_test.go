package workrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

func TestWorkRunBeginAtomicallyReservesExactPlanAndTicket(t *testing.T) {
	repo := initWorkRunRepository(t)
	applicability, registry, plan := verificationFixture(t, repo, true)
	store := openTestWorkRunStore(t, repo, "reservation")
	state := startDirectWorkRun(t, store)

	requirements := verificationRequirementRefs(plan)
	handoff := testHandoffForSnapshot(
		t,
		store,
		ImplementationRouteDirectInline,
		testSHARef("reservation-scope"),
		currentWorkRunSnapshot(t, repo),
		requirements,
		[]string{},
		"",
	)
	var err error
	state, err = store.BindImplementationHandoff(context.Background(), BindImplementationHandoffRequest{
		ExpectedRevision: state.Revision, RequestID: "handoff",
		Handoff: handoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	forecastInput := VerificationForecastInput{
		WorkRunID: store.WorkRunID, Handoff: handoff,
		Applicability: applicability, Registry: registry, Plan: plan,
		PlanRevisionRef: testSHARef("plan-revision"), Availability: ForecastAvailable,
		AvailabilityRef: testSHARef("available-observation"), DiagnosticRefs: []string{},
	}
	registerTestForecast(t, store, forecastInput)
	state, err = store.RecordVerificationForecast(context.Background(), RecordVerificationForecastRequest{
		ExpectedRevision: state.Revision, RequestID: "forecast", Input: forecastInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispositionRequest := RecordVerificationDispositionRequest{
		ExpectedRevision: state.Revision, RequestID: "disposition", Kind: DispositionRun,
		AssumptionsRef: testSHARef("assumptions"), ActorRef: testSHARef("provider"),
		DecisionRef: testSHARef("automatic-quick"),
	}
	if _, err := store.RecordVerificationDisposition(context.Background(), dispositionRequest); err == nil {
		t.Fatal("hash-shaped but unowned verification disposition was accepted")
	}
	registerTestDisposition(t, store, *state.Forecast, dispositionRequest)
	state, err = store.RecordVerificationDisposition(context.Background(), dispositionRequest)
	if err != nil {
		t.Fatal(err)
	}
	beforeBeginRevision := state.Revision
	obligation := plan.Obligations[0]
	step, markerPath := testWorkRunExecStep(t)
	hostBinding, err := hostruntime.BindExecStep(step)
	if err != nil {
		t.Fatal(err)
	}
	ticket := AdmittedActionTicket{
		TicketRef: testSHARef("ticket-one"), SubjectRef: applicability.Digest,
		CandidateRef: handoff.CandidateRef, VerificationContextRef: state.Forecast.Digest,
		ExpectedRevision: forecastInput.PlanRevisionRef, Slot: obligation.ID,
		Capability: obligation.CapabilityRef, SlotBindingRef: testSHARef("slot-one"),
		HostRequestDigest: hostBinding.RequestDigest,
	}
	port := &fakeEvidencePort{
		tickets:    map[string]AdmittedActionTicket{ticket.TicketRef: ticket},
		executions: map[string]AdmittedExecutionEvidence{},
	}
	if _, err := store.Begin(context.Background(), BeginRequest{
		ExpectedRevision: beforeBeginRevision, RequestID: "begin-without-port",
		Applicability: applicability, Registry: registry, Plan: plan,
		ActionTicketRef: ticket.TicketRef,
	}); !errors.Is(err, ErrEvidencePortUnavailable) {
		t.Fatalf("begin without EPD port error = %v", err)
	}
	unchanged, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != beforeBeginRevision || len(unchanged.Reservations) != 0 {
		t.Fatalf("failed begin mutated journal: %#v", unchanged)
	}
	executor := testExecutorForStore(t, store)
	if _, err := executor.ExecuteAuthorized(context.Background(), step, nil); !errors.Is(
		err,
		hostruntime.ErrLaunchCapabilityRequired,
	) {
		t.Fatalf("execution before Begin error = %v, want capability required", err)
	}
	if _, err := os.Lstat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("execution before Begin created process side effect: %v", err)
	}

	store = store.WithEvidencePort(port)
	beginRequest := BeginRequest{
		ExpectedRevision: beforeBeginRevision, RequestID: "begin-one",
		Applicability: applicability, Registry: registry, Plan: plan,
		ActionTicketRef: ticket.TicketRef,
	}
	outcome, err := store.Begin(context.Background(), beginRequest)
	if err != nil {
		t.Fatal(err)
	}
	state = outcome.State
	if outcome.Capability == nil {
		t.Fatal("successful Begin omitted the one-shot HCR launch capability")
	}
	if len(state.Reservations) != 1 || state.NextOrdinal != 2 {
		t.Fatalf("reservation state = %#v", state)
	}
	reservation := state.Reservations[0]
	if reservation.WorkRunID != store.WorkRunID ||
		reservation.ExpectedWorkRevision != beforeBeginRevision ||
		reservation.Ordinal != 1 || reservation.ActionTicketRef != ticket.TicketRef ||
		reservation.PlanPreimageRef != plan.Digest ||
		reservation.PlanRevisionRef != forecastInput.PlanRevisionRef ||
		reservation.ActorRef != testSHARef("provider") ||
		reservation.DecisionRef != testSHARef("automatic-quick") {
		t.Fatalf("reservation omitted exact begin facts: %#v", reservation)
	}
	process, err := executor.ExecuteAuthorized(context.Background(), step, outcome.Capability)
	if err != nil {
		t.Fatalf("authorized HCR execution failed: %v", err)
	}
	if process.TerminalCause != hostruntime.TerminalExited || process.ExitCode != 0 {
		t.Fatalf("authorized process evidence = %#v", process)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("authorized HCR execution omitted process side effect: %v", err)
	}
	if _, err := executor.ExecuteAuthorized(
		context.Background(),
		step,
		outcome.Capability,
	); !errors.Is(err, hostruntime.ErrLaunchCapabilityConsumed) {
		t.Fatalf("one-shot capability replay error = %v", err)
	}
	claimedState, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(claimedState.LaunchClaims) != 1 ||
		claimedState.LaunchClaims[0].ReservationRef != reservation.ReservationRef {
		t.Fatalf("authorized process lacks exact durable launch claim: %#v", claimedState)
	}
	if len(testAuthorityForStore(t, store).sddBindings) != 0 {
		t.Fatal("direct verification attempted to bind an SDD phase")
	}

	replayStore := openTestWorkRunStore(t, repo, "reservation").
		WithEvidencePort(port).
		WithAuthorityPorts(store.authority)
	replayed, err := replayStore.Begin(context.Background(), beginRequest)
	if !errors.Is(err, ErrVerificationLaunchClaimed) {
		t.Fatalf("exact Begin replay after launch = %#v, %v", replayed, err)
	}

	second := ticket
	second.TicketRef = testSHARef("ticket-two")
	second.SlotBindingRef = testSHARef("slot-two")
	port.tickets[second.TicketRef] = second
	if _, err := store.Begin(context.Background(), BeginRequest{
		ExpectedRevision: claimedState.Revision, RequestID: "begin-duplicate-slot",
		Applicability: applicability, Registry: registry, Plan: plan,
		ActionTicketRef: second.TicketRef,
	}); !errors.Is(err, ErrVerificationReserved) {
		t.Fatalf("duplicate slot begin error = %v", err)
	}
	final, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if final.Revision != claimedState.Revision || len(final.Reservations) != 1 ||
		final.NextOrdinal != 2 {
		t.Fatalf("rejected duplicate consumed state: %#v", final)
	}
}

func TestWorkRunBeginRejectsTerminalResultWithoutAdvancingHead(t *testing.T) {
	repo := initWorkRunRepository(t)
	applicability, registry, plan := verificationFixture(t, repo, true)
	store := openTestWorkRunStore(t, repo, "terminal-begin")
	state := startDirectWorkRun(t, store)

	handoff := testHandoffForSnapshot(
		t,
		store,
		ImplementationRouteDirectInline,
		testSHARef("terminal-begin-scope"),
		currentWorkRunSnapshot(t, repo),
		verificationRequirementRefs(plan),
		[]string{},
		"",
	)
	var err error
	state, err = store.BindImplementationHandoff(
		context.Background(),
		BindImplementationHandoffRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "terminal-begin-handoff",
			Handoff:          handoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	forecastInput := VerificationForecastInput{
		WorkRunID: store.WorkRunID, Handoff: handoff,
		Applicability: applicability, Registry: registry, Plan: plan,
		PlanRevisionRef: testSHARef("terminal-begin-plan-revision"),
		Availability:    ForecastAvailable,
		AvailabilityRef: testSHARef("terminal-begin-availability"),
		DiagnosticRefs:  []string{},
	}
	registerTestForecast(t, store, forecastInput)
	state, err = store.RecordVerificationForecast(
		context.Background(),
		RecordVerificationForecastRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "terminal-begin-forecast",
			Input:            forecastInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	dispositionRequest := RecordVerificationDispositionRequest{
		ExpectedRevision: state.Revision,
		RequestID:        "terminal-begin-disposition",
		Kind:             DispositionRun,
		AssumptionsRef:   testSHARef("terminal-begin-assumptions"),
		ActorRef:         testSHARef("terminal-begin-provider"),
		DecisionRef:      testSHARef("terminal-begin-decision"),
	}
	registerTestDisposition(t, store, *state.Forecast, dispositionRequest)
	state, err = store.RecordVerificationDisposition(
		context.Background(),
		dispositionRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	result := reviewtransaction.VerificationResultRef{
		Schema:    reviewtransaction.VerificationResultRefSchema,
		ResultRef: testSHARef("terminal-begin-result"),
		Subject:   plan.Subject, PolicyHash: plan.PolicyHash,
		PlanDigest: plan.Digest, ApplicabilityDigest: plan.ApplicabilityDigest,
		Aggregate:            reviewtransaction.VerificationAggregateUnavailable,
		CompletedObligations: []string{},
		EvidenceRefs:         []string{},
	}
	authority := testAuthorityForStore(t, store)
	authority.results[result.ResultRef] = VerificationResultAuthority{
		Applicability: applicability,
		Registry:      registry,
		Plan:          plan,
		Result:        result,
	}
	state, err = store.BindVerificationResult(
		context.Background(),
		BindVerificationResultRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "terminal-begin-result",
			Applicability:    applicability,
			Registry:         registry,
			Plan:             plan,
			Result:           result,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	obligation := plan.Obligations[0]
	ticket := AdmittedActionTicket{
		TicketRef:              testSHARef("terminal-begin-ticket"),
		SubjectRef:             applicability.Digest,
		CandidateRef:           handoff.CandidateRef,
		VerificationContextRef: state.Forecast.Digest,
		ExpectedRevision:       forecastInput.PlanRevisionRef,
		Slot:                   obligation.ID,
		Capability:             obligation.CapabilityRef,
		SlotBindingRef:         testSHARef("terminal-begin-slot"),
		HostRequestDigest:      testSHARef("terminal-begin-host-request"),
	}
	port := &countingEvidencePort{
		fakeEvidencePort: fakeEvidencePort{
			tickets:    map[string]AdmittedActionTicket{ticket.TicketRef: ticket},
			executions: map[string]AdmittedExecutionEvidence{},
		},
	}
	store = store.WithEvidencePort(port)
	request := BeginRequest{
		ExpectedRevision: state.Revision,
		RequestID:        "terminal-begin-attempt",
		Applicability:    applicability,
		Registry:         registry,
		Plan:             plan,
		ActionTicketRef:  ticket.TicketRef,
	}
	headPath := filepath.Join(store.Dir, "HEAD")
	headBefore, err := os.ReadFile(headPath)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	recordsBefore, err := os.ReadDir(filepath.Join(store.Dir, "records"))
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		outcome, beginErr := store.Begin(context.Background(), request)
		if !errors.Is(beginErr, ErrWorkRunInvalidTransition) {
			t.Fatalf("terminal Begin attempt %d error = %v", attempt+1, beginErr)
		}
		if outcome.Capability != nil {
			t.Fatalf("terminal Begin attempt %d issued launch capability", attempt+1)
		}
	}
	if port.ticketReads != 0 {
		t.Fatalf("terminal Begin consulted action-ticket evidence %d times", port.ticketReads)
	}

	headAfter, err := os.ReadFile(headPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(headAfter) != string(headBefore) {
		t.Fatalf("terminal Begin advanced HEAD: before %q after %q", headBefore, headAfter)
	}
	after, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	stateAfter, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if string(stateAfter) != string(stateBefore) {
		t.Fatalf("terminal Begin changed state: before %s after %s", stateBefore, stateAfter)
	}
	recordsAfter, err := os.ReadDir(filepath.Join(store.Dir, "records"))
	if err != nil {
		t.Fatal(err)
	}
	if len(recordsAfter) != len(recordsBefore) {
		t.Fatalf(
			"terminal Begin published a record: before %d after %d",
			len(recordsBefore),
			len(recordsAfter),
		)
	}
}

func TestWorkRunNotRequiredResultAndReviewRemainReferenceOnly(t *testing.T) {
	repo := initWorkRunRepository(t)
	applicability, registry, plan := verificationFixture(t, repo, false)
	store := openTestWorkRunStore(t, repo, "not-required")
	state := startDirectWorkRun(t, store)
	handoff := testHandoffForSnapshot(
		t,
		store,
		ImplementationRouteDirectInline,
		testSHARef("passive-scope"),
		currentWorkRunSnapshot(t, repo),
		[]string{},
		[]string{},
		"",
	)
	var err error
	state, err = store.BindImplementationHandoff(context.Background(), BindImplementationHandoffRequest{
		ExpectedRevision: state.Revision, RequestID: "handoff-passive", Handoff: handoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	forecastInput := VerificationForecastInput{
		WorkRunID: store.WorkRunID, Handoff: handoff,
		Applicability: applicability, Registry: registry, Plan: plan,
		PlanRevisionRef: testSHARef("passive-plan-revision"),
		Availability:    ForecastNotRequired, AvailabilityRef: testSHARef("native-not-required"),
		DiagnosticRefs: []string{},
	}
	registerTestForecast(t, store, forecastInput)
	state, err = store.RecordVerificationForecast(context.Background(), RecordVerificationForecastRequest{
		ExpectedRevision: state.Revision, RequestID: "forecast-passive",
		Input: forecastInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := reviewtransaction.VerificationResultRef{
		Schema:    reviewtransaction.VerificationResultRefSchema,
		ResultRef: testSHARef("not-required-result"), Subject: plan.Subject,
		PolicyHash: plan.PolicyHash, PlanDigest: plan.Digest,
		ApplicabilityDigest:  plan.ApplicabilityDigest,
		Aggregate:            reviewtransaction.VerificationAggregateNotRequired,
		CompletedObligations: []string{}, EvidenceRefs: []string{},
	}
	authority := testAuthorityForStore(t, store)
	resultRequest := BindVerificationResultRequest{
		ExpectedRevision: state.Revision, RequestID: "result-passive",
		Applicability: applicability, Registry: registry, Plan: plan, Result: result,
	}
	if _, err := store.BindVerificationResult(
		context.Background(),
		resultRequest,
	); err == nil {
		t.Fatal("hash-shaped but unowned verification result was accepted")
	}
	authority.results[result.ResultRef] = VerificationResultAuthority{
		Applicability: applicability, Registry: registry, Plan: plan, Result: result,
	}
	state, err = store.BindVerificationResult(context.Background(), resultRequest)
	if err != nil {
		t.Fatal(err)
	}
	reviewRef := testSHARef("review-receipt")
	reviewRequest := BindReviewReceiptRequest{
		ExpectedRevision: state.Revision, RequestID: "review-passive",
		ReviewReceiptRef: reviewRef,
	}
	if _, err := store.BindReviewReceipt(
		context.Background(),
		reviewRequest,
	); err == nil {
		t.Fatal("hash-shaped but unowned review receipt was accepted")
	}
	t.Run("valid receipt cannot cross result identity", func(t *testing.T) {
		wrongResultRef := testSHARef("other-not-required-result")
		authority.receipts[reviewReceiptLookup{
			receiptRef: reviewRef,
			resultRef:  wrongResultRef,
		}] = ReviewReceiptAuthority{
			ReceiptRef: reviewRef, CandidateRef: handoff.CandidateRef,
			VerificationResultRef: wrongResultRef,
		}
		if _, err := store.BindReviewReceipt(
			context.Background(),
			reviewRequest,
		); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cross-result review bind error = %v, want exact-pair lookup miss", err)
		}
		unchanged, err := store.Status()
		if err != nil {
			t.Fatal(err)
		}
		if unchanged.Revision != state.Revision || unchanged.ReviewReceiptRef != "" {
			t.Fatalf("cross-result review attempt mutated WorkRun: %#v", unchanged)
		}
	})
	authority.receipts[reviewReceiptLookup{
		receiptRef: reviewRef,
		resultRef:  result.ResultRef,
	}] = ReviewReceiptAuthority{
		ReceiptRef: reviewRef, CandidateRef: handoff.CandidateRef,
		VerificationResultRef: result.ResultRef,
	}
	state, err = store.BindReviewReceipt(context.Background(), reviewRequest)
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Verification.Outcome != VerificationNotRequired ||
		len(status.Verification.ResultRefs) != 1 ||
		status.Verification.ResultRefs[0] != result.ResultRef ||
		status.ReviewReceiptRef != reviewRef {
		t.Fatalf("reference-only public result = %#v", status)
	}
	if status.PublicState != PublicStateChecking {
		t.Fatalf("WorkRun inferred PAD readiness: %q", status.PublicState)
	}
	if len(state.Reservations) != 0 || state.NextOrdinal != 1 {
		t.Fatalf("passive result consumed verification attempt: %#v", state)
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"aggregate"`) ||
		strings.Contains(string(payload), `"completed_obligations"`) {
		t.Fatalf("WorkRun copied RAR-owned result preimages: %s", payload)
	}

	authority.mu.Lock()
	delete(authority.results, result.ResultRef)
	authority.mu.Unlock()
	if _, err := store.PublicStatus(context.Background()); err == nil {
		t.Fatal("public status trusted a persisted result ref without owner resolution")
	}
	authority.mu.Lock()
	authority.results[result.ResultRef] = VerificationResultAuthority{
		Applicability: applicability, Registry: registry, Plan: plan, Result: result,
	}
	authority.mu.Unlock()
}

func TestWorkRunLongForecastAwaitsDecisionWithoutCeremony(t *testing.T) {
	tests := []struct {
		name string
		cost reviewtransaction.VerificationCost
		want PublicState
	}{
		{
			name: "quick proceeds to checking",
			cost: reviewtransaction.VerificationCostQuick,
			want: PublicStateChecking,
		},
		{
			name: "long asks before verification",
			cost: reviewtransaction.VerificationCostLong,
			want: PublicStateNeedsYourDecision,
		},
		{
			name: "very long asks before verification",
			cost: reviewtransaction.VerificationCostVeryLong,
			want: PublicStateNeedsYourDecision,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := initWorkRunRepository(t)
			applicability, registry, plan := verificationFixtureWithCost(
				t,
				repo,
				true,
				test.cost,
			)
			store := openTestWorkRunStore(t, repo, "forecast-cost")
			state := startDirectWorkRun(t, store)
			handoff := testHandoffForSnapshot(
				t,
				store,
				ImplementationRouteDirectInline,
				testSHARef("cost-scope"),
				currentWorkRunSnapshot(t, repo),
				verificationRequirementRefs(plan),
				[]string{},
				"",
			)
			var err error
			state, err = store.BindImplementationHandoff(
				context.Background(),
				BindImplementationHandoffRequest{
					ExpectedRevision: state.Revision,
					RequestID:        "handoff-cost",
					Handoff:          handoff,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			input := VerificationForecastInput{
				WorkRunID: store.WorkRunID, Handoff: handoff,
				Applicability: applicability, Registry: registry, Plan: plan,
				PlanRevisionRef: testSHARef("cost-plan-revision"),
				Availability:    ForecastAvailable,
				AvailabilityRef: testSHARef("cost-available"),
				DiagnosticRefs:  []string{},
			}
			registerTestForecast(t, store, input)
			if _, err := store.RecordVerificationForecast(
				context.Background(),
				RecordVerificationForecastRequest{
					ExpectedRevision: state.Revision,
					RequestID:        "forecast-cost",
					Input:            input,
				},
			); err != nil {
				t.Fatal(err)
			}
			status, err := store.PublicStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if status.PublicState != test.want {
				t.Fatalf("forecast cost %q state = %q, want %q", test.cost, status.PublicState, test.want)
			}
		})
	}
}

func TestWorkRunSDDReservationMustBindBeforeCapabilityActivation(t *testing.T) {
	repo := initWorkRunRepository(t)
	applicability, registry, plan := verificationFixture(t, repo, true)
	store := openTestWorkRunStore(t, repo, "sdd-reservation")
	authority := testAuthorityForStore(t, store)

	proposal, err := DecideImplementationRoute(ImplementationRouteInput{
		DurablePlanning: []DurablePlanningReason{PlanningArchitecture},
	})
	if err != nil {
		t.Fatal(err)
	}
	deliveryRef := testSHARef("sdd-delivery")
	registerTestDeliveryIntent(t, store, deliveryRef)
	state, err := store.Start(context.Background(), StartRequest{
		RequestID: "sdd-start", RouteDecision: proposal,
		DeliveryIntentRef: deliveryRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	acceptanceRef := testSHARef("sdd-acceptance")
	authority.routes[acceptanceRef] = RouteSelectionAuthority{
		DecisionRef: acceptanceRef, PendingDecisionDigest: proposal.Digest,
		SelectedRoute: ImplementationRouteSDD,
	}
	state, err = store.AcceptSDD(context.Background(), AcceptSDDRequest{
		ExpectedRevision: state.Revision, RequestID: "sdd-accept",
		AcceptanceRef: acceptanceRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	const runRef = "sdd-change"
	authority.runs[runRef] = SDDRunAuthority{
		RunRef: runRef, WorkRunID: store.WorkRunID,
		RouteAcceptanceRef: acceptanceRef,
	}
	state, err = store.BindSDDRun(context.Background(), BindSDDRunRequest{
		ExpectedRevision: state.Revision, RequestID: "sdd-bind",
		SDDRunRef: runRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	handoff := testHandoffForSnapshot(
		t,
		store,
		ImplementationRouteSDD,
		testSHARef("sdd-scope"),
		currentWorkRunSnapshot(t, repo),
		verificationRequirementRefs(plan),
		[]string{},
		runRef,
	)
	state, err = store.BindImplementationHandoff(
		context.Background(),
		BindImplementationHandoffRequest{
			ExpectedRevision: state.Revision, RequestID: "sdd-handoff",
			Handoff: handoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	forecastInput := VerificationForecastInput{
		WorkRunID: store.WorkRunID, Handoff: handoff,
		Applicability: applicability, Registry: registry, Plan: plan,
		PlanRevisionRef: testSHARef("sdd-plan-revision"),
		Availability:    ForecastAvailable,
		AvailabilityRef: testSHARef("sdd-available"),
		DiagnosticRefs:  []string{},
	}
	registerTestForecast(t, store, forecastInput)
	state, err = store.RecordVerificationForecast(
		context.Background(),
		RecordVerificationForecastRequest{
			ExpectedRevision: state.Revision, RequestID: "sdd-forecast",
			Input: forecastInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	dispositionRequest := RecordVerificationDispositionRequest{
		ExpectedRevision: state.Revision, RequestID: "sdd-disposition",
		Kind: DispositionRun, AssumptionsRef: testSHARef("sdd-assumptions"),
		ActorRef: testSHARef("sdd-provider"), DecisionRef: testSHARef("sdd-decision"),
	}
	registerTestDisposition(t, store, *state.Forecast, dispositionRequest)
	state, err = store.RecordVerificationDisposition(
		context.Background(),
		dispositionRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	step, _ := testWorkRunExecStep(t)
	binding, err := hostruntime.BindExecStep(step)
	if err != nil {
		t.Fatal(err)
	}
	obligation := plan.Obligations[0]
	ticket := AdmittedActionTicket{
		TicketRef: testSHARef("sdd-ticket"), SubjectRef: applicability.Digest,
		CandidateRef:           handoff.CandidateRef,
		VerificationContextRef: state.Forecast.Digest,
		ExpectedRevision:       forecastInput.PlanRevisionRef,
		Slot:                   obligation.ID, Capability: obligation.CapabilityRef,
		SlotBindingRef:    testSHARef("sdd-slot"),
		HostRequestDigest: binding.RequestDigest,
	}
	port := &fakeEvidencePort{
		tickets:    map[string]AdmittedActionTicket{ticket.TicketRef: ticket},
		executions: map[string]AdmittedExecutionEvidence{},
	}
	store = store.WithEvidencePort(port)
	beginRequest := BeginRequest{
		ExpectedRevision: state.Revision, RequestID: "sdd-begin",
		Applicability: applicability, Registry: registry, Plan: plan,
		ActionTicketRef: ticket.TicketRef,
	}
	bindFailure := errors.New("SDD attempt store unavailable")
	authority.sddBindErr = bindFailure
	outcome, err := store.Begin(context.Background(), beginRequest)
	if !errors.Is(err, bindFailure) || outcome.Capability != nil {
		t.Fatalf("failed SDD bind outcome = %#v, %v", outcome, err)
	}
	persisted, statusErr := store.Status()
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if len(persisted.Reservations) != 1 || len(persisted.LaunchClaims) != 0 {
		t.Fatalf("failed SDD binding produced unsafe state: %#v", persisted)
	}
	authority.sddBindErr = nil
	outcome, err = store.Begin(context.Background(), beginRequest)
	if err != nil || outcome.Capability == nil {
		t.Fatalf("idempotent SDD binding recovery = %#v, %v", outcome, err)
	}
	if len(authority.sddBindings) != 1 {
		t.Fatalf("SDD binding count = %d, want 1", len(authority.sddBindings))
	}
	bound := authority.sddBindings[0]
	if bound.SDDRunRef != runRef ||
		bound.ReservationRef != persisted.Reservations[0].ReservationRef ||
		bound.ActionTicketRef != ticket.TicketRef {
		t.Fatalf("SDD owner received wrong reservation binding: %#v", bound)
	}
}

type fakeEvidencePort struct {
	tickets    map[string]AdmittedActionTicket
	executions map[string]AdmittedExecutionEvidence
}

type countingEvidencePort struct {
	fakeEvidencePort
	ticketReads int
}

func (port *countingEvidencePort) ReadActionTicket(
	ctx context.Context,
	ref string,
) (AdmittedActionTicket, error) {
	port.ticketReads++
	return port.fakeEvidencePort.ReadActionTicket(ctx, ref)
}

func (port *fakeEvidencePort) ReadActionTicket(
	_ context.Context,
	ref string,
) (AdmittedActionTicket, error) {
	ticket, exists := port.tickets[ref]
	if !exists {
		return AdmittedActionTicket{}, os.ErrNotExist
	}
	return ticket, nil
}

func (port *fakeEvidencePort) ReadExecutionEvidence(
	_ context.Context,
	ref string,
) (AdmittedExecutionEvidence, error) {
	execution, exists := port.executions[ref]
	if !exists {
		return AdmittedExecutionEvidence{}, os.ErrNotExist
	}
	return execution, nil
}

func verificationFixture(
	t *testing.T,
	repo string,
	active bool,
) (
	reviewtransaction.VerificationApplicability,
	reviewtransaction.VerificationPlanRegistry,
	reviewtransaction.VerificationPlan,
) {
	t.Helper()
	return verificationFixtureWithCost(
		t,
		repo,
		active,
		reviewtransaction.VerificationCostQuick,
	)
}

func verificationFixtureWithCost(
	t *testing.T,
	repo string,
	active bool,
	cost reviewtransaction.VerificationCost,
) (
	reviewtransaction.VerificationApplicability,
	reviewtransaction.VerificationPlanRegistry,
	reviewtransaction.VerificationPlan,
) {
	t.Helper()
	logicalPath := "docs/guide.md"
	content := []byte("# Guide\nHuman prose.\n")
	if active {
		logicalPath = "main.go"
		content = []byte("package main\n\nfunc main() {}\n")
	}
	fullPath := filepath.Join(repo, filepath.FromSlash(logicalPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	obligation := reviewtransaction.VerificationObligation{
		ID: "unit.test", RequirementRef: testSHARef("requirement"),
		ArgvRef: testSHARef("argv"), CapabilityRef: "go.test",
		Cost: cost,
	}
	registry, err := reviewtransaction.NewVerificationPlanRegistry(
		testSHARef("policy"), []string{}, []reviewtransaction.VerificationObligation{obligation},
	)
	if err != nil {
		t.Fatal(err)
	}
	builder := reviewtransaction.SnapshotBuilder{Repo: repo}
	snapshot, err := builder.Build(context.Background(), reviewtransaction.Target{
		Kind: reviewtransaction.TargetCurrentChanges, IntendedUntracked: []string{logicalPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	applicability, err := builder.ClassifyVerificationApplicability(
		context.Background(), snapshot, registry, []string{},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := reviewtransaction.BuildVerificationPlan(applicability, registry)
	if err != nil {
		t.Fatal(err)
	}
	if active && plan.Applicability != reviewtransaction.VerificationApplicable {
		t.Fatalf("active fixture applicability = %q", plan.Applicability)
	}
	if !active && plan.Applicability != reviewtransaction.VerificationNotApplicable {
		t.Fatalf("passive fixture applicability = %q", plan.Applicability)
	}
	return applicability, registry, plan
}

func verificationRequirementRefs(plan reviewtransaction.VerificationPlan) []string {
	refs := make([]string, 0, len(plan.Obligations))
	for _, obligation := range plan.Obligations {
		refs = append(refs, obligation.RequirementRef)
	}
	return refs
}

func testWorkRunExecStep(t *testing.T) (hostruntime.ExecStep, string) {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(t.TempDir(), "launched")
	environment := map[string]string{
		"GO_WANT_WORKRUN_HELPER": "1",
		"WORKRUN_MARKER":         markerPath,
	}
	if runtime.GOOS == "windows" {
		environment["SYSTEMROOT"] = os.Getenv("SYSTEMROOT")
	}
	return hostruntime.ExecStep{
		ExecutionID: "workrun-authorized-launch",
		Program:     program,
		Args:        []string{"-test.run=^TestWorkRunHostHelperProcess$"},
		CWD:         t.TempDir(),
		Env:         environment,
		Deadline:    5 * time.Second,
		Limits: hostruntime.StreamLimits{
			StdoutBytes: 1 << 20,
			StderrBytes: 1 << 20,
		},
	}, markerPath
}

func TestWorkRunHostHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_WORKRUN_HELPER") != "1" {
		return
	}
	if err := os.WriteFile(
		os.Getenv("WORKRUN_MARKER"),
		[]byte("launched\n"),
		0o600,
	); err != nil {
		os.Exit(97)
	}
}
