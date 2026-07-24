package workprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const (
	runtimeConnectorTestBearer  = "runtime-connector-test-secret"
	runtimeConnectorTestSession = "session:runtime-connector-test"
)

func TestHTTPSProductiveRuntimeConnectorImplementsExactTypedPorts(t *testing.T) {
	repositoryRef := runtimeConnectorTestRef("repository")
	baseRevision := runtimeConnectorTestGitRevision("base")
	candidateRevision := runtimeConnectorTestGitRevision("candidate")
	candidate := deliveryadmission.CandidateBinding{
		Ref:    "candidate:runtime-connector",
		Digest: runtimeConnectorTestRef("candidate-binding"),
	}
	destination := deliveryadmission.DestinationBinding{
		RepositoryRef:    repositoryRef,
		TargetRef:        "refs/heads/main",
		ObservedRevision: baseRevision,
		DefaultBranch:    true,
	}
	directBinding := PADGitBinding{
		Schema:                 PADGitBindingSchema,
		Candidate:              candidate,
		Destination:            destination,
		Mechanism:              deliveryadmission.MechanismFastForwardOnly,
		HostingRepositoryRef:   "hosting:owner/repository",
		CandidateRevision:      candidateRevision,
		ExpectedRemoteRevision: baseRevision,
	}
	pullRequestBinding := directBinding
	pullRequestBinding.Mechanism = deliveryadmission.MechanismPullRequest
	pullRequestBinding.PullRequestRef = "pull-request:42"
	policies := runtimeConnectorTestPolicies(t, 7, false)
	ticket, process := runtimeConnectorTestSemanticExecution(t)

	var calls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		calls.Add(1)
		runtimeConnectorRequireHeaders(t, request)
		switch request.URL.Path {
		case ProductiveRuntimeBootstrapPathV1:
			bootstrap := runtimeConnectorDecodeBootstrap(t, request)
			response, err := NewProductiveRuntimeBootstrapResponse(
				bootstrap,
				runtimeConnectorTestSession,
			)
			if err != nil {
				t.Fatal(err)
			}
			runtimeConnectorWriteJSON(t, writer, response)
		case ProductiveRuntimeCallPathV1:
			call := runtimeConnectorDecodeCall(t, request)
			var payload any
			var payloadErr error
			switch call.Operation {
			case ProductiveRuntimeOperationPolicySnapshot:
				payload, payloadErr = NewProductivePolicySnapshot(
					repositoryRef,
					model.AgentCodex,
					runtimeConnectorTestSession,
					7,
					policies,
				)
			case ProductiveRuntimeOperationOutcomeIntake:
				var input productiveOutcomeIntakeRequest
				runtimeConnectorDecodePayload(t, call.Payload, &input)
				if input.Context.RepositoryRef != repositoryRef ||
					input.Request.Outcome != "Ship the exact governed result." {
					t.Fatalf("outcome input = %#v", input)
				}
				legacy := ownerOutcomeTestIntake(
					"runtime-connector-run",
					deliveryadmission.RouteDirectMain,
				)
				legacy.RoutingFacts = workrun.ImplementationRouteInput{
					WriteIntent:    workrun.WriteIntentAnalytical,
					WriteFileCount: 2,
					DurablePlanning: []workrun.DurablePlanningReason{
						workrun.PlanningArchitecture,
					},
				}
				payload = legacy
			case ProductiveRuntimeOperationSemantic:
				var input productiveSemanticEvaluationRequest
				runtimeConnectorDecodePayload(t, call.Payload, &input)
				payload = SemanticEvaluation{
					Schema:               SemanticEvaluationSchemaV1,
					RequirementRef:       input.Ticket.SemanticRequirementRef,
					CandidateRef:         input.Ticket.CandidateRef,
					RequestDigest:        input.Process.RequestDigest,
					ToolchainIdentityRef: input.Process.ToolchainIdentityRef,
					StdoutRawDigest:      input.Process.Stdout.RawDigest,
					StderrRawDigest:      input.Process.Stderr.RawDigest,
					Outcome:              SemanticEvaluationPassed,
				}
			case ProductiveRuntimeOperationPADGitBinding:
				var input productivePADGitBindingRequest
				runtimeConnectorDecodePayload(t, call.Payload, &input)
				if input.Mechanism == deliveryadmission.MechanismPullRequest {
					payload = pullRequestBinding
				} else {
					payload = directBinding
				}
			case ProductiveRuntimeOperationObserveDelivery:
				var input HostingObservationRequest
				runtimeConnectorDecodePayload(t, call.Payload, &input)
				payload = HostingDeliveryObservation{
					Schema:                 HostingDeliveryObservationSchema,
					Request:                input,
					CandidateRevision:      input.Binding.CandidateRevision,
					ExpectedRemoteRevision: input.Binding.ExpectedRemoteRevision,
					RemoteIdentity:         "remote:origin/main",
					RemoteRevision:         baseRevision,
					ProtectionState:        HostingProtectionPermitted,
					ProtectionRevision:     "protection:7",
					PullRequestState:       HostingPullRequestNotApplicable,
					RequiredChecksState:    HostingChecksNotApplicable,
				}
			case ProductiveRuntimeOperationBranchCAS:
				var input HostingBranchCASRequest
				runtimeConnectorDecodePayload(t, call.Payload, &input)
				payload = HostingBranchCASReceipt{
					Schema:           HostingBranchCASReceiptSchema,
					Request:          input,
					Outcome:          HostingEffectApplied,
					RemoteRevision:   input.CandidateRevision,
					DeliveryRef:      input.CandidateRevision,
					EvidenceRevision: "evidence:branch-cas/7",
				}
			case ProductiveRuntimeOperationMergePR:
				var input HostingPullRequestMergeRequest
				runtimeConnectorDecodePayload(t, call.Payload, &input)
				payload = HostingPullRequestMergeReceipt{
					Schema:             HostingPullRequestReceiptSchema,
					Request:            input,
					Outcome:            HostingEffectApplied,
					RemoteRevision:     runtimeConnectorTestGitRevision("merge"),
					MergedHeadRevision: input.ExpectedHeadRevision,
					DeliveryRef:        "delivery:pull-request/42",
					EvidenceRevision:   "evidence:pull-request/42",
				}
			default:
				t.Fatalf("unsupported operation %q", call.Operation)
			}
			if payloadErr != nil {
				t.Fatal(payloadErr)
			}
			response, err := NewProductiveRuntimeResponse(call, payload)
			if err != nil {
				t.Fatal(err)
			}
			runtimeConnectorWriteJSON(t, writer, response)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	connector, err := NewHTTPSProductiveRuntimeConnector(
		context.Background(),
		runtimeConnectorTestConfig(
			server,
			repositoryRef,
			0,
			0,
		),
		runtimeConnectorTestBearer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if connector.RepositoryRef() != repositoryRef ||
		connector.AgentID() != model.AgentCodex ||
		connector.ConnectorSessionRef() != runtimeConnectorTestSession {
		t.Fatalf("connector identity = %#v", connector.Handshake())
	}
	handshake := connector.Handshake()
	handshake.Operations[0] = ProductiveRuntimeOperation("tampered")
	if connector.Handshake().Operations[0] != ProductiveRuntimeOperationPolicySnapshot {
		t.Fatal("Handshake() exposed mutable operation storage")
	}

	firstSnapshot, err := connector.ResolvePolicySnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot, err := connector.ResolvePolicySnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstSnapshot, secondSnapshot) {
		t.Fatalf("idempotent policy snapshots differ: %#v %#v", firstSnapshot, secondSnapshot)
	}
	intake, err := connector.ResolveOutcomeIntake(
		context.Background(),
		OwnerOutcomeContext{
			RepositoryRef:  repositoryRef,
			RepositoryRoot: t.TempDir(),
		},
		OutcomeStartRequest{Outcome: "Ship the exact governed result."},
	)
	if err != nil {
		t.Fatal(err)
	}
	if intake.WorkRunID != "runtime-connector-run" ||
		intake.DeclinedSDDFallback != nil {
		t.Fatalf("outcome intake = %#v", intake)
	}
	if err := intake.validate(repositoryRef, OutcomeStartRequest{
		Outcome: "Ship the exact governed result.",
	}); err == nil || !strings.Contains(
		err.Error(),
		"requires an owner-authored",
	) {
		t.Fatalf("legacy transport intake strict validation error = %v", err)
	}
	evaluation, err := connector.EvaluateSemanticExecution(
		context.Background(),
		ticket,
		process,
	)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Outcome != SemanticEvaluationPassed {
		t.Fatalf("semantic evaluation = %#v", evaluation)
	}

	bindingTransport, err := NewTransportPADGitBindingAuthority(connector)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := bindingTransport.ResolvePADGitBinding(
		context.Background(),
		candidate,
		destination,
		deliveryadmission.MechanismFastForwardOnly,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != directBinding {
		t.Fatalf("PAD Git binding = %#v", resolved)
	}
	hosting, err := NewTransportHostingAuthority(connector)
	if err != nil {
		t.Fatal(err)
	}
	observationRequest := HostingObservationRequest{
		Binding:   directBinding,
		Mechanism: deliveryadmission.MechanismFastForwardOnly,
	}
	observation, err := hosting.ObserveDelivery(
		context.Background(),
		observationRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.RemoteRevision != baseRevision {
		t.Fatalf("hosting observation = %#v", observation)
	}
	casRequest := HostingBranchCASRequest{
		CommandRef:           runtimeConnectorTestRef("cas-command"),
		RepositoryRef:        repositoryRef,
		HostingRepositoryRef: directBinding.HostingRepositoryRef,
		TargetRef:            destination.TargetRef,
		ExpectedRevision:     baseRevision,
		CandidateRevision:    candidateRevision,
		ExecutionExpiresAt:   time.Now().Add(time.Minute).Unix(),
	}
	casReceipt, err := hosting.CompareAndSwapBranch(
		context.Background(),
		casRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if casReceipt.Outcome != HostingEffectApplied {
		t.Fatalf("branch CAS receipt = %#v", casReceipt)
	}
	mergeRequest := HostingPullRequestMergeRequest{
		CommandRef:           runtimeConnectorTestRef("merge-command"),
		RepositoryRef:        repositoryRef,
		HostingRepositoryRef: directBinding.HostingRepositoryRef,
		TargetRef:            destination.TargetRef,
		PullRequestRef:       pullRequestBinding.PullRequestRef,
		ExpectedBaseRevision: baseRevision,
		ExpectedHeadRevision: candidateRevision,
		ExecutionExpiresAt:   time.Now().Add(time.Minute).Unix(),
	}
	mergeReceipt, err := hosting.MergePullRequest(
		context.Background(),
		mergeRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if mergeReceipt.Outcome != HostingEffectApplied {
		t.Fatalf("pull-request merge receipt = %#v", mergeReceipt)
	}
	if calls.Load() != 9 {
		t.Fatalf("authenticated calls = %d, want 9", calls.Load())
	}
}

func TestHTTPSProductiveRuntimeConnectorRejectsUnsafeConfigurationAndRedirects(
	t *testing.T,
) {
	repositoryRef := runtimeConnectorTestRef("repository")
	tests := []struct {
		name   string
		config ProductiveRuntimeConnectorConfig
		token  string
	}{
		{
			name: "plain HTTP",
			config: ProductiveRuntimeConnectorConfig{
				EndpointURL:   "http://runtime.example.test",
				RepositoryRef: repositoryRef,
				AgentID:       model.AgentCodex,
			},
			token: runtimeConnectorTestBearer,
		},
		{
			name: "URL credentials",
			config: ProductiveRuntimeConnectorConfig{
				EndpointURL:   "https://user:password@runtime.example.test",
				RepositoryRef: repositoryRef,
				AgentID:       model.AgentCodex,
			},
			token: runtimeConnectorTestBearer,
		},
		{
			name: "newline in bearer",
			config: ProductiveRuntimeConnectorConfig{
				EndpointURL:   "https://runtime.example.test",
				RepositoryRef: repositoryRef,
				AgentID:       model.AgentCodex,
			},
			token: "secret\ninjection",
		},
		{
			name: "unknown agent",
			config: ProductiveRuntimeConnectorConfig{
				EndpointURL:   "https://runtime.example.test",
				RepositoryRef: repositoryRef,
				AgentID:       model.AgentID("unknown"),
			},
			token: runtimeConnectorTestBearer,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewHTTPSProductiveRuntimeConnector(
				context.Background(),
				test.config,
				test.token,
			)
			if !errors.Is(err, ErrProductiveRuntimeInvalidConfig) {
				t.Fatalf("configuration error = %v", err)
			}
			if strings.Contains(err.Error(), test.token) {
				t.Fatal("configuration error exposed the bearer credential")
			}
		})
	}

	var redirected atomic.Bool
	var target *httptest.Server
	target = httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		redirected.Store(true)
		http.Error(writer, "must not arrive", http.StatusInternalServerError)
	}))
	defer target.Close()
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		http.Redirect(
			writer,
			request,
			target.URL+ProductiveRuntimeBootstrapPathV1,
			http.StatusTemporaryRedirect,
		)
	}))
	defer redirector.Close()
	config := runtimeConnectorTestConfig(
		redirector,
		repositoryRef,
		0,
		0,
	)
	config.HTTPClient.Transport = redirector.Client().Transport
	if _, err := NewHTTPSProductiveRuntimeConnector(
		context.Background(),
		config,
		runtimeConnectorTestBearer,
	); !errors.Is(err, ErrProductiveRuntimeRedirect) {
		t.Fatalf("redirect error = %v", err)
	}
	if redirected.Load() {
		t.Fatal("connector followed an HTTP redirect")
	}
}

