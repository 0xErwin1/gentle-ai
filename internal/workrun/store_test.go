package workrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

func TestWorkRunStoreReadDoesNotCreateAuthority(t *testing.T) {
	repo := initWorkRunRepository(t)
	store := openTestWorkRunStore(t, repo, "read-only")

	authorityRoot := filepath.Join(store.commonDir, "gentle-ai", "work-runs")
	for _, path := range []string{authorityRoot, store.repositoryDir, store.Dir} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("work run authority %q exists before read: %v", path, err)
		}
	}
	if _, err := store.Status(); !errors.Is(err, ErrWorkRunNotStarted) {
		t.Fatalf("Status() error = %v, want ErrWorkRunNotStarted", err)
	}
	for _, path := range []string{authorityRoot, store.repositoryDir, store.Dir} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("read created work run authority %q: %v", path, err)
		}
	}
}

func TestWorkRunStoreReopenKeepsExactRepositoryShard(t *testing.T) {
	repo := initWorkRunRepository(t)
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(
		context.Background(),
		repo,
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := OpenWorkRunStoreWithRepositoryIdentityLease(
		context.Background(),
		lease,
		"stable-reopen",
	)
	if err != nil {
		t.Fatal(err)
	}
	authority := newTestAuthorityRepository()
	first = first.WithAuthorityPorts(AuthorityPorts{
		PAD: authority, ExplicitSDDRequest: authority, Route: authority, SDD: authority,
		Verification: authority, Launch: hostruntime.NewExecutor(),
	})
	started := startDirectWorkRun(t, first)

	reopened := openTestWorkRunStore(t, repo, "stable-reopen")
	if reopened.Dir != first.Dir ||
		reopened.repositoryDir != first.repositoryDir ||
		reopened.RepositoryRef() != first.RepositoryRef() ||
		reopened.storageKey != first.storageKey {
		t.Fatalf(
			"reopened WorkRun identity differs:\nfirst=%#v\nreopened=%#v",
			first,
			reopened,
		)
	}
	state, err := reopened.Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.Revision != started.Revision || state.WorkRunID != "stable-reopen" {
		t.Fatalf("reopened state = %#v, want revision %q", state, started.Revision)
	}
	want := filepath.Join(
		first.commonDir,
		"gentle-ai",
		"work-runs",
		"v1",
		"repositories",
		first.storageKey,
		"stable-reopen",
	)
	if first.Dir != want {
		t.Fatalf("WorkRun Dir = %q, want %q", first.Dir, want)
	}
	if _, err := OpenWorkRunStoreWithRepositoryIdentityLease(
		context.Background(),
		nil,
		"stable-reopen",
	); err == nil {
		t.Fatal("nil WorkRun repository identity lease was accepted")
	}
}

func TestWorkRunStoreIsolatesMainAndLinkedWorktreeWithSameID(t *testing.T) {
	repo := initWorkRunRepository(t)
	linked := filepath.Join(t.TempDir(), "linked")
	runWorkRunGit(t, repo, "worktree", "add", "-q", "-b", "workrun-linked", linked)

	mainStore := openTestWorkRunStore(t, repo, "shared-id")
	linkedStore := openTestWorkRunStore(t, linked, "shared-id")
	if mainStore.commonDir != linkedStore.commonDir {
		t.Fatalf(
			"linked worktrees have different common dirs: %q != %q",
			mainStore.commonDir,
			linkedStore.commonDir,
		)
	}
	if mainStore.storageKey == linkedStore.storageKey ||
		mainStore.RepositoryRef() == linkedStore.RepositoryRef() ||
		mainStore.Dir == linkedStore.Dir {
		t.Fatalf(
			"linked worktrees share WorkRun authority:\nmain=%q (%s)\nlinked=%q (%s)",
			mainStore.Dir,
			mainStore.RepositoryRef(),
			linkedStore.Dir,
			linkedStore.RepositoryRef(),
		)
	}

	mainStarted := startDirectWorkRun(t, mainStore)
	linkedStarted := startDirectWorkRun(t, linkedStore)
	if _, err := mainStore.BindImplementationHandoff(
		context.Background(),
		BindImplementationHandoffRequest{
			ExpectedRevision: mainStarted.Revision,
			RequestID:        "main-handoff",
			Handoff: testHandoff(
				t,
				ImplementationRouteDirectInline,
				"",
				"main-scope",
			),
		},
	); err != nil {
		t.Fatal(err)
	}
	linkedState, err := linkedStore.Status()
	if err != nil {
		t.Fatal(err)
	}
	if linkedState.Revision != linkedStarted.Revision || linkedState.Handoff != nil {
		t.Fatalf("main worktree mutated linked WorkRun state: %#v", linkedState)
	}
}

func TestWorkRunStoreOldHandleRejectsGitControlSwapWithoutWritingReplacement(t *testing.T) {
	repo := initWorkRunRepository(t)
	store := openTestWorkRunStore(t, repo, "stale-handle")
	deliveryRef := testSHARef("stale-handle-delivery")
	registerTestDeliveryIntent(t, store, deliveryRef)

	originalGitDir := filepath.Join(repo, ".git")
	movedGitDir := filepath.Join(repo, ".git-before-swap")
	if err := os.Rename(originalGitDir, movedGitDir); err != nil {
		t.Skipf("platform cannot replace Git control directory: %v", err)
	}
	runWorkRunGit(t, repo, "init", "-q")

	if _, err := store.Start(context.Background(), StartRequest{
		RequestID: "stale-start", RouteDecision: directRouteDecision(t),
		DeliveryIntentRef: deliveryRef,
	}); !errors.Is(err, reviewtransaction.ErrRepositoryIdentityChanged) {
		t.Fatalf(
			"stale WorkRun Start error = %v, want ErrRepositoryIdentityChanged",
			err,
		)
	}
	if _, err := store.Status(); !errors.Is(
		err,
		reviewtransaction.ErrRepositoryIdentityChanged,
	) {
		t.Fatalf(
			"stale WorkRun Status error = %v, want ErrRepositoryIdentityChanged",
			err,
		)
	}
	for _, path := range []string{
		store.Dir,
		filepath.Join(
			movedGitDir,
			"gentle-ai",
			"work-runs",
			"v1",
			"repositories",
			store.storageKey,
			store.WorkRunID,
		),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("stale handle wrote WorkRun authority at %q: %v", path, err)
		}
	}
}

