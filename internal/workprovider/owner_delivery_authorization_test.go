package workprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestOwnerCoordinatorIssuesAndBindsNormalDeliveryAuthorization(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "owner-normal-authorization")
	admission := fixture.admitDefaultDelivery(t, "owner-normal-authorization")
	terminal := ownerPrepareAuthorizationTerminal(
		t,
		fixture,
		admission,
		"owner-normal-authorization",
		reviewtransaction.VerificationAggregateNotRequired,
		false,
	)
	decisionRef := ownerPublishAuthorizationDecision(
		t,
		fixture,
		admission,
		terminal.authority,
		"normal-primary",
		deliveryadmission.DeliveryAuthorizeNormal,
	)

	root := filepath.Join(fixture.commonDir, "gentle-ai")
	stale := OwnerIssueDeliveryAuthorizationRequest{
		ExpectedRevision: ownerTestRef("stale-normal-authorization"),
		DecisionRef:      decisionRef,
	}
	beforeStale := ownerTreeContentFingerprint(t, root)
	if _, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		stale,
	); !errors.As(err, new(*workrun.RevisionConflictError)) {
		t.Fatalf("stale authorization issue error = %v", err)
	}
	if after := ownerTreeContentFingerprint(t, root); after != beforeStale {
		t.Fatalf(
			"stale authorization published authority:\nbefore %s\nafter  %s",
			beforeStale,
			after,
		)
	}

	mismatchedAdmission := fixture.admitDefaultDelivery(
		t,
		"owner-normal-mismatched-intent",
	)
	mismatchedDecisionRef := ownerPublishAuthorizationDecision(
		t,
		fixture,
		mismatchedAdmission,
		terminal.authority,
		"normal-mismatched-intent",
		deliveryadmission.DeliveryAuthorizeNormal,
	)
	beforeMismatch := ownerTreeContentFingerprint(t, root)
	if _, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		OwnerIssueDeliveryAuthorizationRequest{
			ExpectedRevision: terminal.state.Revision,
			DecisionRef:      mismatchedDecisionRef,
		},
	); !errors.Is(err, deliveryadmission.ErrBindingMismatch) {
		t.Fatalf("mismatched decision error = %v", err)
	}
	if after := ownerTreeContentFingerprint(t, root); after != beforeMismatch {
		t.Fatalf(
			"mismatched decision published authority:\nbefore %s\nafter  %s",
			beforeMismatch,
			after,
		)
	}

	alternateDecisionRef := ownerPublishAuthorizationDecision(
		t,
		fixture,
		admission,
		terminal.authority,
		"normal-alternate-gate",
		deliveryadmission.DeliveryAuthorizeNormal,
	)
	request := OwnerIssueDeliveryAuthorizationRequest{
		ExpectedRevision: terminal.state.Revision,
		DecisionRef:      decisionRef,
	}
	delivered, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.DeliveryAuthorizationRef == "" {
		t.Fatalf("normal delivery state = %#v", delivered)
	}
	live, err := fixture.pad.ResolveLiveAuthorization(
		context.Background(),
		delivered.DeliveryAuthorizationRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if live.Kind != deliveryadmission.AuthorizationNormal ||
		live.Binding.DecisionRef != decisionRef ||
		live.Binding.AuthorityRef != admission.AuthorityRef ||
		live.Binding.IntentRef != terminal.state.DeliveryIntentRef ||
		live.Binding.Candidate.Digest != terminal.state.Handoff.CandidateRef ||
		live.Binding.VerificationResultRef !=
			terminal.state.VerificationResultRef ||
		live.Binding.ReviewReceiptRef != terminal.state.ReviewReceiptRef {
		t.Fatalf("normal live authorization = %#v", live)
	}

	beforeReplay := ownerTreeContentFingerprint(t, root)
	replayed, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Revision != delivered.Revision ||
		replayed.DeliveryAuthorizationRef !=
			delivered.DeliveryAuthorizationRef {
		t.Fatalf("normal authorization replay = %#v", replayed)
	}
	if after := ownerTreeContentFingerprint(t, root); after != beforeReplay {
		t.Fatalf(
			"normal replay reissued authority:\nbefore %s\nafter  %s",
			beforeReplay,
			after,
		)
	}

	beforeConflict := ownerTreeContentFingerprint(t, root)
	if _, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		OwnerIssueDeliveryAuthorizationRequest{
			ExpectedRevision: terminal.state.Revision,
			DecisionRef:      alternateDecisionRef,
		},
	); !errors.Is(err, deliveryadmission.ErrTrustedRepositoryConflict) {
		t.Fatalf("bound authorization conflict error = %v", err)
	}
	if after := ownerTreeContentFingerprint(t, root); after != beforeConflict {
		t.Fatalf(
			"authorization conflict published authority:\nbefore %s\nafter  %s",
			beforeConflict,
			after,
		)
	}
}