func TestHTTPSProductiveRuntimeConnectorFailsClosedOnInvalidResponses(
	t *testing.T,
) {
	repositoryRef := runtimeConnectorTestRef("repository")
	tests := []struct {
		name string
		send func(*testing.T, http.ResponseWriter, ProductiveRuntimeBootstrapRequest)
		want error
	}{
		{
			name: "non 2xx",
			send: func(_ *testing.T, writer http.ResponseWriter, _ ProductiveRuntimeBootstrapRequest) {
				http.Error(writer, "secret server detail", http.StatusUnauthorized)
			},
			want: ErrProductiveRuntimeHTTPStatus,
		},
		{
			name: "unknown field",
			send: func(t *testing.T, writer http.ResponseWriter, request ProductiveRuntimeBootstrapRequest) {
				response := runtimeConnectorBootstrapResponse(t, request)
				body, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				body = append(body[:len(body)-1], []byte(`,"unexpected":true}`)...)
				runtimeConnectorWriteRawJSON(writer, body)
			},
			want: ErrProductiveRuntimeMalformedResponse,
		},
		{
			name: "duplicate field",
			send: func(t *testing.T, writer http.ResponseWriter, request ProductiveRuntimeBootstrapRequest) {
				response := runtimeConnectorBootstrapResponse(t, request)
				body, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				body = append(
					[]byte(`{"schema":"`+ProductiveRuntimeBootstrapResponseSchemaV1+`",`),
					body[1:]...,
				)
				runtimeConnectorWriteRawJSON(writer, body)
			},
			want: ErrProductiveRuntimeMalformedResponse,
		},
		{
			name: "cross repository",
			send: func(t *testing.T, writer http.ResponseWriter, request ProductiveRuntimeBootstrapRequest) {
				response := runtimeConnectorBootstrapResponse(t, request)
				response.RepositoryRef = runtimeConnectorTestRef("foreign")
				response.ResponseDigest, _ = productiveRuntimeDigest(response.projection())
				runtimeConnectorWriteJSON(t, writer, response)
			},
			want: ErrProductiveRuntimeBindingMismatch,
		},
		{
			name: "nonce mismatch",
			send: func(t *testing.T, writer http.ResponseWriter, request ProductiveRuntimeBootstrapRequest) {
				response := runtimeConnectorBootstrapResponse(t, request)
				response.Nonce = "nonce:" + strings.Repeat("0", 64)
				response.ResponseDigest, _ = productiveRuntimeDigest(response.projection())
				runtimeConnectorWriteJSON(t, writer, response)
			},
			want: ErrProductiveRuntimeBindingMismatch,
		},
		{
			name: "response digest mismatch",
			send: func(t *testing.T, writer http.ResponseWriter, request ProductiveRuntimeBootstrapRequest) {
				response := runtimeConnectorBootstrapResponse(t, request)
				response.ResponseDigest = runtimeConnectorTestRef("wrong")
				runtimeConnectorWriteJSON(t, writer, response)
			},
			want: ErrProductiveRuntimeBindingMismatch,
		},
		{
			name: "truncated JSON",
			send: func(_ *testing.T, writer http.ResponseWriter, _ ProductiveRuntimeBootstrapRequest) {
				runtimeConnectorWriteRawJSON(writer, []byte(`{"schema":`))
			},
			want: ErrProductiveRuntimeMalformedResponse,
		},
		{
			name: "wrong content type",
			send: func(_ *testing.T, writer http.ResponseWriter, _ ProductiveRuntimeBootstrapRequest) {
				writer.Header().Set("Content-Type", "text/plain")
				_, _ = writer.Write([]byte(`{}`))
			},
			want: ErrProductiveRuntimeMalformedResponse,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				httpRequest *http.Request,
			) {
				runtimeConnectorRequireHeaders(t, httpRequest)
				request := runtimeConnectorDecodeBootstrap(t, httpRequest)
				test.send(t, writer, request)
			}))
			defer server.Close()
			_, err := NewHTTPSProductiveRuntimeConnector(
				context.Background(),
				runtimeConnectorTestConfig(
					server,
					repositoryRef,
					0,
					0,
				),
				runtimeConnectorTestBearer,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("response error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), runtimeConnectorTestBearer) ||
				strings.Contains(err.Error(), "secret server detail") {
				t.Fatal("connector error exposed a secret")
			}
		})
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(strings.Repeat("x", 513)))
	}))
	defer server.Close()
	_, err := NewHTTPSProductiveRuntimeConnector(
		context.Background(),
		runtimeConnectorTestConfig(
			server,
			repositoryRef,
			0,
			512,
		),
		runtimeConnectorTestBearer,
	)
	if !errors.Is(err, ErrProductiveRuntimeResponseTooLarge) {
		t.Fatalf("oversize response error = %v", err)
	}
}

