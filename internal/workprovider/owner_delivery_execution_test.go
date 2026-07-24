package workprovider

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type ownerUnavailablePADDeliveryPorts struct{}

func (ownerUnavailablePADDeliveryPorts) ResolvePADGitBinding(
	context.Context,
	deliveryadmission.CandidateBinding,
	deliveryadmission.DestinationBinding,
	deliveryadmission.Mechanism,
) (PADGitBinding, error) {
	return PADGitBinding{}, ErrPADHostingUnavailable
}

func (ownerUnavailablePADDeliveryPorts) RequireCommit(
	context.Context,
	string,
	string,
) error {
	return ErrPADHostingUnavailable
}

func (ownerUnavailablePADDeliveryPorts) ObserveAncestry(
	context.Context,
	string,
	string,
	string,
) (PADGitAncestryObservation, error) {
	return PADGitAncestryObservation{}, ErrPADHostingUnavailable
}

func (ownerUnavailablePADDeliveryPorts) ObserveDelivery(
	context.Context,
	HostingObservationRequest,
) (HostingDeliveryObservation, error) {
	return HostingDeliveryObservation{}, ErrPADHostingUnavailable
}

func (ownerUnavailablePADDeliveryPorts) CompareAndSwapBranch(
	context.Context,
	HostingBranchCASRequest,
) (HostingBranchCASReceipt, error) {
	return HostingBranchCASReceipt{}, ErrPADHostingUnavailable
}

func (ownerUnavailablePADDeliveryPorts) MergePullRequest(
	context.Context,
	HostingPullRequestMergeRequest,
) (HostingPullRequestMergeReceipt, error) {
	return HostingPullRequestMergeReceipt{}, ErrPADHostingUnavailable
}

