package workrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

type consentFixture struct {
	store         WorkRunStore
	state         WorkRunState
	prompt        VerificationDecisionPromptV1
	applicability reviewtransaction.VerificationApplicability
	registry      reviewtransaction.VerificationPlanRegistry
	plan          reviewtransaction.VerificationPlan
}

type countingLaunchAuthority struct {
	mu    sync.Mutex
	calls int
}

func (authority *countingLaunchAuthority) ActivateLaunch(
	context.Context,
	hostruntime.LaunchBinding,
	hostruntime.LaunchClaim,
) (*hostruntime.LaunchCapability, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.calls++
	return nil, errors.New("unexpected launch activation")
}

func (authority *countingLaunchAuthority) Calls() int {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.calls
}

func TestWorkRunVerificationDecisionIsOwnerBoundReplaySafeAndLaunchFree(
	t *testing.T,
) {
	fixture := newConsentFixture(
		t,
		"long-consent-run",
		reviewtransaction.VerificationCostLong,
	)
	launch := &countingLaunchAuthority{}
	fixture.store.authority.Launch = launch

	ticket := consentFixtureTicket(t, fixture)
	fixture.store = fixture.store.WithEvidencePort(&fakeEvidencePort{
		tickets: map[string]AdmittedActionTicket{
			ticket.TicketRef: ticket,
		},
		executions: map[string]AdmittedExecutionEvidence{},
	})
	before := fixture.state
	if _, err := fixture.store.Begin(
		context.Background(),
		BeginRequest{
			ExpectedRevision: before.Revision,
			RequestID:        "begin-before-consent",
			Applicability:    fixture.applicability,
			Registry:         fixture.registry,
			Plan:             fixture.plan,
			ActionTicketRef:  ticket.TicketRef,
		},
	); !errors.Is(err, ErrWorkRunInvalidTransition) {
		t.Fatalf("Begin before consent error = %v", err)
	}
	legacy := RecordVerificationDispositionRequest{
		ExpectedRevision: before.Revision,
		RequestID:        "legacy-disposition-bypass",
		Kind:             DispositionRun,
		AssumptionsRef:   fixture.prompt.AssumptionsRef,
		ActorRef:         testSHARef("legacy-consent-actor"),
		DecisionRef:      testSHARef("legacy-consent-decision"),
	}
	registerTestDisposition(t, fixture.store, *before.Forecast, legacy)
	if _, err := fixture.store.RecordVerificationDisposition(
		context.Background(),
		legacy,
	); !errors.Is(err, ErrWorkRunInvalidTransition) {
		t.Fatalf("legacy consent bypass error = %v", err)
	}
	assertConsentStateUnchanged(t, fixture.store, before, launch)

	request := DecideVerificationRequest{
		ExpectedRevision: before.Revision,
		RequestID:        "accept-long-verification",
		PromptRef:        fixture.prompt.PromptRef,
		Choice:           VerificationDecisionRun,
	}
	registerTestVerificationDecision(
		t,
		fixture.store,
		fixture.prompt,
		request.Choice,
		testSHARef("consent-actor"),
		testSHARef("consent-run-decision"),
		"",
	)
	decided, err := fixture.store.DecideVerification(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if decided.Revision == before.Revision ||
		decided.Disposition == nil ||
		decided.Disposition.Kind != DispositionRun ||
		decided.Disposition.ForecastDigest != before.Forecast.Digest ||
		decided.Disposition.AssumptionsRef != fixture.prompt.AssumptionsRef ||
		decided.Disposition.ActorRef != testSHARef("consent-actor") ||
		decided.Disposition.DecisionRef !=
			testSHARef("consent-run-decision") ||
		decided.ProductiveResumeRevision != decided.Revision ||
		len(decided.Reservations) != 0 ||
		len(decided.LaunchClaims) != 0 ||
		decided.NextOrdinal != before.NextOrdinal ||
		decided.ProductiveBlockerRef != "" {
		t.Fatalf("durable verification decision = %#v", decided)
	}
	if launch.Calls() != 0 {
		t.Fatalf("verification decision activated HCR %d times", launch.Calls())
	}
	resumeStatus, err := fixture.store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resumeStatus.PublicState != PublicStateChecking ||
		resumeStatus.Revision != decided.Revision {
		t.Fatalf(
			"run decision did not expose one bounded resume revision: %#v",
			resumeStatus,
		)
	}
	progressed := decided
	progressed.Revision = testSHARef("resume-internal-progress")
	if got := publicWorkRunRevision(progressed); got != decided.Revision {
		t.Fatalf("bounded resume CAS drifted to %q", got)
	}

	reopened, err := OpenWorkRunStore(
		context.Background(),
		fixture.store.Repo,
		fixture.store.WorkRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	reopened = reopened.WithAuthorityPorts(fixture.store.authority)
	replayed, err := reopened.DecideVerification(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, decided) || launch.Calls() != 0 {
		t.Fatalf(
			"exact verification decision replay changed state: %#v",
			replayed,
		)
	}

	conflict := request
	conflict.Choice = VerificationDecisionDefer
	if _, err := fixture.store.DecideVerification(
		context.Background(),
		conflict,
	); !errors.Is(err, ErrWorkRunRequestConflict) {
		t.Fatalf("same request ID with another choice error = %v", err)
	}
	if _, err := fixture.store.DecideVerification(
		context.Background(),
		DecideVerificationRequest{
			ExpectedRevision: before.Revision,
			RequestID:        "stale-decision",
			PromptRef:        fixture.prompt.PromptRef,
			Choice:           VerificationDecisionRun,
		},
	); !errors.Is(err, ErrWorkRunRevisionConflict) {
		t.Fatalf("stale verification decision error = %v", err)
	}

	registerTestVerificationDecision(
		t,
		fixture.store,
		fixture.prompt,
		VerificationDecisionDefer,
		testSHARef("consent-actor"),
		testSHARef("consent-defer-after-run"),
		"",
	)
	if _, err := fixture.store.DecideVerification(
		context.Background(),
		DecideVerificationRequest{
			ExpectedRevision: decided.Revision,
			RequestID:        "replace-decision",
			PromptRef:        fixture.prompt.PromptRef,
			Choice:           VerificationDecisionDefer,
		},
	); !errors.Is(err, ErrWorkRunInvalidTransition) {
		t.Fatalf("replacement verification decision error = %v", err)
	}
	current, err := fixture.store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != decided.Revision ||
		current.Disposition == nil ||
		current.Disposition.Kind != DispositionRun ||
		launch.Calls() != 0 {
		t.Fatalf("rejected decision replacement mutated state: %#v", current)
	}
}

func TestWorkRunVerificationDecisionRejectsStaleOrMismatchedOwnerAuthority(
	t *testing.T,
) {
	t.Run("unowned prompt", func(t *testing.T) {
		fixture := newConsentFixture(
			t,
			"long-consent-unowned",
			reviewtransaction.VerificationCostLong,
		)
		before := fixture.state
		_, err := fixture.store.DecideVerification(
			context.Background(),
			DecideVerificationRequest{
				ExpectedRevision: before.Revision,
				RequestID:        "unowned-prompt",
				PromptRef:        fixture.prompt.PromptRef,
				Choice:           VerificationDecisionRun,
			},
		)
		if err == nil {
			t.Fatal("unowned verification prompt was accepted")
		}
		assertConsentStateUnchanged(t, fixture.store, before, nil)
	})

	t.Run("mismatched forecast", func(t *testing.T) {
		fixture := newConsentFixture(
			t,
			"long-consent-mismatch",
			reviewtransaction.VerificationCostLong,
		)
		foreign := fixture.prompt
		foreign.ForecastRef = testSHARef("foreign-forecast")
		foreign.AssumptionsRef = foreign.ForecastRef
		var err error
		foreign.PromptRef, err = verificationDecisionPromptRef(foreign)
		if err != nil {
			t.Fatal(err)
		}
		registerTestVerificationDecision(
			t,
			fixture.store,
			foreign,
			VerificationDecisionRun,
			testSHARef("foreign-actor"),
			testSHARef("foreign-decision"),
			"",
		)
		before := fixture.state
		_, err = fixture.store.DecideVerification(
			context.Background(),
			DecideVerificationRequest{
				ExpectedRevision: before.Revision,
				RequestID:        "foreign-forecast",
				PromptRef:        foreign.PromptRef,
				Choice:           VerificationDecisionRun,
			},
		)
		if err == nil {
			t.Fatal("foreign verification forecast was accepted")
		}
		assertConsentStateUnchanged(t, fixture.store, before, nil)
	})

	t.Run("authority substitutes choice", func(t *testing.T) {
		fixture := newConsentFixture(
			t,
			"long-consent-choice-mismatch",
			reviewtransaction.VerificationCostLong,
		)
		authority := testAuthorityForStore(t, fixture.store)
		authority.mu.Lock()
		authority.verificationDecisions[verificationDecisionLookup{
			promptRef: fixture.prompt.PromptRef,
			choice:    VerificationDecisionRun,
		}] = VerificationDecisionAuthority{
			Prompt:      fixture.prompt,
			Choice:      VerificationDecisionDefer,
			ActorRef:    testSHARef("choice-actor"),
			DecisionRef: testSHARef("choice-decision"),
		}
		authority.mu.Unlock()
		before := fixture.state
		_, err := fixture.store.DecideVerification(
			context.Background(),
			DecideVerificationRequest{
				ExpectedRevision: before.Revision,
				RequestID:        "choice-substitution",
				PromptRef:        fixture.prompt.PromptRef,
				Choice:           VerificationDecisionRun,
			},
		)
		if !errors.Is(err, ErrAuthorityBindingMismatch) {
			t.Fatalf("substituted owner choice error = %v", err)
		}
		assertConsentStateUnchanged(t, fixture.store, before, nil)
	})
}

func TestVerificationDecisionResumeRevisionSurvivesCorrectionReplan(
	t *testing.T,
) {
	fixture := newConsentFixture(
		t,
		"long-consent-correction-replan",
		reviewtransaction.VerificationCostLong,
	)
	registerTestVerificationDecision(
		t,
		fixture.store,
		fixture.prompt,
		VerificationDecisionRun,
		testSHARef("correction-consent-actor"),
		testSHARef("correction-consent-decision"),
		"",
	)
	decided, err := fixture.store.DecideVerification(
		context.Background(),
		DecideVerificationRequest{
			ExpectedRevision: fixture.state.Revision,
			RequestID:        "accept-before-correction",
			PromptRef:        fixture.prompt.PromptRef,
			Choice:           VerificationDecisionRun,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resumeRevision := decided.ProductiveResumeRevision
	if resumeRevision == "" || resumeRevision != decided.Revision {
		t.Fatalf("verification decision resume revision = %q", resumeRevision)
	}

	if err := os.WriteFile(
		filepath.Join(fixture.store.Repo, "tracked.txt"),
		[]byte("corrected\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	correctedHandoff := testHandoffForSnapshot(
		t,
		fixture.store,
		decided.Handoff.Route,
		decided.Handoff.ScopeDigest,
		currentWorkRunSnapshot(t, fixture.store.Repo),
		decided.Handoff.DeclaredObligationRefs,
		decided.Handoff.EvidenceRefs,
		decided.Handoff.SDDRunRef,
	)
	replanned, err := fixture.store.ReplanVerificationAfterCorrection(
		context.Background(),
		ReplanVerificationAfterCorrectionRequest{
			ExpectedRevision: decided.Revision,
			RequestID:        "replan-after-consented-correction",
			CorrectedHandoff: correctedHandoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if replanned.Revision == resumeRevision ||
		replanned.ProductiveResumeRevision != resumeRevision ||
		replanned.Forecast != nil ||
		replanned.Disposition != nil {
		t.Fatalf("correction replan lost bounded resume anchor: %#v", replanned)
	}
	status, err := fixture.store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Revision != resumeRevision ||
		status.PublicState != PublicStateChecking {
		t.Fatalf("correction replan public status = %#v", status)
	}

	reopened, err := OpenWorkRunStore(
		context.Background(),
		fixture.store.Repo,
		fixture.store.WorkRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	reopened = reopened.WithAuthorityPorts(fixture.store.authority)
	replayed, err := reopened.Status()
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ProductiveResumeRevision != resumeRevision {
		t.Fatalf("replayed correction resume revision = %q", replayed.ProductiveResumeRevision)
	}
	replayedStatus, err := reopened.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if replayedStatus.Revision != resumeRevision {
		t.Fatalf("replayed correction public revision = %q", replayedStatus.Revision)
	}
}

func TestWorkRunDeferredVerificationPersistsWithoutBeginOrProductiveBlocker(
	t *testing.T,
) {
	fixture := newConsentFixture(
		t,
		"long-consent-defer",
		reviewtransaction.VerificationCostVeryLong,
	)
	launch := &countingLaunchAuthority{}
	fixture.store.authority.Launch = launch
	registerTestVerificationDecision(
		t,
		fixture.store,
		fixture.prompt,
		VerificationDecisionDefer,
		testSHARef("defer-actor"),
		testSHARef("defer-decision"),
		"",
	)
	decided, err := fixture.store.DecideVerification(
		context.Background(),
		DecideVerificationRequest{
			ExpectedRevision: fixture.state.Revision,
			RequestID:        "defer-verification",
			PromptRef:        fixture.prompt.PromptRef,
			Choice:           VerificationDecisionDefer,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decided.Disposition == nil ||
		decided.Disposition.Kind != DispositionDefer ||
		decided.ProductiveBlockerRef != "" ||
		len(decided.Reservations) != 0 ||
		len(decided.LaunchClaims) != 0 ||
		launch.Calls() != 0 {
		t.Fatalf("deferred verification state = %#v", decided)
	}
}

func TestWorkRunDeferredRunnerRequiresOwnerRunnerAuthorityWithoutLaunch(
	t *testing.T,
) {
	fixture := newConsentFixture(
		t,
		"long-consent-deferred-runner",
		reviewtransaction.VerificationCostVeryLong,
	)
	fixture.prompt.Choices = append(
		fixture.prompt.Choices,
		VerificationDecisionDeferredRunner,
	)
	var err error
	fixture.prompt.PromptRef, err = verificationDecisionPromptRef(fixture.prompt)
	if err != nil {
		t.Fatal(err)
	}
	launch := &countingLaunchAuthority{}
	fixture.store.authority.Launch = launch
	request := DecideVerificationRequest{
		ExpectedRevision: fixture.state.Revision,
		RequestID:        "deferred-runner-without-owner-runner",
		PromptRef:        fixture.prompt.PromptRef,
		Choice:           VerificationDecisionDeferredRunner,
	}
	registerTestVerificationDecision(
		t,
		fixture.store,
		fixture.prompt,
		request.Choice,
		testSHARef("deferred-runner-actor"),
		testSHARef("deferred-runner-decision"),
		"",
	)
	if _, err := fixture.store.DecideVerification(
		context.Background(),
		request,
	); err == nil {
		t.Fatal("deferred runner without owner runner authority was accepted")
	}
	assertConsentStateUnchanged(t, fixture.store, fixture.state, launch)

	request.RequestID = "deferred-runner-with-owner-runner"
	runnerRef := testSHARef("owner-deferred-runner")
	registerTestVerificationDecision(
		t,
		fixture.store,
		fixture.prompt,
		request.Choice,
		testSHARef("deferred-runner-actor"),
		testSHARef("deferred-runner-decision"),
		runnerRef,
	)
	decided, err := fixture.store.DecideVerification(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if decided.Disposition == nil ||
		decided.Disposition.Kind != DispositionDeferredRunner ||
		decided.Disposition.RunnerRef != runnerRef ||
		len(decided.Reservations) != 0 ||
		len(decided.LaunchClaims) != 0 ||
		launch.Calls() != 0 {
		t.Fatalf("owner deferred-runner decision = %#v", decided)
	}
}

func TestWorkRunQuickConsentIsDurableAndVisibleBeforeDecision(t *testing.T) {
	fixture := newConsentFixture(
		t,
		"quick-consent-required",
		reviewtransaction.VerificationCostQuick,
	)
	if !fixture.state.Forecast.RequiresConsent {
		t.Fatal("quick owner consent was not sealed into the forecast")
	}
	status, err := fixture.store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicState != PublicStateNeedsYourDecision {
		t.Fatalf("quick consent public state = %q", status.PublicState)
	}
	if status.Revision != fixture.state.Revision ||
		fixture.prompt.ExpectedRevision != fixture.state.Revision ||
		fixture.prompt.ExpectedRevision != status.Revision {
		t.Fatalf(
			"checkpoint CAS projection status=%q prompt=%q state=%q",
			status.Revision,
			fixture.prompt.ExpectedRevision,
			fixture.state.Revision,
		)
	}

	automatic := *fixture.state.Forecast
	automatic.RequiresConsent = false
	automatic.Digest, err = verificationForecastDigest(automatic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewVerificationDecisionPrompt(
		VerificationDecisionPromptRequest{
			WorkRunID:        fixture.store.WorkRunID,
			ExpectedRevision: fixture.state.Revision,
			Forecast:         automatic,
			AssumptionsRef:   testSHARef("automatic-quick-assumptions"),
			Choices: []VerificationDecisionChoice{
				VerificationDecisionRun,
				VerificationDecisionDefer,
				VerificationDecisionReduceScope,
			},
		},
	); err == nil {
		t.Fatal("automatic quick forecast produced a consent prompt")
	}
}

func newConsentFixture(
	t *testing.T,
	workRunID string,
	cost reviewtransaction.VerificationCost,
) consentFixture {
	t.Helper()
	repo := initWorkRunRepository(t)
	applicability, registry, plan := verificationFixtureWithCost(
		t,
		repo,
		true,
		cost,
	)
	store := openTestWorkRunStore(t, repo, workRunID)
	state := startDirectWorkRun(t, store)
	var err error
	state, err = store.BeginProductiveAdvance(
		context.Background(),
		BeginProductiveAdvanceRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "begin-productive-advance",
			SourceRevision:   state.Revision,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	handoff := testHandoffForSnapshot(
		t,
		store,
		ImplementationRouteDirectInline,
		testSHARef(workRunID+"-scope"),
		currentWorkRunSnapshot(t, repo),
		verificationRequirementRefs(plan),
		[]string{},
		"",
	)
	state, err = store.BindImplementationHandoff(
		context.Background(),
		BindImplementationHandoffRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "bind-handoff",
			Handoff:          handoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	input := VerificationForecastInput{
		WorkRunID:       store.WorkRunID,
		Handoff:         handoff,
		Applicability:   applicability,
		Registry:        registry,
		Plan:            plan,
		PlanRevisionRef: testSHARef(workRunID + "-plan"),
		Availability:    ForecastAvailable,
		AvailabilityRef: testSHARef(workRunID + "-availability"),
		RequiresConsent: true,
		DiagnosticRefs:  []string{},
	}
	registerTestForecast(t, store, input)
	state, err = store.RecordVerificationForecast(
		context.Background(),
		RecordVerificationForecastRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "record-forecast",
			Input:            input,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := NewVerificationDecisionPrompt(
		VerificationDecisionPromptRequest{
			WorkRunID:        store.WorkRunID,
			ExpectedRevision: state.Revision,
			Forecast:         *state.Forecast,
			AssumptionsRef:   testSHARef(workRunID + "-assumptions"),
			Choices: []VerificationDecisionChoice{
				VerificationDecisionRun,
				VerificationDecisionDefer,
				VerificationDecisionReduceScope,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return consentFixture{
		store:         store,
		state:         state,
		prompt:        prompt,
		applicability: applicability,
		registry:      registry,
		plan:          plan,
	}
}

func registerTestVerificationDecision(
	t *testing.T,
	store WorkRunStore,
	prompt VerificationDecisionPromptV1,
	choice VerificationDecisionChoice,
	actorRef string,
	decisionRef string,
	runnerRef string,
) {
	t.Helper()
	authority := testAuthorityForStore(t, store)
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.verificationDecisions[verificationDecisionLookup{
		promptRef: prompt.PromptRef,
		choice:    choice,
	}] = VerificationDecisionAuthority{
		Prompt:      prompt,
		Choice:      choice,
		ActorRef:    actorRef,
		DecisionRef: decisionRef,
		RunnerRef:   runnerRef,
	}
}

func consentFixtureTicket(
	t *testing.T,
	fixture consentFixture,
) AdmittedActionTicket {
	t.Helper()
	obligation := fixture.plan.Obligations[0]
	return AdmittedActionTicket{
		TicketRef:              testSHARef("pre-consent-ticket"),
		SubjectRef:             fixture.applicability.Digest,
		CandidateRef:           fixture.state.Handoff.CandidateRef,
		VerificationContextRef: fixture.state.Forecast.Digest,
		ExpectedRevision:       fixture.state.Forecast.PlanRevisionRef,
		Slot:                   obligation.ID,
		Capability:             obligation.CapabilityRef,
		SlotBindingRef:         testSHARef("pre-consent-slot"),
		HostRequestDigest:      testSHARef("pre-consent-host-request"),
	}
}

func assertConsentStateUnchanged(
	t *testing.T,
	store WorkRunStore,
	before WorkRunState,
	launch *countingLaunchAuthority,
) {
	t.Helper()
	after, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected verification decision mutated WorkRun: %#v", after)
	}
	if launch != nil && launch.Calls() != 0 {
		t.Fatalf("rejected verification decision activated HCR %d times", launch.Calls())
	}
}
