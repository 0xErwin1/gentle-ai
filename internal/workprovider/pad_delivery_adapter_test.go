package workprovider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
)

var (
	errPADTestLostResponse = errors.New("test hosting response was lost")
	errPADTestUnavailable  = errors.New("test hosting endpoint is unavailable")
)

type padDeliveryTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *padDeliveryTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *padDeliveryTestClock) SetUnix(value int64) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = time.Unix(value, 0).UTC()
}

type padDeliveryTestGitAuthority struct {
	repositoryRef string
	root          string
}

func (authority *padDeliveryTestGitAuthority) RequireCommit(
	_ context.Context,
	repositoryRef string,
	revision string,
) error {
	if repositoryRef != authority.repositoryRef {
		return ErrPADRepositoryAuthorityMismatch
	}
	oid, err := padDeliveryTestOID(revision)
	if err != nil {
		return err
	}
	_, err = runPADDeliveryTestGit(
		authority.root,
		"cat-file",
		"-e",
		oid+"^{commit}",
	)
	return err
}

func (authority *padDeliveryTestGitAuthority) ObserveAncestry(
	_ context.Context,
	repositoryRef string,
	baseRevision string,
	headRevision string,
) (PADGitAncestryObservation, error) {
	if repositoryRef != authority.repositoryRef {
		return PADGitAncestryObservation{}, ErrPADRepositoryAuthorityMismatch
	}
	base, err := padDeliveryTestOID(baseRevision)
	if err != nil {
		return PADGitAncestryObservation{}, err
	}
	head, err := padDeliveryTestOID(headRevision)
	if err != nil {
		return PADGitAncestryObservation{}, err
	}
	state := PADGitAncestor
	if _, err := runPADDeliveryTestGit(
		authority.root,
		"merge-base",
		"--is-ancestor",
		base,
		head,
	); err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
			return PADGitAncestryObservation{}, err
		}
		state = PADGitNotAncestor
	}
	observation := PADGitAncestryObservation{
		RepositoryRef: repositoryRef,
		BaseRevision:  baseRevision,
		HeadRevision:  headRevision,
		State:         state,
	}
	observation.EvidenceRef = padDeliveryDigest(
		"gentle-ai.pad-test-git-ancestry/v1",
		observation,
	)
	return observation, nil
}

type padDeliveryTestHosting struct {
	mu                  sync.Mutex
	bareRepository      string
	binding             PADGitBinding
	clock               *padDeliveryTestClock
	protection          HostingProtectionState
	checks              HostingChecksState
	pullRequestState    HostingPullRequestState
	deliveryRef         string
	failCASBeforeApply  bool
	failCASAfterApply   bool
	failMergeAfterApply bool
	bindingCalls        int
	observationCalls    int
	casCalls            int
	mergeCalls          int
	lastCAS             HostingBranchCASRequest
	lastMerge           HostingPullRequestMergeRequest
}

func (hosting *padDeliveryTestHosting) ResolvePADGitBinding(
	_ context.Context,
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
) (PADGitBinding, error) {
	hosting.mu.Lock()
	defer hosting.mu.Unlock()
	hosting.bindingCalls++
	if candidate != hosting.binding.Candidate ||
		destination != hosting.binding.Destination ||
		mechanism != hosting.binding.Mechanism {
		return PADGitBinding{}, errPADTestUnavailable
	}
	return hosting.binding, nil
}