func TestDecodeStrictProductiveRuntimeJSONRejectsCaseFoldedDuplicates(
	t *testing.T,
) {
	type nested struct {
		Schema string `json:"schema"`
	}
	type envelope struct {
		Schema  string `json:"schema"`
		Payload nested `json:"payload"`
	}
	for _, test := range []struct {
		name    string
		payload string
	}{
		{
			name: "outer object",
			payload: `{
				"Schema":"shadow",
				"schema":"canonical",
				"payload":{"schema":"nested"}
			}`,
		},
		{
			name: "nested object",
			payload: `{
				"schema":"canonical",
				"payload":{"Schema":"shadow","schema":"nested"}
			}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var decoded envelope
			if err := decodeStrictProductiveRuntimeJSON(
				[]byte(test.payload),
				&decoded,
			); !errors.Is(err, ErrProductiveRuntimeMalformedResponse) {
				t.Fatalf("case-folded duplicate error = %v", err)
			}
		})
	}
}

func TestRejectProductiveRuntimeDuplicateJSONFieldsScalesLinearly(
	t *testing.T,
) {
	const uniqueFields = 32_768
	var payload strings.Builder
	payload.Grow(uniqueFields * 16)
	payload.WriteByte('{')
	for index := range uniqueFields {
		payload.WriteString(`"field`)
		payload.WriteString(strconv.Itoa(index))
		payload.WriteString(`":0,`)
	}
	payload.WriteString(`"Field0":1}`)
	if payload.Len() > int(defaultProductiveRuntimeResponseBytes) {
		t.Fatalf(
			"adversarial JSON is %d bytes, beyond the default response bound",
			payload.Len(),
		)
	}
	if err := rejectProductiveRuntimeDuplicateJSONFields(
		[]byte(payload.String()),
	); err == nil {
		t.Fatal("case-folded duplicate after many unique fields was accepted")
	}
}

func TestFoldProductiveRuntimeJSONNameMatchesEqualFold(t *testing.T) {
	for _, pair := range [][2]string{
		{"Schema", "schema"},
		{"K", "\u212a"},
		{"S", "\u017f"},
		{"repositoryRef", "repositoryREF"},
		{"schema", "scheme"},
		{"agentId", "agent-id"},
	} {
		got := foldProductiveRuntimeJSONName(pair[0]) ==
			foldProductiveRuntimeJSONName(pair[1])
		want := strings.EqualFold(pair[0], pair[1])
		if got != want {
			t.Fatalf("fold equivalence for %q and %q = %t, want %t", pair[0], pair[1], got, want)
		}
	}
}

func TestHTTPSProductiveRuntimeConnectorRejectsPolicyForkAndCrossRepoInputs(
	t *testing.T,
) {
	repositoryRef := runtimeConnectorTestRef("repository")
	foreignRepositoryRef := runtimeConnectorTestRef("foreign")
	policies := runtimeConnectorTestPolicies(t, 5, false)
	forked := runtimeConnectorTestPolicies(t, 5, true)
	var policyCalls atomic.Int64
	var allCalls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		allCalls.Add(1)
		if request.URL.Path == ProductiveRuntimeBootstrapPathV1 {
			bootstrap := runtimeConnectorDecodeBootstrap(t, request)
			runtimeConnectorWriteJSON(
				t,
				writer,
				runtimeConnectorBootstrapResponse(t, bootstrap),
			)
			return
		}
		call := runtimeConnectorDecodeCall(t, request)
		if call.Operation != ProductiveRuntimeOperationPolicySnapshot {
			t.Fatalf("unexpected cross-repository network call %q", call.Operation)
		}
		index := policyCalls.Add(1)
		selected := policies
		if index == 2 {
			selected = forked
		}
		snapshot, err := NewProductivePolicySnapshot(
			repositoryRef,
			model.AgentCodex,
			runtimeConnectorTestSession,
			5,
			selected,
		)
		if err != nil {
			t.Fatal(err)
		}
		response, err := NewProductiveRuntimeResponse(call, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		runtimeConnectorWriteJSON(t, writer, response)
	}))
	defer server.Close()
	connector, err := NewHTTPSProductiveRuntimeConnector(
		context.Background(),
		runtimeConnectorTestConfig(server, repositoryRef, 0, 0),
		runtimeConnectorTestBearer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connector.ResolvePolicySnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := connector.ResolvePolicySnapshot(
		context.Background(),
	); !errors.Is(err, ErrProductiveRuntimeBindingMismatch) {
		t.Fatalf("forked policy revision error = %v", err)
	}
	before := allCalls.Load()
	if _, err := connector.ResolveOutcomeIntake(
		context.Background(),
		OwnerOutcomeContext{
			RepositoryRef:  foreignRepositoryRef,
			RepositoryRoot: t.TempDir(),
		},
		OutcomeStartRequest{Outcome: "Do not cross repositories."},
	); !errors.Is(err, ErrProductiveRuntimeBindingMismatch) {
		t.Fatalf("cross-repository outcome error = %v", err)
	}
	candidate := deliveryadmission.CandidateBinding{
		Ref:    "candidate:foreign",
		Digest: runtimeConnectorTestRef("candidate"),
	}
	destination := deliveryadmission.DestinationBinding{
		RepositoryRef:    foreignRepositoryRef,
		TargetRef:        "refs/heads/main",
		ObservedRevision: runtimeConnectorTestGitRevision("base"),
		DefaultBranch:    true,
	}
	if _, err := connector.ResolvePADGitBinding(
		context.Background(),
		candidate,
		destination,
		deliveryadmission.MechanismFastForwardOnly,
	); !errors.Is(err, ErrProductiveRuntimeBindingMismatch) {
		t.Fatalf("cross-repository binding error = %v", err)
	}
	if allCalls.Load() != before {
		t.Fatal("cross-repository input reached the connector authority")
	}
}

