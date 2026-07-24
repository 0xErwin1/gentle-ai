package workprovider

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type ownerTestRouteReevaluationProbe struct {
	repositoryRef string
	now           int64
	mu            sync.Mutex
	calls         int
	last          deliveryadmission.LiveGateProbeRequest
	entered       chan struct{}
	release       chan struct{}
	enterOnce     sync.Once
}

type ownerProductiveRouteBindingAuthority struct {
	mu      sync.Mutex
	binding PADGitBinding
	calls   int
}

func (authority *ownerProductiveRouteBindingAuthority) ResolvePADGitBinding(
	_ context.Context,
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
) (PADGitBinding, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.calls++
	if err := authority.binding.Validate(
		candidate,
		destination,
		mechanism,
	); err != nil {
		return PADGitBinding{}, err
	}
	return authority.binding, nil
}

func (authority *ownerProductiveRouteBindingAuthority) count() int {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.calls
}

func (authority *ownerProductiveRouteBindingAuthority) setBinding(
	binding PADGitBinding,
) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.binding = binding
}

type ownerProductiveRouteHostingAuthority struct {
	mu           sync.Mutex
	observations int
	effects      int
}

func (authority *ownerProductiveRouteHostingAuthority) ObserveDelivery(
	_ context.Context,
	request HostingObservationRequest,
) (HostingDeliveryObservation, error) {
	authority.mu.Lock()
	authority.observations++
	authority.mu.Unlock()
	observation := HostingDeliveryObservation{
		Schema:                 HostingDeliveryObservationSchema,
		Request:                request,
		CandidateRevision:      request.Binding.CandidateRevision,
		ExpectedRemoteRevision: request.Binding.ExpectedRemoteRevision,
		RemoteIdentity:         request.Binding.ExpectedRemoteRevision,
		RemoteRevision:         request.Binding.ExpectedRemoteRevision,
		ProtectionState:        HostingProtectionPermitted,
		ProtectionRevision:     "protection:productive-route",
	}
	switch request.Mechanism {
	case deliveryadmission.MechanismFastForwardOnly:
		observation.PullRequestState = HostingPullRequestNotApplicable
		observation.RequiredChecksState = HostingChecksNotApplicable
	case deliveryadmission.MechanismPullRequest:
		observation.PullRequestRef = request.PullRequestRef
		observation.PullRequestState = HostingPullRequestOpen
		observation.PullRequestHeadRevision =
			request.Binding.CandidateRevision
		observation.PullRequestBaseRevision =
			request.Binding.ExpectedRemoteRevision
		observation.RequiredChecksState = HostingChecksPassed
		observation.RequiredChecksRevision = "checks:productive-route"
	}
	return observation, observation.Validate(request)
}

func (authority *ownerProductiveRouteHostingAuthority) CompareAndSwapBranch(
	context.Context,
	HostingBranchCASRequest,
) (HostingBranchCASReceipt, error) {
	authority.mu.Lock()
	authority.effects++
	authority.mu.Unlock()
	return HostingBranchCASReceipt{}, errors.New(
		"productive route test unexpectedly reached a branch effect",
	)
}

func (authority *ownerProductiveRouteHostingAuthority) MergePullRequest(
	context.Context,
	HostingPullRequestMergeRequest,
) (HostingPullRequestMergeReceipt, error) {
	authority.mu.Lock()
	authority.effects++
	authority.mu.Unlock()
	return HostingPullRequestMergeReceipt{}, errors.New(
		"productive route test unexpectedly reached a merge effect",
	)
}

func (authority *ownerProductiveRouteHostingAuthority) snapshot() (
	observations int,
	effects int,
) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.observations, authority.effects
}

type ownerTestMutableActivationResolver struct {
	mu    sync.Mutex
	mode  ActivationMode
	first chan struct{}
	once  sync.Once
}

func (resolver *ownerTestMutableActivationResolver) ResolveActivation(
	_ context.Context,
	_ string,
) (ActivationMode, error) {
	resolver.mu.Lock()
	mode := resolver.mode
	resolver.mu.Unlock()
	resolver.once.Do(func() {
		if resolver.first != nil {
			close(resolver.first)
		}
	})
	return mode, nil
}

func (resolver *ownerTestMutableActivationResolver) set(mode ActivationMode) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.mode = mode
}

func (probe *ownerTestRouteReevaluationProbe) RepositoryRef() string {
	if probe == nil {
		return ""
	}
	return probe.repositoryRef
}

func (probe *ownerTestRouteReevaluationProbe) ProbeLiveGate(
	_ context.Context,
	request deliveryadmission.LiveGateProbeRequest,
) (deliveryadmission.LiveGateProbeResult, error) {
	probe.mu.Lock()
	probe.calls++
	probe.last = request
	probe.mu.Unlock()
	if probe.entered != nil {
		probe.enterOnce.Do(func() { close(probe.entered) })
	}
	if probe.release != nil {
		<-probe.release
	}
	requestRef, err := request.Ref()
	if err != nil {
		return deliveryadmission.LiveGateProbeResult{}, err
	}
	return deliveryadmission.LiveGateProbeResult{
		Schema:                 deliveryadmission.LiveGateProbeResultContractV1,
		RequestRef:             requestRef,
		Request:                request,
		ObservedRemoteRevision: request.Destination.ObservedRevision,
		RemoteFreshnessRef:     ownerTestRef("route-remote-freshness"),
		ProtectionMode:         deliveryadmission.ProtectionRespect,
		ProtectionSatisfied:    true,
		ProtectionRef:          ownerTestRef("route-protection"),
		FastForwardSatisfied:   true,
		FastForwardRef:         ownerTestRef("route-fast-forward"),
		ObservedAt:             probe.now,
		ExpiresAt:              probe.now + 60,
	}, nil
}

func (probe *ownerTestRouteReevaluationProbe) snapshot() (
	int,
	deliveryadmission.LiveGateProbeRequest,
) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return probe.calls, probe.last
}

