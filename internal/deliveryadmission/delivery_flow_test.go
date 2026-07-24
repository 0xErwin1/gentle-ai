package deliveryadmission

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

type testLiveGateProbe struct {
	now          int64
	err          error
	mutate       func(*LiveGateProbeResult)
	varyEvidence bool
	clock        *atomic.Int64
	barrierTotal int32
	allProbed    chan struct{}
	release      chan struct{}
	calls        atomic.Int32
	mu           sync.Mutex
	produced     []LiveGateProbeResult
}

func (probe *testLiveGateProbe) ProbeLiveGate(
	ctx context.Context,
	request LiveGateProbeRequest,
) (LiveGateProbeResult, error) {
	call := probe.calls.Add(1)
	if probe.err != nil {
		return LiveGateProbeResult{}, probe.err
	}
	requestRef, err := request.Ref()
	if err != nil {
		return LiveGateProbeResult{}, err
	}
	result := LiveGateProbeResult{
		Schema:     LiveGateProbeResultContractV1,
		RequestRef: requestRef, Request: request,
		ObservedRemoteRevision: request.Destination.ObservedRevision,
		RemoteFreshnessRef:     testDigest("a"),
		ProtectionMode:         ProtectionRespect,
		ProtectionSatisfied:    true,
		ProtectionRef:          testDigest("b"),
		ObservedAt:             probe.now,
		ExpiresAt:              probe.now + 60,
	}
	if request.Mechanism == MechanismPullRequest {
		result.PullRequestRef = "github:pull/1795"
		result.RequiredChecksSatisfied = true
		result.RequiredChecksRef = testDigest("c")
	} else {
		result.FastForwardSatisfied = true
		result.FastForwardRef = testDigest("e")
	}
	if probe.varyEvidence {
		character := string("0123456789abcdef"[int(call)%16])
		result.RemoteFreshnessRef = testDigest(character)
		if probe.clock != nil {
			result.ObservedAt = probe.clock.Add(1)
			result.ExpiresAt = result.ObservedAt + 60
		}
	}
	if probe.mutate != nil {
		probe.mutate(&result)
	}
	probe.mu.Lock()
	probe.produced = append(probe.produced, result)
	probe.mu.Unlock()
	if probe.barrierTotal > 0 {
		if call == probe.barrierTotal {
			close(probe.allProbed)
		}
		select {
		case <-probe.release:
		case <-ctx.Done():
			return LiveGateProbeResult{}, ctx.Err()
		}
	}
	return result, nil
}

func (probe *testLiveGateProbe) producedResults() []LiveGateProbeResult {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	return append([]LiveGateProbeResult(nil), probe.produced...)
}

type atomicClockResolver struct {
	TrustedResolver
	now *atomic.Int64
}

func (resolver atomicClockResolver) CurrentUnix(context.Context) (int64, error) {
	return resolver.now.Load(), nil
}

type postProbeExpiryResolver struct {
	TrustedResolver
	now   int64
	phase atomic.Int32
}

func (resolver *postProbeExpiryResolver) CurrentUnix(context.Context) (int64, error) {
	if resolver.phase.CompareAndSwap(1, 2) {
		return resolver.now, nil
	}
	if resolver.phase.Load() >= 2 {
		return resolver.now + 1, nil
	}
	return resolver.now, nil
}

type testDeliveryExecutor struct {
	now                  int64
	failBeforeEffect     error
	failuresBeforeEffect int
	mu                   sync.Mutex
	results              map[string]ExecutionResult
	calls                int
	effects              int
	last                 ExecutionCommand
}