func (hosting *padDeliveryTestHosting) ObserveDelivery(
	_ context.Context,
	request HostingObservationRequest,
) (HostingDeliveryObservation, error) {
	hosting.mu.Lock()
	defer hosting.mu.Unlock()
	hosting.observationCalls++
	if request.Binding != hosting.binding {
		return HostingDeliveryObservation{}, errPADTestUnavailable
	}
	remoteRevision, err := hosting.remoteRevision(request.Binding.Destination.TargetRef)
	if err != nil {
		return HostingDeliveryObservation{}, err
	}
	observation := HostingDeliveryObservation{
		Schema:                 HostingDeliveryObservationSchema,
		Request:                request,
		CandidateRevision:      request.Binding.CandidateRevision,
		ExpectedRemoteRevision: request.Binding.ExpectedRemoteRevision,
		RemoteIdentity:         remoteRevision,
		RemoteRevision:         remoteRevision,
		ProtectionState:        hosting.protection,
		ProtectionRevision:     "protection:test-v1",
	}
	if request.Mechanism == deliveryadmission.MechanismFastForwardOnly {
		observation.PullRequestState = HostingPullRequestNotApplicable
		observation.RequiredChecksState = HostingChecksNotApplicable
		return observation, nil
	}
	observation.PullRequestRef = request.PullRequestRef
	observation.PullRequestState = hosting.pullRequestState
	observation.PullRequestHeadRevision = request.Binding.CandidateRevision
	observation.PullRequestBaseRevision = request.Binding.ExpectedRemoteRevision
	observation.RequiredChecksState = hosting.checks
	observation.RequiredChecksRevision = "checks:test-v1"
	if hosting.pullRequestState == HostingPullRequestMerged {
		observation.DeliveryRef = hosting.deliveryRef
	}
	return observation, nil
}

func (hosting *padDeliveryTestHosting) CompareAndSwapBranch(
	_ context.Context,
	request HostingBranchCASRequest,
) (HostingBranchCASReceipt, error) {
	hosting.mu.Lock()
	defer hosting.mu.Unlock()
	hosting.casCalls++
	hosting.lastCAS = request
	if request.HostingRepositoryRef != hosting.binding.HostingRepositoryRef ||
		request.RepositoryRef != hosting.binding.Destination.RepositoryRef ||
		request.TargetRef != hosting.binding.Destination.TargetRef {
		return HostingBranchCASReceipt{}, errPADTestUnavailable
	}
	if hosting.clock.Now().Unix() >= request.ExecutionExpiresAt {
		return HostingBranchCASReceipt{}, deliveryadmission.ErrExpired
	}
	if hosting.failCASBeforeApply {
		hosting.failCASBeforeApply = false
		return HostingBranchCASReceipt{}, errPADTestUnavailable
	}
	current, err := hosting.remoteRevision(request.TargetRef)
	if err != nil {
		return HostingBranchCASReceipt{}, err
	}
	if current != request.ExpectedRevision ||
		hosting.protection != HostingProtectionPermitted {
		return hosting.branchReceipt(request, HostingEffectConflict, current, ""), nil
	}
	base, _ := padDeliveryTestOID(request.ExpectedRevision)
	head, _ := padDeliveryTestOID(request.CandidateRevision)
	if _, err := runPADDeliveryTestBareGit(
		hosting.bareRepository,
		"merge-base",
		"--is-ancestor",
		base,
		head,
	); err != nil {
		return hosting.branchReceipt(request, HostingEffectRejected, current, ""), nil
	}
	if _, err := runPADDeliveryTestBareGit(
		hosting.bareRepository,
		"update-ref",
		request.TargetRef,
		head,
		base,
	); err != nil {
		current, readErr := hosting.remoteRevision(request.TargetRef)
		if readErr != nil {
			return HostingBranchCASReceipt{}, errors.Join(err, readErr)
		}
		return hosting.branchReceipt(request, HostingEffectConflict, current, ""), nil
	}
	if hosting.failCASAfterApply {
		hosting.failCASAfterApply = false
		return HostingBranchCASReceipt{}, errPADTestLostResponse
	}
	return hosting.branchReceipt(
		request,
		HostingEffectApplied,
		request.CandidateRevision,
		request.CandidateRevision,
	), nil
}