func TestOwnerCoordinatorReevaluatesDeliveryRouteWithoutChangingContentAuthority(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "delivery-route-reevaluation")
	ctx := context.Background()
	destination := deliveryadmission.DestinationBinding{
		RepositoryRef:    fixture.repositoryRef,
		TargetRef:        "refs/heads/main",
		ObservedRevision: "git:base/1",
		DefaultBranch:    true,
	}
	scopeDigest := ownerTestRef("delivery-route-scope")
	sourceAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RoutePRWithoutIssue,
		"source",
		scopeDigest,
		destination,
	)
	before, sourceDecisionRef := ownerPrepareTerminalRouteDecision(
		t,
		fixture,
		sourceAdmission,
	)
	targetAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RouteDirectMain,
		"target",
		scopeDigest,
		destination,
	)
	probe := &ownerTestRouteReevaluationProbe{
		repositoryRef: fixture.repositoryRef,
		now:           fixture.now,
	}
	coordinator := ownerCoordinatorWithRouteProbe(t, fixture, probe)
	request := OwnerDeliveryRouteReevaluationRequest{
		ExpectedRevision:           before.Revision,
		SourceDecisionRef:          sourceDecisionRef,
		TargetAdmissionDecisionRef: targetAdmission.Admission.AdmissionDecisionRef,
		Mechanism:                  deliveryadmission.MechanismFastForwardOnly,
	}
	changed, err := coordinator.ReevaluateDeliveryRoute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ReevaluationRef == "" ||
		changed.State.DeliveryRouteReevaluationRef !=
			changed.ReevaluationRef ||
		changed.State.DeliveryIntentRef != targetAdmission.Admission.IntentRef ||
		changed.Reevaluation.SourceDecisionRef != sourceDecisionRef ||
		changed.Reevaluation.TargetAdmissionDecisionRef !=
			targetAdmission.Admission.AdmissionDecisionRef {
		t.Fatalf("route reevaluation = %#v", changed)
	}
	if changed.State.ImplementationRoute != before.ImplementationRoute ||
		!reflect.DeepEqual(changed.State.RouteDecision, before.RouteDecision) ||
		changed.State.RouteAcceptanceRef != before.RouteAcceptanceRef ||
		changed.State.SDDRunRef != before.SDDRunRef ||
		!reflect.DeepEqual(changed.State.Handoff, before.Handoff) ||
		changed.State.VerificationResultRef !=
			before.VerificationResultRef ||
		changed.State.PostVerificationSnapshotRef !=
			before.PostVerificationSnapshotRef ||
		changed.State.ReviewReceiptRef != before.ReviewReceiptRef ||
		changed.Reevaluation.Candidate.Digest !=
			before.Handoff.CandidateRef ||
		changed.Reevaluation.VerificationResultRef !=
			before.VerificationResultRef ||
		changed.Reevaluation.ReviewReceiptRef != before.ReviewReceiptRef {
		t.Fatalf("route reevaluation changed content authority: %#v", changed.State)
	}
	probeCalls, lastProbe := probe.snapshot()
	if probeCalls != 1 ||
		lastProbe.Route != deliveryadmission.RouteDirectMain ||
		lastProbe.Candidate.Digest != before.Handoff.CandidateRef ||
		lastProbe.Destination != destination {
		t.Fatalf("live route probe = %d / %#v", probeCalls, lastProbe)
	}
	stored, err := fixture.pad.ResolveRouteReevaluation(
		ctx,
		changed.ReevaluationRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored != changed.Reevaluation {
		t.Fatalf("stored route reevaluation differs: %#v", stored)
	}

	replayed, err := coordinator.ReevaluateDeliveryRoute(ctx, request)
	if err != nil {
		t.Fatalf("exact owner route replay: %v", err)
	}
	probeCalls, _ = probe.snapshot()
	if replayed.ReevaluationRef != changed.ReevaluationRef ||
		!reflect.DeepEqual(replayed.State, changed.State) ||
		probeCalls != 1 {
		t.Fatalf(
			"route replay ref/state/probes = %q/%#v/%d",
			replayed.ReevaluationRef,
			replayed.State,
			probeCalls,
		)
	}

	alternateDecision, err := fixture.pad.ResolveDeliveryDecision(
		ctx,
		changed.Reevaluation.DecisionRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	alternateDecision.Gates.RemoteFreshnessRef = ownerTestRef(
		"alternate-target-route-remote-freshness",
	)
	alternateDecision.GateRef, err = fixture.pad.PublishGateEvidence(
		ctx,
		alternateDecision.Gates,
	)
	if err != nil {
		t.Fatal(err)
	}
	alternateDecisionRef, err := fixture.pad.PublishDeliveryDecision(
		ctx,
		alternateDecision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if alternateDecisionRef == changed.Reevaluation.DecisionRef {
		t.Fatal("alternate target decision did not receive a distinct ref")
	}
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	beforeMismatch := ownerTreeContentFingerprint(t, root)
	if _, err := coordinator.IssueAndBindDeliveryAuthorization(
		ctx,
		OwnerIssueDeliveryAuthorizationRequest{
			ExpectedRevision: changed.State.Revision,
			DecisionRef:      alternateDecisionRef,
		},
	); !errors.Is(err, deliveryadmission.ErrBindingMismatch) {
		t.Fatalf("alternate target decision error = %v", err)
	}
	if ownerTreeContentFingerprint(t, root) != beforeMismatch {
		t.Fatal("alternate target decision mutated durable authority")
	}
	afterMismatch, err := coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterMismatch, changed.State) {
		t.Fatalf("alternate target decision changed WorkRun: %#v", afterMismatch)
	}
	if _, err := resolveOwnerPADAuthorizationJournal(
		ctx,
		coordinator,
	); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("alternate target decision journal error = %v", err)
	}

	alternateAuthorization, err := deliveryadmission.IssueAuthorization(
		ctx,
		fixture.pad,
		coordinator.rar,
		deliveryadmission.AuthorizationIssueRequest{
			DecisionRef:  alternateDecisionRef,
			Nonce:        "nonce:alternate-target-route-delivery",
			AuthorityRef: alternateDecision.AuthorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	alternateAuthorizationRef, err := fixture.pad.PublishAuthorization(
		ctx,
		alternateAuthorization,
	)
	if err != nil {
		t.Fatal(err)
	}
	beforeGenericBind := ownerTreeContentFingerprint(t, root)
	if _, err := coordinator.BindDeliveryAuthorization(
		ctx,
		OwnerBindDeliveryAuthorizationRequest{
			ExpectedRevision: changed.State.Revision,
			AuthorizationRef: alternateAuthorizationRef,
		},
	); !errors.Is(err, workrun.ErrAuthorityBindingMismatch) {
		t.Fatalf("generic alternate authorization bind error = %v", err)
	}
	if ownerTreeContentFingerprint(t, root) != beforeGenericBind {
		t.Fatal("generic alternate authorization bind mutated durable authority")
	}
	afterGenericBind, err := coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterGenericBind, changed.State) {
		t.Fatalf(
			"generic alternate authorization bind changed WorkRun: %#v",
			afterGenericBind,
		)
	}
	if _, err := resolveOwnerPADAuthorizationJournal(
		ctx,
		coordinator,
	); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("generic alternate authorization journal error = %v", err)
	}

	delivered, err := coordinator.IssueAndBindDeliveryAuthorization(
		ctx,
		OwnerIssueDeliveryAuthorizationRequest{
			ExpectedRevision: changed.State.Revision,
			DecisionRef:      changed.Reevaluation.DecisionRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.DeliveryAuthorizationRef == "" ||
		delivered.DeliveryIntentRef != targetAdmission.Admission.IntentRef ||
		delivered.DeliveryRouteReevaluationRef != changed.ReevaluationRef ||
		!reflect.DeepEqual(delivered.Handoff, before.Handoff) {
		t.Fatalf("target-route authorization binding = %#v", delivered)
	}
	live, err := fixture.pad.ResolveLiveAuthorization(
		ctx,
		delivered.DeliveryAuthorizationRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if live.Binding.DecisionRef != changed.Reevaluation.DecisionRef {
		t.Fatalf("target-route live authorization = %#v", live)
	}
}

func TestOwnerCoordinatorReevaluatesRouteWithExactTargetCandidateAuthority(
	t *testing.T,
) {
	fixture := newOwnerProductiveRouteReevaluationFixture(
		t,
		"productive-route-exact-target",
	)
	changed, err := fixture.coordinator.ReevaluateDeliveryRoute(
		context.Background(),
		fixture.request(),
	)
	if err != nil {
		t.Fatal(err)
	}
	targetDecision, err := fixture.owner.pad.ResolveDeliveryDecision(
		context.Background(),
		changed.Reevaluation.DecisionRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, targetLookupRef, err := fixture.catalog.lookup(
		fixture.targetBinding.Candidate,
		fixture.targetBinding.Destination,
		fixture.targetBinding.Mechanism,
	)
	if err != nil {
		t.Fatal(err)
	}
	targetIndex, exists, err := fixture.catalog.readIndex(targetLookupRef)
	if err != nil {
		t.Fatal(err)
	}
	if !exists ||
		targetDecision.CandidateAuthorityRef != targetIndex.RecordRef ||
		targetDecision.CandidateAuthorityRef ==
			fixture.sourceAuthority.RecordRef {
		t.Fatalf(
			"target decision authority = %q, source %q, index %#v",
			targetDecision.CandidateAuthorityRef,
			fixture.sourceAuthority.RecordRef,
			targetIndex,
		)
	}
	targetAuthority, err := fixture.catalog.ResolveCandidateAuthority(
		context.Background(),
		targetDecision.CandidateAuthorityRef,
		fixture.targetBinding.Candidate,
		fixture.targetBinding.Destination,
		fixture.targetBinding.Mechanism,
		fixture.sourceAuthority.CandidateTree,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePADRouteCandidateTransition(
		fixture.sourceAuthority,
		targetAuthority,
		fixture.targetBinding.Destination,
		fixture.targetBinding.Mechanism,
	); err != nil {
		t.Fatal(err)
	}
	observations, effects := fixture.hosting.snapshot()
	if fixture.bindings.count() != 1 ||
		observations != 1 ||
		effects != 0 {
		t.Fatalf(
			"target authority/probe/effects = %d/%d/%d, want 1/1/0",
			fixture.bindings.count(),
			observations,
			effects,
		)
	}
}

func TestOwnerCoordinatorRejectsCandidateRewireBeforeRouteReevaluation(
	t *testing.T,
) {
	fixture := newOwnerProductiveRouteReevaluationFixture(
		t,
		"productive-route-source-rewire",
	)
	replacement := ownerCoherentlyRewirePADCandidateCatalog(
		t,
		fixture.owner.repo,
		fixture.owner.repositoryRef,
		fixture.catalog,
		fixture.sourceBinding,
		"route-source-replacement.txt",
	)
	if replacement.RecordRef == fixture.sourceAuthority.RecordRef {
		t.Fatal("source candidate rewire did not change the catalog record")
	}
	before, err := fixture.coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(fixture.owner.commonDir, "gentle-ai")
	beforeGates := ownerCountFilesWithSuffix(
		t,
		root,
		".gate-evidence.json",
	)
	beforeDecisions := ownerCountFilesWithSuffix(
		t,
		root,
		".delivery-decision.json",
	)
	beforeReevaluations := ownerCountFilesWithSuffix(
		t,
		root,
		".route-reevaluation.json",
	)

	if _, err := fixture.coordinator.ReevaluateDeliveryRoute(
		context.Background(),
		fixture.request(),
	); !errors.Is(err, ErrPADCandidateCatalogConflict) {
		t.Fatalf("coherently rewired source route candidate = %v", err)
	}
	observations, effects := fixture.hosting.snapshot()
	if observations != 0 || effects != 0 || fixture.bindings.count() != 0 {
		t.Fatalf(
			"source rewire reached target authority/probe/effect = %d/%d/%d",
			fixture.bindings.count(),
			observations,
			effects,
		)
	}
	after, err := fixture.coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) ||
		ownerCountFilesWithSuffix(t, root, ".gate-evidence.json") !=
			beforeGates ||
		ownerCountFilesWithSuffix(t, root, ".delivery-decision.json") !=
			beforeDecisions ||
		ownerCountFilesWithSuffix(t, root, ".route-reevaluation.json") !=
			beforeReevaluations {
		t.Fatal("source candidate rewire published authority or mutated WorkRun")
	}
}

func TestOwnerCoordinatorRejectsTargetCandidateAuthorityDrift(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		mutate func(
			t *testing.T,
			fixture *ownerProductiveRouteReevaluationFixture,
			binding *PADGitBinding,
		)
	}{
		{
			name: "same_tree_different_commit",
			mutate: func(
				t *testing.T,
				fixture *ownerProductiveRouteReevaluationFixture,
				binding *PADGitBinding,
			) {
				binding.CandidateRevision = "git:" + strings.TrimSpace(
					ownerGit(
						t,
						fixture.owner.repo,
						"commit-tree",
						fixture.sourceAuthority.CandidateTree,
						"-p",
						strings.TrimPrefix(
							fixture.sourceBinding.CandidateRevision,
							"git:",
						),
						"-m",
						"same tree replacement commit",
					),
				)
			},
		},
		{
			name: "hosting_repository",
			mutate: func(
				_ *testing.T,
				_ *ownerProductiveRouteReevaluationFixture,
				binding *PADGitBinding,
			) {
				binding.HostingRepositoryRef =
					"github:attacker/replacement"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newOwnerProductiveRouteReevaluationFixture(
				t,
				"productive-route-target-drift-"+test.name,
			)
			replacementBinding := fixture.targetBinding
			test.mutate(t, &fixture, &replacementBinding)
			replacementRecord, err := fixture.catalog.newRecord(
				fixture.sourceAuthority.CandidateTree,
				replacementBinding,
			)
			if err != nil {
				t.Fatal(err)
			}
			ownerRewritePADCandidateCatalogRecord(
				t,
				fixture.catalog,
				replacementRecord,
			)
			before, err := fixture.coordinator.work.Status()
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(fixture.owner.commonDir, "gentle-ai")
			beforeGates := ownerCountFilesWithSuffix(
				t,
				root,
				".gate-evidence.json",
			)
			beforeDecisions := ownerCountFilesWithSuffix(
				t,
				root,
				".delivery-decision.json",
			)
			beforeReevaluations := ownerCountFilesWithSuffix(
				t,
				root,
				".route-reevaluation.json",
			)

			if _, err := fixture.coordinator.ReevaluateDeliveryRoute(
				context.Background(),
				fixture.request(),
			); !errors.Is(err, deliveryadmission.ErrBindingMismatch) {
				t.Fatalf("target authority drift = %v", err)
			}
			observations, effects := fixture.hosting.snapshot()
			if observations != 0 || effects != 0 {
				t.Fatalf(
					"target drift reached probe/effect = %d/%d",
					observations,
					effects,
				)
			}
			after, err := fixture.coordinator.work.Status()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, before) ||
				ownerCountFilesWithSuffix(
					t,
					root,
					".gate-evidence.json",
				) != beforeGates ||
				ownerCountFilesWithSuffix(
					t,
					root,
					".delivery-decision.json",
				) != beforeDecisions ||
				ownerCountFilesWithSuffix(
					t,
					root,
					".route-reevaluation.json",
				) != beforeReevaluations {
				t.Fatal(
					"target authority drift published authority or mutated WorkRun",
				)
			}
		})
	}
}

func TestOwnerCoordinatorRejectsUnpublishedTargetCandidateAuthorityDrift(
	t *testing.T,
) {
	fixture := newOwnerProductiveRouteReevaluationFixture(
		t,
		"productive-route-unpublished-target-drift",
	)
	_, targetLookupRef, err := fixture.catalog.lookup(
		fixture.targetBinding.Candidate,
		fixture.targetBinding.Destination,
		fixture.targetBinding.Mechanism,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists, err := fixture.catalog.readIndex(targetLookupRef); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Fatal("target candidate slot unexpectedly occupied before reevaluation")
	}
	drifted := fixture.targetBinding
	drifted.HostingRepositoryRef = "github:attacker/replacement"
	fixture.bindings.setBinding(drifted)

	before, err := fixture.coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(fixture.owner.commonDir, "gentle-ai")
	beforeObjects := ownerCountFilesWithSuffix(
		t,
		filepath.Join(fixture.catalog.root, "objects"),
		".json",
	)
	beforeBindings := ownerCountFilesWithSuffix(
		t,
		filepath.Join(fixture.catalog.root, "bindings"),
		".json",
	)
	beforeGates := ownerCountFilesWithSuffix(
		t,
		root,
		".gate-evidence.json",
	)
	beforeDecisions := ownerCountFilesWithSuffix(
		t,
		root,
		".delivery-decision.json",
	)
	beforeReevaluations := ownerCountFilesWithSuffix(
		t,
		root,
		".route-reevaluation.json",
	)

	if _, err := fixture.coordinator.ReevaluateDeliveryRoute(
		context.Background(),
		fixture.request(),
	); !errors.Is(err, deliveryadmission.ErrBindingMismatch) {
		t.Fatalf("unpublished target authority drift = %v", err)
	}
	after, err := fixture.coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	observations, effects := fixture.hosting.snapshot()
	if !reflect.DeepEqual(after, before) ||
		fixture.bindings.count() != 1 ||
		observations != 0 ||
		effects != 0 ||
		ownerCountFilesWithSuffix(
			t,
			filepath.Join(fixture.catalog.root, "objects"),
			".json",
		) != beforeObjects ||
		ownerCountFilesWithSuffix(
			t,
			filepath.Join(fixture.catalog.root, "bindings"),
			".json",
		) != beforeBindings ||
		ownerCountFilesWithSuffix(t, root, ".gate-evidence.json") !=
			beforeGates ||
		ownerCountFilesWithSuffix(t, root, ".delivery-decision.json") !=
			beforeDecisions ||
		ownerCountFilesWithSuffix(t, root, ".route-reevaluation.json") !=
			beforeReevaluations {
		t.Fatal("rejected target proposal published authority or mutated WorkRun")
	}
	if _, exists, err := fixture.catalog.readIndex(targetLookupRef); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Fatal("rejected target proposal poisoned the candidate slot")
	}

	fixture.bindings.setBinding(fixture.targetBinding)
	changed, err := fixture.coordinator.ReevaluateDeliveryRoute(
		context.Background(),
		fixture.request(),
	)
	if err != nil {
		t.Fatalf("legitimate retry after rejected target proposal: %v", err)
	}
	if changed.ReevaluationRef == "" {
		t.Fatal("legitimate retry did not publish route reevaluation")
	}
	if _, exists, err := fixture.catalog.readIndex(targetLookupRef); err != nil {
		t.Fatal(err)
	} else if !exists {
		t.Fatal("legitimate retry did not publish target candidate authority")
	}
	observations, effects = fixture.hosting.snapshot()
	if fixture.bindings.count() != 2 ||
		observations != 1 ||
		effects != 0 {
		t.Fatalf(
			"legitimate retry authority/probe/effect = %d/%d/%d, want 2/1/0",
			fixture.bindings.count(),
			observations,
			effects,
		)
	}
}

func TestOwnerCoordinatorConcurrentRouteReevaluationPublishesOneAuthority(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "concurrent-route-reevaluation")
	ctx := context.Background()
	destination := deliveryadmission.DestinationBinding{
		RepositoryRef:    fixture.repositoryRef,
		TargetRef:        "refs/heads/main",
		ObservedRevision: "git:base/1",
		DefaultBranch:    true,
	}
	scopeDigest := ownerTestRef("concurrent-route-scope")
	sourceAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RoutePRWithoutIssue,
		"concurrent-source",
		scopeDigest,
		destination,
	)
	before, sourceDecisionRef := ownerPrepareTerminalRouteDecision(
		t,
		fixture,
		sourceAdmission,
	)
	targetAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RouteDirectMain,
		"concurrent-target",
		scopeDigest,
		destination,
	)
	entered := make(chan struct{})
	release := make(chan struct{})
	probe := &ownerTestRouteReevaluationProbe{
		repositoryRef: fixture.repositoryRef,
		now:           fixture.now,
		entered:       entered,
		release:       release,
	}
	coordinator := ownerCoordinatorWithRouteProbe(t, fixture, probe)
	request := OwnerDeliveryRouteReevaluationRequest{
		ExpectedRevision:           before.Revision,
		SourceDecisionRef:          sourceDecisionRef,
		TargetAdmissionDecisionRef: targetAdmission.Admission.AdmissionDecisionRef,
		Mechanism:                  deliveryadmission.MechanismFastForwardOnly,
	}
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	beforeGates := ownerCountFilesWithSuffix(t, root, ".gate-evidence.json")
	beforeDecisions := ownerCountFilesWithSuffix(t, root, ".delivery-decision.json")
	beforeReevaluations := ownerCountFilesWithSuffix(t, root, ".route-reevaluation.json")

	const contenders = 2
	results := make(chan OwnerDeliveryRouteReevaluation, contenders)
	failures := make(chan error, contenders)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := coordinator.ReevaluateDeliveryRoute(ctx, request)
			if err != nil {
				failures <- err
				return
			}
			results <- result
		}()
	}
	close(start)
	<-entered
	close(release)
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatalf("concurrent route reevaluation: %v", err)
	}
	var first *OwnerDeliveryRouteReevaluation
	count := 0
	for result := range results {
		count++
		if first == nil {
			value := result
			first = &value
			continue
		}
		if result.ReevaluationRef != first.ReevaluationRef ||
			!reflect.DeepEqual(result.State, first.State) {
			t.Fatalf("concurrent route reevaluation returned different authority")
		}
	}
	probeCalls, _ := probe.snapshot()
	if count != contenders || first == nil || probeCalls != 1 {
		t.Fatalf(
			"concurrent results/probes = %d/%d, want %d/1",
			count,
			probeCalls,
			contenders,
		)
	}
	if got := ownerCountFilesWithSuffix(t, root, ".gate-evidence.json") -
		beforeGates; got != 1 {
		t.Fatalf("concurrent route gate publications = %d, want 1", got)
	}
	if got := ownerCountFilesWithSuffix(t, root, ".delivery-decision.json") -
		beforeDecisions; got != 1 {
		t.Fatalf("concurrent route decision publications = %d, want 1", got)
	}
	if got := ownerCountFilesWithSuffix(t, root, ".route-reevaluation.json") -
		beforeReevaluations; got != 1 {
		t.Fatalf("concurrent route reevaluation publications = %d, want 1", got)
	}
	beforeReplay := ownerTreeContentFingerprint(t, root)
	replayed, err := coordinator.ReevaluateDeliveryRoute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	probeCalls, _ = probe.snapshot()
	if replayed.ReevaluationRef != first.ReevaluationRef ||
		probeCalls != 1 ||
		ownerTreeContentFingerprint(t, root) != beforeReplay {
		t.Fatal("exact concurrent winner replay re-probed or published authority")
	}
}

