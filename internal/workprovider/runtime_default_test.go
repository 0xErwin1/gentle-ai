package workprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/model"
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
	opened, err := opener.OpenRuntimeOutcome(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := opened.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
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

func TestEnvironmentRuntimeOutcomeOpenerAdvertisesOnlyBoundConnector(
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
		ProductiveRuntimeAgentEnvironment:     string(model.AgentCodex),
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
	opened, err := opener.OpenRuntimeOutcome(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := opened.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if received.RepositoryRef != authority.RepositoryRef() ||
		received.AgentID != model.AgentCodex ||
		received.EndpointURL != values[ProductiveRuntimeURLEnvironment] {
		t.Fatalf("connector config = %#v", received)
	}
	if capabilities.WorkRouting.Exposure != WorkRoutingAdvertised ||
		capabilities.ConnectorSessionRef != "session:runtime-default" {
		t.Fatalf("advertised capabilities = %#v", capabilities)
	}
}

func TestEnvironmentRuntimeOutcomeOpenerRejectsConnectorRebinding(
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
			ProductiveRuntimeAgentEnvironment:     string(model.AgentCodex),
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
	if _, err := opener.OpenRuntimeOutcome(
		context.Background(),
		repo,
	); !errors.Is(err, ErrProductiveRuntimeBindingMismatch) {
		t.Fatalf("connector rebinding error = %v", err)
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
			return connector, nil
		},
	}
	opened, err := opener.OpenRuntimeOutcome(ctx, repo)
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
}

func runtimeDefaultEnvironment(
	values map[string]string,
) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
