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
	realAgentE2EEnvironment = "GENTLE_AI_REAL_AGENT_E2E"
	testBinaryEnvironment   = "GENTLE_AI_TEST_BINARY"
	pinnedOpenCodeVersion   = versions.OpenCode
	runtimeBearerToken      = "organic-e2e-owner-secret"
	runtimeSessionRef       = "session:organic-runtime-e2e"
)

type realAgentScenario struct {
	name          string
	workRunID     string
	outcome       string
	routing       workrun.ImplementationRouteInput
	expectedRoute workrun.ImplementationRoute
	actorTool     string
	actorMarker   string
	actorPrompt   string
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

	scenarios := []realAgentScenario{
		{
			name:      "direct inline implementation",
			workRunID: "organic-e2e-direct",
			outcome:   "Apply one already-understood mechanical file change.",
			routing: workrun.ImplementationRouteInput{
				WriteIntent:    workrun.WriteIntentAtomicMechanical,
				WriteFileCount: 1,
			},
			expectedRoute: workrun.ImplementationRouteDirectInline,
			actorTool:     "bash",
			actorMarker:   "DIRECT_IMPLEMENTATION_OK",
		},
		{
			name:      "delegated direct implementation",
			workRunID: "organic-e2e-delegated",
			outcome:   "Understand four files and implement the bounded outcome.",
			routing: workrun.ImplementationRouteInput{
				ReadIntent:    workrun.ReadIntentExploreUnderstand,
				ReadFileCount: 4,
			},
			expectedRoute: workrun.ImplementationRouteDelegatedDirect,
			actorTool:     "task",
			actorMarker:   "DELEGATED_IMPLEMENTATION_OK",
			actorPrompt: "Act as the delegated-direct implementation worker. " +
				"Read the exact managed WorkRun status, confirm its route is " +
				"delegated_direct with no SDD run, then return exactly " +
				"DELEGATED_IMPLEMENTATION_OK.",
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
			actorTool:     "task",
			actorMarker:   "COMMON_REVIEW_OK",
			actorPrompt: "Act as the common native review worker after direct " +
				"implementation. Read the exact managed WorkRun status, confirm " +
				"the route remains direct_inline with no SDD run, then return " +
				"exactly COMMON_REVIEW_OK.",
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
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	repo := initOrganicRepository(t)
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	repositoryRef := lease.Identity().RepositoryRef
	baseRevision := organicGit(t, repo, "rev-parse", "HEAD")

	policies := organicRoutePolicies(t, 1)
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
		Route:          deliveryadmission.RoutePRWithoutIssue,
		ScopeSelectors: []string{"tracked.txt"},
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
		repo,
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

	command := exec.CommandContext(
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
	command.Env = append(os.Environ(),
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
	)
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
	assertOrganicJourney(
		t,
		events,
		scenario,
		repositoryRef,
		runtimeSessionRef,
	)
	modelServer.assertComplete(t)
	runtimeServer.assertCalls(t)
}

type organicRuntimeServer struct {
	*httptest.Server
	mu             sync.Mutex
	repositoryRef  string
	repositoryRoot string
	snapshot       workprovider.ProductivePolicySnapshot
	intake         workprovider.OwnerOutcomeIntake
	scenario       realAgentScenario
	operations     []workprovider.ProductiveRuntimeOperation
	bootstraps     int
}

func newOrganicRuntimeServer(
	t *testing.T,
	repositoryRef string,
	repositoryRoot string,
	snapshot workprovider.ProductivePolicySnapshot,
	intake workprovider.OwnerOutcomeIntake,
	scenario realAgentScenario,
) *organicRuntimeServer {
	t.Helper()
	fixture := &organicRuntimeServer{
		repositoryRef:  repositoryRef,
		repositoryRoot: repositoryRoot,
		snapshot:       snapshot,
		intake:         intake,
		scenario:       scenario,
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
				intakeRequest.Context.RepositoryRoot != fixture.repositoryRoot ||
				intakeRequest.Request.Outcome != fixture.scenario.outcome ||
				intakeRequest.Request.ExplicitSDDRequested {
				http.Error(writer, "unexpected outcome intake payload", http.StatusBadRequest)
				return
			}
			payload = fixture.intake
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

func (fixture *organicRuntimeServer) assertCalls(t *testing.T) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	wantBootstraps := 4
	if fixture.scenario.actorTool == "task" {
		wantBootstraps++
	}
	if fixture.bootstraps != wantBootstraps {
		t.Fatalf(
			"runtime bootstraps = %d, want %d",
			fixture.bootstraps,
			wantBootstraps,
		)
	}
	var policyCalls int
	var intakeCalls int
	for _, operation := range fixture.operations {
		switch operation {
		case workprovider.ProductiveRuntimeOperationPolicySnapshot:
			policyCalls++
		case workprovider.ProductiveRuntimeOperationOutcomeIntake:
			intakeCalls++
		}
	}
	if policyCalls != 1 || intakeCalls != 1 {
		t.Fatalf("runtime operations = %#v", fixture.operations)
	}
}

type openCodeFixtureServer struct {
	*httptest.Server
	mu               sync.Mutex
	scenario         realAgentScenario
	requiredPrompt   string
	mainCalls        int
	subagentCalls    int
	subagentChecks   int
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
			fixture.subagentCalls++
			fixture.mu.Unlock()
			fixture.writeTool(
				writer,
				"actor-status",
				"bash",
				organicStatusCommand(),
			)
			return
		case "tool":
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
			map[string]any{
				"command": `node -e "process.stdout.write('` +
					fixture.scenario.actorMarker + `')"`,
			},
		)
	case 5:
		fixture.writeTool(writer, "status-after-actor", "bash", organicStatusCommand())
	case 6:
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
	if fixture.mainCalls != 6 || fixture.issuedActorTools != 1 {
		t.Fatalf(
			"model calls/actor tools = %d/%d",
			fixture.mainCalls,
			fixture.issuedActorTools,
		)
	}
	wantSubagents := 0
	if fixture.scenario.actorTool == "task" {
		wantSubagents = 1
	}
	if fixture.subagentCalls != wantSubagents {
		t.Fatalf(
			"subagent calls = %d, want %d",
			fixture.subagentCalls,
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

func assertOrganicJourney(
	t *testing.T,
	events []openCodeEvent,
	scenario realAgentScenario,
	repositoryRef string,
	sessionRef string,
) {
	t.Helper()
	var (
		capabilities *workprovider.RuntimeCapabilitiesV1
		start        *workrun.WorkStatusV1
		statuses     []workrun.WorkStatusV1
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
		}
	}
	if capabilities == nil ||
		capabilities.RepositoryRef != repositoryRef ||
		capabilities.AgentID != model.AgentOpenCode ||
		capabilities.WorkRouting.Exposure != workprovider.WorkRoutingAdvertised ||
		capabilities.ConnectorSessionRef != sessionRef {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	if len(statuses) != 2 {
		t.Fatalf("status observations = %d, want 2", len(statuses))
	}
	values := []struct {
		name  string
		value *workrun.WorkStatusV1
	}{
		{name: "start", value: start},
		{name: "status-before-actor", value: &statuses[0]},
		{name: "status-after-actor", value: &statuses[1]},
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
	if taskSeen != (scenario.actorTool == "task") {
		t.Fatalf("task actor seen = %t, want %t", taskSeen, scenario.actorTool == "task")
	}
	if start.Revision != statuses[0].Revision ||
		start.Revision != statuses[1].Revision ||
		start.ImplementationRoute != statuses[0].ImplementationRoute ||
		start.ImplementationRoute != statuses[1].ImplementationRoute {
		t.Fatalf(
			"actor changed route/revision: %#v -> %#v -> %#v",
			start,
			statuses[0],
			statuses[1],
		)
	}
}

func organicCapabilityCommand() map[string]any {
	return map[string]any{"command": organicNodeCommand(
		"['work-capabilities','--cwd',process.env.ORGANIC_E2E_REPO,"+
			"'--contract','gentle-ai.work-capabilities/v1','--json']",
		"",
	)}
}

func organicStartCommand() map[string]any {
	return map[string]any{"command": organicNodeCommand(
		"['work-start','--cwd',process.env.ORGANIC_E2E_REPO,"+
			"'--contract','gentle-ai.work-start/v1','--json']",
		"input:JSON.stringify({outcome:process.env.ORGANIC_E2E_OUTCOME,"+
			"explicitSddRequested:false}),",
	)}
}

func organicStatusCommand() map[string]any {
	return map[string]any{"command": organicNodeCommand(
		"['work-status','--cwd',process.env.ORGANIC_E2E_REPO,"+
			"'--work-run',process.env.ORGANIC_E2E_WORK_RUN_ID,"+
			"'--contract','gentle-ai.work-status/v1','--json']",
		"",
	)}
}

func organicNodeCommand(arguments string, extraOptions string) string {
	return `node -e "const {spawnSync}=require('child_process');` +
		`const r=spawnSync(process.env.GENTLE_AI_TEST_BINARY,` +
		arguments + `,{` + extraOptions +
		`encoding:'utf8',env:process.env});` +
		`process.stdout.write(JSON.stringify({status:r.status,` +
		`stdout:r.stdout||'',stderr:r.stderr||''}));` +
		`process.exit(r.status===0?0:1)"`
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
			3600,
			0,
		)
		if err != nil {
			t.Fatal(err)
		}
		policies = append(policies, policy)
	}
	return policies
}

func initOrganicRepository(t *testing.T) string {
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
	return repo
}

func organicGit(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, arguments...)...)
	command.Env = append(
		os.Environ(),
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
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
	command := exec.Command("go", "build", "-trimpath", "-o", path, "./cmd/gentle-ai")
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(
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
	command := exec.Command(name, "--version")
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