func (executor *testDeliveryExecutor) ExecuteOnce(
	_ context.Context,
	command ExecutionCommand,
) (ExecutionResult, error) {
	commandRef, err := command.Ref()
	if err != nil {
		return ExecutionResult{}, err
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.calls++
	executor.last = command
	if executor.results == nil {
		executor.results = make(map[string]ExecutionResult)
	}
	if result, ok := executor.results[commandRef]; ok {
		return result, nil
	}
	if executor.now >= command.ExecutionExpiresAt {
		return ExecutionResult{}, ErrExpired
	}
	if executor.failuresBeforeEffect > 0 {
		executor.failuresBeforeEffect--
		return ExecutionResult{}, executor.failBeforeEffect
	}
	executor.effects++
	result := ExecutionResult{
		Schema:     ExecutionResultContractV1,
		CommandRef: commandRef, AuthorizationRef: command.AuthorizationRef,
		Route: command.Route, Candidate: command.Candidate,
		Outcome: ExecutionSucceeded, DeliveryRef: "git:update/1",
		EvidenceRef: testDigest("d"), CompletedAt: executor.now,
	}
	executor.results[commandRef] = result
	return result, nil
}

type testRouteReevaluationRepository struct {
	*trustedRepository
}

func (repository *testRouteReevaluationRepository) PublishGateEvidence(
	_ context.Context,
	value GateEvidence,
) (string, error) {
	ref, err := value.Ref()
	if err != nil {
		return "", err
	}
	repository.gates[ref] = value
	return ref, nil
}

func (repository *testRouteReevaluationRepository) PublishDeliveryDecision(
	_ context.Context,
	value DeliveryDecision,
) (string, error) {
	ref, err := value.Ref()
	if err != nil {
		return "", err
	}
	repository.decisions[ref] = value
	return ref, nil
}

func TestSelectDeliveryIntentDefaultsToApprovedIssueRoute(t *testing.T) {
	repository := newTrustedRepository()
	destination := DestinationBinding{
		RepositoryRef: "github:gentle-ai", TargetRef: "refs/heads/main",
		ObservedRevision: "git:base/1", DefaultBranch: true,
	}
	policy, err := NewRoutePolicy(
		"policy:pr-with-issue",
		"revision:1",
		RoutePRWithIssue,
		true,
		false,
		false,
		120,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyRef := putPolicy(t, repository, policy)
	repository.livePolicies[livePolicyKey{
		repository: destination.RepositoryRef,
		route:      RoutePRWithIssue,
	}] = policyRef

	intent, err := SelectDeliveryIntent(
		context.Background(),
		repository,
		IntentSelectionRequest{
			Nonce: "nonce:default-route", ScopeDigest: testDigest("1"),
			Destination: destination,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Route != RoutePRWithIssue || intent.PolicyRef != policyRef {
		t.Fatalf("selected route/policy = %q/%q", intent.Route, intent.PolicyRef)
	}
	intentRef := mustRef(t, intent)
	repository.intents[intentRef] = intent
	authority := AuthoritySignal{
		Schema: AuthoritySignalContractV1, SignalID: "signal:repository",
		IssuerRef:  "repository-policy:gentle-ai",
		Provenance: ProvenanceRepositoryPolicy,
		PolicyRef:  policyRef, Policy: policy.Binding(), ExpiresAt: testNow + 100,
	}
	authorityRef := putAuthority(t, repository, authority)

	if _, err := Admit(
		context.Background(),
		repository,
		AdmissionRequest{
			IntentRef: intentRef, PolicyRef: policyRef, AuthorityRef: authorityRef,
		},
	); err == nil {
		t.Fatal("default PR-with-issue admission succeeded without approved issue governance")
	}
	governance := GovernanceDecision{
		Schema: GovernanceDecisionContractV1, Kind: GovernanceApprovedIssue,
		DecisionID: "governance:issue/1794", SubjectRef: intentRef,
		Route: RoutePRWithIssue, RepositoryRef: destination.RepositoryRef,
		Destination: destination, PolicyRef: policyRef, Policy: policy.Binding(),
		AuthorityRef: authorityRef, ApprovedIssueRef: "github:issue/1794",
		ExpiresAt: testNow + 100,
	}
	governanceRef := putGovernance(t, repository, governance)
	admission, err := Admit(
		context.Background(),
		repository,
		AdmissionRequest{
			IntentRef: intentRef, PolicyRef: policyRef, GovernanceRef: governanceRef,
			AuthorityRef: authorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if admission.Disposition != AdmissionAdmitted ||
		admission.IssueAdmission != IssueApproved {
		t.Fatalf(
			"default admission = %q/%q",
			admission.Disposition,
			admission.IssueAdmission,
		)
	}
}

func TestSelectDeliveryIntentFailsClosedWithoutEnabledDefault(t *testing.T) {
	for _, test := range []struct {
		name           string
		requestedRoute Route
		policyRoute    Route
		enabled        bool
	}{
		{name: "disabled default", policyRoute: RoutePRWithIssue, enabled: false},
		{name: "unknown explicit route", requestedRoute: Route("force_main"), policyRoute: RoutePRWithIssue, enabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := newTrustedRepository()
			destination := DestinationBinding{
				RepositoryRef: "github:gentle-ai", TargetRef: "refs/heads/main",
				ObservedRevision: "git:base/1", DefaultBranch: true,
			}
			policy, err := NewRoutePolicy(
				"policy:fail-closed",
				"revision:1",
				test.policyRoute,
				test.enabled,
				false,
				false,
				120,
				0,
			)
			if err != nil {
				t.Fatal(err)
			}
			policyRef := putPolicy(t, repository, policy)
			repository.livePolicies[livePolicyKey{
				repository: destination.RepositoryRef,
				route:      test.policyRoute,
			}] = policyRef
			_, err = SelectDeliveryIntent(
				context.Background(),
				repository,
				IntentSelectionRequest{
					Nonce: "nonce:fail-closed", RequestedRoute: test.requestedRoute,
					ScopeDigest: testDigest("1"), Destination: destination,
				},
			)
			if err == nil {
				t.Fatal("unsafe route selection succeeded")
			}
		})
	}
}

func TestDirectMainLiveProbeFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*LiveGateProbeResult)
		err    error
	}{
		{
			name: "stale remote",
			mutate: func(result *LiveGateProbeResult) {
				result.ObservedRemoteRevision = "git:changed"
			},
		},
		{
			name: "protection denied",
			mutate: func(result *LiveGateProbeResult) {
				result.ProtectionSatisfied = false
			},
		},
		{
			name: "non-fast-forward update",
			mutate: func(result *LiveGateProbeResult) {
				result.FastForwardSatisfied = false
			},
		},
		{
			name: "expired observation",
			mutate: func(result *LiveGateProbeResult) {
				result.ExpiresAt = result.ObservedAt
			},
		},
		{name: "probe unavailable", err: errors.New("hosting provider unavailable")},
		{
			name: "mismatched request",
			mutate: func(result *LiveGateProbeResult) {
				result.Request.Candidate.Ref = "candidate:other"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := newScenario(
				t,
				RouteDirectMain,
				reviewtransaction.VerificationAggregateComplete,
				false,
				false,
			)
			request, err := NewLiveGateProbeRequest(
				RouteDirectMain,
				current.decision.Candidate,
				current.decision.Gates.Destination,
				current.policyRef,
				current.policy.Binding(),
				MechanismFastForwardOnly,
			)
			if err != nil {
				t.Fatal(err)
			}
			probe := &testLiveGateProbe{
				now: current.repository.now, err: test.err, mutate: test.mutate,
			}
			if _, err := ProbeGateEvidence(
				context.Background(),
				current.repository,
				probe,
				request,
			); err == nil {
				t.Fatal("unsafe live gate probe was accepted")
			}
		})
	}
}

func TestProbeGateEvidenceAcceptsObservationMadeDuringProbe(t *testing.T) {
	current := newScenario(
		t,
		RouteDirectMain,
		reviewtransaction.VerificationAggregateComplete,
		false,
		false,
	)
	request, err := NewLiveGateProbeRequest(
		RouteDirectMain,
		current.decision.Candidate,
		current.decision.Gates.Destination,
		current.policyRef,
		current.policy.Binding(),
		MechanismFastForwardOnly,
	)
	if err != nil {
		t.Fatal(err)
	}
	observedAt := current.repository.now + 1
	probe := &testLiveGateProbe{
		now: current.repository.now,
		mutate: func(result *LiveGateProbeResult) {
			current.repository.now = observedAt
			result.ObservedAt = observedAt
			result.ExpiresAt = observedAt + 60
		},
	}

	gates, err := ProbeGateEvidence(
		context.Background(),
		current.repository,
		probe,
		request,
	)
	if err != nil {
		t.Fatalf("observation made during probe was rejected: %v", err)
	}
	if gates.ObservedAt != observedAt || gates.ExpiresAt != observedAt+60 {
		t.Fatalf(
			"accepted probe window = %d..%d, want %d..%d",
			gates.ObservedAt,
			gates.ExpiresAt,
			observedAt,
			observedAt+60,
		)
	}
}

func TestReevaluateDeliveryRouteReusesContentAndRecomputesGovernance(t *testing.T) {
	source := newScenario(
		t,
		RoutePRWithIssue,
		reviewtransaction.VerificationAggregateComplete,
		false,
		false,
	)
	targetAdmissionRef, _ := addDirectMainTargetAdmission(
		t,
		source,
		source.decision.Gates.Destination,
		source.decision.AdmissionDecision.Intent.ScopeDigest,
	)
	repository := &testRouteReevaluationRepository{
		trustedRepository: source.repository,
	}
	probe := &testLiveGateProbe{now: source.repository.now}
	request := RouteReevaluationRequest{
		SourceDecisionRef:          source.decisionRef,
		TargetAdmissionDecisionRef: targetAdmissionRef,
		Mechanism:                  MechanismFastForwardOnly,
	}

	first, err := ReevaluateDeliveryRoute(
		context.Background(),
		repository,
		repository,
		probe,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReevaluateDeliveryRoute(
		context.Background(),
		repository,
		repository,
		probe,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("exact reevaluation replay changed\nfirst:  %#v\nsecond: %#v", first, second)
	}
	decision := repository.decisions[first.DecisionRef]
	if first.SourceRoute != RoutePRWithIssue || first.TargetRoute != RouteDirectMain ||
		decision.Candidate != source.decision.Candidate ||
		decision.ReviewReceiptRef != source.receiptRef ||
		decision.VerificationResultRef != source.resultRef ||
		decision.AdmissionDecisionRef != targetAdmissionRef ||
		decision.Policy.Route != RouteDirectMain ||
		decision.Gates.Mechanism != MechanismFastForwardOnly {
		t.Fatalf("route-only reevaluation widened content authority: %#v", first)
	}
	if probe.calls.Load() != 2 {
		t.Fatalf("live governance probes = %d, want 2", probe.calls.Load())
	}
}

func TestReevaluateDeliveryRouteRejectsScopeOrDestinationChange(t *testing.T) {
	source := newScenario(
		t,
		RoutePRWithIssue,
		reviewtransaction.VerificationAggregateComplete,
		false,
		false,
	)
	tests := []struct {
		name        string
		destination DestinationBinding
		scope       string
	}{
		{
			name:        "scope",
			destination: source.decision.Gates.Destination,
			scope:       testDigest("f"),
		},
		{
			name: "destination revision",
			destination: func() DestinationBinding {
				value := source.decision.Gates.Destination
				value.ObservedRevision = "git:other"
				return value
			}(),
			scope: source.decision.AdmissionDecision.Intent.ScopeDigest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			targetAdmissionRef, _ := addDirectMainTargetAdmission(
				t,
				source,
				test.destination,
				test.scope,
			)
			repository := &testRouteReevaluationRepository{
				trustedRepository: source.repository,
			}
			probe := &testLiveGateProbe{now: source.repository.now}
			_, err := ReevaluateDeliveryRoute(
				context.Background(),
				repository,
				repository,
				probe,
				RouteReevaluationRequest{
					SourceDecisionRef:          source.decisionRef,
					TargetAdmissionDecisionRef: targetAdmissionRef,
					Mechanism:                  MechanismFastForwardOnly,
				},
			)
			if !errors.Is(err, ErrBindingMismatch) {
				t.Fatalf("route-only widening error = %v, want ErrBindingMismatch", err)
			}
			if probe.calls.Load() != 0 {
				t.Fatalf("invalid route change launched %d probes", probe.calls.Load())
			}
		})
	}
}

func addDirectMainTargetAdmission(
	t *testing.T,
	source scenario,
	destination DestinationBinding,
	scopeDigest string,
) (string, string) {
	t.Helper()
	policy, err := NewRoutePolicy(
		"policy:direct-main-reevaluation",
		"revision:1",
		RouteDirectMain,
		true,
		false,
		false,
		120,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyRef := putPolicy(t, source.repository, policy)
	source.repository.livePolicies[livePolicyKey{
		repository: destination.RepositoryRef,
		route:      RouteDirectMain,
	}] = policyRef
	authority := AuthoritySignal{
		Schema: AuthoritySignalContractV1, SignalID: "signal:direct-reevaluation",
		IssuerRef: "maintainer:one", Provenance: ProvenanceMaintainerControl,
		PolicyRef: policyRef, Policy: policy.Binding(), ExpiresAt: testNow + 1_000,
	}
	authorityRef := putAuthority(t, source.repository, authority)
	intent, err := NewIntent(
		"nonce:direct-reevaluation/"+scopeDigest[len(scopeDigest)-1:],
		RouteDirectMain,
		scopeDigest,
		destination,
		policyRef,
		policy.Binding(),
		source.repository.now,
	)
	if err != nil {
		t.Fatal(err)
	}
	intentRef := mustRef(t, intent)
	source.repository.intents[intentRef] = intent
	admission, err := Admit(
		context.Background(),
		source.repository,
		AdmissionRequest{
			IntentRef: intentRef, PolicyRef: policyRef, AuthorityRef: authorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	admissionRef := mustRef(t, admission)
	source.repository.admissions[admissionRef] = admission
	return admissionRef, authorityRef
}

func TestExecuteAuthorizedDirectMainReprobesAndReplaysOneEffect(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t,
		current.repository,
		authorization.Binding.Destination.RepositoryRef,
	)
	probe := &testLiveGateProbe{now: current.repository.now}
	executor := &testDeliveryExecutor{now: current.repository.now}
	request := ExecuteDeliveryRequest{
		AuthorizationRef: authorizationRef, AuthorizationKind: AuthorizationNormal,
	}
	first, err := ExecuteAuthorizedDelivery(
		context.Background(),
		current.repository,
		current.repository,
		probe,
		executor,
		store,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ExecuteAuthorizedDelivery(
		context.Background(),
		current.repository,
		current.repository,
		probe,
		executor,
		store,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("execution replay changed\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if probe.calls.Load() != 1 {
		t.Fatalf("live probes = %d, want one before first effect", probe.calls.Load())
	}
	if executor.calls != 2 || executor.effects != 1 {
		t.Fatalf(
			"executor calls/effects = %d/%d, want 2/1",
			executor.calls,
			executor.effects,
		)
	}
	if executor.last.Mechanism != MechanismFastForwardOnly ||
		executor.last.ProtectionMode != ProtectionRespect ||
		executor.last.PullRequestRef != "" ||
		executor.last.ExpectedRemoteRevision != authorization.Binding.Destination.ObservedRevision {
		t.Fatalf("direct-main command can weaken delivery: %#v", executor.last)
	}
}

func TestExecuteAuthorizedDeliveryRejectsLiveGateExpiringBeforeReservation(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t,
		current.repository,
		authorization.Binding.Destination.RepositoryRef,
	)
	resolver := &postProbeExpiryResolver{
		TrustedResolver: current.repository,
		now:             current.repository.now,
	}
	probe := &testLiveGateProbe{
		now: current.repository.now,
		mutate: func(result *LiveGateProbeResult) {
			result.ExpiresAt = result.ObservedAt + 1
			resolver.phase.Store(1)
		},
	}
	executor := &testDeliveryExecutor{now: current.repository.now}

	_, err := ExecuteAuthorizedDelivery(
		context.Background(),
		resolver,
		current.repository,
		probe,
		executor,
		store,
		ExecuteDeliveryRequest{
			AuthorizationRef:  authorizationRef,
			AuthorizationKind: AuthorizationNormal,
		},
	)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("post-probe expiry error = %v, want ErrExpired", err)
	}
	if executor.calls != 0 || executor.effects != 0 {
		t.Fatalf(
			"expired evidence reached executor/effect = %d/%d",
			executor.calls,
			executor.effects,
		)
	}
	if _, consumed, readErr := store.authorizationUse(
		context.Background(),
		authorizationRef,
	); readErr != nil || consumed {
		t.Fatalf("expired live gate reserved authorization: %v/%v", consumed, readErr)
	}
}

func TestExecuteAuthorizedDeliveryRejectsResumedFirstEffectAfterLiveGateExpiry(
	t *testing.T,
) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t,
		current.repository,
		authorization.Binding.Destination.RepositoryRef,
	)
	probe := &testLiveGateProbe{now: current.repository.now}
	executor := &testDeliveryExecutor{
		now:                  current.repository.now,
		failBeforeEffect:     errors.New("executor unavailable before effect"),
		failuresBeforeEffect: 1,
	}
	request := ExecuteDeliveryRequest{
		AuthorizationRef: authorizationRef, AuthorizationKind: AuthorizationNormal,
	}

	_, err := ExecuteAuthorizedDelivery(
		context.Background(),
		current.repository,
		current.repository,
		probe,
		executor,
		store,
		request,
	)
	if !errors.Is(err, ErrExecutionRejected) {
		t.Fatalf("initial executor interruption = %v, want ErrExecutionRejected", err)
	}
	use, consumed, err := store.authorizationUse(context.Background(), authorizationRef)
	if err != nil || !consumed {
		t.Fatalf("durable interrupted reservation = %v/%v", consumed, err)
	}
	if use.ExecutionExpiresAt <= use.ConsumedAt ||
		use.ExecutionExpiresAt >= authorization.Binding.ExpiresAt {
		t.Fatalf(
			"test requires live expiry between reservation and authorization expiry: %#v",
			use,
		)
	}
	if executor.last.ExecutionExpiresAt != use.ExecutionExpiresAt {
		t.Fatalf(
			"executor expiry = %d, want durable %d",
			executor.last.ExecutionExpiresAt,
			use.ExecutionExpiresAt,
		)
	}

	current.repository.now = use.ExecutionExpiresAt
	executor.now = use.ExecutionExpiresAt
	_, err = ExecuteAuthorizedDelivery(
		context.Background(),
		current.repository,
		current.repository,
		probe,
		executor,
		store,
		request,
	)
	if !errors.Is(err, ErrExecutionRejected) || !errors.Is(err, ErrExpired) {
		t.Fatalf(
			"resumed first effect error = %v, want ErrExecutionRejected and ErrExpired",
			err,
		)
	}
	if probe.calls.Load() != 1 || executor.calls != 2 || executor.effects != 0 {
		t.Fatalf(
			"resumed expired probe/executor/effects = %d/%d/%d, want 1/2/0",
			probe.calls.Load(),
			executor.calls,
			executor.effects,
		)
	}
}

func TestExecuteAuthorizedDeliveryBoundsFirstEffectByAuthorizationExpiry(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t,
		current.repository,
		authorization.Binding.Destination.RepositoryRef,
	)
	probe := &testLiveGateProbe{
		now: current.repository.now,
		mutate: func(result *LiveGateProbeResult) {
			result.ExpiresAt = authorization.Binding.ExpiresAt + 30
		},
	}
	executor := &testDeliveryExecutor{
		now:                  current.repository.now,
		failBeforeEffect:     errors.New("executor unavailable before effect"),
		failuresBeforeEffect: 1,
	}
	request := ExecuteDeliveryRequest{
		AuthorizationRef: authorizationRef, AuthorizationKind: AuthorizationNormal,
	}

	_, err := ExecuteAuthorizedDelivery(
		context.Background(),
		current.repository,
		current.repository,
		probe,
		executor,
		store,
		request,
	)
	if !errors.Is(err, ErrExecutionRejected) {
		t.Fatalf("initial executor interruption = %v, want ErrExecutionRejected", err)
	}
	produced := probe.producedResults()
	if len(produced) != 1 ||
		produced[0].ExpiresAt <= authorization.Binding.ExpiresAt {
		t.Fatalf(
			"test requires probe expiry after authorization expiry: %#v",
			produced,
		)
	}
	use, consumed, err := store.authorizationUse(context.Background(), authorizationRef)
	if err != nil || !consumed {
		t.Fatalf("durable interrupted reservation = %v/%v", consumed, err)
	}
	if use.ExecutionExpiresAt != authorization.Binding.ExpiresAt ||
		executor.last.ExecutionExpiresAt != authorization.Binding.ExpiresAt {
		t.Fatalf(
			"effective execution expiry use/command = %d/%d, want authorization %d",
			use.ExecutionExpiresAt,
			executor.last.ExecutionExpiresAt,
			authorization.Binding.ExpiresAt,
		)
	}

	current.repository.now = authorization.Binding.ExpiresAt
	executor.now = authorization.Binding.ExpiresAt
	_, err = ExecuteAuthorizedDelivery(
		context.Background(),
		current.repository,
		current.repository,
		probe,
		executor,
		store,
		request,
	)
	if !errors.Is(err, ErrExecutionRejected) || !errors.Is(err, ErrExpired) {
		t.Fatalf(
			"resumed first effect error = %v, want ErrExecutionRejected and ErrExpired",
			err,
		)
	}
	if probe.calls.Load() != 1 || executor.calls != 2 || executor.effects != 0 {
		t.Fatalf(
			"authorization-expired probe/executor/effects = %d/%d/%d, want 1/2/0",
			probe.calls.Load(),
			executor.calls,
			executor.effects,
		)
	}
}

func TestExecuteAuthorizedDeliveryReplaysTerminalResultAfterLiveGateExpiry(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t,
		current.repository,
		authorization.Binding.Destination.RepositoryRef,
	)
	probe := &testLiveGateProbe{now: current.repository.now}
	executor := &testDeliveryExecutor{now: current.repository.now}
	request := ExecuteDeliveryRequest{
		AuthorizationRef: authorizationRef, AuthorizationKind: AuthorizationNormal,
	}
	first, err := ExecuteAuthorizedDelivery(
		context.Background(),
		current.repository,
		current.repository,
		probe,
		executor,
		store,
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	use, consumed, err := store.authorizationUse(context.Background(), authorizationRef)
	if err != nil || !consumed {
		t.Fatalf("durable terminal reservation = %v/%v", consumed, err)
	}
	if use.ExecutionExpiresAt >= authorization.Binding.ExpiresAt {
		t.Fatalf(
			"test requires live expiry before authorization expiry: %d >= %d",
			use.ExecutionExpiresAt,
			authorization.Binding.ExpiresAt,
		)
	}

	current.repository.now = use.ExecutionExpiresAt
	executor.now = use.ExecutionExpiresAt
	second, err := ExecuteAuthorizedDelivery(
		context.Background(),
		current.repository,
		current.repository,
		probe,
		executor,
		store,
		request,
	)
	if err != nil {
		t.Fatalf("terminal replay after live expiry: %v", err)
	}
	if second != first {
		t.Fatalf("terminal replay changed\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if probe.calls.Load() != 1 || executor.calls != 2 || executor.effects != 1 {
		t.Fatalf(
			"terminal replay probe/executor/effects = %d/%d/%d, want 1/2/1",
			probe.calls.Load(),
			executor.calls,
			executor.effects,
		)
	}
}

func TestExecuteAuthorizedDeliveryRejectsUnsafeLiveStateBeforeConsumption(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*LiveGateProbeResult)
	}{
		{
			name: "remote moved",
			mutate: func(result *LiveGateProbeResult) {
				result.ObservedRemoteRevision = "git:remote-moved"
			},
		},
		{
			name: "protection no longer permits update",
			mutate: func(result *LiveGateProbeResult) {
				result.ProtectionSatisfied = false
			},
		},
		{
			name: "candidate no longer fast-forwards",
			mutate: func(result *LiveGateProbeResult) {
				result.FastForwardSatisfied = false
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			current, authorizationRef, authorization := normalAuthorization(t)
			store := openScenarioUseStore(
				t,
				current.repository,
				authorization.Binding.Destination.RepositoryRef,
			)
			probe := &testLiveGateProbe{
				now: current.repository.now, mutate: test.mutate,
			}
			executor := &testDeliveryExecutor{now: current.repository.now}
			_, err := ExecuteAuthorizedDelivery(
				context.Background(),
				current.repository,
				current.repository,
				probe,
				executor,
				store,
				ExecuteDeliveryRequest{
					AuthorizationRef:  authorizationRef,
					AuthorizationKind: AuthorizationNormal,
				},
			)
			if err == nil {
				t.Fatal("unsafe direct-main delivery succeeded")
			}
			if executor.effects != 0 {
				t.Fatalf("unsafe delivery executed %d effects", executor.effects)
			}
			if _, consumed, readErr := store.authorizationUse(
				context.Background(),
				authorizationRef,
			); readErr != nil || consumed {
				t.Fatalf("unsafe probe consumed authorization: %v/%v", consumed, readErr)
			}
		})
	}
}

