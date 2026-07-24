package workprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/mutationintegrity"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/sddstatus"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type ownerTestClock struct {
	now int64
}

func (clock ownerTestClock) Now() time.Time {
	return time.Unix(clock.now, 0).UTC()
}

type ownerTestActivationResolver struct {
	mode  ActivationMode
	err   error
	repos []string
}

func (resolver *ownerTestActivationResolver) ResolveActivation(
	_ context.Context,
	repo string,
) (ActivationMode, error) {
	resolver.repos = append(resolver.repos, repo)
	if resolver.err != nil {
		return ActivationDisabled, resolver.err
	}
	return resolver.mode, nil
}

type ownerTestLaunchAuthority struct{}

func (ownerTestLaunchAuthority) ActivateLaunch(
	_ context.Context,
	_ hostruntime.LaunchBinding,
	_ hostruntime.LaunchClaim,
) (*hostruntime.LaunchCapability, error) {
	return nil, errors.New("owner coordinator route tests do not launch HCR")
}

type ownerCoordinatorFixture struct {
	repo          string
	commonDir     string
	repositoryRef string
	now           int64
	workRunID     string

	coordinator *OwnerCoordinator
	pad         *deliveryadmission.TrustedRepository
}

func newOwnerCoordinatorFixture(
	t *testing.T,
	workRunID string,
) ownerCoordinatorFixture {
	t.Helper()
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	ownerGit(t, repo, "init", "--quiet")
	ownerGit(t, repo, "config", "user.name", "Owner Coordinator Test")
	ownerGit(t, repo, "config", "user.email", "owner-coordinator@example.test")
	if err := os.WriteFile(
		filepath.Join(repo, "README.md"),
		[]byte("owner coordinator\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	ownerGit(t, repo, "add", "README.md")
	ownerGit(t, repo, "commit", "--quiet", "-m", "fixture")
	absolute, err := filepath.Abs(repo)
	if err != nil {
		t.Fatal(err)
	}
	repo = filepath.Clean(absolute)
	commonDir := strings.TrimSpace(ownerGit(t, repo, "rev-parse", "--git-common-dir"))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repo, commonDir)
	}
	commonDir, err = filepath.Abs(commonDir)
	if err != nil {
		t.Fatal(err)
	}

	rar, err := reviewtransaction.OpenRARAuthorityRepository(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	coordination, err := OpenCoordinationAuthorityStore(
		ctx,
		repo,
		workRunID,
		rar,
	)
	if err != nil {
		t.Fatal(err)
	}
	transitions, err := OpenTransitionAuthorityStore(ctx, repo, workRunID)
	if err != nil {
		t.Fatal(err)
	}
	evidenceStore, err := evidence.OpenStore(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	mutationStore, err := mutationintegrity.OpenStore(
		ctx,
		coordination.identity.lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	workStore, err := workrun.OpenWorkRunStore(ctx, repo, workRunID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Unix()
	padRepositoryAuthority, err := NewPADRepositoryAuthority(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	repositoryRef := padRepositoryAuthority.RepositoryRef()
	pad, err := deliveryadmission.OpenTrustedRepository(
		ctx,
		padRepositoryAuthority,
		repositoryRef,
		deliveryadmission.WithTrustedRepositoryClock(ownerTestClock{now: now}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pad.Close(); err != nil {
			t.Errorf("close PAD store: %v", err)
		}
	})
	padAuthority, err := NewPADWorkRunAdapter(padRepositoryAuthority)
	if err != nil {
		t.Fatal(err)
	}
	padAuthority.open = func(
		ctx context.Context,
		authority *PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		return deliveryadmission.OpenTrustedRepository(
			ctx,
			authority,
			authority.RepositoryRef(),
			deliveryadmission.WithTrustedRepositoryClock(
				ownerTestClock{now: now},
			),
		)
	}
	coordinator, err := NewOwnerCoordinator(ctx, OwnerCoordinatorDependencies{
		WorkRun: workStore, Coordination: coordination,
		RAR: rar, Transitions: transitions, Evidence: evidenceStore,
		Mutations:       mutationStore,
		PADAuthority:    padAuthority,
		SDDAuthority:    SDDWorkRunAuthority{Repo: repo},
		LaunchAuthority: ownerTestLaunchAuthority{},
		Activation:      StaticActivationResolver{Mode: ActivationEnabled},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ownerCoordinatorFixture{
		repo: repo, commonDir: filepath.Clean(commonDir),
		repositoryRef: repositoryRef, now: now, workRunID: workRunID,
		coordinator: coordinator, pad: pad,
	}
}

func (fixture ownerCoordinatorFixture) admitDefaultIntent(
	t *testing.T,
	seed string,
) string {
	t.Helper()
	return fixture.admitDefaultDelivery(t, seed).Admission.IntentRef
}

type ownerAdmittedDelivery struct {
	Policy       deliveryadmission.RoutePolicy
	PolicyRef    string
	Authority    deliveryadmission.AuthoritySignal
	AuthorityRef string
	Intent       deliveryadmission.DeliveryIntent
	Admission    OwnerDeliveryAdmission
}

func (fixture ownerCoordinatorFixture) admitDefaultDelivery(
	t *testing.T,
	seed string,
) ownerAdmittedDelivery {
	t.Helper()
	ctx := context.Background()
	policy, err := deliveryadmission.NewRoutePolicy(
		"policy:"+fixture.workRunID,
		"revision:1",
		deliveryadmission.RoutePRWithoutIssue,
		true,
		false,
		false,
		120,
		0,
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
		SignalID:   "signal:" + fixture.workRunID,
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
		ownerTestRef("scope-"+seed),
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
	admitted, err := fixture.coordinator.AdmitDeliveryIntent(
		ctx,
		OwnerDeliveryAdmissionRequest{
			Intent:       intent,
			AuthorityRef: authorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.IntentRef == "" {
		t.Fatalf("default intent admission = %#v", admitted)
	}
	if _, err := fixture.pad.ResolveAdmittedIntent(
		ctx,
		admitted.IntentRef,
	); err != nil {
		t.Fatalf("resolve default admitted intent: %v", err)
	}
	return ownerAdmittedDelivery{
		Policy:       policy,
		PolicyRef:    policyRef,
		Authority:    authority,
		AuthorityRef: authorityRef,
		Intent:       intent,
		Admission:    admitted,
	}
}

func TestOwnerCoordinatorStartsDirectAndDelegatedWithoutSDD(t *testing.T) {
	tests := []struct {
		name     string
		input    workrun.ImplementationRouteInput
		decision workrun.RouteDecision
		route    workrun.ImplementationRoute
	}{
		{
			name: "direct",
			input: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
			decision: workrun.RouteDecisionDirectInline,
			route:    workrun.ImplementationRouteDirectInline,
		},
		{
			name: "delegated",
			input: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAnalytical,
				WriteFileCount: 2,
			},
			decision: workrun.RouteDecisionDelegatedDirect,
			route:    workrun.ImplementationRouteDelegatedDirect,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOwnerCoordinatorFixture(
				t,
				"route-"+test.name,
			)
			intentRef := fixture.admitDefaultIntent(t, "intent-"+test.name)
			state, err := fixture.coordinator.StartWork(
				context.Background(),
				OwnerStartWorkRequest{
					DeliveryIntentRef: intentRef,
					RouteInput:        test.input,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if state.RouteDecision.Decision != test.decision ||
				state.ImplementationRoute != test.route ||
				state.SDDRunRef != "" ||
				state.RouteAcceptanceRef != "" {
				t.Fatalf("route state = %#v", state)
			}
			root := filepath.Join(fixture.commonDir, "gentle-ai")
			beforeReplay := ownerTreeFingerprint(t, root)
			replayed, err := fixture.coordinator.StartWork(
				context.Background(),
				OwnerStartWorkRequest{
					DeliveryIntentRef: intentRef,
					RouteInput:        test.input,
				},
			)
			if err != nil || replayed.Revision != state.Revision {
				t.Fatalf("route replay = %#v, %v", replayed, err)
			}
			if afterReplay := ownerTreeFingerprint(
				t,
				root,
			); afterReplay != beforeReplay {
				t.Fatalf(
					"route replay published authority:\nbefore %s\nafter  %s",
					beforeReplay,
					afterReplay,
				)
			}
			if _, err := os.Stat(filepath.Join(
				fixture.commonDir,
				"gentle-ai",
				"sdd-runtime",
			)); !os.IsNotExist(err) {
				t.Fatalf("direct/delegated route created SDD state: %v", err)
			}
		})
	}
}

func TestOwnerCoordinatorProposedSDDRequiresOptInBeforeBinding(t *testing.T) {
	fixture := newOwnerCoordinatorFixture(t, "sdd-opt-in")
	ctx := context.Background()
	intentRef := fixture.admitDefaultIntent(t, "intent-sdd")
	proposed, err := fixture.coordinator.StartWork(
		ctx,
		OwnerStartWorkRequest{
			DeliveryIntentRef: intentRef,
			RouteInput: workrun.ImplementationRouteInput{
				DurablePlanning: []workrun.DurablePlanningReason{
					workrun.PlanningArchitecture,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if proposed.RouteDecision.Decision != workrun.RouteDecisionProposeSDD ||
		proposed.ImplementationRoute != "" ||
		proposed.RouteAcceptanceRef != "" {
		t.Fatalf("proposal state = %#v", proposed)
	}

	const runRef = "change-opt-in"
	if err := os.MkdirAll(
		filepath.Join(fixture.repo, "openspec", "changes", runRef),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	runtime, err := sddstatus.OpenRuntimeStore(ctx, fixture.repo, runRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.coordinator.BindAcceptedSDDRun(
		ctx,
		OwnerBindSDDRunRequest{
			ExpectedRevision:   proposed.Revision,
			RouteAcceptanceRef: ownerTestRef("not-accepted"),
			RunRef:             runRef,
		},
	); !errors.Is(err, workrun.ErrWorkRunInvalidTransition) {
		t.Fatalf("pre-accept SDD bind error = %v", err)
	}
	if _, err := os.Stat(runtime.Dir); !os.IsNotExist(err) {
		t.Fatalf("rejected pre-accept bind published SDD runtime: %v", err)
	}

	accepted, err := fixture.coordinator.AcceptProposedSDD(
		ctx,
		OwnerAcceptSDDRequest{
			ExpectedRevision:      proposed.Revision,
			PendingDecisionDigest: proposed.RouteDecision.Digest,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.ImplementationRoute != workrun.ImplementationRouteSDD ||
		accepted.RouteAcceptanceRef == "" ||
		accepted.SDDRunRef != "" {
		t.Fatalf("accepted SDD state = %#v", accepted)
	}
	acceptReplay, err := fixture.coordinator.AcceptProposedSDD(
		ctx,
		OwnerAcceptSDDRequest{
			ExpectedRevision:      proposed.Revision,
			PendingDecisionDigest: proposed.RouteDecision.Digest,
		},
	)
	if err != nil || acceptReplay.Revision != accepted.Revision {
		t.Fatalf("exact SDD acceptance replay = %#v, %v", acceptReplay, err)
	}
	bound, err := fixture.coordinator.BindAcceptedSDDRun(
		ctx,
		OwnerBindSDDRunRequest{
			ExpectedRevision:   accepted.Revision,
			RouteAcceptanceRef: accepted.RouteAcceptanceRef,
			RunRef:             runRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.SDDRunRef != runRef ||
		bound.ImplementationRoute != workrun.ImplementationRouteSDD {
		t.Fatalf("bound SDD state = %#v", bound)
	}
	binding, err := runtime.ResolveWorkRunBinding(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if binding.RunRef != runRef ||
		binding.WorkRunID != fixture.workRunID ||
		binding.RouteAcceptanceRef != accepted.RouteAcceptanceRef {
		t.Fatalf("SDD owner binding = %#v", binding)
	}
}

func TestOwnerCoordinatorOwnsExplicitSDDRequestAuthority(t *testing.T) {
	fixture := newOwnerCoordinatorFixture(t, "explicit-sdd")
	ctx := context.Background()
	intentRef := fixture.admitDefaultIntent(t, "intent-explicit-sdd")
	root := filepath.Join(fixture.commonDir, "gentle-ai")

	beforeRejected := ownerTreeFingerprint(t, root)
	if _, err := fixture.coordinator.StartWork(
		ctx,
		OwnerStartWorkRequest{
			DeliveryIntentRef: intentRef,
			RouteInput: workrun.ImplementationRouteInput{
				ExplicitSDDRequestRef: ownerTestRef("caller-authored-sdd"),
			},
			ExplicitSDDRequested: true,
		},
	); err == nil {
		t.Fatal("owner start accepted a caller-authored explicit SDD authority ref")
	}
	if after := ownerTreeFingerprint(t, root); after != beforeRejected {
		t.Fatalf(
			"rejected caller-authored SDD request published authority:\nbefore %s\nafter  %s",
			beforeRejected,
			after,
		)
	}
	if _, err := fixture.coordinator.StartWork(
		ctx,
		OwnerStartWorkRequest{
			DeliveryIntentRef:    ownerTestRef("unadmitted-intent"),
			ExplicitSDDRequested: true,
		},
	); !errors.Is(err, deliveryadmission.ErrAdmittedDecisionNotFound) {
		t.Fatalf("unadmitted explicit SDD request error = %v", err)
	}
	if after := ownerTreeFingerprint(t, root); after != beforeRejected {
		t.Fatalf(
			"unadmitted explicit SDD request published authority:\nbefore %s\nafter  %s",
			beforeRejected,
			after,
		)
	}

	started, err := fixture.coordinator.StartWork(
		ctx,
		OwnerStartWorkRequest{
			DeliveryIntentRef:    intentRef,
			ExplicitSDDRequested: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	explicitRef := started.RouteDecision.ExplicitSDDRequestRef
	if started.RouteDecision.Decision != workrun.RouteDecisionProposeSDD ||
		started.ImplementationRoute != workrun.ImplementationRouteSDD ||
		explicitRef == "" ||
		started.RouteAcceptanceRef != explicitRef {
		t.Fatalf("explicit SDD state = %#v", started)
	}
	explicit, err := fixture.coordinator.coordination.ResolveExplicitSDDRequest(
		ctx,
		explicitRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.AuthorityRef != explicitRef ||
		explicit.WorkRunID != fixture.workRunID ||
		explicit.DeliveryIntentRef != intentRef {
		t.Fatalf("explicit SDD authority = %#v", explicit)
	}
	beforeReplay := ownerTreeFingerprint(t, root)
	replayed, err := fixture.coordinator.StartWork(
		ctx,
		OwnerStartWorkRequest{
			DeliveryIntentRef:    intentRef,
			ExplicitSDDRequested: true,
		},
	)
	if err != nil || replayed.Revision != started.Revision {
		t.Fatalf("explicit SDD replay = %#v, %v", replayed, err)
	}
	if after := ownerTreeFingerprint(t, root); after != beforeReplay {
		t.Fatalf(
			"explicit SDD replay published authority:\nbefore %s\nafter  %s",
			beforeReplay,
			after,
		)
	}
}

func TestOwnerCoordinatorRejectsStaleRouteChoiceWithoutPublication(t *testing.T) {
	fixture := newOwnerCoordinatorFixture(t, "stale-route")
	intentRef := fixture.admitDefaultIntent(t, "intent-stale-route")
	proposed, err := fixture.coordinator.StartWork(
		context.Background(),
		OwnerStartWorkRequest{
			DeliveryIntentRef: intentRef,
			RouteInput: workrun.ImplementationRouteInput{
				DurablePlanning: []workrun.DurablePlanningReason{
					workrun.PlanningArchitecture,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	staleRevision := ownerTestRef("stale-route-revision")
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "accept",
			run: func() error {
				_, err := fixture.coordinator.AcceptProposedSDD(
					context.Background(),
					OwnerAcceptSDDRequest{
						ExpectedRevision:      staleRevision,
						PendingDecisionDigest: proposed.RouteDecision.Digest,
					},
				)
				return err
			},
		},
		{
			name: "reroute",
			run: func() error {
				_, err := fixture.coordinator.RerouteProposedSDD(
					context.Background(),
					OwnerRerouteRequest{
						ExpectedRevision:      staleRevision,
						PendingDecisionDigest: proposed.RouteDecision.Digest,
						RouteInput: workrun.ImplementationRouteInput{
							WriteIntent:    workrun.WriteIntentAtomicMechanical,
							WriteFileCount: 1,
						},
					},
				)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := ownerTreeFingerprint(t, root)
			if err := test.run(); !errors.Is(
				err,
				workrun.ErrWorkRunRevisionConflict,
			) {
				t.Fatalf("stale %s error = %v", test.name, err)
			}
			after := ownerTreeFingerprint(t, root)
			if before != after {
				t.Fatalf(
					"stale %s published route authority:\nbefore %s\nafter  %s",
					test.name,
					before,
					after,
				)
			}
		})
	}
}

func TestOwnerCoordinatorCanRerouteDeclinedSDDWithoutCreatingSDD(t *testing.T) {
	fixture := newOwnerCoordinatorFixture(t, "sdd-reroute")
	intentRef := fixture.admitDefaultIntent(t, "intent-reroute")
	proposed, err := fixture.coordinator.StartWork(
		context.Background(),
		OwnerStartWorkRequest{
			DeliveryIntentRef: intentRef,
			RouteInput: workrun.ImplementationRouteInput{
				DurablePlanning: []workrun.DurablePlanningReason{
					workrun.PlanningMultiWorkUnit,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rerouted, err := fixture.coordinator.RerouteProposedSDD(
		context.Background(),
		OwnerRerouteRequest{
			ExpectedRevision:      proposed.Revision,
			PendingDecisionDigest: proposed.RouteDecision.Digest,
			RouteInput: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if rerouted.RouteDecision.Decision != workrun.RouteDecisionDirectInline ||
		rerouted.ImplementationRoute != workrun.ImplementationRouteDirectInline ||
		rerouted.RouteAcceptanceRef == "" ||
		rerouted.SDDRunRef != "" {
		t.Fatalf("rerouted state = %#v", rerouted)
	}
	replay, err := fixture.coordinator.RerouteProposedSDD(
		context.Background(),
		OwnerRerouteRequest{
			ExpectedRevision:      proposed.Revision,
			PendingDecisionDigest: proposed.RouteDecision.Digest,
			RouteInput: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
		},
	)
	if err != nil || replay.Revision != rerouted.Revision {
		t.Fatalf("exact reroute replay = %#v, %v", replay, err)
	}
	if _, err := os.Stat(filepath.Join(
		fixture.commonDir,
		"gentle-ai",
		"sdd-runtime",
	)); !os.IsNotExist(err) {
		t.Fatalf("reroute created SDD state: %v", err)
	}
}

func TestOwnerCoordinatorRejectsCrossRepositoryComposition(t *testing.T) {
	left := newOwnerCoordinatorFixture(t, "same-work-run")
	right := newOwnerCoordinatorFixture(t, "same-work-run")
	_, err := NewOwnerCoordinator(
		context.Background(),
		OwnerCoordinatorDependencies{
			WorkRun:         left.coordinator.work,
			Coordination:    right.coordinator.coordination,
			RAR:             right.coordinator.rar,
			Transitions:     right.coordinator.transitions,
			Evidence:        right.coordinator.evidence,
			Mutations:       right.coordinator.mutations,
			PADAuthority:    right.coordinator.pad,
			SDDAuthority:    right.coordinator.sdd,
			LaunchAuthority: ownerTestLaunchAuthority{},
			Activation:      StaticActivationResolver{Mode: ActivationEnabled},
		},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "different repository identities") {
		t.Fatalf("cross-repository composition error = %v", err)
	}
}

func TestOwnerCoordinatorRejectsForeignMutationIntegrityStore(t *testing.T) {
	left := newOwnerCoordinatorFixture(t, "foreign-mmi")
	right := newOwnerCoordinatorFixture(t, "foreign-mmi")
	_, err := NewOwnerCoordinator(
		context.Background(),
		OwnerCoordinatorDependencies{
			WorkRun:         left.coordinator.work,
			Coordination:    left.coordinator.coordination,
			RAR:             left.coordinator.rar,
			Transitions:     left.coordinator.transitions,
			Evidence:        left.coordinator.evidence,
			Mutations:       right.coordinator.mutations,
			PADAuthority:    left.coordinator.pad,
			SDDAuthority:    left.coordinator.sdd,
			LaunchAuthority: ownerTestLaunchAuthority{},
			Activation:      StaticActivationResolver{Mode: ActivationEnabled},
		},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "different repository identities") {
		t.Fatalf("foreign mutation-integrity composition error = %v", err)
	}
}

func TestOwnerCoordinatorRejectsLinkedWorktreeEvidenceStore(t *testing.T) {
	fixture := newOwnerCoordinatorFixture(t, "linked-evidence")
	linked := filepath.Join(filepath.Dir(fixture.repo), "linked-evidence-worktree")
	ownerGit(
		t,
		fixture.repo,
		"worktree",
		"add",
		"--quiet",
		"--detach",
		linked,
		"HEAD",
	)
	t.Cleanup(func() {
		ownerGit(t, fixture.repo, "worktree", "remove", "--force", linked)
	})
	foreignEvidence, err := evidence.OpenStore(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if foreignEvidence.Root() != fixture.coordinator.evidence.Root() {
		t.Fatalf(
			"linked worktree evidence roots differ: %q != %q",
			foreignEvidence.Root(),
			fixture.coordinator.evidence.Root(),
		)
	}
	if foreignEvidence.RepositoryRef() ==
		fixture.coordinator.evidence.RepositoryRef() {
		t.Fatal("linked worktree evidence stores share an exact repository ref")
	}
	_, err = NewOwnerCoordinator(
		context.Background(),
		OwnerCoordinatorDependencies{
			WorkRun:         fixture.coordinator.work,
			Coordination:    fixture.coordinator.coordination,
			RAR:             fixture.coordinator.rar,
			Transitions:     fixture.coordinator.transitions,
			Evidence:        foreignEvidence,
			Mutations:       fixture.coordinator.mutations,
			PADAuthority:    fixture.coordinator.pad,
			SDDAuthority:    fixture.coordinator.sdd,
			LaunchAuthority: ownerTestLaunchAuthority{},
			Activation:      StaticActivationResolver{Mode: ActivationEnabled},
		},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "different repository identities") {
		t.Fatalf("linked-worktree evidence composition error = %v", err)
	}
}

func TestOwnerCoordinatorRejectsLinkedWorktreeRARStore(t *testing.T) {
	fixture := newOwnerCoordinatorFixture(t, "linked-rar")
	linked := filepath.Join(filepath.Dir(fixture.repo), "linked-rar-worktree")
	ownerGit(
		t,
		fixture.repo,
		"worktree",
		"add",
		"--quiet",
		"--detach",
		linked,
		"HEAD",
	)
	t.Cleanup(func() {
		ownerGit(t, fixture.repo, "worktree", "remove", "--force", linked)
	})
	foreignRAR, err := reviewtransaction.OpenRARAuthorityRepository(
		context.Background(),
		linked,
	)
	if err != nil {
		t.Fatal(err)
	}
	mixedCoordination, err := OpenCoordinationAuthorityStore(
		context.Background(),
		fixture.repo,
		fixture.workRunID,
		foreignRAR,
	)
	if err != nil {
		t.Fatal(err)
	}
	if foreignRAR.RepositoryRef() ==
		fixture.coordinator.rar.RepositoryRef() {
		t.Fatal("linked worktree RAR stores share an exact repository ref")
	}
	_, err = NewOwnerCoordinator(
		context.Background(),
		OwnerCoordinatorDependencies{
			WorkRun:         fixture.coordinator.work,
			Coordination:    mixedCoordination,
			RAR:             foreignRAR,
			Transitions:     fixture.coordinator.transitions,
			Evidence:        fixture.coordinator.evidence,
			Mutations:       fixture.coordinator.mutations,
			PADAuthority:    fixture.coordinator.pad,
			SDDAuthority:    fixture.coordinator.sdd,
			LaunchAuthority: ownerTestLaunchAuthority{},
			Activation:      StaticActivationResolver{Mode: ActivationEnabled},
		},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "different repository identities") {
		t.Fatalf("linked-worktree RAR composition error = %v", err)
	}
}

func TestOwnerCoordinatorRequiresActivationWithoutPublishing(t *testing.T) {
	fixture := newOwnerCoordinatorFixture(t, "missing-activation")
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	before := ownerTreeFingerprint(t, root)
	_, err := NewOwnerCoordinator(
		context.Background(),
		OwnerCoordinatorDependencies{
			WorkRun:         fixture.coordinator.work,
			Coordination:    fixture.coordinator.coordination,
			RAR:             fixture.coordinator.rar,
			Transitions:     fixture.coordinator.transitions,
			Evidence:        fixture.coordinator.evidence,
			Mutations:       fixture.coordinator.mutations,
			PADAuthority:    fixture.coordinator.pad,
			SDDAuthority:    fixture.coordinator.sdd,
			LaunchAuthority: ownerTestLaunchAuthority{},
		},
	)
	if err == nil {
		t.Fatal("owner coordinator accepted a missing activation resolver")
	}
	if after := ownerTreeFingerprint(t, root); after != before {
		t.Fatalf(
			"missing activation construction published authority:\nbefore %s\nafter  %s",
			before,
			after,
		)
	}
}

func TestOwnerCoordinatorAdmitsAndPublishesPADIntent(t *testing.T) {
	fixture := newOwnerCoordinatorFixture(t, "pad-admit")
	ctx := context.Background()
	policy, err := deliveryadmission.NewRoutePolicy(
		"policy:pr-without-issue",
		"revision:1",
		deliveryadmission.RoutePRWithoutIssue,
		true,
		false,
		false,
		120,
		0,
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
		SignalID:   "signal:pr-without-issue",
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
		"nonce:owner-coordinator",
		deliveryadmission.RoutePRWithoutIssue,
		ownerTestRef("scope"),
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
	admitted, err := fixture.coordinator.AdmitDeliveryIntent(
		ctx,
		OwnerDeliveryAdmissionRequest{
			Intent:       intent,
			AuthorityRef: authorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.IntentRef == "" || admitted.AdmissionDecisionRef == "" {
		t.Fatalf("admission = %#v", admitted)
	}
	resolved, err := fixture.pad.ResolveAdmittedIntent(ctx, admitted.IntentRef)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != intent {
		t.Fatalf("resolved intent = %#v, want %#v", resolved, intent)
	}
	beforeReplay := ownerTreeFingerprint(
		t,
		filepath.Join(fixture.commonDir, "gentle-ai"),
	)
	time.Sleep(1100 * time.Millisecond)
	replay, err := fixture.coordinator.AdmitDeliveryIntent(
		ctx,
		OwnerDeliveryAdmissionRequest{
			Intent:       intent,
			AuthorityRef: authorityRef,
		},
	)
	if err != nil ||
		replay.IntentRef != admitted.IntentRef ||
		replay.AdmissionDecisionRef != admitted.AdmissionDecisionRef {
		t.Fatalf("exact admission replay = %#v, %v", replay, err)
	}
	afterReplay := ownerTreeFingerprint(
		t,
		filepath.Join(fixture.commonDir, "gentle-ai"),
	)
	if beforeReplay != afterReplay {
		t.Fatalf(
			"admission replay published a second clock-dependent decision:\nbefore %s\nafter  %s",
			beforeReplay,
			afterReplay,
		)
	}
	conflictingAuthority := authority
	conflictingAuthority.SignalID = "signal:pr-without-issue-conflict"
	conflictingAuthorityRef, err := fixture.pad.PublishAuthoritySignal(
		ctx,
		conflictingAuthority,
	)
	if err != nil {
		t.Fatal(err)
	}
	beforeConflict := ownerTreeFingerprint(
		t,
		filepath.Join(fixture.commonDir, "gentle-ai"),
	)
	if _, err := fixture.coordinator.AdmitDeliveryIntent(
		ctx,
		OwnerDeliveryAdmissionRequest{
			Intent:       intent,
			AuthorityRef: conflictingAuthorityRef,
		},
	); !errors.Is(err, deliveryadmission.ErrTrustedRepositoryConflict) {
		t.Fatalf("conflicting admission replay error = %v", err)
	}
	if afterConflict := ownerTreeFingerprint(
		t,
		filepath.Join(fixture.commonDir, "gentle-ai"),
	); afterConflict != beforeConflict {
		t.Fatalf(
			"conflicting admission replay published authority:\nbefore %s\nafter  %s",
			beforeConflict,
			afterConflict,
		)
	}
}

func TestOwnerCoordinatorStatusDoesNotPublish(t *testing.T) {
	fixture := newOwnerCoordinatorFixture(t, "read-only")
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	beforeMissing := ownerTreeFingerprint(t, root)
	if _, err := fixture.coordinator.Status(context.Background()); !errors.Is(
		err,
		workrun.ErrWorkRunNotStarted,
	) {
		t.Fatalf("missing status error = %v", err)
	}
	afterMissing := ownerTreeFingerprint(t, root)
	if beforeMissing != afterMissing {
		t.Fatalf(
			"missing status published authority:\nbefore %s\nafter  %s",
			beforeMissing,
			afterMissing,
		)
	}
	if _, err := os.Stat(fixture.coordinator.work.Dir); !os.IsNotExist(err) {
		t.Fatalf("missing status created WorkRun storage: %v", err)
	}

	intentRef := fixture.admitDefaultIntent(t, "intent-read-only")
	started, err := fixture.coordinator.StartWork(
		context.Background(),
		OwnerStartWorkRequest{
			DeliveryIntentRef: intentRef,
			RouteInput: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	before := ownerTreeFingerprint(t, root)
	for range 8 {
		status, err := fixture.coordinator.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.Revision != started.Revision {
			t.Fatalf("status revision = %q, want %q", status.Revision, started.Revision)
		}
	}
	after := ownerTreeFingerprint(t, root)
	if before != after {
		t.Fatalf("status polling published authority:\nbefore %s\nafter  %s", before, after)
	}
}

func TestOwnerCoordinatorRunsRARForecastDispositionAndEPDTransition(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "verification-journey")
	ctx := context.Background()
	admission := fixture.admitDefaultDelivery(t, "verification-journey")
	applicability, registry, plan, snapshot := ownerVerificationPlan(
		t,
		fixture.repo,
		"verification-journey",
	)
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
	state, err := fixture.coordinator.StartWork(
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
	requirements := make([]string, 0, len(plan.Obligations))
	for _, obligation := range plan.Obligations {
		requirements = append(requirements, obligation.RequirementRef)
	}
	handoffExpectedRevision := state.Revision
	handoffRequest := OwnerBindHandoffRequest{
		ExpectedRevision: handoffExpectedRevision,
		Target: reviewtransaction.Target{
			Kind: reviewtransaction.TargetCurrentChanges,
			IntendedUntracked: append(
				[]string(nil),
				snapshot.IntendedUntracked...,
			),
		},
		IntendedPaths:          append([]string(nil), snapshot.Paths...),
		DeclaredObligationRefs: requirements,
		EvidenceRefs:           []string{},
	}
	state, err = fixture.coordinator.BindImplementationHandoff(
		ctx,
		handoffRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Handoff == nil ||
		state.Handoff.Schema != workrun.ImplementationHandoffSchemaV2 ||
		state.Handoff.ScopeDigest != admission.Intent.ScopeDigest ||
		state.Handoff.Subject != plan.Subject ||
		state.Handoff.MutationCompletionRef == "" {
		t.Fatalf("owner-derived handoff = %#v", state.Handoff)
	}
	handoff := *state.Handoff

	root := filepath.Join(fixture.commonDir, "gentle-ai")
	beforeHandoffReplay := ownerTreeContentFingerprint(t, root)
	handoffReplay, err := fixture.coordinator.BindImplementationHandoff(
		ctx,
		handoffRequest,
	)
	if err != nil || handoffReplay.Revision != state.Revision {
		t.Fatalf("handoff replay = %#v, %v", handoffReplay, err)
	}
	if after := ownerTreeContentFingerprint(t, root); after != beforeHandoffReplay {
		t.Fatalf(
			"handoff replay published authority:\nbefore %s\nafter  %s",
			beforeHandoffReplay,
			after,
		)
	}
	forecastRequest := OwnerForecastRequest{
		ExpectedRevision: state.Revision,
		Handoff:          handoff,
		PlanRevisionRef:  planAuthority.AuthorityRef,
		Availability:     workrun.ForecastAvailable,
		DiagnosticRefs:   []string{},
	}
	staleForecast := forecastRequest
	staleForecast.ExpectedRevision = ownerTestRef("stale-forecast")
	beforeStaleForecast := ownerTreeFingerprint(t, root)
	if _, err := fixture.coordinator.PublishVerificationForecast(
		ctx,
		staleForecast,
	); !errors.As(err, new(*workrun.RevisionConflictError)) {
		t.Fatalf("stale forecast error = %v", err)
	}
	if after := ownerTreeFingerprint(t, root); after != beforeStaleForecast {
		t.Fatalf(
			"stale forecast published authority:\nbefore %s\nafter  %s",
			beforeStaleForecast,
			after,
		)
	}
	forecastState, err := fixture.coordinator.PublishVerificationForecast(
		ctx,
		forecastRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if forecastState.Forecast == nil ||
		forecastState.Forecast.PlanRevisionRef != planAuthority.AuthorityRef ||
		forecastState.Forecast.Availability != workrun.ForecastAvailable {
		t.Fatalf("forecast state = %#v", forecastState)
	}
	beforeForecastReplay := ownerTreeFingerprint(t, root)
	forecastReplay, err := fixture.coordinator.PublishVerificationForecast(
		ctx,
		forecastRequest,
	)
	if err != nil || forecastReplay.Revision != forecastState.Revision {
		t.Fatalf("forecast replay = %#v, %v", forecastReplay, err)
	}
	if after := ownerTreeFingerprint(t, root); after != beforeForecastReplay {
		t.Fatalf(
			"forecast replay published authority:\nbefore %s\nafter  %s",
			beforeForecastReplay,
			after,
		)
	}

	dispositionRequest := OwnerDispositionRequest{
		ExpectedRevision: forecastState.Revision,
		Handoff:          handoff,
		Forecast:         *forecastState.Forecast,
		Kind:             workrun.DispositionRun,
		AssumptionsRef:   ownerTestRef("verification-assumptions"),
		ActorRef:         ownerTestRef("verification-actor"),
	}
	staleDisposition := dispositionRequest
	staleDisposition.ExpectedRevision = ownerTestRef("stale-disposition")
	beforeStaleDisposition := ownerTreeFingerprint(t, root)
	if _, err := fixture.coordinator.PublishVerificationDisposition(
		ctx,
		staleDisposition,
	); !errors.As(err, new(*workrun.RevisionConflictError)) {
		t.Fatalf("stale disposition error = %v", err)
	}
	if after := ownerTreeFingerprint(t, root); after != beforeStaleDisposition {
		t.Fatalf(
			"stale disposition published authority:\nbefore %s\nafter  %s",
			beforeStaleDisposition,
			after,
		)
	}
	dispositionState, err := fixture.coordinator.PublishVerificationDisposition(
		ctx,
		dispositionRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if dispositionState.Disposition == nil ||
		dispositionState.Disposition.Kind != workrun.DispositionRun {
		t.Fatalf("disposition state = %#v", dispositionState)
	}
	beforeDispositionReplay := ownerTreeFingerprint(t, root)
	dispositionReplay, err :=
		fixture.coordinator.PublishVerificationDisposition(
			ctx,
			dispositionRequest,
		)
	if err != nil || dispositionReplay.Revision != dispositionState.Revision {
		t.Fatalf("disposition replay = %#v, %v", dispositionReplay, err)
	}
	if after := ownerTreeFingerprint(t, root); after != beforeDispositionReplay {
		t.Fatalf(
			"disposition replay published authority:\nbefore %s\nafter  %s",
			beforeDispositionReplay,
			after,
		)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	obligation := plan.Obligations[0]
	ticket, err := evidence.NewActionTicket(evidence.ActionTicketRequest{
		IssuerRef:              dispositionRequest.ActorRef,
		SubjectRef:             dispositionState.Forecast.SubjectRef,
		CandidateRef:           handoff.CandidateRef,
		VerificationContextRef: dispositionState.Forecast.Digest,
		ExpectedRevision:       planAuthority.AuthorityRef,
		Slot:                   obligation.ID,
		Program:                executable,
		Args:                   []string{"-test.run=^$"},
		CWD:                    fixture.repo,
		Environment:            map[string]string{},
		Deadline:               5 * time.Second,
		OutputLimits: hostruntime.StreamLimits{
			StdoutBytes: 1024,
			StderrBytes: 1024,
		},
		Capability:        obligation.CapabilityRef,
		RedactionLiterals: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	transitionRequest := OwnerVerificationTransitionRequest{
		ExpectedRevision: dispositionState.Revision,
		Ticket:           ticket,
		IssuedAt:         fixture.now,
		ExpiresAt:        fixture.now + 300,
	}
	staleTransition := transitionRequest
	staleTransition.ExpectedRevision = ownerTestRef("stale-transition")
	beforeStaleTransition := ownerTreeFingerprint(t, root)
	if _, err := fixture.coordinator.IssueVerificationTransition(
		ctx,
		staleTransition,
	); !errors.As(err, new(*workrun.RevisionConflictError)) {
		t.Fatalf("stale transition error = %v", err)
	}
	if after := ownerTreeFingerprint(t, root); after != beforeStaleTransition {
		t.Fatalf(
			"stale transition published authority:\nbefore %s\nafter  %s",
			beforeStaleTransition,
			after,
		)
	}
	transition, err := fixture.coordinator.IssueVerificationTransition(
		ctx,
		transitionRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if transition.TicketRef != ticket.TicketRef ||
		transition.Authorization.Class != AuthorizationGeneral ||
		transition.Authorization.ExpectedRevision != dispositionState.Revision {
		t.Fatalf("verification transition = %#v", transition)
	}
	resolved, phase, err := fixture.coordinator.transitions.ResolveAuthorization(
		ctx,
		transition.Authorization.AuthorizationRef,
		fixture.now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if phase != TransitionPending ||
		resolved.AuthorizationRef != transition.Authorization.AuthorizationRef {
		t.Fatalf("resolved transition = %#v, %q", resolved, phase)
	}
	beforeTransitionReplay := ownerTreeFingerprint(t, root)
	replayed, err := fixture.coordinator.IssueVerificationTransition(
		ctx,
		transitionRequest,
	)
	if err != nil ||
		replayed.Authorization.AuthorizationRef !=
			transition.Authorization.AuthorizationRef {
		t.Fatalf("transition replay = %#v, %v", replayed, err)
	}
	if after := ownerTreeFingerprint(t, root); after != beforeTransitionReplay {
		t.Fatalf(
			"transition replay published authority:\nbefore %s\nafter  %s",
			beforeTransitionReplay,
			after,
		)
	}
}

func TestOwnerCoordinatorReplansOneCorrectionWithFreshMMICompletion(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "correction-replan")
	ctx := context.Background()
	admission := fixture.admitDefaultDelivery(t, "correction-replan")
	applicability, registry, plan, snapshot := ownerVerificationPlan(
		t,
		fixture.repo,
		"correction-replan",
	)
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
	state, err := fixture.coordinator.StartWork(
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
	requirements := make([]string, 0, len(plan.Obligations))
	for _, obligation := range plan.Obligations {
		requirements = append(requirements, obligation.RequirementRef)
	}
	target := reviewtransaction.Target{
		Kind: reviewtransaction.TargetCurrentChanges,
		IntendedUntracked: append(
			[]string(nil),
			snapshot.IntendedUntracked...,
		),
	}
	state, err = fixture.coordinator.BindImplementationHandoff(
		ctx,
		OwnerBindHandoffRequest{
			ExpectedRevision:       state.Revision,
			Target:                 target,
			IntendedPaths:          append([]string(nil), snapshot.Paths...),
			DeclaredObligationRefs: requirements,
			EvidenceRefs:           []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	originalHandoff := *state.Handoff
	state, err = fixture.coordinator.PublishVerificationForecast(
		ctx,
		OwnerForecastRequest{
			ExpectedRevision: state.Revision,
			Handoff:          originalHandoff,
			PlanRevisionRef:  planAuthority.AuthorityRef,
			Availability:     workrun.ForecastAvailable,
			DiagnosticRefs:   []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	originalForecast := *state.Forecast
	logicalPath := "correction-replan.go"
	if err := os.WriteFile(
		filepath.Join(fixture.repo, logicalPath),
		[]byte("package ownerfixture\n\nfunc value() int { return 2 }\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	correctionRequest := OwnerCorrectionReplanRequest{
		ExpectedRevision:       state.Revision,
		Target:                 target,
		IntendedPaths:          []string{logicalPath},
		DeclaredObligationRefs: requirements,
		EvidenceRefs:           []string{},
	}
	correctionExpectedRevision := state.Revision
	state, err = fixture.coordinator.ReplanVerificationAfterCorrection(
		ctx,
		correctionRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Handoff == nil ||
		state.VerificationReplan == nil ||
		state.Forecast != nil ||
		state.Handoff.CandidateRef == originalHandoff.CandidateRef ||
		state.Handoff.MutationCompletionRef ==
			originalHandoff.MutationCompletionRef ||
		state.Handoff.Route != originalHandoff.Route ||
		state.Handoff.ScopeDigest != admission.Intent.ScopeDigest ||
		state.Handoff.SDDRunRef != originalHandoff.SDDRunRef ||
		state.VerificationReplan.OriginalAvailabilityRef !=
			originalForecast.AvailabilityRef {
		t.Fatalf("corrected WorkRun state = %#v", state)
	}
	if _, err := fixture.coordinator.mutations.Read(
		ctx,
		state.Handoff.MutationCompletionRef,
	); err != nil {
		t.Fatalf("read corrected MMI completion: %v", err)
	}

	replayed, err := fixture.coordinator.ReplanVerificationAfterCorrection(
		ctx,
		correctionRequest,
	)
	if err != nil || replayed.Revision != state.Revision {
		t.Fatalf("correction replay = %#v, %v", replayed, err)
	}
	wrongCAS := correctionRequest
	wrongCAS.ExpectedRevision = state.Revision
	if _, err := fixture.coordinator.ReplanVerificationAfterCorrection(
		ctx,
		wrongCAS,
	); err == nil {
		t.Fatal("correction replay accepted a different CAS/request identity")
	}

	if err := os.WriteFile(
		filepath.Join(fixture.repo, logicalPath),
		[]byte("package ownerfixture\n\nfunc value() int { return 3 }\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	second := correctionRequest
	second.ExpectedRevision = state.Revision
	beforeSecond := ownerTreeFingerprint(
		t,
		filepath.Join(fixture.commonDir, "gentle-ai"),
	)
	if _, err := fixture.coordinator.ReplanVerificationAfterCorrection(
		ctx,
		second,
	); !errors.Is(err, workrun.ErrWorkRunInvalidTransition) {
		t.Fatalf("second correction error = %v", err)
	}
	if afterSecond := ownerTreeFingerprint(
		t,
		filepath.Join(fixture.commonDir, "gentle-ai"),
	); afterSecond != beforeSecond {
		t.Fatalf(
			"rejected second correction persisted authority:\nbefore %s\nafter  %s",
			beforeSecond,
			afterSecond,
		)
	}
	if correctionExpectedRevision == state.Revision {
		t.Fatal("correction did not advance WorkRun revision")
	}
}

func TestOwnerCoordinatorRecoveryOnlyCompletesRARPADTerminalization(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "recovery-terminal")
	ctx := context.Background()
	admission := fixture.admitDefaultDelivery(t, "recovery-terminal")
	applicability, registry, plan, snapshot := ownerNotRequiredPlan(
		t,
		fixture.repo,
		"recovery-terminal",
	)
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
	state, err := fixture.coordinator.StartWork(
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
			DeclaredObligationRefs: []string{},
			EvidenceRefs:           []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.Handoff == nil ||
		state.Handoff.ScopeDigest != admission.Intent.ScopeDigest {
		t.Fatalf("owner-derived recovery handoff = %#v", state.Handoff)
	}
	handoff := *state.Handoff
	state, err = fixture.coordinator.PublishVerificationForecast(
		ctx,
		OwnerForecastRequest{
			ExpectedRevision: state.Revision,
			Handoff:          handoff,
			PlanRevisionRef:  planAuthority.AuthorityRef,
			Availability:     workrun.ForecastNotRequired,
			DiagnosticRefs:   []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	receiptRef := ownerApprovedCompactReceiptRef(
		t,
		fixture.repo,
		"recovery-terminal",
		snapshot,
		plan.PolicyHash,
	)
	result := reviewtransaction.VerificationResultRef{
		Schema:               reviewtransaction.VerificationResultRefSchema,
		ResultRef:            ownerTestRef("recovery-terminal-result"),
		Subject:              plan.Subject,
		PolicyHash:           plan.PolicyHash,
		PlanDigest:           plan.Digest,
		ApplicabilityDigest:  plan.ApplicabilityDigest,
		Aggregate:            reviewtransaction.VerificationAggregateNotRequired,
		CompletedObligations: []string{},
		EvidenceRefs:         []string{},
	}
	rarAuthority, err := fixture.coordinator.rar.Publish(
		ctx,
		reviewtransaction.RARAuthorityPublication{
			LineageID:     "recovery-terminal",
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
	authorizationRef := ownerPublishPADDeliveryAuthorization(
		t,
		fixture,
		admission,
		rarAuthority,
	)

	resolver := &ownerTestActivationResolver{mode: ActivationRecoveryOnly}
	fixture.coordinator.activation = resolver
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	resultRequest := OwnerBindResultRequest{
		ExpectedRevision: state.Revision,
		ResultRef:        result.ResultRef,
	}
	resultState, err := fixture.coordinator.BindVerificationResult(
		ctx,
		resultRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resultState.VerificationResultRef != result.ResultRef {
		t.Fatalf("result state = %#v", resultState)
	}
	beforeResultReplay := ownerTreeFingerprint(t, root)
	resultReplay, err := fixture.coordinator.BindVerificationResult(
		ctx,
		resultRequest,
	)
	if err != nil || resultReplay.Revision != resultState.Revision {
		t.Fatalf("result replay = %#v, %v", resultReplay, err)
	}
	if after := ownerTreeFingerprint(t, root); after != beforeResultReplay {
		t.Fatalf(
			"result replay published authority:\nbefore %s\nafter  %s",
			beforeResultReplay,
			after,
		)
	}
	reviewRequest := OwnerBindReviewRequest{
		ExpectedRevision: resultState.Revision,
		ReviewReceiptRef: receiptRef,
	}
	reviewState, err := fixture.coordinator.BindReviewReceipt(
		ctx,
		reviewRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reviewState.ReviewReceiptRef != receiptRef {
		t.Fatalf("review state = %#v", reviewState)
	}
	beforeReviewReplay := ownerTreeFingerprint(t, root)
	reviewReplay, err := fixture.coordinator.BindReviewReceipt(
		ctx,
		reviewRequest,
	)
	if err != nil || reviewReplay.Revision != reviewState.Revision {
		t.Fatalf("review replay = %#v, %v", reviewReplay, err)
	}
	if after := ownerTreeFingerprint(t, root); after != beforeReviewReplay {
		t.Fatalf(
			"review replay published authority:\nbefore %s\nafter  %s",
			beforeReviewReplay,
			after,
		)
	}
	deliveryRequest := OwnerBindDeliveryAuthorizationRequest{
		ExpectedRevision: reviewState.Revision,
		AuthorizationRef: authorizationRef,
	}
	deliveryState, err := fixture.coordinator.BindDeliveryAuthorization(
		ctx,
		deliveryRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if deliveryState.DeliveryAuthorizationRef != authorizationRef {
		t.Fatalf("delivery state = %#v", deliveryState)
	}
	beforeDeliveryReplay := ownerTreeFingerprint(t, root)
	deliveryReplay, err := fixture.coordinator.BindDeliveryAuthorization(
		ctx,
		deliveryRequest,
	)
	if err != nil || deliveryReplay.Revision != deliveryState.Revision {
		t.Fatalf("delivery replay = %#v, %v", deliveryReplay, err)
	}
	if after := ownerTreeFingerprint(t, root); after != beforeDeliveryReplay {
		t.Fatalf(
			"delivery replay published authority:\nbefore %s\nafter  %s",
			beforeDeliveryReplay,
			after,
		)
	}
	if len(resolver.repos) != 6 {
		t.Fatalf("recovery terminal activation resolutions = %#v", resolver.repos)
	}
	for _, repo := range resolver.repos {
		if repo != fixture.repo {
			t.Fatalf("recovery activation repo = %q, want %q", repo, fixture.repo)
		}
	}
	status, err := fixture.coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Revision != deliveryState.Revision ||
		status.VerificationResultRef != result.ResultRef ||
		status.ReviewReceiptRef != receiptRef ||
		status.DeliveryAuthorizationRef != authorizationRef {
		t.Fatalf("terminal status = %#v", status)
	}
}

func TestOwnerCoordinatorResolvesKillSwitchLiveBeforeMutations(t *testing.T) {
	fixture := newOwnerCoordinatorFixture(t, "activation-live")
	intentRef := fixture.admitDefaultIntent(t, "activation-live")
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	resolver := &ownerTestActivationResolver{mode: ActivationRecoveryOnly}
	fixture.coordinator.activation = resolver

	before := ownerTreeFingerprint(t, root)
	if _, err := fixture.coordinator.StartWork(
		context.Background(),
		OwnerStartWorkRequest{
			DeliveryIntentRef: intentRef,
			RouteInput: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
		},
	); !errors.Is(err, ErrRecoveryOnly) {
		t.Fatalf("recovery-only start error = %v", err)
	}
	if after := ownerTreeFingerprint(t, root); after != before {
		t.Fatalf(
			"recovery-only start published authority:\nbefore %s\nafter  %s",
			before,
			after,
		)
	}
	if _, err := fixture.coordinator.IssueVerificationTransition(
		context.Background(),
		OwnerVerificationTransitionRequest{},
	); !errors.Is(err, ErrRecoveryOnly) {
		t.Fatalf("recovery-only transition issue error = %v", err)
	}
	if after := ownerTreeFingerprint(t, root); after != before {
		t.Fatalf(
			"recovery-only transition issue published authority:\nbefore %s\nafter  %s",
			before,
			after,
		)
	}
	if _, err := fixture.coordinator.Status(
		context.Background(),
	); !errors.Is(err, workrun.ErrWorkRunNotStarted) {
		t.Fatalf("recovery-only read status error = %v", err)
	}

	resolver.mode = ActivationEnabled
	started, err := fixture.coordinator.StartWork(
		context.Background(),
		OwnerStartWorkRequest{
			DeliveryIntentRef: intentRef,
			RouteInput: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
		},
	)
	if err != nil || !started.Started {
		t.Fatalf("live activation enable start = %#v, %v", started, err)
	}
	if len(resolver.repos) != 3 {
		t.Fatalf("activation resolutions = %#v", resolver.repos)
	}
	for _, repo := range resolver.repos {
		if repo != fixture.repo {
			t.Fatalf("activation resolved repo %q, want %q", repo, fixture.repo)
		}
	}
}

func TestOwnerCoordinatorUnsetEnvironmentDefaultsReadOnlyWithoutPublication(
	t *testing.T,
) {
	restoreDefaultActivationEnvironment(t)
	fixture := newOwnerCoordinatorFixture(t, "activation-default-read-only")
	intentRef := fixture.admitDefaultIntent(t, "activation-default-read-only")
	fixture.coordinator.activation = EnvironmentActivationResolver{}
	root := filepath.Join(fixture.commonDir, "gentle-ai")

	before := ownerTreeFingerprint(t, root)
	_, err := fixture.coordinator.StartWork(
		context.Background(),
		OwnerStartWorkRequest{
			DeliveryIntentRef: intentRef,
			RouteInput: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
		},
	)
	if !errors.Is(err, ErrCapabilityReadOnly) {
		t.Fatalf("unset environment start error = %v", err)
	}
	if after := ownerTreeFingerprint(t, root); after != before {
		t.Fatalf(
			"unset environment start published authority:\nbefore %s\nafter  %s",
			before,
			after,
		)
	}
}

func TestOwnerCoordinatorKillSwitchModesFailClosedWithoutPublication(
	t *testing.T,
) {
	tests := []struct {
		name string
		mode ActivationMode
		want error
	}{
		{"read-only", ActivationReadOnly, ErrCapabilityReadOnly},
		{"disabled", ActivationDisabled, ErrCapabilityDisabled},
		{"invalid", ActivationMode("unexpected"), ErrInvalidActivationMode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOwnerCoordinatorFixture(t, "activation-"+test.name)
			intentRef := fixture.admitDefaultIntent(t, "activation-"+test.name)
			fixture.coordinator.activation = StaticActivationResolver{
				Mode: test.mode,
			}
			root := filepath.Join(fixture.commonDir, "gentle-ai")
			before := ownerTreeFingerprint(t, root)
			_, err := fixture.coordinator.StartWork(
				context.Background(),
				OwnerStartWorkRequest{
					DeliveryIntentRef: intentRef,
					RouteInput: workrun.ImplementationRouteInput{
						WriteIntent:    workrun.WriteIntentAtomicMechanical,
						WriteFileCount: 1,
					},
				},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("%s start error = %v", test.name, err)
			}
			if after := ownerTreeFingerprint(t, root); after != before {
				t.Fatalf(
					"%s start published authority:\nbefore %s\nafter  %s",
					test.name,
					before,
					after,
				)
			}
		})
	}
}

func TestOwnerCoordinatorRejectsReusingVerificationReservation(t *testing.T) {
	state := workrun.WorkRunState{
		Reservations: []workrun.VerificationReservation{
			{
				ExpectedWorkRevision: ownerTestRef("expected"),
				ActionTicketRef:      ownerTestRef("ticket"),
				Slot:                 "unit",
			},
		},
	}
	exact, conflict := ownerTicketReservationState(
		state,
		ownerTestRef("expected"),
		ownerTestRef("ticket"),
		"unit",
	)
	if !exact || conflict {
		t.Fatalf("exact reservation = (%t, %t)", exact, conflict)
	}
	exact, conflict = ownerTicketReservationState(
		state,
		ownerTestRef("next"),
		ownerTestRef("other-ticket"),
		"unit",
	)
	if exact || !conflict {
		t.Fatalf("slot reuse = (%t, %t)", exact, conflict)
	}
	exact, conflict = ownerTicketReservationState(
		state,
		ownerTestRef("next"),
		ownerTestRef("ticket"),
		"integration",
	)
	if exact || !conflict {
		t.Fatalf("ticket reuse = (%t, %t)", exact, conflict)
	}
}

func TestOwnerVerificationTransitionRequestCannotSelectActivationClass(t *testing.T) {
	requestType := reflect.TypeOf(OwnerVerificationTransitionRequest{})
	if _, exists := requestType.FieldByName("Class"); exists {
		t.Fatal("ordinary owner transition request can select a privileged activation class")
	}
}

func TestOwnerImplementationRequestsCannotAuthorMMIAuthority(t *testing.T) {
	for _, requestType := range []reflect.Type{
		reflect.TypeOf(OwnerBindHandoffRequest{}),
		reflect.TypeOf(OwnerCorrectionReplanRequest{}),
	} {
		for _, forbidden := range []string{
			"Handoff",
			"Route",
			"ScopeDigest",
			"Subject",
			"CandidateRef",
			"MutationCompletionRef",
		} {
			if _, exists := requestType.FieldByName(forbidden); exists {
				t.Fatalf(
					"%s exposes caller-authored %s",
					requestType.Name(),
					forbidden,
				)
			}
		}
	}
}

func ownerGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", repo}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func ownerTestRef(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ownerVerificationPlan(
	t *testing.T,
	repo string,
	seed string,
) (
	reviewtransaction.VerificationApplicability,
	reviewtransaction.VerificationPlanRegistry,
	reviewtransaction.VerificationPlan,
	reviewtransaction.Snapshot,
) {
	t.Helper()
	logicalPath := seed + ".go"
	if err := os.WriteFile(
		filepath.Join(repo, logicalPath),
		[]byte("package ownerfixture\n\nfunc value() int { return 1 }\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	obligation := reviewtransaction.VerificationObligation{
		ID:             "unit.test",
		RequirementRef: ownerTestRef(seed + "-requirement"),
		ArgvRef:        ownerTestRef(seed + "-argv"),
		CapabilityRef:  "go.test",
		Cost:           reviewtransaction.VerificationCostQuick,
	}
	registry, err := reviewtransaction.NewVerificationPlanRegistry(
		ownerTestRef(seed+"-policy"),
		[]string{},
		[]reviewtransaction.VerificationObligation{obligation},
	)
	if err != nil {
		t.Fatal(err)
	}
	builder := reviewtransaction.SnapshotBuilder{Repo: repo}
	snapshot, err := builder.Build(
		context.Background(),
		reviewtransaction.Target{
			Kind:              reviewtransaction.TargetCurrentChanges,
			IntendedUntracked: []string{logicalPath},
		},
	)
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
	if applicability.Decision != reviewtransaction.VerificationApplicable {
		t.Fatalf("verification applicability = %#v", applicability)
	}
	plan, err := reviewtransaction.BuildVerificationPlan(
		applicability,
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	return applicability, registry, plan, snapshot
}

func ownerNotRequiredPlan(
	t *testing.T,
	repo string,
	seed string,
) (
	reviewtransaction.VerificationApplicability,
	reviewtransaction.VerificationPlanRegistry,
	reviewtransaction.VerificationPlan,
	reviewtransaction.Snapshot,
) {
	t.Helper()
	logicalPath := seed + ".md"
	if err := os.WriteFile(
		filepath.Join(repo, logicalPath),
		[]byte("# Maintainer notes\n\nHuman-readable documentation only.\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	registry, err := reviewtransaction.NewVerificationPlanRegistry(
		ownerTestRef(seed+"-policy"),
		[]string{},
		[]reviewtransaction.VerificationObligation{},
	)
	if err != nil {
		t.Fatal(err)
	}
	builder := reviewtransaction.SnapshotBuilder{Repo: repo}
	snapshot, err := builder.Build(
		context.Background(),
		reviewtransaction.Target{
			Kind:              reviewtransaction.TargetCurrentChanges,
			IntendedUntracked: []string{logicalPath},
		},
	)
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
	if applicability.Decision != reviewtransaction.VerificationNotApplicable {
		t.Fatalf("not-required applicability = %#v", applicability)
	}
	plan, err := reviewtransaction.BuildVerificationPlan(
		applicability,
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	return applicability, registry, plan, snapshot
}

func ownerApprovedCompactReceiptRef(
	t *testing.T,
	repo string,
	lineageID string,
	snapshot reviewtransaction.Snapshot,
	policyHash string,
) string {
	t.Helper()
	ctx := context.Background()
	assessment, err := (reviewtransaction.SnapshotBuilder{
		Repo: repo,
	}).AssessSnapshotRisk(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	lenses, err := reviewtransaction.SelectReviewLenses(
		assessment,
		"reliability",
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err := reviewtransaction.NewCompactState(reviewtransaction.Start{
		LineageID:            lineageID,
		Mode:                 reviewtransaction.ModeOrdinaryBounded,
		Generation:           1,
		Snapshot:             snapshot,
		PolicyHash:           policyHash,
		RiskLevel:            assessment.Level,
		SelectedLenses:       lenses,
		OriginalChangedLines: &assessment.ChangedLines,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(
		ctx,
		repo,
		lineageID,
	)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := store.Replace("", "review/start", state)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]reviewtransaction.LensResult, len(lenses))
	for index, lens := range lenses {
		results[index] = reviewtransaction.LensResult{
			Lens:     lens,
			Findings: []reviewtransaction.Finding{},
			Evidence: []string{"candidate inspected"},
		}
	}
	if err := state.CompleteReview(reviewtransaction.CompactReviewInput{
		LensResults:     results,
		Classifications: []reviewtransaction.FindingEvidence{},
		RefuterOutcomes: []reviewtransaction.EvidenceResult{},
	}); err != nil {
		t.Fatal(err)
	}
	revision, err = store.Replace(
		revision,
		"review/complete-review",
		state,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.CompleteVerification(
		[]byte("independent verification passed\n"),
		true,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Replace(
		revision,
		"review/complete-verification",
		state,
	); err != nil {
		t.Fatal(err)
	}
	receipt, err := state.Receipt()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteReceipt(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(store.ReceiptPath())
	if err != nil {
		t.Fatal(err)
	}
	return ownerTestRef(string(payload))
}

func ownerPublishPADDeliveryAuthorization(
	t *testing.T,
	fixture ownerCoordinatorFixture,
	admission ownerAdmittedDelivery,
	rarAuthority reviewtransaction.RARVerificationAuthority,
) string {
	t.Helper()
	ctx := context.Background()
	gates := deliveryadmission.GateEvidence{
		Schema: deliveryadmission.GateEvidenceContractV1,
		Route:  deliveryadmission.RoutePRWithoutIssue,
		Candidate: deliveryadmission.CandidateBinding{
			Ref:    "candidate:" + fixture.workRunID,
			Digest: rarAuthority.Result.Subject.SnapshotIdentity,
		},
		Destination:        admission.Intent.Destination,
		PolicyRef:          admission.PolicyRef,
		Policy:             admission.Policy.Binding(),
		Mechanism:          deliveryadmission.MechanismPullRequest,
		ProtectionMode:     deliveryadmission.ProtectionRespect,
		PullRequestRef:     "github:pr/1794",
		RequiredChecksRef:  ownerTestRef("terminal-required-checks"),
		RemoteFreshnessRef: ownerTestRef("terminal-remote-freshness"),
		ProtectionRef:      ownerTestRef("terminal-protection"),
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
			ReviewReceiptRef:      rarAuthority.Receipt.ReceiptRef,
			VerificationResultRef: rarAuthority.Result.ResultRef,
			GateRef:               gateRef,
			AuthorityRef:          admission.AuthorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	decisionRef, err := fixture.pad.PublishDeliveryDecision(ctx, decision)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := deliveryadmission.IssueAuthorization(
		ctx,
		fixture.pad,
		fixture.coordinator.rar,
		deliveryadmission.AuthorizationIssueRequest{
			DecisionRef:  decisionRef,
			Nonce:        "nonce:terminal-" + fixture.workRunID,
			AuthorityRef: admission.AuthorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorizationRef, err := fixture.pad.PublishAuthorization(
		ctx,
		authorization,
	)
	if err != nil {
		t.Fatal(err)
	}
	return authorizationRef
}

func ownerTreeFingerprint(t *testing.T, root string) string {
	t.Helper()
	entries := make([]string, 0, 64)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		line := fmt.Sprintf(
			"%s|%s|%d|%d",
			filepath.ToSlash(relative),
			info.Mode().String(),
			info.Size(),
			info.ModTime().UnixNano(),
		)
		if !entry.IsDir() {
			payload, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(payload)
			line += "|" + hex.EncodeToString(sum[:])
		}
		entries = append(entries, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return hex.EncodeToString(sum[:])
}

// ownerTreeContentFingerprint ignores transient directory mtimes from owner
// lock acquisition while still proving that replay publishes no durable file
// or authority-content change.
func ownerTreeContentFingerprint(t *testing.T, root string) string {
	t.Helper()
	entries := make([]string, 0, 64)
	err := filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if filepath.Base(relative) == "LOCK" {
			// Advisory lock ownership is deliberately refreshed on every
			// replay. It is transient coordination metadata, not durable
			// authority or state.
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(payload)
		entries = append(entries, fmt.Sprintf(
			"%s|%s|%d|%s",
			filepath.ToSlash(relative),
			info.Mode().String(),
			info.Size(),
			hex.EncodeToString(sum[:]),
		))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return hex.EncodeToString(sum[:])
}