func TestWorkRunStoreRejectsHashShapedFactsWithoutOwnerAuthority(t *testing.T) {
	repo := initWorkRunRepository(t)
	store := openTestWorkRunStore(t, repo, "unowned")
	deliveryRef := testSHARef("fabricated-delivery")
	if _, err := store.Start(context.Background(), StartRequest{
		RequestID: "unowned-start", RouteDecision: directRouteDecision(t),
		DeliveryIntentRef: deliveryRef,
	}); err == nil {
		t.Fatal("hash-shaped but unowned delivery intent was accepted")
	}
	if _, err := store.Status(); !errors.Is(err, ErrWorkRunNotStarted) {
		t.Fatalf("failed unowned start mutated state: %v", err)
	}

	mutated := store
	mutated.Dir = filepath.Join(filepath.Dir(store.Dir), "attacker-selected")
	if mutated.RepositoryRef() != "" {
		t.Fatal("caller-mutated WorkRunStore exposed a repository reference")
	}
	registerTestDeliveryIntent(t, mutated, deliveryRef)
	if _, err := mutated.Start(context.Background(), StartRequest{
		RequestID: "mutated-path", RouteDecision: directRouteDecision(t),
		DeliveryIntentRef: deliveryRef,
	}); err == nil {
		t.Fatal("caller-mutated WorkRunStore.Dir was accepted")
	}
	if _, err := os.Lstat(mutated.Dir); !os.IsNotExist(err) {
		t.Fatalf("caller-mutated WorkRunStore.Dir was written: %v", err)
	}
}