func newOwnerUnavailablePADDeliveryAdapter(
	t *testing.T,
	authority *PADRepositoryAuthority,
) *PADDeliveryAdapter {
	t.Helper()
	ports := ownerUnavailablePADDeliveryPorts{}
	adapter, err := NewPADDeliveryAdapter(
		context.Background(),
		authority,
		ports,
		ports,
		ports,
	)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func TestOwnerCoordinatorRequiresOneSameRepositoryPADDeliveryAdapter(
	t *testing.T,
) {
	left := newOwnerCoordinatorFixture(t, "bound-delivery-left")
	right := newOwnerCoordinatorFixture(t, "bound-delivery-right")
	dependencies := OwnerCoordinatorDependencies{
		WorkRun:         left.coordinator.work,
		Coordination:    left.coordinator.coordination,
		RAR:             left.coordinator.rar,
		Transitions:     left.coordinator.transitions,
		Evidence:        left.coordinator.evidence,
		Mutations:       left.coordinator.mutations,
		PADAuthority:    left.coordinator.pad,
		SDDAuthority:    left.coordinator.sdd,
		LaunchAuthority: ownerTestLaunchAuthority{},
		Activation:      StaticActivationResolver{Mode: ActivationEnabled},
	}
	if _, err := NewOwnerCoordinator(
		context.Background(),
		dependencies,
	); err == nil {
		t.Fatal("owner coordinator accepted an absent PAD delivery adapter")
	}

	dependencies.PADDelivery = right.coordinator.padDelivery
	if _, err := NewOwnerCoordinator(
		context.Background(),
		dependencies,
	); err == nil ||
		!strings.Contains(err.Error(), "different repository identities") {
		t.Fatalf("cross-repository PAD delivery composition = %v", err)
	}

	dependencies.PADDelivery = left.coordinator.padDelivery
	if _, err := NewOwnerCoordinator(
		context.Background(),
		dependencies,
	); err != nil {
		t.Fatalf("same-authority PAD delivery composition = %v", err)
	}
}

func TestOwnerCoordinatorRequiresCompleteSameRepositoryCandidateBindingAuthorities(
	t *testing.T,
) {
	left := newOwnerCoordinatorFixture(t, "candidate-authority-left")
	right := newOwnerCoordinatorFixture(t, "candidate-authority-right")
	leftObjects, err := NewFixedPADGitObjectAuthority(
		context.Background(),
		left.coordinator.pad.authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	leftCatalog, err := NewPADCandidateCatalog(
		context.Background(),
		left.coordinator.pad.authority,
		leftObjects,
	)
	if err != nil {
		t.Fatal(err)
	}
	rightObjects, err := NewFixedPADGitObjectAuthority(
		context.Background(),
		right.coordinator.pad.authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	rightCatalog, err := NewPADCandidateCatalog(
		context.Background(),
		right.coordinator.pad.authority,
		rightObjects,
	)
	if err != nil {
		t.Fatal(err)
	}
	dependencies := OwnerCoordinatorDependencies{
		WorkRun:         left.coordinator.work,
		Coordination:    left.coordinator.coordination,
		RAR:             left.coordinator.rar,
		Transitions:     left.coordinator.transitions,
		Evidence:        left.coordinator.evidence,
		Mutations:       left.coordinator.mutations,
		PADAuthority:    left.coordinator.pad,
		PADDelivery:     left.coordinator.padDelivery,
		SDDAuthority:    left.coordinator.sdd,
		LaunchAuthority: ownerTestLaunchAuthority{},
		Activation:      StaticActivationResolver{Mode: ActivationEnabled},
	}
	dependencies.PADCandidateCatalog = leftCatalog
	if _, err := NewOwnerCoordinator(
		context.Background(),
		dependencies,
	); err == nil || !strings.Contains(err.Error(), "both PAD candidate") {
		t.Fatalf("partial candidate binding composition = %v", err)
	}

	dependencies.PADGitBindingAuthority = ownerUnavailablePADDeliveryPorts{}
	if _, err := NewOwnerCoordinator(
		context.Background(),
		dependencies,
	); err != nil {
		t.Fatalf("complete candidate binding composition = %v", err)
	}

	dependencies.PADCandidateCatalog = rightCatalog
	if _, err := NewOwnerCoordinator(
		context.Background(),
		dependencies,
	); err == nil ||
		!strings.Contains(err.Error(), "different repository identities") {
		t.Fatalf("foreign candidate catalog composition = %v", err)
	}
}

func TestOwnerCoordinatorExecuteBoundDeliveryRequiresAuthorization(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "bound-delivery-no-auth")
	admission := fixture.admitDefaultDelivery(t, "bound-delivery-no-auth")
	terminal := ownerPrepareAuthorizationTerminal(
		t,
		fixture,
		admission,
		"bound-delivery-no-auth",
		reviewtransaction.VerificationAggregateNotRequired,
		false,
	)
	if terminal.state.DeliveryAuthorizationRef != "" {
		t.Fatal("terminal fixture unexpectedly carries delivery authorization")
	}
	if _, err := fixture.coordinator.ExecuteBoundDelivery(
		context.Background(),
	); !errors.Is(err, workrun.ErrWorkRunInvalidTransition) {
		t.Fatalf("delivery without authorization = %v", err)
	}
}

func TestOwnerCoordinatorRejectsMismatchedConnectorBindingBeforeAuthorization(
	t *testing.T,
) {
	newOwnerBoundDeliveryFixtureWithConnectorBinding(
		t,
		"bound-delivery-mismatched-connector",
		reviewtransaction.VerificationAggregateNotRequired,
		true,
	)
}

func TestOwnerCoordinatorExecutesNormalAndExceptionBoundDelivery(
	t *testing.T,
) {
	for _, test := range []struct {
		name      string
		aggregate reviewtransaction.VerificationAggregate
		kind      deliveryadmission.AuthorizationKind
	}{
		{
			name:      "normal",
			aggregate: reviewtransaction.VerificationAggregateNotRequired,
			kind:      deliveryadmission.AuthorizationNormal,
		},
		{
			name:      "exception",
			aggregate: reviewtransaction.VerificationAggregateUnavailable,
			kind:      deliveryadmission.AuthorizationException,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOwnerBoundDeliveryFixture(
				t,
				"bound-delivery-"+test.name,
				test.aggregate,
			)
			if fixture.kind != test.kind {
				t.Fatalf(
					"derived authorization kind = %q, want %q",
					fixture.kind,
					test.kind,
				)
			}
			result, err := fixture.owner.coordinator.ExecuteBoundDelivery(
				context.Background(),
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != deliveryadmission.ExecutionSucceeded ||
				result.AuthorizationRef != fixture.authorizationRef ||
				result.DeliveryRef != fixture.candidateRevision {
				t.Fatalf("bound delivery result = %#v", result)
			}
			fixture.hosting.mu.Lock()
			mergeCalls := fixture.hosting.mergeCalls
			fixture.hosting.mu.Unlock()
			if mergeCalls != 1 {
				t.Fatalf("merge calls = %d, want 1", mergeCalls)
			}
		})
	}
}

func TestOwnerCoordinatorExecuteBoundDeliveryReplaysAfterExpiry(
	t *testing.T,
) {
	fixture := newOwnerBoundDeliveryFixture(
		t,
		"bound-delivery-replay",
		reviewtransaction.VerificationAggregateNotRequired,
	)
	first, err := fixture.owner.coordinator.ExecuteBoundDelivery(
		context.Background(),
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.SetUnix(fixture.authorizationExpiresAt + 60)
	fixture.hosting.mu.Lock()
	observationsBefore := fixture.hosting.observationCalls
	fixture.hosting.mu.Unlock()

	second, err := fixture.owner.coordinator.ExecuteBoundDelivery(
		context.Background(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("terminal delivery replay changed:\nfirst  %#v\nsecond %#v", first, second)
	}
	fixture.hosting.mu.Lock()
	observationsAfter := fixture.hosting.observationCalls
	mergeCalls := fixture.hosting.mergeCalls
	fixture.hosting.mu.Unlock()
	if observationsAfter != observationsBefore || mergeCalls != 1 {
		t.Fatalf(
			"terminal replay touched hosting: observations %d -> %d, merges %d",
			observationsBefore,
			observationsAfter,
			mergeCalls,
		)
	}
}

func TestOwnerCoordinatorExecuteBoundDeliveryRejectsExpiredFirstUse(
	t *testing.T,
) {
	fixture := newOwnerBoundDeliveryFixture(
		t,
		"bound-delivery-expired",
		reviewtransaction.VerificationAggregateNotRequired,
	)
	fixture.clock.SetUnix(fixture.authorizationExpiresAt)
	if _, err := fixture.owner.coordinator.ExecuteBoundDelivery(
		context.Background(),
	); !errors.Is(err, deliveryadmission.ErrExpired) {
		t.Fatalf("expired bound delivery = %v", err)
	}
	fixture.hosting.mu.Lock()
	mergeCalls := fixture.hosting.mergeCalls
	fixture.hosting.mu.Unlock()
	if mergeCalls != 0 {
		t.Fatalf("expired authorization caused %d merge effects", mergeCalls)
	}
}

func TestOwnerCoordinatorExecuteBoundDeliveryConcurrentReplayHasOneEffect(
	t *testing.T,
) {
	fixture := newOwnerBoundDeliveryFixture(
		t,
		"bound-delivery-concurrent",
		reviewtransaction.VerificationAggregateNotRequired,
	)
	const workers = 12
	results := make([]deliveryadmission.ExecutionResult, workers)
	errs := make([]error, workers)
	var wait sync.WaitGroup
	for index := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index], errs[index] =
				fixture.owner.coordinator.ExecuteBoundDelivery(
					context.Background(),
				)
		}()
	}
	wait.Wait()
	for index := range workers {
		if errs[index] != nil {
			t.Fatalf("worker %d: %v", index, errs[index])
		}
		if results[index] != results[0] {
			t.Fatalf("worker %d observed a different terminal result", index)
		}
	}
	fixture.hosting.mu.Lock()
	mergeCalls := fixture.hosting.mergeCalls
	fixture.hosting.mu.Unlock()
	if mergeCalls != 1 {
		t.Fatalf("concurrent bound delivery effects = %d, want 1", mergeCalls)
	}
}

type ownerDeliveryActivationSequence struct {
	mu    sync.Mutex
	modes []ActivationMode
}

func (resolver *ownerDeliveryActivationSequence) ResolveActivation(
	_ context.Context,
	_ string,
) (ActivationMode, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if len(resolver.modes) == 0 {
		return ActivationDisabled, nil
	}
	mode := resolver.modes[0]
	resolver.modes = resolver.modes[1:]
	return mode, nil
}

func TestOwnerCoordinatorExecuteBoundDeliveryRechecksKillSwitch(
	t *testing.T,
) {
	fixture := newOwnerBoundDeliveryFixture(
		t,
		"bound-delivery-kill-switch",
		reviewtransaction.VerificationAggregateNotRequired,
	)
	fixture.owner.coordinator.activation = &ownerDeliveryActivationSequence{
		modes: []ActivationMode{
			ActivationEnabled,
			ActivationDisabled,
		},
	}
	if _, err := fixture.owner.coordinator.ExecuteBoundDelivery(
		context.Background(),
	); !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("disabled bound delivery = %v", err)
	}
	fixture.hosting.mu.Lock()
	observations := fixture.hosting.observationCalls
	mergeCalls := fixture.hosting.mergeCalls
	fixture.hosting.mu.Unlock()
	if observations != 0 || mergeCalls != 0 {
		t.Fatalf(
			"kill switch allowed hosting access: observations %d, merges %d",
			observations,
			mergeCalls,
		)
	}
}

