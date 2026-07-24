package workprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type coordinationPlanPort struct {
	repository *reviewtransaction.RARAuthorityRepository
	authority  reviewtransaction.RARPlanAuthority
	stale      bool
}

func (port *coordinationPlanPort) ResolvePlan(
	_ context.Context,
	ref string,
) (reviewtransaction.RARPlanAuthority, error) {
	if port == nil || port.stale {
		return reviewtransaction.RARPlanAuthority{}, errors.New("plan is stale")
	}
	if port.authority.AuthorityRef != ref {
		return reviewtransaction.RARPlanAuthority{}, os.ErrNotExist
	}
	return port.repository.ResolvePlan(context.Background(), ref)
}

type coordinationTestPAD struct {
	intentRef string
}

func (pad coordinationTestPAD) ResolveDeliveryIntent(
	_ context.Context,
	ref string,
) (workrun.DeliveryIntentAuthority, error) {
	if ref != pad.intentRef {
		return workrun.DeliveryIntentAuthority{}, os.ErrNotExist
	}
	return workrun.DeliveryIntentAuthority{IntentRef: ref}, nil
}

func (coordinationTestPAD) ResolveLiveDeliveryAuthorization(
	context.Context,
	string,
) (workrun.DeliveryAuthorizationAuthority, error) {
	return workrun.DeliveryAuthorizationAuthority{},
		workrun.ErrDeliveryAuthorizationInactive
}

type coordinationFixture struct {
	repo        string
	plans       *coordinationPlanPort
	store       *CoordinationAuthorityStore
	handoff     workrun.ImplementationHandoff
	plan        reviewtransaction.RARPlanAuthority
	forecast    ForecastAuthorityRecord
	forecastRun workrun.VerificationForecast
	disposition DispositionAuthorityRecord
	route       RouteSelectionRecord
}

