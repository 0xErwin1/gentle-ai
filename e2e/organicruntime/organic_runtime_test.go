package organicruntime_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/versions"
	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const (
	realAgentE2EEnvironment             = "GENTLE_AI_REAL_AGENT_E2E"
	testBinaryEnvironment               = "GENTLE_AI_TEST_BINARY"
	pinnedOpenCodeVersion               = versions.OpenCode
	runtimeBearerToken                  = "organic-e2e-owner-secret"
	runtimeSessionRef                   = "session:organic-runtime-e2e"
	organicAgentTimeout                 = 4 * time.Minute
	organicSetupTimeout                 = 2 * time.Minute
	organicLocalTimeout                 = 30 * time.Second
	organicCommandWaitDelay             = 10 * time.Second
	organicShortAuthorizationTTLSeconds = int64(15)
)

type realAgentScenario struct {
	name                string
	workRunID           string
	outcome             string
	routing             workrun.ImplementationRouteInput
	expectedRoute       workrun.ImplementationRoute
	actorTool           string
	actorMarker         string
	actorPrompt         string
	commonReview        bool
	killSwitchAtAdvance bool
	proveTerminalReplay bool
}

func TestRealOpenCodeOrganicRuntimeJourneys(t *testing.T) {
	if os.Getenv(realAgentE2EEnvironment) != "1" {
		t.Skip("set GENTLE_AI_REAL_AGENT_E2E=1 to run the pinned real-agent journeys")
	}
	requireExecutableVersion(t, "opencode", pinnedOpenCodeVersion)
	requireExecutable(t, "node")
	moduleRoot := organicModuleRoot(t)
	binary := organicTestBinary(t, moduleRoot)
	orchestrator, err := os.ReadFile(
		filepath.Join(moduleRoot, "internal", "assets", "opencode", "sdd-orchestrator.md"),
	)
	if err != nil {
		t.Fatal(err)
	}
	sharedCache := t.TempDir()
	sharedConfig := prepareOpenCodeConfig(t)

	// Evidence replacement map:
	//   marker-only actor + post-actor work-status -> committed candidate +
	//     terminal work-advance + real remote effect;
	//   unchanged WorkRun revision after actor -> exact START CAS consumed by
	//     work-advance and a new terminal revision;
	//   simulated delivery marker -> bare-repository update-ref CAS, exact tree
	//     and blob proof, expiry-stable Ready, and lost-response replay.
	// The route/no-SDD status checks and real delegated/common task actors remain
	// because they prove distinct safety behavior rather than delivery success.
	scenarios := []realAgentScenario{
		{
			name:      "direct inline implementation",
			workRunID: "organic-e2e-direct",
			outcome:   "Apply one already-understood mechanical file change.",
			routing: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
			expectedRoute:       workrun.ImplementationRouteDirectInline,
			actorTool:           "bash",
			actorMarker:         "DIRECT_IMPLEMENTATION_COMMITTED",
			proveTerminalReplay: true,
		},
		{
			name:      "delegated direct implementation",
			workRunID: "organic-e2e-delegated",
			outcome:   "Understand four files and implement the bounded outcome.",
			routing: workrun.ImplementationRouteInput{
				ReadIntent:     workrun.ReadIntentExploreUnderstand,
				ReadFileCount:  4,
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
			expectedRoute: workrun.ImplementationRouteDelegatedDirect,
			actorTool:     "task",
			actorMarker:   "DELEGATED_IMPLEMENTATION_COMMITTED",
			actorPrompt: "Act as the delegated-direct implementation worker. " +
				"Read the exact managed WorkRun status, confirm its route is " +
				"delegated_direct with no SDD run, implement the exact admitted " +
				"documentation scope, explicitly commit it, then return exactly " +
				"DELEGATED_IMPLEMENTATION_COMMITTED.",
		},
		{
			name:      "direct route with common review actor",
			workRunID: "organic-e2e-common-review",
			outcome:   "Apply one mechanical change and run the common review actor.",
			routing: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
			expectedRoute: workrun.ImplementationRouteDirectInline,
			actorTool:     "bash",
			actorMarker:   "COMMON_REVIEW_OK",
			commonReview:  true,
			actorPrompt: "Act as the common native review worker after direct " +
				"implementation. Read the exact managed WorkRun status, confirm " +
				"the route remains direct_inline with no SDD run, then return " +
				"exactly COMMON_REVIEW_OK.",
		},
		{
			name:      "managed start kill switch before advance",
			workRunID: "organic-e2e-kill-switch",
			outcome:   "Apply one mechanical file change, then stop if the owner runtime is disabled.",
			routing: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
			expectedRoute:       workrun.ImplementationRouteDirectInline,
			actorTool:           "bash",
			actorMarker:         "KILL_SWITCH_CANDIDATE_COMMITTED",
			killSwitchAtAdvance: true,
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			runRealAgentScenario(
				t,
				moduleRoot,
				binary,
				string(orchestrator),
				sharedCache,
				sharedConfig,
				scenario,
			)
		})
	}
}

func runRealAgentScenario(
	t *testing.T,
	moduleRoot string,
	binary string,
	orchestrator string,
	sharedCache string,
	sharedConfig string,
	scenario realAgentScenario,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(
		context.Background(),
		organicAgentTimeout,
	)
	defer cancel()

	repository := initOrganicRepository(t)
	repo := repository.worktree
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	repositoryRef := lease.Identity().RepositoryRef
	baseRevision := "git:" + repository.baseRevision

	authorizationTTL := int64(3600)
	if scenario.proveTerminalReplay {
		authorizationTTL = organicShortAuthorizationTTLSeconds
	}
	policies := organicRoutePolicies(t, 1, authorizationTTL)
	snapshot, err := workprovider.NewProductivePolicySnapshot(
		repositoryRef,
		model.AgentOpenCode,
		runtimeSessionRef,
		1,
		policies,
	)
	if err != nil {
		t.Fatal(err)
	}
	intake := workprovider.OwnerOutcomeIntake{
		WorkRunID:      scenario.workRunID,
		Nonce:          "nonce:" + scenario.workRunID,
		Route:          deliveryadmission.RouteDirectMain,
		ScopeSelectors: []string{"docs/passive-note.md"},
		Destination: workprovider.OwnerDestinationInput{
			TargetRef:        "refs/heads/main",
			ObservedRevision: baseRevision,
			DefaultBranch:    true,
		},
		PrimaryAuthority: workprovider.OwnerAuthoritySignalInput{
			SignalID:   "signal:" + scenario.workRunID,
			IssuerRef:  "maintainer:organic-e2e",
			Provenance: deliveryadmission.ProvenanceMaintainerControl,
			ExpiresAt:  time.Now().UTC().Add(time.Hour).Unix(),
		},
		RoutingFacts: scenario.routing,
	}

	runtimeServer := newOrganicRuntimeServer(
		t,
		repositoryRef,
		repository,
		snapshot,
		intake,
		scenario,
	)
	defer runtimeServer.Close()
	caFile := writeOrganicServerCA(t, runtimeServer)
	tokenFile := filepath.Join(t.TempDir(), "runtime.token")
	if err := os.WriteFile(tokenFile, []byte(runtimeBearerToken), 0o600); err != nil {
		t.Fatal(err)
	}

	modelServer := newOpenCodeFixtureServer(t, scenario, orchestrator)
	defer modelServer.Close()
	config := organicOpenCodeConfig(t, modelServer.URL, orchestrator)
	home := t.TempDir()
	startRevisionFile := filepath.Join(home, "start-revision")
	for _, path := range []string{
		filepath.Join(home, "data"),
		filepath.Join(home, "state"),
		filepath.Join(home, "opencode"),
		sharedCache,
		sharedConfig,
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	command := organicCommandContext(
		ctx,
		"opencode",
		"run",
		"--pure",
		"--format",
		"json",
		"--agent",
		"organic",
		"--model",
		"fixture/fixture",
		"--dir",
		repo,
		scenario.outcome,
	)
	command.Dir = repo
	environment := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+sharedConfig,
		"XDG_CACHE_HOME="+sharedCache,
		"XDG_DATA_HOME="+filepath.Join(home, "data"),
		"XDG_STATE_HOME="+filepath.Join(home, "state"),
		"OPENCODE_CONFIG_DIR="+filepath.Join(sharedConfig, "opencode"),
		"OPENCODE_TEST_HOME="+filepath.Join(home, "opencode"),
		"OPENCODE_CONFIG_CONTENT="+config,
		"OPENCODE_AUTH_CONTENT={}",
		"OPENCODE_DISABLE_PROJECT_CONFIG=1",
		"OPENCODE_DISABLE_AUTOUPDATE=1",
		"OPENCODE_DISABLE_AUTOCOMPACT=1",
		"OPENCODE_DISABLE_CLAUDE_CODE=1",
		"OPENCODE_DISABLE_DEFAULT_PLUGINS=1",
		"OPENCODE_DISABLE_EXTERNAL_SKILLS=1",
		"OPENCODE_DISABLE_LSP_DOWNLOAD=1",
		"OPENCODE_DISABLE_MODELS_FETCH=1",
		"OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER=1",
		"OPENCODE_FAST_BOOT=1",
		"OPENCODE_PURE=1",
		workprovider.WorkRoutingModeEnvironment+"="+
			string(workprovider.ActivationEnabled),
		workprovider.ProductiveRuntimeURLEnvironment+"="+runtimeServer.URL,
		workprovider.ProductiveRuntimeTokenFileEnvironment+"="+tokenFile,
		workprovider.ProductiveRuntimeCAFileEnvironment+"="+caFile,
		workprovider.ProductiveRuntimeAgentEnvironment+"="+string(model.AgentOpenCode),
		testBinaryEnvironment+"="+binary,
		"ORGANIC_E2E_REPO="+repo,
		"ORGANIC_E2E_OUTCOME="+scenario.outcome,
		"ORGANIC_E2E_WORK_RUN_ID="+scenario.workRunID,
		"ORGANIC_E2E_START_REVISION_FILE="+startRevisionFile,
	)
	command.Env = environment
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf(
			"opencode run: %v\nstdout:\n%s\nstderr:\n%s",
			err,
			stdout.String(),
			stderr.String(),
		)
	}
	if stderr.Len() != 0 {
		t.Fatalf("opencode stderr:\n%s", stderr.String())
	}

	events := decodeOpenCodeEvents(t, stdout.Bytes())
	evidence := assertOrganicJourney(
		t,
		events,
		scenario,
		repositoryRef,
		runtimeSessionRef,
	)
	modelServer.assertComplete(t)
	assertOrganicNoSDD(t, repository)
	if scenario.killSwitchAtAdvance {
		assertOrganicKilledAdvance(t, repository, runtimeServer, evidence)
	} else {
		assertOrganicDeliveredCandidate(t, repository, runtimeServer, evidence)
	}
	if scenario.proveTerminalReplay {
		assertOrganicTerminalReplay(
			t,
			binary,
			repo,
			environment,
			runtimeServer,
			evidence,
		)
	}
	runtimeServer.assertCalls(t)
	assertOrganicOnlyMainRef(t, repository.bare)
}