func TestWorkRunStoreRejectsAuthoritySymlinkAndPublishesPrivateArtifacts(t *testing.T) {
	repo := initWorkRunRepository(t)
	store := openTestWorkRunStore(t, repo, "private-publication")
	sharedRoot := filepath.Join(store.commonDir, "gentle-ai")
	if err := os.MkdirAll(sharedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	workRunsRoot := filepath.Join(sharedRoot, "work-runs")
	if err := os.Symlink(outside, workRunsRoot); err == nil {
		deliveryRef := testSHARef("symlink-delivery")
		registerTestDeliveryIntent(t, store, deliveryRef)
		if _, err := store.Start(context.Background(), StartRequest{
			RequestID: "symlink-start", RouteDecision: directRouteDecision(t),
			DeliveryIntentRef: deliveryRef,
		}); err == nil {
			t.Fatal("symlinked WorkRun authority root was accepted")
		}
		if _, err := os.Lstat(filepath.Join(outside, "v1")); !os.IsNotExist(err) {
			t.Fatalf("symlink attack wrote outside authority: %v", err)
		}
		if err := os.Remove(workRunsRoot); err != nil {
			t.Fatal(err)
		}
	}

	state := startDirectWorkRun(t, store)
	if state.Revision == "" {
		t.Fatal("private WorkRun publication omitted revision")
	}
	for _, directory := range []string{
		filepath.Join(sharedRoot, "work-runs"),
		filepath.Join(sharedRoot, "work-runs", "v1"),
		filepath.Join(sharedRoot, "work-runs", "v1", "repositories"),
		store.repositoryDir,
		store.Dir,
		filepath.Join(store.Dir, "records"),
	} {
		if err := validatePrivateWorkRunDirectory(directory); err != nil {
			t.Fatalf("published directory %q is not owner-only: %v", directory, err)
		}
	}
	if err := validatePrivateWorkRunFile(filepath.Join(store.Dir, "HEAD")); err != nil {
		t.Fatalf("published HEAD is not owner-only: %v", err)
	}
	if err := validatePrivateWorkRunFile(
		filepath.Join(store.repositoryDir, "repository-binding.json"),
	); err != nil {
		t.Fatalf("published repository binding is not owner-only: %v", err)
	}
	records, err := os.ReadDir(filepath.Join(store.Dir, "records"))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("private genesis record count = %d, want 1", len(records))
	}
	if err := validatePrivateWorkRunFile(
		filepath.Join(store.Dir, "records", records[0].Name()),
	); err != nil {
		t.Fatalf("published immutable record is not owner-only: %v", err)
	}
}

