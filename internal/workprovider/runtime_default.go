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
	"sync"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
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

var ErrProductiveRuntimeInvocationAgentUnavailable = errors.New(
	"productive runtime invocation agent is unavailable",
)

var ErrProductiveRuntimeInvocationAgentInvalid = errors.New(
	"productive runtime invocation agent is invalid",
)

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
	return opener.openRuntimeOutcome(ctx, repo, "")
}

func (opener EnvironmentRuntimeOutcomeOpener) OpenRuntimeOutcomeForInvocation(
	ctx context.Context,
	repo string,
	agent model.AgentID,
) (RuntimeOutcome, error) {
	if agent != "" && !validProductiveRuntimeAgent(agent) {
		return nil, fmt.Errorf(
			"%w: unsupported agent %q",
			ErrProductiveRuntimeInvocationAgentInvalid,
			agent,
		)
	}
	return opener.openRuntimeOutcome(ctx, repo, agent)
}

func (opener EnvironmentRuntimeOutcomeOpener) OpenRuntimeOutcomeForLegacyCapabilities(
	ctx context.Context,
	repo string,
) (RuntimeOutcome, error) {
	agent, err := opener.resolveLegacyCapabilitiesAgent()
	if err != nil {
		return nil, err
	}
	return opener.openRuntimeOutcome(ctx, repo, agent)
}

func (opener EnvironmentRuntimeOutcomeOpener) openRuntimeOutcome(
	ctx context.Context,
	repo string,
	agent model.AgentID,
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
		openConnector: opener.openConnector,
	}
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

func (opener EnvironmentRuntimeOutcomeOpener) resolveLegacyCapabilitiesAgent() (
	model.AgentID,
	error,
) {
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
	openConnector func(
		context.Context,
		string,
		model.AgentID,
	) (ProductiveRuntimeConnector, error)
	connectorMu sync.Mutex
	connector   ProductiveRuntimeConnector
}

func (runtimeOutcome *productiveRuntimeOutcome) ensureConnector(
	ctx context.Context,
) (ProductiveRuntimeConnector, error) {
	if err := runtimeOutcome.validateBoundAgent(ctx); err != nil {
		return nil, err
	}
	runtimeOutcome.connectorMu.Lock()
	defer runtimeOutcome.connectorMu.Unlock()
	if runtimeOutcome.connector != nil {
		if runtimeOutcome.connector.RepositoryRef() !=
			runtimeOutcome.repositoryRef ||
			runtimeOutcome.connector.AgentID() != runtimeOutcome.agent {
			return nil, ErrProductiveRuntimeBindingMismatch
		}
		return runtimeOutcome.connector, nil
	}
	if runtimeOutcome.openConnector == nil {
		return nil, fmt.Errorf(
			"%w: missing lazy connector opener",
			ErrProductiveRuntimeUnavailable,
		)
	}
	connector, err := runtimeOutcome.openConnector(
		ctx,
		runtimeOutcome.repositoryRef,
		runtimeOutcome.agent,
	)
	if err != nil {
		return nil, err
	}
	runtimeOutcome.connector = connector
	return connector, nil
}

func (runtimeOutcome *productiveRuntimeOutcome) bindWorkRunAgent(
	ctx context.Context,
	workRunID string,
) (productiveStartAuthority, error) {
	if err := runtimeOutcome.validate(ctx); err != nil {
		return productiveStartAuthority{}, err
	}
	if !workRunIDPattern.MatchString(workRunID) {
		return productiveStartAuthority{}, errors.New(
			"productive runtime requires a canonical WorkRun identifier",
		)
	}
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(
		ctx,
		runtimeOutcome.repo,
	)
	if err != nil {
		return productiveStartAuthority{}, err
	}
	if err := lease.Validate(ctx); err != nil {
		return productiveStartAuthority{}, err
	}
	if lease.Identity().RepositoryRef != runtimeOutcome.repositoryRef {
		return productiveStartAuthority{},
			ErrProductiveRuntimeBindingMismatch
	}
	authority, _, err := resolveProductiveWorkRunBinding(
		ctx,
		lease,
		workRunID,
	)
	if err != nil {
		return productiveStartAuthority{}, err
	}
	runtimeOutcome.connectorMu.Lock()
	defer runtimeOutcome.connectorMu.Unlock()
	if runtimeOutcome.connector != nil &&
		(runtimeOutcome.connector.RepositoryRef() !=
			runtimeOutcome.repositoryRef ||
			runtimeOutcome.connector.AgentID() != authority.AgentID) {
		return productiveStartAuthority{},
			ErrProductiveRuntimeBindingMismatch
	}
	// Post-START identity is derived exclusively from immutable owner
	// authority. A legacy ambient assertion is never allowed to rebind it.
	runtimeOutcome.agent = authority.AgentID
	return authority, nil
}