func TestOwnerCoordinatorRouteReevaluationRechecksActivationUnderOwnerLock(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "route-activation-under-lock")
	destination := deliveryadmission.DestinationBinding{
		RepositoryRef:    fixture.repositoryRef,
		TargetRef:        "refs/heads/main",
		ObservedRevision: "git:base/1",
		DefaultBranch:    true,
	}
	scopeDigest := ownerTestRef("route-activation-scope")
	sourceAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RoutePRWithoutIssue,
		"activation-source",
		scopeDigest,
		destination,
	)
	before, sourceDecisionRef := ownerPrepareTerminalRouteDecision(
		t,
		fixture,
		sourceAdmission,
	)
	targetAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RouteDirectMain,
		"activation-target",
		scopeDigest,
		destination,
	)
	probe := &ownerTestRouteReevaluationProbe{
		repositoryRef: fixture.repositoryRef,
		now:           fixture.now,
	}
	coordinator := ownerCoordinatorWithRouteProbe(t, fixture, probe)
	openCalls := 0
	openRepository := coordinator.pad.open
	coordinator.pad.open = func(
		ctx context.Context,
		authority *PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		openCalls++
		return openRepository(ctx, authority)
	}
	firstGuard := make(chan struct{})
	activation := &ownerTestMutableActivationResolver{
		mode:  ActivationEnabled,
		first: firstGuard,
	}
	coordinator.activation = activation
	lock, err := acquirePADDeliveryOwnerLock(
		context.Background(),
		coordinator.work.Dir,
	)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	beforeContent := ownerTreeContentFingerprint(t, root)
	beforeGates := ownerCountFilesWithSuffix(t, root, ".gate-evidence.json")
	beforeDecisions := ownerCountFilesWithSuffix(t, root, ".delivery-decision.json")
	beforeReevaluations := ownerCountFilesWithSuffix(t, root, ".route-reevaluation.json")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, callErr := coordinator.ReevaluateDeliveryRoute(
			ctx,
			OwnerDeliveryRouteReevaluationRequest{
				ExpectedRevision:           before.Revision,
				SourceDecisionRef:          sourceDecisionRef,
				TargetAdmissionDecisionRef: targetAdmission.Admission.AdmissionDecisionRef,
				Mechanism:                  deliveryadmission.MechanismFastForwardOnly,
			},
		)
		result <- callErr
	}()
	<-firstGuard
	activation.set(ActivationDisabled)
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("post-lock activation error = %v", err)
	}
	probeCalls, _ := probe.snapshot()
	if probeCalls != 0 {
		t.Fatalf("disabled-under-lock route reevaluation probed %d times", probeCalls)
	}
	if openCalls != 0 {
		t.Fatalf("disabled-under-lock route reevaluation opened PAD %d times", openCalls)
	}
	if ownerCountFilesWithSuffix(t, root, ".gate-evidence.json") != beforeGates ||
		ownerCountFilesWithSuffix(t, root, ".delivery-decision.json") !=
			beforeDecisions ||
		ownerCountFilesWithSuffix(t, root, ".route-reevaluation.json") !=
			beforeReevaluations ||
		ownerTreeContentFingerprint(t, root) != beforeContent {
		t.Fatal("disabled-under-lock route reevaluation published PAD authority")
	}
	after, err := coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision ||
		after.DeliveryRouteReevaluationRef != "" {
		t.Fatalf("disabled-under-lock WorkRun state = %#v", after)
	}
}