func TestHTTPSProductiveRuntimeConnectorHonorsCancellationAndTimeout(
	t *testing.T,
) {
	repositoryRef := runtimeConnectorTestRef("repository")
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		close(started)
		<-release
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := NewHTTPSProductiveRuntimeConnector(
			ctx,
			runtimeConnectorTestConfig(server, repositoryRef, time.Second, 0),
			runtimeConnectorTestBearer,
		)
		result <- err
	}()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	close(release)

	timeoutRelease := make(chan struct{})
	timeoutServer := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		<-timeoutRelease
	}))
	defer timeoutServer.Close()
	_, err := NewHTTPSProductiveRuntimeConnector(
		context.Background(),
		runtimeConnectorTestConfig(
			timeoutServer,
			repositoryRef,
			10*time.Millisecond,
			0,
		),
		runtimeConnectorTestBearer,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	close(timeoutRelease)
}

func runtimeConnectorTestSemanticExecution(
	t *testing.T,
) (evidence.ActionTicket, hostruntime.ProcessEvidence) {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	issuerRef := runtimeConnectorTestRef("semantic-issuer")
	requirementRef := runtimeConnectorTestRef("semantic-requirement")
	authority, err := evidence.ProvisionSemanticAuthority(
		issuerRef,
		requirementRef,
		[]byte(strings.Repeat("s", evidence.SemanticAuthoritySeedBytes)),
	)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := evidence.NewActionTicket(evidence.ActionTicketRequest{
		IssuerRef:              issuerRef,
		SubjectRef:             runtimeConnectorTestRef("semantic-subject"),
		CandidateRef:           runtimeConnectorTestRef("semantic-candidate"),
		VerificationContextRef: runtimeConnectorTestRef("semantic-context"),
		ExpectedRevision:       runtimeConnectorTestRef("semantic-revision"),
		Slot:                   "verify.runtime-connector",
		Program:                program,
		Args: []string{
			"-test.run=^TestRuntimeConnectorHelperProcess$",
		},
		CWD:         t.TempDir(),
		Environment: map[string]string{"GO_WANT_RUNTIME_CONNECTOR_HELPER": "1"},
		Deadline:    5 * time.Second,
		OutputLimits: hostruntime.StreamLimits{
			StdoutBytes: 1 << 10,
			StderrBytes: 1 << 10,
		},
		Capability:             "go.test",
		SemanticRequirementRef: requirementRef,
		SemanticAuthority:      authority.Identity(),
	})
	if err != nil {
		t.Fatal(err)
	}
	step, err := ticket.ExecStep()
	if err != nil {
		t.Fatal(err)
	}
	executor := hostruntime.NewExecutor()
	capability, err := executor.ActivateLaunch(
		context.Background(),
		hostruntime.LaunchBinding{
			Schema:          hostruntime.LaunchBindingSchemaV1,
			WorkRunID:       "runtime-connector-test",
			ReservationRef:  runtimeConnectorTestRef("launch-reservation"),
			ActionTicketRef: ticket.TicketRef,
			RequestDigest:   ticket.RequestBinding.RequestDigest,
		},
		func(context.Context) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	process, err := executor.ExecuteAuthorized(
		context.Background(),
		step,
		capability,
	)
	if err != nil {
		t.Fatal(err)
	}
	return ticket, process
}

func TestRuntimeConnectorHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_RUNTIME_CONNECTOR_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("runtime connector semantic output")
}