func resolveProductiveWorkRunBinding(
	ctx context.Context,
	lease *reviewtransaction.RepositoryIdentityLease,
	workRunID string,
) (productiveStartAuthority, workrun.WorkRunState, error) {
	if lease == nil || !workRunIDPattern.MatchString(workRunID) {
		return productiveStartAuthority{}, workrun.WorkRunState{},
			ErrProductiveRuntimeBindingMismatch
	}
	store, err := existingProductiveAdvanceStore(lease, workRunID)
	if err != nil {
		return productiveStartAuthority{}, workrun.WorkRunState{}, err
	}
	authority, err := store.startAuthority(ctx)
	if err != nil {
		return productiveStartAuthority{}, workrun.WorkRunState{}, err
	}
	workStore, err := workrun.OpenWorkRunStoreWithRepositoryIdentityLease(
		ctx,
		lease,
		workRunID,
	)
	if err != nil {
		return productiveStartAuthority{}, workrun.WorkRunState{}, err
	}
	state, err := workStore.Status()
	if err != nil {
		return productiveStartAuthority{}, workrun.WorkRunState{}, err
	}
	if authority.RepositoryRef != lease.Identity().RepositoryRef ||
		authority.WorkRunID != workRunID ||
		state.WorkRunID != workRunID ||
		state.DeliveryIntentRef != authority.DeliveryIntentRef ||
		!validProductiveRuntimeAgent(authority.AgentID) {
		return productiveStartAuthority{}, workrun.WorkRunState{},
			ErrProductiveRuntimeBindingMismatch
	}
	return authority, state, nil
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
		if err := lease.Validate(ctx); err != nil {
			return nil, err
		}
		start, _, err := resolveProductiveWorkRunBinding(
			ctx,
			lease,
			workRunID,
		)
		if err != nil {
			return nil, err
		}
		evaluator = &productiveRuntimeOutcome{
			repo:          lease.Identity().RepositoryRoot,
			repositoryRef: lease.Identity().RepositoryRef,
			agent:         start.AgentID,
			activation:    activation,
			openConnector: opener.Runtime.openConnector,
		}
	}
	return (ProductionRepositoryOpener{
		SemanticEvaluator: evaluator,
	}).OpenRepository(ctx, repo, workRunID)
}

func (runtimeOutcome *productiveRuntimeOutcome) EvaluateSemanticExecution(
	ctx context.Context,
	ticket evidence.ActionTicket,
	process hostruntime.ProcessEvidence,
) (SemanticEvaluation, error) {
	connector, err := runtimeOutcome.ensureConnector(ctx)
	if err != nil {
		return SemanticEvaluation{}, err
	}
	return connector.EvaluateSemanticExecution(ctx, ticket, process)
}

func (runtimeOutcome *productiveRuntimeOutcome) Capabilities(
	ctx context.Context,
) (RuntimeCapabilitiesV1, error) {
	claim, _, err := runtimeOutcome.runtimeCapabilityClaim(ctx, false)
	if err != nil {
		return RuntimeCapabilitiesV1{}, err
	}
	capabilities := RuntimeCapabilitiesV1{
		Schema:        workrun.WorkCapabilitiesContractV1,
		Contract:      workrun.WorkCapabilitiesContractV1,
		RepositoryRef: runtimeOutcome.repositoryRef,
		AgentID:       runtimeOutcome.agent,
		WorkRouting:   claim,
		Contracts: RuntimeContractSetV1{
			Start:      workrun.WorkStartContractV1,
			Route:      workrun.WorkRouteContractV1,
			Advance:    workrun.WorkAdvanceContractV1,
			Reconcile:  workrun.WorkReconcileContractV1,
			Status:     workrun.WorkStatusContractV1,
			Transition: workrun.WorkTransitionContractV1,
		},
	}
	return capabilities, capabilities.Validate()
}

func (runtimeOutcome *productiveRuntimeOutcome) CapabilitiesV2(
	ctx context.Context,
) (RuntimeCapabilitiesV2, error) {
	claim, sessionRef, err := runtimeOutcome.runtimeCapabilityClaim(ctx, true)
	if err != nil {
		return RuntimeCapabilitiesV2{}, err
	}
	capabilities := RuntimeCapabilitiesV2{
		Schema:              RuntimeCapabilitiesContractV2,
		Contract:            RuntimeCapabilitiesContractV2,
		RepositoryRef:       runtimeOutcome.repositoryRef,
		AgentID:             runtimeOutcome.agent,
		WorkRouting:         claim,
		ConnectorSessionRef: sessionRef,
		Contracts: RuntimeContractSetV2{
			Start:              workrun.WorkStartContractV1,
			Route:              workrun.WorkRouteContractV1,
			Advance:            workrun.WorkAdvanceContractV2,
			VerificationDecide: workrun.WorkVerificationDecideContractV1,
			Reconcile:          workrun.WorkReconcileContractV1,
			Status:             workrun.WorkStatusContractV1,
			Transition:         workrun.WorkTransitionContractV1,
		},
	}
	return capabilities, capabilities.Validate()
}

