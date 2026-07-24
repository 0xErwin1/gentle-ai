package deliveryadmission

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const testNow = int64(2_000_000_000)

var _ func(
	context.Context,
	TrustedResolver,
	RARAuthorityPort,
	string,
	UseRequest,
	*DirectoryUseStore,
) (AuthorizationUse, error) = ConsumeAuthorization

func testDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func testTree(character string) string {
	return strings.Repeat(character, 40)
}

func initExactDeliveryRepository(
	t *testing.T,
) (string, reviewtransaction.RepositoryIdentity, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init exact delivery repository: %v\n%s", err, output)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(absolute)
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(
		context.Background(),
		root,
	)
	if err != nil {
		t.Fatal(err)
	}
	return root, lease.Identity(), lease.StorageKey()
}

func exactDeliveryIdentityForRoot(
	t *testing.T,
	root string,
) (reviewtransaction.RepositoryIdentity, string) {
	t.Helper()
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(
		context.Background(),
		root,
	)
	if err != nil {
		t.Fatal(err)
	}
	return lease.Identity(), lease.StorageKey()
}

type reviewKey struct {
	receipt string
	result  string
}

type livePolicyKey struct {
	repository string
	route      Route
}

type trustedRepository struct {
	now            int64
	repositories   map[string]string
	intents        map[string]DeliveryIntent
	policies       map[string]RoutePolicy
	livePolicies   map[livePolicyKey]string
	authorities    map[string]AuthoritySignal
	governance     map[string]GovernanceDecision
	risks          map[string]ResidualRisk
	admissions     map[string]AdmissionDecision
	gates          map[string]GateEvidence
	decisions      map[string]DeliveryDecision
	authorizations map[string]DeliveryAuthorization
	exceptions     map[string]DeliveryExceptionAuthorization
	reviews        map[reviewKey]reviewtransaction.RARVerificationAuthority
}

func newTrustedRepository() *trustedRepository {
	return &trustedRepository{
		now:          testNow,
		repositories: make(map[string]string),
		intents:      make(map[string]DeliveryIntent), policies: make(map[string]RoutePolicy),
		livePolicies: make(map[livePolicyKey]string),
		authorities:  make(map[string]AuthoritySignal), governance: make(map[string]GovernanceDecision),
		risks: make(map[string]ResidualRisk), admissions: make(map[string]AdmissionDecision),
		gates: make(map[string]GateEvidence), decisions: make(map[string]DeliveryDecision),
		authorizations: make(map[string]DeliveryAuthorization),
		exceptions:     make(map[string]DeliveryExceptionAuthorization),
		reviews:        make(map[reviewKey]reviewtransaction.RARVerificationAuthority),
	}
}

func lookup[T any](values map[string]T, ref string) (T, error) {
	value, ok := values[ref]
	if !ok {
		var zero T
		return zero, errors.New("not present in provider-owned repository")
	}
	return value, nil
}

func (repository *trustedRepository) CurrentUnix(context.Context) (int64, error) {
	return repository.now, nil
}
func (repository *trustedRepository) ResolveDeliveryRepository(
	_ context.Context,
	ref string,
) (string, error) {
	return lookup(repository.repositories, ref)
}
func (repository *trustedRepository) ResolveIntent(_ context.Context, ref string) (DeliveryIntent, error) {
	return lookup(repository.intents, ref)
}
func (repository *trustedRepository) ResolveRoutePolicy(_ context.Context, ref string) (RoutePolicy, error) {
	return lookup(repository.policies, ref)
}
func (repository *trustedRepository) ResolveLiveRoutePolicy(
	_ context.Context,
	repositoryRef string,
	route Route,
) (RoutePolicy, error) {
	ref, ok := repository.livePolicies[livePolicyKey{repository: repositoryRef, route: route}]
	if !ok {
		return RoutePolicy{}, errors.New("no provider-owned live route policy")
	}
	return lookup(repository.policies, ref)
}
func (repository *trustedRepository) ResolveAuthoritySignal(_ context.Context, ref string) (AuthoritySignal, error) {
	return lookup(repository.authorities, ref)
}
func (repository *trustedRepository) ResolveGovernanceDecision(_ context.Context, ref string) (GovernanceDecision, error) {
	return lookup(repository.governance, ref)
}
func (repository *trustedRepository) ResolveResidualRisk(_ context.Context, ref string) (ResidualRisk, error) {
	return lookup(repository.risks, ref)
}
func (repository *trustedRepository) ResolveAdmissionDecision(_ context.Context, ref string) (AdmissionDecision, error) {
	return lookup(repository.admissions, ref)
}
func (repository *trustedRepository) ResolveGateEvidence(_ context.Context, ref string) (GateEvidence, error) {
	return lookup(repository.gates, ref)
}
func (repository *trustedRepository) ResolveDeliveryDecision(_ context.Context, ref string) (DeliveryDecision, error) {
	return lookup(repository.decisions, ref)
}
func (repository *trustedRepository) ResolveAuthorization(_ context.Context, ref string) (DeliveryAuthorization, error) {
	return lookup(repository.authorizations, ref)
}
func (repository *trustedRepository) ResolveExceptionAuthorization(_ context.Context, ref string) (DeliveryExceptionAuthorization, error) {
	return lookup(repository.exceptions, ref)
}
func (repository *trustedRepository) ResolveTerminalReview(
	_ context.Context,
	receiptRef string,
	resultRef string,
) (reviewtransaction.RARVerificationAuthority, error) {
	value, ok := repository.reviews[reviewKey{receipt: receiptRef, result: resultRef}]
	if !ok {
		return reviewtransaction.RARVerificationAuthority{},
			errors.New("RAR did not authorize this receipt/result pair")
	}
	return value, nil
}