func TestCoordinationExplicitSDDRequestPrecedesRouteDecision(t *testing.T) {
	repo := initCoordinationRepository(t)
	const workRunID = "explicit-sdd-start"
	ctx := context.Background()
	coordination, err := OpenCoordinationAuthorityStore(
		ctx,
		repo,
		workRunID,
		nil,
	)
	if err != nil {
		t.Fatalf("OpenCoordinationAuthorityStore() error = %v", err)
	}
	deliveryIntentRef := coordinationTestRef("explicit-delivery")
	explicit, err := coordination.PublishExplicitSDDRequest(
		ctx,
		ExplicitSDDRequestPublication{
			WorkRunID:         workRunID,
			DeliveryIntentRef: deliveryIntentRef,
		},
	)
	if err != nil {
		t.Fatalf("PublishExplicitSDDRequest() error = %v", err)
	}
	replayed, err := coordination.PublishExplicitSDDRequest(
		ctx,
		ExplicitSDDRequestPublication{
			WorkRunID:         workRunID,
			DeliveryIntentRef: deliveryIntentRef,
		},
	)
	if err != nil {
		t.Fatalf("PublishExplicitSDDRequest(replay) error = %v", err)
	}
	if replayed.AuthorityRef != explicit.AuthorityRef {
		t.Fatalf(
			"explicit SDD replay ref = %q, want %q",
			replayed.AuthorityRef,
			explicit.AuthorityRef,
		)
	}
	if _, err := coordination.PublishExplicitSDDRequest(
		ctx,
		ExplicitSDDRequestPublication{
			WorkRunID:         workRunID,
			DeliveryIntentRef: coordinationTestRef("conflicting-delivery"),
		},
	); !errors.Is(err, ErrCoordinationAuthorityConflict) {
		t.Fatalf("PublishExplicitSDDRequest(conflict) error = %v", err)
	}

	// The owner authority exists before route calculation, so its ref can be
	// included without either digest depending on the other.
	decision, err := workrun.DecideImplementationRoute(
		workrun.ImplementationRouteInput{
			ExplicitSDDRequestRef: explicit.AuthorityRef,
		},
	)
	if err != nil {
		t.Fatalf("DecideImplementationRoute() error = %v", err)
	}
	if decision.ExplicitSDDRequestRef != explicit.AuthorityRef ||
		decision.Digest == explicit.AuthorityRef {
		t.Fatalf(
			"explicit SDD authority/decision binding = %#v, authority %q",
			decision,
			explicit.AuthorityRef,
		)
	}
	work, err := workrun.OpenWorkRunStore(ctx, repo, workRunID)
	if err != nil {
		t.Fatalf("OpenWorkRunStore() error = %v", err)
	}

	fabricatedRef := coordinationTestRef("fabricated-explicit")
	fabricatedDecision, err := workrun.DecideImplementationRoute(
		workrun.ImplementationRouteInput{
			ExplicitSDDRequestRef: fabricatedRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fabricated := work.WithAuthorityPorts(workrun.AuthorityPorts{
		PAD:                coordinationTestPAD{intentRef: deliveryIntentRef},
		ExplicitSDDRequest: coordination,
	})
	if _, err := fabricated.Start(ctx, workrun.StartRequest{
		RequestID:         "fabricated-explicit",
		RouteDecision:     fabricatedDecision,
		DeliveryIntentRef: deliveryIntentRef,
	}); err == nil {
		t.Fatal("unpublished explicit SDD request was accepted")
	}
	if _, err := work.Status(); !errors.Is(err, workrun.ErrWorkRunNotStarted) {
		t.Fatalf("failed explicit SDD start mutated WorkRun: %v", err)
	}

	otherDeliveryIntentRef := coordinationTestRef("other-delivery")
	mismatched := work.WithAuthorityPorts(workrun.AuthorityPorts{
		PAD:                coordinationTestPAD{intentRef: otherDeliveryIntentRef},
		ExplicitSDDRequest: coordination,
	})
	if _, err := mismatched.Start(ctx, workrun.StartRequest{
		RequestID:         "mismatched-explicit-intent",
		RouteDecision:     decision,
		DeliveryIntentRef: otherDeliveryIntentRef,
	}); !errors.Is(err, workrun.ErrAuthorityBindingMismatch) {
		t.Fatalf("explicit SDD delivery-intent mismatch error = %v", err)
	}
	if _, err := work.Status(); !errors.Is(err, workrun.ErrWorkRunNotStarted) {
		t.Fatalf("mismatched explicit SDD start mutated WorkRun: %v", err)
	}

	configured := work.WithAuthorityPorts(workrun.AuthorityPorts{
		PAD:                coordinationTestPAD{intentRef: deliveryIntentRef},
		ExplicitSDDRequest: coordination,
	})
	started, err := configured.Start(ctx, workrun.StartRequest{
		RequestID:         "start-explicit-sdd",
		RouteDecision:     decision,
		DeliveryIntentRef: deliveryIntentRef,
	})
	if err != nil {
		t.Fatalf("WorkRun.Start(explicit SDD) error = %v", err)
	}
	if started.ImplementationRoute != workrun.ImplementationRouteSDD ||
		started.RouteAcceptanceRef != explicit.AuthorityRef ||
		started.SDDRunRef != "" {
		t.Fatalf("explicit SDD WorkRun state = %#v", started)
	}
}

func TestCoordinationAuthorityExactReplayAndConflict(t *testing.T) {
	fixture := newCoordinationFixture(t, "coordination-replay")
	ctx := context.Background()

	replayedForecast, err := fixture.store.PublishForecast(
		ctx,
		forecastPublication(fixture, workrun.ForecastAvailable),
	)
	if err != nil {
		t.Fatalf("PublishForecast(replay) error = %v", err)
	}
	if replayedForecast.AuthorityRef != fixture.forecast.AuthorityRef {
		t.Fatalf(
			"forecast replay ref = %q, want %q",
			replayedForecast.AuthorityRef,
			fixture.forecast.AuthorityRef,
		)
	}
	conflictingForecast := forecastPublication(
		fixture,
		workrun.ForecastUnavailable,
	)
	conflictingForecast.DiagnosticRefs = []string{testRevision("a")}
	if _, err := fixture.store.PublishForecast(
		ctx,
		conflictingForecast,
	); !errors.Is(err, ErrCoordinationAuthorityConflict) {
		t.Fatalf("PublishForecast(conflict) error = %v", err)
	}

	replayedDisposition, err := fixture.store.PublishDisposition(
		ctx,
		dispositionPublication(fixture, workrun.DispositionRun),
	)
	if err != nil {
		t.Fatalf("PublishDisposition(replay) error = %v", err)
	}
	if replayedDisposition.AuthorityRef != fixture.disposition.AuthorityRef {
		t.Fatalf(
			"disposition replay ref = %q, want %q",
			replayedDisposition.AuthorityRef,
			fixture.disposition.AuthorityRef,
		)
	}
	if _, err := fixture.store.PublishDisposition(
		ctx,
		dispositionPublication(fixture, workrun.DispositionDefer),
	); !errors.Is(err, ErrCoordinationAuthorityConflict) {
		t.Fatalf("PublishDisposition(conflict) error = %v", err)
	}

	replayedRoute, err := fixture.store.PublishRouteSelection(
		ctx,
		routePublication(fixture, workrun.ImplementationRouteSDD, ""),
	)
	if err != nil {
		t.Fatalf("PublishRouteSelection(replay) error = %v", err)
	}
	if replayedRoute.AuthorityRef != fixture.route.AuthorityRef {
		t.Fatalf(
			"route replay ref = %q, want %q",
			replayedRoute.AuthorityRef,
			fixture.route.AuthorityRef,
		)
	}
	if _, err := fixture.store.PublishRouteSelection(
		ctx,
		routePublication(
			fixture,
			workrun.ImplementationRouteDirectInline,
			testRevision("b"),
		),
	); !errors.Is(err, ErrCoordinationAuthorityConflict) {
		t.Fatalf("PublishRouteSelection(conflict) error = %v", err)
	}

	resolvedForecast, err := fixture.store.ResolveForecast(
		ctx,
		fixture.forecast.AuthorityRef,
	)
	if err != nil {
		t.Fatalf("ResolveForecast() error = %v", err)
	}
	if !reflect.DeepEqual(resolvedForecast.Plan, fixture.plan.Plan) ||
		resolvedForecast.Registry.Digest != fixture.plan.Registry.Digest ||
		resolvedForecast.Applicability.Digest !=
			fixture.plan.Applicability.Digest {
		t.Fatal("ResolveForecast() did not load the exact RAR plan preimages")
	}
	resolvedDisposition, err := fixture.store.ResolveDisposition(
		ctx,
		fixture.disposition.AuthorityRef,
	)
	if err != nil {
		t.Fatalf("ResolveDisposition() error = %v", err)
	}
	if resolvedDisposition.DecisionRef != fixture.disposition.AuthorityRef ||
		resolvedDisposition.ForecastDigest != fixture.forecastRun.Digest {
		t.Fatalf("ResolveDisposition() = %#v", resolvedDisposition)
	}
	resolvedRoute, err := fixture.store.ResolveRouteSelection(
		ctx,
		fixture.route.AuthorityRef,
	)
	if err != nil {
		t.Fatalf("ResolveRouteSelection() error = %v", err)
	}
	if resolvedRoute.DecisionRef != fixture.route.AuthorityRef ||
		resolvedRoute.PendingDecisionDigest !=
			fixture.route.PendingDecisionDigest {
		t.Fatalf("ResolveRouteSelection() = %#v", resolvedRoute)
	}
}

func TestCoordinationAuthorityConcurrentPublicationConverges(t *testing.T) {
	fixture := newCoordinationFixture(t, "coordination-concurrent")
	ctx := context.Background()
	request := RouteSelectionPublication{
		WorkRunID:              fixture.store.workRunID,
		PendingDecisionDigest:  testRevision("8"),
		SelectedRoute:          workrun.ImplementationRouteDirectInline,
		SelectedDecisionDigest: testRevision("9"),
	}
	const publishers = 12
	refs := make(chan string, publishers)
	errs := make(chan error, publishers)
	var wait sync.WaitGroup
	for range publishers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			record, err := fixture.store.PublishRouteSelection(ctx, request)
			if err != nil {
				errs <- err
				return
			}
			refs <- record.AuthorityRef
		}()
	}
	wait.Wait()
	close(refs)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent exact publication error = %v", err)
	}
	var want string
	for ref := range refs {
		if want == "" {
			want = ref
		}
		if ref != want {
			t.Errorf("concurrent replay ref = %q, want %q", ref, want)
		}
	}
	if want == "" {
		t.Fatal("concurrent exact publication produced no authority")
	}

	request.PendingDecisionDigest = testRevision("a")
	alternative := request
	alternative.SelectedRoute = workrun.ImplementationRouteDelegatedDirect
	alternative.SelectedDecisionDigest = testRevision("b")
	results := make(chan error, 2)
	for _, publication := range []RouteSelectionPublication{
		request,
		alternative,
	} {
		wait.Add(1)
		go func(value RouteSelectionPublication) {
			defer wait.Done()
			_, err := fixture.store.PublishRouteSelection(ctx, value)
			results <- err
		}(publication)
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrCoordinationAuthorityConflict):
			conflicts++
		default:
			t.Errorf("concurrent conflicting publication error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf(
			"concurrent conflict results = %d success, %d conflict",
			successes,
			conflicts,
		)
	}
}