func (hosting *padDeliveryTestHosting) MergePullRequest(
	_ context.Context,
	request HostingPullRequestMergeRequest,
) (HostingPullRequestMergeReceipt, error) {
	hosting.mu.Lock()
	defer hosting.mu.Unlock()
	hosting.mergeCalls++
	hosting.lastMerge = request
	if request.HostingRepositoryRef != hosting.binding.HostingRepositoryRef ||
		request.RepositoryRef != hosting.binding.Destination.RepositoryRef ||
		request.TargetRef != hosting.binding.Destination.TargetRef ||
		request.PullRequestRef != hosting.binding.PullRequestRef {
		return HostingPullRequestMergeReceipt{}, errPADTestUnavailable
	}
	if hosting.clock.Now().Unix() >= request.ExecutionExpiresAt {
		return HostingPullRequestMergeReceipt{}, deliveryadmission.ErrExpired
	}
	current, err := hosting.remoteRevision(request.TargetRef)
	if err != nil {
		return HostingPullRequestMergeReceipt{}, err
	}
	if current != request.ExpectedBaseRevision ||
		hosting.protection != HostingProtectionPermitted ||
		hosting.checks != HostingChecksPassed ||
		hosting.pullRequestState != HostingPullRequestOpen {
		return hosting.mergeReceipt(request, HostingEffectConflict, current, "", ""), nil
	}
	base, _ := padDeliveryTestOID(request.ExpectedBaseRevision)
	head, _ := padDeliveryTestOID(request.ExpectedHeadRevision)
	if _, err := runPADDeliveryTestBareGit(
		hosting.bareRepository,
		"update-ref",
		request.TargetRef,
		head,
		base,
	); err != nil {
		current, readErr := hosting.remoteRevision(request.TargetRef)
		if readErr != nil {
			return HostingPullRequestMergeReceipt{}, errors.Join(err, readErr)
		}
		return hosting.mergeReceipt(request, HostingEffectConflict, current, "", ""), nil
	}
	hosting.pullRequestState = HostingPullRequestMerged
	hosting.deliveryRef = request.ExpectedHeadRevision
	if hosting.failMergeAfterApply {
		hosting.failMergeAfterApply = false
		return HostingPullRequestMergeReceipt{}, errPADTestLostResponse
	}
	return hosting.mergeReceipt(
		request,
		HostingEffectApplied,
		request.ExpectedHeadRevision,
		request.ExpectedHeadRevision,
		hosting.deliveryRef,
	), nil
}

func (hosting *padDeliveryTestHosting) remoteRevision(targetRef string) (string, error) {
	value, err := runPADDeliveryTestBareGit(
		hosting.bareRepository,
		"rev-parse",
		"--verify",
		targetRef+"^{commit}",
	)
	if err != nil {
		return "", err
	}
	return "git:" + value, nil
}

func (hosting *padDeliveryTestHosting) branchReceipt(
	request HostingBranchCASRequest,
	outcome HostingEffectOutcome,
	remoteRevision string,
	deliveryRef string,
) HostingBranchCASReceipt {
	return HostingBranchCASReceipt{
		Schema:           HostingBranchCASReceiptSchema,
		Request:          request,
		Outcome:          outcome,
		RemoteRevision:   remoteRevision,
		DeliveryRef:      deliveryRef,
		EvidenceRevision: padDeliveryDigest("gentle-ai.pad-test-cas/v1", request),
	}
}

func (hosting *padDeliveryTestHosting) mergeReceipt(
	request HostingPullRequestMergeRequest,
	outcome HostingEffectOutcome,
	remoteRevision string,
	mergedHeadRevision string,
	deliveryRef string,
) HostingPullRequestMergeReceipt {
	return HostingPullRequestMergeReceipt{
		Schema:             HostingPullRequestReceiptSchema,
		Request:            request,
		Outcome:            outcome,
		RemoteRevision:     remoteRevision,
		MergedHeadRevision: mergedHeadRevision,
		DeliveryRef:        deliveryRef,
		EvidenceRevision:   padDeliveryDigest("gentle-ai.pad-test-merge/v1", request),
	}
}

type padDeliveryTestFixture struct {
	authority *PADRepositoryAuthority
	adapter   *PADDeliveryAdapter
	hosting   *padDeliveryTestHosting
	clock     *padDeliveryTestClock
	base      string
	candidate string
	request   deliveryadmission.LiveGateProbeRequest
	command   deliveryadmission.ExecutionCommand
}

