package workprovider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type stubRuntimeOutcomeOpener struct {
	runtime RuntimeOutcome
	err     error
	calls   int
	repo    string
}

func (opener *stubRuntimeOutcomeOpener) OpenRuntimeOutcome(
	_ context.Context,
	repo string,
) (RuntimeOutcome, error) {
	opener.calls++
	opener.repo = repo
	return opener.runtime, opener.err
}

type stubRuntimeOutcome struct {
	capabilities RuntimeCapabilitiesV1
	status       workrun.WorkStatusV1
	started      []OutcomeStartRequest
}

func (runtime *stubRuntimeOutcome) Capabilities(
	context.Context,
) (RuntimeCapabilitiesV1, error) {
	return runtime.capabilities, nil
}

func (runtime *stubRuntimeOutcome) StartOutcome(
	_ context.Context,
	request OutcomeStartRequest,
) (workrun.WorkStatusV1, error) {
	runtime.started = append(runtime.started, request)
	return runtime.status, nil
}

func TestRuntimeControllerNegotiatesBeforeOpenOrPayloadValidation(t *testing.T) {
	t.Parallel()

	opener := &stubRuntimeOutcomeOpener{
		err: errors.New("runtime opener must not be called"),
	}
	controller := NewRuntimeController(opener)

	capabilities, err := controller.Capabilities(
		context.Background(),
		RuntimeCapabilitiesRequest{Contract: ""},
	)
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if capabilities.Diagnostic == nil ||
		capabilities.Diagnostic.Operation !=
			workrun.DiagnosticOperationWorkCapabilities {
		t.Fatalf("Capabilities() result = %#v", capabilities)
	}

	start, err := controller.Start(context.Background(), RuntimeStartRequest{
		Contract: "", Payload: []byte(`{"not":"validated"}`),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if start.Diagnostic == nil ||
		start.Diagnostic.Operation != workrun.DiagnosticOperationWorkStart {
		t.Fatalf("Start() result = %#v", start)
	}
	if opener.calls != 0 {
		t.Fatalf("unsupported contracts opened runtime %d times", opener.calls)
	}
}

func TestRuntimeControllerReturnsRepositoryBoundHandshakeAndWorkRun(t *testing.T) {
	t.Parallel()

	canonical, err := capabilitymanifest.ForAgent(model.AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &stubRuntimeOutcome{
		capabilities: RuntimeCapabilitiesV1{
			Schema:        workrun.WorkCapabilitiesContractV1,
			Contract:      workrun.WorkCapabilitiesContractV1,
			RepositoryRef: runtimeContractRef("repository"),
			AgentID:       model.AgentCodex,
			WorkRouting: RuntimeCapabilityClaimV1{
				ID:                    workrun.WorkRoutingCapabilityV1,
				Exposure:              WorkRoutingAdvertised,
				ImplementationRouting: canonical.ImplementationRouting,
			},
			Contracts: RuntimeContractSetV1{
				Start:      workrun.WorkStartContractV1,
				Status:     workrun.WorkStatusContractV1,
				Transition: workrun.WorkTransitionContractV1,
			},
			ConnectorSessionRef: runtimeContractRef("connector-session"),
		},
		status: workrun.WorkStatusV1{
			Schema:              workrun.WorkStatusContractV1,
			Contract:            workrun.WorkStatusContractV1,
			WorkRunID:           "work-runtime-contract",
			Revision:            runtimeContractRef("revision"),
			PublicState:         workrun.PublicStateWorking,
			RouteDecision:       workrun.RouteDecisionDirectInline,
			ImplementationRoute: workrun.ImplementationRouteDirectInline,
			Verification: workrun.VerificationSummaryV1{
				Outcome:    workrun.VerificationPending,
				ResultRefs: []string{},
			},
		},
	}
	opener := &stubRuntimeOutcomeOpener{runtime: runtime}
	controller := NewRuntimeController(opener)

	capabilities, err := controller.Capabilities(
		context.Background(),
		RuntimeCapabilitiesRequest{
			Repo: "/repo", Contract: workrun.WorkCapabilitiesContractV1,
		},
	)
	if err != nil {
		t.Fatalf("Capabilities() error = %v", err)
	}
	if capabilities.Capabilities == nil ||
		capabilities.Capabilities.ConnectorSessionRef !=
			runtime.capabilities.ConnectorSessionRef {
		t.Fatalf("Capabilities() result = %#v", capabilities)
	}

	start, err := controller.Start(context.Background(), RuntimeStartRequest{
		Repo: "/repo", Contract: workrun.WorkStartContractV1,
		Payload: []byte(
			`{"outcome":"Create the requested poster.","explicitSddRequested":false}`,
		),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if start.Status == nil ||
		start.Status.WorkRunID != runtime.status.WorkRunID {
		t.Fatalf("Start() result = %#v", start)
	}
	if len(runtime.started) != 1 ||
		runtime.started[0] != (OutcomeStartRequest{
			Outcome: "Create the requested poster.",
		}) {
		t.Fatalf("runtime requests = %#v", runtime.started)
	}
	if opener.calls != 2 || opener.repo != "/repo" {
		t.Fatalf("opener calls/repo = %d/%q", opener.calls, opener.repo)
	}
}

func TestRuntimeCapabilitiesValidateAdvertisementBinding(t *testing.T) {
	t.Parallel()

	canonical, err := capabilitymanifest.ForAgent(model.AgentPi)
	if err != nil {
		t.Fatal(err)
	}
	base := RuntimeCapabilitiesV1{
		Schema:        workrun.WorkCapabilitiesContractV1,
		Contract:      workrun.WorkCapabilitiesContractV1,
		RepositoryRef: runtimeContractRef("repository"),
		AgentID:       model.AgentPi,
		WorkRouting: RuntimeCapabilityClaimV1{
			ID:                    workrun.WorkRoutingCapabilityV1,
			Exposure:              WorkRoutingAdvertised,
			ImplementationRouting: canonical.ImplementationRouting,
		},
		Contracts: RuntimeContractSetV1{
			Start:      workrun.WorkStartContractV1,
			Status:     workrun.WorkStatusContractV1,
			Transition: workrun.WorkTransitionContractV1,
		},
		ConnectorSessionRef: runtimeContractRef("connector"),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	dormant := base
	dormant.WorkRouting.Exposure = WorkRoutingDormant
	dormant.ConnectorSessionRef = ""
	if err := dormant.Validate(); err != nil {
		t.Fatalf("dormant Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*RuntimeCapabilitiesV1)
		want   string
	}{
		{
			name: "advertised without connector",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.ConnectorSessionRef = ""
			},
			want: "connectorSessionRef",
		},
		{
			name: "dormant with connector",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.WorkRouting.Exposure = WorkRoutingDormant
			},
			want: "dormant",
		},
		{
			name: "routing drift",
			mutate: func(value *RuntimeCapabilitiesV1) {
				value.WorkRouting.ImplementationRouting.DirectInline.
					MaxUnderstandingFiles = 4
			},
			want: "canonical ACI",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			err := value.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDecodeOutcomeStartEnvelopeIsStrictAndBounded(t *testing.T) {
	t.Parallel()

	valid, err := decodeOutcomeStartEnvelope(
		[]byte(`{"outcome":"Fix the typo.","explicitSddRequested":true}`),
	)
	if err != nil {
		t.Fatalf("decodeOutcomeStartEnvelope() error = %v", err)
	}
	if valid.Outcome != "Fix the typo." || !valid.ExplicitSDDRequested {
		t.Fatalf("decoded request = %#v", valid)
	}

	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "unknown field",
			payload: `{"outcome":"Fix it.","route":"direct_main"}`,
			want:    "unknown field",
		},
		{
			name:    "duplicate",
			payload: `{"outcome":"Fix it.","outcome":"Replace it."}`,
			want:    "repeats field",
		},
		{
			name:    "trailing",
			payload: `{"outcome":"Fix it."}{}`,
			want:    "trailing JSON",
		},
		{
			name:    "non canonical outcome",
			payload: `{"outcome":" Fix it."}`,
			want:    "canonical outcome",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeOutcomeStartEnvelope([]byte(test.payload))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf(
					"decodeOutcomeStartEnvelope() error = %v, want %q",
					err,
					test.want,
				)
			}
		})
	}

	_, err = decodeOutcomeStartEnvelope(
		make([]byte, maximumOutcomeStartEnvelopeBytes+1),
	)
	if err == nil || !strings.Contains(err.Error(), "bounded") {
		t.Fatalf("oversize error = %v", err)
	}
}

func runtimeContractRef(seed string) string {
	return testPADRef("runtime-contract-" + seed)
}