func TestCoordinationAuthorityRejectsCrossRunReplay(t *testing.T) {
	fixture := newCoordinationFixture(t, "coordination-run-a")
	explicit, err := fixture.store.PublishExplicitSDDRequest(
		context.Background(),
		ExplicitSDDRequestPublication{
			WorkRunID:         fixture.store.workRunID,
			DeliveryIntentRef: coordinationTestRef("explicit-cross-run"),
		},
	)
	if err != nil {
		t.Fatalf("PublishExplicitSDDRequest() error = %v", err)
	}
	other, err := OpenCoordinationAuthorityStore(
		context.Background(),
		fixture.repo,
		"coordination-run-b",
		fixture.plans,
	)
	if err != nil {
		t.Fatalf("OpenCoordinationAuthorityStore(other) error = %v", err)
	}
	checks := []struct {
		name string
		call func() error
	}{
		{
			name: "forecast",
			call: func() error {
				_, err := other.ResolveForecast(
					context.Background(),
					fixture.forecast.AuthorityRef,
				)
				return err
			},
		},
		{
			name: "disposition",
			call: func() error {
				_, err := other.ResolveDisposition(
					context.Background(),
					fixture.disposition.AuthorityRef,
				)
				return err
			},
		},
		{
			name: "explicit_sdd_request",
			call: func() error {
				_, err := other.ResolveExplicitSDDRequest(
					context.Background(),
					explicit.AuthorityRef,
				)
				return err
			},
		},
		{
			name: "route",
			call: func() error {
				_, err := other.ResolveRouteSelection(
					context.Background(),
					fixture.route.AuthorityRef,
				)
				return err
			},
		},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(
				err,
				ErrCoordinationAuthorityCrossRun,
			) {
				t.Fatalf("cross-run resolution error = %v", err)
			}
		})
	}

	request := forecastPublication(fixture, workrun.ForecastAvailable)
	if _, err := other.PublishForecast(
		context.Background(),
		request,
	); !errors.Is(err, ErrCoordinationAuthorityCrossRun) {
		t.Fatalf("cross-run publication error = %v", err)
	}
}