type organicRuntimeServer struct {
	*httptest.Server
	mu                 sync.Mutex
	repositoryRef      string
	repository         organicRepository
	snapshot           workprovider.ProductivePolicySnapshot
	intake             workprovider.OwnerOutcomeIntake
	scenario           realAgentScenario
	operations         []workprovider.ProductiveRuntimeOperation
	bootstraps         int
	branchCASCalls     int
	branchCASEffects   int
	executionExpiresAt int64
	candidateRevision  string
	failure            string
}

type organicPADGitBindingRequest struct {
	Candidate   deliveryadmission.CandidateBinding   `json:"candidate"`
	Destination deliveryadmission.DestinationBinding `json:"destination"`
	Mechanism   deliveryadmission.Mechanism          `json:"mechanism"`
}

func newOrganicRuntimeServer(
	t *testing.T,
	repositoryRef string,
	repository organicRepository,
	snapshot workprovider.ProductivePolicySnapshot,
	intake workprovider.OwnerOutcomeIntake,
	scenario realAgentScenario,
) *organicRuntimeServer {
	t.Helper()
	fixture := &organicRuntimeServer{
		repositoryRef: repositoryRef,
		repository:    repository,
		snapshot:      snapshot,
		intake:        intake,
		scenario:      scenario,
	}
	fixture.Server = httptest.NewTLSServer(http.HandlerFunc(fixture.serveHTTP))
	return fixture
}

func (fixture *organicRuntimeServer) serveHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost ||
		request.Header.Get("Authorization") != "Bearer "+runtimeBearerToken {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	switch request.URL.Path {
	case workprovider.ProductiveRuntimeBootstrapPathV1:
		var bootstrap workprovider.ProductiveRuntimeBootstrapRequest
		if err := decodeExactJSON(request.Body, &bootstrap); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if bootstrap.RepositoryRef != fixture.repositoryRef ||
			bootstrap.AgentID != model.AgentOpenCode {
			http.Error(writer, "unexpected bootstrap identity", http.StatusBadRequest)
			return
		}
		response, err := workprovider.NewProductiveRuntimeBootstrapResponse(
			bootstrap,
			runtimeSessionRef,
		)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		fixture.bootstraps++
		fixture.mu.Unlock()
		writeExactJSON(writer, response)
	case workprovider.ProductiveRuntimeCallPathV1:
		var call workprovider.ProductiveRuntimeRequest
		if err := decodeExactJSON(request.Body, &call); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		if err := call.Validate(); err != nil ||
			call.RepositoryRef != fixture.repositoryRef ||
			call.AgentID != model.AgentOpenCode ||
			call.ConnectorSessionRef != runtimeSessionRef {
			http.Error(writer, "invalid call binding", http.StatusBadRequest)
			return
		}
		var payload any
		switch call.Operation {
		case workprovider.ProductiveRuntimeOperationPolicySnapshot:
			if string(bytes.TrimSpace(call.Payload)) != "{}" {
				http.Error(writer, "unexpected policy payload", http.StatusBadRequest)
				return
			}
			payload = fixture.snapshot
		case workprovider.ProductiveRuntimeOperationOutcomeIntake:
			var intakeRequest struct {
				Context workprovider.OwnerOutcomeContext `json:"context"`
				Request workprovider.OutcomeStartRequest `json:"request"`
			}
			if err := decodeExactJSON(
				bytes.NewReader(call.Payload),
				&intakeRequest,
			); err != nil ||
				intakeRequest.Context.RepositoryRef != fixture.repositoryRef ||
				!sameOrganicDirectory(
					intakeRequest.Context.RepositoryRoot,
					fixture.repository.worktree,
				) ||
				intakeRequest.Request.Outcome != fixture.scenario.outcome ||
				intakeRequest.Request.ExplicitSDDRequested {
				http.Error(writer, "unexpected outcome intake payload", http.StatusBadRequest)
				return
			}
			payload = fixture.intake
		case workprovider.ProductiveRuntimeOperationPADGitBinding:
			var bindingRequest organicPADGitBindingRequest
			if err := decodeExactJSON(
				bytes.NewReader(call.Payload),
				&bindingRequest,
			); err != nil {
				fixture.reject(
					writer,
					"decode PAD Git binding request: %v",
					err,
				)
				return
			}
			binding, err := fixture.resolvePADGitBinding(bindingRequest)
			if err != nil {
				fixture.reject(writer, "resolve PAD Git binding: %v", err)
				return
			}
			payload = binding
		case workprovider.ProductiveRuntimeOperationObserveDelivery:
			var observationRequest workprovider.HostingObservationRequest
			if err := decodeExactJSON(
				bytes.NewReader(call.Payload),
				&observationRequest,
			); err != nil {
				fixture.reject(
					writer,
					"decode delivery observation request: %v",
					err,
				)
				return
			}
			observation, err := fixture.observeDelivery(observationRequest)
			if err != nil {
				fixture.reject(writer, "observe delivery: %v", err)
				return
			}
			payload = observation
		case workprovider.ProductiveRuntimeOperationBranchCAS:
			var casRequest workprovider.HostingBranchCASRequest
			if err := decodeExactJSON(
				bytes.NewReader(call.Payload),
				&casRequest,
			); err != nil {
				fixture.reject(writer, "decode branch CAS request: %v", err)
				return
			}
			receipt, err := fixture.compareAndSwapBranch(
				request.Context(),
				casRequest,
			)
			if err != nil {
				fixture.reject(writer, "branch CAS: %v", err)
				return
			}
			payload = receipt
		default:
			http.Error(writer, "unexpected operation", http.StatusBadRequest)
			return
		}
		response, err := workprovider.NewProductiveRuntimeResponse(call, payload)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		fixture.operations = append(fixture.operations, call.Operation)
		fixture.mu.Unlock()
		writeExactJSON(writer, response)
	default:
		http.NotFound(writer, request)
	}
}