func runtimeConnectorTestConfig(
	server *httptest.Server,
	repositoryRef string,
	timeout time.Duration,
	responseLimit int64,
) ProductiveRuntimeConnectorConfig {
	return ProductiveRuntimeConnectorConfig{
		EndpointURL:      server.URL,
		RepositoryRef:    repositoryRef,
		AgentID:          model.AgentCodex,
		HTTPClient:       server.Client(),
		RequestTimeout:   timeout,
		MaxResponseBytes: responseLimit,
	}
}

func runtimeConnectorRequireHeaders(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Method != http.MethodPost ||
		request.Header.Get("Authorization") !=
			"Bearer "+runtimeConnectorTestBearer ||
		request.Header.Get("Accept") != "application/json" ||
		request.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("runtime connector request headers = %#v", request.Header)
	}
}

func runtimeConnectorDecodeBootstrap(
	t *testing.T,
	request *http.Request,
) ProductiveRuntimeBootstrapRequest {
	t.Helper()
	var bootstrap ProductiveRuntimeBootstrapRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bootstrap); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Validate(); err != nil {
		t.Fatal(err)
	}
	return bootstrap
}

func runtimeConnectorDecodeCall(
	t *testing.T,
	request *http.Request,
) ProductiveRuntimeRequest {
	t.Helper()
	var call ProductiveRuntimeRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&call); err != nil {
		t.Fatal(err)
	}
	if err := call.Validate(); err != nil {
		t.Fatal(err)
	}
	return call
}

