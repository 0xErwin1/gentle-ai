package workprovider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const (
	ProductiveRuntimeURLEnvironment       = "GENTLE_AI_PRODUCTIVE_RUNTIME_URL"
	ProductiveRuntimeTokenFileEnvironment = "GENTLE_AI_PRODUCTIVE_RUNTIME_TOKEN_FILE"
	ProductiveRuntimeCAFileEnvironment    = "GENTLE_AI_PRODUCTIVE_RUNTIME_CA_FILE"
	ProductiveRuntimeAgentEnvironment     = "GENTLE_AI_PRODUCTIVE_RUNTIME_AGENT"

	maximumProductiveRuntimeCABytes = 1 << 20
)

type productiveRuntimeConnectorFactory func(
	context.Context,
	ProductiveRuntimeConnectorConfig,
	string,
) (ProductiveRuntimeConnector, error)

// EnvironmentRuntimeOutcomeOpener is the shipped operator-controlled
// composition. The caller supplies only a repository path. Agent identity,
// activation, endpoint, trust roots, and credentials are fixed outside the
// outcome request and CLI flag surface.
type EnvironmentRuntimeOutcomeOpener struct {
	Activation       ActivationResolver
	LookupEnv        func(string) (string, bool)
	ConnectorFactory productiveRuntimeConnectorFactory
}

func NewDefaultRuntimeController() RuntimeController {
	return NewRuntimeController(EnvironmentRuntimeOutcomeOpener{})
}

func (opener EnvironmentRuntimeOutcomeOpener) OpenRuntimeOutcome(
	ctx context.Context,
	repo string,
) (RuntimeOutcome, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve productive runtime repository identity: %w",
			err,
		)
	}
	if err := lease.Validate(ctx); err != nil {
		return nil, fmt.Errorf(
			"validate productive runtime repository identity: %w",
			err,
		)
	}
	agent, err := opener.resolveAgent()
	if err != nil {
		return nil, err
	}
	activation := opener.activation()
	mode, err := activation.ResolveActivation(
		ctx,
		lease.Identity().RepositoryRoot,
	)
	if err != nil {
		return nil, err
	}
	if err := mode.Validate(); err != nil {
		return nil, err
	}
	runtimeOutcome := &productiveRuntimeOutcome{
		repo:          lease.Identity().RepositoryRoot,
		repositoryRef: lease.Identity().RepositoryRef,
		agent:         agent,
		activation:    activation,
	}
	if mode != ActivationEnabled {
		return runtimeOutcome, nil
	}
	connector, err := opener.openConnector(
		ctx,
		lease.Identity().RepositoryRef,
		agent,
	)
	if err != nil {
		return nil, err
	}
	runtimeOutcome.connector = connector
	return runtimeOutcome, nil
}

func (opener EnvironmentRuntimeOutcomeOpener) activation() ActivationResolver {
	if opener.Activation != nil {
		return opener.Activation
	}
	return EnvironmentActivationResolver{LookupEnv: opener.LookupEnv}
}

func (opener EnvironmentRuntimeOutcomeOpener) lookup(
	name string,
) (string, bool) {
	if opener.LookupEnv != nil {
		return opener.LookupEnv(name)
	}
	return os.LookupEnv(name)
}

func (opener EnvironmentRuntimeOutcomeOpener) resolveAgent() (model.AgentID, error) {
	value, exists := opener.lookup(ProductiveRuntimeAgentEnvironment)
	agent := model.AgentID(value)
	if !exists || !validProductiveRuntimeAgent(agent) {
		return "", fmt.Errorf(
			"%w: %s must name one supported agent",
			ErrProductiveRuntimeInvalidConfig,
			ProductiveRuntimeAgentEnvironment,
		)
	}
	return agent, nil
}