func TestWorkRunStoreExactReplayConflictAndStaleCAS(t *testing.T) {
	repo := initWorkRunRepository(t)
	store := openTestWorkRunStore(t, repo, "cas")
	startRequest := StartRequest{
		RequestID: "start-1", RouteDecision: directRouteDecision(t),
		DeliveryIntentRef: testSHARef("delivery"),
	}
	registerTestDeliveryIntent(t, store, startRequest.DeliveryIntentRef)
	started, err := store.Start(context.Background(), startRequest)
	if err != nil {
		t.Fatal(err)
	}
	sddRoot := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(store.Dir))), "sdd-runtime")
	if _, err := os.Lstat(sddRoot); !os.IsNotExist(err) {
		t.Fatalf("direct work created SDD authority: %v", err)
	}
	replayed, err := store.Start(context.Background(), startRequest)
	if err != nil {
		t.Fatalf("exact replay failed: %v", err)
	}
	if replayed.Revision != started.Revision {
		t.Fatalf("exact replay revision = %q, want %q", replayed.Revision, started.Revision)
	}
	conflict := startRequest
	conflict.DeliveryIntentRef = testSHARef("other-delivery")
	registerTestDeliveryIntent(t, store, conflict.DeliveryIntentRef)
	if _, err := store.Start(context.Background(), conflict); !errors.Is(err, ErrWorkRunRequestConflict) {
		t.Fatalf("request-ID conflict error = %v", err)
	}

	handoff := testHandoff(t, ImplementationRouteDirectInline, "", "scope-a")
	applied, err := store.BindImplementationHandoff(context.Background(), BindImplementationHandoffRequest{
		ExpectedRevision: started.Revision, RequestID: "handoff-1", Handoff: handoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Revision == started.Revision {
		t.Fatal("handoff did not advance revision")
	}
	headPath := filepath.Join(store.Dir, "HEAD")
	headBeforeReplay, err := os.ReadFile(headPath)
	if err != nil {
		t.Fatal(err)
	}
	historicalStart, err := store.Start(context.Background(), startRequest)
	if err != nil {
		t.Fatalf("historical start replay failed: %v", err)
	}
	if historicalStart.Revision != started.Revision ||
		historicalStart.Handoff != nil ||
		historicalStart.Forecast != nil {
		t.Fatalf("start replay returned newer HEAD state: %#v", historicalStart)
	}
	currentAfterReplay, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if currentAfterReplay.Revision != applied.Revision ||
		currentAfterReplay.Handoff == nil {
		t.Fatalf("historical replay changed current HEAD state: %#v", currentAfterReplay)
	}
	headAfterReplay, err := os.ReadFile(headPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(headAfterReplay) != string(headBeforeReplay) {
		t.Fatalf(
			"historical replay rewrote HEAD: before %q after %q",
			headBeforeReplay,
			headAfterReplay,
		)
	}
	replayed, err = store.BindImplementationHandoff(context.Background(), BindImplementationHandoffRequest{
		ExpectedRevision: started.Revision, RequestID: "handoff-1", Handoff: handoff,
	})
	if err != nil || replayed.Revision != applied.Revision {
		t.Fatalf("handoff exact replay = %q, %v; want %q", replayed.Revision, err, applied.Revision)
	}
	changedHandoff := testHandoff(t, ImplementationRouteDirectInline, "", "scope-b")
	if _, err := store.BindImplementationHandoff(context.Background(), BindImplementationHandoffRequest{
		ExpectedRevision: started.Revision, RequestID: "handoff-1", Handoff: changedHandoff,
	}); !errors.Is(err, ErrWorkRunRequestConflict) {
		t.Fatalf("changed exact-request replay error = %v", err)
	}
	if _, err := store.BindImplementationHandoff(context.Background(), BindImplementationHandoffRequest{
		ExpectedRevision: started.Revision, RequestID: "handoff-stale", Handoff: changedHandoff,
	}); !errors.Is(err, ErrWorkRunRevisionConflict) {
		t.Fatalf("stale handoff error = %v", err)
	}
}

func TestWorkRunStoreConcurrentCASPublishesOneSuccessor(t *testing.T) {
	repo := initWorkRunRepository(t)
	store := openTestWorkRunStore(t, repo, "concurrent")
	started := startDirectWorkRun(t, store)
	requests := []BindImplementationHandoffRequest{
		{
			ExpectedRevision: started.Revision, RequestID: "handoff-a",
			Handoff: testHandoff(t, ImplementationRouteDirectInline, "", "scope-a"),
		},
		{
			ExpectedRevision: started.Revision, RequestID: "handoff-b",
			Handoff: testHandoff(t, ImplementationRouteDirectInline, "", "scope-b"),
		},
	}
	start := make(chan struct{})
	results := make(chan error, len(requests))
	var wait sync.WaitGroup
	for _, request := range requests {
		request := request
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := store.BindImplementationHandoff(context.Background(), request)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	successes := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrWorkRunRevisionConflict), errors.Is(err, ErrWorkRunConcurrentUpdate):
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful successors = %d, want exactly 1", successes)
	}
	state, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.Handoff == nil || state.NextOrdinal != 1 {
		t.Fatalf("unexpected final state: %#v", state)
	}
	records, err := os.ReadDir(filepath.Join(store.Dir, "records"))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("immutable record count = %d, want genesis + one successor", len(records))
	}
}