func TestOwnerDeliveryLockSerializesRouteChangeAgainstAuthorizationIssuance(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "route-vs-authorization")
	destination := deliveryadmission.DestinationBinding{
		RepositoryRef:    fixture.repositoryRef,
		TargetRef:        "refs/heads/main",
		ObservedRevision: "git:base/1",
		DefaultBranch:    true,
	}
	scopeDigest := ownerTestRef("route-vs-authorization-scope")
	sourceAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RoutePRWithoutIssue,
		"route-vs-auth-source",
		scopeDigest,
		destination,
	)
	before, sourceDecisionRef := ownerPrepareTerminalRouteDecision(
		t,
		fixture,
		sourceAdmission,
	)
	targetAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RouteDirectMain,
		"route-vs-auth-target",
		scopeDigest,
		destination,
	)
	entered := make(chan struct{})
	release := make(chan struct{})
	probe := &ownerTestRouteReevaluationProbe{
		repositoryRef: fixture.repositoryRef,
		now:           fixture.now,
		entered:       entered,
		release:       release,
	}
	coordinator := ownerCoordinatorWithRouteProbe(t, fixture, probe)
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	beforeAuthorizations := ownerCountFilesWithSuffix(t, root, ".authorization.json")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	routeResult := make(chan OwnerDeliveryRouteReevaluation, 1)
	routeFailure := make(chan error, 1)
	go func() {
		result, err := coordinator.ReevaluateDeliveryRoute(
			ctx,
			OwnerDeliveryRouteReevaluationRequest{
				ExpectedRevision:           before.Revision,
				SourceDecisionRef:          sourceDecisionRef,
				TargetAdmissionDecisionRef: targetAdmission.Admission.AdmissionDecisionRef,
				Mechanism:                  deliveryadmission.MechanismFastForwardOnly,
			},
		)
		if err != nil {
			routeFailure <- err
			return
		}
		routeResult <- result
	}()
	<-entered
	authorizationFailure := make(chan error, 1)
	go func() {
		_, err := coordinator.IssueAndBindDeliveryAuthorization(
			ctx,
			OwnerIssueDeliveryAuthorizationRequest{
				ExpectedRevision: before.Revision,
				DecisionRef:      sourceDecisionRef,
			},
		)
		authorizationFailure <- err
	}()
	close(release)
	var route OwnerDeliveryRouteReevaluation
	select {
	case err := <-routeFailure:
		t.Fatalf("route reevaluation lost shared delivery lock: %v", err)
	case route = <-routeResult:
	}
	if route.State.DeliveryRouteReevaluationRef == "" {
		t.Fatalf("route reevaluation did not bind: %#v", route.State)
	}
	if err := <-authorizationFailure; !errors.Is(
		err,
		workrun.ErrWorkRunRevisionConflict,
	) {
		t.Fatalf("stale source authorization error = %v", err)
	}
	if got := ownerCountFilesWithSuffix(t, root, ".authorization.json"); got != beforeAuthorizations {
		t.Fatalf(
			"stale source authorization published %d objects, want %d",
			got,
			beforeAuthorizations,
		)
	}
	status, err := coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Revision != route.State.Revision ||
		status.DeliveryIntentRef != targetAdmission.Admission.IntentRef ||
		status.DeliveryAuthorizationRef != "" {
		t.Fatalf("shared delivery lock final state = %#v", status)
	}
}