func TestOwnerCoordinatorCorrectedUnavailableUsesPADExceptionWithoutRestartingReview(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "owner-corrected-exception")
	admission := ownerAdmitIncompleteDelivery(
		t,
		fixture,
		"owner-corrected-exception",
	)
	terminal := ownerPrepareAuthorizationTerminal(
		t,
		fixture,
		admission,
		"owner-corrected-exception",
		reviewtransaction.VerificationAggregateUnavailable,
		true,
	)
	decisionRef := ownerPublishAuthorizationDecision(
		t,
		fixture,
		admission,
		terminal.authority,
		"corrected-unavailable",
		deliveryadmission.DeliveryRequireException,
	)
	humanDecisionRef := ownerPublishExceptionGovernance(
		t,
		fixture,
		decisionRef,
	)

	delivered, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		OwnerIssueDeliveryAuthorizationRequest{
			ExpectedRevision: terminal.state.Revision,
			DecisionRef:      decisionRef,
			HumanDecisionRef: humanDecisionRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.WorkRunID != terminal.started.WorkRunID ||
		delivered.DeliveryIntentRef != terminal.started.DeliveryIntentRef ||
		delivered.RouteDecision.Digest != terminal.started.RouteDecision.Digest ||
		delivered.ImplementationRoute !=
			terminal.started.ImplementationRoute ||
		delivered.VerificationReplan == nil ||
		delivered.VerificationReplan.OriginalPlanDigest !=
			terminal.initialPlan.Digest ||
		delivered.VerificationResultRef != terminal.authority.Result.ResultRef ||
		delivered.ReviewReceiptRef != terminal.authority.Receipt.ReceiptRef ||
		delivered.DeliveryAuthorizationRef == "" {
		t.Fatalf("corrected exception WorkRun = %#v", delivered)
	}
	resolvedResult, err := fixture.coordinator.rar.ResolveResult(
		context.Background(),
		delivered.VerificationResultRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedResult.Result.Aggregate !=
		reviewtransaction.VerificationAggregateUnavailable {
		t.Fatalf(
			"corrected aggregate = %q",
			resolvedResult.Result.Aggregate,
		)
	}
	live, err := fixture.pad.ResolveLiveAuthorization(
		context.Background(),
		delivered.DeliveryAuthorizationRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if live.Kind != deliveryadmission.AuthorizationException ||
		live.Binding.DecisionRef != decisionRef ||
		live.Binding.Candidate.Digest != delivered.Handoff.CandidateRef ||
		live.Binding.VerificationResultRef != delivered.VerificationResultRef ||
		live.Binding.ReviewReceiptRef != delivered.ReviewReceiptRef {
		t.Fatalf("corrected exception live authorization = %#v", live)
	}
	exception, err := fixture.pad.ResolveExceptionAuthorization(
		context.Background(),
		delivered.DeliveryAuthorizationRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if exception.HumanDecisionRef != humanDecisionRef {
		t.Fatalf("exception human decision = %q", exception.HumanDecisionRef)
	}

	root := filepath.Join(fixture.commonDir, "gentle-ai")
	beforeReplay := ownerTreeContentFingerprint(t, root)
	replayed, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		OwnerIssueDeliveryAuthorizationRequest{
			ExpectedRevision: terminal.state.Revision,
			DecisionRef:      decisionRef,
			HumanDecisionRef: humanDecisionRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Revision != delivered.Revision {
		t.Fatalf("corrected exception replay = %#v", replayed)
	}
	if after := ownerTreeContentFingerprint(t, root); after != beforeReplay {
		t.Fatalf(
			"corrected exception replay reissued authority:\nbefore %s\nafter  %s",
			beforeReplay,
			after,
		)
	}
}

func TestOwnerCoordinatorBlockedPADDecisionPublishesNoAuthorization(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "owner-blocked-authorization")
	admission := fixture.admitDefaultDelivery(t, "owner-blocked-authorization")
	terminal := ownerPrepareAuthorizationTerminal(
		t,
		fixture,
		admission,
		"owner-blocked-authorization",
		reviewtransaction.VerificationAggregateUnavailable,
		false,
	)
	decisionRef := ownerPublishAuthorizationDecision(
		t,
		fixture,
		admission,
		terminal.authority,
		"blocked",
		deliveryadmission.DeliveryBlocked,
	)
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	before := ownerTreeContentFingerprint(t, root)
	if _, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		OwnerIssueDeliveryAuthorizationRequest{
			ExpectedRevision: terminal.state.Revision,
			DecisionRef:      decisionRef,
		},
	); !errors.Is(err, deliveryadmission.ErrUnauthorized) {
		t.Fatalf("blocked authorization error = %v", err)
	}
	if after := ownerTreeContentFingerprint(t, root); after != before {
		t.Fatalf(
			"blocked decision published authority:\nbefore %s\nafter  %s",
			before,
			after,
		)
	}
}

func TestOwnerCoordinatorConcurrentAuthorizationReplayPublishesOnceAcrossClockTick(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "owner-concurrent-authorization")
	admission := fixture.admitDefaultDelivery(
		t,
		"owner-concurrent-authorization",
	)
	terminal := ownerPrepareAuthorizationTerminal(
		t,
		fixture,
		admission,
		"owner-concurrent-authorization",
		reviewtransaction.VerificationAggregateNotRequired,
		false,
	)
	decisionRef := ownerPublishAuthorizationDecision(
		t,
		fixture,
		admission,
		terminal.authority,
		"concurrent",
		deliveryadmission.DeliveryAuthorizeNormal,
	)

	control := newOwnerAuthorizationRaceControl(fixture.now)
	originalOpen := fixture.coordinator.pad.open
	fixture.coordinator.pad.open = func(
		ctx context.Context,
		authority *PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		opened, err := originalOpen(ctx, authority)
		if err != nil {
			return nil, err
		}
		repository, ok := opened.(*deliveryadmission.TrustedRepository)
		if !ok {
			_ = opened.Close()
			return nil, errors.New("test PAD repository has unexpected type")
		}
		return &ownerAuthorizationRaceRepository{
			TrustedRepository: repository,
			control:           control,
		}, nil
	}

	request := OwnerIssueDeliveryAuthorizationRequest{
		ExpectedRevision: terminal.state.Revision,
		DecisionRef:      decisionRef,
	}
	type outcome struct {
		state workrun.WorkRunState
		err   error
	}
	outcomes := make(chan outcome, 2)
	go func() {
		state, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
			context.Background(),
			request,
		)
		outcomes <- outcome{state: state, err: err}
	}()
	<-control.firstClockRead
	control.now.Store(fixture.now + 1)
	go func() {
		state, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
			context.Background(),
			request,
		)
		outcomes <- outcome{state: state, err: err}
	}()
	close(control.releaseFirstClock)

	first := <-outcomes
	second := <-outcomes
	if first.err != nil || second.err != nil {
		t.Fatalf(
			"concurrent authorization errors = %v / %v",
			first.err,
			second.err,
		)
	}
	if first.state.DeliveryAuthorizationRef == "" ||
		first.state.DeliveryAuthorizationRef !=
			second.state.DeliveryAuthorizationRef ||
		first.state.Revision != second.state.Revision {
		t.Fatalf(
			"concurrent authorization states = %#v / %#v",
			first.state,
			second.state,
		)
	}
	if got := control.normalPublications.Load(); got != 1 {
		t.Fatalf("normal authorization publications = %d, want 1", got)
	}

	root := filepath.Join(fixture.commonDir, "gentle-ai")
	beforeReplay := ownerTreeContentFingerprint(t, root)
	replayed, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.DeliveryAuthorizationRef !=
		first.state.DeliveryAuthorizationRef {
		t.Fatalf("post-race replay = %#v", replayed)
	}
	if got := control.normalPublications.Load(); got != 1 {
		t.Fatalf("post-race publications = %d, want 1", got)
	}
	if after := ownerTreeContentFingerprint(t, root); after != beforeReplay {
		t.Fatalf(
			"post-race exact replay changed authority:\nbefore %s\nafter  %s",
			beforeReplay,
			after,
		)
	}
}

