package workprovider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type runtimeDefaultConnector struct {
	ProductiveRuntimeConnector
	repositoryRef string
	agent         model.AgentID
	sessionRef    string
	snapshot      ProductivePolicySnapshot
	intake        OwnerOutcomeIntake
	intakeCalls   int
}

func (connector runtimeDefaultConnector) RepositoryRef() string {
	return connector.repositoryRef
}

func (connector runtimeDefaultConnector) AgentID() model.AgentID {
	return connector.agent
}

func (connector runtimeDefaultConnector) ConnectorSessionRef() string {
	return connector.sessionRef
}

func (connector runtimeDefaultConnector) Handshake() ProductiveRuntimeHandshake {
	return ProductiveRuntimeHandshake{
		Schema:              ProductiveRuntimeHandshakeSchemaV1,
		RepositoryRef:       connector.repositoryRef,
		AgentID:             connector.agent,
		ConnectorSessionRef: connector.sessionRef,
		Operations:          ProductiveRuntimeOperationsV1(),
	}
}

func (connector *runtimeDefaultConnector) ResolvePolicySnapshot(
	context.Context,
) (ProductivePolicySnapshot, error) {
	return connector.snapshot, nil
}

func (connector *runtimeDefaultConnector) ResolveOutcomeIntake(
	_ context.Context,
	ownerContext OwnerOutcomeContext,
	request OutcomeStartRequest,
) (OwnerOutcomeIntake, error) {
	connector.intakeCalls++
	if ownerContext.RepositoryRef != connector.repositoryRef ||
		ownerContext.RepositoryRoot == "" {
		return OwnerOutcomeIntake{}, ErrProductiveRuntimeBindingMismatch
	}
	if err := request.validate(); err != nil {
		return OwnerOutcomeIntake{}, err
	}
	return connector.intake, nil
}