type ownerBoundDeliveryFixture struct {
	owner                  ownerCoordinatorFixture
	hosting                *padDeliveryTestHosting
	clock                  *padDeliveryTestClock
	kind                   deliveryadmission.AuthorizationKind
	authorizationRef       string
	authorizationExpiresAt int64
	candidateRevision      string
}

func newOwnerBoundDeliveryFixture(
	t *testing.T,
	seed string,
	aggregate reviewtransaction.VerificationAggregate,
) ownerBoundDeliveryFixture {
	t.Helper()
	return newOwnerBoundDeliveryFixtureWithConnectorBinding(
		t,
		seed,
		aggregate,
		false,
	)
}

func newOwnerBoundDeliveryFixtureWithConnectorBinding(
	t *testing.T,
	seed string,
	aggregate reviewtransaction.VerificationAggregate,
	mismatchedBinding bool,
) ownerBoundDeliveryFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("real Git owner delivery integration test")
	}
	ctx := context.Background()
	owner := newOwnerCoordinatorFixture(t, seed)
	ownerGit(t, owner.repo, "branch", "-M", "main")
	baseRevision := "git:" + strings.TrimSpace(
		ownerGit(t, owner.repo, "rev-parse", "HEAD"),
	)
	bareRepository := filepath.Join(t.TempDir(), "remote.git")
	ownerGit(
		t,
		owner.repo,
		"init",
		"--bare",
		"--quiet",
		bareRepository,
	)
	ownerGit(t, owner.repo, "remote", "add", "origin", bareRepository)
	ownerGit(
		t,
		owner.repo,
		"push",
		"--quiet",
		"origin",
		"main:refs/heads/main",
	)

	allowIncomplete :=
		aggregate == reviewtransaction.VerificationAggregateUnavailable
	admission := ownerAdmitBoundDelivery(
		t,
		owner,
		seed,
		baseRevision,
		allowIncomplete,
	)
	terminal := ownerPrepareAuthorizationTerminal(
		t,
		owner,
		admission,
		seed,
		aggregate,
		false,
	)
	ownerGit(t, owner.repo, "add", "-A")
	ownerGit(t, owner.repo, "commit", "--quiet", "-m", "candidate")
	candidateRevision := "git:" + strings.TrimSpace(
		ownerGit(t, owner.repo, "rev-parse", "HEAD"),
	)
	ownerGit(
		t,
		owner.repo,
		"push",
		"--quiet",
		"origin",
		"HEAD:refs/heads/candidate",
	)

	candidate := deliveryadmission.CandidateBinding{
		Ref:    "candidate:" + seed,
		Digest: terminal.authority.Result.Subject.SnapshotIdentity,
	}
	pullRequestRef := "github:pr/" + seed
	gates := deliveryadmission.GateEvidence{
		Schema:             deliveryadmission.GateEvidenceContractV1,
		Route:              admission.Intent.Route,
		Candidate:          candidate,
		Destination:        admission.Intent.Destination,
		PolicyRef:          admission.PolicyRef,
		Policy:             admission.Policy.Binding(),
		Mechanism:          deliveryadmission.MechanismPullRequest,
		ProtectionMode:     deliveryadmission.ProtectionRespect,
		PullRequestRef:     pullRequestRef,
		RequiredChecksRef:  ownerTestRef(seed + "-required-checks"),
		RemoteFreshnessRef: ownerTestRef(seed + "-remote-freshness"),
		ProtectionRef:      ownerTestRef(seed + "-protection"),
		ObservedAt:         owner.now - 1,
		ExpiresAt:          owner.now + 300,
	}
	gateRef, err := owner.pad.PublishGateEvidence(ctx, gates)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := deliveryadmission.Decide(
		ctx,
		owner.pad,
		owner.coordinator.rar,
		deliveryadmission.DeliveryRequest{
			AdmissionDecisionRef:  admission.Admission.AdmissionDecisionRef,
			PolicyRef:             admission.PolicyRef,
			ReviewReceiptRef:      terminal.authority.Receipt.ReceiptRef,
			VerificationResultRef: terminal.authority.Result.ResultRef,
			GateRef:               gateRef,
			AuthorityRef:          admission.AuthorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	decisionRef, err := owner.pad.PublishDeliveryDecision(ctx, decision)
	if err != nil {
		t.Fatal(err)
	}
	binding := PADGitBinding{
		Schema:                 PADGitBindingSchema,
		Candidate:              candidate,
		Destination:            admission.Intent.Destination,
		Mechanism:              deliveryadmission.MechanismPullRequest,
		HostingRepositoryRef:   "github:owner/repository",
		CandidateRevision:      candidateRevision,
		ExpectedRemoteRevision: baseRevision,
		PullRequestRef:         pullRequestRef,
	}
	if mismatchedBinding {
		binding.CandidateRevision = "not-a-git-revision"
	}
	clock := &padDeliveryTestClock{
		now: time.Unix(owner.now, 0).UTC(),
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
	objects, err := NewFixedPADGitObjectAuthority(
		ctx,
		owner.coordinator.pad.authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewPADCandidateCatalog(
		ctx,
		owner.coordinator.pad.authority,
		objects,
	)
	if err != nil {
		t.Fatal(err)
	}
	hosting, err := NewTransportHostingAuthority(hostingTransport)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewPADDeliveryAdapter(
		ctx,
		owner.coordinator.pad.authority,
		catalog,
		objects,
		hosting,
	)
	if err != nil {
		t.Fatal(err)
	}
	adapter.now = clock.Now
	owner.coordinator.padDelivery = adapter
	owner.coordinator.padCandidates = catalog
	owner.coordinator.padBindingSource = bindings
	owner.coordinator.pad.open = func(
		ctx context.Context,
		authority *PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		return deliveryadmission.OpenTrustedRepository(
			ctx,
			authority,
			authority.RepositoryRef(),
			deliveryadmission.WithTrustedRepositoryClock(clock),
		)
	}
	request := OwnerIssueDeliveryAuthorizationRequest{
		ExpectedRevision: terminal.state.Revision,
		DecisionRef:      decisionRef,
	}
	if allowIncomplete {
		request.HumanDecisionRef = ownerPublishExceptionGovernance(
			t,
			owner,
			decisionRef,
		)
	}
	delivered, err := owner.coordinator.IssueAndBindDeliveryAuthorization(
		ctx,
		request,
	)
	if mismatchedBinding {
		if !errors.Is(err, ErrPADHostingCorrupt) {
			t.Fatalf("mismatched connector binding error = %v", err)
		}
		state, statusErr := owner.coordinator.work.Status()
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if state.DeliveryAuthorizationRef != "" {
			t.Fatalf(
				"mismatched connector binding published authorization %q",
				state.DeliveryAuthorizationRef,
			)
		}
		if _, resolveErr := catalog.ResolvePADGitBinding(
			ctx,
			candidate,
			admission.Intent.Destination,
			deliveryadmission.MechanismPullRequest,
		); !errors.Is(resolveErr, ErrPADCandidateCatalogUnavailable) {
			t.Fatalf(
				"mismatched connector binding occupied catalog: %v",
				resolveErr,
			)
		}
		return ownerBoundDeliveryFixture{}
	}
	if err != nil {
		t.Fatal(err)
	}
	resolvedBinding, err := catalog.ResolvePADGitBinding(
		ctx,
		candidate,
		admission.Intent.Destination,
		deliveryadmission.MechanismPullRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedBinding != binding {
		t.Fatalf(
			"owner-provisioned candidate binding = %#v, want %#v",
			resolvedBinding,
			binding,
		)
	}
	hostingTransport.mu.Lock()
	bindingCallsBeforeReplay := hostingTransport.bindingCalls
	hostingTransport.mu.Unlock()
	owner.coordinator.padBindingSource = ownerUnavailablePADDeliveryPorts{}
	replayed, err := owner.coordinator.IssueAndBindDeliveryAuthorization(
		ctx,
		request,
	)
	owner.coordinator.padBindingSource = bindings
	if err != nil {
		t.Fatalf("catalog-backed authorization replay: %v", err)
	}
	if replayed.Revision != delivered.Revision ||
		replayed.DeliveryAuthorizationRef != delivered.DeliveryAuthorizationRef {
		t.Fatalf("catalog-backed authorization replay = %#v", replayed)
	}
	hostingTransport.mu.Lock()
	bindingCallsAfterReplay := hostingTransport.bindingCalls
	hostingTransport.mu.Unlock()
	if bindingCallsBeforeReplay != 1 ||
		bindingCallsAfterReplay != bindingCallsBeforeReplay {
		t.Fatalf(
			"candidate binding transport calls = %d -> %d, want one first-use call",
			bindingCallsBeforeReplay,
			bindingCallsAfterReplay,
		)
	}

	var (
		kind      deliveryadmission.AuthorizationKind
		expiresAt int64
	)
	switch decision.Disposition {
	case deliveryadmission.DeliveryAuthorizeNormal:
		authorization, err := deliveryadmission.ValidateAuthorization(
			ctx,
			owner.pad,
			owner.coordinator.rar,
			delivered.DeliveryAuthorizationRef,
		)
		if err != nil {
			t.Fatal(err)
		}
		kind = deliveryadmission.AuthorizationNormal
		expiresAt = authorization.Binding.ExpiresAt
	case deliveryadmission.DeliveryRequireException:
		authorization, err :=
			deliveryadmission.ValidateExceptionAuthorization(
				ctx,
				owner.pad,
				owner.coordinator.rar,
				delivered.DeliveryAuthorizationRef,
			)
		if err != nil {
			t.Fatal(err)
		}
		kind = deliveryadmission.AuthorizationException
		expiresAt = authorization.Binding.ExpiresAt
	default:
		t.Fatalf("unsupported delivery disposition %q", decision.Disposition)
	}
	return ownerBoundDeliveryFixture{
		owner:                  owner,
		hosting:                hostingTransport,
		clock:                  clock,
		kind:                   kind,
		authorizationRef:       delivered.DeliveryAuthorizationRef,
		authorizationExpiresAt: expiresAt,
		candidateRevision:      candidateRevision,
	}
}

func ownerAdmitBoundDelivery(
	t *testing.T,
	fixture ownerCoordinatorFixture,
	seed string,
	baseRevision string,
	allowIncomplete bool,
) ownerAdmittedDelivery {
	t.Helper()
	ctx := context.Background()
	exceptionTTL := int64(0)
	if allowIncomplete {
		exceptionTTL = 90
	}
	policy, err := deliveryadmission.NewRoutePolicy(
		"policy:"+seed,
		"revision:1",
		deliveryadmission.RoutePRWithoutIssue,
		true,
		allowIncomplete,
		false,
		120,
		exceptionTTL,
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
			TargetRef:        "refs/heads/main",
			ObservedRevision: baseRevision,
			DefaultBranch:    true,
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