func TestCoordinationAuthorityRevalidatesLivePlan(t *testing.T) {
	fixture := newCoordinationFixture(t, "coordination-stale")
	fixture.plans.stale = true
	if _, err := fixture.store.ResolveForecast(
		context.Background(),
		fixture.forecast.AuthorityRef,
	); !errors.Is(err, ErrCoordinationAuthorityStale) {
		t.Fatalf("ResolveForecast(stale) error = %v", err)
	}
	if _, err := fixture.store.ResolveDisposition(
		context.Background(),
		fixture.disposition.AuthorityRef,
	); !errors.Is(err, ErrCoordinationAuthorityStale) {
		t.Fatalf("ResolveDisposition(stale) error = %v", err)
	}
	if _, err := fixture.store.ResolveRouteSelection(
		context.Background(),
		fixture.route.AuthorityRef,
	); err != nil {
		t.Fatalf("route selection unexpectedly depends on RAR plan: %v", err)
	}
}

func TestCoordinationAuthorityDetectsCorruption(t *testing.T) {
	t.Run("explicit_sdd_request", func(t *testing.T) {
		fixture := newCoordinationFixture(t, "coordination-corrupt-explicit")
		record, err := fixture.store.PublishExplicitSDDRequest(
			context.Background(),
			ExplicitSDDRequestPublication{
				WorkRunID:         fixture.store.workRunID,
				DeliveryIntentRef: coordinationTestRef("explicit-corruption"),
			},
		)
		if err != nil {
			t.Fatalf("PublishExplicitSDDRequest() error = %v", err)
		}
		if err := os.WriteFile(
			fixture.store.objectPath(
				coordinationKindExplicitSDD,
				record.AuthorityRef,
			),
			[]byte("{}\n"),
			0o600,
		); err != nil {
			t.Fatalf("corrupt explicit SDD request object: %v", err)
		}
		if _, err := fixture.store.ResolveExplicitSDDRequest(
			context.Background(),
			record.AuthorityRef,
		); !errors.Is(err, ErrCoordinationAuthorityCorrupt) {
			t.Fatalf("ResolveExplicitSDDRequest(corrupt) error = %v", err)
		}
	})
	t.Run("object", func(t *testing.T) {
		fixture := newCoordinationFixture(t, "coordination-corrupt-object")
		objectPath := fixture.store.objectPath(
			coordinationKindForecast,
			fixture.forecast.AuthorityRef,
		)
		if err := os.WriteFile(objectPath, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("corrupt forecast object: %v", err)
		}
		if _, err := fixture.store.ResolveForecast(
			context.Background(),
			fixture.forecast.AuthorityRef,
		); !errors.Is(err, ErrCoordinationAuthorityCorrupt) {
			t.Fatalf("ResolveForecast(corrupt) error = %v", err)
		}
	})
	t.Run("unpublished_orphan", func(t *testing.T) {
		fixture := newCoordinationFixture(t, "coordination-corrupt-slot")
		slotRef, err := forecastAuthoritySlotRef(fixture.forecast)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(
			fixture.store.slotPath(coordinationKindForecast, slotRef),
		); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.ResolveForecast(
			context.Background(),
			fixture.forecast.AuthorityRef,
		); !errors.Is(err, ErrCoordinationAuthorityCorrupt) {
			t.Fatalf("ResolveForecast(orphan) error = %v", err)
		}
	})
}

