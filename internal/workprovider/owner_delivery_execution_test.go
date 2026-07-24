package workprovider

import (
	"context"
	"errors"
	"os"
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

func TestOwnerCoordinatorAllowsReadOnlyCandidateCatalogAndRequiresCatalogForMutableBindings(
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
	); err != nil {
		t.Fatalf("read-only candidate catalog composition = %v", err)
	}

	dependencies.PADCandidateCatalog = nil
	dependencies.PADGitBindingAuthority = ownerUnavailablePADDeliveryPorts{}
	if _, err := NewOwnerCoordinator(
		context.Background(),
		dependencies,
	); err == nil ||
		!strings.Contains(err.Error(), "requires a PAD candidate catalog") {
		t.Fatalf("mutable binding without candidate catalog = %v", err)
	}

	dependencies.PADCandidateCatalog = leftCatalog
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

func TestBoundDeliveryExecutionRejectsTerminalProductiveBlocker(
	t *testing.T,
) {
	state := workrun.WorkRunState{
		DeliveryAuthorizationRef: ownerTestRef("bound-authorization"),
		ProductiveBlockerRef:     ownerTestRef("terminal-blocker"),
	}
	if err := validateOwnerBoundDeliveryState(
		state,
		false,
	); !errors.Is(err, workrun.ErrWorkRunInvalidTransition) {
		t.Fatalf("blocked bound delivery validation = %v", err)
	}
	if err := validateOwnerBoundDeliveryState(state, true); err == nil ||
		!errors.Is(err, workrun.ErrWorkRunInvalidTransition) ||
		strings.Contains(err.Error(), "productive blocker fences") {
		t.Fatalf(
			"recovery-only validation bypassed blocker but not normal authority: %v",
			err,
		)
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

func TestOwnerCoordinatorExecuteBoundDeliveryDoesNotPromoteUnanchoredTerminalAfterExpiry(
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
	if first.Outcome != deliveryadmission.ExecutionSucceeded ||
		first.AuthorizationRef != fixture.authorizationRef ||
		first.DeliveryRef != fixture.candidateRevision {
		t.Fatalf("fresh terminal delivery = %#v", first)
	}
	fixture.clock.SetUnix(fixture.authorizationExpiresAt + 60)
	fixture.hosting.mu.Lock()
	observationsBefore := fixture.hosting.observationCalls
	fixture.hosting.mu.Unlock()

	if _, err := fixture.owner.coordinator.ExecuteBoundDelivery(
		context.Background(),
	); !errors.Is(err, deliveryadmission.ErrExecutionResultUnavailable) {
		t.Fatalf("unanchored terminal execution replay = %v", err)
	}
	if _, found, err := fixture.owner.coordinator.RecoverBoundDelivery(
		context.Background(),
	); found ||
		!errors.Is(err, deliveryadmission.ErrExecutionResultUnavailable) {
		t.Fatalf(
			"unanchored terminal recovery = found %t, result %v",
			found,
			err,
		)
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

func TestOwnerCoordinatorExecuteBoundDeliveryConcurrentLosersRequireWorkRunAnchor(
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
	successes := 0
	unavailable := 0
	for index := range workers {
		switch {
		case errs[index] == nil:
			successes++
			if results[index].Outcome != deliveryadmission.ExecutionSucceeded ||
				results[index].AuthorizationRef != fixture.authorizationRef ||
				results[index].DeliveryRef != fixture.candidateRevision {
				t.Fatalf("worker %d result = %#v", index, results[index])
			}
		case errors.Is(
			errs[index],
			deliveryadmission.ErrExecutionResultUnavailable,
		):
			unavailable++
		default:
			t.Fatalf("worker %d: %v", index, errs[index])
		}
	}
	if successes != 1 || unavailable != workers-1 {
		t.Fatalf(
			"concurrent results: successes %d, unavailable %d",
			successes,
			unavailable,
		)
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

func TestOwnerCoordinatorRejectsCoherentCandidateRewireBeforeConsumption(
	t *testing.T,
) {
	fixture := newOwnerBoundDeliveryFixture(
		t,
		"bound-delivery-candidate-rewire",
		reviewtransaction.VerificationAggregateNotRequired,
	)
	stateBefore, err := fixture.owner.coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	remoteBefore, err := fixture.hosting.remoteRevision(
		fixture.binding.Destination.TargetRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.hosting.mu.Lock()
	observationsBefore := fixture.hosting.observationCalls
	mergeCallsBefore := fixture.hosting.mergeCalls
	fixture.hosting.mu.Unlock()

	replacement := ownerCoherentlyRewireCandidateCatalog(t, &fixture)
	if replacement.RecordRef == fixture.candidateAuthority.RecordRef ||
		replacement.CandidateTree == fixture.candidateAuthority.CandidateTree ||
		replacement.Binding.CandidateRevision ==
			fixture.candidateAuthority.Binding.CandidateRevision {
		t.Fatalf(
			"coherent replacement did not change exact authority:\nA %#v\nB %#v",
			fixture.candidateAuthority,
			replacement,
		)
	}
	if _, err := fixture.owner.coordinator.ExecuteBoundDelivery(
		context.Background(),
	); !errors.Is(err, ErrPADCandidateCatalogConflict) {
		t.Fatalf("coherently rewired bound delivery = %v", err)
	}

	stateAfter, err := fixture.owner.coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if stateAfter.Revision != stateBefore.Revision ||
		stateAfter.DeliveryAuthorizationRef !=
			stateBefore.DeliveryAuthorizationRef {
		t.Fatalf(
			"failed candidate continuity check mutated WorkRun:\nbefore %#v\nafter  %#v",
			stateBefore,
			stateAfter,
		)
	}
	fixture.hosting.mu.Lock()
	observationsAfter := fixture.hosting.observationCalls
	mergeCallsAfter := fixture.hosting.mergeCalls
	fixture.hosting.mu.Unlock()
	if observationsAfter != observationsBefore ||
		mergeCallsAfter != mergeCallsBefore {
		t.Fatalf(
			"rewire reached hosting: observations %d -> %d, merges %d -> %d",
			observationsBefore,
			observationsAfter,
			mergeCallsBefore,
			mergeCallsAfter,
		)
	}
	remoteAfter, err := fixture.hosting.remoteRevision(
		fixture.binding.Destination.TargetRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if remoteAfter != remoteBefore {
		t.Fatalf("rewire changed remote %q -> %q", remoteBefore, remoteAfter)
	}
	if _, err := os.Stat(ownerBoundAuthorizationUsePath(fixture)); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("rewire consumed authorization before failing: %v", err)
	}
}

func TestOwnerCoordinatorRejectsCandidateRewireBetweenProbeAndEffect(
	t *testing.T,
) {
	fixture := newOwnerBoundDeliveryFixture(
		t,
		"bound-delivery-midflight-candidate-rewire",
		reviewtransaction.VerificationAggregateNotRequired,
	)
	remoteBefore, err := fixture.hosting.remoteRevision(
		fixture.binding.Destination.TargetRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.hosting.mu.Lock()
	observationsBefore := fixture.hosting.observationCalls
	mergeCallsBefore := fixture.hosting.mergeCalls
	fixture.hosting.mu.Unlock()

	var replacement PADCandidateAuthority
	hooked := &ownerHookedHostingAuthority{
		delegate: fixture.owner.coordinator.padDelivery.hosting,
		afterFirstObservation: func() {
			replacement = ownerCoherentlyRewireCandidateCatalog(
				t,
				&fixture,
			)
		},
	}
	fixture.owner.coordinator.padDelivery.hosting = hooked
	if _, err := fixture.owner.coordinator.ExecuteBoundDelivery(
		context.Background(),
	); !errors.Is(err, ErrPADCandidateCatalogConflict) {
		t.Fatalf("mid-flight candidate rewire = %v", err)
	}
	if replacement.RecordRef == "" ||
		replacement.RecordRef == fixture.candidateAuthority.RecordRef {
		t.Fatalf("mid-flight hook did not install replacement: %#v", replacement)
	}
	fixture.hosting.mu.Lock()
	observationsAfter := fixture.hosting.observationCalls
	mergeCallsAfter := fixture.hosting.mergeCalls
	fixture.hosting.mu.Unlock()
	if observationsAfter != observationsBefore+1 ||
		mergeCallsAfter != mergeCallsBefore {
		t.Fatalf(
			"mid-flight rewire crossed effect boundary: observations %d -> %d, merges %d -> %d",
			observationsBefore,
			observationsAfter,
			mergeCallsBefore,
			mergeCallsAfter,
		)
	}
	remoteAfter, err := fixture.hosting.remoteRevision(
		fixture.binding.Destination.TargetRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if remoteAfter != remoteBefore {
		t.Fatalf(
			"mid-flight rewire changed remote %q -> %q",
			remoteBefore,
			remoteAfter,
		)
	}
	if _, err := os.Stat(ownerBoundAuthorizationUsePath(fixture)); err != nil {
		t.Fatalf(
			"mid-flight guard failed before the expected probe reservation: %v",
			err,
		)
	}
}

type ownerHookedHostingAuthority struct {
	delegate              HostingAuthority
	once                  sync.Once
	afterFirstObservation func()
}

func (authority *ownerHookedHostingAuthority) ObserveDelivery(
	ctx context.Context,
	request HostingObservationRequest,
) (HostingDeliveryObservation, error) {
	observation, err := authority.delegate.ObserveDelivery(ctx, request)
	if err == nil && authority.afterFirstObservation != nil {
		authority.once.Do(authority.afterFirstObservation)
	}
	return observation, err
}

func (authority *ownerHookedHostingAuthority) CompareAndSwapBranch(
	ctx context.Context,
	request HostingBranchCASRequest,
) (HostingBranchCASReceipt, error) {
	return authority.delegate.CompareAndSwapBranch(ctx, request)
}

func (authority *ownerHookedHostingAuthority) MergePullRequest(
	ctx context.Context,
	request HostingPullRequestMergeRequest,
) (HostingPullRequestMergeReceipt, error) {
	return authority.delegate.MergePullRequest(ctx, request)
}

type ownerBoundDeliveryFixture struct {
	owner                  ownerCoordinatorFixture
	hosting                *padDeliveryTestHosting
	clock                  *padDeliveryTestClock
	catalog                *PADCandidateCatalog
	candidateAuthority     PADCandidateAuthority
	binding                PADGitBinding
	kind                   deliveryadmission.AuthorizationKind
	authorizationRef       string
	authorizationExpiresAt int64
	candidateRevision      string
}

func ownerCoherentlyRewireCandidateCatalog(
	t *testing.T,
	fixture *ownerBoundDeliveryFixture,
) PADCandidateAuthority {
	t.Helper()
	return ownerCoherentlyRewirePADCandidateCatalog(
		t,
		fixture.owner.repo,
		fixture.owner.repositoryRef,
		fixture.catalog,
		fixture.binding,
		"candidate-authority-replacement.txt",
	)
}

func ownerCoherentlyRewirePADCandidateCatalog(
	t *testing.T,
	repository string,
	repositoryRef string,
	catalog *PADCandidateCatalog,
	original PADGitBinding,
	filename string,
) PADCandidateAuthority {
	t.Helper()
	path := filepath.Join(repository, filename)
	if err := os.WriteFile(path, []byte("replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ownerGit(
		t,
		repository,
		"add",
		filename,
	)
	ownerGit(
		t,
		repository,
		"commit",
		"--quiet",
		"-m",
		"candidate authority replacement",
	)
	binding := original
	binding.CandidateRevision = "git:" + strings.TrimSpace(
		ownerGit(t, repository, "rev-parse", "HEAD"),
	)
	candidateTree := strings.TrimSpace(
		ownerGit(t, repository, "rev-parse", "HEAD^{tree}"),
	)
	record, err := catalog.newRecord(candidateTree, binding)
	if err != nil {
		t.Fatal(err)
	}
	recordPayload, err := canonicalCoordinationPayload(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		catalog.recordPath(record.RecordRef),
		recordPayload,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	index := padCandidateCatalogIndex{
		Schema:        padCandidateCatalogIndexSchema,
		RepositoryRef: repositoryRef,
		LookupRef:     record.LookupRef,
		RecordRef:     record.RecordRef,
	}
	indexPayload, err := canonicalCoordinationPayload(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		catalog.indexPath(record.LookupRef),
		indexPayload,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	resolved, err := catalog.ResolvePADGitBinding(
		context.Background(),
		binding.Candidate,
		binding.Destination,
		binding.Mechanism,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != binding {
		t.Fatalf(
			"coherent replacement lookup = %#v, want %#v",
			resolved,
			binding,
		)
	}
	return record.authority()
}

func ownerBoundAuthorizationUsePath(
	fixture ownerBoundDeliveryFixture,
) string {
	return filepath.Join(
		fixture.owner.commonDir,
		"gentle-ai",
		"delivery-authorization-uses",
		"v1",
		"repositories",
		fixture.owner.coordinator.pad.authority.identity.lease.StorageKey(),
		strings.TrimPrefix(fixture.authorizationRef, "sha256:")+".json",
	)
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
	candidateAuthority, err :=
		owner.coordinator.preparePADCandidateBindingFor(
			ctx,
			candidate,
			admission.Intent.Destination,
			deliveryadmission.MechanismPullRequest,
			terminal.authority.Receipt.ReceiptRef,
			terminal.authority.Result.ResultRef,
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
	decision, err := deliveryadmission.Decide(
		ctx,
		owner.pad,
		owner.coordinator.rar,
		deliveryadmission.DeliveryRequest{
			AdmissionDecisionRef:  admission.Admission.AdmissionDecisionRef,
			PolicyRef:             admission.PolicyRef,
			ReviewReceiptRef:      terminal.authority.Receipt.ReceiptRef,
			VerificationResultRef: terminal.authority.Result.ResultRef,
			CandidateAuthorityRef: candidateAuthority.RecordRef,
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
		catalog:                catalog,
		candidateAuthority:     candidateAuthority,
		binding:                binding,
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