func TestOwnerCoordinatorRecoversExactAuthorizationAfterLostPublishResponse(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "owner-lost-authorization-response")
	admission := fixture.admitDefaultDelivery(
		t,
		"owner-lost-authorization-response",
	)
	terminal := ownerPrepareAuthorizationTerminal(
		t,
		fixture,
		admission,
		"owner-lost-authorization-response",
		reviewtransaction.VerificationAggregateNotRequired,
		false,
	)
	decisionRef := ownerPublishAuthorizationDecision(
		t,
		fixture,
		admission,
		terminal.authority,
		"lost-response",
		deliveryadmission.DeliveryAuthorizeNormal,
	)

	control := &ownerAuthorizationLostResponseControl{}
	control.now.Store(fixture.now)
	control.failAfterPublish.Store(true)
	originalOpen := fixture.coordinator.pad.open
	fixture.coordinator.pad.open = func(
		ctx context.Context,
		authority *PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		opened, err := originalOpen(ctx, authority)
		if err != nil {
			return nil, err
		}
		repository, ok := opened.(*deliveryadmission.TrustedRepository)
		if !ok {
			_ = opened.Close()
			return nil, errors.New("test PAD repository has unexpected type")
		}
		return &ownerAuthorizationLostResponseRepository{
			TrustedRepository: repository,
			control:           control,
		}, nil
	}
	request := OwnerIssueDeliveryAuthorizationRequest{
		ExpectedRevision: terminal.state.Revision,
		DecisionRef:      decisionRef,
	}
	if _, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		request,
	); !errors.Is(err, errOwnerAuthorizationLostResponse) {
		t.Fatalf("lost authorization response error = %v", err)
	}
	afterFailure, err := fixture.coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.Revision != terminal.state.Revision ||
		afterFailure.DeliveryAuthorizationRef != "" {
		t.Fatalf("lost-response WorkRun state = %#v", afterFailure)
	}
	journal, err := resolveOwnerPADAuthorizationJournal(
		context.Background(),
		fixture.coordinator,
	)
	if err != nil {
		t.Fatal(err)
	}
	live, err := fixture.pad.ResolveLiveAuthorization(
		context.Background(),
		journal.AuthorizationRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if live.Binding.DecisionRef != decisionRef ||
		control.publications.Load() != 1 {
		t.Fatalf(
			"prepared lost-response authorization = %#v / publications %d",
			live,
			control.publications.Load(),
		)
	}

	control.now.Store(fixture.now + 1)
	delivered, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.DeliveryAuthorizationRef != journal.AuthorizationRef ||
		control.publications.Load() != 1 {
		t.Fatalf(
			"recovered authorization = %#v / publications %d",
			delivered,
			control.publications.Load(),
		)
	}
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	beforeReplay := ownerTreeContentFingerprint(t, root)
	if _, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		request,
	); err != nil {
		t.Fatal(err)
	}
	if control.publications.Load() != 1 ||
		ownerTreeContentFingerprint(t, root) != beforeReplay {
		t.Fatal("recovered exact replay reissued authorization authority")
	}
}