func TestCoordinationExplicitSDDResolveDoesNotCreateStorage(t *testing.T) {
	repo := initCoordinationRepository(t)
	store, err := OpenCoordinationAuthorityStore(
		context.Background(),
		repo,
		"explicit-read-only",
		nil,
	)
	if err != nil {
		t.Fatalf("OpenCoordinationAuthorityStore() error = %v", err)
	}
	if _, err := store.ResolveExplicitSDDRequest(
		context.Background(),
		coordinationTestRef("missing-explicit"),
	); err == nil {
		t.Fatal("missing explicit SDD authority resolved")
	}
	if _, err := os.Stat(store.root); !os.IsNotExist(err) {
		t.Fatalf("read-only explicit SDD resolve created storage: %v", err)
	}
}

func TestCoordinationAuthorityUsesPrivateStableGitPath(t *testing.T) {
	fixture := newCoordinationFixture(t, "coordination-secure")
	if runtime.GOOS != "windows" {
		assertCoordinationMode(t, fixture.store.root, 0o700)
		assertCoordinationMode(
			t,
			fixture.store.objectPath(
				coordinationKindForecast,
				fixture.forecast.AuthorityRef,
			),
			0o600,
		)
		slotRef, err := forecastAuthoritySlotRef(fixture.forecast)
		if err != nil {
			t.Fatal(err)
		}
		assertCoordinationMode(
			t,
			fixture.store.slotPath(coordinationKindForecast, slotRef),
			0o600,
		)
	}

	hostile := filepath.Join(t.TempDir(), "hostile-git")
	if err := os.MkdirAll(hostile, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", hostile)
	reopened, err := OpenCoordinationAuthorityStore(
		context.Background(),
		fixture.repo,
		fixture.store.workRunID,
		fixture.plans,
	)
	if err != nil {
		t.Fatalf("OpenCoordinationAuthorityStore(hostile env) error = %v", err)
	}
	if reopened.identity.repositoryIdentity !=
		fixture.store.identity.repositoryIdentity {
		t.Fatal("ambient GIT_DIR changed coordination repository identity")
	}

	attacker := filepath.Join(t.TempDir(), "attacker")
	if err := os.Mkdir(attacker, 0o700); err != nil {
		t.Fatal(err)
	}
	objects := filepath.Join(fixture.store.root, "objects")
	if err := os.RemoveAll(objects); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(attacker, objects); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable on Windows: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := fixture.store.PublishForecast(
		context.Background(),
		ForecastAuthorityPublication{
			WorkRunID:       fixture.store.workRunID,
			Handoff:         fixture.handoff,
			PlanRevisionRef: fixture.plan.AuthorityRef,
			Availability:    workrun.ForecastAvailable,
			DiagnosticRefs:  []string{},
		},
	); err == nil {
		t.Fatal("PublishForecast() followed a substituted objects symlink")
	}
	entries, err := os.ReadDir(attacker)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("attacker directory received files: %v", entries)
	}
}