func (fixture *organicRuntimeServer) resolvePADGitBinding(
	request organicPADGitBindingRequest,
) (workprovider.PADGitBinding, error) {
	if request.Destination.RepositoryRef != fixture.repositoryRef ||
		request.Destination.TargetRef != "refs/heads/main" ||
		request.Destination.ObservedRevision !=
			"git:"+fixture.repository.baseRevision ||
		request.Mechanism != deliveryadmission.MechanismFastForwardOnly {
		return workprovider.PADGitBinding{}, errors.New(
			"PAD Git binding request escaped the admitted direct-main destination",
		)
	}
	candidate, err := organicGitOutput(
		context.Background(),
		fixture.repository.worktree,
		"rev-parse",
		"HEAD",
	)
	if err != nil {
		return workprovider.PADGitBinding{}, err
	}
	if _, err := organicBareGitOutput(
		context.Background(),
		fixture.repository.bare,
		"cat-file",
		"-e",
		candidate+"^{commit}",
	); err != nil {
		return workprovider.PADGitBinding{}, fmt.Errorf(
			"resolve candidate through pre-existing owner object authority: %w",
			err,
		)
	}
	remote, err := organicBareGitOutput(
		context.Background(),
		fixture.repository.bare,
		"rev-parse",
		"refs/heads/main",
	)
	if err != nil {
		return workprovider.PADGitBinding{}, err
	}
	binding := workprovider.PADGitBinding{
		Schema:                 workprovider.PADGitBindingSchema,
		Candidate:              request.Candidate,
		Destination:            request.Destination,
		Mechanism:              request.Mechanism,
		HostingRepositoryRef:   "bare:organic-runtime-e2e",
		CandidateRevision:      "git:" + candidate,
		ExpectedRemoteRevision: "git:" + remote,
	}
	if binding.ExpectedRemoteRevision != request.Destination.ObservedRevision ||
		binding.CandidateRevision == binding.ExpectedRemoteRevision {
		return workprovider.PADGitBinding{}, errors.New(
			"PAD Git binding did not bind one new committed candidate",
		)
	}
	if err := binding.Validate(
		request.Candidate,
		request.Destination,
		request.Mechanism,
	); err != nil {
		return workprovider.PADGitBinding{}, err
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.candidateRevision != "" &&
		fixture.candidateRevision != binding.CandidateRevision {
		return workprovider.PADGitBinding{}, errors.New(
			"candidate revision changed across owner binding calls",
		)
	}
	fixture.candidateRevision = binding.CandidateRevision
	return binding, nil
}

func (fixture *organicRuntimeServer) observeDelivery(
	request workprovider.HostingObservationRequest,
) (workprovider.HostingDeliveryObservation, error) {
	if err := request.Validate(); err != nil {
		return workprovider.HostingDeliveryObservation{}, err
	}
	if request.Mechanism != deliveryadmission.MechanismFastForwardOnly ||
		request.Binding.Destination.RepositoryRef != fixture.repositoryRef {
		return workprovider.HostingDeliveryObservation{}, errors.New(
			"delivery observation escaped direct-main",
		)
	}
	remote, err := organicBareGitOutput(
		context.Background(),
		fixture.repository.bare,
		"rev-parse",
		"refs/heads/main",
	)
	if err != nil {
		return workprovider.HostingDeliveryObservation{}, err
	}
	observation := workprovider.HostingDeliveryObservation{
		Schema:                 workprovider.HostingDeliveryObservationSchema,
		Request:                request,
		CandidateRevision:      request.Binding.CandidateRevision,
		ExpectedRemoteRevision: request.Binding.ExpectedRemoteRevision,
		RemoteIdentity:         request.Binding.ExpectedRemoteRevision,
		RemoteRevision:         "git:" + remote,
		ProtectionState:        workprovider.HostingProtectionPermitted,
		ProtectionRevision:     "protection:organic-runtime-e2e:1",
		PullRequestState:       workprovider.HostingPullRequestNotApplicable,
		RequiredChecksState:    workprovider.HostingChecksNotApplicable,
	}
	if err := observation.Validate(request); err != nil {
		return workprovider.HostingDeliveryObservation{}, err
	}
	return observation, nil
}

func (fixture *organicRuntimeServer) compareAndSwapBranch(
	ctx context.Context,
	request workprovider.HostingBranchCASRequest,
) (workprovider.HostingBranchCASReceipt, error) {
	if err := request.Validate(); err != nil {
		return workprovider.HostingBranchCASReceipt{}, err
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.branchCASCalls++
	if request.RepositoryRef != fixture.repositoryRef ||
		request.HostingRepositoryRef != "bare:organic-runtime-e2e" ||
		request.TargetRef != "refs/heads/main" ||
		request.ExpectedRevision != "git:"+fixture.repository.baseRevision ||
		request.CandidateRevision != fixture.candidateRevision {
		return workprovider.HostingBranchCASReceipt{}, errors.New(
			"branch CAS request escaped its exact owner binding",
		)
	}
	if time.Now().UTC().Unix() >= request.ExecutionExpiresAt {
		return workprovider.HostingBranchCASReceipt{}, errors.New(
			"branch CAS arrived after execution expiry",
		)
	}
	if fixture.executionExpiresAt != 0 &&
		fixture.executionExpiresAt != request.ExecutionExpiresAt {
		return workprovider.HostingBranchCASReceipt{}, errors.New(
			"branch CAS execution expiry changed",
		)
	}
	if err := requireOrganicOnlyMainRef(ctx, fixture.repository.bare); err != nil {
		return workprovider.HostingBranchCASReceipt{}, err
	}
	fixture.executionExpiresAt = request.ExecutionExpiresAt
	if _, err := organicBareGitOutput(
		ctx,
		fixture.repository.bare,
		"update-ref",
		request.TargetRef,
		strings.TrimPrefix(request.CandidateRevision, "git:"),
		strings.TrimPrefix(request.ExpectedRevision, "git:"),
	); err != nil {
		return workprovider.HostingBranchCASReceipt{}, err
	}
	remote, err := organicBareGitOutput(
		ctx,
		fixture.repository.bare,
		"rev-parse",
		request.TargetRef,
	)
	if err != nil {
		return workprovider.HostingBranchCASReceipt{}, err
	}
	if err := requireOrganicOnlyMainRef(ctx, fixture.repository.bare); err != nil {
		return workprovider.HostingBranchCASReceipt{}, err
	}
	fixture.branchCASEffects++
	receipt := workprovider.HostingBranchCASReceipt{
		Schema:           workprovider.HostingBranchCASReceiptSchema,
		Request:          request,
		Outcome:          workprovider.HostingEffectApplied,
		RemoteRevision:   "git:" + remote,
		DeliveryRef:      request.CandidateRevision,
		EvidenceRevision: "evidence:organic-runtime-e2e:branch-cas:1",
	}
	if err := receipt.Validate(request); err != nil {
		return workprovider.HostingBranchCASReceipt{}, err
	}
	return receipt, nil
}

func (fixture *organicRuntimeServer) reject(
	writer http.ResponseWriter,
	format string,
	arguments ...any,
) {
	fixture.mu.Lock()
	fixture.failure = fmt.Sprintf(format, arguments...)
	fixture.mu.Unlock()
	http.Error(writer, "runtime fixture failure", http.StatusBadRequest)
}

func (fixture *organicRuntimeServer) assertCalls(t *testing.T) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.failure != "" {
		t.Fatal(fixture.failure)
	}
	wantBootstraps := 3
	if !fixture.scenario.killSwitchAtAdvance {
		wantBootstraps++
	}
	if fixture.scenario.actorTool == "task" ||
		fixture.scenario.commonReview {
		wantBootstraps++
	}
	if fixture.scenario.proveTerminalReplay {
		wantBootstraps += 2
	}
	if fixture.bootstraps != wantBootstraps {
		t.Fatalf(
			"runtime bootstraps = %d, want %d",
			fixture.bootstraps,
			wantBootstraps,
		)
	}
	wantOperations := []workprovider.ProductiveRuntimeOperation{
		workprovider.ProductiveRuntimeOperationPolicySnapshot,
		workprovider.ProductiveRuntimeOperationOutcomeIntake,
	}
	if !fixture.scenario.killSwitchAtAdvance {
		wantOperations = append(
			wantOperations,
			workprovider.ProductiveRuntimeOperationPolicySnapshot,
			workprovider.ProductiveRuntimeOperationPADGitBinding,
			workprovider.ProductiveRuntimeOperationObserveDelivery,
			workprovider.ProductiveRuntimeOperationObserveDelivery,
			workprovider.ProductiveRuntimeOperationObserveDelivery,
			workprovider.ProductiveRuntimeOperationBranchCAS,
		)
	}
	if fixture.scenario.proveTerminalReplay {
		wantOperations = append(
			wantOperations,
			workprovider.ProductiveRuntimeOperationPolicySnapshot,
		)
	}
	if !equalOrganicOperations(fixture.operations, wantOperations) {
		t.Fatalf(
			"runtime operations = %#v, want %#v",
			fixture.operations,
			wantOperations,
		)
	}
	wantCAS := 1
	if fixture.scenario.killSwitchAtAdvance {
		wantCAS = 0
	}
	if fixture.branchCASCalls != wantCAS ||
		fixture.branchCASEffects != wantCAS {
		t.Fatalf(
			"branch CAS calls/effects = %d/%d, want %d/%d",
			fixture.branchCASCalls,
			fixture.branchCASEffects,
			wantCAS,
			wantCAS,
		)
	}
}

func equalOrganicOperations(
	left []workprovider.ProductiveRuntimeOperation,
	right []workprovider.ProductiveRuntimeOperation,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type openCodeFixtureServer struct {
	*httptest.Server
	mu               sync.Mutex
	scenario         realAgentScenario
	requiredPrompt   string
	mainCalls        int
	subagentStarts   int
	subagentChecks   int
	subagentCommits  int
	issuedActorTools int
	failure          string
}

type openAIRequest struct {
	Messages []openAIMessage   `json:"messages"`
	Tools    []json.RawMessage `json:"tools"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func newOpenCodeFixtureServer(
	t *testing.T,
	scenario realAgentScenario,
	requiredPrompt string,
) *openCodeFixtureServer {
	t.Helper()
	fixture := &openCodeFixtureServer{
		scenario:       scenario,
		requiredPrompt: requiredPrompt,
	}
	fixture.Server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	return fixture
}

func (fixture *openCodeFixtureServer) serveHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method", http.StatusMethodNotAllowed)
		return
	}
	var input openAIRequest
	if err := json.NewDecoder(
		io.LimitReader(request.Body, 4<<20),
	).Decode(&input); err != nil {
		fixture.fail(writer, "decode model request: %v", err)
		return
	}
	if len(input.Tools) == 0 {
		fixture.writeText(writer, "Organic runtime journey", "stop")
		return
	}
	if len(input.Messages) == 0 {
		fixture.fail(writer, "model request has no messages")
		return
	}
	last := input.Messages[len(input.Messages)-1]
	system := ""
	hasActorPrompt := false
	for _, message := range input.Messages {
		if message.Role == "system" {
			system += messageText(message.Content)
		}
		if message.Role == "user" &&
			strings.Contains(
				messageText(message.Content),
				fixture.scenario.actorPrompt,
			) {
			hasActorPrompt = true
		}
	}
	isMain := strings.Contains(system, fixture.requiredPrompt)
	lastText := messageText(last.Content)
	if !isMain && hasActorPrompt {
		switch last.Role {
		case "user":
			if !strings.Contains(lastText, fixture.scenario.actorPrompt) {
				fixture.fail(
					writer,
					"subagent prompt does not contain %q: %q",
					fixture.scenario.actorPrompt,
					lastText,
				)
				return
			}
			fixture.mu.Lock()
			fixture.subagentStarts++
			fixture.mu.Unlock()
			fixture.writeTool(
				writer,
				"actor-status",
				"bash",
				organicStatusCommand(),
			)
			return
		case "tool":
			if strings.Contains(lastText, fixture.scenario.actorMarker) {
				if fixture.scenario.actorTool != "task" {
					fixture.fail(
						writer,
						"only delegated implementation may commit inside the task actor",
					)
					return
				}
				fixture.mu.Lock()
				fixture.subagentCommits++
				fixture.mu.Unlock()
				fixture.writeText(
					writer,
					fixture.scenario.actorMarker,
					"stop",
				)
				return
			}
			if !strings.Contains(lastText, fixture.scenario.workRunID) ||
				!strings.Contains(lastText, string(fixture.scenario.expectedRoute)) ||
				strings.Contains(lastText, "sddRunRef") {
				fixture.fail(
					writer,
					"subagent status evidence does not prove route/no-SDD: %s",
					lastText,
				)
				return
			}
			fixture.mu.Lock()
			fixture.subagentChecks++
			fixture.mu.Unlock()
			if fixture.scenario.actorTool == "task" {
				fixture.writeTool(
					writer,
					"delegated-implementation",
					"bash",
					organicActorCommand(
						fixture.scenario.actorMarker,
					),
				)
				return
			}
			fixture.writeText(writer, fixture.scenario.actorMarker, "stop")
			return
		default:
			fixture.fail(
				writer,
				"unexpected subagent terminal role %q",
				last.Role,
			)
			return
		}
	}
	if !isMain {
		fixture.fail(writer, "production organic orchestrator contract is absent")
		return
	}
	fixture.mu.Lock()
	fixture.mainCalls++
	call := fixture.mainCalls
	fixture.mu.Unlock()
	switch call {
	case 1:
		fixture.writeTool(writer, "capabilities", "bash", organicCapabilityCommand())
	case 2:
		fixture.writeTool(writer, "start", "bash", organicStartCommand())
	case 3:
		fixture.writeTool(writer, "status", "bash", organicStatusCommand())
	case 4:
		fixture.mu.Lock()
		fixture.issuedActorTools++
		fixture.mu.Unlock()
		if fixture.scenario.actorTool == "task" {
			fixture.writeTool(
				writer,
				"actor",
				"task",
				map[string]any{
					"description":   "Run organic actor",
					"prompt":        fixture.scenario.actorPrompt,
					"subagent_type": "general",
				},
			)
			return
		}
		fixture.writeTool(
			writer,
			"actor",
			"bash",
			organicActorCommand(fixture.scenario.actorMarker),
		)
	case 5:
		if fixture.scenario.commonReview {
			if !strings.Contains(lastText, fixture.scenario.actorMarker) {
				fixture.fail(
					writer,
					"common review was requested before committed implementation evidence: %s",
					lastText,
				)
				return
			}
			fixture.mu.Lock()
			fixture.issuedActorTools++
			fixture.mu.Unlock()
			fixture.writeTool(
				writer,
				"common-review",
				"task",
				map[string]any{
					"description":   "Run common native review",
					"prompt":        fixture.scenario.actorPrompt,
					"subagent_type": "general",
				},
			)
			return
		}
		if !strings.Contains(lastText, fixture.scenario.actorMarker) {
			fixture.fail(
				writer,
				"work-advance was requested before actor commit evidence: %s",
				lastText,
			)
			return
		}
		fixture.writeTool(
			writer,
			"advance",
			"bash",
			organicAdvanceCommand(fixture.scenario.killSwitchAtAdvance),
		)
	case 6:
		if fixture.scenario.commonReview {
			if !strings.Contains(lastText, fixture.scenario.actorMarker) {
				fixture.fail(
					writer,
					"work-advance was requested before common review evidence: %s",
					lastText,
				)
				return
			}
			fixture.writeTool(
				writer,
				"advance",
				"bash",
				organicAdvanceCommand(false),
			)
			return
		}
		fixture.writeText(writer, "Organic journey complete.", "stop")
	case 7:
		if !fixture.scenario.commonReview {
			fixture.fail(writer, "unexpected seventh main model call")
			return
		}
		fixture.writeText(writer, "Organic journey complete.", "stop")
	default:
		fixture.fail(writer, "unexpected main model call %d", call)
	}
}

func (fixture *openCodeFixtureServer) fail(
	writer http.ResponseWriter,
	format string,
	arguments ...any,
) {
	fixture.mu.Lock()
	fixture.failure = fmt.Sprintf(format, arguments...)
	fixture.mu.Unlock()
	http.Error(writer, "fixture failure", http.StatusInternalServerError)
}

func (fixture *openCodeFixtureServer) writeTool(
	writer http.ResponseWriter,
	id string,
	name string,
	arguments any,
) {
	encoded, _ := json.Marshal(arguments)
	fixture.writeChunks(writer, []any{
		map[string]any{
			"id":      "chat",
			"object":  "chat.completion.chunk",
			"created": 0,
			"model":   "fixture",
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"index": 0,
						"id":    "call_" + id,
						"type":  "function",
						"function": map[string]any{
							"name":      name,
							"arguments": string(encoded),
						},
					}},
				},
				"finish_reason": nil,
			}},
		},
		organicFinishChunk("tool_calls"),
	})
}

func (fixture *openCodeFixtureServer) writeText(
	writer http.ResponseWriter,
	content string,
	reason string,
) {
	fixture.writeChunks(writer, []any{
		map[string]any{
			"id":      "chat",
			"object":  "chat.completion.chunk",
			"created": 0,
			"model":   "fixture",
			"choices": []any{map[string]any{
				"index": 0,
				"delta": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": nil,
			}},
		},
		organicFinishChunk(reason),
	})
}

func organicFinishChunk(reason string) map[string]any {
	return map[string]any{
		"id":      "chat",
		"object":  "chat.completion.chunk",
		"created": 0,
		"model":   "fixture",
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": reason,
		}},
		"usage": map[string]any{
			"prompt_tokens":     1,
			"completion_tokens": 1,
			"total_tokens":      2,
		},
	}
}

func (fixture *openCodeFixtureServer) writeChunks(
	writer http.ResponseWriter,
	chunks []any,
) {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	for _, chunk := range chunks {
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(writer, "data: %s\n\n", encoded)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	_, _ = io.WriteString(writer, "data: [DONE]\n\n")
}

func (fixture *openCodeFixtureServer) assertComplete(t *testing.T) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.failure != "" {
		t.Fatal(fixture.failure)
	}
	wantMainCalls := 6
	wantActorTools := 1
	if fixture.scenario.commonReview {
		wantMainCalls = 7
		wantActorTools = 2
	}
	if fixture.mainCalls != wantMainCalls ||
		fixture.issuedActorTools != wantActorTools {
		t.Fatalf(
			"model calls/actor tools = %d/%d, want %d/%d",
			fixture.mainCalls,
			fixture.issuedActorTools,
			wantMainCalls,
			wantActorTools,
		)
	}
	wantSubagents := 0
	if fixture.scenario.actorTool == "task" ||
		fixture.scenario.commonReview {
		wantSubagents = 1
	}
	if fixture.subagentStarts != wantSubagents {
		t.Fatalf(
			"subagent starts = %d, want %d",
			fixture.subagentStarts,
			wantSubagents,
		)
	}
	if fixture.subagentChecks != wantSubagents {
		t.Fatalf(
			"subagent status checks = %d, want %d",
			fixture.subagentChecks,
			wantSubagents,
		)
	}
	wantSubagentCommits := 0
	if fixture.scenario.actorTool == "task" {
		wantSubagentCommits = 1
	}
	if fixture.subagentCommits != wantSubagentCommits {
		t.Fatalf(
			"subagent commits = %d, want %d",
			fixture.subagentCommits,
			wantSubagentCommits,
		)
	}
}

type openCodeEvent struct {
	Type string `json:"type"`
	Part struct {
		Tool  string `json:"tool"`
		State struct {
			Status string `json:"status"`
			Input  struct {
				Command string `json:"command"`
			} `json:"input"`
			Output string `json:"output"`
		} `json:"state"`
	} `json:"part"`
}

func decodeOpenCodeEvents(t *testing.T, payload []byte) []openCodeEvent {
	t.Helper()
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	var events []openCodeEvent
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event openCodeEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode OpenCode JSONL %q: %v", line, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

type organicJourneyEvidence struct {
	start         workrun.WorkStatusV1
	beforeActor   workrun.WorkStatusV1
	advance       workrun.WorkAdvanceV1
	advanceOutput []byte
}

func assertOrganicJourney(
	t *testing.T,
	events []openCodeEvent,
	scenario realAgentScenario,
	repositoryRef string,
	sessionRef string,
) organicJourneyEvidence {
	t.Helper()
	var (
		capabilities *workprovider.RuntimeCapabilitiesV1
		start        *workrun.WorkStatusV1
		statuses     []workrun.WorkStatusV1
		advance      *workrun.WorkAdvanceV1
		advanceBytes []byte
		actorSeen    bool
		taskSeen     bool
	)
	for _, event := range events {
		if event.Type != "tool_use" {
			continue
		}
		if event.Part.State.Status != "completed" {
			t.Fatalf("tool %s state = %#v", event.Part.Tool, event.Part.State)
		}
		if event.Part.Tool == "task" {
			taskSeen = true
			if !strings.Contains(event.Part.State.Output, scenario.actorMarker) {
				t.Fatalf("task actor output = %q", event.Part.State.Output)
			}
			actorSeen = true
			continue
		}
		if strings.Contains(event.Part.State.Output, scenario.actorMarker) {
			actorSeen = true
			continue
		}
		var result struct {
			Status int    `json:"status"`
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		}
		if err := json.Unmarshal([]byte(event.Part.State.Output), &result); err != nil {
			t.Fatalf("decode command result %q: %v", event.Part.State.Output, err)
		}
		if result.Status != 0 || result.Stderr != "" {
			t.Fatalf("command result = %#v", result)
		}
		switch {
		case strings.Contains(event.Part.State.Input.Command, "work-capabilities"):
			var value workprovider.RuntimeCapabilitiesV1
			if err := json.Unmarshal([]byte(result.Stdout), &value); err != nil {
				t.Fatal(err)
			}
			capabilities = &value
		case strings.Contains(event.Part.State.Input.Command, "work-start"):
			var value workrun.WorkStatusV1
			if err := json.Unmarshal([]byte(result.Stdout), &value); err != nil {
				t.Fatal(err)
			}
			start = &value
		case strings.Contains(event.Part.State.Input.Command, "work-status"):
			var value workrun.WorkStatusV1
			if err := json.Unmarshal([]byte(result.Stdout), &value); err != nil {
				t.Fatal(err)
			}
			statuses = append(statuses, value)
		case strings.Contains(event.Part.State.Input.Command, "work-advance"):
			if advance != nil {
				t.Fatal("actor invoked work-advance more than once")
			}
			var value workrun.WorkAdvanceV1
			if err := json.Unmarshal([]byte(result.Stdout), &value); err != nil {
				t.Fatal(err)
			}
			advance = &value
			advanceBytes = []byte(result.Stdout)
		}
	}
	if capabilities == nil ||
		capabilities.RepositoryRef != repositoryRef ||
		capabilities.AgentID != model.AgentOpenCode ||
		capabilities.WorkRouting.Exposure != workprovider.WorkRoutingAdvertised ||
		capabilities.Contracts.Advance != workrun.WorkAdvanceContractV1 ||
		capabilities.ConnectorSessionRef != sessionRef {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	if len(statuses) != 1 {
		t.Fatalf(
			"main status observations = %d, want one pre-actor observation",
			len(statuses),
		)
	}
	values := []struct {
		name  string
		value *workrun.WorkStatusV1
	}{
		{name: "start", value: start},
		{name: "status-before-actor", value: &statuses[0]},
	}
	for _, observed := range values {
		name, value := observed.name, observed.value
		if value == nil ||
			value.WorkRunID != scenario.workRunID ||
			value.ImplementationRoute != scenario.expectedRoute ||
			value.SDDRunRef != "" {
			t.Fatalf("%s = %#v", name, value)
		}
		if err := value.Validate(); err != nil {
			t.Fatalf("%s validation = %v", name, err)
		}
	}
	if !actorSeen {
		t.Fatal("real agent did not execute the scenario actor")
	}
	wantTask := scenario.actorTool == "task" || scenario.commonReview
	if taskSeen != wantTask {
		t.Fatalf("task actor seen = %t, want %t", taskSeen, wantTask)
	}
	if start.Revision != statuses[0].Revision ||
		start.ImplementationRoute != statuses[0].ImplementationRoute {
		t.Fatalf(
			"pre-actor status changed route/revision: %#v -> %#v",
			start,
			statuses[0],
		)
	}
	if advance == nil {
		t.Fatal("real agent did not invoke the required post-actor work-advance")
	}
	if err := advance.Validate(); err != nil {
		t.Fatalf("work-advance validation = %v", err)
	}
	if advance.PreviousRevision != start.Revision ||
		advance.Status.WorkRunID != scenario.workRunID ||
		advance.Status.ImplementationRoute != scenario.expectedRoute ||
		advance.Status.SDDRunRef != "" {
		t.Fatalf("work-advance authority binding = %#v", advance)
	}
	if scenario.killSwitchAtAdvance {
		if advance.Status.PublicState != workrun.PublicStateNeedsYourDecision ||
			advance.Diagnostic == nil ||
			advance.Diagnostic.Code !=
				workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable ||
			advance.DeliveryResultRef != "" {
			t.Fatalf("kill-switch terminal result = %#v", advance)
		}
	} else if advance.Status.PublicState != workrun.PublicStateReady ||
		advance.DeliveryResultRef == "" ||
		advance.Diagnostic != nil {
		t.Fatalf(
			"productive terminal result = %#v / diagnostic %#v",
			advance,
			advance.Diagnostic,
		)
	}
	return organicJourneyEvidence{
		start:         *start,
		beforeActor:   statuses[0],
		advance:       *advance,
		advanceOutput: advanceBytes,
	}
}

func assertOrganicDeliveredCandidate(
	t *testing.T,
	repository organicRepository,
	runtimeServer *organicRuntimeServer,
	evidence organicJourneyEvidence,
) {
	t.Helper()
	candidate := organicGit(t, repository.worktree, "rev-parse", "HEAD")
	if candidate == repository.baseRevision {
		t.Fatal("actor marker did not produce a new committed candidate")
	}
	if dirty := organicGit(
		t,
		repository.worktree,
		"status",
		"--porcelain=v1",
		"--untracked-files=all",
	); dirty != "" {
		t.Fatalf("actor left an uncommitted worktree: %q", dirty)
	}
	remote, err := organicBareGitOutput(
		context.Background(),
		repository.bare,
		"rev-parse",
		"refs/heads/main",
	)
	if err != nil {
		t.Fatal(err)
	}
	if remote != candidate {
		t.Fatalf("remote main = %s, want committed candidate %s", remote, candidate)
	}
	if _, err := organicBareGitOutput(
		context.Background(),
		repository.bare,
		"merge-base",
		"--is-ancestor",
		repository.baseRevision,
		candidate,
	); err != nil {
		t.Fatalf("delivered candidate is not a base descendant: %v", err)
	}
	localTree := organicGit(
		t,
		repository.worktree,
		"rev-parse",
		candidate+"^{tree}",
	)
	remoteTree, err := organicBareGitOutput(
		context.Background(),
		repository.bare,
		"rev-parse",
		"refs/heads/main^{tree}",
	)
	if err != nil {
		t.Fatal(err)
	}
	if remoteTree != localTree {
		t.Fatalf("remote/local candidate trees = %s/%s", remoteTree, localTree)
	}
	const wantDocument = "" +
		"# Passive note\n\n" +
		"Organic runtime delivered this committed documentation change."
	document, err := organicBareGitOutput(
		context.Background(),
		repository.bare,
		"show",
		"refs/heads/main:docs/passive-note.md",
	)
	if err != nil {
		t.Fatal(err)
	}
	if document != wantDocument {
		t.Fatalf("delivered document = %q, want %q", document, wantDocument)
	}
	paths := organicGit(
		t,
		repository.worktree,
		"diff-tree",
		"--no-commit-id",
		"--name-only",
		"-r",
		repository.baseRevision,
		candidate,
	)
	if paths != "docs/passive-note.md" {
		t.Fatalf("committed candidate paths = %q", paths)
	}
	runtimeServer.mu.Lock()
	casCalls := runtimeServer.branchCASCalls
	casEffects := runtimeServer.branchCASEffects
	boundCandidate := runtimeServer.candidateRevision
	runtimeServer.mu.Unlock()
	if casCalls != 1 || casEffects != 1 ||
		boundCandidate != "git:"+candidate ||
		evidence.advance.Status.PublicState != workrun.PublicStateReady {
		t.Fatalf(
			"productive delivery evidence = calls %d effects %d candidate %q advance %#v",
			casCalls,
			casEffects,
			boundCandidate,
			evidence.advance,
		)
	}
}

func assertOrganicKilledAdvance(
	t *testing.T,
	repository organicRepository,
	runtimeServer *organicRuntimeServer,
	evidence organicJourneyEvidence,
) {
	t.Helper()
	candidate := organicGit(t, repository.worktree, "rev-parse", "HEAD")
	if candidate == repository.baseRevision {
		t.Fatal("kill-switch journey did not reach a committed actor candidate")
	}
	remote, err := organicBareGitOutput(
		context.Background(),
		repository.bare,
		"rev-parse",
		"refs/heads/main",
	)
	if err != nil {
		t.Fatal(err)
	}
	runtimeServer.mu.Lock()
	casCalls := runtimeServer.branchCASCalls
	casEffects := runtimeServer.branchCASEffects
	expiresAt := runtimeServer.executionExpiresAt
	runtimeServer.mu.Unlock()
	if remote != repository.baseRevision ||
		casCalls != 0 ||
		casEffects != 0 ||
		expiresAt != 0 ||
		evidence.advance.Status.PublicState !=
			workrun.PublicStateNeedsYourDecision ||
		evidence.advance.Diagnostic == nil ||
		evidence.advance.Diagnostic.Code !=
			workrun.WorkAdvanceDiagnosticProviderAuthorityUnavailable {
		t.Fatalf(
			"kill switch did not fail closed: remote %s base %s calls/effects %d/%d expiry %d advance %#v",
			remote,
			repository.baseRevision,
			casCalls,
			casEffects,
			expiresAt,
			evidence.advance,
		)
	}
}

func assertOrganicTerminalReplay(
	t *testing.T,
	binary string,
	repo string,
	environment []string,
	runtimeServer *organicRuntimeServer,
	evidence organicJourneyEvidence,
) {
	t.Helper()
	runtimeServer.mu.Lock()
	expiresAt := runtimeServer.executionExpiresAt
	runtimeServer.mu.Unlock()
	now := time.Now().UTC()
	expiry := time.Unix(expiresAt, 0).UTC()
	remaining := expiry.Sub(now)
	if expiresAt <= 0 ||
		remaining <= 0 ||
		remaining > time.Duration(
			organicShortAuthorizationTTLSeconds+2,
		)*time.Second {
		t.Fatalf(
			"execution expiry/remaining = %d/%s; short bound was not captured",
			expiresAt,
			remaining,
		)
	}
	timer := time.NewTimer(time.Until(expiry.Add(1100 * time.Millisecond)))
	defer timer.Stop()
	<-timer.C
	if time.Now().UTC().Unix() < expiresAt {
		t.Fatalf("terminal status check ran before execution expiry %d", expiresAt)
	}
	statusBytes := runOrganicBinary(
		t,
		binary,
		repo,
		environment,
		"work-status",
		"--cwd",
		repo,
		"--work-run",
		evidence.start.WorkRunID,
		"--contract",
		workrun.WorkStatusContractV1,
		"--json",
	)
	var status workrun.WorkStatusV1
	if err := json.Unmarshal(statusBytes, &status); err != nil {
		t.Fatal(err)
	}
	if err := status.Validate(); err != nil {
		t.Fatal(err)
	}
	if status.PublicState != workrun.PublicStateReady ||
		status.Revision != evidence.advance.Status.Revision ||
		status.WorkRunID != evidence.start.WorkRunID {
		t.Fatalf("Ready did not survive execution expiry: %#v", status)
	}
	replayBytes := runOrganicBinary(
		t,
		binary,
		repo,
		environment,
		"work-advance",
		"--cwd",
		repo,
		"--work-run",
		evidence.start.WorkRunID,
		"--expected-revision",
		evidence.start.Revision,
		"--contract",
		workrun.WorkAdvanceContractV1,
		"--json",
	)
	if !bytes.Equal(replayBytes, evidence.advanceOutput) {
		t.Fatalf(
			"same-CAS replay changed exact result bytes:\nfirst  %s\nreplay %s",
			evidence.advanceOutput,
			replayBytes,
		)
	}
	runtimeServer.mu.Lock()
	defer runtimeServer.mu.Unlock()
	if runtimeServer.branchCASCalls != 1 ||
		runtimeServer.branchCASEffects != 1 {
		t.Fatalf(
			"same-CAS replay repeated effect: calls/effects %d/%d",
			runtimeServer.branchCASCalls,
			runtimeServer.branchCASEffects,
		)
	}
}

func assertOrganicNoSDD(t *testing.T, repository organicRepository) {
	t.Helper()
	common := organicGit(
		t,
		repository.worktree,
		"rev-parse",
		"--git-common-dir",
	)
	if !filepath.IsAbs(common) {
		common = filepath.Join(repository.worktree, common)
	}
	root := filepath.Join(filepath.Clean(common), "gentle-ai")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if strings.HasPrefix(name, "sdd-") ||
			name == "sdd" ||
			name == "trace" ||
			name == "evaluation" {
			t.Fatalf(
				"direct implementation created forbidden SDD/trace/evaluation artifact %q",
				filepath.Join(root, entry.Name()),
			)
		}
	}
}

func runOrganicBinary(
	t *testing.T,
	binary string,
	repo string,
	environment []string,
	arguments ...string,
) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(
		context.Background(),
		organicLocalTimeout,
	)
	defer cancel()
	command := organicCommandContext(ctx, binary, arguments...)
	command.Dir = repo
	command.Env = environment
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf(
			"%s %v: %v\nstdout:\n%s\nstderr:\n%s",
			binary,
			arguments,
			err,
			stdout.String(),
			stderr.String(),
		)
	}
	if stderr.Len() != 0 {
		t.Fatalf("%s %v stderr:\n%s", binary, arguments, stderr.String())
	}
	return append([]byte(nil), stdout.Bytes()...)
}

func organicCapabilityCommand() map[string]any {
	return map[string]any{"command": organicNodeCommand(
		"['work-capabilities','--cwd',process.env.ORGANIC_E2E_REPO,"+
			"'--contract','gentle-ai.work-capabilities/v1','--json']",
		"",
	)}
}

func organicStartCommand() map[string]any {
	return map[string]any{"command": `node -e "const fs=require('fs');` +
		`const {spawnSync}=require('child_process');` +
		`const r=spawnSync(process.env.GENTLE_AI_TEST_BINARY,` +
		`['work-start','--cwd',process.env.ORGANIC_E2E_REPO,` +
		`'--contract','gentle-ai.work-start/v1','--json'],{` +
		`input:JSON.stringify({outcome:process.env.ORGANIC_E2E_OUTCOME,` +
		`explicitSddRequested:false}),encoding:'utf8',env:process.env,` +
		`timeout:30000});` +
		`const status=Number.isInteger(r.status)?r.status:1;` +
		`const stderr=r.stderr||(Number.isInteger(r.status)?'':` +
		`String(r.error||r.signal||'process did not exit'));` +
		`if(status===0){const v=JSON.parse(r.stdout);` +
		`fs.writeFileSync(process.env.ORGANIC_E2E_START_REVISION_FILE,` +
		`v.revision,{encoding:'utf8',mode:384});}` +
		`process.stdout.write(JSON.stringify({status,` +
		`stdout:r.stdout||'',stderr}));` +
		`process.exit(status===0?0:1)"`}
}

func organicStatusCommand() map[string]any {
	return map[string]any{"command": organicNodeCommand(
		"['work-status','--cwd',process.env.ORGANIC_E2E_REPO,"+
			"'--work-run',process.env.ORGANIC_E2E_WORK_RUN_ID,"+
			"'--contract','gentle-ai.work-status/v1','--json']",
		"",
	)}
}

func organicAdvanceCommand(killSwitch bool) map[string]any {
	environment := "process.env"
	if killSwitch {
		environment = `Object.assign({},process.env,{'` +
			workprovider.WorkRoutingModeEnvironment + `':'` +
			string(workprovider.ActivationDisabled) + `'})`
	}
	return map[string]any{"command": `node -e "const fs=require('fs');` +
		`const {spawnSync}=require('child_process');` +
		`const revision=fs.readFileSync(` +
		`process.env.ORGANIC_E2E_START_REVISION_FILE,'utf8');` +
		`const r=spawnSync(process.env.GENTLE_AI_TEST_BINARY,` +
		`['work-advance','--cwd',process.env.ORGANIC_E2E_REPO,` +
		`'--work-run',process.env.ORGANIC_E2E_WORK_RUN_ID,` +
		`'--expected-revision',revision,` +
		`'--contract','gentle-ai.work-advance/v1','--json'],{` +
		`encoding:'utf8',env:` + environment + `,timeout:120000});` +
		`const status=Number.isInteger(r.status)?r.status:1;` +
		`const stderr=r.stderr||(Number.isInteger(r.status)?'':` +
		`String(r.error||r.signal||'process did not exit'));` +
		`process.stdout.write(JSON.stringify({status,` +
		`stdout:r.stdout||'',stderr}));` +
		`process.exit(status===0?0:1)"`}
}

func organicActorCommand(marker string) map[string]any {
	return map[string]any{"command": `node -e "const fs=require('fs');` +
		`const {spawnSync}=require('child_process');` +
		`const repo=process.env.ORGANIC_E2E_REPO;` +
		`const run=(args)=>{const r=spawnSync('git',args,{cwd:repo,` +
		`encoding:'utf8',env:process.env,timeout:30000});` +
		`if(r.status!==0){` +
		`process.stderr.write(r.stderr||r.stdout||'git failed');` +
		`process.exit(r.status||1);}};` +
		`fs.mkdirSync(repo+'/docs',{recursive:true});` +
		`fs.writeFileSync(repo+'/docs/passive-note.md',` +
		`'# Passive note\\n\\nOrganic runtime delivered this committed documentation change.\\n');` +
		`run(['add','--','docs/passive-note.md']);` +
		`run(['commit','-m','docs: add passive organic runtime note']);` +
		`process.stdout.write('` + marker + `')"`}
}

func organicNodeCommand(arguments string, extraOptions string) string {
	return `node -e "const {spawnSync}=require('child_process');` +
		`const r=spawnSync(process.env.GENTLE_AI_TEST_BINARY,` +
		arguments + `,{` + extraOptions +
		`encoding:'utf8',env:process.env,timeout:30000});` +
		`const status=Number.isInteger(r.status)?r.status:1;` +
		`const stderr=r.stderr||(Number.isInteger(r.status)?'':` +
		`String(r.error||r.signal||'process did not exit'));` +
		`process.stdout.write(JSON.stringify({status,` +
		`stdout:r.stdout||'',stderr}));` +
		`process.exit(status===0?0:1)"`
}

func organicOpenCodeConfig(
	t *testing.T,
	serverURL string,
	orchestrator string,
) string {
	t.Helper()
	config := map[string]any{
		"provider": map[string]any{
			"fixture": map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "Organic E2E Fixture",
				"options": map[string]any{
					"baseURL": serverURL + "/v1",
					"apiKey":  "fixture",
				},
				"models": map[string]any{
					"fixture": map[string]any{"name": "Fixture"},
				},
			},
		},
		"agent": map[string]any{
			"organic": map[string]any{
				"description": "Organic runtime E2E",
				"mode":        "primary",
				"model":       "fixture/fixture",
				"prompt":      orchestrator,
				"permission": map[string]any{
					"bash": "allow",
					"task": "allow",
					"edit": "deny",
				},
			},
		},
		"plugin": []any{},
		"compaction": map[string]any{
			"auto": false,
		},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func organicRoutePolicies(
	t *testing.T,
	revision uint64,
	authorizationTTLSeconds int64,
) []deliveryadmission.RoutePolicy {
	t.Helper()
	routes := []deliveryadmission.Route{
		deliveryadmission.RoutePRWithIssue,
		deliveryadmission.RoutePRWithoutIssue,
		deliveryadmission.RouteDirectMain,
		deliveryadmission.RouteEmergency,
	}
	policies := make([]deliveryadmission.RoutePolicy, 0, len(routes))
	for _, route := range routes {
		policy, err := deliveryadmission.NewRoutePolicy(
			"policy:"+string(route),
			"snapshot:"+strconv.FormatUint(revision, 10),
			route,
			true,
			false,
			false,
			authorizationTTLSeconds,
			0,
		)
		if err != nil {
			t.Fatal(err)
		}
		policies = append(policies, policy)
	}
	return policies
}

type organicRepository struct {
	worktree     string
	bare         string
	baseRevision string
}

func TestSameOrganicDirectoryAcceptsCanonicalAliases(t *testing.T) {
	directory := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(directory, alias); err != nil {
		t.Skipf("directory aliases are unavailable: %v", err)
	}
	if !sameOrganicDirectory(directory, alias) {
		t.Fatal("same repository directory alias was rejected")
	}
	file := filepath.Join(directory, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if sameOrganicDirectory(directory, file) {
		t.Fatal("regular file was accepted as the repository directory")
	}
}

func sameOrganicDirectory(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil &&
		rightErr == nil &&
		leftInfo.IsDir() &&
		rightInfo.IsDir() &&
		os.SameFile(leftInfo, rightInfo)
}

func initOrganicRepository(t *testing.T) organicRepository {
	t.Helper()
	repo := t.TempDir()
	organicGit(t, repo, "init", "--initial-branch=main")
	organicGit(t, repo, "config", "user.name", "Organic E2E")
	organicGit(t, repo, "config", "user.email", "organic-e2e@example.invalid")
	if err := os.WriteFile(
		filepath.Join(repo, "tracked.txt"),
		[]byte("organic runtime\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	organicGit(t, repo, "add", "tracked.txt")
	organicGit(t, repo, "commit", "-m", "test: seed organic runtime")
	baseRevision := organicGit(t, repo, "rev-parse", "HEAD")
	bare := filepath.Join(t.TempDir(), "origin.git")
	organicGit(t, repo, "init", "--bare", "--quiet", bare)
	organicGit(t, repo, "remote", "add", "origin", bare)
	organicGit(
		t,
		repo,
		"push",
		"--quiet",
		"origin",
		"main:refs/heads/main",
	)
	configureOrganicBareObjectAuthority(t, repo, bare, baseRevision)
	assertOrganicOnlyMainRef(t, bare)
	return organicRepository{
		worktree:     repo,
		bare:         bare,
		baseRevision: baseRevision,
	}
}

func configureOrganicBareObjectAuthority(
	t *testing.T,
	repo string,
	bare string,
	baseRevision string,
) {
	t.Helper()
	gitDir := organicGit(t, repo, "rev-parse", "--absolute-git-dir")
	objectDir, err := filepath.EvalSymlinks(filepath.Join(gitDir, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	objectDir, err = filepath.Abs(objectDir)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(objectDir) {
		t.Fatalf("alternate object authority is not absolute: %q", objectDir)
	}
	info, err := os.Stat(objectDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("alternate object authority %q: %v", objectDir, err)
	}
	alternate := filepath.ToSlash(filepath.Clean(objectDir))
	if strings.ContainsAny(alternate, "\x00\r\n") {
		t.Fatalf("alternate object authority is not one portable path: %q", alternate)
	}
	infoDir := filepath.Join(bare, "objects", "info")
	if err := os.MkdirAll(infoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(infoDir, "alternates"),
		[]byte(alternate+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := organicBareGitOutput(
		context.Background(),
		bare,
		"cat-file",
		"-e",
		baseRevision+"^{commit}",
	); err != nil {
		t.Fatalf("pre-existing bare object authority cannot resolve base: %v", err)
	}
}

func organicGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	output, err := organicGitOutput(
		context.Background(),
		repo,
		arguments...,
	)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func organicGitOutput(
	parent context.Context,
	repo string,
	arguments ...string,
) (string, error) {
	ctx, cancel := context.WithTimeout(
		parent,
		organicLocalTimeout,
	)
	defer cancel()
	command := organicCommandContext(
		ctx,
		"git",
		append([]string{"-C", repo}, arguments...)...,
	)
	command.Env = append(
		os.Environ(),
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git -C %q %v: %w\n%s", repo, arguments, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func organicBareGitOutput(
	parent context.Context,
	bare string,
	arguments ...string,
) (string, error) {
	ctx, cancel := context.WithTimeout(parent, organicLocalTimeout)
	defer cancel()
	command := organicCommandContext(
		ctx,
		"git",
		append([]string{"--git-dir=" + bare}, arguments...)...,
	)
	command.Env = append(
		os.Environ(),
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(
			"git --git-dir=%q %v: %w\n%s",
			bare,
			arguments,
			err,
			output,
		)
	}
	return strings.TrimSpace(string(output)), nil
}

func requireOrganicOnlyMainRef(parent context.Context, bare string) error {
	refs, err := organicBareGitOutput(
		parent,
		bare,
		"for-each-ref",
		"--format=%(refname)",
	)
	if err != nil {
		return err
	}
	if refs != "refs/heads/main" {
		return fmt.Errorf(
			"bare repository refs = %q, want only refs/heads/main",
			refs,
		)
	}
	return nil
}

func assertOrganicOnlyMainRef(t *testing.T, bare string) {
	t.Helper()
	if err := requireOrganicOnlyMainRef(context.Background(), bare); err != nil {
		t.Fatal(err)
	}
}

func organicModuleRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func organicTestBinary(t *testing.T, moduleRoot string) string {
	t.Helper()
	if configured := os.Getenv(testBinaryEnvironment); configured != "" {
		path, err := filepath.Abs(configured)
		if err != nil {
			t.Fatal(err)
		}
		requireRegularFile(t, path)
		return path
	}
	name := "gentle-ai"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(
		context.Background(),
		organicSetupTimeout,
	)
	defer cancel()
	command := organicCommandContext(
		ctx,
		"go",
		"build",
		"-trimpath",
		"-o",
		path,
		"./cmd/gentle-ai",
	)
	command.Dir = moduleRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build test binary: %v\n%s", err, output)
	}
	requireRegularFile(t, path)
	return path
}

func prepareOpenCodeConfig(t *testing.T) string {
	t.Helper()
	requireExecutable(t, "npm")
	root := t.TempDir()
	config := filepath.Join(root, "opencode")
	if err := os.MkdirAll(config, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(
		`{"private":true,"dependencies":{"@opencode-ai/plugin":"` +
			pinnedOpenCodeVersion + `"}}` + "\n",
	)
	if err := os.WriteFile(
		filepath.Join(config, "package.json"),
		manifest,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(
		context.Background(),
		organicSetupTimeout,
	)
	defer cancel()
	command := organicCommandContext(
		ctx,
		"npm",
		"install",
		"--ignore-scripts",
		"--no-audit",
		"--no-fund",
		"--package-lock=false",
		"--prefix",
		config,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("prepare pinned OpenCode plugin: %v\n%s", err, output)
	}
	requireRegularFile(
		t,
		filepath.Join(
			config,
			"node_modules",
			"@opencode-ai",
			"plugin",
			"package.json",
		),
	)
	return root
}

func requireRegularFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("required file %q: %v", path, err)
	}
}

func requireExecutable(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Fatalf("required executable %s: %v", name, err)
	}
}

func requireExecutableVersion(t *testing.T, name string, expected string) {
	t.Helper()
	requireExecutable(t, name)
	ctx, cancel := context.WithTimeout(
		context.Background(),
		organicLocalTimeout,
	)
	defer cancel()
	command := organicCommandContext(ctx, name, "--version")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s --version: %v\n%s", name, err, output)
	}
	if strings.TrimSpace(string(output)) != expected {
		t.Fatalf(
			"%s version = %q, want %q",
			name,
			strings.TrimSpace(string(output)),
			expected,
		)
	}
}

func organicCommandContext(
	ctx context.Context,
	name string,
	arguments ...string,
) *exec.Cmd {
	command := exec.CommandContext(ctx, name, arguments...)
	command.WaitDelay = organicCommandWaitDelay
	return command
}

func writeOrganicServerCA(
	t *testing.T,
	server *organicRuntimeServer,
) string {
	t.Helper()
	certificate := server.Certificate()
	if certificate == nil {
		t.Fatal("TLS server has no certificate")
	}
	if _, err := x509.ParseCertificate(certificate.Raw); err != nil {
		t.Fatal(err)
	}
	payload := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certificate.Raw,
	})
	path := filepath.Join(t.TempDir(), "runtime-ca.pem")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func decodeExactJSON(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func writeExactJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
	}
}

func messageText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var builder strings.Builder
		for _, part := range value {
			encoded, _ := json.Marshal(part)
			builder.Write(encoded)
		}
		return builder.String()
	default:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
}