func (opener EnvironmentRuntimeOutcomeOpener) openConnector(
	ctx context.Context,
	repositoryRef string,
	agent model.AgentID,
) (ProductiveRuntimeConnector, error) {
	endpoint, endpointExists := opener.lookup(ProductiveRuntimeURLEnvironment)
	tokenPath, tokenExists := opener.lookup(
		ProductiveRuntimeTokenFileEnvironment,
	)
	if !endpointExists || endpoint == "" || !tokenExists || tokenPath == "" {
		return nil, fmt.Errorf(
			"%w: enabled work routing requires %s and %s",
			ErrProductiveRuntimeInvalidConfig,
			ProductiveRuntimeURLEnvironment,
			ProductiveRuntimeTokenFileEnvironment,
		)
	}
	token, err := readPrivateProductiveRuntimeToken(tokenPath)
	if err != nil {
		return nil, err
	}
	client, err := opener.httpClient()
	if err != nil {
		return nil, err
	}
	factory := opener.ConnectorFactory
	if factory == nil {
		factory = func(
			ctx context.Context,
			config ProductiveRuntimeConnectorConfig,
			token string,
		) (ProductiveRuntimeConnector, error) {
			return NewHTTPSProductiveRuntimeConnector(ctx, config, token)
		}
	}
	connector, err := factory(ctx, ProductiveRuntimeConnectorConfig{
		EndpointURL:   endpoint,
		RepositoryRef: repositoryRef,
		AgentID:       agent,
		HTTPClient:    client,
	}, token)
	if err != nil {
		return nil, err
	}
	if connector == nil ||
		connector.RepositoryRef() != repositoryRef ||
		connector.AgentID() != agent {
		return nil, fmt.Errorf(
			"%w: connector factory returned another repository or agent",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	if err := connector.Handshake().Validate(); err != nil {
		return nil, err
	}
	return connector, nil
}

func (opener EnvironmentRuntimeOutcomeOpener) httpClient() (*http.Client, error) {
	caPath, exists := opener.lookup(ProductiveRuntimeCAFileEnvironment)
	if !exists {
		return &http.Client{}, nil
	}
	if caPath == "" {
		return nil, fmt.Errorf(
			"%w: %s is explicitly empty",
			ErrProductiveRuntimeInvalidConfig,
			ProductiveRuntimeCAFileEnvironment,
		)
	}
	certificate, err := readBoundedProductiveRuntimeFile(
		caPath,
		maximumProductiveRuntimeCABytes,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: read runtime CA file: %v",
			ErrProductiveRuntimeInvalidConfig,
			err,
		)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(certificate) {
		return nil, fmt.Errorf(
			"%w: runtime CA file contains no certificates",
			ErrProductiveRuntimeInvalidConfig,
		)
	}
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
		},
	}}, nil
}

type productiveRuntimeOutcome struct {
	repo          string
	repositoryRef string
	agent         model.AgentID
	activation    ActivationResolver
	connector     ProductiveRuntimeConnector
}

// EnvironmentProductionRepositoryOpener gives status/apply the same
// authenticated semantic evaluator used by outcome start whenever productive
// routing is enabled. Dormant rollback modes preserve the existing reader and
// recovery composition without inventing a connector.
type EnvironmentProductionRepositoryOpener struct {
	Runtime EnvironmentRuntimeOutcomeOpener
}

func (opener EnvironmentProductionRepositoryOpener) OpenRepository(
	ctx context.Context,
	repo string,
	workRunID string,
) (Repository, error) {
	activation := opener.Runtime.activation()
	mode, err := activation.ResolveActivation(ctx, repo)
	if err != nil {
		return nil, err
	}
	if err := mode.Validate(); err != nil {
		return nil, err
	}
	var evaluator SemanticEvaluatorPort
	if mode == ActivationEnabled {
		lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
		if err != nil {
			return nil, err
		}
		agent, err := opener.Runtime.resolveAgent()
		if err != nil {
			return nil, err
		}
		connector, err := opener.Runtime.openConnector(
			ctx,
			lease.Identity().RepositoryRef,
			agent,
		)
		if err != nil {
			return nil, err
		}
		evaluator = connector
	}
	return (ProductionRepositoryOpener{
		SemanticEvaluator: evaluator,
	}).OpenRepository(ctx, repo, workRunID)
}

func (runtimeOutcome *productiveRuntimeOutcome) Capabilities(
	ctx context.Context,
) (RuntimeCapabilitiesV1, error) {
	if err := runtimeOutcome.validate(ctx); err != nil {
		return RuntimeCapabilitiesV1{}, err
	}
	mode, err := runtimeOutcome.activation.ResolveActivation(
		ctx,
		runtimeOutcome.repo,
	)
	if err != nil {
		return RuntimeCapabilitiesV1{}, err
	}
	manifest, err := effectiveAgentCapabilityManifest(
		runtimeOutcome.agent,
		mode,
	)
	if err != nil {
		return RuntimeCapabilitiesV1{}, err
	}
	canonical, err := capabilitymanifest.ForAgent(runtimeOutcome.agent)
	if err != nil {
		return RuntimeCapabilitiesV1{}, err
	}
	exposure := WorkRoutingDormant
	sessionRef := ""
	if manifest.Advertises(capabilitymanifest.ContractWorkRoutingV1) {
		if runtimeOutcome.connector == nil {
			return RuntimeCapabilitiesV1{}, fmt.Errorf(
				"%w: enabled work routing has no authenticated connector",
				ErrProductiveRuntimeUnavailable,
			)
		}
		handshake := runtimeOutcome.connector.Handshake()
		if err := handshake.Validate(); err != nil {
			return RuntimeCapabilitiesV1{}, err
		}
		if handshake.RepositoryRef != runtimeOutcome.repositoryRef ||
			handshake.AgentID != runtimeOutcome.agent {
			return RuntimeCapabilitiesV1{}, ErrProductiveRuntimeBindingMismatch
		}
		exposure = WorkRoutingAdvertised
		sessionRef = handshake.ConnectorSessionRef
	}
	capabilities := RuntimeCapabilitiesV1{
		Schema:        workrun.WorkCapabilitiesContractV1,
		Contract:      workrun.WorkCapabilitiesContractV1,
		RepositoryRef: runtimeOutcome.repositoryRef,
		AgentID:       runtimeOutcome.agent,
		WorkRouting: RuntimeCapabilityClaimV1{
			ID:                    workrun.WorkRoutingCapabilityV1,
			Exposure:              exposure,
			ImplementationRouting: canonical.ImplementationRouting,
		},
		Contracts: RuntimeContractSetV1{
			Start:      workrun.WorkStartContractV1,
			Advance:    workrun.WorkAdvanceContractV1,
			Status:     workrun.WorkStatusContractV1,
			Transition: workrun.WorkTransitionContractV1,
		},
		ConnectorSessionRef: sessionRef,
	}
	return capabilities, capabilities.Validate()
}