func (runtimeOutcome *productiveRuntimeOutcome) runtimeCapabilityClaim(
	ctx context.Context,
	bindV2 bool,
) (RuntimeCapabilityClaimV1, string, error) {
	if err := runtimeOutcome.validate(ctx); err != nil {
		return RuntimeCapabilityClaimV1{}, "", err
	}
	claim := RuntimeCapabilityClaimV1{
		ID:                    workrun.WorkRoutingCapabilityV1,
		Exposure:              WorkRoutingDormant,
		ImplementationRouting: capabilitymanifest.CanonicalImplementationRouting(),
	}
	if !validProductiveRuntimeAgent(runtimeOutcome.agent) {
		if bindV2 && runtimeOutcome.agent == "" {
			return claim, "", nil
		}
		return RuntimeCapabilityClaimV1{}, "",
			ErrProductiveRuntimeInvocationAgentUnavailable
	}
	mode, err := runtimeOutcome.activation.ResolveActivation(
		ctx,
		runtimeOutcome.repo,
	)
	if err != nil {
		return RuntimeCapabilityClaimV1{}, "", err
	}
	manifest, err := effectiveAgentCapabilityManifest(
		runtimeOutcome.agent,
		mode,
	)
	if err != nil {
		return RuntimeCapabilityClaimV1{}, "", err
	}
	canonical, err := capabilitymanifest.ForAgent(runtimeOutcome.agent)
	if err != nil {
		return RuntimeCapabilityClaimV1{}, "", err
	}
	claim.ImplementationRouting = canonical.ImplementationRouting
	if !bindV2 ||
		!manifest.Advertises(capabilitymanifest.ContractWorkRoutingV1) {
		return claim, "", nil
	}
	connector, err := runtimeOutcome.ensureConnector(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return RuntimeCapabilityClaimV1{}, "", err
		}
		// Before START, any unavailable or unauthenticated composition is a
		// truthful dormant result. It cannot advertise or create authority.
		return claim, "", nil
	}
	handshake := connector.Handshake()
	if err := handshake.Validate(); err != nil {
		return RuntimeCapabilityClaimV1{}, "", err
	}
	if handshake.RepositoryRef != runtimeOutcome.repositoryRef ||
		handshake.AgentID != runtimeOutcome.agent {
		return RuntimeCapabilityClaimV1{}, "",
			ErrProductiveRuntimeBindingMismatch
	}
	claim.Exposure = WorkRoutingAdvertised
	return claim, handshake.ConnectorSessionRef, nil
}

func (runtimeOutcome *productiveRuntimeOutcome) StartOutcome(
	ctx context.Context,
	request OutcomeStartRequest,
) (workrun.WorkStatusV1, error) {
	if err := request.validate(); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	if err := runtimeOutcome.validateBoundAgent(ctx); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	capabilities, err := runtimeOutcome.CapabilitiesV2(ctx)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	if capabilities.WorkRouting.Exposure != WorkRoutingAdvertised {
		return workrun.WorkStatusV1{}, ErrCapabilityReadOnly
	}
	connector, err := runtimeOutcome.ensureConnector(ctx)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	factory, err := NewProductiveOwnerCoordinatorFactory(
		ctx,
		runtimeOutcome.repo,
		runtimeOutcome.agent,
		runtimeOutcome.activation,
		connector,
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
	snapshot, err := connector.ResolvePolicySnapshot(ctx)
	if err != nil {
		return workrun.WorkStatusV1{}, err
	}
	if err := provisioner.Provision(ctx, snapshot); err != nil {
		return workrun.WorkStatusV1{}, err
	}
	rechecked, err := runtimeOutcome.CapabilitiesV2(ctx)
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
		intake:  connector,
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
		runtimeOutcome.activation == nil {
		return ErrProductiveRuntimeUnavailable
	}
	return nil
}

func (runtimeOutcome *productiveRuntimeOutcome) validateBoundAgent(
	ctx context.Context,
) error {
	if err := runtimeOutcome.validate(ctx); err != nil {
		return err
	}
	if !validProductiveRuntimeAgent(runtimeOutcome.agent) {
		return ErrProductiveRuntimeInvocationAgentUnavailable
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
	_ RuntimeOutcomeOpener    = EnvironmentRuntimeOutcomeOpener{}
	_ RuntimeOutcome          = (*productiveRuntimeOutcome)(nil)
	_ ResumableRuntimeOutcome = (*productiveRuntimeOutcome)(nil)
	_ RepositoryOpener        = EnvironmentProductionRepositoryOpener{}
)