func newCoordinationFixture(
	t *testing.T,
	workRunID string,
) coordinationFixture {
	t.Helper()
	repo := initCoordinationRepository(t)
	ctx := context.Background()
	snapshot, err := (reviewtransaction.SnapshotBuilder{
		Repo: repo,
	}).Build(ctx, reviewtransaction.Target{
		Kind:              reviewtransaction.TargetCurrentChanges,
		IntendedUntracked: []string{},
	})
	if err != nil {
		t.Fatalf("Build(snapshot) error = %v", err)
	}
	subject, err := reviewtransaction.VerificationSubjectFromSnapshot(snapshot)
	if err != nil {
		t.Fatalf("VerificationSubjectFromSnapshot() error = %v", err)
	}
	policyHash := testRevision("1")
	obligation := reviewtransaction.VerificationObligation{
		ID:             "unit",
		RequirementRef: testRevision("2"),
		ArgvRef:        testRevision("3"),
		CapabilityRef:  "host.exec",
		Cost:           reviewtransaction.VerificationCostQuick,
	}
	registry, err := reviewtransaction.NewVerificationPlanRegistry(
		policyHash,
		[]string{},
		[]reviewtransaction.VerificationObligation{obligation},
	)
	if err != nil {
		t.Fatalf("NewVerificationPlanRegistry() error = %v", err)
	}
	applicability, err := (reviewtransaction.SnapshotBuilder{
		Repo: repo,
	}).ClassifyVerificationApplicability(
		ctx,
		snapshot,
		registry,
		[]string{},
	)
	if err != nil {
		t.Fatalf("ClassifyVerificationApplicability() error = %v", err)
	}
	if applicability.Decision != reviewtransaction.VerificationApplicable {
		t.Fatalf(
			"fixture applicability = %q, want applicable",
			applicability.Decision,
		)
	}
	plan, err := reviewtransaction.BuildVerificationPlan(
		applicability,
		registry,
	)
	if err != nil {
		t.Fatalf("BuildVerificationPlan() error = %v", err)
	}
	identity, err := resolveCoordinationRepositoryIdentity(ctx, repo)
	if err != nil {
		t.Fatalf("resolveCoordinationRepositoryIdentity() error = %v", err)
	}
	rar, err := reviewtransaction.OpenRARAuthorityRepository(ctx, repo)
	if err != nil {
		t.Fatalf("OpenRARAuthorityRepository() error = %v", err)
	}
	planAuthority, err := rar.PublishPlan(
		ctx,
		reviewtransaction.RARPlanPublication{
			Snapshot:      snapshot,
			Applicability: applicability,
			Registry:      registry,
			Plan:          plan,
		},
	)
	if err != nil {
		t.Fatalf("PublishPlan() error = %v", err)
	}
	if planAuthority.RepositoryIdentity != identity.repositoryIdentity {
		t.Fatalf(
			"RAR repository identity = %q, coordination identity = %q",
			planAuthority.RepositoryIdentity,
			identity.repositoryIdentity,
		)
	}
	plans := &coordinationPlanPort{
		repository: rar,
		authority:  planAuthority,
	}
	store, err := OpenCoordinationAuthorityStore(
		ctx,
		repo,
		workRunID,
		plans,
	)
	if err != nil {
		t.Fatalf("OpenCoordinationAuthorityStore() error = %v", err)
	}
	handoff, err := workrun.NewImplementationHandoff(
		workrun.ImplementationRouteDirectInline,
		testRevision("4"),
		subject,
		[]string{obligation.RequirementRef},
		[]string{},
		"",
	)
	if err != nil {
		t.Fatalf("NewImplementationHandoff() error = %v", err)
	}
	fixture := coordinationFixture{
		repo:    repo,
		plans:   plans,
		store:   store,
		handoff: handoff,
		plan:    planAuthority,
	}
	fixture.forecast, err = store.PublishForecast(
		ctx,
		forecastPublication(fixture, workrun.ForecastAvailable),
	)
	if err != nil {
		t.Fatalf("PublishForecast() error = %v", err)
	}
	fixture.forecastRun = testVerificationForecast(
		t,
		workRunID,
		handoff,
		planAuthority,
		fixture.forecast,
	)
	fixture.disposition, err = store.PublishDisposition(
		ctx,
		dispositionPublication(fixture, workrun.DispositionRun),
	)
	if err != nil {
		t.Fatalf("PublishDisposition() error = %v", err)
	}
	fixture.route, err = store.PublishRouteSelection(
		ctx,
		routePublication(fixture, workrun.ImplementationRouteSDD, ""),
	)
	if err != nil {
		t.Fatalf("PublishRouteSelection() error = %v", err)
	}
	return fixture
}

