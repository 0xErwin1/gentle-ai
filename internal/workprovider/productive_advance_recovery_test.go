package workprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const productiveAdvanceVerificationHelper = "GENTLE_AI_PRODUCTIVE_ADVANCE_VERIFY"

func TestProductiveAdvanceVerificationHelper(t *testing.T) {
	if os.Getenv(productiveAdvanceVerificationHelper) != "1" {
		return
	}
}

type productiveAdvanceTestCASMode string

const (
	productiveAdvanceTestCASSucceed       productiveAdvanceTestCASMode = "succeed"
	productiveAdvanceTestCASExpired       productiveAdvanceTestCASMode = "pre-effect-expired"
	productiveAdvanceTestCASPostClaim     productiveAdvanceTestCASMode = "post-claim-expired"
	productiveAdvanceTestCASFailed        productiveAdvanceTestCASMode = "failed"
	productiveAdvanceTestCASIndeterminate productiveAdvanceTestCASMode = "indeterminate"
)

type productiveAdvanceTestConnector struct {
	base              runtimeDefaultConnector
	repo              string
	candidateRevision string
	hosting           *padDeliveryTestHosting
	casMode           productiveAdvanceTestCASMode
	verification      []ProductiveVerificationAction
	verificationMu    sync.Mutex
	catalogCalls      int
	semanticCalls     int
	reviewCalls       int
}

func (connector *productiveAdvanceTestConnector) RepositoryRef() string {
	return connector.base.RepositoryRef()
}

func (connector *productiveAdvanceTestConnector) AgentID() model.AgentID {
	return connector.base.AgentID()
}

func (connector *productiveAdvanceTestConnector) ConnectorSessionRef() string {
	return connector.base.ConnectorSessionRef()
}

func (connector *productiveAdvanceTestConnector) Handshake() ProductiveRuntimeHandshake {
	return connector.base.Handshake()
}

func (connector *productiveAdvanceTestConnector) ResolvePolicySnapshot(
	ctx context.Context,
) (ProductivePolicySnapshot, error) {
	return connector.base.ResolvePolicySnapshot(ctx)
}

func (connector *productiveAdvanceTestConnector) ResolveOutcomeIntake(
	ctx context.Context,
	ownerContext OwnerOutcomeContext,
	request OutcomeStartRequest,
) (OwnerOutcomeIntake, error) {
	return connector.base.ResolveOutcomeIntake(ctx, ownerContext, request)
}

func (connector *productiveAdvanceTestConnector) EvaluateSemanticExecution(
	_ context.Context,
	ticket evidence.ActionTicket,
	process hostruntime.ProcessEvidence,
) (SemanticEvaluation, error) {
	connector.verificationMu.Lock()
	defer connector.verificationMu.Unlock()
	if len(connector.verification) == 0 {
		return SemanticEvaluation{}, errors.New(
			"passive productive advance must not execute semantic verification",
		)
	}
	connector.semanticCalls++
	return SemanticEvaluation{
		Schema:               SemanticEvaluationSchemaV1,
		RequirementRef:       ticket.SemanticRequirementRef,
		CandidateRef:         ticket.CandidateRef,
		RequestDigest:        process.RequestDigest,
		ToolchainIdentityRef: process.ToolchainIdentityRef,
		StdoutRawDigest:      process.Stdout.RawDigest,
		StderrRawDigest:      process.Stderr.RawDigest,
		Outcome:              SemanticEvaluationPassed,
	}, nil
}

func (connector *productiveAdvanceTestConnector) ResolveVerificationCatalog(
	_ context.Context,
	request ProductiveVerificationCatalogRequest,
) (ProductiveVerificationCatalog, error) {
	connector.verificationMu.Lock()
	defer connector.verificationMu.Unlock()
	connector.catalogCalls++
	actions := make(
		[]ProductiveVerificationAction,
		len(connector.verification),
	)
	copy(actions, connector.verification)
	return ProductiveVerificationCatalog{
		Schema:  ProductiveVerificationCatalogSchemaV1,
		Subject: request.Subject,
		Actions: actions,
	}, nil
}

func (connector *productiveAdvanceTestConnector) ReviewCandidate(
	_ context.Context,
	request ProductiveReviewRequest,
) (ProductiveReviewResult, error) {
	connector.verificationMu.Lock()
	defer connector.verificationMu.Unlock()
	connector.reviewCalls++
	paths := make(
		[]string,
		len(request.ChangedPathManifest),
	)
	for index, entry := range request.ChangedPathManifest {
		paths[index] = entry.Path
	}
	return ProductiveReviewResult{
		Schema:      ProductiveReviewResultSchemaV1,
		SubjectHash: request.Subject.SubjectHash,
		Inspection: reviewtransaction.ArtifactInspection{
			Status: reviewtransaction.ArtifactInspectionCompleted,
			Paths:  paths,
		},
		Findings: []reviewtransaction.Finding{},
		Evidence: []string{
			"inspected " + paths[0] +
				":1 against the complete frozen candidate",
		},
	}, nil
}

func (connector *productiveAdvanceTestConnector) ResolvePADGitBinding(
	ctx context.Context,
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
) (PADGitBinding, error) {
	binding := PADGitBinding{
		Schema:                 PADGitBindingSchema,
		Candidate:              candidate,
		Destination:            destination,
		Mechanism:              mechanism,
		HostingRepositoryRef:   "hosting:productive-advance-test",
		CandidateRevision:      connector.candidateRevision,
		ExpectedRemoteRevision: destination.ObservedRevision,
	}
	connector.hosting.mu.Lock()
	if connector.hosting.binding.Schema == "" {
		connector.hosting.binding = binding
	} else if connector.hosting.binding != binding {
		connector.hosting.mu.Unlock()
		return PADGitBinding{}, ErrPADCandidateCatalogConflict
	}
	connector.hosting.mu.Unlock()
	return connector.hosting.ResolvePADGitBinding(
		ctx,
		candidate,
		destination,
		mechanism,
	)
}

func (connector *productiveAdvanceTestConnector) ObserveDelivery(
	ctx context.Context,
	request HostingObservationRequest,
) (HostingDeliveryObservation, error) {
	observation, err := connector.hosting.ObserveDelivery(ctx, request)
	if err != nil || connector.casMode != productiveAdvanceTestCASExpired {
		return observation, err
	}
	connector.hosting.mu.Lock()
	observationCalls := connector.hosting.observationCalls
	connector.hosting.mu.Unlock()
	if observationCalls == 2 {
		connector.hosting.clock.SetUnix(
			connector.hosting.clock.Now().Add(24 * time.Hour).Unix(),
		)
	}
	return observation, nil
}

func (connector *productiveAdvanceTestConnector) CompareAndSwapBranch(
	ctx context.Context,
	request HostingBranchCASRequest,
) (HostingBranchCASReceipt, error) {
	switch connector.casMode {
	case productiveAdvanceTestCASPostClaim:
		connector.hosting.mu.Lock()
		connector.hosting.casCalls++
		connector.hosting.lastCAS = request
		connector.hosting.mu.Unlock()
		return HostingBranchCASReceipt{}, deliveryadmission.ErrExpired
	case productiveAdvanceTestCASIndeterminate:
		connector.hosting.mu.Lock()
		connector.hosting.casCalls++
		connector.hosting.lastCAS = request
		connector.hosting.mu.Unlock()
		return HostingBranchCASReceipt{}, ErrPADDeliveryIndeterminate
	case productiveAdvanceTestCASFailed:
		connector.hosting.mu.Lock()
		connector.hosting.casCalls++
		connector.hosting.lastCAS = request
		current, err := connector.hosting.remoteRevision(request.TargetRef)
		if err != nil {
			connector.hosting.mu.Unlock()
			return HostingBranchCASReceipt{}, err
		}
		receipt := connector.hosting.branchReceipt(
			request,
			HostingEffectRejected,
			current,
			"",
		)
		connector.hosting.mu.Unlock()
		return receipt, nil
	default:
		return connector.hosting.CompareAndSwapBranch(ctx, request)
	}
}