func TestOwnerCoordinatorStaleRouteUnderDeliveryLockPublishesNoPADAuthority(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "stale-route-under-lock")
	destination := deliveryadmission.DestinationBinding{
		RepositoryRef:    fixture.repositoryRef,
		TargetRef:        "refs/heads/main",
		ObservedRevision: "git:base/1",
		DefaultBranch:    true,
	}
	scopeDigest := ownerTestRef("stale-route-under-lock-scope")
	sourceAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RoutePRWithoutIssue,
		"stale-route-source",
		scopeDigest,
		destination,
	)
	before, sourceDecisionRef := ownerPrepareTerminalRouteDecision(
		t,
		fixture,
		sourceAdmission,
	)
	targetAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RouteDirectMain,
		"stale-route-target",
		scopeDigest,
		destination,
	)
	probe := &ownerTestRouteReevaluationProbe{
		repositoryRef: fixture.repositoryRef,
		now:           fixture.now,
	}
	routeCoordinator := ownerCoordinatorWithRouteProbe(t, fixture, probe)

	control := newOwnerAuthorizationRaceControl(fixture.now)
	originalOpen := routeCoordinator.pad.open
	var openMu sync.Mutex
	openCalls := 0
	routeCoordinator.pad.open = func(
		ctx context.Context,
		authority *PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		openMu.Lock()
		openCalls++
		openMu.Unlock()
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

	root := filepath.Join(fixture.commonDir, "gentle-ai")
	beforeGates := ownerCountFilesWithSuffix(t, root, ".gate-evidence.json")
	beforeDecisions := ownerCountFilesWithSuffix(t, root, ".delivery-decision.json")
	beforeReevaluations := ownerCountFilesWithSuffix(
		t,
		root,
		".route-reevaluation.json",
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	authorizationResult := make(chan workrun.WorkRunState, 1)
	authorizationFailure := make(chan error, 1)
	go func() {
		state, err := fixture.coordinator.IssueAndBindDeliveryAuthorization(
			ctx,
			OwnerIssueDeliveryAuthorizationRequest{
				ExpectedRevision: before.Revision,
				DecisionRef:      sourceDecisionRef,
			},
		)
		if err != nil {
			authorizationFailure <- err
			return
		}
		authorizationResult <- state
	}()
	<-control.firstClockRead

	firstGuard := make(chan struct{})
	routeCoordinator.activation = &ownerTestMutableActivationResolver{
		mode:  ActivationEnabled,
		first: firstGuard,
	}
	routeFailure := make(chan error, 1)
	go func() {
		_, err := routeCoordinator.ReevaluateDeliveryRoute(
			ctx,
			OwnerDeliveryRouteReevaluationRequest{
				ExpectedRevision:           before.Revision,
				SourceDecisionRef:          sourceDecisionRef,
				TargetAdmissionDecisionRef: targetAdmission.Admission.AdmissionDecisionRef,
				Mechanism:                  deliveryadmission.MechanismFastForwardOnly,
			},
		)
		routeFailure <- err
	}()
	<-firstGuard
	time.Sleep(3 * padDeliveryOwnerLockPoll)
	close(control.releaseFirstClock)

	var delivered workrun.WorkRunState
	select {
	case err := <-authorizationFailure:
		t.Fatalf("authorization winner error = %v", err)
	case delivered = <-authorizationResult:
	}
	if delivered.DeliveryAuthorizationRef == "" {
		t.Fatalf("authorization winner state = %#v", delivered)
	}
	if err := <-routeFailure; !errors.Is(
		err,
		workrun.ErrWorkRunRevisionConflict,
	) {
		t.Fatalf("stale route under lock error = %v", err)
	}
	probeCalls, _ := probe.snapshot()
	if probeCalls != 0 {
		t.Fatalf("stale route under lock probed %d times", probeCalls)
	}
	openMu.Lock()
	gotOpenCalls := openCalls
	openMu.Unlock()
	if gotOpenCalls != 2 {
		t.Fatalf(
			"PAD opens = %d, want only authorization issue and bind opens",
			gotOpenCalls,
		)
	}
	if ownerCountFilesWithSuffix(t, root, ".gate-evidence.json") != beforeGates ||
		ownerCountFilesWithSuffix(t, root, ".delivery-decision.json") !=
			beforeDecisions ||
		ownerCountFilesWithSuffix(t, root, ".route-reevaluation.json") !=
			beforeReevaluations {
		t.Fatal("stale route under lock published PAD route authority")
	}
	status, err := routeCoordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(status, delivered) ||
		status.DeliveryRouteReevaluationRef != "" {
		t.Fatalf("stale route under lock final state = %#v", status)
	}
}

func TestOwnerCoordinatorPreparedAuthorizationBlocksRouteReevaluation(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(
		t,
		"prepared-authorization-blocks-route",
	)
	ctx := context.Background()
	destination := deliveryadmission.DestinationBinding{
		RepositoryRef:    fixture.repositoryRef,
		TargetRef:        "refs/heads/main",
		ObservedRevision: "git:base/1",
		DefaultBranch:    true,
	}
	scopeDigest := ownerTestRef("prepared-authorization-route-scope")
	sourceAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RoutePRWithoutIssue,
		"prepared-authorization-source",
		scopeDigest,
		destination,
	)
	before, sourceDecisionRef := ownerPrepareTerminalRouteDecision(
		t,
		fixture,
		sourceAdmission,
	)
	targetAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		fixture,
		deliveryadmission.RouteDirectMain,
		"prepared-authorization-target",
		scopeDigest,
		destination,
	)
	probe := &ownerTestRouteReevaluationProbe{
		repositoryRef: fixture.repositoryRef,
		now:           fixture.now,
	}
	coordinator := ownerCoordinatorWithRouteProbe(t, fixture, probe)

	control := &ownerAuthorizationLostResponseControl{}
	control.now.Store(fixture.now)
	control.failBeforePublish.Store(true)
	originalOpen := coordinator.pad.open
	coordinator.pad.open = func(
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
	if _, err := coordinator.IssueAndBindDeliveryAuthorization(
		ctx,
		OwnerIssueDeliveryAuthorizationRequest{
			ExpectedRevision: before.Revision,
			DecisionRef:      sourceDecisionRef,
		},
	); !errors.Is(err, errOwnerAuthorizationLostResponse) {
		t.Fatalf("prepare authorization error = %v", err)
	}
	if control.publications.Load() != 1 {
		t.Fatalf(
			"prepared authorization publication attempts = %d, want 1",
			control.publications.Load(),
		)
	}
	journal, err := resolveOwnerPADAuthorizationJournal(ctx, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pad.ResolveAuthorization(
		ctx,
		journal.AuthorizationRef,
	); !errors.Is(err, deliveryadmission.ErrTrustedRepositoryUnavailable) {
		t.Fatalf("unexpected prepared authorization object error = %v", err)
	}

	openCalls := 0
	coordinator.pad.open = func(
		ctx context.Context,
		authority *PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		openCalls++
		return originalOpen(ctx, authority)
	}
	root := filepath.Join(fixture.commonDir, "gentle-ai")
	beforeContent := ownerTreeContentFingerprint(t, root)
	beforeGates := ownerCountFilesWithSuffix(t, root, ".gate-evidence.json")
	beforeDecisions := ownerCountFilesWithSuffix(t, root, ".delivery-decision.json")
	beforeReevaluations := ownerCountFilesWithSuffix(
		t,
		root,
		".route-reevaluation.json",
	)
	_, err = coordinator.ReevaluateDeliveryRoute(
		ctx,
		OwnerDeliveryRouteReevaluationRequest{
			ExpectedRevision:           before.Revision,
			SourceDecisionRef:          sourceDecisionRef,
			TargetAdmissionDecisionRef: targetAdmission.Admission.AdmissionDecisionRef,
			Mechanism:                  deliveryadmission.MechanismFastForwardOnly,
		},
	)
	if !errors.Is(err, ErrPADDeliveryAuthorizationPrepared) {
		t.Fatalf("prepared authorization route error = %v", err)
	}
	probeCalls, _ := probe.snapshot()
	if probeCalls != 0 {
		t.Fatalf("prepared authorization route probed %d times", probeCalls)
	}
	if openCalls != 0 {
		t.Fatalf("prepared authorization route opened PAD %d times", openCalls)
	}
	if ownerCountFilesWithSuffix(t, root, ".gate-evidence.json") != beforeGates ||
		ownerCountFilesWithSuffix(t, root, ".delivery-decision.json") !=
			beforeDecisions ||
		ownerCountFilesWithSuffix(t, root, ".route-reevaluation.json") !=
			beforeReevaluations ||
		ownerTreeContentFingerprint(t, root) != beforeContent {
		t.Fatal("prepared authorization route changed durable authority")
	}
	after, err := coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("prepared authorization route changed WorkRun: %#v", after)
	}
}

