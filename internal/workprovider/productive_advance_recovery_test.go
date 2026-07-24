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
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

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
	context.Context,
	evidence.ActionTicket,
	hostruntime.ProcessEvidence,
) (SemanticEvaluation, error) {
	return SemanticEvaluation{}, errors.New(
		"passive productive advance must not execute semantic verification",
	)
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
	start, err := runtimeOutcome.StartOutcome(ctx, OutcomeStartRequest{
		Outcome: "Commit and deliver one passive documentation change.",
	})
	if err != nil {
		t.Fatal(err)
	}
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
