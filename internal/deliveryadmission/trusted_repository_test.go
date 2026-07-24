package deliveryadmission

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

type mutableOwnerClock struct {
	mu  sync.Mutex
	now int64
}

func (clock *mutableOwnerClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return time.Unix(clock.now, 0)
}

func (clock *mutableOwnerClock) set(now int64) {
	clock.mu.Lock()
	clock.now = now
	clock.mu.Unlock()
}

func openProductTrustedRepository(
	t *testing.T,
	repositoryRef string,
	now int64,
) (*TrustedRepository, *trustedRepository, *mutableOwnerClock) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	owner := newTrustedRepository()
	owner.repositories[repositoryRef] = filepath.Clean(absolute)
	clock := &mutableOwnerClock{now: now}
	repository, err := OpenTrustedRepository(
		context.Background(),
		owner,
		repositoryRef,
		WithTrustedRepositoryClock(clock),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repository.Close(); err != nil {
			t.Errorf("close trusted repository: %v", err)
		}
	})
	return repository, owner, clock
}

func TestTrustedRepositoryAdmitsAndResolvesAllFourRoutes(t *testing.T) {
	for _, route := range []Route{
		RoutePRWithIssue,
		RoutePRWithoutIssue,
		RouteDirectMain,
		RouteEmergency,
	} {
		t.Run(string(route), func(t *testing.T) {
			repositoryRef := "github:trusted-" + string(route)
			repository, _, _ := openProductTrustedRepository(t, repositoryRef, testNow)
			policy, err := NewRoutePolicy(
				"policy:"+string(route),
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
			policyRef, err := repository.PublishRoutePolicy(context.Background(), policy)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := repository.PutLiveRoutePolicy(
				context.Background(),
				LiveRoutePolicyUpdate{Route: route, PolicyRef: policyRef},
			)
			if err != nil || revision == "" {
				t.Fatalf("publish live policy = %q, %v", revision, err)
			}
			provenance := ProvenanceMaintainerControl
			if route == RoutePRWithIssue {
				provenance = ProvenanceRepositoryPolicy
			}
			authority := AuthoritySignal{
				Schema:     AuthoritySignalContractV1,
				SignalID:   "signal:" + string(route),
				IssuerRef:  "maintainer:one",
				Provenance: provenance,
				PolicyRef:  policyRef,
				Policy:     policy.Binding(),
				ExpiresAt:  testNow + 1_000,
			}
			authorityRef, err := repository.PublishAuthoritySignal(
				context.Background(),
				authority,
			)
			if err != nil {
				t.Fatal(err)
			}
			destination := DestinationBinding{
				RepositoryRef:    repositoryRef,
				TargetRef:        "refs/heads/main",
				ObservedRevision: "git:base/1",
				DefaultBranch:    true,
			}
			intent, err := NewIntent(
				"nonce:"+string(route),
				route,
				testDigest("a"),
				destination,
				policyRef,
				policy.Binding(),
				testNow-1,
			)
			if err != nil {
				t.Fatal(err)
			}
			intentRef, err := repository.PublishIntent(context.Background(), intent)
			if err != nil {
				t.Fatal(err)
			}
			governanceRef := ""
			if route == RoutePRWithIssue {
				governance := GovernanceDecision{
					Schema:           GovernanceDecisionContractV1,
					Kind:             GovernanceApprovedIssue,
					DecisionID:       "governance:issue",
					SubjectRef:       intentRef,
					Route:            route,
					RepositoryRef:    repositoryRef,
					Destination:      destination,
					PolicyRef:        policyRef,
					Policy:           policy.Binding(),
					AuthorityRef:     authorityRef,
					ApprovedIssueRef: "github:issue/1794",
					ExpiresAt:        testNow + 500,
				}
				governanceRef, err = repository.PublishGovernanceDecision(
					context.Background(),
					governance,
				)
				if err != nil {
					t.Fatal(err)
				}
			}
			if route == RouteEmergency {
				risk := ResidualRisk{
					Schema:      ResidualRiskContractV1,
					RiskID:      "risk:emergency",
					SubjectRef:  intentRef,
					Route:       route,
					Destination: destination,
					PolicyRef:   policyRef,
					Policy:      policy.Binding(),
					Description: "Owner accepted the exact emergency admission risk.",
					ExpiresAt:   testNow + 500,
				}
				riskRef, err := repository.PublishResidualRisk(context.Background(), risk)
				if err != nil {
					t.Fatal(err)
				}
				governance := GovernanceDecision{
					Schema:          GovernanceDecisionContractV1,
					Kind:            GovernanceEmergency,
					DecisionID:      "governance:emergency",
					SubjectRef:      intentRef,
					Route:           route,
					RepositoryRef:   repositoryRef,
					Destination:     destination,
					PolicyRef:       policyRef,
					Policy:          policy.Binding(),
					AuthorityRef:    authorityRef,
					Reason:          "Owner approved bounded emergency delivery.",
					ResidualRiskRef: riskRef,
					ExpiresAt:       testNow + 500,
				}
				governanceRef, err = repository.PublishGovernanceDecision(
					context.Background(),
					governance,
				)
				if err != nil {
					t.Fatal(err)
				}
			}
			admission, err := Admit(
				context.Background(),
				repository,
				AdmissionRequest{
					IntentRef:     intentRef,
					PolicyRef:     policyRef,
					GovernanceRef: governanceRef,
					AuthorityRef:  authorityRef,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			admissionRef, err := repository.PublishAdmissionDecision(
				context.Background(),
				admission,
			)
			if err != nil {
				t.Fatal(err)
			}
			if replay, err := repository.PublishAdmissionDecision(
				context.Background(),
				admission,
			); err != nil || replay != admissionRef {
				t.Fatalf("exact replay = %q, %v; want %q", replay, err, admissionRef)
			}
			resolved, err := repository.ResolveAdmittedIntent(
				context.Background(),
				intentRef,
			)
			if err != nil {
				t.Fatal(err)
			}
			if resolved != intent {
				t.Fatalf("resolved intent = %#v, want %#v", resolved, intent)
			}
		})
	}
}

func TestTrustedRepositoryLivePolicyCASRotationAndConcurrency(t *testing.T) {
	repository, _, _ := openProductTrustedRepository(
		t,
		"github:policy-rotation",
		testNow,
	)
	policies := make([]RoutePolicy, 4)
	refs := make([]string, 4)
	for index := range policies {
		policy, err := NewRoutePolicy(
			"policy:rotation",
			"revision:"+string(rune('1'+index)),
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
		policies[index] = policy
		refs[index], err = repository.PublishRoutePolicy(context.Background(), policy)
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := repository.PutLiveRoutePolicy(
		context.Background(),
		LiveRoutePolicyUpdate{Route: RouteDirectMain, PolicyRef: refs[0]},
	)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := repository.PutLiveRoutePolicy(
		context.Background(),
		LiveRoutePolicyUpdate{Route: RouteDirectMain, PolicyRef: refs[0]},
	); err != nil || replay != first {
		t.Fatalf("initial live-policy replay = %q, %v; want %q", replay, err, first)
	}
	second, err := repository.PutLiveRoutePolicy(
		context.Background(),
		LiveRoutePolicyUpdate{
			Route: RouteDirectMain, PolicyRef: refs[1], ExpectedRevision: first,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := repository.PutLiveRoutePolicy(
		context.Background(),
		LiveRoutePolicyUpdate{
			Route: RouteDirectMain, PolicyRef: refs[1], ExpectedRevision: first,
		},
	); err != nil || replay != second {
		t.Fatalf("rotated live-policy replay = %q, %v; want %q", replay, err, second)
	}
	if _, err := repository.PutLiveRoutePolicy(
		context.Background(),
		LiveRoutePolicyUpdate{
			Route: RouteDirectMain, PolicyRef: refs[0], ExpectedRevision: first,
		},
	); !errors.Is(err, ErrTrustedRepositoryConflict) {
		t.Fatalf("stale CAS error = %v", err)
	}
	var successes atomic.Int32
	var wait sync.WaitGroup
	for _, ref := range refs[2:] {
		ref := ref
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := repository.PutLiveRoutePolicy(
				context.Background(),
				LiveRoutePolicyUpdate{
					Route:            RouteDirectMain,
					PolicyRef:        ref,
					ExpectedRevision: second,
				},
			); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrTrustedRepositoryConflict) {
				t.Errorf("concurrent CAS error = %v", err)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 {
		t.Fatalf("concurrent CAS successes = %d, want 1", successes.Load())
	}
}

func TestTrustedRepositoryRejectsCrossRepositoryPreimages(t *testing.T) {
	repository, _, _ := openProductTrustedRepository(
		t,
		"github:bound-repository",
		testNow,
	)
	policy, err := NewRoutePolicy(
		"policy:cross-repository",
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
	policyRef, err := repository.PublishRoutePolicy(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := NewIntent(
		"nonce:cross-repository",
		RouteDirectMain,
		testDigest("9"),
		DestinationBinding{
			RepositoryRef:    "github:other-repository",
			TargetRef:        "refs/heads/main",
			ObservedRevision: "git:other/base",
			DefaultBranch:    true,
		},
		policyRef,
		policy.Binding(),
		testNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PublishIntent(
		context.Background(),
		intent,
	); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("cross-repository intent error = %v", err)
	}
	if _, err := repository.ResolveDeliveryRepository(
		context.Background(),
		"github:other-repository",
	); !errors.Is(err, ErrTrustedRepositoryUnavailable) {
		t.Fatalf("cross-repository mapping error = %v", err)
	}
}

func TestTrustedRepositoryLiveAuthorizationExpiryAndConsumption(t *testing.T) {
	current := newScenario(
		t,
		RouteDirectMain,
		reviewtransaction.VerificationAggregateComplete,
		false,
		false,
	)
	current.repository.now++
	authorization, err := IssueAuthorization(
		context.Background(),
		current.repository,
		current.repository,
		AuthorizationIssueRequest{
			DecisionRef:  current.decisionRef,
			Nonce:        "nonce:product-live",
			AuthorityRef: current.authorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	repositoryRef := authorization.Binding.Destination.RepositoryRef
	repository, owner, clock := openProductTrustedRepository(
		t,
		repositoryRef,
		current.repository.now,
	)
	current.repository.repositories[repositoryRef] = owner.repositories[repositoryRef]
	repository.rar = current.repository
	publishScenarioToProduct(t, repository, current)
	authorizationRef, err := repository.PublishAuthorization(
		context.Background(),
		authorization,
	)
	if err != nil {
		t.Fatal(err)
	}
	live, err := repository.ResolveLiveAuthorization(
		context.Background(),
		authorizationRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if live.Kind != AuthorizationNormal || live.Binding != authorization.Binding {
		t.Fatalf("live authorization = %#v", live)
	}
	clock.set(authorization.Binding.ExpiresAt)
	if _, err := repository.ResolveLiveAuthorization(
		context.Background(),
		authorizationRef,
	); !errors.Is(err, ErrLiveAuthorizationExpired) {
		t.Fatalf("expired live authorization error = %v", err)
	}
	clock.set(authorization.Binding.IssuedAt)
	store, err := OpenDirectoryUseStore(
		context.Background(),
		repository,
		repositoryRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := ConsumeAuthorization(
		context.Background(),
		repository,
		current.repository,
		authorizationRef,
		UseRequest{
			IntentRef:   authorization.Binding.IntentRef,
			Route:       authorization.Binding.Route,
			Candidate:   authorization.Binding.Candidate,
			Destination: authorization.Binding.Destination,
			PolicyRef:   authorization.Binding.PolicyRef,
			Policy:      authorization.Binding.Policy.Binding(),
		},
		store,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ResolveLiveAuthorization(
		context.Background(),
		authorizationRef,
	); !errors.Is(err, ErrLiveAuthorizationConsumed) {
		t.Fatalf("consumed live authorization error = %v", err)
	}
}

func TestTrustedRepositoryResolvesLiveExceptionAuthorization(t *testing.T) {
	current := newScenario(
		t,
		RouteEmergency,
		reviewtransaction.VerificationAggregateUnavailable,
		true,
		false,
	)
	current.repository.now++
	humanRef := putExceptionGovernance(t, current)
	authorization, err := IssueExceptionAuthorization(
		context.Background(),
		current.repository,
		current.repository,
		ExceptionAuthorizationIssueRequest{
			AuthorizationIssueRequest: AuthorizationIssueRequest{
				DecisionRef:  current.decisionRef,
				Nonce:        "nonce:product-exception",
				AuthorityRef: current.authorityRef,
			},
			HumanDecisionRef: humanRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	repositoryRef := authorization.Binding.Destination.RepositoryRef
	repository, owner, _ := openProductTrustedRepository(
		t,
		repositoryRef,
		current.repository.now,
	)
	current.repository.repositories[repositoryRef] = owner.repositories[repositoryRef]
	repository.rar = current.repository
	publishScenarioToProduct(t, repository, current)
	authorizationRef, err := repository.PublishExceptionAuthorization(
		context.Background(),
		authorization,
	)
	if err != nil {
		t.Fatal(err)
	}
	live, err := repository.ResolveLiveAuthorization(
		context.Background(),
		authorizationRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if live.Kind != AuthorizationException || live.Binding != authorization.Binding {
		t.Fatalf("live exception authorization = %#v", live)
	}
}

func TestTrustedRepositoryRevalidatesCompactRARAuthorization(t *testing.T) {
	current := newScenario(
		t,
		RouteDirectMain,
		reviewtransaction.VerificationAggregateComplete,
		false,
		false,
	)
	historicalKey := reviewKey{
		receipt: current.receiptRef,
		result:  current.resultRef,
	}
	compact := compactRARReview(t, current.repository.reviews[historicalKey])
	delete(current.repository.reviews, historicalKey)
	current.receiptRef = compact.Receipt.ReceiptRef
	current.repository.reviews[reviewKey{
		receipt: current.receiptRef,
		result:  current.resultRef,
	}] = compact
	current.repository.now++
	decision, err := Decide(
		context.Background(),
		current.repository,
		current.repository,
		DeliveryRequest{
			AdmissionDecisionRef:  current.admissionRef,
			PolicyRef:             current.policyRef,
			ReviewReceiptRef:      current.receiptRef,
			VerificationResultRef: current.resultRef,
			GateRef:               current.gateRef,
			AuthorityRef:          current.authorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	current.decision = decision
	current.decisionRef = mustRef(t, decision)
	current.repository.decisions[current.decisionRef] = decision
	current.repository.now++
	authorization, err := IssueAuthorization(
		context.Background(),
		current.repository,
		current.repository,
		AuthorizationIssueRequest{
			DecisionRef:  current.decisionRef,
			Nonce:        "nonce:compact-product",
			AuthorityRef: current.authorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	repositoryRef := authorization.Binding.Destination.RepositoryRef
	repository, owner, _ := openProductTrustedRepository(
		t,
		repositoryRef,
		current.repository.now,
	)
	current.repository.repositories[repositoryRef] = owner.repositories[repositoryRef]
	repository.rar = current.repository
	publishScenarioToProduct(t, repository, current)
	authorizationRef, err := repository.PublishAuthorization(
		context.Background(),
		authorization,
	)
	if err != nil {
		t.Fatal(err)
	}
	live, err := repository.ResolveLiveAuthorization(
		context.Background(),
		authorizationRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if live.Binding.ReviewReceiptRef != compact.Receipt.ReceiptRef {
		t.Fatalf(
			"live compact receipt = %q, want %q",
			live.Binding.ReviewReceiptRef,
			compact.Receipt.ReceiptRef,
		)
	}
}

func publishScenarioToProduct(
	t *testing.T,
	repository *TrustedRepository,
	current scenario,
) {
	t.Helper()
	ctx := context.Background()
	for _, value := range current.repository.policies {
		if _, err := repository.PublishRoutePolicy(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.PutLiveRoutePolicy(
		ctx,
		LiveRoutePolicyUpdate{Route: current.policy.Route, PolicyRef: current.policyRef},
	); err != nil {
		t.Fatal(err)
	}
	for _, value := range current.repository.authorities {
		if _, err := repository.PublishAuthoritySignal(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range current.repository.intents {
		if _, err := repository.PublishIntent(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range current.repository.risks {
		if _, err := repository.PublishResidualRisk(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range current.repository.governance {
		if _, err := repository.PublishGovernanceDecision(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range current.repository.admissions {
		if _, err := repository.PublishAdmissionDecision(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range current.repository.gates {
		if _, err := repository.PublishGateEvidence(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range current.repository.decisions {
		if _, err := repository.PublishDeliveryDecision(ctx, value); err != nil {
			t.Fatal(err)
		}
	}
}