func (connector *productiveAdvanceTestConnector) MergePullRequest(
	ctx context.Context,
	request HostingPullRequestMergeRequest,
) (HostingPullRequestMergeReceipt, error) {
	return connector.hosting.MergePullRequest(ctx, request)
}

type productiveAdvanceTestActivation struct {
	hosting            *padDeliveryTestHosting
	disableAfterEffect bool
}

func (resolver *productiveAdvanceTestActivation) ResolveActivation(
	context.Context,
	string,
) (ActivationMode, error) {
	if resolver.disableAfterEffect {
		resolver.hosting.mu.Lock()
		effected := resolver.hosting.casCalls > 0
		resolver.hosting.mu.Unlock()
		if effected {
			return ActivationDisabled, nil
		}
	}
	return ActivationEnabled, nil
}

type productiveAdvanceDisableAfterExecutionAnchor struct {
	store workrun.WorkRunStore
}

func (resolver productiveAdvanceDisableAfterExecutionAnchor) ResolveActivation(
	ctx context.Context,
	_ string,
) (ActivationMode, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	state, err := resolver.store.Status()
	if err != nil {
		return "", err
	}
	if state.ProductiveExecutionResultRef != "" &&
		state.DeliveryResultRef == "" {
		return ActivationDisabled, nil
	}
	return ActivationEnabled, nil
}

type productiveAdvanceTestFixture struct {
	repo       string
	bare       string
	workRunID  string
	base       string
	clock      *padDeliveryTestClock
	runtime    *productiveRuntimeOutcome
	connector  *productiveAdvanceTestConnector
	activation *productiveAdvanceTestActivation
	start      workrun.WorkStatusV1
}

func newProductiveAdvanceTestFixture(
	t *testing.T,
	seed string,
	mode productiveAdvanceTestCASMode,
	disableAfterEffect bool,
) productiveAdvanceTestFixture {
	return newProductiveAdvanceTestFixtureForCandidate(
		t,
		seed,
		mode,
		disableAfterEffect,
		false,
	)
}

func newProductiveActiveAdvanceTestFixture(
	t *testing.T,
	seed string,
) productiveAdvanceTestFixture {
	return newProductiveAdvanceTestFixtureForCandidate(
		t,
		seed,
		productiveAdvanceTestCASSucceed,
		false,
		true,
	)
}