func TestOwnerCoordinatorExpiredPreparedAuthorizationFailsClosedWithoutRepublish(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "owner-expired-prepared-auth")
	admission := fixture.admitDefaultDelivery(
		t,
		"owner-expired-prepared-auth",
	)
	terminal := ownerPrepareAuthorizationTerminal(
		t,
		fixture,
		admission,
		"owner-expired-prepared-auth",
		reviewtransaction.VerificationAggregateNotRequired,
		false,
	)
	decisionRef := ownerPublishAuthorizationDecision(
		t,
		fixture,
		admission,
		terminal.authority,
		"expired-prepared",
		deliveryadmission.DeliveryAuthorizeNormal,
	)
	control := &ownerAuthorizationLostResponseControl{}
	control.now.Store(fixture.now)
	control.failBeforePublish.Store(true)
	originalOpen := fixture.coordinator.pad.open
	fixture.coordinator.pad.open = func(
		ctx context.Context,
		authority *PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		opened, err := originalOpen(ctx, authority)
		if err != nil {
			return nil, err
		}
		repository, ok := opened.(*deliveryadmission.TrustedRepository)
		if !ok {
			_ = opened.Close()
			return nil, errors.New("test PAD repository has unexpected type")
		}
		return &ownerAuthorizationLostResponseRepository{
			TrustedRepository: repository,
			control:           control,
		}, nil
	}
	request := OwnerIssueDeliveryAuthorizationRequest{
		ExpectedRevision: terminal.state.Revision,
		DecisionRef:      decisionRef,
	}
	if _, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
		context.Background(),
		request,
	); !errors.Is(err, errOwnerAuthorizationLostResponse) {
		t.Fatalf("prepared authorization failure = %v", err)
	}
	journal, err := resolveOwnerPADAuthorizationJournal(
		context.Background(),
		fixture.coordinator,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pad.ResolveAuthorization(
		context.Background(),
		journal.AuthorizationRef,
	); !errors.Is(err, deliveryadmission.ErrTrustedRepositoryUnavailable) {
		t.Fatalf("unexpected prepared authorization object error = %v", err)
	}
	control.now.Store(journal.ExpiresAt)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
			context.Background(),
			request,
		); !errors.Is(
			err,
			ErrPADDeliveryAuthorizationPreparedExpired,
		) || !errors.Is(err, deliveryadmission.ErrExpired) {
			t.Fatalf("expired prepared retry %d error = %v", attempt, err)
		}
	}
	if control.publications.Load() != 1 {
		t.Fatalf(
			"expired prepared publication attempts = %d, want 1",
			control.publications.Load(),
		)
	}
	state, err := fixture.coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != terminal.state.Revision ||
		state.DeliveryAuthorizationRef != "" {
		t.Fatalf("expired prepared WorkRun state = %#v", state)
	}
}