func runtimeConnectorDecodePayload(
	t *testing.T,
	payload []byte,
	output any,
) {
	t.Helper()
	if err := decodeStrictProductiveRuntimeJSON(payload, output); err != nil {
		t.Fatal(err)
	}
}

func runtimeConnectorBootstrapResponse(
	t *testing.T,
	request ProductiveRuntimeBootstrapRequest,
) ProductiveRuntimeBootstrapResponse {
	t.Helper()
	response, err := NewProductiveRuntimeBootstrapResponse(
		request,
		runtimeConnectorTestSession,
	)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func runtimeConnectorWriteJSON(
	t *testing.T,
	writer http.ResponseWriter,
	value any,
) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConnectorWriteRawJSON(writer, body)
}

func runtimeConnectorWriteRawJSON(
	writer http.ResponseWriter,
	body []byte,
) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body)
}

func runtimeConnectorTestPolicies(
	t *testing.T,
	revision uint64,
	fork bool,
) []deliveryadmission.RoutePolicy {
	t.Helper()
	policies := make(
		[]deliveryadmission.RoutePolicy,
		0,
		len(productivePolicyRoutesV1),
	)
	for _, route := range productivePolicyRoutesV1 {
		ttl := int64(120)
		if fork && route == deliveryadmission.RouteDirectMain {
			ttl++
		}
		policy, err := deliveryadmission.NewRoutePolicy(
			"policy:"+string(route),
			"snapshot:"+strconv.FormatUint(revision, 10),
			route,
			true,
			false,
			false,
			ttl,
			0,
		)
		if err != nil {
			t.Fatal(err)
		}
		policies = append(policies, policy)
	}
	return policies
}

func runtimeConnectorTestRef(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func runtimeConnectorTestGitRevision(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "git:" + hex.EncodeToString(sum[:20])
}