func TestWorkRunProposalAcceptanceBindingAndSafeReroute(t *testing.T) {
	repo := initWorkRunRepository(t)
	store := openTestWorkRunStore(t, repo, "proposal")
	proposal, err := DecideImplementationRoute(ImplementationRouteInput{
		DurablePlanning: []DurablePlanningReason{PlanningArchitecture},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposalDeliveryRef := testSHARef("delivery-proposal")
	registerTestDeliveryIntent(t, store, proposalDeliveryRef)
	state, err := store.Start(context.Background(), StartRequest{
		RequestID: "start-proposal", RouteDecision: proposal,
		DeliveryIntentRef: proposalDeliveryRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicState != PublicStateNeedsYourDecision || status.ImplementationRoute != "" {
		t.Fatalf("pending proposal leaked executable route: %#v", status)
	}
	if _, err := store.BindSDDRun(context.Background(), BindSDDRunRequest{
		ExpectedRevision: state.Revision, RequestID: "bind-too-early", SDDRunRef: "change-a",
	}); !errors.Is(err, ErrWorkRunInvalidTransition) {
		t.Fatalf("pre-acceptance SDD bind error = %v", err)
	}

	fabricatedAcceptance := testSHARef("fabricated-acceptance")
	if _, err := store.AcceptSDD(context.Background(), AcceptSDDRequest{
		ExpectedRevision: state.Revision, RequestID: "fabricated-acceptance",
		AcceptanceRef: fabricatedAcceptance,
	}); err == nil {
		t.Fatal("hash-shaped but unowned SDD acceptance was accepted")
	}
	unchanged, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != state.Revision ||
		unchanged.ImplementationRoute != "" {
		t.Fatalf("failed SDD acceptance mutated state: %#v", unchanged)
	}

	acceptanceRef := testSHARef("accepted")
	authority := testAuthorityForStore(t, store)
	authority.routes[acceptanceRef] = RouteSelectionAuthority{
		DecisionRef: acceptanceRef, PendingDecisionDigest: proposal.Digest,
		SelectedRoute: ImplementationRouteSDD,
	}
	state, err = store.AcceptSDD(context.Background(), AcceptSDDRequest{
		ExpectedRevision: state.Revision, RequestID: "accept-sdd",
		AcceptanceRef: acceptanceRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err = store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicState != PublicStateWorking || status.ImplementationRoute != "" || status.SDDRunRef != "" {
		t.Fatalf("accepted but unbound SDD route leaked publicly: %#v", status)
	}
	authority.runs["change-a"] = SDDRunAuthority{
		RunRef: "change-a", WorkRunID: store.WorkRunID,
		RouteAcceptanceRef: acceptanceRef,
	}
	state, err = store.BindSDDRun(context.Background(), BindSDDRunRequest{
		ExpectedRevision: state.Revision, RequestID: "bind-sdd", SDDRunRef: "change-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err = store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ImplementationRoute != ImplementationRouteSDD || status.SDDRunRef != "change-a" {
		t.Fatalf("bound SDD route = %#v", status)
	}
	sddHandoff := testHandoff(t, ImplementationRouteSDD, "change-a", "scope-sdd")
	if _, err := store.BindImplementationHandoff(context.Background(), BindImplementationHandoffRequest{
		ExpectedRevision: state.Revision, RequestID: "handoff-sdd", Handoff: sddHandoff,
	}); err != nil {
		t.Fatal(err)
	}

	rerouteStore := openTestWorkRunStore(t, repo, "reroute")
	rerouteDeliveryRef := testSHARef("delivery-reroute")
	registerTestDeliveryIntent(t, rerouteStore, rerouteDeliveryRef)
	pending, err := rerouteStore.Start(context.Background(), StartRequest{
		RequestID: "start-reroute", RouteDecision: proposal,
		DeliveryIntentRef: rerouteDeliveryRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	direct := directRouteDecision(t)
	rerouteRef := testSHARef("decline-and-reduce")
	rerouteAuthority := testAuthorityForStore(t, rerouteStore)
	rerouteAuthority.routes[rerouteRef] = RouteSelectionAuthority{
		DecisionRef: rerouteRef, PendingDecisionDigest: proposal.Digest,
		SelectedRoute:          ImplementationRouteDirectInline,
		SelectedDecisionDigest: direct.Digest,
	}
	rerouted, err := rerouteStore.Reroute(context.Background(), RerouteRequest{
		ExpectedRevision: pending.Revision, RequestID: "reroute-direct",
		OwnerDecisionRef: rerouteRef, RouteDecision: direct,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rerouted.RouteDecision.Digest != direct.Digest ||
		rerouted.ImplementationRoute != ImplementationRouteDirectInline {
		t.Fatalf("reroute did not install a fresh direct decision: %#v", rerouted)
	}
	if _, err := rerouteStore.BindSDDRun(context.Background(), BindSDDRunRequest{
		ExpectedRevision: rerouted.Revision, RequestID: "bind-after-reroute", SDDRunRef: "forbidden",
	}); !errors.Is(err, ErrWorkRunInvalidTransition) {
		t.Fatalf("rerouted direct run accepted SDD binding: %v", err)
	}

	explicitRef := testSHARef("explicit-sdd")
	explicitDecision, err := DecideImplementationRoute(ImplementationRouteInput{
		ExplicitSDDRequestRef: explicitRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	explicitStore := openTestWorkRunStore(t, repo, "explicit")
	explicitDeliveryRef := testSHARef("delivery-explicit")
	registerTestDeliveryIntent(t, explicitStore, explicitDeliveryRef)
	explicitAuthority := testAuthorityForStore(t, explicitStore)
	if _, err := explicitStore.Start(context.Background(), StartRequest{
		RequestID: "start-explicit-unowned", RouteDecision: explicitDecision,
		DeliveryIntentRef: explicitDeliveryRef,
	}); err == nil {
		t.Fatal("hash-shaped but unowned explicit SDD request was accepted")
	}
	if _, err := explicitStore.Status(); !errors.Is(err, ErrWorkRunNotStarted) {
		t.Fatalf("failed explicit SDD start mutated state: %v", err)
	}
	explicitAuthority.explicitSDDRequests[explicitRef] = ExplicitSDDRequestAuthority{
		AuthorityRef: explicitRef, WorkRunID: explicitStore.WorkRunID,
		DeliveryIntentRef: explicitDeliveryRef,
	}
	explicit, err := explicitStore.Start(context.Background(), StartRequest{
		RequestID: "start-explicit", RouteDecision: explicitDecision,
		DeliveryIntentRef: explicitDeliveryRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.ImplementationRoute != ImplementationRouteSDD ||
		explicit.RouteAcceptanceRef != explicitRef || explicit.SDDRunRef != "" {
		t.Fatalf("explicit SDD request did not select an unbound accepted route: %#v", explicit)
	}
}

func startDirectWorkRun(t *testing.T, store WorkRunStore) WorkRunState {
	t.Helper()
	deliveryIntentRef := testSHARef("delivery")
	registerTestDeliveryIntent(t, store, deliveryIntentRef)
	state, err := store.Start(context.Background(), StartRequest{
		RequestID: "start", RouteDecision: directRouteDecision(t),
		DeliveryIntentRef: deliveryIntentRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func directRouteDecision(t *testing.T) ImplementationRouteDecision {
	t.Helper()
	decision, err := DecideImplementationRoute(ImplementationRouteInput{
		WriteIntent: WriteIntentAtomicMechanical, WriteFileCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func testHandoff(
	t *testing.T,
	route ImplementationRoute,
	sddRunRef string,
	scopeSeed string,
) ImplementationHandoff {
	t.Helper()
	handoff, err := NewImplementationHandoff(
		route, testSHARef(scopeSeed), testVerificationSubject(),
		[]string{}, []string{}, sddRunRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	return handoff
}

func testVerificationSubject() reviewtransaction.VerificationSubject {
	return reviewtransaction.VerificationSubject{
		Kind:     reviewtransaction.TargetCurrentChanges,
		BaseTree: strings.Repeat("1", 40), CandidateTree: strings.Repeat("2", 40),
		PathsDigest: testSHARef("paths"), SnapshotIdentity: testSHARef("snapshot"),
	}
}

func openTestWorkRunStore(t *testing.T, repo, workRunID string) WorkRunStore {
	t.Helper()
	store, err := OpenWorkRunStore(context.Background(), repo, workRunID)
	if err != nil {
		t.Fatal(err)
	}
	authority := newTestAuthorityRepository()
	executor := hostruntime.NewExecutor()
	return store.WithAuthorityPorts(AuthorityPorts{
		PAD: authority, ExplicitSDDRequest: authority, Route: authority, SDD: authority,
		Verification: authority, Launch: executor,
	})
}

func initWorkRunRepository(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("work run store integration requires git")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	repo := t.TempDir()
	runWorkRunGit(t, repo, "init", "-q")
	runWorkRunGit(t, repo, "config", "user.email", "workrun@example.com")
	runWorkRunGit(t, repo, "config", "user.name", "Work Run")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runWorkRunGit(t, repo, "add", "--", "tracked.txt")
	runWorkRunGit(t, repo, "commit", "-qm", "base")
	return repo
}

func runWorkRunGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func testSHARef(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}