func TestExecuteAuthorizedDeliveryRejectsGenericPreconsumption(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t,
		current.repository,
		authorization.Binding.Destination.RepositoryRef,
	)
	if _, err := ConsumeAuthorization(
		context.Background(),
		current.repository,
		current.repository,
		authorizationRef,
		useRequest(authorization),
		store,
	); err != nil {
		t.Fatal(err)
	}
	probe := &testLiveGateProbe{now: current.repository.now}
	executor := &testDeliveryExecutor{now: current.repository.now}
	_, err := ExecuteAuthorizedDelivery(
		context.Background(),
		current.repository,
		current.repository,
		probe,
		executor,
		store,
		ExecuteDeliveryRequest{
			AuthorizationRef:  authorizationRef,
			AuthorizationKind: AuthorizationNormal,
		},
	)
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("generic preconsumption error = %v, want ErrBindingMismatch", err)
	}
	if probe.calls.Load() != 0 || executor.effects != 0 {
		t.Fatalf(
			"generic preconsumption reached probe/effect = %d/%d",
			probe.calls.Load(),
			executor.effects,
		)
	}
}

func TestExecuteAuthorizedDeliveryConcurrentReplayHasOneEffect(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t,
		current.repository,
		authorization.Binding.Destination.RepositoryRef,
	)
	var ownerClock atomic.Int64
	ownerClock.Store(current.repository.now)
	resolver := atomicClockResolver{
		TrustedResolver: current.repository,
		now:             &ownerClock,
	}
	const contenders = 16
	allProbed := make(chan struct{})
	releaseProbes := make(chan struct{})
	probe := &testLiveGateProbe{
		now:          current.repository.now,
		varyEvidence: true,
		clock:        &ownerClock,
		barrierTotal: contenders,
		allProbed:    allProbed,
		release:      releaseProbes,
	}
	executor := &testDeliveryExecutor{now: current.repository.now + contenders + 1}
	request := ExecuteDeliveryRequest{
		AuthorizationRef: authorizationRef, AuthorizationKind: AuthorizationNormal,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan ExecutionResult, contenders)
	failures := make(chan error, contenders)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := ExecuteAuthorizedDelivery(
				ctx,
				resolver,
				current.repository,
				probe,
				executor,
				store,
				request,
			)
			if err != nil {
				failures <- err
				return
			}
			results <- result
		}()
	}
	close(start)
	select {
	case <-allProbed:
		close(releaseProbes)
	case <-ctx.Done():
		t.Fatalf(
			"only %d/%d contenders reached their distinct live probe: %v",
			probe.calls.Load(),
			contenders,
			ctx.Err(),
		)
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatalf("concurrent delivery: %v", err)
	}
	var first *ExecutionResult
	for result := range results {
		if first == nil {
			value := result
			first = &value
			continue
		}
		if result != *first {
			t.Fatalf("concurrent replay returned distinct result")
		}
	}
	if first == nil {
		t.Fatal("concurrent delivery returned no result")
	}
	produced := probe.producedResults()
	if len(produced) != contenders {
		t.Fatalf("produced probes = %d, want %d", len(produced), contenders)
	}
	observedTimes := make(map[int64]struct{}, contenders)
	probeRefs := make(map[string]struct{}, contenders)
	for _, result := range produced {
		observedTimes[result.ObservedAt] = struct{}{}
		ref, refErr := result.Ref()
		if refErr != nil {
			t.Fatalf("probe result ref: %v", refErr)
		}
		probeRefs[ref] = struct{}{}
	}
	if len(observedTimes) != contenders || len(probeRefs) != contenders {
		t.Fatalf(
			"distinct probe ObservedAt/refs = %d/%d, want %d/%d",
			len(observedTimes),
			len(probeRefs),
			contenders,
			contenders,
		)
	}
	use, consumed, err := store.authorizationUse(ctx, authorizationRef)
	if err != nil || !consumed {
		t.Fatalf("durable execution reservation = %v/%v", consumed, err)
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.effects != 1 || executor.calls != contenders {
		t.Fatalf(
			"concurrent executor calls/effects = %d/%d, want %d/1",
			executor.calls,
			executor.effects,
			contenders,
		)
	}
	if executor.last.LiveGateRef != use.LiveGateRef {
		t.Fatalf(
			"executor replay gate = %s, want durable winner %s",
			executor.last.LiveGateRef,
			use.LiveGateRef,
		)
	}
}