func newProductiveAdvanceTestFixtureForCandidate(
	t *testing.T,
	seed string,
	mode productiveAdvanceTestCASMode,
	disableAfterEffect bool,
	active bool,
) productiveAdvanceTestFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("productive work-advance proof uses real Git repositories")
	}
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	ownerGit(t, repo, "config", "user.name", "Productive Advance Test")
	ownerGit(
		t,
		repo,
		"config",
		"user.email",
		"productive-advance@example.test",
	)
	if err := os.WriteFile(
		filepath.Join(repo, "README.md"),
		[]byte("# Productive advance fixture\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	ownerGit(t, repo, "add", "README.md")
	ownerGit(t, repo, "commit", "--quiet", "-m", "base")
	ownerGit(t, repo, "branch", "-M", "main")
	baseRevision := "git:" + ownerGitOID(t, repo, "HEAD")

	bare := filepath.Join(t.TempDir(), "remote.git")
	ownerGit(t, repo, "init", "--bare", "--quiet", bare)
	ownerGit(t, repo, "remote", "add", "origin", bare)
	ownerGit(
		t,
		repo,
		"push",
		"--quiet",
		"origin",
		"main:refs/heads/main",
	)
	authority, err := NewPADRepositoryAuthority(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	const sessionRef = "session:productive-advance-test"
	snapshot, err := NewProductivePolicySnapshot(
		authority.RepositoryRef(),
		model.AgentCodex,
		sessionRef,
		1,
		runtimePolicyTestPolicies(t, 1, false),
	)
	if err != nil {
		t.Fatal(err)
	}
	workRunID := "productive-advance-" + seed
	intake := ownerOutcomeTestIntake(
		workRunID,
		deliveryadmission.RouteDirectMain,
	)
	intake.ScopeSelectors = []string{"docs/passive-note.md"}
	if active {
		intake.ScopeSelectors = []string{"internal/active.go"}
	}
	intake.Destination.ObservedRevision = baseRevision
	clock := &padDeliveryTestClock{now: time.Now().UTC()}
	hosting := &padDeliveryTestHosting{
		bareRepository:   bare,
		clock:            clock,
		protection:       HostingProtectionPermitted,
		checks:           HostingChecksNotApplicable,
		pullRequestState: HostingPullRequestNotApplicable,
	}
	connector := &productiveAdvanceTestConnector{
		base: runtimeDefaultConnector{
			repositoryRef: authority.RepositoryRef(),
			agent:         model.AgentCodex,
			sessionRef:    sessionRef,
			snapshot:      snapshot,
			intake:        intake,
		},
		repo:    repo,
		hosting: hosting,
		casMode: mode,
	}
	activation := &productiveAdvanceTestActivation{
		hosting:            hosting,
		disableAfterEffect: disableAfterEffect,
	}
	runtimeOutcome := &productiveRuntimeOutcome{
		repo:          repo,
		repositoryRef: authority.RepositoryRef(),
		agent:         model.AgentCodex,
		activation:    activation,
		connector:     connector,
	}
	outcome := "Commit and deliver one passive documentation change."
	if active {
		outcome = "Commit and deliver one small Go source change."
	}
	start, err := runtimeOutcome.StartOutcome(ctx, OutcomeStartRequest{
		Outcome: outcome,
	})
	if err != nil {
		t.Fatal(err)
	}
	if active {
		if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(repo, "internal", "active.go"),
			[]byte("package internal\n\nfunc Active() bool { return true }\n"),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
		ownerGit(t, repo, "add", "internal/active.go")
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		connector.verification = []ProductiveVerificationAction{{
			ID:      "go-test",
			Program: executable,
			Args: []string{
				"-test.run=^TestProductiveAdvanceVerificationHelper$",
			},
			CWD: repo,
			Environment: map[string]string{
				productiveAdvanceVerificationHelper: "1",
			},
			Capability:           "go-test",
			Cost:                 reviewtransaction.VerificationCostQuick,
			DeadlineMilliseconds: int64((10 * time.Second) / time.Millisecond),
			OutputLimits: hostruntime.StreamLimits{
				StdoutBytes: 32 << 10,
				StderrBytes: 32 << 10,
			},
			RedactionLiterals: []string{},
		}}
	} else {
		if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(repo, "docs", "passive-note.md"),
			[]byte("# Passive note\n\nThis change requires no executable verification.\n"),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
		ownerGit(t, repo, "add", "docs/passive-note.md")
	}
	ownerGit(t, repo, "commit", "--quiet", "-m", "docs: add passive note")
	connector.candidateRevision = "git:" + ownerGitOID(t, repo, "HEAD")
	ownerGit(
		t,
		repo,
		"push",
		"--quiet",
		"origin",
		"HEAD:refs/heads/candidate",
	)
	return productiveAdvanceTestFixture{
		repo: repo, bare: bare, workRunID: workRunID,
		base:    baseRevision,
		clock:   clock,
		runtime: runtimeOutcome, connector: connector,
		activation: activation, start: start,
	}
}

func (fixture productiveAdvanceTestFixture) advanceWithOwnerClock(
	t *testing.T,
) (workrun.WorkAdvanceV1, error) {
	t.Helper()
	ctx := context.Background()
	fixture.clock.SetUnix(time.Now().UTC().Unix())
	factory, err := NewProductiveOwnerCoordinatorFactory(
		ctx,
		fixture.repo,
		model.AgentCodex,
		fixture.activation,
		fixture.connector,
	)
	if err != nil {
		t.Fatal(err)
	}
	factory.pad.open = func(
		ctx context.Context,
		authority *PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		return deliveryadmission.OpenTrustedRepository(
			ctx,
			authority,
			authority.RepositoryRef(),
			deliveryadmission.WithTrustedRepositoryClock(fixture.clock),
		)
	}
	factory.padDelivery.now = fixture.clock.Now
	return advanceProductiveWork(
		ctx,
		factory,
		fixture.workRunID,
		fixture.start.Revision,
	)
}

func (fixture productiveAdvanceTestFixture) remoteRevision(
	t *testing.T,
) string {
	t.Helper()
	fixture.connector.hosting.mu.Lock()
	defer fixture.connector.hosting.mu.Unlock()
	revision, err := fixture.connector.hosting.remoteRevision(
		"refs/heads/main",
	)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func ownerGitOID(t *testing.T, repo, revision string) string {
	t.Helper()
	return stringTrimSpace(ownerGit(t, repo, "rev-parse", revision))
}

func stringTrimSpace(value string) string {
	for len(value) > 0 {
		last := value[len(value)-1]
		if last != '\n' && last != '\r' && last != ' ' && last != '\t' {
			break
		}
		value = value[:len(value)-1]
	}
	for len(value) > 0 {
		first := value[0]
		if first != '\n' && first != '\r' && first != ' ' && first != '\t' {
			break
		}
		value = value[1:]
	}
	return value
}

func (fixture productiveAdvanceTestFixture) casCalls() int {
	fixture.connector.hosting.mu.Lock()
	defer fixture.connector.hosting.mu.Unlock()
	return fixture.connector.hosting.casCalls
}

func (fixture productiveAdvanceTestFixture) verificationCalls() (int, int) {
	fixture.connector.verificationMu.Lock()
	defer fixture.connector.verificationMu.Unlock()
	return fixture.connector.semanticCalls, fixture.connector.reviewCalls
}

func (fixture productiveAdvanceTestFixture) verificationCatalogCalls() int {
	fixture.connector.verificationMu.Lock()
	defer fixture.connector.verificationMu.Unlock()
	return fixture.connector.catalogCalls
}

func (fixture productiveAdvanceTestFixture) existingStore(
	t *testing.T,
) (productiveAdvanceStore, *ProductionOwnerCoordinatorFactory) {
	t.Helper()
	ctx := context.Background()
	factory, err := NewProductionOwnerCoordinatorFactory(
		ctx,
		fixture.repo,
		model.AgentCodex,
		StaticActivationResolver{Mode: ActivationDisabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := existingProductiveAdvanceStore(
		factory.lease,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	store.pad = factory.pad
	return store, factory
}

func (fixture productiveAdvanceTestFixture) currentStateAndStatus(
	t *testing.T,
	factory *ProductionOwnerCoordinatorFactory,
) (workrun.WorkRunState, workrun.WorkStatusV1) {
	t.Helper()
	ctx := context.Background()
	coordinator, err := factory.openForProductiveRecovery(
		ctx,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err := coordinator.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status, err := coordinator.work.PublicStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return state, status
}

func TestWorkAdvanceQuickGoUsesOwnerCatalogSemanticEvidenceAndRealReview(
	t *testing.T,
) {
	fixture := newProductiveActiveAdvanceTestFixture(t, "quick-go")
	ctx := context.Background()
	first, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	semanticCalls, reviewCalls := fixture.verificationCalls()
	if first.Status.PublicState != workrun.PublicStateReady ||
		first.DeliveryResultRef == "" ||
		first.Diagnostic != nil ||
		semanticCalls != 1 ||
		reviewCalls != 1 ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"quick Go advance/calls = %#v / semantic:%d review:%d CAS:%d",
			first,
			semanticCalls,
			reviewCalls,
			fixture.casCalls(),
		)
	}

	_, factory := fixture.existingStore(t)
	state, _ := fixture.currentStateAndStatus(t, factory)
	if state.Handoff == nil ||
		len(state.Handoff.DeclaredObligationRefs) != 1 ||
		state.Forecast == nil ||
		state.Forecast.Availability != workrun.ForecastAvailable ||
		state.Forecast.MaximumCost == nil ||
		*state.Forecast.MaximumCost !=
			reviewtransaction.VerificationCostQuick ||
		state.Disposition == nil ||
		state.Disposition.Kind != workrun.DispositionRun ||
		len(state.Reservations) != 1 ||
		len(state.LaunchClaims) != 1 ||
		state.VerificationResultRef == "" ||
		state.ReviewReceiptRef == "" {
		t.Fatalf("quick Go authority state = %#v", state)
	}
	coordinator, err := factory.openForProductiveRecovery(
		ctx,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := coordinator.evidence.ReadExecutionForTicket(
		state.Reservations[0].ActionTicketRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Outcome != evidence.ExecutionComplete ||
		execution.SemanticProof == nil {
		t.Fatalf("quick Go semantic evidence = %#v", execution)
	}
	reviewStore, err := reviewtransaction.CompactAuthoritativeStore(
		ctx,
		fixture.repo,
		productiveAdvanceLineage(
			fixture.connector.RepositoryRef(),
			fixture.workRunID,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(filepath.Join(
		reviewStore.Dir,
		reviewtransaction.CompactReviewerResultsDir,
		"00-"+reviewtransaction.LensReliability+".json",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		string(artifact),
		reviewtransaction.AdmittedReviewerResultSchema,
	) {
		t.Fatal("quick Go review lacks a durable admitted reviewer artifact")
	}

	replayed, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	semanticAfter, reviewAfter := fixture.verificationCalls()
	if err != nil ||
		!reflect.DeepEqual(replayed, first) ||
		semanticAfter != semanticCalls ||
		reviewAfter != reviewCalls ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"quick Go replay = %#v, %v; semantic:%d review:%d CAS:%d",
			replayed,
			err,
			semanticAfter,
			reviewAfter,
			fixture.casCalls(),
		)
	}
}

func TestWorkAdvanceV2QuickOwnerConsentIsIndependentOfCost(
	t *testing.T,
) {
	fixture := newProductiveActiveAdvanceTestFixture(
		t,
		"quick-explicit-consent",
	)
	fixture.connector.verification[0].RequiresConsent = true
	checkpoint, err := fixture.runtime.AdvanceOutcomeV2(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, factory := fixture.existingStore(t)
	state, _ := fixture.currentStateAndStatus(t, factory)
	semanticCalls, reviewCalls := fixture.verificationCalls()
	if checkpoint.VerificationDecision == nil ||
		checkpoint.VerificationDecision.Cost !=
			reviewtransaction.VerificationCostQuick ||
		state.Forecast == nil ||
		!state.Forecast.RequiresConsent ||
		state.Forecast.MaximumCost == nil ||
		*state.Forecast.MaximumCost !=
			reviewtransaction.VerificationCostQuick ||
		state.Disposition != nil ||
		len(state.Reservations) != 0 ||
		len(state.LaunchClaims) != 0 ||
		semanticCalls != 0 ||
		reviewCalls != 0 ||
		fixture.casCalls() != 0 {
		t.Fatalf(
			"quick explicit-consent checkpoint = %#v state=%#v calls=%d/%d/%d",
			checkpoint,
			state,
			semanticCalls,
			reviewCalls,
			fixture.casCalls(),
		)
	}
}

func TestWorkAdvanceV2LongVerificationRequiresPureDecisionThenResumesOnce(
	t *testing.T,
) {
	fixture := newProductiveActiveAdvanceTestFixture(t, "long-consent")
	fixture.connector.verification[0].Cost =
		reviewtransaction.VerificationCostLong
	ctx := context.Background()

	checkpoint, err := fixture.runtime.AdvanceOutcomeV2(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.VerificationDecision == nil ||
		checkpoint.PreviousRevision != fixture.start.Revision ||
		checkpoint.Status.PublicState !=
			workrun.PublicStateNeedsYourDecision ||
		checkpoint.Status.Revision ==
			checkpoint.PreviousRevision ||
		checkpoint.VerificationDecision.ExpectedRevision !=
			checkpoint.Status.Revision ||
		checkpoint.VerificationDecision.Cost !=
			reviewtransaction.VerificationCostLong ||
		checkpoint.Diagnostic != nil ||
		checkpoint.DeliveryResultRef != "" {
		t.Fatalf("long verification checkpoint = %#v", checkpoint)
	}
	semanticCalls, reviewCalls := fixture.verificationCalls()
	if semanticCalls != 0 || reviewCalls != 0 || fixture.casCalls() != 0 {
		t.Fatalf(
			"checkpoint executed work: semantic=%d review=%d CAS=%d",
			semanticCalls,
			reviewCalls,
			fixture.casCalls(),
		)
	}
	advanceStore, recoveryFactory := fixture.existingStore(t)
	checkpointState, _ := fixture.currentStateAndStatus(
		t,
		recoveryFactory,
	)
	if checkpointState.Forecast == nil ||
		checkpointState.Forecast.RequiresConsent ||
		checkpointState.Forecast.MaximumCost == nil ||
		*checkpointState.Forecast.MaximumCost !=
			reviewtransaction.VerificationCostLong ||
		checkpointState.Disposition != nil ||
		checkpointState.ProductiveResumeRevision != "" ||
		len(checkpointState.Reservations) != 0 ||
		len(checkpointState.LaunchClaims) != 0 ||
		checkpointState.VerificationResultRef != "" ||
		checkpointState.ReviewReceiptRef != "" ||
		checkpointState.DeliveryAuthorizationRef != "" {
		t.Fatalf(
			"checkpoint mutated past consent = %#v",
			checkpointState,
		)
	}

	promptPath := filepath.Join(
		advanceStore.root,
		productiveVerificationPromptName(
			checkpoint.VerificationDecision.PromptRef,
		),
	)
	if err := os.Remove(promptPath); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(t.TempDir(), "runtime.token")
	if err := os.WriteFile(tokenPath, []byte("owner-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	connectorOpenCalls := 0
	outageController := NewRuntimeController(
		EnvironmentRuntimeOutcomeOpener{
			Activation: fixture.activation,
			LookupEnv: runtimeDefaultEnvironment(map[string]string{
				ProductiveRuntimeURLEnvironment:       "https://runtime.invalid",
				ProductiveRuntimeTokenFileEnvironment: tokenPath,
			}),
			ConnectorFactory: func(
				context.Context,
				ProductiveRuntimeConnectorConfig,
				string,
			) (ProductiveRuntimeConnector, error) {
				connectorOpenCalls++
				return nil, errors.New("connector outage")
			},
		},
	)
	replayedResult, err := outageController.AdvanceV2(
		ctx,
		RuntimeAdvanceV2Request{
			Repo:             fixture.repo,
			WorkRunID:        fixture.workRunID,
			ExpectedRevision: fixture.start.Revision,
			Contract:         workrun.WorkAdvanceContractV2,
		},
	)
	if err != nil ||
		replayedResult.Advance == nil ||
		!reflect.DeepEqual(*replayedResult.Advance, checkpoint) {
		t.Fatalf(
			"outage checkpoint replay = %#v, %v",
			replayedResult,
			err,
		)
	}
	if connectorOpenCalls != 0 {
		t.Fatalf(
			"fresh outage checkpoint opened connector %d times",
			connectorOpenCalls,
		)
	}
	if _, err := os.Stat(promptPath); err != nil {
		t.Fatalf("checkpoint replay did not repair prompt lookup: %v", err)
	}
	semanticCalls, reviewCalls = fixture.verificationCalls()
	if fixture.verificationCatalogCalls() != 1 ||
		semanticCalls != 0 ||
		reviewCalls != 0 ||
		fixture.casCalls() != 0 {
		t.Fatal("checkpoint replay executed productive work")
	}

	decisionResult, err := outageController.DecideVerification(
		ctx,
		RuntimeVerificationDecisionRequest{
			Repo:      fixture.repo,
			WorkRunID: fixture.workRunID,
			Contract:  workrun.WorkVerificationDecideContractV1,
			PromptRef: checkpoint.VerificationDecision.PromptRef,
			Choice:    workrun.VerificationDecisionRun,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decisionResult.Decision == nil {
		t.Fatal("fresh-process decision returned no receipt")
	}
	decision := *decisionResult.Decision
	if decision.PreviousRevision != checkpoint.Status.Revision ||
		decision.Status.PublicState != workrun.PublicStateChecking ||
		decision.Status.Revision == decision.PreviousRevision ||
		decision.DispositionRef == "" {
		t.Fatalf("run decision receipt = %#v", decision)
	}
	decisionState, _ := fixture.currentStateAndStatus(t, recoveryFactory)
	if decisionState.Disposition == nil ||
		decisionState.Disposition.Kind != workrun.DispositionRun ||
		decisionState.ProductiveResumeRevision !=
			decision.Status.Revision ||
		len(decisionState.Reservations) != 0 ||
		len(decisionState.LaunchClaims) != 0 ||
		decisionState.VerificationResultRef != "" ||
		decisionState.ReviewReceiptRef != "" ||
		decisionState.DeliveryAuthorizationRef != "" {
		t.Fatalf("run decision launched work = %#v", decisionState)
	}
	semanticCalls, reviewCalls = fixture.verificationCalls()
	if semanticCalls != 0 || reviewCalls != 0 || fixture.casCalls() != 0 {
		t.Fatal("run decision executed productive work")
	}
	if connectorOpenCalls != 0 {
		t.Fatalf("pure decision opened connector %d times", connectorOpenCalls)
	}
	replayedDecision, err := fixture.runtime.DecideVerificationOutcome(
		ctx,
		fixture.workRunID,
		checkpoint.VerificationDecision.PromptRef,
		workrun.VerificationDecisionRun,
	)
	if err != nil || !reflect.DeepEqual(replayedDecision, decision) {
		t.Fatalf(
			"decision replay = %#v, %v",
			replayedDecision,
			err,
		)
	}
	if _, err := fixture.runtime.AdvanceOutcomeV2(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	); err == nil {
		t.Fatal("stale pre-consent revision resumed verification")
	}

	terminal, err := fixture.runtime.AdvanceOutcomeV2(
		ctx,
		fixture.workRunID,
		decision.Status.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	semanticCalls, reviewCalls = fixture.verificationCalls()
	if terminal.VerificationDecision != nil ||
		terminal.PreviousRevision != decision.Status.Revision ||
		terminal.Status.PublicState != workrun.PublicStateReady ||
		terminal.DeliveryResultRef == "" ||
		terminal.Diagnostic != nil ||
		semanticCalls != 1 ||
		reviewCalls != 1 ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"resumed terminal/calls = %#v / semantic:%d review:%d CAS:%d",
			terminal,
			semanticCalls,
			reviewCalls,
			fixture.casCalls(),
		)
	}
	replayedTerminal, err := fixture.runtime.AdvanceOutcomeV2(
		ctx,
		fixture.workRunID,
		decision.Status.Revision,
	)
	semanticAfter, reviewAfter := fixture.verificationCalls()
	if err != nil ||
		!reflect.DeepEqual(replayedTerminal, terminal) ||
		semanticAfter != semanticCalls ||
		reviewAfter != reviewCalls ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"resume replay = %#v, %v; semantic:%d review:%d CAS:%d",
			replayedTerminal,
			err,
			semanticAfter,
			reviewAfter,
			fixture.casCalls(),
		)
	}
	terminalDecisionResult, err := outageController.DecideVerification(
		ctx,
		RuntimeVerificationDecisionRequest{
			Repo:      fixture.repo,
			WorkRunID: fixture.workRunID,
			Contract:  workrun.WorkVerificationDecideContractV1,
			PromptRef: checkpoint.VerificationDecision.PromptRef,
			Choice:    workrun.VerificationDecisionRun,
		},
	)
	semanticFinal, reviewFinal := fixture.verificationCalls()
	if err != nil ||
		terminalDecisionResult.Decision == nil ||
		!reflect.DeepEqual(*terminalDecisionResult.Decision, decision) ||
		connectorOpenCalls != 0 ||
		semanticFinal != semanticAfter ||
		reviewFinal != reviewAfter ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"terminal decision replay = %#v, %v; connector:%d semantic:%d review:%d CAS:%d",
			terminalDecisionResult,
			err,
			connectorOpenCalls,
			semanticFinal,
			reviewFinal,
			fixture.casCalls(),
		)
	}
}

func TestWorkVerificationDecisionRejectsPrecreatedUnboundReceipt(
	t *testing.T,
) {
	fixture := newProductiveActiveAdvanceTestFixture(
		t,
		"precreated-decision-receipt",
	)
	fixture.connector.verification[0].Cost =
		reviewtransaction.VerificationCostLong
	ctx := context.Background()
	checkpoint, err := fixture.runtime.AdvanceOutcomeV2(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil || checkpoint.VerificationDecision == nil {
		t.Fatalf("checkpoint = %#v, %v", checkpoint, err)
	}
	store, factory := fixture.existingStore(t)
	fakeStatus := checkpoint.Status
	fakeStatus.Revision = productiveAdvanceSHA256(
		[]byte("unbound-verification-decision-revision"),
	)
	fakeStatus.PublicState = workrun.PublicStateChecking
	fakeReceipt := workrun.WorkVerificationDecideV1{
		Schema:           workrun.WorkVerificationDecideContractV1,
		Contract:         workrun.WorkVerificationDecideContractV1,
		WorkRunID:        fixture.workRunID,
		PreviousRevision: checkpoint.Status.Revision,
		PromptRef:        checkpoint.VerificationDecision.PromptRef,
		ForecastRef:      checkpoint.VerificationDecision.ForecastRef,
		AssumptionsRef:   checkpoint.VerificationDecision.AssumptionsRef,
		Choice:           workrun.VerificationDecisionRun,
		DispositionRef: productiveAdvanceSHA256(
			[]byte("unbound-verification-disposition"),
		),
		Status: fakeStatus,
	}
	if err := fakeReceipt.Validate(); err != nil {
		t.Fatalf("fake receipt fixture is not syntactically valid: %v", err)
	}
	record := productiveVerificationReceiptRecord{
		Schema:        productiveVerificationReceiptSchema,
		RepositoryRef: store.lease.Identity().RepositoryRef,
		WorkRunID:     fixture.workRunID,
		PromptRef:     fakeReceipt.PromptRef,
		Choice:        fakeReceipt.Choice,
		Receipt:       fakeReceipt,
	}
	if err := store.publishJSON(
		ctx,
		productiveVerificationReceiptName(
			fakeReceipt.PromptRef,
			fakeReceipt.Choice,
		),
		record,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runtime.DecideVerificationOutcome(
		ctx,
		fixture.workRunID,
		fakeReceipt.PromptRef,
		fakeReceipt.Choice,
	); err == nil {
		t.Fatal("precreated receipt bypassed durable decision authority")
	}
	state, _ := fixture.currentStateAndStatus(t, factory)
	semanticCalls, reviewCalls := fixture.verificationCalls()
	if state.Disposition != nil ||
		state.ProductiveResumeRevision != "" ||
		len(state.Reservations) != 0 ||
		len(state.LaunchClaims) != 0 ||
		semanticCalls != 0 ||
		reviewCalls != 0 ||
		fixture.casCalls() != 0 {
		t.Fatalf(
			"rejected precreated receipt mutated state: %#v calls=%d/%d/%d",
			state,
			semanticCalls,
			reviewCalls,
			fixture.casCalls(),
		)
	}
}

func TestWorkAdvanceV2NonRunDecisionsNeverCreateResumeAuthority(
	t *testing.T,
) {
	for _, choice := range []workrun.VerificationDecisionChoice{
		workrun.VerificationDecisionDefer,
		workrun.VerificationDecisionReduceScope,
	} {
		t.Run(string(choice), func(t *testing.T) {
			fixture := newProductiveActiveAdvanceTestFixture(
				t,
				"long-"+string(choice),
			)
			fixture.connector.verification[0].Cost =
				reviewtransaction.VerificationCostLong
			ctx := context.Background()
			checkpoint, err := fixture.runtime.AdvanceOutcomeV2(
				ctx,
				fixture.workRunID,
				fixture.start.Revision,
			)
			if err != nil || checkpoint.VerificationDecision == nil {
				t.Fatalf("checkpoint = %#v, %v", checkpoint, err)
			}
			decision, err := fixture.runtime.DecideVerificationOutcome(
				ctx,
				fixture.workRunID,
				checkpoint.VerificationDecision.PromptRef,
				choice,
			)
			if err != nil {
				t.Fatal(err)
			}
			_, factory := fixture.existingStore(t)
			state, _ := fixture.currentStateAndStatus(t, factory)
			semanticCalls, reviewCalls := fixture.verificationCalls()
			if decision.Choice != choice ||
				decision.Status.PublicState !=
					workrun.PublicStateNeedsYourDecision ||
				state.Disposition == nil ||
				state.Disposition.DecisionRef !=
					decision.DispositionRef ||
				state.ProductiveResumeRevision !=
					decision.Status.Revision ||
				len(state.Reservations) != 0 ||
				len(state.LaunchClaims) != 0 ||
				state.VerificationResultRef != "" ||
				state.ReviewReceiptRef != "" ||
				state.DeliveryAuthorizationRef != "" ||
				semanticCalls != 0 ||
				reviewCalls != 0 ||
				fixture.casCalls() != 0 {
				t.Fatalf(
					"non-run decision created work authority: receipt=%#v state=%#v calls=%d/%d/%d",
					decision,
					state,
					semanticCalls,
					reviewCalls,
					fixture.casCalls(),
				)
			}
			if _, err := fixture.runtime.AdvanceOutcomeV2(
				ctx,
				fixture.workRunID,
				decision.Status.Revision,
			); err == nil {
				t.Fatal("non-run decision authorized a productive resume")
			}
			semanticCalls, reviewCalls = fixture.verificationCalls()
			if semanticCalls != 0 ||
				reviewCalls != 0 ||
				fixture.casCalls() != 0 {
				t.Fatal("rejected non-run resume executed productive work")
			}
		})
	}
}

func TestWorkAdvanceReadySurvivesConsumedAuthorizationExpiryAndRestart(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"ready-restart",
		productiveAdvanceTestCASSucceed,
		false,
	)
	ctx := context.Background()
	first, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status.PublicState != workrun.PublicStateReady ||
		first.DeliveryResultRef == "" ||
		first.Diagnostic != nil ||
		fixture.casCalls() != 1 ||
		fixture.remoteRevision(t) != fixture.connector.candidateRevision {
		t.Fatalf("initial Ready advance/effect = %#v / %d", first, fixture.casCalls())
	}

	store, factory := fixture.existingStore(t)
	factory.pad.open = func(
		ctx context.Context,
		authority *PADRepositoryAuthority,
	) (padTrustedRepository, error) {
		return deliveryadmission.OpenTrustedRepository(
			ctx,
			authority,
			authority.RepositoryRef(),
			deliveryadmission.WithTrustedRepositoryClock(
				ownerTestClock{now: time.Now().UTC().Add(24 * time.Hour).Unix()},
			),
		)
	}
	replayed, err := recoverProductiveWork(
		ctx,
		factory,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatalf("restart replay after authorization expiry: %v", err)
	}
	if !reflect.DeepEqual(replayed, first) || fixture.casCalls() != 1 {
		t.Fatalf(
			"restart replay/effects changed:\nfirst  %#v\nreplay %#v\nCAS %d",
			first,
			replayed,
			fixture.casCalls(),
		)
	}
	state, status := fixture.currentStateAndStatus(t, factory)
	if status.PublicState != workrun.PublicStateReady ||
		state.DeliveryResultRef != first.DeliveryResultRef {
		t.Fatalf("restarted terminal state/status = %#v / %#v", state, status)
	}
	if cached, ok, err := store.result(
		ctx,
		fixture.start.Revision,
		state,
		status,
	); err != nil || !ok || !reflect.DeepEqual(cached, first) {
		t.Fatalf("exact cached replay = %#v, %t, %v", cached, ok, err)
	}
}

func TestWorkAdvanceCachedReadyRevalidatesLiveWorkRunAndTerminalAuthority(
	t *testing.T,
) {
	t.Run("live WorkRun mismatch", func(t *testing.T) {
		fixture := newProductiveAdvanceTestFixture(
			t,
			"ready-workrun-mismatch",
			productiveAdvanceTestCASSucceed,
			false,
		)
		first, err := fixture.runtime.AdvanceOutcome(
			context.Background(),
			fixture.workRunID,
			fixture.start.Revision,
		)
		if err != nil {
			t.Fatal(err)
		}
		store, factory := fixture.existingStore(t)
		state, status := fixture.currentStateAndStatus(t, factory)
		state.DeliveryResultRef = ownerTestRef("foreign-terminal-result")
		if _, ok, err := store.result(
			context.Background(),
			fixture.start.Revision,
			state,
			status,
		); err == nil || ok {
			t.Fatalf("mismatched WorkRun replay accepted: %#v, %t", first, ok)
		}
	})

	for _, test := range []struct {
		name   string
		target string
		remove bool
	}{
		{name: "tampered cached result", target: "cache"},
		{name: "tampered terminal authority", target: "authority"},
		{name: "missing terminal authority", target: "authority", remove: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProductiveAdvanceTestFixture(
				t,
				"ready-"+test.name[0:7],
				productiveAdvanceTestCASSucceed,
				false,
			)
			first, err := fixture.runtime.AdvanceOutcome(
				context.Background(),
				fixture.workRunID,
				fixture.start.Revision,
			)
			if err != nil {
				t.Fatal(err)
			}
			store, factory := fixture.existingStore(t)
			var path string
			if test.target == "cache" {
				path = filepath.Join(
					store.root,
					store.resultName(fixture.start.Revision),
				)
			} else {
				path = filepath.Join(
					store.root,
					"delivery-result-"+
						productiveAdvanceRevisionKey(first.DeliveryResultRef)+
						".json",
				)
			}
			if test.remove {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				payload, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				payload[len(payload)/2] ^= 1
				if err := os.WriteFile(path, payload, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := recoverProductiveWork(
				context.Background(),
				factory,
				fixture.workRunID,
				fixture.start.Revision,
			); err == nil {
				t.Fatal("corrupt or absent authority produced a terminal replay")
			}
		})
	}

	t.Run("missing cache is reconstructed only from live terminal authority", func(t *testing.T) {
		fixture := newProductiveAdvanceTestFixture(
			t,
			"ready-missing-cache",
			productiveAdvanceTestCASSucceed,
			false,
		)
		first, err := fixture.runtime.AdvanceOutcome(
			context.Background(),
			fixture.workRunID,
			fixture.start.Revision,
		)
		if err != nil {
			t.Fatal(err)
		}
		store, factory := fixture.existingStore(t)
		state, status := fixture.currentStateAndStatus(t, factory)
		cachePath := filepath.Join(
			store.root,
			store.resultName(fixture.start.Revision),
		)
		if err := os.Remove(cachePath); err != nil {
			t.Fatal(err)
		}
		if cached, ok, err := store.result(
			context.Background(),
			fixture.start.Revision,
			state,
			status,
		); err != nil || ok {
			t.Fatalf("missing cache was accepted as replay: %#v, %t, %v", cached, ok, err)
		}
		reconstructed, err := recoverProductiveWork(
			context.Background(),
			factory,
			fixture.workRunID,
			fixture.start.Revision,
		)
		if err != nil || !reflect.DeepEqual(reconstructed, first) ||
			fixture.casCalls() != 1 {
			t.Fatalf(
				"authority-only reconstruction = %#v, %v; CAS %d",
				reconstructed,
				err,
				fixture.casCalls(),
			)
		}
	})
}

func TestWorkAdvanceNeedsDecisionBindsExactStageAndReplaysStableDiagnostic(
	t *testing.T,
) {
	tests := []struct {
		name     string
		mode     productiveAdvanceTestCASMode
		code     workrun.WorkAdvanceDiagnosticCode
		next     workrun.WorkAdvanceDiagnosticNextAction
		casCalls int
	}{
		{
			name:     "expired before first effect",
			mode:     productiveAdvanceTestCASExpired,
			code:     workrun.WorkAdvanceDiagnosticDeliveryAuthorizationExpired,
			next:     workrun.WorkAdvanceNextActionStartFresh,
			casCalls: 0,
		},
		{
			name:     "failed effect",
			mode:     productiveAdvanceTestCASFailed,
			code:     workrun.WorkAdvanceDiagnosticDeliveryEffectFailed,
			next:     workrun.WorkAdvanceNextActionStartFresh,
			casCalls: 1,
		},
		{
			name:     "expired after durable claim",
			mode:     productiveAdvanceTestCASPostClaim,
			code:     workrun.WorkAdvanceDiagnosticDeliveryOutcomeIndeterminate,
			next:     workrun.WorkAdvanceNextActionReconcile,
			casCalls: 1,
		},
		{
			name:     "indeterminate effect",
			mode:     productiveAdvanceTestCASIndeterminate,
			code:     workrun.WorkAdvanceDiagnosticDeliveryOutcomeIndeterminate,
			next:     workrun.WorkAdvanceNextActionReconcile,
			casCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProductiveAdvanceTestFixture(
				t,
				"decision-"+string(test.mode),
				test.mode,
				false,
			)
			var first workrun.WorkAdvanceV1
			var err error
			if test.mode == productiveAdvanceTestCASExpired {
				first, err = fixture.advanceWithOwnerClock(t)
			} else {
				first, err = fixture.runtime.AdvanceOutcome(
					context.Background(),
					fixture.workRunID,
					fixture.start.Revision,
				)
			}
			if err != nil {
				t.Fatal(err)
			}
			message, ok := workrun.WorkAdvanceDiagnosticMessage(test.code)
			if !ok {
				t.Fatalf("test diagnostic %q is not closed", test.code)
			}
			diagnosticErr := error(nil)
			if first.Diagnostic != nil {
				diagnosticErr = first.Diagnostic.Validate()
			}
			if first.Status.PublicState != workrun.PublicStateNeedsYourDecision ||
				first.DeliveryResultRef != "" ||
				first.Diagnostic == nil ||
				first.Diagnostic.Code != test.code ||
				first.Diagnostic.Message != message ||
				first.Diagnostic.NextAction != test.next ||
				first.Status.Diagnostic == nil ||
				!reflect.DeepEqual(
					first.Status.Diagnostic,
					first.Diagnostic,
				) ||
				diagnosticErr != nil {
				t.Fatalf(
					"bounded diagnostic = %#v; diagnostic = %#v; want code=%q message=%q; validate=%v",
					first,
					first.Diagnostic,
					test.code,
					message,
					diagnosticErr,
				)
			}
			if fixture.casCalls() != test.casCalls {
				t.Fatalf(
					"terminal blocker CAS calls = %d, want %d",
					fixture.casCalls(),
					test.casCalls,
				)
			}
			if fixture.remoteRevision(t) != fixture.base {
				t.Fatal("pre-effect terminal blocker changed the delivery destination")
			}
			replayed, err := fixture.runtime.AdvanceOutcome(
				context.Background(),
				fixture.workRunID,
				fixture.start.Revision,
			)
			if err != nil || !reflect.DeepEqual(replayed, first) ||
				fixture.casCalls() != test.casCalls {
				t.Fatalf(
					"diagnostic replay = %#v, %v; CAS %d",
					replayed,
					err,
					fixture.casCalls(),
				)
			}

			store, factory := fixture.existingStore(t)
			state, status := fixture.currentStateAndStatus(t, factory)
			resolved, err := store.resolveDiagnosticForState(
				context.Background(),
				first.Diagnostic.Ref,
				state,
			)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.WorkRevision != state.ProductiveBlockerSourceRevision ||
				resolved.DeliveryIntentRef != state.DeliveryIntentRef ||
				!reflect.DeepEqual(resolved.Handoff, state.Handoff) ||
				resolved.VerificationResultRef != state.VerificationResultRef ||
				resolved.ReviewReceiptRef != state.ReviewReceiptRef ||
				resolved.DeliveryAuthorizationRef != state.DeliveryAuthorizationRef {
				t.Fatalf("diagnostic did not bind exact terminal stage: %#v / %#v", resolved, state)
			}
			mismatched := state
			mismatched.ProductiveBlockerSourceRevision =
				ownerTestRef("another-source-revision")
			if _, err := store.resolveDiagnosticForState(
				context.Background(),
				first.Diagnostic.Ref,
				mismatched,
			); err == nil {
				t.Fatal("diagnostic replay accepted another WorkRun source revision")
			}
			if _, err := store.resolveDiagnosticForState(
				context.Background(),
				ownerTestRef("forged-diagnostic-authority"),
				state,
			); err == nil {
				t.Fatal("diagnostic replay accepted a hash-shaped fake authority")
			}
			padCalls := 0
			store.pad.open = func(
				context.Context,
				*PADRepositoryAuthority,
			) (padTrustedRepository, error) {
				padCalls++
				return nil, errors.New("live PAD must not resolve a terminal blocker")
			}
			cached, ok, err := store.result(
				context.Background(),
				fixture.start.Revision,
				state,
				status,
			)
			if err != nil || !ok || !reflect.DeepEqual(cached, first) {
				t.Fatalf("blocked cached replay = %#v, %t, %v", cached, ok, err)
			}
			if padCalls != 0 {
				t.Fatalf("terminal blocker replay resolved live PAD %d times", padCalls)
			}
		})
	}
}

func TestWorkAdvanceFactualGitFailuresUseSpecificBlockers(t *testing.T) {
	t.Run("candidate has the wrong base", func(t *testing.T) {
		fixture := newProductiveAdvanceTestFixture(
			t,
			"candidate-wrong-base",
			productiveAdvanceTestCASSucceed,
			false,
		)
		ownerGit(t, fixture.repo, "checkout", "--quiet", "--orphan", "divergent")
		ownerGit(
			t,
			fixture.repo,
			"rm",
			"--quiet",
			"-rf",
			"--ignore-unmatch",
			".",
		)
		if err := os.MkdirAll(filepath.Join(fixture.repo, "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(fixture.repo, "docs", "passive-note.md"),
			[]byte("# Divergent passive note\n"),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
		ownerGit(t, fixture.repo, "add", "docs/passive-note.md")
		ownerGit(t, fixture.repo, "commit", "--quiet", "-m", "divergent candidate")

		first, err := fixture.runtime.AdvanceOutcome(
			context.Background(),
			fixture.workRunID,
			fixture.start.Revision,
		)
		if err != nil {
			t.Fatal(err)
		}
		assertProductiveAdvanceDiagnostic(
			t,
			first,
			workrun.WorkAdvanceDiagnosticCandidateWrongBase,
		)
		if fixture.casCalls() != 0 || fixture.remoteRevision(t) != fixture.base {
			t.Fatalf(
				"wrong-base candidate reached delivery: CAS=%d remote=%q",
				fixture.casCalls(),
				fixture.remoteRevision(t),
			)
		}
		replayed, err := fixture.runtime.AdvanceOutcome(
			context.Background(),
			fixture.workRunID,
			fixture.start.Revision,
		)
		if err != nil || !reflect.DeepEqual(replayed, first) ||
			fixture.casCalls() != 0 {
			t.Fatalf("wrong-base replay = %#v, %v", replayed, err)
		}
	})

	t.Run("candidate commit tree changed", func(t *testing.T) {
		fixture := newProductiveAdvanceTestFixture(
			t,
			"candidate-tree-changed",
			productiveAdvanceTestCASSucceed,
			false,
		)
		ctx := context.Background()
		factory, err := NewProductiveOwnerCoordinatorFactory(
			ctx,
			fixture.repo,
			model.AgentCodex,
			fixture.activation,
			fixture.connector,
		)
		if err != nil {
			t.Fatal(err)
		}
		objects := factory.candidateStore.objects
		realGit := objects.executable
		fakeGit := filepath.Join(t.TempDir(), "git")
		script := "#!/bin/sh\n" +
			"last=''\n" +
			"for arg do last=\"$arg\"; done\n" +
			"case \"$last\" in\n" +
			"  *'^{tree}') printf '%s\\n' '" +
			strings.Repeat("0", objects.objectIDLength) +
			"'; exit 0 ;;\n" +
			"esac\n" +
			"exec '" + realGit + "' \"$@\"\n"
		if err := os.WriteFile(fakeGit, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		objects.executable = fakeGit

		first, err := advanceProductiveWork(
			ctx,
			factory,
			fixture.workRunID,
			fixture.start.Revision,
		)
		if err != nil {
			t.Fatal(err)
		}
		assertProductiveAdvanceDiagnostic(
			t,
			first,
			workrun.WorkAdvanceDiagnosticCandidateChanged,
		)
		if fixture.casCalls() != 0 || fixture.remoteRevision(t) != fixture.base {
			t.Fatalf(
				"changed candidate tree reached delivery: CAS=%d remote=%q",
				fixture.casCalls(),
				fixture.remoteRevision(t),
			)
		}
		replayed, err := advanceProductiveWork(
			ctx,
			factory,
			fixture.workRunID,
			fixture.start.Revision,
		)
		if err != nil || !reflect.DeepEqual(replayed, first) ||
			fixture.casCalls() != 0 {
			t.Fatalf("changed-tree replay = %#v, %v", replayed, err)
		}
	})
}

func TestProductiveGitObservationCancellationRemainsNonTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	blocker, err := productiveGitObservationFailure(
		ctx,
		ErrPADGitObjectUnavailable,
		workrun.WorkAdvanceDiagnosticCandidateChanged,
	)
	if blocker != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Git observation = %q, %v", blocker, err)
	}
}

func assertProductiveAdvanceDiagnostic(
	t *testing.T,
	advance workrun.WorkAdvanceV1,
	code workrun.WorkAdvanceDiagnosticCode,
) {
	t.Helper()
	message, ok := workrun.WorkAdvanceDiagnosticMessage(code)
	if !ok {
		t.Fatalf("diagnostic code %q is not closed", code)
	}
	if advance.Status.PublicState != workrun.PublicStateNeedsYourDecision ||
		advance.DeliveryResultRef != "" ||
		advance.Diagnostic == nil ||
		advance.Diagnostic.Code != code ||
		advance.Diagnostic.Message != message ||
		advance.Diagnostic.Validate() != nil {
		t.Fatalf("bounded diagnostic = %#v / %#v", advance, advance.Diagnostic)
	}
}

func TestWorkAdvanceConcurrentSameExpectedRevisionHasOneEffectAndExactReplay(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"concurrent",
		productiveAdvanceTestCASSucceed,
		false,
	)
	const workers = 8
	results := make([]workrun.WorkAdvanceV1, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results[index], errs[index] = fixture.runtime.AdvanceOutcome(
				context.Background(),
				fixture.workRunID,
				fixture.start.Revision,
			)
		}()
	}
	close(start)
	wait.Wait()
	var firstErr error
	var concurrentTerminal *workrun.WorkAdvanceV1
	for index := range workers {
		if errs[index] != nil {
			if firstErr == nil {
				firstErr = errs[index]
			}
			t.Logf("concurrent worker %d: %v", index, errs[index])
			continue
		}
		if concurrentTerminal == nil {
			value := results[index]
			concurrentTerminal = &value
			continue
		}
		if !reflect.DeepEqual(results[index], *concurrentTerminal) {
			t.Fatalf("concurrent worker %d observed another terminal", index)
		}
	}
	if firstErr != nil {
		t.Fatalf("concurrent work advance did not converge: %v", firstErr)
	}
	if concurrentTerminal == nil {
		t.Fatal("concurrent work advance returned no terminal")
	}
	if concurrentTerminal.Status.PublicState != workrun.PublicStateReady ||
		fixture.casCalls() != 1 ||
		fixture.remoteRevision(t) != fixture.connector.candidateRevision {
		t.Fatalf("concurrent terminal/effects = %#v / %d", *concurrentTerminal, fixture.casCalls())
	}
	lostResponseReplay, err := fixture.runtime.AdvanceOutcome(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil || !reflect.DeepEqual(lostResponseReplay, *concurrentTerminal) ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"lost-response replay = %#v, %v; CAS %d",
			lostResponseReplay,
			err,
			fixture.casCalls(),
		)
	}
}

func TestWorkAdvanceKillSwitchBeforeExecutionAnchorRequiresManualReconciliation(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"kill-switch-recovery",
		productiveAdvanceTestCASSucceed,
		true,
	)
	_, err := fixture.runtime.AdvanceOutcome(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
	)
	if !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("post-effect kill switch error = %v", err)
	}
	if fixture.casCalls() != 1 {
		t.Fatalf("first attempt effects = %d, want 1", fixture.casCalls())
	}
	if fixture.remoteRevision(t) != fixture.connector.candidateRevision {
		t.Fatal("kill-switch test did not cross the durable delivery-effect boundary")
	}
	workStore, err := workrun.OpenWorkRunStore(
		context.Background(),
		fixture.repo,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	crashed, err := workStore.Status()
	if err != nil {
		t.Fatal(err)
	}
	if crashed.ProductiveExecutionResultRef != "" ||
		crashed.DeliveryResultRef != "" ||
		crashed.ProductiveBlockerRef != "" {
		t.Fatalf("pre-anchor crash unexpectedly committed terminal authority: %#v", crashed)
	}
	recovered, err := fixture.runtime.AdvanceOutcome(
		context.Background(),
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatalf("read-only recovery: %v", err)
	}
	if recovered.Status.PublicState != workrun.PublicStateNeedsYourDecision ||
		recovered.DeliveryResultRef != "" ||
		recovered.Diagnostic == nil ||
		recovered.Diagnostic.Code !=
			workrun.WorkAdvanceDiagnosticDeliveryOutcomeIndeterminate ||
		recovered.Diagnostic.NextAction !=
			workrun.WorkAdvanceNextActionReconcile ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"read-only recovery/effects = %#v / %d",
			recovered,
			fixture.casCalls(),
		)
	}
	reconciled, err := fixture.runtime.ReconcileOutcome(
		context.Background(),
		fixture.workRunID,
		recovered.Status.Revision,
		recovered.Diagnostic.Ref,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.Outcome != workrun.WorkReconcileManualResolution ||
		reconciled.DeliveryResultRef != "" ||
		reconciled.Status.PublicState !=
			workrun.PublicStateNeedsYourDecision ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"pre-anchor reconciliation/effects = %#v / %d",
			reconciled,
			fixture.casCalls(),
		)
	}
}

func TestWorkAdvanceKillSwitchAfterExecutionAnchorRecoversExactDeliveryWithoutSecondEffect(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"kill-switch-post-anchor-recovery",
		productiveAdvanceTestCASSucceed,
		false,
	)
	ctx := context.Background()
	workStore, err := workrun.OpenWorkRunStore(
		ctx,
		fixture.repo,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.runtime.activation =
		productiveAdvanceDisableAfterExecutionAnchor{store: workStore}
	if _, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	); !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("post-anchor kill switch error = %v", err)
	}
	crashed, err := workStore.Status()
	if err != nil {
		t.Fatal(err)
	}
	if crashed.ProductiveExecutionResultRef == "" ||
		crashed.DeliveryResultRef != "" ||
		crashed.ProductiveBlockerRef != "" ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"post-anchor crash state/effects = %#v / %d",
			crashed,
			fixture.casCalls(),
		)
	}

	recovered, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status.PublicState != workrun.PublicStateReady ||
		recovered.DeliveryResultRef == "" ||
		recovered.Diagnostic != nil ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"post-anchor recovery/effects = %#v / %d",
			recovered,
			fixture.casCalls(),
		)
	}
	replayed, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil ||
		!reflect.DeepEqual(replayed, recovered) ||
		fixture.casCalls() != 1 {
		t.Fatalf(
			"post-anchor exact replay = %#v, %v; CAS %d",
			replayed,
			err,
			fixture.casCalls(),
		)
	}
}
