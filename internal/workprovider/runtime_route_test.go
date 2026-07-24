package workprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/sddstatus"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type productiveRouteTestFixture struct {
	repo      string
	workRunID string
	runtime   *productiveRuntimeOutcome
	start     workrun.WorkStatusV1
}

func newProductiveRouteTestFixture(
	t *testing.T,
	workRunID string,
	explicitSDD bool,
) productiveRouteTestFixture {
	t.Helper()
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	authority, err := NewPADRepositoryAuthority(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	const sessionRef = "session:productive-route-test"
	snapshot, err := NewProductivePolicySnapshot(
		authority.RepositoryRef(),
		model.AgentCodex,
		sessionRef,
		1,
		runtimePolicyTestPolicies(t, 1, false),
	)
	if err != nil {
		t.Fatal(err)
	}
	intake := ownerOutcomeTestIntake(
		workRunID,
		deliveryadmission.RoutePRWithoutIssue,
	)
	if !explicitSDD {
		intake.RoutingFacts = workrun.ImplementationRouteInput{
			WriteIntent:    workrun.WriteIntentAnalytical,
			WriteFileCount: 2,
			DurablePlanning: []workrun.DurablePlanningReason{
				workrun.PlanningArchitecture,
			},
		}
		intake.DeclinedSDDFallback = &workrun.ImplementationRouteInput{
			WriteIntent:    workrun.WriteIntentAnalytical,
			WriteFileCount: 2,
		}
	}
	connector := &runtimeDefaultConnector{
		repositoryRef: authority.RepositoryRef(),
		agent:         model.AgentCodex,
		sessionRef:    sessionRef,
		snapshot:      snapshot,
		intake:        intake,
	}
	runtime := &productiveRuntimeOutcome{
		repo:          repo,
		repositoryRef: authority.RepositoryRef(),
		agent:         model.AgentCodex,
		activation: StaticActivationResolver{
			Mode: ActivationEnabled,
		},
		connector: connector,
	}
	start, err := runtime.StartOutcome(ctx, OutcomeStartRequest{
		Outcome:              "Implement the bounded route recovery change.",
		ExplicitSDDRequested: explicitSDD,
	})
	if err != nil {
		t.Fatal(err)
	}
	return productiveRouteTestFixture{
		repo: repo, workRunID: workRunID, runtime: runtime, start: start,
	}
}

func TestProductiveRuntimeAcceptsAndBindsOptionalSDDWithExactReplay(
	t *testing.T,
) {
	fixture := newProductiveRouteTestFixture(
		t,
		"productive-route-accept",
		false,
	)
	if fixture.start.RoutePhase != workrun.RoutePhaseDecisionPending ||
		fixture.start.PublicState != workrun.PublicStateNeedsYourDecision {
		t.Fatalf("optional proposal start = %#v", fixture.start)
	}
	ctx := context.Background()
	accepted, err := fixture.runtime.DecideRoute(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
		workrun.WorkRouteChoiceAcceptSDD,
	)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status.RoutePhase != workrun.RoutePhaseSDDRuntimePending ||
		accepted.Status.PublicState != workrun.PublicStateWorking ||
		accepted.SelectedRoute != workrun.ImplementationRouteSDD {
		t.Fatalf("accepted route = %#v", accepted)
	}

	const runRef = "organic-recovery-route"
	if err := os.MkdirAll(
		filepath.Join(fixture.repo, "openspec", "changes", runRef),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	bound, err := fixture.runtime.BindSDDRoute(
		ctx,
		fixture.workRunID,
		accepted.Status.Revision,
		runRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Status.RoutePhase !=
		workrun.RoutePhaseImplementationSelected ||
		bound.Status.ImplementationRoute != workrun.ImplementationRouteSDD ||
		bound.Status.SDDRunRef != runRef {
		t.Fatalf("bound SDD route = %#v", bound)
	}

	// A lost accept response is replayed from WorkRun's historical receipt,
	// not projected from the later bound state.
	replayedAccept, err := fixture.runtime.DecideRoute(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
		workrun.WorkRouteChoiceAcceptSDD,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayedAccept, accepted) {
		t.Fatalf(
			"accepted historical replay = %#v, want %#v",
			replayedAccept,
			accepted,
		)
	}
	replayedBind, err := fixture.runtime.BindSDDRoute(
		ctx,
		fixture.workRunID,
		accepted.Status.Revision,
		runRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayedBind, bound) {
		t.Fatalf("bound historical replay = %#v, want %#v", replayedBind, bound)
	}
}

func TestProductiveRuntimeDeclineUsesOnlyPersistedOwnerFallback(t *testing.T) {
	fixture := newProductiveRouteTestFixture(
		t,
		"productive-route-decline",
		false,
	)
	declined, err := fixture.runtime.DecideRoute(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
		workrun.WorkRouteChoiceDeclineSDD,
	)
	if err != nil {
		t.Fatal(err)
	}
	if declined.SelectedRoute != workrun.ImplementationRouteDelegatedDirect ||
		declined.Status.RouteDecision !=
			workrun.RouteDecisionDelegatedDirect ||
		declined.Status.RoutePhase !=
			workrun.RoutePhaseImplementationSelected ||
		declined.Status.ImplementationRoute !=
			workrun.ImplementationRouteDelegatedDirect {
		t.Fatalf("declined owner fallback = %#v", declined)
	}
	replayed, err := fixture.runtime.DecideRoute(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
		workrun.WorkRouteChoiceDeclineSDD,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, declined) {
		t.Fatalf("decline replay = %#v, want %#v", replayed, declined)
	}
}

func TestProductiveRuntimeExplicitSDDStartsAcceptedAndRejectsDecide(
	t *testing.T,
) {
	fixture := newProductiveRouteTestFixture(
		t,
		"productive-route-explicit",
		true,
	)
	if fixture.start.RoutePhase != workrun.RoutePhaseSDDRuntimePending ||
		fixture.start.PublicState != workrun.PublicStateWorking {
		t.Fatalf("explicit SDD start = %#v", fixture.start)
	}
	ctx := context.Background()
	_, err := fixture.runtime.DecideRoute(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
		workrun.WorkRouteChoiceAcceptSDD,
	)
	if err == nil || !errors.Is(err, workrun.ErrWorkRunInvalidTransition) {
		t.Fatalf("explicit SDD duplicate decision error = %v", err)
	}

	const runRef = "explicit-sdd-runtime"
	if err := os.MkdirAll(
		filepath.Join(fixture.repo, "openspec", "changes", runRef),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	bound, err := fixture.runtime.BindSDDRoute(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
		runRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Status.RoutePhase != workrun.RoutePhaseImplementationSelected ||
		bound.Status.ImplementationRoute != workrun.ImplementationRouteSDD ||
		bound.Status.SDDRunRef != runRef {
		t.Fatalf("explicit SDD binding = %#v", bound)
	}
}

func TestProductiveRuntimeSDDBindIntentRecoversOnlyPinnedRun(t *testing.T) {
	fixture := newProductiveRouteTestFixture(
		t,
		"productive-route-bind-intent",
		false,
	)
	ctx := context.Background()
	accepted, err := fixture.runtime.DecideRoute(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
		workrun.WorkRouteChoiceAcceptSDD,
	)
	if err != nil {
		t.Fatal(err)
	}
	const (
		pinnedRun = "pinned-sdd-runtime"
		otherRun  = "other-sdd-runtime"
	)
	for _, runRef := range []string{pinnedRun, otherRun} {
		if err := os.MkdirAll(
			filepath.Join(fixture.repo, "openspec", "changes", runRef),
			0o755,
		); err != nil {
			t.Fatal(err)
		}
	}
	authority, err := fixture.runtime.resolveProductiveRouteAuthority(
		ctx,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := authority.factory.Open(ctx, fixture.workRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.coordination.PublishSDDBindingIntent(
		ctx,
		SDDBindingIntentPublication{
			WorkRunID:          fixture.workRunID,
			ExpectedRevision:   accepted.Status.Revision,
			RouteAcceptanceRef: authority.state.RouteAcceptanceRef,
			RunRef:             pinnedRun,
		},
	); err != nil {
		t.Fatal(err)
	}

	_, err = fixture.runtime.BindSDDRoute(
		ctx,
		fixture.workRunID,
		accepted.Status.Revision,
		otherRun,
	)
	if !errors.Is(err, ErrCoordinationAuthorityConflict) {
		t.Fatalf("different run after durable intent error = %v", err)
	}
	otherStore, err := sddstatus.OpenRuntimeStore(
		ctx,
		fixture.repo,
		otherRun,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherStore.ResolveWorkRunBinding(ctx); !errors.Is(
		err,
		sddstatus.ErrSDDWorkRunBindingNotFound,
	) {
		t.Fatalf("rejected run binding error = %v", err)
	}

	bound, err := fixture.runtime.BindSDDRoute(
		ctx,
		fixture.workRunID,
		accepted.Status.Revision,
		pinnedRun,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Status.SDDRunRef != pinnedRun {
		t.Fatalf("recovered pinned SDD binding = %#v", bound)
	}
}

func TestProductiveRuntimeConcurrentSDDBindsLeaveNoLosingLedger(
	t *testing.T,
) {
	fixture := newProductiveRouteTestFixture(
		t,
		"productive-route-bind-race",
		false,
	)
	ctx := context.Background()
	accepted, err := fixture.runtime.DecideRoute(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
		workrun.WorkRouteChoiceAcceptSDD,
	)
	if err != nil {
		t.Fatal(err)
	}
	runRefs := []string{"concurrent-sdd-a", "concurrent-sdd-b"}
	for _, runRef := range runRefs {
		if err := os.MkdirAll(
			filepath.Join(fixture.repo, "openspec", "changes", runRef),
			0o755,
		); err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		runRef string
		route  workrun.WorkRouteV1
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, len(runRefs))
	var ready sync.WaitGroup
	ready.Add(len(runRefs))
	for _, runRef := range runRefs {
		runRef := runRef
		go func() {
			ready.Done()
			<-start
			route, callErr := fixture.runtime.BindSDDRoute(
				ctx,
				fixture.workRunID,
				accepted.Status.Revision,
				runRef,
			)
			results <- result{runRef: runRef, route: route, err: callErr}
		}()
	}
	ready.Wait()
	close(start)

	var winner string
	for range runRefs {
		result := <-results
		if result.err == nil {
			if winner != "" {
				t.Fatalf(
					"both concurrent SDD bindings succeeded: %s and %s",
					winner,
					result.runRef,
				)
			}
			winner = result.runRef
			if result.route.Status.SDDRunRef != result.runRef {
				t.Fatalf("winning route = %#v", result.route)
			}
		}
	}
	if winner == "" {
		t.Fatal("neither concurrent SDD binding succeeded")
	}
	for _, runRef := range runRefs {
		store, err := sddstatus.OpenRuntimeStore(ctx, fixture.repo, runRef)
		if err != nil {
			t.Fatal(err)
		}
		binding, err := store.ResolveWorkRunBinding(ctx)
		if runRef == winner {
			if err != nil || binding.RunRef != winner ||
				binding.WorkRunID != fixture.workRunID {
				t.Fatalf("winning SDD ledger = %#v error=%v", binding, err)
			}
			continue
		}
		if !errors.Is(err, sddstatus.ErrSDDWorkRunBindingNotFound) {
			t.Fatalf("losing SDD ledger binding error = %v", err)
		}
	}
}

func TestProductiveRuntimeInvalidSDDRunDoesNotPoisonBindingIntent(
	t *testing.T,
) {
	fixture := newProductiveRouteTestFixture(
		t,
		"productive-route-invalid-bind",
		false,
	)
	ctx := context.Background()
	accepted, err := fixture.runtime.DecideRoute(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
		workrun.WorkRouteChoiceAcceptSDD,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.runtime.BindSDDRoute(
		ctx,
		fixture.workRunID,
		accepted.Status.Revision,
		"missing-sdd-runtime",
	)
	if !errors.Is(err, sddstatus.ErrSDDRunAuthorityNotFound) {
		t.Fatalf("missing SDD run error = %v", err)
	}

	const validRun = "valid-after-missing-sdd"
	if err := os.MkdirAll(
		filepath.Join(fixture.repo, "openspec", "changes", validRun),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	bound, err := fixture.runtime.BindSDDRoute(
		ctx,
		fixture.workRunID,
		accepted.Status.Revision,
		validRun,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Status.SDDRunRef != validRun {
		t.Fatalf("binding after missing run = %#v", bound)
	}
}

func TestProductiveRuntimeForeignSDDBindingDoesNotPoisonIntent(
	t *testing.T,
) {
	fixture := newProductiveRouteTestFixture(
		t,
		"productive-route-foreign-bind",
		false,
	)
	ctx := context.Background()
	accepted, err := fixture.runtime.DecideRoute(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
		workrun.WorkRouteChoiceAcceptSDD,
	)
	if err != nil {
		t.Fatal(err)
	}
	const foreignRun = "foreign-bound-sdd"
	if err := os.MkdirAll(
		filepath.Join(fixture.repo, "openspec", "changes", foreignRun),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	foreignStore, err := sddstatus.OpenRuntimeStore(
		ctx,
		fixture.repo,
		foreignRun,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreignStore.BindWorkRun(
		ctx,
		"other-work-run",
		testPADRef("foreign-route-acceptance"),
	); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.runtime.BindSDDRoute(
		ctx,
		fixture.workRunID,
		accepted.Status.Revision,
		foreignRun,
	)
	if !errors.Is(err, sddstatus.ErrSDDWorkRunBindingConflict) {
		t.Fatalf("foreign-bound SDD error = %v", err)
	}

	const validRun = "valid-after-foreign-sdd"
	if err := os.MkdirAll(
		filepath.Join(fixture.repo, "openspec", "changes", validRun),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	bound, err := fixture.runtime.BindSDDRoute(
		ctx,
		fixture.workRunID,
		accepted.Status.Revision,
		validRun,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Status.SDDRunRef != validRun {
		t.Fatalf("binding after foreign run = %#v", bound)
	}
}

func TestProductiveRouteUnknownWorkRunCreatesNoRunAuthority(t *testing.T) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	authority, err := NewPADRepositoryAuthority(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	const sessionRef = "session:unknown-productive-route"
	snapshot, err := NewProductivePolicySnapshot(
		authority.RepositoryRef(),
		model.AgentCodex,
		sessionRef,
		1,
		runtimePolicyTestPolicies(t, 1, false),
	)
	if err != nil {
		t.Fatal(err)
	}
	connector := &runtimeDefaultConnector{
		repositoryRef: authority.RepositoryRef(),
		agent:         model.AgentCodex,
		sessionRef:    sessionRef,
		snapshot:      snapshot,
		intake: ownerOutcomeTestIntake(
			"unused-route-intake",
			deliveryadmission.RoutePRWithoutIssue,
		),
	}
	runtime := &productiveRuntimeOutcome{
		repo:          repo,
		repositoryRef: authority.RepositoryRef(),
		agent:         model.AgentCodex,
		activation: StaticActivationResolver{
			Mode: ActivationEnabled,
		},
		connector: connector,
	}
	root := defaultProviderRoot(t, repo)
	if _, statErr := os.Lstat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("fresh runtime already has provider authority: %v", statErr)
	}
	unknown := "productive-route-unknown"
	_, err = runtime.DecideRoute(
		ctx,
		unknown,
		testPADRef("unknown-route-expected"),
		workrun.WorkRouteChoiceAcceptSDD,
	)
	if !errors.Is(err, workrun.ErrWorkRunNotStarted) {
		t.Fatalf("unknown WorkRun route error = %v", err)
	}
	if _, statErr := os.Lstat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unknown WorkRun route created provider authority: %v", statErr)
	}
}

func TestProductiveRouteRejectsMalformedAuthorityBeforeStorage(t *testing.T) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	authority, err := NewPADRepositoryAuthority(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	activation := &countingActivationResolver{mode: ActivationEnabled}
	runtime := &productiveRuntimeOutcome{
		repo:          repo,
		repositoryRef: authority.RepositoryRef(),
		agent:         model.AgentCodex,
		activation:    activation,
	}
	root := defaultProviderRoot(t, repo)
	if _, statErr := os.Lstat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("fresh runtime already has provider authority: %v", statErr)
	}

	attempts := []struct {
		name string
		call func() error
	}{
		{
			name: "decide malformed revision",
			call: func() error {
				_, callErr := runtime.DecideRoute(
					ctx,
					"productive-route-malformed",
					"not-a-revision",
					workrun.WorkRouteChoiceAcceptSDD,
				)
				return callErr
			},
		},
		{
			name: "bind malformed revision",
			call: func() error {
				_, callErr := runtime.BindSDDRoute(
					ctx,
					"productive-route-malformed",
					"not-a-revision",
					"existing-sdd-run",
				)
				return callErr
			},
		},
	}
	for _, attempt := range attempts {
		t.Run(attempt.name, func(t *testing.T) {
			if err := attempt.call(); err == nil {
				t.Fatal("malformed route authority unexpectedly succeeded")
			}
			if _, statErr := os.Lstat(root); !errors.Is(
				statErr,
				os.ErrNotExist,
			) {
				t.Fatalf(
					"malformed route authority created provider storage: %v",
					statErr,
				)
			}
			if activation.calls != 0 {
				t.Fatalf(
					"malformed route authority resolved activation %d times",
					activation.calls,
				)
			}
		})
	}
}