func mustRef(t *testing.T, value interface{ Ref() (string, error) }) string {
	t.Helper()
	ref, err := value.Ref()
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func putPolicy(t *testing.T, repository *trustedRepository, value RoutePolicy) string {
	t.Helper()
	ref := mustRef(t, value)
	repository.policies[ref] = value
	return ref
}

func putAuthority(t *testing.T, repository *trustedRepository, value AuthoritySignal) string {
	t.Helper()
	ref := mustRef(t, value)
	repository.authorities[ref] = value
	return ref
}

func putGovernance(t *testing.T, repository *trustedRepository, value GovernanceDecision) string {
	t.Helper()
	ref := mustRef(t, value)
	repository.governance[ref] = value
	return ref
}

func putRisk(t *testing.T, repository *trustedRepository, value ResidualRisk) string {
	t.Helper()
	ref := mustRef(t, value)
	repository.risks[ref] = value
	return ref
}

type scenario struct {
	repository            *trustedRepository
	policy                RoutePolicy
	policyRef             string
	authorityRef          string
	secondRef             string
	intentRef             string
	governanceRef         string
	admissionRef          string
	receiptRef            string
	resultRef             string
	gateRef               string
	candidateAuthorityRef string
	decisionRef           string
	decision              DeliveryDecision
	verification          reviewtransaction.VerificationResultRef
}

func newScenario(
	t *testing.T,
	route Route,
	outcome reviewtransaction.VerificationAggregate,
	allowIncomplete bool,
	secondMaintainer bool,
) scenario {
	t.Helper()
	return newScenarioForRepository(
		t,
		route,
		outcome,
		allowIncomplete,
		secondMaintainer,
		"github:gentle-ai",
	)
}

func newScenarioForRepository(
	t *testing.T,
	route Route,
	outcome reviewtransaction.VerificationAggregate,
	allowIncomplete bool,
	secondMaintainer bool,
	repositoryRef string,
) scenario {
	t.Helper()
	repository := newTrustedRepository()
	exceptionTTL := int64(0)
	if allowIncomplete {
		exceptionTTL = 60
	}
	policy, err := NewRoutePolicy(
		"policy:delivery", "revision:1", route, true, allowIncomplete,
		secondMaintainer, 120, exceptionTTL,
	)
	if err != nil {
		t.Fatal(err)
	}
	policyRef := putPolicy(t, repository, policy)
	provenance := ProvenanceMaintainerControl
	if route == RoutePRWithIssue {
		provenance = ProvenanceRepositoryPolicy
	}
	authority := AuthoritySignal{
		Schema: AuthoritySignalContractV1, SignalID: "signal:primary",
		IssuerRef: "maintainer:one", Provenance: provenance,
		PolicyRef: policyRef, Policy: policy.Binding(), ExpiresAt: testNow + 1_000,
	}
	authorityRef := putAuthority(t, repository, authority)
	secondRef := ""
	if secondMaintainer {
		second := AuthoritySignal{
			Schema: AuthoritySignalContractV1, SignalID: "signal:secondary",
			IssuerRef: "maintainer:two", Provenance: ProvenanceSecondMaintainer,
			PolicyRef: policyRef, Policy: policy.Binding(), ExpiresAt: testNow + 1_000,
		}
		secondRef = putAuthority(t, repository, second)
	}
	initialDestination := DestinationBinding{
		RepositoryRef: repositoryRef, TargetRef: "refs/heads/main",
		ObservedRevision: "git:base/1", DefaultBranch: true,
	}
	repository.livePolicies[livePolicyKey{
		repository: initialDestination.RepositoryRef,
		route:      route,
	}] = policyRef
	intent, err := NewIntent(
		"nonce:intent", route, testDigest("a"), initialDestination,
		policyRef, policy.Binding(), testNow-1,
	)
	if err != nil {
		t.Fatal(err)
	}
	intentRef := mustRef(t, intent)
	repository.intents[intentRef] = intent
	governanceRef := ""
	if route == RoutePRWithIssue {
		governanceRef = putGovernance(t, repository, GovernanceDecision{
			Schema: GovernanceDecisionContractV1, Kind: GovernanceApprovedIssue,
			DecisionID: "governance:issue", SubjectRef: intentRef, Route: route,
			RepositoryRef: initialDestination.RepositoryRef, Destination: initialDestination,
			PolicyRef: policyRef, Policy: policy.Binding(), AuthorityRef: authorityRef,
			ApprovedIssueRef: "github:issue/1794", ExpiresAt: testNow + 500,
		})
	}
	if route == RouteEmergency {
		riskRef := putRisk(t, repository, ResidualRisk{
			Schema: ResidualRiskContractV1, RiskID: "risk:emergency/1",
			SubjectRef: intentRef, Route: route, Destination: initialDestination,
			PolicyRef: policyRef, Policy: policy.Binding(),
			Description: "Emergency delivery may proceed with explicitly recorded incomplete proof.",
			ExpiresAt:   testNow + 500,
		})
		governanceRef = putGovernance(t, repository, GovernanceDecision{
			Schema: GovernanceDecisionContractV1, Kind: GovernanceEmergency,
			DecisionID: "governance:emergency", SubjectRef: intentRef, Route: route,
			RepositoryRef: initialDestination.RepositoryRef, Destination: initialDestination,
			PolicyRef: policyRef, Policy: policy.Binding(), AuthorityRef: authorityRef,
			SecondAuthorityRef: secondRef,
			Reason:             "Urgent delivery with explicit residual risk.",
			ResidualRiskRef:    riskRef, ExpiresAt: testNow + 500,
		})
	}
	admission, err := Admit(context.Background(), repository, AdmissionRequest{
		IntentRef: intentRef, PolicyRef: policyRef, GovernanceRef: governanceRef,
		AuthorityRef: authorityRef, SecondAuthorityRef: secondRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	admissionRef := mustRef(t, admission)
	repository.admissions[admissionRef] = admission

	review := newRARReview(t, outcome)
	receiptRef := review.Receipt.ReceiptRef
	resultRef := review.Result.ResultRef
	verification := review.Result
	repository.reviews[reviewKey{receipt: receiptRef, result: resultRef}] = review

	candidate := CandidateBinding{
		Ref: "candidate:normalized/1", Digest: verification.Subject.SnapshotIdentity,
	}
	finalDestination := initialDestination
	finalDestination.ObservedRevision = "git:base/2"
	gates := GateEvidence{
		Schema: GateEvidenceContractV1, Route: route, Candidate: candidate,
		Destination: finalDestination, PolicyRef: policyRef, Policy: policy.Binding(),
		ProtectionMode: ProtectionRespect, RemoteFreshnessRef: testDigest("e"),
		ProtectionRef: testDigest("f"), ObservedAt: testNow, ExpiresAt: testNow + 200,
	}
	if route == RoutePRWithIssue || route == RoutePRWithoutIssue {
		gates.Mechanism = MechanismPullRequest
		gates.PullRequestRef = "github:pull/1795"
		gates.RequiredChecksRef = testDigest("1")
	} else {
		gates.Mechanism = MechanismFastForwardOnly
	}
	gateRef := mustRef(t, gates)
	repository.gates[gateRef] = gates
	candidateAuthorityRef := testDigest("8")
	repository.now++
	decision, err := Decide(
		context.Background(), repository, repository,
		DeliveryRequest{
			AdmissionDecisionRef: admissionRef, PolicyRef: policyRef,
			ReviewReceiptRef: receiptRef, VerificationResultRef: resultRef,
			CandidateAuthorityRef: candidateAuthorityRef,
			GateRef:               gateRef, AuthorityRef: authorityRef,
			SecondAuthorityRef: secondRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	decisionRef := mustRef(t, decision)
	repository.decisions[decisionRef] = decision
	return scenario{
		repository: repository, policy: policy, policyRef: policyRef,
		authorityRef: authorityRef, secondRef: secondRef, intentRef: intentRef,
		governanceRef: governanceRef, admissionRef: admissionRef,
		receiptRef: receiptRef, resultRef: resultRef, gateRef: gateRef,
		candidateAuthorityRef: candidateAuthorityRef,
		decisionRef:           decisionRef, decision: decision, verification: verification,
	}
}

func newRARReview(
	t *testing.T,
	outcome reviewtransaction.VerificationAggregate,
) reviewtransaction.RARVerificationAuthority {
	t.Helper()
	subject := reviewtransaction.VerificationSubject{
		Kind:     reviewtransaction.TargetCurrentChanges,
		BaseTree: testTree("1"), CandidateTree: testTree("2"),
		PathsDigest: testDigest("3"), SnapshotIdentity: testDigest("4"),
	}
	if err := subject.Validate(); err != nil {
		t.Fatal(err)
	}
	receipt := reviewtransaction.Receipt{
		Schema: reviewtransaction.ReceiptSchema, LineageID: "delivery-lineage",
		Mode: reviewtransaction.ModeOrdinaryBounded, Generation: 1,
		BaseTree: subject.BaseTree, InitialReviewTree: subject.CandidateTree,
		FinalCandidateTree: subject.CandidateTree, PathsDigest: subject.PathsDigest,
		FixDeltaHash: reviewtransaction.EmptyFixDeltaHash,
		PolicyHash:   testDigest("5"), LedgerHash: testDigest("6"), EvidenceHash: testDigest("7"),
		Counters:  reviewtransaction.Counters{FinalVerifications: 1},
		RiskLevel: reviewtransaction.RiskLow, SelectedLenses: []string{},
		LensResults: []reviewtransaction.LensResult{}, TerminalState: reviewtransaction.TerminalApproved,
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewtransaction.ParseReceipt(payload); err != nil {
		t.Fatalf("RAR receipt fixture: %v", err)
	}
	obligations := []reviewtransaction.VerificationObligation{
		{
			ID: "unit", RequirementRef: testDigest("d"), ArgvRef: testDigest("e"),
			CapabilityRef: "capability:unit", Cost: reviewtransaction.VerificationCostQuick,
		},
	}
	applicabilityDecision := reviewtransaction.VerificationApplicable
	contentActivity := reviewtransaction.VerificationContentActive
	reasons := []reviewtransaction.VerificationApplicabilityReason{
		{Code: reviewtransaction.VerificationReasonActiveSource, Path: "main.go"},
	}
	completed := []string{}
	if outcome == reviewtransaction.VerificationAggregateComplete {
		completed = []string{"unit"}
	}
	if outcome == reviewtransaction.VerificationAggregateNotRequired {
		obligations = []reviewtransaction.VerificationObligation{}
		applicabilityDecision = reviewtransaction.VerificationNotApplicable
		contentActivity = reviewtransaction.VerificationContentPassive
		reasons = []reviewtransaction.VerificationApplicabilityReason{
			{Code: reviewtransaction.VerificationReasonPassiveDocument, Path: "README.md"},
		}
	}
	registry, err := reviewtransaction.NewVerificationPlanRegistry(
		receipt.PolicyHash,
		[]string{},
		obligations,
	)
	if err != nil {
		t.Fatalf("RAR registry fixture: %v", err)
	}
	applicability := reviewtransaction.VerificationApplicability{
		Schema:            reviewtransaction.VerificationApplicabilitySchema,
		ClassifierVersion: reviewtransaction.VerificationClassifierVersion,
		Subject:           subject,
		PolicyHash:        receipt.PolicyHash,
		RegistryDigest:    registry.Digest,
		Decision:          applicabilityDecision,
		ContentActivity:   contentActivity,
		Reasons:           reasons,
		EvidenceRefs:      []string{testDigest("b")},
	}
	applicability.Digest = testReviewContractDigest(
		t,
		"gentle-ai.verification-applicability-digest/v1",
		applicability,
	)
	if err := applicability.Validate(); err != nil {
		t.Fatalf("RAR applicability fixture: %v", err)
	}
	plan, err := reviewtransaction.BuildVerificationPlan(applicability, registry)
	if err != nil {
		t.Fatalf("RAR plan fixture: %v", err)
	}
	result := reviewtransaction.VerificationResultRef{
		Schema:    reviewtransaction.VerificationResultRefSchema,
		ResultRef: testDigest("8"), Subject: subject, PolicyHash: receipt.PolicyHash,
		PlanDigest: plan.Digest, ApplicabilityDigest: applicability.Digest,
		Aggregate: outcome, CompletedObligations: completed,
		EvidenceRefs: []string{testDigest("b")},
	}
	if err := reviewtransaction.ValidateVerificationResultRef(
		applicability,
		registry,
		plan,
		result,
	); err != nil {
		t.Fatalf("RAR result fixture: %v", err)
	}
	review := reviewtransaction.RARVerificationAuthority{
		Schema: reviewtransaction.RARVerificationAuthoritySchema,
		Receipt: reviewtransaction.RARNativeReceiptAuthority{
			Schema:            reviewtransaction.RARNativeReceiptSchema,
			Version:           reviewtransaction.RARReceiptHistoricalV1,
			AuthorityRevision: testDigest("c"),
			ReceiptRef:        testNativeReceiptRef(t, receipt),
			Historical:        &receipt,
		},
		Applicability: applicability,
		Registry:      registry,
		Plan:          plan,
		Result:        result,
	}
	review.AuthorityRef = testReviewContractDigest(
		t,
		"gentle-ai.rar-verification-authority-digest/v1",
		review,
	)
	if err := review.Validate(); err != nil {
		t.Fatalf("RAR authority fixture: %v", err)
	}
	return review
}

func testNativeReceiptRef(t *testing.T, receipt any) string {
	t.Helper()
	payload, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest)
}

func testReviewContractDigest(t *testing.T, domain string, value any) string {
	t.Helper()
	switch typed := value.(type) {
	case reviewtransaction.VerificationApplicability:
		typed.Digest = ""
		value = typed
	case reviewtransaction.RARVerificationAuthority:
		typed.AuthorityRef = ""
		value = typed
	}
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(payload)
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

func compactRARReview(
	t *testing.T,
	review reviewtransaction.RARVerificationAuthority,
) reviewtransaction.RARVerificationAuthority {
	t.Helper()
	if review.Receipt.Historical == nil {
		t.Fatal("compact RAR fixture requires a historical source receipt")
	}
	historical := *review.Receipt.Historical
	compact := reviewtransaction.CompactReceipt{
		Schema:             reviewtransaction.CompactReceiptSchema,
		LineageID:          historical.LineageID,
		Generation:         historical.Generation,
		BaseTree:           historical.BaseTree,
		InitialReviewTree:  historical.InitialReviewTree,
		FinalCandidateTree: historical.FinalCandidateTree,
		PathsDigest:        historical.PathsDigest,
		FixDeltaHash:       historical.FixDeltaHash,
		PolicyHash:         historical.PolicyHash,
		EvidenceHash:       historical.EvidenceHash,
		RiskLevel:          historical.RiskLevel,
		SelectedLenses:     append([]string{}, historical.SelectedLenses...),
		ResolvedFindingIDs: []string{},
		TerminalState:      historical.TerminalState,
	}
	if err := compact.Validate(); err != nil {
		t.Fatalf("compact RAR receipt fixture: %v", err)
	}
	review.Receipt.Version = reviewtransaction.RARReceiptCompactV2
	review.Receipt.ReceiptRef = testNativeReceiptRef(t, compact)
	review.Receipt.Compact = &compact
	review.Receipt.Historical = nil
	review.AuthorityRef = testReviewContractDigest(
		t,
		"gentle-ai.rar-verification-authority-digest/v1",
		review,
	)
	if err := review.Validate(); err != nil {
		t.Fatalf("compact RAR authority fixture: %v", err)
	}
	return review
}

func TestPADConsumesNativeCompactRARAuthorityByOwnerResultRef(t *testing.T) {
	repository := newTrustedRepository()
	review := compactRARReview(
		t,
		newRARReview(t, reviewtransaction.VerificationAggregateComplete),
	)
	key := reviewKey{
		receipt: review.Receipt.ReceiptRef,
		result:  review.Result.ResultRef,
	}
	repository.reviews[key] = review

	resolved, err := resolveRARReview(
		context.Background(),
		repository,
		key.receipt,
		key.result,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.authority.Receipt.Version != reviewtransaction.RARReceiptCompactV2 ||
		resolved.result.ResultRef != review.Result.ResultRef {
		t.Fatalf("resolved compact owner authority = %#v", resolved)
	}
	if hashValue(review.Result) == review.Result.ResultRef {
		t.Fatal("test fixture accidentally made owner ResultRef equal PAD's wrapper hash")
	}
}

func TestPADRejectsSelfDeclaredAuthorityPolicyAndGovernance(t *testing.T) {
	t.Parallel()
	current := newScenario(t, RoutePRWithoutIssue, reviewtransaction.VerificationAggregateComplete, false, false)
	maliciousAuthority := AuthoritySignal{
		Schema: AuthoritySignalContractV1, SignalID: "pr-head:self-declared",
		IssuerRef: "attacker", Provenance: ProvenanceMaintainerControl,
		PolicyRef: current.policyRef, Policy: current.policy.Binding(),
		ExpiresAt: testNow + 1_000,
	}
	_, err := Admit(context.Background(), current.repository, AdmissionRequest{
		IntentRef: current.intentRef, PolicyRef: current.policyRef,
		AuthorityRef: mustRef(t, maliciousAuthority),
	})
	if !errors.Is(err, ErrUntrustedPreimage) {
		t.Fatalf("self-declared authority error = %v, want ErrUntrustedPreimage", err)
	}

	issue := newScenario(t, RoutePRWithIssue, reviewtransaction.VerificationAggregateComplete, false, false)
	maliciousGovernance := issue.repository.governance[issue.governanceRef]
	maliciousGovernance.ApprovedIssueRef = "attacker:claims-approved"
	_, err = Admit(context.Background(), issue.repository, AdmissionRequest{
		IntentRef: issue.intentRef, PolicyRef: issue.policyRef,
		GovernanceRef: mustRef(t, maliciousGovernance), AuthorityRef: issue.authorityRef,
	})
	if !errors.Is(err, ErrUntrustedPreimage) {
		t.Fatalf("self-declared issue approval error = %v, want ErrUntrustedPreimage", err)
	}

	maliciousPolicy, err := NewRoutePolicy(
		current.policy.ID, "attacker:revision", RoutePRWithoutIssue,
		true, true, false, 10_000, 10_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Admit(context.Background(), current.repository, AdmissionRequest{
		IntentRef: current.intentRef, PolicyRef: mustRef(t, maliciousPolicy),
		AuthorityRef: current.authorityRef,
	})
	if !errors.Is(err, ErrUntrustedPreimage) {
		t.Fatalf("self-declared policy error = %v, want ErrUntrustedPreimage", err)
	}
}

func TestExactPreimagesRejectAliasesAndPoisonedLookup(t *testing.T) {
	t.Parallel()
	current := newScenario(t, RoutePRWithIssue, reviewtransaction.VerificationAggregateComplete, false, false)
	_, err := Admit(context.Background(), current.repository, AdmissionRequest{
		IntentRef: "intent:printable-alias", PolicyRef: current.policyRef,
		GovernanceRef: current.governanceRef, AuthorityRef: current.authorityRef,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("intent alias error = %v, want ErrInvalid", err)
	}
	_, err = Decide(context.Background(), current.repository, current.repository, DeliveryRequest{
		AdmissionDecisionRef: "admission:printable-alias", PolicyRef: current.policyRef,
		ReviewReceiptRef: current.receiptRef, VerificationResultRef: current.resultRef,
		GateRef: current.gateRef, AuthorityRef: current.authorityRef,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("admission alias error = %v, want ErrInvalid", err)
	}
	poisoned := current.policy
	poisoned.Enabled = false
	current.repository.policies[current.policyRef] = poisoned
	_, err = resolvePolicy(context.Background(), current.repository, current.policyRef)
	if !errors.Is(err, ErrUntrustedPreimage) {
		t.Fatalf("poisoned preimage error = %v, want ErrUntrustedPreimage", err)
	}
}

func TestDeliveryDecisionContentAddressesOptionalCandidateAuthority(
	t *testing.T,
) {
	t.Parallel()
	current := newScenario(
		t,
		RouteDirectMain,
		reviewtransaction.VerificationAggregateComplete,
		false,
		false,
	)
	if current.decision.CandidateAuthorityRef !=
		current.candidateAuthorityRef {
		t.Fatalf(
			"candidate authority ref = %q, want %q",
			current.decision.CandidateAuthorityRef,
			current.candidateAuthorityRef,
		)
	}
	withoutAuthority := current.decision
	withoutAuthority.CandidateAuthorityRef = ""
	if err := withoutAuthority.Validate(); err != nil {
		t.Fatalf("historical v1 decision without candidate authority: %v", err)
	}
	withoutRef := mustRef(t, withoutAuthority)
	if withoutRef == current.decisionRef {
		t.Fatal("candidate authority did not contribute to decision content address")
	}
	invalid := current.decision
	invalid.CandidateAuthorityRef = "candidate-authority:alias"
	if err := invalid.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid candidate authority ref = %v", err)
	}
}

func TestPADConsumesExactNativeRARAuthority(t *testing.T) {
	t.Parallel()
	current := newScenario(t, RouteDirectMain, reviewtransaction.VerificationAggregateComplete, false, false)

	selfDeclared := current.verification
	selfDeclared.Aggregate = reviewtransaction.VerificationAggregateNotRequired
	selfDeclared.ResultRef = testDigest("f")
	_, err := Decide(context.Background(), current.repository, current.repository, DeliveryRequest{
		AdmissionDecisionRef: current.admissionRef, PolicyRef: current.policyRef,
		ReviewReceiptRef: current.receiptRef, VerificationResultRef: selfDeclared.ResultRef,
		GateRef: current.gateRef, AuthorityRef: current.authorityRef,
	})
	if !errors.Is(err, ErrUntrustedPreimage) {
		t.Fatalf("self-declared RAR outcome error = %v, want ErrUntrustedPreimage", err)
	}

	key := reviewKey{receipt: current.receiptRef, result: current.resultRef}
	poisoned := current.repository.reviews[key]
	poisoned.Result.Aggregate = reviewtransaction.VerificationAggregateFailed
	current.repository.reviews[key] = poisoned
	_, err = ValidateDeliveryDecision(
		context.Background(), current.repository, current.repository, current.decisionRef,
	)
	if !errors.Is(err, ErrUntrustedPreimage) {
		t.Fatalf("poisoned RAR preimage error = %v, want ErrUntrustedPreimage", err)
	}
}

func TestEmergencyResidualRiskRequiresExactOwnerPreimage(t *testing.T) {
	t.Parallel()
	current := newScenario(
		t, RouteEmergency, reviewtransaction.VerificationAggregateUnavailable, true, false,
	)
	governance := current.repository.governance[current.governanceRef]
	risk := current.repository.risks[governance.ResidualRiskRef]
	risk.Description = "Candidate-authored replacement risk."
	current.repository.risks[governance.ResidualRiskRef] = risk
	_, err := ValidateAdmissionDecision(
		context.Background(), current.repository, current.admissionRef,
	)
	if !errors.Is(err, ErrUntrustedPreimage) {
		t.Fatalf("poisoned residual-risk error = %v, want ErrUntrustedPreimage", err)
	}
}

func TestRouteMatrixAndAuthorizationSeparation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		route       Route
		outcome     reviewtransaction.VerificationAggregate
		allow       bool
		second      bool
		disposition DeliveryDisposition
	}{
		{"PR with issue", RoutePRWithIssue, reviewtransaction.VerificationAggregateComplete, false, false, DeliveryAuthorizeNormal},
		{"PR without issue", RoutePRWithoutIssue, reviewtransaction.VerificationAggregateNotRequired, false, false, DeliveryAuthorizeNormal},
		{"direct main", RouteDirectMain, reviewtransaction.VerificationAggregateComplete, false, false, DeliveryAuthorizeNormal},
		{"emergency incomplete", RouteEmergency, reviewtransaction.VerificationAggregateUnavailable, true, true, DeliveryRequireException},
		{"partial blocked", RoutePRWithIssue, reviewtransaction.VerificationAggregatePartial, false, false, DeliveryBlocked},
		{"failed blocked", RouteEmergency, reviewtransaction.VerificationAggregateFailed, true, false, DeliveryBlocked},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			current := newScenario(t, tt.route, tt.outcome, tt.allow, tt.second)
			if current.decision.Disposition != tt.disposition {
				t.Fatalf("Disposition = %q, want %q", current.decision.Disposition, tt.disposition)
			}
		})
	}

	normal := newScenario(t, RouteDirectMain, reviewtransaction.VerificationAggregateComplete, false, false)
	normal.repository.now++
	authorization, err := IssueAuthorization(
		context.Background(), normal.repository, normal.repository,
		AuthorizationIssueRequest{
			DecisionRef: normal.decisionRef, Nonce: "nonce:normal",
			AuthorityRef: normal.authorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorizationRef := mustRef(t, authorization)
	normal.repository.authorizations[authorizationRef] = authorization

	exceptional := newScenario(t, RouteEmergency, reviewtransaction.VerificationAggregateUnavailable, true, false)
	exceptional.repository.now++
	humanDecisionRef := putExceptionGovernance(t, exceptional)
	exception, err := IssueExceptionAuthorization(
		context.Background(), exceptional.repository, exceptional.repository,
		ExceptionAuthorizationIssueRequest{
			AuthorizationIssueRequest: AuthorizationIssueRequest{
				DecisionRef: exceptional.decisionRef, Nonce: "nonce:exception",
				AuthorityRef: exceptional.authorityRef,
			},
			HumanDecisionRef: humanDecisionRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if exception.HumanDecisionRef != humanDecisionRef {
		t.Fatal("exception did not bind exact human decision")
	}
	_, err = IssueExceptionAuthorization(
		context.Background(), exceptional.repository, exceptional.repository,
		ExceptionAuthorizationIssueRequest{
			AuthorizationIssueRequest: AuthorizationIssueRequest{
				DecisionRef: exceptional.decisionRef, Nonce: "nonce:forged",
				AuthorityRef: exceptional.authorityRef,
			},
			HumanDecisionRef: testDigest("f"),
		},
	)
	if !errors.Is(err, ErrUntrustedPreimage) {
		t.Fatalf("forged human decision error = %v, want ErrUntrustedPreimage", err)
	}
	_, err = IssueAuthorization(
		context.Background(), exceptional.repository, exceptional.repository,
		AuthorizationIssueRequest{
			DecisionRef: exceptional.decisionRef, Nonce: "nonce:wrong",
			AuthorityRef: exceptional.authorityRef,
		},
	)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("normal grant for unavailable error = %v, want ErrUnauthorized", err)
	}
}

func putExceptionGovernance(t *testing.T, current scenario) string {
	t.Helper()
	candidate := current.decision.Candidate
	riskRef := putRisk(t, current.repository, ResidualRisk{
		Schema: ResidualRiskContractV1, RiskID: "risk:accepted/1",
		SubjectRef: current.decisionRef, Route: current.decision.AdmissionDecision.Route,
		Candidate: &candidate, Destination: current.decision.Gates.Destination,
		PolicyRef: current.policyRef, Policy: current.policy.Binding(),
		Description: "A maintainer accepted the exact incomplete-verification residual risk.",
		ExpiresAt:   current.repository.now + 30,
	})
	return putGovernance(t, current.repository, GovernanceDecision{
		Schema: GovernanceDecisionContractV1, Kind: GovernanceException,
		DecisionID: "governance:exception", SubjectRef: current.decisionRef,
		Route:         current.decision.AdmissionDecision.Route,
		RepositoryRef: current.decision.Gates.Destination.RepositoryRef,
		Candidate:     &candidate, Destination: current.decision.Gates.Destination,
		PolicyRef: current.policyRef, Policy: current.policy.Binding(),
		AuthorityRef:       current.authorityRef,
		SecondAuthorityRef: current.secondRef,
		Reason:             "Accept exact residual risk for this delivery decision.",
		ResidualRiskRef:    riskRef, ExpiresAt: current.repository.now + 30,
	})
}

func TestReplayCannotAuthorizeDeniedAdmission(t *testing.T) {
	t.Parallel()
	current := newScenario(t, RouteDirectMain, reviewtransaction.VerificationAggregateComplete, false, false)
	disabled, err := NewRoutePolicy(
		"policy:disabled", "revision:1", RouteDirectMain, false, false, false, 120, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	disabledRef := putPolicy(t, current.repository, disabled)
	current.repository.livePolicies[livePolicyKey{
		repository: current.decision.AdmissionDecision.Destination.RepositoryRef,
		route:      RouteDirectMain,
	}] = disabledRef
	disabledAuthority := AuthoritySignal{
		Schema: AuthoritySignalContractV1, SignalID: "signal:disabled",
		IssuerRef: "maintainer:one", Provenance: ProvenanceMaintainerControl,
		PolicyRef: disabledRef, Policy: disabled.Binding(), ExpiresAt: testNow + 1_000,
	}
	disabledAuthorityRef := putAuthority(t, current.repository, disabledAuthority)
	intent, err := NewIntent(
		"nonce:denied", RouteDirectMain, testDigest("d"),
		current.decision.AdmissionDecision.Destination, disabledRef, disabled.Binding(), testNow-1,
	)
	if err != nil {
		t.Fatal(err)
	}
	intentRef := mustRef(t, intent)
	current.repository.intents[intentRef] = intent
	denied, err := Admit(context.Background(), current.repository, AdmissionRequest{
		IntentRef: intentRef, PolicyRef: disabledRef, AuthorityRef: disabledAuthorityRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if denied.Disposition != AdmissionDenied {
		t.Fatalf("disposition = %q, want denied", denied.Disposition)
	}
	deniedRef := mustRef(t, denied)
	current.repository.admissions[deniedRef] = denied
	forged := current.decision
	forged.AdmissionDecisionRef = deniedRef
	forged.AdmissionDecision = denied
	if err := forged.Validate(); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("delivery over denied admission error = %v, want ErrBindingMismatch", err)
	}
}

func openScenarioUseStore(
	t *testing.T,
	repository *trustedRepository,
	repositoryRef string,
) *DirectoryUseStore {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	command := exec.Command("git", "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(root)
	repository.repositories[repositoryRef] = root
	return openAliasedScenarioUseStore(t, repository, repositoryRef, root)
}

// openAliasedScenarioUseStore keeps the older pure PAD scenarios focused on
// one-shot semantics even though their synthetic `github:*` refs intentionally
// predate the production exact-Git-ref contract. Public opener coverage below
// always uses the exact lease ref.
func openAliasedScenarioUseStore(
	t *testing.T,
	repository *trustedRepository,
	repositoryRef string,
	root string,
) *DirectoryUseStore {
	t.Helper()
	identity, err := resolveDeliveryGitIdentity(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(
		identity.commonDir,
		"gentle-ai",
		"delivery-authorization-uses",
		"v1",
		"repositories",
		identity.storageKey,
	)
	secureDirectory, err := openSecureUseDirectory(
		identity.commonDir,
		directory,
		identity.commonInfo,
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &DirectoryUseStore{
		directory: directory, repositoryRoot: identity.repositoryRoot,
		repositoryRef: repositoryRef, identityRef: identity.repositoryRef,
		gitCommonDir: identity.commonDir, gitDir: identity.gitDir,
		storageKey: identity.storageKey, identityLease: identity.lease,
		resolver: repository, secureDirectory: secureDirectory,
	}
	if err := store.validateAuthority(context.Background()); err != nil {
		_ = secureDirectory.close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close authorization-use store: %v", err)
		}
	})
	return store
}

func TestDirectoryUseStoreIgnoresAmbientGitRepositorySelection(t *testing.T) {
	repository := newTrustedRepository()
	root := filepath.Join(t.TempDir(), "repository")
	if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init repository: %v\n%s", err, output)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(root)
	identity, _ := exactDeliveryIdentityForRoot(t, root)
	repositoryRef := identity.RepositoryRef
	attackerCommon := filepath.Join(t.TempDir(), "attacker.git")
	if output, err := exec.Command(
		"git", "init", "--quiet", "--bare", attackerCommon,
	).CombinedOutput(); err != nil {
		t.Fatalf("git init attacker common-dir: %v\n%s", err, output)
	}
	attackerCommon, err = filepath.Abs(attackerCommon)
	if err != nil {
		t.Fatal(err)
	}
	attackerCommon = filepath.Clean(attackerCommon)
	repository.repositories[repositoryRef] = root
	t.Setenv("GIT_DIR", attackerCommon)
	t.Setenv("GIT_WORK_TREE", root)
	t.Setenv("GIT_COMMON_DIR", attackerCommon)

	store, err := OpenDirectoryUseStore(context.Background(), repository, repositoryRef)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	wantCommon := filepath.Join(root, ".git")
	if store.gitCommonDir != wantCommon {
		t.Fatalf("Git common-dir = %q, want real repository control %q", store.gitCommonDir, wantCommon)
	}
	if _, err := os.Lstat(filepath.Join(attackerCommon, "gentle-ai")); !os.IsNotExist(err) {
		t.Fatalf("ambient Git selection redirected PAD mutations: %v", err)
	}
}

func TestDirectoryUseStoreRejectsRepositoryRootIdentityReplacement(t *testing.T) {
	parent := t.TempDir()
	mainRoot := filepath.Join(parent, "main")
	if output, err := exec.Command("git", "init", "--quiet", mainRoot).CombinedOutput(); err != nil {
		t.Fatalf("git init main repository: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(mainRoot, "README.md"), []byte("authority\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", mainRoot, "add", "README.md"},
		{
			"-C", mainRoot, "-c", "user.name=PAD Test",
			"-c", "user.email=pad@example.invalid", "commit", "--quiet", "-m", "initial",
		},
	} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	selectedRoot := filepath.Join(parent, "selected")
	if output, err := exec.Command(
		"git", "-C", mainRoot, "worktree", "add", "--quiet", "-b", "selected-one", selectedRoot,
	).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add first root: %v\n%s", err, output)
	}
	selectedRoot, err := filepath.Abs(selectedRoot)
	if err != nil {
		t.Fatal(err)
	}
	selectedRoot = filepath.Clean(selectedRoot)
	repository := newTrustedRepository()
	identity, _ := exactDeliveryIdentityForRoot(t, selectedRoot)
	repositoryRef := identity.RepositoryRef
	repository.repositories[repositoryRef] = selectedRoot
	store, err := OpenDirectoryUseStore(context.Background(), repository, repositoryRef)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if output, err := exec.Command(
		"git", "-C", mainRoot, "worktree", "remove", "--force", selectedRoot,
	).CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove selected root: %v\n%s", err, output)
	}
	if output, err := exec.Command(
		"git", "-C", mainRoot, "worktree", "add", "--quiet", "-b", "selected-two", selectedRoot,
	).CombinedOutput(); err != nil {
		t.Fatalf("git worktree replace selected root: %v\n%s", err, output)
	}
	if err := store.validateAuthority(context.Background()); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("replaced repository root error = %v, want ErrBindingMismatch", err)
	}
}

func normalAuthorization(t *testing.T) (scenario, string, DeliveryAuthorization) {
	t.Helper()
	current := newScenario(t, RouteDirectMain, reviewtransaction.VerificationAggregateComplete, false, false)
	current.repository.now++
	authorization, err := IssueAuthorization(
		context.Background(), current.repository, current.repository,
		AuthorizationIssueRequest{
			DecisionRef: current.decisionRef, Nonce: "nonce:normal",
			AuthorityRef: current.authorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ref := mustRef(t, authorization)
	current.repository.authorizations[ref] = authorization
	return current, ref, authorization
}

func useRequest(authorization DeliveryAuthorization) UseRequest {
	return UseRequest{
		IntentRef: authorization.Binding.IntentRef, Route: authorization.Binding.Route,
		Candidate: authorization.Binding.Candidate, Destination: authorization.Binding.Destination,
		PolicyRef: authorization.Binding.PolicyRef, Policy: authorization.Binding.Policy.Binding(),
	}
}

func TestDirectoryUseStorePersistsCanonicalOneShotRecord(t *testing.T) {
	t.Parallel()
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t, current.repository, authorization.Binding.Destination.RepositoryRef,
	)
	current.repository.now++
	use, err := ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if err != nil {
		t.Fatal(err)
	}
	current.repository.now++
	_, err = ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if !errors.Is(err, ErrAlreadyConsumed) {
		t.Fatalf("replay error = %v, want ErrAlreadyConsumed", err)
	}
	files, err := os.ReadDir(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("durable records = %d, want 1", len(files))
	}
	path := filepath.Join(store.directory, files[0].Name())
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(use)
	if string(payload) != string(canonical) {
		t.Fatalf("record is not canonical JSON\n got: %s\nwant: %s", payload, canonical)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode = %o, want 600", info.Mode().Perm())
	}
}

func TestDirectoryUseStoreConcurrentCASHasOneWinner(t *testing.T) {
	t.Parallel()
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t, current.repository, authorization.Binding.Destination.RepositoryRef,
	)
	current.repository.now++
	const contenders = 32
	var winners atomic.Int32
	var replays atomic.Int32
	var unexpected atomic.Value
	var wait sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := ConsumeAuthorization(
				context.Background(), current.repository, current.repository,
				authorizationRef, useRequest(authorization), store,
			)
			switch {
			case err == nil:
				winners.Add(1)
			case errors.Is(err, ErrAlreadyConsumed):
				replays.Add(1)
			default:
				unexpected.Store(fmt.Sprintf("%v", err))
			}
		}()
	}
	close(start)
	wait.Wait()
	if value := unexpected.Load(); value != nil {
		t.Fatalf("unexpected concurrent error: %s", value)
	}
	if winners.Load() != 1 || replays.Load() != contenders-1 {
		t.Fatalf("winners/replays = %d/%d, want 1/%d", winners.Load(), replays.Load(), contenders-1)
	}
}

func TestDirectoryUseStoreOneShotSpansIndependentHandles(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	repositoryRef := authorization.Binding.Destination.RepositoryRef
	first := openScenarioUseStore(t, current.repository, repositoryRef)
	second := openAliasedScenarioUseStore(
		t,
		current.repository,
		repositoryRef,
		first.repositoryRoot,
	)

	current.repository.now++
	var winners atomic.Int32
	var replays atomic.Int32
	var unexpected atomic.Value
	var wait sync.WaitGroup
	start := make(chan struct{})
	stores := []*DirectoryUseStore{first, second}
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func(store *DirectoryUseStore) {
			defer wait.Done()
			<-start
			_, consumeErr := ConsumeAuthorization(
				context.Background(),
				current.repository,
				current.repository,
				authorizationRef,
				useRequest(authorization),
				store,
			)
			switch {
			case consumeErr == nil:
				winners.Add(1)
			case errors.Is(consumeErr, ErrAlreadyConsumed):
				replays.Add(1)
			default:
				unexpected.Store(consumeErr.Error())
			}
		}(stores[index%len(stores)])
	}
	close(start)
	wait.Wait()
	if value := unexpected.Load(); value != nil {
		t.Fatalf("independent-handle contender error: %s", value)
	}
	if winners.Load() != 1 || replays.Load() != 31 {
		t.Fatalf(
			"independent-handle winners/replays = %d/%d, want 1/31",
			winners.Load(),
			replays.Load(),
		)
	}
}

func TestTrustedClockExpiryAndBindingFailBeforeReservation(t *testing.T) {
	t.Parallel()
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t, current.repository, authorization.Binding.Destination.RepositoryRef,
	)
	request := useRequest(authorization)
	request.Destination.ObservedRevision = "git:stale"
	current.repository.now++
	_, err := ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, request, store,
	)
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("binding error = %v, want ErrBindingMismatch", err)
	}
	current.repository.now = authorization.Binding.ExpiresAt
	_, err = ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("trusted-clock expiry error = %v, want ErrExpired", err)
	}
	files, err := os.ReadDir(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("failed uses created %d durable records", len(files))
	}
}

func TestGovernanceAndRARBindingsCannotCrossCandidateOrDestination(t *testing.T) {
	t.Parallel()
	current := newScenario(t, RouteEmergency, reviewtransaction.VerificationAggregateUnavailable, true, false)
	current.repository.now++
	humanRef := putExceptionGovernance(t, current)
	governance := current.repository.governance[humanRef]
	candidate := *governance.Candidate
	candidate.Digest = testDigest("f")
	governance.Candidate = &candidate
	poisonedRef := mustRef(t, governance)
	current.repository.governance[poisonedRef] = governance
	_, err := IssueExceptionAuthorization(
		context.Background(), current.repository, current.repository,
		ExceptionAuthorizationIssueRequest{
			AuthorizationIssueRequest: AuthorizationIssueRequest{
				DecisionRef: current.decisionRef, Nonce: "nonce:cross-candidate",
				AuthorityRef: current.authorityRef,
			},
			HumanDecisionRef: poisonedRef,
		},
	)
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("cross-candidate governance error = %v, want ErrBindingMismatch", err)
	}
}

func TestRouteAndExceptionAuthorityCannotOutliveOwnerDecision(t *testing.T) {
	t.Parallel()
	issue := newScenario(
		t, RoutePRWithIssue, reviewtransaction.VerificationAggregateComplete, false, false,
	)
	governance := issue.repository.governance[issue.governanceRef]
	issue.repository.now = governance.ExpiresAt
	_, err := Decide(
		context.Background(), issue.repository, issue.repository,
		DeliveryRequest{
			AdmissionDecisionRef: issue.admissionRef, PolicyRef: issue.policyRef,
			ReviewReceiptRef: issue.receiptRef, VerificationResultRef: issue.resultRef,
			GateRef: issue.gateRef, AuthorityRef: issue.authorityRef,
		},
	)
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("expired issue governance error = %v, want ErrBindingMismatch", err)
	}

	exceptional := newScenario(
		t, RouteEmergency, reviewtransaction.VerificationAggregateUnavailable, true, false,
	)
	exceptional.repository.now++
	humanRef := putExceptionGovernance(t, exceptional)
	human := exceptional.repository.governance[humanRef]
	risk := exceptional.repository.risks[human.ResidualRiskRef]
	authorization, err := IssueExceptionAuthorization(
		context.Background(), exceptional.repository, exceptional.repository,
		ExceptionAuthorizationIssueRequest{
			AuthorizationIssueRequest: AuthorizationIssueRequest{
				DecisionRef: exceptional.decisionRef, Nonce: "nonce:bounded-exception",
				AuthorityRef: exceptional.authorityRef,
			},
			HumanDecisionRef: humanRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Binding.ExpiresAt > human.ExpiresAt ||
		authorization.Binding.ExpiresAt > risk.ExpiresAt {
		t.Fatalf(
			"exception expiry %d exceeds governance/risk %d/%d",
			authorization.Binding.ExpiresAt, human.ExpiresAt, risk.ExpiresAt,
		)
	}
}

func TestGateFreshnessUsesTrustedClockAndCapsAuthorization(t *testing.T) {
	t.Parallel()
	current := newScenario(
		t, RouteDirectMain, reviewtransaction.VerificationAggregateComplete, false, false,
	)
	current.repository.now = current.decision.Gates.ExpiresAt
	_, err := Decide(
		context.Background(), current.repository, current.repository,
		DeliveryRequest{
			AdmissionDecisionRef: current.admissionRef, PolicyRef: current.policyRef,
			ReviewReceiptRef: current.receiptRef, VerificationResultRef: current.resultRef,
			GateRef: current.gateRef, AuthorityRef: current.authorityRef,
		},
	)
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("expired gate error = %v, want ErrBindingMismatch", err)
	}

	current.repository.now = current.decision.DecidedAt + 1
	authorization, err := IssueAuthorization(
		context.Background(), current.repository, current.repository,
		AuthorizationIssueRequest{
			DecisionRef: current.decisionRef, Nonce: "nonce:gate-deadline",
			AuthorityRef: current.authorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Binding.ExpiresAt > current.decision.Gates.ExpiresAt {
		t.Fatalf(
			"authorization expiry %d exceeds gate %d",
			authorization.Binding.ExpiresAt, current.decision.Gates.ExpiresAt,
		)
	}
}

func TestStaleTrustedPolicyCannotBeSelectedOrConsumed(t *testing.T) {
	t.Parallel()
	current, authorizationRef, authorization := normalAuthorization(t)
	replacement, err := NewRoutePolicy(
		"policy:delivery", "revision:2", RouteDirectMain,
		true, false, false, 120, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	replacementRef := putPolicy(t, current.repository, replacement)
	current.repository.livePolicies[livePolicyKey{
		repository: authorization.Binding.Destination.RepositoryRef,
		route:      authorization.Binding.Route,
	}] = replacementRef
	store := openScenarioUseStore(
		t, current.repository, authorization.Binding.Destination.RepositoryRef,
	)
	current.repository.now++
	_, err = ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("stale policy consume error = %v, want ErrBindingMismatch", err)
	}
	files, readErr := os.ReadDir(store.directory)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(files) != 0 {
		t.Fatalf("stale policy created %d durable reservations", len(files))
	}
}