func TestOwnerCoordinatorRouteReevaluationRequiresConcreteLiveAuthority(
	t *testing.T,
) {
	fixture := newOwnerCoordinatorFixture(t, "missing-route-live-authority")
	root := fixture.commonDir
	before := ownerTreeFingerprint(t, root)
	_, err := fixture.coordinator.ReevaluateDeliveryRoute(
		context.Background(),
		OwnerDeliveryRouteReevaluationRequest{
			ExpectedRevision:           ownerTestRef("revision"),
			SourceDecisionRef:          ownerTestRef("source-decision"),
			TargetAdmissionDecisionRef: ownerTestRef("target-admission"),
			Mechanism:                  deliveryadmission.MechanismPullRequest,
		},
	)
	if !errors.Is(err, ErrPADRouteReevaluationUnavailable) {
		t.Fatalf("missing live route authority error = %v", err)
	}
	if after := ownerTreeFingerprint(t, root); after != before {
		t.Fatalf(
			"missing live authority published state:\nbefore %s\nafter  %s",
			before,
			after,
		)
	}
}

func TestOwnerRouteReevaluationRequestHasNoCallerAuthoritySurface(
	t *testing.T,
) {
	typ := reflect.TypeOf(OwnerDeliveryRouteReevaluationRequest{})
	want := []string{
		"ExpectedRevision",
		"SourceDecisionRef",
		"TargetAdmissionDecisionRef",
		"Mechanism",
	}
	if typ.NumField() != len(want) {
		t.Fatalf("route reevaluation request fields = %d, want %d", typ.NumField(), len(want))
	}
	for index, name := range want {
		if typ.Field(index).Name != name {
			t.Fatalf("route reevaluation field %d = %q, want %q", index, typ.Field(index).Name, name)
		}
	}
}

