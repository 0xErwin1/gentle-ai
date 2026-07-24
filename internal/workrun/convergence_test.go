package workrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

func TestWorkRunHandoffRequiresOwnerMutationCompletion(t *testing.T) {
	repo := initWorkRunRepository(t)
	store := openTestWorkRunStore(t, repo, "owner-completion")
	started := startDirectWorkRun(t, store)
	snapshot := currentWorkRunSnapshot(t, repo)
	subject, err := reviewtransaction.VerificationSubjectFromSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := NewImplementationHandoff(
		ImplementationRouteDirectInline,
		testSHARef("owner-completion-scope"),
		subject,
		[]string{},
		[]string{},
		"",
		testSHARef("unowned-mutation-completion"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindImplementationHandoff(
		context.Background(),
		BindImplementationHandoffRequest{
			ExpectedRevision: started.Revision,
			RequestID:        "unowned-handoff",
			Handoff:          handoff,
		},
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unowned mutation completion error = %v", err)
	}
	unchanged, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != started.Revision || unchanged.Handoff != nil {
		t.Fatalf("unowned completion mutated WorkRun: %#v", unchanged)
	}
}

func TestImplementationHandoffV1RemainsReplayableButCannotGainCompletion(
	t *testing.T,
) {
	legacy := ImplementationHandoff{
		Schema:                 ImplementationHandoffSchemaV1,
		Route:                  ImplementationRouteDirectInline,
		ScopeDigest:            testSHARef("legacy-scope"),
		Subject:                testVerificationSubject(),
		CandidateRef:           testVerificationSubject().SnapshotIdentity,
		DeclaredObligationRefs: []string{},
		EvidenceRefs:           []string{},
	}
	var err error
	legacy.Digest, err = implementationHandoffDigestV1(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy handoff replay validation = %v", err)
	}
	record := workRunRecord{
		Schema:           workRunRecordSchemaV1,
		WorkRunID:        "legacy-replay",
		PreviousRevision: testSHARef("legacy-previous"),
		Operation:        workOperationBindHandoff,
		RequestID:        "legacy-handoff",
		RequestDigest:    testSHARef("legacy-request"),
		Handoff:          &legacy,
	}
	_, payload, err := workRunRecordRevision(record)
	if err != nil {
		t.Fatal(err)
	}
	var decoded workRunRecord
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	_, replayedPayload, err := workRunRecordRevision(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, replayedPayload) {
		t.Fatal("legacy handoff gained additive fields during canonical replay")
	}
	legacy.MutationCompletionRef = testSHARef("forged-legacy-completion")
	if err := legacy.Validate(); err == nil {
		t.Fatal("legacy handoff gained mutation completion authority")
	}
}

func TestWorkRunVerifierMutationStopsBeforeReviewFreeze(t *testing.T) {
	repo := initWorkRunRepository(t)
	applicability, registry, plan := verificationFixture(t, repo, false)
	store := openTestWorkRunStore(t, repo, "verifier-mutation")
	state := startDirectWorkRun(t, store)
	snapshot := currentWorkRunSnapshot(t, repo)
	handoff := testHandoffForSnapshot(
		t,
		store,
		ImplementationRouteDirectInline,
		testSHARef("verifier-mutation-scope"),
		snapshot,
		[]string{},
		[]string{},
		"",
	)
	var err error
	state, err = store.BindImplementationHandoff(
		context.Background(),
		BindImplementationHandoffRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "mutation-handoff",
			Handoff:          handoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	forecastInput := VerificationForecastInput{
		WorkRunID:       store.WorkRunID,
		Handoff:         handoff,
		Applicability:   applicability,
		Registry:        registry,
		Plan:            plan,
		PlanRevisionRef: testSHARef("mutation-plan-revision"),
		Availability:    ForecastNotRequired,
		AvailabilityRef: testSHARef("mutation-not-required"),
		DiagnosticRefs:  []string{},
	}
	registerTestForecast(t, store, forecastInput)
	state, err = store.RecordVerificationForecast(
		context.Background(),
		RecordVerificationForecastRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "mutation-forecast",
			Input:            forecastInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result := reviewtransaction.VerificationResultRef{
		Schema:               reviewtransaction.VerificationResultRefSchema,
		ResultRef:            testSHARef("mutation-result"),
		Subject:              plan.Subject,
		PolicyHash:           plan.PolicyHash,
		PlanDigest:           plan.Digest,
		ApplicabilityDigest:  plan.ApplicabilityDigest,
		Aggregate:            reviewtransaction.VerificationAggregateNotRequired,
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
	if err := os.WriteFile(
		filepath.Join(repo, "docs", "guide.md"),
		[]byte("# Guide\nVerifier mutation.\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	stopped, err := store.BindVerificationResult(
		context.Background(),
		BindVerificationResultRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "mutation-result",
			Applicability:    applicability,
			Registry:         registry,
			Plan:             plan,
			Result:           result,
		},
	)
	if !errors.Is(err, reviewtransaction.ErrVerificationSubjectMutated) {
		t.Fatalf("verifier mutation bind error = %v", err)
	}
	if stopped.VerificationStop == nil ||
		stopped.VerificationStop.Reason != VerificationStopMutated ||
		stopped.VerificationResultRef != "" ||
		stopped.PostVerificationSnapshotRef != "" {
		t.Fatalf("verifier mutation did not stop exactly: %#v", stopped)
	}
	if _, err := store.BindReviewReceipt(
		context.Background(),
		BindReviewReceiptRequest{
			ExpectedRevision: stopped.Revision,
			RequestID:        "mutation-review",
			ReviewReceiptRef: testSHARef("mutation-review"),
		},
	); !errors.Is(err, ErrWorkRunInvalidTransition) {
		t.Fatalf("mutated subject review-freeze error = %v", err)
	}
	status, err := store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicState != PublicStateNeedsYourDecision {
		t.Fatalf("mutated subject public state = %q", status.PublicState)
	}
}

func TestWorkRunCorrectionReplanBindsClosureWithoutRestartingWorkflow(
	t *testing.T,
) {
	repo := initWorkRunRepository(t)
	originalApplicability, registry, originalPlan := verificationFixture(
		t,
		repo,
		false,
	)
	store := openTestWorkRunStore(t, repo, "correction-replan")
	state := startDirectWorkRun(t, store)
	scopeRef := testSHARef("correction-replan-scope")
	originalSnapshot := currentWorkRunSnapshot(t, repo)
	originalHandoff := testHandoffForSnapshot(
		t,
		store,
		ImplementationRouteDirectInline,
		scopeRef,
		originalSnapshot,
		[]string{},
		[]string{},
		"",
	)
	var err error
	state, err = store.BindImplementationHandoff(
		context.Background(),
		BindImplementationHandoffRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "correction-original-handoff",
			Handoff:          originalHandoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	originalForecastInput := VerificationForecastInput{
		WorkRunID:       store.WorkRunID,
		Handoff:         originalHandoff,
		Applicability:   originalApplicability,
		Registry:        registry,
		Plan:            originalPlan,
		PlanRevisionRef: testSHARef("correction-original-plan-revision"),
		Availability:    ForecastNotRequired,
		AvailabilityRef: testSHARef("correction-original-not-required"),
		DiagnosticRefs:  []string{},
	}
	registerTestForecast(t, store, originalForecastInput)
	state, err = store.RecordVerificationForecast(
		context.Background(),
		RecordVerificationForecastRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "correction-original-forecast",
			Input:            originalForecastInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	originalRevision := state.Revision

	if err := os.WriteFile(
		filepath.Join(repo, "docs", "guide.md"),
		[]byte("# Guide\nCorrected human prose.\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	correctedSnapshot := currentWorkRunSnapshot(t, repo)
	builder := reviewtransaction.SnapshotBuilder{Repo: repo}
	correctedApplicability, err := builder.ClassifyVerificationApplicability(
		context.Background(),
		correctedSnapshot,
		registry,
		[]string{},
	)
	if err != nil {
		t.Fatal(err)
	}
	correctedPlan, err := reviewtransaction.BuildVerificationPlan(
		correctedApplicability,
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	correctedHandoff := testHandoffForSnapshot(
		t,
		store,
		ImplementationRouteDirectInline,
		scopeRef,
		correctedSnapshot,
		[]string{},
		[]string{},
		"",
	)
	state, err = store.ReplanVerificationAfterCorrection(
		context.Background(),
		ReplanVerificationAfterCorrectionRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "correction-replan",
			CorrectedHandoff: correctedHandoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.VerificationReplan == nil ||
		state.VerificationReplan.Ordinal != 1 ||
		state.Forecast != nil ||
		state.ImplementationRoute != ImplementationRouteDirectInline ||
		state.SDDRunRef != "" ||
		state.NextOrdinal != 1 ||
		len(state.Reservations) != 0 ||
		len(state.LaunchClaims) != 0 {
		t.Fatalf("correction replan restarted unrelated authority: %#v", state)
	}
	if state.VerificationReplan.PreviousForecastDigest == "" ||
		state.VerificationReplan.PreviousHandoffDigest !=
			originalHandoff.Digest {
		t.Fatalf("correction replan lost original binding: %#v", state.VerificationReplan)
	}
	if originalRevision == state.Revision {
		t.Fatal("correction replan did not append to the same WorkRun")
	}

	correctedForecastInput := VerificationForecastInput{
		WorkRunID:       store.WorkRunID,
		Handoff:         correctedHandoff,
		Applicability:   correctedApplicability,
		Registry:        registry,
		Plan:            correctedPlan,
		PlanRevisionRef: testSHARef("correction-corrected-plan-revision"),
		Availability:    ForecastNotRequired,
		AvailabilityRef: testSHARef("correction-corrected-not-required"),
		DiagnosticRefs:  []string{},
	}
	registerTestForecast(t, store, correctedForecastInput)
	state, err = store.RecordVerificationForecast(
		context.Background(),
		RecordVerificationForecastRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "correction-corrected-forecast",
			Input:            correctedForecastInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result := reviewtransaction.VerificationResultRef{
		Schema:               reviewtransaction.VerificationResultRefSchema,
		ResultRef:            testSHARef("correction-corrected-result"),
		Subject:              correctedPlan.Subject,
		PolicyHash:           correctedPlan.PolicyHash,
		PlanDigest:           correctedPlan.Digest,
		ApplicabilityDigest:  correctedPlan.ApplicabilityDigest,
		Aggregate:            reviewtransaction.VerificationAggregateNotRequired,
		CompletedObligations: []string{},
		EvidenceRefs:         []string{},
	}
	closure, err := reviewtransaction.BuildCorrectionImpactClosure(
		originalApplicability,
		registry,
		originalPlan,
		correctedApplicability,
		registry,
		correctedPlan,
		result,
	)
	if err != nil {
		t.Fatal(err)
	}
	authority := testAuthorityForStore(t, store)
	authority.results[result.ResultRef] = VerificationResultAuthority{
		Applicability: correctedApplicability,
		Registry:      registry,
		Plan:          correctedPlan,
		Result:        result,
	}
	state, err = store.BindVerificationResult(
		context.Background(),
		BindVerificationResultRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "correction-result",
			Applicability:    correctedApplicability,
			Registry:         registry,
			Plan:             correctedPlan,
			Result:           result,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.PostVerificationSnapshotRef != correctedSnapshot.Identity ||
		state.CorrectionImpactClosureRef != closure.Digest ||
		len(state.ReusableVerificationObligations) != 0 ||
		state.VerificationStop != nil {
		t.Fatalf("corrected result convergence = %#v", state)
	}
	reviewRef := testSHARef("correction-review-receipt")
	authority.receipts[reviewReceiptLookup{
		receiptRef: reviewRef,
		resultRef:  result.ResultRef,
	}] = ReviewReceiptAuthority{
		ReceiptRef:            reviewRef,
		CandidateRef:          correctedHandoff.CandidateRef,
		VerificationResultRef: result.ResultRef,
	}
	state, err = store.BindReviewReceipt(
		context.Background(),
		BindReviewReceiptRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "correction-review",
			ReviewReceiptRef: reviewRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.ReviewReceiptRef != reviewRef {
		t.Fatalf("corrected review receipt was not frozen: %#v", state)
	}
}

func TestWorkRunFailedVerificationStopsBeforeReviewFreeze(t *testing.T) {
	repo := initWorkRunRepository(t)
	applicability, registry, plan := verificationFixture(t, repo, true)
	store := openTestWorkRunStore(t, repo, "failed-stop")
	state := startDirectWorkRun(t, store)
	snapshot := currentWorkRunSnapshot(t, repo)
	handoff := testHandoffForSnapshot(
		t,
		store,
		ImplementationRouteDirectInline,
		testSHARef("failed-stop-scope"),
		snapshot,
		verificationRequirementRefs(plan),
		[]string{},
		"",
	)
	var err error
	state, err = store.BindImplementationHandoff(
		context.Background(),
		BindImplementationHandoffRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "failed-handoff",
			Handoff:          handoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	forecastInput := VerificationForecastInput{
		WorkRunID:       store.WorkRunID,
		Handoff:         handoff,
		Applicability:   applicability,
		Registry:        registry,
		Plan:            plan,
		PlanRevisionRef: testSHARef("failed-plan-revision"),
		Availability:    ForecastAvailable,
		AvailabilityRef: testSHARef("failed-available"),
		DiagnosticRefs:  []string{},
	}
	registerTestForecast(t, store, forecastInput)
	state, err = store.RecordVerificationForecast(
		context.Background(),
		RecordVerificationForecastRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "failed-forecast",
			Input:            forecastInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	dispositionRequest := RecordVerificationDispositionRequest{
		ExpectedRevision: state.Revision,
		RequestID:        "failed-disposition",
		Kind:             DispositionRun,
		AssumptionsRef:   testSHARef("failed-assumptions"),
		ActorRef:         testSHARef("failed-actor"),
		DecisionRef:      testSHARef("failed-decision"),
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
	hostBinding, err := hostruntime.BindExecStep(step)
	if err != nil {
		t.Fatal(err)
	}
	obligation := plan.Obligations[0]
	ticket := AdmittedActionTicket{
		TicketRef:              testSHARef("failed-ticket"),
		SubjectRef:             applicability.Digest,
		CandidateRef:           handoff.CandidateRef,
		VerificationContextRef: state.Forecast.Digest,
		ExpectedRevision:       forecastInput.PlanRevisionRef,
		Slot:                   obligation.ID,
		Capability:             obligation.CapabilityRef,
		SlotBindingRef:         testSHARef("failed-slot"),
		HostRequestDigest:      hostBinding.RequestDigest,
	}
	evidenceRef := testSHARef("failed-evidence")
	port := &fakeEvidencePort{
		tickets: map[string]AdmittedActionTicket{
			ticket.TicketRef: ticket,
		},
		executions: map[string]AdmittedExecutionEvidence{},
	}
	store = store.WithEvidencePort(port)
	outcome, err := store.Begin(
		context.Background(),
		BeginRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "failed-begin",
			Applicability:    applicability,
			Registry:         registry,
			Plan:             plan,
			ActionTicketRef:  ticket.TicketRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executor := testExecutorForStore(t, store)
	if _, err := executor.ExecuteAuthorized(
		context.Background(),
		step,
		outcome.Capability,
	); err != nil {
		t.Fatal(err)
	}
	state, err = store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Reservations) != 1 || len(state.LaunchClaims) != 1 {
		t.Fatalf("failed verification lacks launch authority: %#v", state)
	}
	reservation := state.Reservations[0]
	port.executions[evidenceRef] = AdmittedExecutionEvidence{
		EvidenceRef:            evidenceRef,
		TicketRef:              ticket.TicketRef,
		SlotBindingRef:         reservation.SlotBindingRef,
		SubjectRef:             reservation.SubjectRef,
		CandidateRef:           reservation.CandidateRef,
		VerificationContextRef: reservation.ForecastDigest,
		ExpectedRevision:       reservation.PlanRevisionRef,
		Slot:                   reservation.Slot,
		Capability:             reservation.Capability,
		Complete:               false,
	}
	result := reviewtransaction.VerificationResultRef{
		Schema:               reviewtransaction.VerificationResultRefSchema,
		ResultRef:            testSHARef("failed-result"),
		Subject:              plan.Subject,
		PolicyHash:           plan.PolicyHash,
		PlanDigest:           plan.Digest,
		ApplicabilityDigest:  plan.ApplicabilityDigest,
		Aggregate:            reviewtransaction.VerificationAggregateFailed,
		CompletedObligations: []string{},
		EvidenceRefs:         []string{evidenceRef},
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
			RequestID:        "failed-result",
			Applicability:    applicability,
			Registry:         registry,
			Plan:             plan,
			Result:           result,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.VerificationStop == nil ||
		state.VerificationStop.Reason != VerificationStopFailed ||
		state.VerificationResultRef != result.ResultRef ||
		state.PostVerificationSnapshotRef != snapshot.Identity {
		t.Fatalf("failed verification did not stop exactly: %#v", state)
	}
	if _, err := store.BindReviewReceipt(
		context.Background(),
		BindReviewReceiptRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "failed-review",
			ReviewReceiptRef: testSHARef("failed-review"),
		},
	); !errors.Is(err, ErrWorkRunInvalidTransition) {
		t.Fatalf("failed verification review-freeze error = %v", err)
	}
}