func TestOwnerCoordinatorRechecksAuthorizationActivationUnderDeliveryLock(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "owner-authorization-activation")
	admission := fixture.admitDefaultDelivery(
		t,
		"owner-authorization-activation",
	)
	terminal := ownerPrepareAuthorizationTerminal(
		t,
		fixture,
		admission,
		"owner-authorization-activation",
		reviewtransaction.VerificationAggregateNotRequired,
		false,
	)
	decisionRef := ownerPublishAuthorizationDecision(
		t,
		fixture,
		admission,
		terminal.authority,
		"activation",
		deliveryadmission.DeliveryAuthorizeNormal,
	)
	lock, err := acquirePADDeliveryOwnerLock(
		context.Background(),
		fixture.coordinator.work.Dir,
	)
	if err != nil {
		t.Fatal(err)
	}
	activation := newOwnerAuthorizationActivationResolver(
		ActivationEnabled,
	)
	fixture.coordinator.activation = activation
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	before := ownerTreeContentFingerprint(t, root)
	result := make(chan error, 1)
	go func() {
		_, callErr :=
			fixture.coordinator.IssueAndBindDeliveryAuthorization(
				context.Background(),
				OwnerIssueDeliveryAuthorizationRequest{
					ExpectedRevision: terminal.state.Revision,
					DecisionRef:      decisionRef,
				},
			)
		result <- callErr
	}()
	<-activation.firstResolution
	activation.mode.Store(string(ActivationDisabled))
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("post-lock authorization activation error = %v", err)
	}
	if after := ownerTreeContentFingerprint(t, root); after != before {
		t.Fatalf(
			"disabled-under-lock authorization published authority:\nbefore %s\nafter  %s",
			before,
			after,
		)
	}
	state, err := fixture.coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != terminal.state.Revision ||
		state.DeliveryAuthorizationRef != "" {
		t.Fatalf("disabled-under-lock authorization state = %#v", state)
	}
}

func TestOwnerDeliveryAuthorizationRequestHasNoCallerAuthoritySurface(
	t *testing.T,
) {
	typ := reflect.TypeOf(OwnerIssueDeliveryAuthorizationRequest{})
	want := []string{"ExpectedRevision", "DecisionRef", "HumanDecisionRef"}
	if typ.NumField() != len(want) {
		t.Fatalf(
			"delivery authorization request fields = %d, want %d",
			typ.NumField(),
			len(want),
		)
	}
	for index, name := range want {
		if typ.Field(index).Name != name {
			t.Fatalf(
				"delivery authorization field %d = %q, want %q",
				index,
				typ.Field(index).Name,
				name,
			)
		}
	}
}

type ownerAuthorizationTerminal struct {
	started     workrun.WorkRunState
	state       workrun.WorkRunState
	initialPlan reviewtransaction.VerificationPlan
	authority   reviewtransaction.RARVerificationAuthority
}