type ownerProductiveRouteReevaluationFixture struct {
	owner             ownerCoordinatorFixture
	coordinator       *OwnerCoordinator
	before            workrun.WorkRunState
	sourceDecisionRef string
	targetAdmission   ownerAdmittedDelivery
	catalog           *PADCandidateCatalog
	sourceAuthority   PADCandidateAuthority
	sourceBinding     PADGitBinding
	targetBinding     PADGitBinding
	bindings          *ownerProductiveRouteBindingAuthority
	hosting           *ownerProductiveRouteHostingAuthority
}

func (fixture ownerProductiveRouteReevaluationFixture) request() OwnerDeliveryRouteReevaluationRequest {
	return OwnerDeliveryRouteReevaluationRequest{
		ExpectedRevision:           fixture.before.Revision,
		SourceDecisionRef:          fixture.sourceDecisionRef,
		TargetAdmissionDecisionRef: fixture.targetAdmission.Admission.AdmissionDecisionRef,
		Mechanism:                  deliveryadmission.MechanismFastForwardOnly,
	}
}

func newOwnerProductiveRouteReevaluationFixture(
	t *testing.T,
	seed string,
) ownerProductiveRouteReevaluationFixture {
	t.Helper()
	ctx := context.Background()
	owner := newOwnerCoordinatorFixture(t, seed)
	baseRevision := "git:" + strings.TrimSpace(
		ownerGit(t, owner.repo, "rev-parse", "HEAD"),
	)
	destination := deliveryadmission.DestinationBinding{
		RepositoryRef:    owner.repositoryRef,
		TargetRef:        "refs/heads/main",
		ObservedRevision: baseRevision,
		DefaultBranch:    true,
	}
	scopeDigest := ownerTestRef(seed + "-scope")
	sourceAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		owner,
		deliveryadmission.RoutePRWithoutIssue,
		seed+"-source",
		scopeDigest,
		destination,
	)
	before, legacySourceDecisionRef := ownerPrepareTerminalRouteDecision(
		t,
		owner,
		sourceAdmission,
	)
	sourceDecision, err := owner.pad.ResolveDeliveryDecision(
		ctx,
		legacySourceDecisionRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	candidateTree, err := owner.coordinator.resolveReviewedPADCandidateTree(
		ctx,
		sourceDecision.ReviewReceiptRef,
		sourceDecision.VerificationResultRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	candidateRevision := "git:" + strings.TrimSpace(
		ownerGit(
			t,
			owner.repo,
			"commit-tree",
			candidateTree,
			"-p",
			strings.TrimPrefix(baseRevision, "git:"),
			"-m",
			"reviewed productive route candidate",
		),
	)
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
	sourceBinding := PADGitBinding{
		Schema:                 PADGitBindingSchema,
		Candidate:              sourceDecision.Candidate,
		Destination:            sourceDecision.Gates.Destination,
		Mechanism:              sourceDecision.Gates.Mechanism,
		HostingRepositoryRef:   "github:owner/productive-route",
		CandidateRevision:      candidateRevision,
		ExpectedRemoteRevision: baseRevision,
		PullRequestRef:         sourceDecision.Gates.PullRequestRef,
	}
	sourceAuthority, err := catalog.BindCandidate(
		ctx,
		candidateTree,
		sourceBinding,
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceDecision.CandidateAuthorityRef = sourceAuthority.RecordRef
	sourceDecisionRef, err := owner.pad.PublishDeliveryDecision(
		ctx,
		sourceDecision,
	)
	if err != nil {
		t.Fatal(err)
	}
	targetAdmission := ownerAdmitRouteOnlyDelivery(
		t,
		owner,
		deliveryadmission.RouteDirectMain,
		seed+"-target",
		scopeDigest,
		destination,
	)
	targetBinding := sourceBinding
	targetBinding.Destination = targetAdmission.Intent.Destination
	targetBinding.Mechanism = deliveryadmission.MechanismFastForwardOnly
	targetBinding.PullRequestRef = ""
	bindings := &ownerProductiveRouteBindingAuthority{
		binding: targetBinding,
	}
	hosting := &ownerProductiveRouteHostingAuthority{}
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
	adapter.now = func() time.Time {
		return time.Unix(owner.now, 0).UTC()
	}
	coordinator, err := NewOwnerCoordinator(
		ctx,
		OwnerCoordinatorDependencies{
			WorkRun:                owner.coordinator.work,
			Coordination:           owner.coordinator.coordination,
			RAR:                    owner.coordinator.rar,
			Transitions:            owner.coordinator.transitions,
			Evidence:               owner.coordinator.evidence,
			Mutations:              owner.coordinator.mutations,
			PADAuthority:           owner.coordinator.pad,
			PADDelivery:            adapter,
			PADRouteProbe:          adapter,
			PADCandidateCatalog:    catalog,
			PADGitBindingAuthority: bindings,
			SDDAuthority:           owner.coordinator.sdd,
			LaunchAuthority:        ownerTestLaunchAuthority{},
			Activation:             StaticActivationResolver{Mode: ActivationEnabled},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ownerProductiveRouteReevaluationFixture{
		owner:             owner,
		coordinator:       coordinator,
		before:            before,
		sourceDecisionRef: sourceDecisionRef,
		targetAdmission:   targetAdmission,
		catalog:           catalog,
		sourceAuthority:   sourceAuthority,
		sourceBinding:     sourceBinding,
		targetBinding:     targetBinding,
		bindings:          bindings,
		hosting:           hosting,
	}
}

func ownerRewritePADCandidateCatalogRecord(
	t *testing.T,
	catalog *PADCandidateCatalog,
	record padCandidateCatalogRecord,
) {
	t.Helper()
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
		RepositoryRef: record.RepositoryRef,
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
}

func ownerAdmitRouteOnlyDelivery(
	t *testing.T,
	fixture ownerCoordinatorFixture,
	route deliveryadmission.Route,
	seed string,
	scopeDigest string,
	destination deliveryadmission.DestinationBinding,
) ownerAdmittedDelivery {
	t.Helper()
	ctx := context.Background()
	policy, err := deliveryadmission.NewRoutePolicy(
		"policy:"+fixture.workRunID+":"+seed,
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
	policyRef, err := fixture.pad.PublishRoutePolicy(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pad.PutLiveRoutePolicy(
		ctx,
		deliveryadmission.LiveRoutePolicyUpdate{
			Route:     route,
			PolicyRef: policyRef,
		},
	); err != nil {
		t.Fatal(err)
	}
	authority := deliveryadmission.AuthoritySignal{
		Schema:     deliveryadmission.AuthoritySignalContractV1,
		SignalID:   "signal:" + fixture.workRunID + ":" + seed,
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
		"nonce:"+fixture.workRunID+":"+seed,
		route,
		scopeDigest,
		destination,
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

func ownerPrepareTerminalRouteDecision(
	t *testing.T,
	fixture ownerCoordinatorFixture,
	admission ownerAdmittedDelivery,
) (workrun.WorkRunState, string) {
	t.Helper()
	ctx := context.Background()
	applicability, registry, plan, snapshot := ownerNotRequiredPlan(
		t,
		fixture.repo,
		"delivery-route-reevaluation",
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
	state, err = fixture.coordinator.PublishVerificationForecast(
		ctx,
		OwnerForecastRequest{
			ExpectedRevision: state.Revision,
			Handoff:          *state.Handoff,
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
		"delivery-route-reevaluation",
		snapshot,
		plan.PolicyHash,
	)
	result := reviewtransaction.VerificationResultRef{
		Schema:               reviewtransaction.VerificationResultRefSchema,
		ResultRef:            ownerTestRef("delivery-route-result"),
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
			LineageID:     "delivery-route-reevaluation",
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
	gates := deliveryadmission.GateEvidence{
		Schema: deliveryadmission.GateEvidenceContractV1,
		Route:  deliveryadmission.RoutePRWithoutIssue,
		Candidate: deliveryadmission.CandidateBinding{
			Ref:    "candidate:" + fixture.workRunID,
			Digest: state.Handoff.CandidateRef,
		},
		Destination:        admission.Intent.Destination,
		PolicyRef:          admission.PolicyRef,
		Policy:             admission.Policy.Binding(),
		Mechanism:          deliveryadmission.MechanismPullRequest,
		ProtectionMode:     deliveryadmission.ProtectionRespect,
		PullRequestRef:     "github:pr/route-source",
		RequiredChecksRef:  ownerTestRef("source-required-checks"),
		RemoteFreshnessRef: ownerTestRef("source-remote-freshness"),
		ProtectionRef:      ownerTestRef("source-protection"),
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
	return state, decisionRef
}

func ownerCoordinatorWithRouteProbe(
	t *testing.T,
	fixture ownerCoordinatorFixture,
	probe PADRouteReevaluationProbe,
) *OwnerCoordinator {
	t.Helper()
	coordinator, err := NewOwnerCoordinator(
		context.Background(),
		OwnerCoordinatorDependencies{
			WorkRun:         fixture.coordinator.work,
			Coordination:    fixture.coordinator.coordination,
			RAR:             fixture.coordinator.rar,
			Transitions:     fixture.coordinator.transitions,
			Evidence:        fixture.coordinator.evidence,
			Mutations:       fixture.coordinator.mutations,
			PADAuthority:    fixture.coordinator.pad,
			PADDelivery:     fixture.coordinator.padDelivery,
			PADRouteProbe:   probe,
			SDDAuthority:    fixture.coordinator.sdd,
			LaunchAuthority: ownerTestLaunchAuthority{},
			Activation:      StaticActivationResolver{Mode: ActivationEnabled},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func ownerCountFilesWithSuffix(
	t *testing.T,
	root string,
	suffix string,
) int {
	t.Helper()
	count := 0
	if err := filepath.WalkDir(
		root,
		func(_ string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), suffix) {
				count++
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	return count
}