func TestEnvironmentRuntimeOutcomeOpenerKeepsDormantHandshakeReadOnly(
	t *testing.T,
) {
	repo := initPADAdapterGitRepository(t)
	called := false
	opener := EnvironmentRuntimeOutcomeOpener{
		Activation: StaticActivationResolver{Mode: ActivationReadOnly},
		LookupEnv: runtimeDefaultEnvironment(map[string]string{
			ProductiveRuntimeAgentEnvironment: string(model.AgentCodex),
		}),
		ConnectorFactory: func(
			context.Context,
			ProductiveRuntimeConnectorConfig,
			string,
		) (ProductiveRuntimeConnector, error) {
			called = true
			return nil, errors.New("must not open")
		},
	}
	result, err := NewRuntimeController(opener).Capabilities(
		context.Background(),
		RuntimeCapabilitiesRequest{
			Repo:     repo,
			Contract: workrun.WorkCapabilitiesContractV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Capabilities == nil {
		t.Fatal("legacy capabilities returned no dormant envelope")
	}
	capabilities := *result.Capabilities
	if called {
		t.Fatal("dormant capability opened the authenticated connector")
	}
	if capabilities.WorkRouting.Exposure != WorkRoutingDormant ||
		capabilities.ConnectorSessionRef != "" {
		t.Fatalf("dormant capabilities = %#v", capabilities)
	}
	if _, err := os.Stat(filepath.Join(
		repo,
		".git",
		"gentle-ai",
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dormant capability created provider storage: %v", err)
	}
}

func TestEnvironmentRuntimeOutcomeOpenerAdvertisesBoundConnectorOnlyInV2(
	t *testing.T,
) {
	repo := initPADAdapterGitRepository(t)
	authority, err := NewPADRepositoryAuthority(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(t.TempDir(), "runtime.token")
	if err := os.WriteFile(tokenPath, []byte("owner-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		ProductiveRuntimeAgentEnvironment:     string(model.AgentClaudeCode),
		ProductiveRuntimeURLEnvironment:       "https://runtime.invalid",
		ProductiveRuntimeTokenFileEnvironment: tokenPath,
	}
	var received ProductiveRuntimeConnectorConfig
	opener := EnvironmentRuntimeOutcomeOpener{
		Activation: StaticActivationResolver{Mode: ActivationEnabled},
		LookupEnv:  runtimeDefaultEnvironment(values),
		ConnectorFactory: func(
			_ context.Context,
			config ProductiveRuntimeConnectorConfig,
			token string,
		) (ProductiveRuntimeConnector, error) {
			received = config
			if token != "owner-secret" {
				t.Fatalf("token = %q", token)
			}
			return &runtimeDefaultConnector{
				repositoryRef: authority.RepositoryRef(),
				agent:         model.AgentCodex,
				sessionRef:    "session:runtime-default",
			}, nil
		},
	}
	opened, err := opener.OpenRuntimeOutcomeForInvocation(
		context.Background(),
		repo,
		model.AgentCodex,
	)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := opened.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if received != (ProductiveRuntimeConnectorConfig{}) {
		t.Fatal("capabilities/v1 opened the authenticated connector")
	}
	resumable, ok := opened.(ResumableRuntimeOutcome)
	if !ok {
		t.Fatal("productive runtime does not expose capabilities/v2")
	}
	capabilities, err := resumable.CapabilitiesV2(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if received.RepositoryRef != authority.RepositoryRef() ||
		received.AgentID != model.AgentCodex ||
		received.EndpointURL != values[ProductiveRuntimeURLEnvironment] {
		t.Fatalf("connector config = %#v", received)
	}
	if legacy.WorkRouting.Exposure != WorkRoutingDormant ||
		legacy.ConnectorSessionRef != "" {
		t.Fatalf("legacy capabilities were advertised = %#v", legacy)
	}
	if capabilities.WorkRouting.Exposure != WorkRoutingAdvertised ||
		capabilities.ConnectorSessionRef != "session:runtime-default" {
		t.Fatalf("advertised v2 capabilities = %#v", capabilities)
	}
}

func TestEnvironmentRuntimeOutcomeOpenerKeepsReboundConnectorDormant(
	t *testing.T,
) {
	repo := initPADAdapterGitRepository(t)
	tokenPath := filepath.Join(t.TempDir(), "runtime.token")
	if err := os.WriteFile(tokenPath, []byte("owner-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := EnvironmentRuntimeOutcomeOpener{
		Activation: StaticActivationResolver{Mode: ActivationEnabled},
		LookupEnv: runtimeDefaultEnvironment(map[string]string{
			ProductiveRuntimeAgentEnvironment:     string(model.AgentClaudeCode),
			ProductiveRuntimeURLEnvironment:       "https://runtime.invalid",
			ProductiveRuntimeTokenFileEnvironment: tokenPath,
		}),
		ConnectorFactory: func(
			context.Context,
			ProductiveRuntimeConnectorConfig,
			string,
		) (ProductiveRuntimeConnector, error) {
			return &runtimeDefaultConnector{
				repositoryRef: runtimeConnectorTestRef("foreign"),
				agent:         model.AgentCodex,
				sessionRef:    "session:runtime-default",
			}, nil
		},
	}
	opened, err := opener.OpenRuntimeOutcomeForInvocation(
		context.Background(),
		repo,
		model.AgentCodex,
	)
	if err != nil {
		t.Fatal(err)
	}
	resumable := opened.(ResumableRuntimeOutcome)
	capabilities, err := resumable.CapabilitiesV2(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.WorkRouting.Exposure != WorkRoutingDormant ||
		capabilities.ConnectorSessionRef != "" {
		t.Fatalf("rebound connector capabilities = %#v", capabilities)
	}
}

func TestEnvironmentRuntimeOutcomeOpenerReturnsIdentitylessDormantV2(
	t *testing.T,
) {
	repo := initPADAdapterGitRepository(t)
	called := false
	opener := EnvironmentRuntimeOutcomeOpener{
		Activation: StaticActivationResolver{Mode: ActivationEnabled},
		ConnectorFactory: func(
			context.Context,
			ProductiveRuntimeConnectorConfig,
			string,
		) (ProductiveRuntimeConnector, error) {
			called = true
			return nil, errors.New("must not open")
		},
	}
	result, err := NewRuntimeController(opener).CapabilitiesV2(
		context.Background(),
		RuntimeCapabilitiesRequest{
			Repo:     repo,
			Contract: RuntimeCapabilitiesContractV2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Capabilities == nil ||
		result.Capabilities.AgentID != "" ||
		result.Capabilities.WorkRouting.Exposure != WorkRoutingDormant ||
		result.Capabilities.ConnectorSessionRef != "" {
		t.Fatalf("identityless capabilities = %#v", result)
	}
	if called {
		t.Fatal("identityless capabilities opened the connector")
	}
	if _, err := os.Stat(filepath.Join(
		repo,
		".git",
		"gentle-ai",
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("identityless capabilities created provider storage: %v", err)
	}
}

func TestEnvironmentRuntimeOutcomeOpenerRejectsInvalidExplicitAgentBeforeIO(
	t *testing.T,
) {
	called := false
	opener := EnvironmentRuntimeOutcomeOpener{
		Activation: StaticActivationResolver{Mode: ActivationEnabled},
		ConnectorFactory: func(
			context.Context,
			ProductiveRuntimeConnectorConfig,
			string,
		) (ProductiveRuntimeConnector, error) {
			called = true
			return nil, errors.New("must not open")
		},
	}
	_, err := NewRuntimeController(opener).CapabilitiesV2(
		context.Background(),
		RuntimeCapabilitiesRequest{
			Repo:     filepath.Join(t.TempDir(), "missing-repository"),
			Contract: RuntimeCapabilitiesContractV2,
			AgentID:  model.AgentID("unsupported-agent"),
		},
	)
	if !errors.Is(err, ErrProductiveRuntimeInvocationAgentInvalid) {
		t.Fatalf("invalid explicit agent error = %v", err)
	}
	if called {
		t.Fatal("invalid explicit agent opened the connector")
	}
}

func TestEnvironmentRuntimeOutcomeStartWithoutAgentCreatesNoProviderStorage(
	t *testing.T,
) {
	repo := initPADAdapterGitRepository(t)
	called := false
	opener := EnvironmentRuntimeOutcomeOpener{
		Activation: StaticActivationResolver{Mode: ActivationEnabled},
		ConnectorFactory: func(
			context.Context,
			ProductiveRuntimeConnectorConfig,
			string,
		) (ProductiveRuntimeConnector, error) {
			called = true
			return nil, errors.New("must not open")
		},
	}
	_, err := opener.OpenRuntimeOutcomeForInvocation(
		context.Background(),
		repo,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewRuntimeController(opener).Start(
		context.Background(),
		RuntimeStartRequest{
			Repo:     repo,
			Contract: workrun.WorkStartContractV1,
			Payload: []byte(
				`{"outcome":"Change one file.","explicitSddRequested":false}`,
			),
		},
	)
	if !errors.Is(err, ErrProductiveRuntimeInvocationAgentUnavailable) {
		t.Fatalf("missing start agent error = %v", err)
	}
	if called {
		t.Fatal("missing start agent opened the connector")
	}
	if _, err := os.Stat(filepath.Join(
		repo,
		".git",
		"gentle-ai",
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing start agent created provider storage: %v", err)
	}
}

func TestPrivateProductiveRuntimeTokenRejectsWeakOrReplacedFiles(
	t *testing.T,
) {
	root := t.TempDir()
	weak := filepath.Join(root, "weak.token")
	if err := os.WriteFile(weak, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if _, err := readPrivateProductiveRuntimeToken(
			weak,
		); !errors.Is(err, ErrProductiveRuntimeInvalidConfig) {
			t.Fatalf("weak token error = %v", err)
		}
	}
	private := filepath.Join(root, "private.token")
	if err := os.WriteFile(private, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := readPrivateProductiveRuntimeToken(private)
	if err != nil || token != "secret" {
		t.Fatalf("private token = %q, %v", token, err)
	}
	link := filepath.Join(root, "link.token")
	if err := os.Symlink(private, link); err == nil {
		if _, err := readPrivateProductiveRuntimeToken(
			link,
		); !errors.Is(err, ErrProductiveRuntimeInvalidConfig) {
			t.Fatalf("symlink token error = %v", err)
		}
	}
}

func TestEnvironmentRuntimeOutcomeStartsProductiveOutcomeWithoutSDD(
	t *testing.T,
) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	authority, err := NewPADRepositoryAuthority(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(t.TempDir(), "runtime.token")
	if err := os.WriteFile(tokenPath, []byte("owner-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	const sessionRef = "session:runtime-default-start"
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
	connector := &runtimeDefaultConnector{
		repositoryRef: authority.RepositoryRef(),
		agent:         model.AgentCodex,
		sessionRef:    sessionRef,
		snapshot:      snapshot,
		intake: ownerOutcomeTestIntake(
			"runtime-default-direct",
			deliveryadmission.RoutePRWithoutIssue,
		),
	}
	connectorOpenCalls := 0
	opener := EnvironmentRuntimeOutcomeOpener{
		Activation: StaticActivationResolver{Mode: ActivationEnabled},
		LookupEnv: runtimeDefaultEnvironment(map[string]string{
			ProductiveRuntimeAgentEnvironment:     string(model.AgentCodex),
			ProductiveRuntimeURLEnvironment:       "https://runtime.invalid",
			ProductiveRuntimeTokenFileEnvironment: tokenPath,
		}),
		ConnectorFactory: func(
			_ context.Context,
			config ProductiveRuntimeConnectorConfig,
			token string,
		) (ProductiveRuntimeConnector, error) {
			if config.RepositoryRef != authority.RepositoryRef() ||
				config.AgentID != model.AgentCodex ||
				token != "owner-secret" {
				t.Fatalf("connector config/token = %#v/%q", config, token)
			}
			connectorOpenCalls++
			return connector, nil
		},
	}
	opened, err := opener.OpenRuntimeOutcomeForInvocation(
		ctx,
		repo,
		model.AgentCodex,
	)
	if err != nil {
		t.Fatal(err)
	}
	status, err := opened.StartOutcome(ctx, OutcomeStartRequest{
		Outcome: "Apply one already-understood mechanical change.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if connector.intakeCalls != 1 {
		t.Fatalf("outcome intake calls = %d, want 1", connector.intakeCalls)
	}
	if status.WorkRunID != connector.intake.WorkRunID ||
		status.RouteDecision != workrun.RouteDecisionDirectInline ||
		status.ImplementationRoute != workrun.ImplementationRouteDirectInline ||
		status.SDDRunRef != "" ||
		status.DeliveryIntentRef == "" {
		t.Fatalf("productive start status = %#v", status)
	}
	if err := status.Validate(); err != nil {
		t.Fatalf("productive start status validation = %v", err)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	store, err := existingProductiveAdvanceStore(lease, status.WorkRunID)
	if err != nil {
		t.Fatal(err)
	}
	start, err := store.startAuthority(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if start.AgentID != model.AgentCodex {
		t.Fatalf("durable START agent = %q", start.AgentID)
	}
	workStore, err := workrun.OpenWorkRunStoreWithRepositoryIdentityLease(
		ctx,
		lease,
		status.WorkRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	orphaned := workStore.Dir + ".orphaned"
	if err := os.Rename(workStore.Dir, orphaned); err != nil {
		t.Fatal(err)
	}
	if _, err := (EnvironmentProductionRepositoryOpener{
		Runtime: opener,
	}).OpenRepository(
		ctx,
		repo,
		status.WorkRunID,
	); err == nil {
		t.Fatal("orphan START opened a production repository")
	}
	if connectorOpenCalls != 1 {
		t.Fatalf(
			"orphan START connector opens = %d, want 1",
			connectorOpenCalls,
		)
	}
	if err := os.Rename(orphaned, workStore.Dir); err != nil {
		t.Fatal(err)
	}

	start.DeliveryIntentRef = productiveAdvanceSHA256(
		[]byte("tampered delivery intent"),
	)
	payload, err := json.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(
		filepath.Join(store.root, "start-authority.json"),
		payload,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := (EnvironmentProductionRepositoryOpener{
		Runtime: opener,
	}).OpenRepository(
		ctx,
		repo,
		status.WorkRunID,
	); !errors.Is(err, ErrProductiveRuntimeBindingMismatch) {
		t.Fatalf("tampered START binding error = %v", err)
	}
	if connectorOpenCalls != 1 {
		t.Fatalf(
			"tampered START connector opens = %d, want 1",
			connectorOpenCalls,
		)
	}
}

func runtimeDefaultEnvironment(
	values map[string]string,
) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