func ownerPrepareAuthorizationTerminal(
	t *testing.T,
	fixture ownerCoordinatorFixture,
	admission ownerAdmittedDelivery,
	seed string,
	aggregate reviewtransaction.VerificationAggregate,
	corrected bool,
) ownerAuthorizationTerminal {
	t.Helper()
	ctx := context.Background()
	var (
		applicability reviewtransaction.VerificationApplicability
		registry      reviewtransaction.VerificationPlanRegistry
		plan          reviewtransaction.VerificationPlan
		snapshot      reviewtransaction.Snapshot
	)
	if aggregate == reviewtransaction.VerificationAggregateNotRequired {
		applicability, registry, plan, snapshot = ownerNotRequiredPlan(
			t,
			fixture.repo,
			seed,
		)
	} else {
		applicability, registry, plan, snapshot = ownerVerificationPlan(
			t,
			fixture.repo,
			seed,
		)
	}
	initialPlan := plan
	planAuthority, err := fixture.coordinator.PublishVerificationPlan(
		ctx,
		reviewtransaction.RARPlanPublication{
			Snapshot:      snapshot,
			Applicability: applicability,
			Registry:      registry,
			Plan:          plan,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	started, err := fixture.coordinator.StartWork(
		ctx,
		OwnerStartWorkRequest{
			DeliveryIntentRef: admission.Admission.IntentRef,
			RouteInput: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	target := reviewtransaction.Target{
		Kind: reviewtransaction.TargetCurrentChanges,
		IntendedUntracked: append(
			[]string(nil),
			snapshot.IntendedUntracked...,
		),
	}
	requirements := ownerAuthorizationPlanRequirements(plan)
	state, err := fixture.coordinator.BindImplementationHandoff(
		ctx,
		OwnerBindHandoffRequest{
			ExpectedRevision:       started.Revision,
			Target:                 target,
			IntendedPaths:          append([]string(nil), snapshot.Paths...),
			DeclaredObligationRefs: requirements,
			EvidenceRefs:           []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	availability := workrun.ForecastAvailable
	if aggregate == reviewtransaction.VerificationAggregateNotRequired {
		availability = workrun.ForecastNotRequired
	}
	state, err = fixture.coordinator.PublishVerificationForecast(
		ctx,
		OwnerForecastRequest{
			ExpectedRevision: state.Revision,
			Handoff:          *state.Handoff,
			PlanRevisionRef:  planAuthority.AuthorityRef,
			Availability:     availability,
			DiagnosticRefs:   []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if corrected {
		logicalPath := seed + ".go"
		if err := os.WriteFile(
			filepath.Join(fixture.repo, logicalPath),
			[]byte(
				"package ownerfixture\n\nfunc value() int { return 2 }\n",
			),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
		state, err = fixture.coordinator.ReplanVerificationAfterCorrection(
			ctx,
			OwnerCorrectionReplanRequest{
				ExpectedRevision:       state.Revision,
				Target:                 target,
				IntendedPaths:          []string{logicalPath},
				DeclaredObligationRefs: requirements,
				EvidenceRefs:           []string{},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		applicability, plan, snapshot =
			ownerAuthorizationCorrectedPlan(
				t,
				fixture.repo,
				target,
				registry,
			)
		planAuthority, err =
			fixture.coordinator.PublishVerificationPlan(
				ctx,
				reviewtransaction.RARPlanPublication{
					Snapshot:      snapshot,
					Applicability: applicability,
					Registry:      registry,
					Plan:          plan,
				},
			)
		if err != nil {
			t.Fatal(err)
		}
		state, err = fixture.coordinator.PublishVerificationForecast(
			ctx,
			OwnerForecastRequest{
				ExpectedRevision: state.Revision,
				Handoff:          *state.Handoff,
				PlanRevisionRef:  planAuthority.AuthorityRef,
				Availability:     availability,
				DiagnosticRefs:   []string{},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if state.WorkRunID != started.WorkRunID ||
			state.RouteDecision.Digest != started.RouteDecision.Digest ||
			state.VerificationReplan == nil ||
			state.VerificationReplan.OriginalPlanDigest != initialPlan.Digest ||
			state.VerificationResultRef != "" ||
			state.ReviewReceiptRef != "" {
			t.Fatalf("correction restarted owner workflow = %#v", state)
		}
	}

	receiptRef := ownerApprovedCompactReceiptRef(
		t,
		fixture.repo,
		seed+"-review",
		snapshot,
		plan.PolicyHash,
	)
	result := reviewtransaction.VerificationResultRef{
		Schema:               reviewtransaction.VerificationResultRefSchema,
		ResultRef:            ownerTestRef(seed + "-result"),
		Subject:              plan.Subject,
		PolicyHash:           plan.PolicyHash,
		PlanDigest:           plan.Digest,
		ApplicabilityDigest:  plan.ApplicabilityDigest,
		Aggregate:            aggregate,
		CompletedObligations: []string{},
		EvidenceRefs:         []string{},
	}
	authority, err := fixture.coordinator.rar.Publish(
		ctx,
		reviewtransaction.RARAuthorityPublication{
			LineageID:     seed + "-review",
			ReceiptRef:    receiptRef,
			Applicability: applicability,
			Registry:      registry,
			Plan:          plan,
			Result:        result,
		},
	)
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
	return ownerAuthorizationTerminal{
		started:     started,
		state:       state,
		initialPlan: initialPlan,
		authority:   authority,
	}
}

func ownerAuthorizationCorrectedPlan(
	t *testing.T,
	repo string,
	target reviewtransaction.Target,
	registry reviewtransaction.VerificationPlanRegistry,
) (
	reviewtransaction.VerificationApplicability,
	reviewtransaction.VerificationPlan,
	reviewtransaction.Snapshot,
) {
	t.Helper()
	builder := reviewtransaction.SnapshotBuilder{Repo: repo}
	snapshot, err := builder.Build(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	applicability, err := builder.ClassifyVerificationApplicability(
		context.Background(),
		snapshot,
		registry,
		[]string{},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := reviewtransaction.BuildVerificationPlan(
		applicability,
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	return applicability, plan, snapshot
}

func ownerAuthorizationPlanRequirements(
	plan reviewtransaction.VerificationPlan,
) []string {
	requirements := make([]string, 0, len(plan.Obligations))
	for _, obligation := range plan.Obligations {
		requirements = append(requirements, obligation.RequirementRef)
	}
	return requirements
}

func ownerPublishAuthorizationDecision(
	t *testing.T,
	fixture ownerCoordinatorFixture,
	admission ownerAdmittedDelivery,
	authority reviewtransaction.RARVerificationAuthority,
	seed string,
	want deliveryadmission.DeliveryDisposition,
) string {
	t.Helper()
	ctx := context.Background()
	gates := deliveryadmission.GateEvidence{
		Schema: deliveryadmission.GateEvidenceContractV1,
		Route:  admission.Intent.Route,
		Candidate: deliveryadmission.CandidateBinding{
			Ref:    "candidate:" + seed,
			Digest: authority.Result.Subject.SnapshotIdentity,
		},
		Destination:        admission.Intent.Destination,
		PolicyRef:          admission.PolicyRef,
		Policy:             admission.Policy.Binding(),
		Mechanism:          deliveryadmission.MechanismPullRequest,
		ProtectionMode:     deliveryadmission.ProtectionRespect,
		PullRequestRef:     "github:pr/" + seed,
		RequiredChecksRef:  ownerTestRef(seed + "-required-checks"),
		RemoteFreshnessRef: ownerTestRef(seed + "-remote-freshness"),
		ProtectionRef:      ownerTestRef(seed + "-protection"),
		ObservedAt:         fixture.now - 1,
		ExpiresAt:          fixture.now + 300,
	}
	gateRef, err := fixture.pad.PublishGateEvidence(ctx, gates)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := deliveryadmission.Decide(
		ctx,
		fixture.pad,
		fixture.coordinator.rar,
		deliveryadmission.DeliveryRequest{
			AdmissionDecisionRef:  admission.Admission.AdmissionDecisionRef,
			PolicyRef:             admission.PolicyRef,
			ReviewReceiptRef:      authority.Receipt.ReceiptRef,
			VerificationResultRef: authority.Result.ResultRef,
			GateRef:               gateRef,
			AuthorityRef:          admission.AuthorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Disposition != want {
		t.Fatalf(
			"delivery disposition = %q, want %q",
			decision.Disposition,
			want,
		)
	}
	decisionRef, err := fixture.pad.PublishDeliveryDecision(ctx, decision)
	if err != nil {
		t.Fatal(err)
	}
	return decisionRef
}

func ownerAdmitIncompleteDelivery(
	t *testing.T,
	fixture ownerCoordinatorFixture,
	seed string,
) ownerAdmittedDelivery {
	t.Helper()
	ctx := context.Background()
	policy, err := deliveryadmission.NewRoutePolicy(
		"policy:"+seed,
		"revision:1",
		deliveryadmission.RoutePRWithoutIssue,
		true,
		true,
		false,
		120,
		90,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyRef, err := fixture.pad.PublishRoutePolicy(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pad.PutLiveRoutePolicy(
		ctx,
		deliveryadmission.LiveRoutePolicyUpdate{
			Route:     deliveryadmission.RoutePRWithoutIssue,
			PolicyRef: policyRef,
		},
	); err != nil {
		t.Fatal(err)
	}
	authority := deliveryadmission.AuthoritySignal{
		Schema:     deliveryadmission.AuthoritySignalContractV1,
		SignalID:   "signal:" + seed,
		IssuerRef:  "maintainer:owner",
		Provenance: deliveryadmission.ProvenanceMaintainerControl,
		PolicyRef:  policyRef,
		Policy:     policy.Binding(),
		ExpiresAt:  fixture.now + 600,
	}
	authorityRef, err := fixture.pad.PublishAuthoritySignal(ctx, authority)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := deliveryadmission.NewIntent(
		"nonce:"+seed,
		deliveryadmission.RoutePRWithoutIssue,
		ownerTestRef(seed+"-scope"),
		deliveryadmission.DestinationBinding{
			RepositoryRef:    fixture.repositoryRef,
			TargetRef:        "refs/heads/feature",
			ObservedRevision: "git:base/1",
			DefaultBranch:    false,
		},
		policyRef,
		policy.Binding(),
		fixture.now-1,
	)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := fixture.coordinator.AdmitDeliveryIntent(
		ctx,
		OwnerDeliveryAdmissionRequest{
			Intent:       intent,
			AuthorityRef: authorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ownerAdmittedDelivery{
		Policy:       policy,
		PolicyRef:    policyRef,
		Authority:    authority,
		AuthorityRef: authorityRef,
		Intent:       intent,
		Admission:    admission,
	}
}

func ownerPublishExceptionGovernance(
	t *testing.T,
	fixture ownerCoordinatorFixture,
	decisionRef string,
) string {
	t.Helper()
	ctx := context.Background()
	decision, err := fixture.pad.ResolveDeliveryDecision(ctx, decisionRef)
	if err != nil {
		t.Fatal(err)
	}
	candidate := decision.Candidate
	risk := deliveryadmission.ResidualRisk{
		Schema:      deliveryadmission.ResidualRiskContractV1,
		RiskID:      "risk:" + fixture.workRunID,
		SubjectRef:  decisionRef,
		Route:       decision.AdmissionDecision.Route,
		Candidate:   &candidate,
		Destination: decision.Gates.Destination,
		PolicyRef:   decision.PolicyRef,
		Policy:      decision.Policy.Binding(),
		Description: "Maintainer accepted the exact unavailable verification residual risk.",
		ExpiresAt:   fixture.now + 120,
	}
	riskRef, err := fixture.pad.PublishResidualRisk(ctx, risk)
	if err != nil {
		t.Fatal(err)
	}
	governance := deliveryadmission.GovernanceDecision{
		Schema:             deliveryadmission.GovernanceDecisionContractV1,
		Kind:               deliveryadmission.GovernanceException,
		DecisionID:         "governance:" + fixture.workRunID,
		SubjectRef:         decisionRef,
		Route:              decision.AdmissionDecision.Route,
		RepositoryRef:      fixture.repositoryRef,
		Candidate:          &candidate,
		Destination:        decision.Gates.Destination,
		PolicyRef:          decision.PolicyRef,
		Policy:             decision.Policy.Binding(),
		AuthorityRef:       decision.AuthorityRef,
		SecondAuthorityRef: decision.SecondAuthorityRef,
		Reason:             "Accept the exact residual risk for this corrected delivery.",
		ResidualRiskRef:    riskRef,
		ExpiresAt:          fixture.now + 120,
	}
	governanceRef, err := fixture.pad.PublishGovernanceDecision(
		ctx,
		governance,
	)
	if err != nil {
		t.Fatal(err)
	}
	return governanceRef
}

type ownerAuthorizationRaceControl struct {
	now                   atomic.Int64
	clockReads            atomic.Int32
	normalPublications    atomic.Int32
	exceptionPublications atomic.Int32
	firstClockRead        chan struct{}
	releaseFirstClock     chan struct{}
	closeFirstClockRead   sync.Once
}

var errOwnerAuthorizationLostResponse = errors.New(
	"test lost PAD authorization publication response",
)

type ownerAuthorizationLostResponseControl struct {
	now               atomic.Int64
	publications      atomic.Int32
	failBeforePublish atomic.Bool
	failAfterPublish  atomic.Bool
}

type ownerAuthorizationLostResponseRepository struct {
	*deliveryadmission.TrustedRepository
	control *ownerAuthorizationLostResponseControl
}

func (repository *ownerAuthorizationLostResponseRepository) CurrentUnix(
	ctx context.Context,
) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return repository.control.now.Load(), nil
}

func (repository *ownerAuthorizationLostResponseRepository) PublishAuthorization(
	ctx context.Context,
	authorization deliveryadmission.DeliveryAuthorization,
) (string, error) {
	repository.control.publications.Add(1)
	if repository.control.failBeforePublish.CompareAndSwap(true, false) {
		return "", errOwnerAuthorizationLostResponse
	}
	ref, err := repository.TrustedRepository.PublishAuthorization(
		ctx,
		authorization,
	)
	if err == nil && repository.control.failAfterPublish.CompareAndSwap(
		true,
		false,
	) {
		return "", errOwnerAuthorizationLostResponse
	}
	return ref, err
}

func (repository *ownerAuthorizationLostResponseRepository) PublishExceptionAuthorization(
	ctx context.Context,
	authorization deliveryadmission.DeliveryExceptionAuthorization,
) (string, error) {
	return repository.TrustedRepository.PublishExceptionAuthorization(
		ctx,
		authorization,
	)
}

type ownerAuthorizationActivationResolver struct {
	mode            atomic.Value
	resolutions     atomic.Int32
	firstResolution chan struct{}
}

func newOwnerAuthorizationActivationResolver(
	mode ActivationMode,
) *ownerAuthorizationActivationResolver {
	resolver := &ownerAuthorizationActivationResolver{
		firstResolution: make(chan struct{}),
	}
	resolver.mode.Store(string(mode))
	return resolver
}

func (resolver *ownerAuthorizationActivationResolver) ResolveActivation(
	_ context.Context,
	_ string,
) (ActivationMode, error) {
	if resolver.resolutions.Add(1) == 1 {
		close(resolver.firstResolution)
	}
	return ActivationMode(resolver.mode.Load().(string)), nil
}

func newOwnerAuthorizationRaceControl(
	now int64,
) *ownerAuthorizationRaceControl {
	control := &ownerAuthorizationRaceControl{
		firstClockRead:    make(chan struct{}),
		releaseFirstClock: make(chan struct{}),
	}
	control.now.Store(now)
	return control
}

type ownerAuthorizationRaceRepository struct {
	*deliveryadmission.TrustedRepository
	control *ownerAuthorizationRaceControl
}

func (repository *ownerAuthorizationRaceRepository) CurrentUnix(
	ctx context.Context,
) (int64, error) {
	now := repository.control.now.Load()
	if repository.control.clockReads.Add(1) == 1 {
		repository.control.closeFirstClockRead.Do(func() {
			close(repository.control.firstClockRead)
		})
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-repository.control.releaseFirstClock:
		}
	}
	return now, nil
}

func (repository *ownerAuthorizationRaceRepository) PublishAuthorization(
	ctx context.Context,
	authorization deliveryadmission.DeliveryAuthorization,
) (string, error) {
	repository.control.normalPublications.Add(1)
	return repository.TrustedRepository.PublishAuthorization(
		ctx,
		authorization,
	)
}

func (repository *ownerAuthorizationRaceRepository) PublishExceptionAuthorization(
	ctx context.Context,
	authorization deliveryadmission.DeliveryExceptionAuthorization,
) (string, error) {
	repository.control.exceptionPublications.Add(1)
	return repository.TrustedRepository.PublishExceptionAuthorization(
		ctx,
		authorization,
	)
}