func forecastPublication(
	fixture coordinationFixture,
	availability workrun.ForecastAvailability,
) ForecastAuthorityPublication {
	return ForecastAuthorityPublication{
		WorkRunID:       fixture.store.workRunID,
		Handoff:         fixture.handoff,
		PlanRevisionRef: fixture.plan.AuthorityRef,
		Availability:    availability,
		DiagnosticRefs:  []string{},
	}
}

func dispositionPublication(
	fixture coordinationFixture,
	kind workrun.VerificationDispositionKind,
) DispositionAuthorityPublication {
	return DispositionAuthorityPublication{
		WorkRunID:      fixture.store.workRunID,
		Handoff:        fixture.handoff,
		Forecast:       fixture.forecastRun,
		Kind:           kind,
		AssumptionsRef: testRevision("5"),
		ActorRef:       testRevision("6"),
	}
}

func routePublication(
	fixture coordinationFixture,
	route workrun.ImplementationRoute,
	selectedDigest string,
) RouteSelectionPublication {
	return RouteSelectionPublication{
		WorkRunID:              fixture.store.workRunID,
		PendingDecisionDigest:  testRevision("7"),
		SelectedRoute:          route,
		SelectedDecisionDigest: selectedDigest,
	}
}

func testVerificationForecast(
	t *testing.T,
	workRunID string,
	handoff workrun.ImplementationHandoff,
	authority reviewtransaction.RARPlanAuthority,
	record ForecastAuthorityRecord,
) workrun.VerificationForecast {
	t.Helper()
	cost := reviewtransaction.VerificationCostQuick
	forecast := workrun.VerificationForecast{
		Schema:              workrun.VerificationForecastSchemaV1,
		WorkRunID:           workRunID,
		HandoffDigest:       handoff.Digest,
		SubjectRef:          authority.Applicability.Digest,
		CandidateRef:        handoff.CandidateRef,
		ApplicabilityDigest: authority.Applicability.Digest,
		RegistryDigest:      authority.Registry.Digest,
		PlanDigest:          authority.Plan.Digest,
		PlanRevisionRef:     authority.AuthorityRef,
		PlanPreimageRef:     authority.Plan.Digest,
		MaximumCost:         &cost,
		Availability:        record.Availability,
		AvailabilityRef:     record.AuthorityRef,
		CapabilityRefs:      []string{"host.exec"},
		DiagnosticRefs:      []string{},
	}
	forecast.Digest = testDomainDigest(
		t,
		"gentle-ai.verification-forecast-digest/v1",
		forecast,
		func(value any) {
			value.(*workrun.VerificationForecast).Digest = ""
		},
	)
	if err := forecast.Validate(); err != nil {
		t.Fatalf("VerificationForecast.Validate() error = %v", err)
	}
	return forecast
}