func TestPADDeliveryAdapterProbesEveryRouteWithoutOpeningStore(t *testing.T) {
	for _, test := range []struct {
		name      string
		route     deliveryadmission.Route
		mechanism deliveryadmission.Mechanism
	}{
		{"pr_with_issue", deliveryadmission.RoutePRWithIssue, deliveryadmission.MechanismPullRequest},
		{"pr_without_issue", deliveryadmission.RoutePRWithoutIssue, deliveryadmission.MechanismPullRequest},
		{"direct_main", deliveryadmission.RouteDirectMain, deliveryadmission.MechanismFastForwardOnly},
		{"emergency", deliveryadmission.RouteEmergency, deliveryadmission.MechanismFastForwardOnly},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPADDeliveryTestFixture(t, test.route, test.mechanism)
			storeRoot := padDeliveryTestStoreRoot(fixture.authority)
			assertPADDeliveryPathMissing(t, storeRoot)

			result, err := fixture.adapter.ProbeLiveGate(
				context.Background(),
				fixture.request,
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := result.Validate(); err != nil {
				t.Fatal(err)
			}
			if test.mechanism == deliveryadmission.MechanismPullRequest &&
				result.PullRequestRef != fixture.hosting.binding.PullRequestRef {
				t.Fatalf("pull request ref = %q", result.PullRequestRef)
			}
			if test.mechanism == deliveryadmission.MechanismFastForwardOnly &&
				!result.FastForwardSatisfied {
				t.Fatal("exact fast-forward was not satisfied")
			}
			assertPADDeliveryPathMissing(t, storeRoot)
		})
	}
}

func TestPADDeliveryAdapterExecutesOneExactCASAcrossConcurrentReplay(t *testing.T) {
	fixture := newPADDeliveryTestFixture(
		t,
		deliveryadmission.RouteDirectMain,
		deliveryadmission.MechanismFastForwardOnly,
	)
	const workers = 12
	results := make([]deliveryadmission.ExecutionResult, workers)
	errs := make([]error, workers)
	var wait sync.WaitGroup
	for index := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index], errs[index] = fixture.adapter.ExecuteOnce(
				context.Background(),
				fixture.command,
			)
		}()
	}
	wait.Wait()
	for index := range workers {
		if errs[index] != nil {
			t.Fatalf("worker %d: %v", index, errs[index])
		}
		if results[index] != results[0] {
			t.Fatalf("worker %d observed another terminal result", index)
		}
	}
	if results[0].Outcome != deliveryadmission.ExecutionSucceeded ||
		results[0].DeliveryRef != fixture.candidate {
		t.Fatalf("terminal result = %+v", results[0])
	}
	fixture.hosting.mu.Lock()
	casCalls := fixture.hosting.casCalls
	lastCAS := fixture.hosting.lastCAS
	fixture.hosting.mu.Unlock()
	if casCalls != 1 {
		t.Fatalf("CAS calls = %d, want 1", casCalls)
	}
	if lastCAS.ExpectedRevision != fixture.base ||
		lastCAS.CandidateRevision != fixture.candidate {
		t.Fatalf("CAS did not bind exact revisions: %+v", lastCAS)
	}
	assertPADDeliveryRemote(t, fixture.hosting, fixture.candidate)

	commandRef, err := fixture.command.Ref()
	if err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(
		padDeliveryTestStoreRoot(fixture.authority),
		"results",
		strings.TrimPrefix(commandRef, "sha256:")+".json",
	)
	if _, err := os.Stat(resultPath); err != nil {
		t.Fatalf("content-addressed terminal result: %v", err)
	}
}