func (runtimeOutcome *productiveRuntimeOutcome) StartOutcome(
	ctx context.Context,
	request OutcomeStartRequest,
) (workrun.WorkStatusV1, error) {
	if err := request.validate(); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	capabilities, err := runtimeOutcome.Capabilities(ctx)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	if capabilities.WorkRouting.Exposure != WorkRoutingAdvertised ||
		runtimeOutcome.connector == nil {
		return workrun.WorkStatusV1{}, ErrCapabilityReadOnly
	}
	factory, err := NewProductiveOwnerCoordinatorFactory(
		ctx,
		runtimeOutcome.repo,
		runtimeOutcome.agent,
		runtimeOutcome.activation,
		runtimeOutcome.connector,
	)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	provisioner, err := NewProductivePolicyProvisioner(
		ctx,
		factory.padAuthority,
	)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	snapshot, err := runtimeOutcome.connector.ResolvePolicySnapshot(ctx)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	if err := provisioner.Provision(ctx, snapshot); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	rechecked, err := runtimeOutcome.Capabilities(ctx)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	if rechecked.WorkRouting.Exposure != WorkRoutingAdvertised ||
		rechecked.ConnectorSessionRef != capabilities.ConnectorSessionRef {
		return workrun.WorkStatusV1{}, errors.New(
			"productive runtime capability changed during policy provisioning",
		)
	}
	service := &OutcomeService{
		factory: factory,
		intake:  runtimeOutcome.connector,
	}
	return service.StartOutcome(ctx, request)
}

func (runtimeOutcome *productiveRuntimeOutcome) validate(
	ctx context.Context,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtimeOutcome == nil ||
		runtimeOutcome.repo == "" ||
		!validPADImmutableRef(runtimeOutcome.repositoryRef) ||
		!validProductiveRuntimeAgent(runtimeOutcome.agent) ||
		runtimeOutcome.activation == nil {
		return ErrProductiveRuntimeUnavailable
	}
	return nil
}

func readPrivateProductiveRuntimeToken(path string) (string, error) {
	payload, err := readBoundedProductiveRuntimeFile(
		path,
		maximumProductiveRuntimeTokenBytes+2,
		true,
	)
	if err != nil {
		return "", fmt.Errorf(
			"%w: read runtime token file: %v",
			ErrProductiveRuntimeInvalidConfig,
			err,
		)
	}
	payload = []byte(strings.TrimSuffix(string(payload), "\n"))
	payload = []byte(strings.TrimSuffix(string(payload), "\r"))
	token := string(payload)
	if !validProductiveRuntimeBearerToken(token) {
		return "", fmt.Errorf(
			"%w: runtime token file contains an invalid bearer credential",
			ErrProductiveRuntimeInvalidConfig,
		)
	}
	return token, nil
}

func readBoundedProductiveRuntimeFile(
	path string,
	limit int64,
	private bool,
) ([]byte, error) {
	if path == "" || path != strings.TrimSpace(path) ||
		strings.ContainsRune(path, '\x00') || limit < 1 {
		return nil, errors.New("runtime file path is invalid")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() ||
		before.Size() < 1 ||
		before.Size() > limit ||
		(private && runtime.GOOS != "windows" &&
			before.Mode().Perm()&0o077 != 0) {
		return nil, errors.New("runtime file is not a bounded private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil ||
		!opened.Mode().IsRegular() ||
		!os.SameFile(before, opened) {
		return nil, errors.New("runtime file changed while opening")
	}
	payload, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil ||
		int64(len(payload)) > limit ||
		!os.SameFile(opened, after) ||
		after.Size() != int64(len(payload)) {
		return nil, errors.New("runtime file changed while reading")
	}
	return payload, nil
}

var (
	_ RuntimeOutcomeOpener = EnvironmentRuntimeOutcomeOpener{}
	_ RuntimeOutcome       = (*productiveRuntimeOutcome)(nil)
	_ RepositoryOpener     = EnvironmentProductionRepositoryOpener{}
)