func testDomainDigest[T any](
	t *testing.T,
	domain string,
	value T,
	clear func(any),
) string {
	t.Helper()
	copyValue := value
	clear(&copyValue)
	payload, err := json.Marshal(copyValue)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func coordinationTestRef(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func initCoordinationRepository(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	runCoordinationGit(t, "", "init", "-q", repo)
	runCoordinationGit(t, repo, "config", "user.name", "Coordination Test")
	runCoordinationGit(
		t,
		repo,
		"config",
		"user.email",
		"coordination@example.test",
	)
	path := filepath.Join(repo, "internal", "app.go")
	if err := os.WriteFile(
		path,
		[]byte("package internal\n\nconst value = 1\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	runCoordinationGit(t, repo, "add", "internal/app.go")
	runCoordinationGit(t, repo, "commit", "-q", "-m", "baseline")
	if err := os.WriteFile(
		path,
		[]byte("package internal\n\nconst value = 2\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	return repo
}

func runCoordinationGit(
	t *testing.T,
	repo string,
	args ...string,
) {
	t.Helper()
	commandArgs := make([]string, 0, len(args)+2)
	if repo != "" {
		commandArgs = append(commandArgs, "-C", repo)
	}
	commandArgs = append(commandArgs, args...)
	command := exec.Command("git", commandArgs...)
	command.Env = append(
		os.Environ(),
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func assertCoordinationMode(
	t *testing.T,
	path string,
	want os.FileMode,
) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %#o, want %#o", path, got, want)
	}
}