func TestPADDeliveryAdapterRecoversLostEffectAndReplaysAfterExpiry(t *testing.T) {
	fixture := newPADDeliveryTestFixture(
		t,
		deliveryadmission.RouteEmergency,
		deliveryadmission.MechanismFastForwardOnly,
	)
	fixture.hosting.failCASAfterApply = true
	if _, err := fixture.adapter.ExecuteOnce(
		context.Background(),
		fixture.command,
	); !errors.Is(err, errPADTestLostResponse) {
		t.Fatalf("lost response = %v", err)
	}
	assertPADDeliveryRemote(t, fixture.hosting, fixture.candidate)

	fixture.clock.SetUnix(fixture.command.ExecutionExpiresAt + 10)
	recovered, err := fixture.adapter.ExecuteOnce(
		context.Background(),
		fixture.command,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Outcome != deliveryadmission.ExecutionSucceeded ||
		recovered.DeliveryRef != fixture.candidate {
		t.Fatalf("recovered result = %+v", recovered)
	}
	fixture.hosting.mu.Lock()
	casCalls := fixture.hosting.casCalls
	observationsBeforeReplay := fixture.hosting.observationCalls
	fixture.hosting.mu.Unlock()
	if casCalls != 1 {
		t.Fatalf("CAS calls after recovery = %d, want 1", casCalls)
	}

	replayed, err := fixture.adapter.ExecuteOnce(
		context.Background(),
		fixture.command,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != recovered {
		t.Fatal("post-expiry replay did not return the durable terminal result")
	}
	fixture.hosting.mu.Lock()
	observationsAfterReplay := fixture.hosting.observationCalls
	fixture.hosting.mu.Unlock()
	if observationsAfterReplay != observationsBeforeReplay {
		t.Fatal("terminal replay touched live hosting state")
	}
}

func TestPADDeliveryAdapterRejectsExpiredFirstEffectBeforeObservation(t *testing.T) {
	fixture := newPADDeliveryTestFixture(
		t,
		deliveryadmission.RouteDirectMain,
		deliveryadmission.MechanismFastForwardOnly,
	)
	fixture.clock.SetUnix(fixture.command.ExecutionExpiresAt)
	if _, err := fixture.adapter.ExecuteOnce(
		context.Background(),
		fixture.command,
	); !errors.Is(err, deliveryadmission.ErrExpired) {
		t.Fatalf("expired first effect = %v", err)
	}
	fixture.hosting.mu.Lock()
	observations := fixture.hosting.observationCalls
	casCalls := fixture.hosting.casCalls
	fixture.hosting.mu.Unlock()
	if observations != 0 || casCalls != 0 {
		t.Fatalf("expired execution observed/effected hosting: %d/%d", observations, casCalls)
	}
}

func TestPADDeliveryAdapterMergesOnlyExactBoundPullRequest(t *testing.T) {
	for _, route := range []deliveryadmission.Route{
		deliveryadmission.RoutePRWithIssue,
		deliveryadmission.RoutePRWithoutIssue,
		deliveryadmission.RouteEmergency,
	} {
		t.Run(string(route), func(t *testing.T) {
			fixture := newPADDeliveryTestFixture(
				t,
				route,
				deliveryadmission.MechanismPullRequest,
			)
			result, err := fixture.adapter.ExecuteOnce(
				context.Background(),
				fixture.command,
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != deliveryadmission.ExecutionSucceeded {
				t.Fatalf("merge result = %+v", result)
			}
			fixture.hosting.mu.Lock()
			mergeCalls := fixture.hosting.mergeCalls
			lastMerge := fixture.hosting.lastMerge
			fixture.hosting.mu.Unlock()
			if mergeCalls != 1 ||
				lastMerge.PullRequestRef != fixture.command.PullRequestRef ||
				lastMerge.ExpectedBaseRevision != fixture.base ||
				lastMerge.ExpectedHeadRevision != fixture.candidate {
				t.Fatalf("exact PR merge request = %+v, calls %d", lastMerge, mergeCalls)
			}
			assertPADDeliveryRemote(t, fixture.hosting, fixture.candidate)
		})
	}
}

func TestPADDeliveryAdapterUsesCurrentProtectionAndChecks(t *testing.T) {
	for _, test := range []struct {
		name      string
		route     deliveryadmission.Route
		mechanism deliveryadmission.Mechanism
		mutate    func(*padDeliveryTestHosting)
	}{
		{
			name:      "protection_denies_direct",
			route:     deliveryadmission.RouteDirectMain,
			mechanism: deliveryadmission.MechanismFastForwardOnly,
			mutate: func(hosting *padDeliveryTestHosting) {
				hosting.protection = HostingProtectionDenied
			},
		},
		{
			name:      "required_checks_are_pending",
			route:     deliveryadmission.RoutePRWithIssue,
			mechanism: deliveryadmission.MechanismPullRequest,
			mutate: func(hosting *padDeliveryTestHosting) {
				hosting.checks = HostingChecksPending
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPADDeliveryTestFixture(t, test.route, test.mechanism)
			fixture.hosting.mu.Lock()
			test.mutate(fixture.hosting)
			fixture.hosting.mu.Unlock()
			if _, err := fixture.adapter.ExecuteOnce(
				context.Background(),
				fixture.command,
			); !errors.Is(err, ErrPADDeliveryConflict) {
				t.Fatalf("current hosting denial = %v", err)
			}
			fixture.hosting.mu.Lock()
			casCalls := fixture.hosting.casCalls
			mergeCalls := fixture.hosting.mergeCalls
			fixture.hosting.mu.Unlock()
			if casCalls != 0 || mergeCalls != 0 {
				t.Fatalf("denied delivery effects = CAS %d, merge %d", casCalls, mergeCalls)
			}
		})
	}
}

func TestPADDeliveryAdapterPersistsIndeterminateBindingDrift(t *testing.T) {
	fixture := newPADDeliveryTestFixture(
		t,
		deliveryadmission.RouteDirectMain,
		deliveryadmission.MechanismFastForwardOnly,
	)
	fixture.hosting.failCASBeforeApply = true
	if _, err := fixture.adapter.ExecuteOnce(
		context.Background(),
		fixture.command,
	); !errors.Is(err, errPADTestUnavailable) {
		t.Fatalf("first claimed attempt = %v", err)
	}
	fixture.hosting.mu.Lock()
	fixture.hosting.binding.HostingRepositoryRef = "github:owner/another-repository"
	fixture.hosting.mu.Unlock()
	if _, err := fixture.adapter.ExecuteOnce(
		context.Background(),
		fixture.command,
	); !errors.Is(err, ErrPADDeliveryIndeterminate) {
		t.Fatalf("binding drift = %v", err)
	}
	fixture.hosting.mu.Lock()
	observations := fixture.hosting.observationCalls
	fixture.hosting.mu.Unlock()
	if _, err := fixture.adapter.ExecuteOnce(
		context.Background(),
		fixture.command,
	); !errors.Is(err, ErrPADDeliveryIndeterminate) {
		t.Fatalf("durable indeterminate replay = %v", err)
	}
	fixture.hosting.mu.Lock()
	replayedObservations := fixture.hosting.observationCalls
	fixture.hosting.mu.Unlock()
	if replayedObservations != observations {
		t.Fatal("durable indeterminate replay touched hosting state")
	}
}

func TestPADDeliveryAdapterNeverRetriesClaimedUnknownEffect(t *testing.T) {
	fixture := newPADDeliveryTestFixture(
		t,
		deliveryadmission.RouteDirectMain,
		deliveryadmission.MechanismFastForwardOnly,
	)
	fixture.hosting.failCASBeforeApply = true
	if _, err := fixture.adapter.ExecuteOnce(
		context.Background(),
		fixture.command,
	); !errors.Is(err, errPADTestUnavailable) {
		t.Fatalf("first unknown effect = %v", err)
	}
	if _, err := fixture.adapter.ExecuteOnce(
		context.Background(),
		fixture.command,
	); !errors.Is(err, ErrPADDeliveryIndeterminate) {
		t.Fatalf("claimed unknown effect replay = %v", err)
	}
	fixture.hosting.mu.Lock()
	casCalls := fixture.hosting.casCalls
	fixture.hosting.mu.Unlock()
	if casCalls != 1 {
		t.Fatalf("unknown claimed effect was retried %d times", casCalls)
	}
	assertPADDeliveryRemote(t, fixture.hosting, fixture.base)
}

func newPADDeliveryTestFixture(
	t *testing.T,
	route deliveryadmission.Route,
	mechanism deliveryadmission.Mechanism,
) padDeliveryTestFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("real Git PAD delivery integration test")
	}
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	bareRepository := filepath.Join(root, "remote.git")
	mustRunPADDeliveryTestGit(t, "", "init", "--quiet", repository)
	mustRunPADDeliveryTestGit(t, repository, "config", "user.name", "PAD Test")
	mustRunPADDeliveryTestGit(
		t,
		repository,
		"config",
		"user.email",
		"pad@example.test",
	)
	path := filepath.Join(repository, "delivery.txt")
	if err := os.WriteFile(path, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunPADDeliveryTestGit(t, repository, "add", "delivery.txt")
	mustRunPADDeliveryTestGit(t, repository, "commit", "--quiet", "-m", "base")
	mustRunPADDeliveryTestGit(t, repository, "branch", "-M", "main")
	base := "git:" + mustRunPADDeliveryTestGit(t, repository, "rev-parse", "HEAD")
	mustRunPADDeliveryTestGit(t, "", "init", "--bare", "--quiet", bareRepository)
	mustRunPADDeliveryTestGit(t, repository, "remote", "add", "origin", bareRepository)
	mustRunPADDeliveryTestGit(
		t,
		repository,
		"push",
		"--quiet",
		"origin",
		"main:refs/heads/main",
	)
	if err := os.WriteFile(path, []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunPADDeliveryTestGit(t, repository, "add", "delivery.txt")
	mustRunPADDeliveryTestGit(t, repository, "commit", "--quiet", "-m", "candidate")
	candidateRevision := "git:" + mustRunPADDeliveryTestGit(
		t,
		repository,
		"rev-parse",
		"HEAD",
	)
	mustRunPADDeliveryTestGit(
		t,
		repository,
		"push",
		"--quiet",
		"origin",
		"HEAD:refs/heads/candidate",
	)

	authority, err := NewPADRepositoryAuthority(context.Background(), repository)
	if err != nil {
		t.Fatal(err)
	}
	candidate := deliveryadmission.CandidateBinding{
		Ref:    "candidate:owner-verified",
		Digest: padDeliveryTestRef("candidate-snapshot"),
	}
	destination := deliveryadmission.DestinationBinding{
		RepositoryRef:    authority.RepositoryRef(),
		TargetRef:        "refs/heads/main",
		ObservedRevision: base,
		DefaultBranch:    true,
	}
	policy, err := deliveryadmission.NewRoutePolicy(
		"policy:pad-delivery-test",
		"revision:1",
		route,
		true,
		false,
		false,
		120,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyRef, err := policy.Ref()
	if err != nil {
		t.Fatal(err)
	}
	pullRequestRef := ""
	if mechanism == deliveryadmission.MechanismPullRequest {
		pullRequestRef = "pr:42"
	}
	binding := PADGitBinding{
		Schema:                 PADGitBindingSchema,
		Candidate:              candidate,
		Destination:            destination,
		Mechanism:              mechanism,
		HostingRepositoryRef:   "github:owner/repository",
		CandidateRevision:      candidateRevision,
		ExpectedRemoteRevision: base,
		PullRequestRef:         pullRequestRef,
	}
	clock := &padDeliveryTestClock{
		now: time.Unix(1_800_000_000, 0).UTC(),
	}
	hostingTransport := &padDeliveryTestHosting{
		bareRepository:   bareRepository,
		binding:          binding,
		clock:            clock,
		protection:       HostingProtectionPermitted,
		checks:           HostingChecksPassed,
		pullRequestState: HostingPullRequestOpen,
	}
	bindings, err := NewTransportPADGitBindingAuthority(hostingTransport)
	if err != nil {
		t.Fatal(err)
	}
	hosting, err := NewTransportHostingAuthority(hostingTransport)
	if err != nil {
		t.Fatal(err)
	}
	gitAuthority := &padDeliveryTestGitAuthority{
		repositoryRef: authority.RepositoryRef(),
		root:          repository,
	}
	adapter, err := NewPADDeliveryAdapter(
		context.Background(),
		authority,
		bindings,
		gitAuthority,
		hosting,
	)
	if err != nil {
		t.Fatal(err)
	}
	adapter.now = clock.Now
	request, err := deliveryadmission.NewLiveGateProbeRequest(
		route,
		candidate,
		destination,
		policyRef,
		policy.Binding(),
		mechanism,
	)
	if err != nil {
		t.Fatal(err)
	}
	command := deliveryadmission.ExecutionCommand{
		Schema:                 deliveryadmission.ExecutionCommandContractV1,
		AuthorizationRef:       padDeliveryTestRef("authorization"),
		AuthorizationKind:      deliveryadmission.AuthorizationNormal,
		DecisionRef:            padDeliveryTestRef("decision"),
		IntentRef:              padDeliveryTestRef("intent"),
		Route:                  route,
		Candidate:              candidate,
		Destination:            destination,
		PolicyRef:              policyRef,
		Policy:                 policy.Binding(),
		GateRef:                padDeliveryTestRef("gate"),
		LiveGateRef:            padDeliveryTestRef("live-gate"),
		ExecutionExpiresAt:     clock.Now().Unix() + 120,
		Mechanism:              mechanism,
		ProtectionMode:         deliveryadmission.ProtectionRespect,
		PullRequestRef:         pullRequestRef,
		ExpectedRemoteRevision: base,
	}
	if err := command.Validate(); err != nil {
		t.Fatal(err)
	}
	return padDeliveryTestFixture{
		authority: authority,
		adapter:   adapter,
		hosting:   hostingTransport,
		clock:     clock,
		base:      base,
		candidate: candidateRevision,
		request:   request,
		command:   command,
	}
}

func padDeliveryTestStoreRoot(authority *PADRepositoryAuthority) string {
	return filepath.Join(
		authority.identity.gitCommonDir,
		"gentle-ai",
		"work-provider",
		"pad-delivery",
		padDeliveryStoreVersion,
		"repositories",
		authority.identity.lease.StorageKey(),
	)
}

func assertPADDeliveryRemote(
	t *testing.T,
	hosting *padDeliveryTestHosting,
	want string,
) {
	t.Helper()
	hosting.mu.Lock()
	defer hosting.mu.Unlock()
	got, err := hosting.remoteRevision(hosting.binding.Destination.TargetRef)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("remote revision = %q, want %q", got, want)
	}
}

func assertPADDeliveryPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s unexpectedly exists: %v", path, err)
	}
}

func padDeliveryTestRef(seed string) string {
	return padDeliveryDigest("gentle-ai.pad-delivery-test-ref/v1", seed)
}

func padDeliveryTestOID(revision string) (string, error) {
	matches := padGitRevisionPattern.FindStringSubmatch(revision)
	if len(matches) != 2 {
		return "", fmt.Errorf("invalid test Git revision %q", revision)
	}
	return matches[1], nil
}

func mustRunPADDeliveryTestGit(
	t *testing.T,
	repository string,
	arguments ...string,
) string {
	t.Helper()
	output, err := runPADDeliveryTestGit(repository, arguments...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(arguments, " "), err)
	}
	return output
}

func runPADDeliveryTestGit(
	repository string,
	arguments ...string,
) (string, error) {
	commandArguments := make([]string, 0, len(arguments)+2)
	if repository != "" {
		commandArguments = append(commandArguments, "-C", repository)
	}
	commandArguments = append(commandArguments, arguments...)
	command := exec.Command("git", commandArguments...)
	command.Env = append(
		os.Environ(),
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func runPADDeliveryTestBareGit(
	repository string,
	arguments ...string,
) (string, error) {
	commandArguments := append([]string{"--git-dir=" + repository}, arguments...)
	return runPADDeliveryTestGit("", commandArguments...)
}
