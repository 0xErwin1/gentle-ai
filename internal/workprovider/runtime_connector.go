package workprovider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/model"
)

const (
	ProductiveRuntimeBootstrapRequestSchemaV1  = "gentle-ai.productive-runtime-bootstrap-request/v1"
	ProductiveRuntimeBootstrapResponseSchemaV1 = "gentle-ai.productive-runtime-bootstrap-response/v1"
	ProductiveRuntimeHandshakeSchemaV1         = "gentle-ai.productive-runtime-handshake/v1"
	ProductiveRuntimeRequestSchemaV1           = "gentle-ai.productive-runtime-request/v1"
	ProductiveRuntimeResponseSchemaV1          = "gentle-ai.productive-runtime-response/v1"
	ProductivePolicySnapshotSchemaV1           = "gentle-ai.productive-policy-snapshot/v1"

	ProductiveRuntimeBootstrapPathV1 = "/v1/bootstrap"
	ProductiveRuntimeCallPathV1      = "/v1/call"

	defaultProductiveRuntimeRequestTimeout = 15 * time.Second
	defaultProductiveRuntimeResponseBytes  = int64(1 << 20)
	maximumProductiveRuntimeResponseBytes  = int64(16 << 20)
	maximumProductiveRuntimeRequestBytes   = 4 << 20
	maximumProductiveRuntimeTokenBytes     = 16 << 10
)

var (
	ErrProductiveRuntimeUnavailable = errors.New(
		"productive runtime connector is unavailable",
	)
	ErrProductiveRuntimeInvalidConfig = errors.New(
		"productive runtime connector configuration is invalid",
	)
	ErrProductiveRuntimeRedirect = errors.New(
		"productive runtime connector redirects are forbidden",
	)
	ErrProductiveRuntimeHTTPStatus = errors.New(
		"productive runtime connector returned a non-success status",
	)
	ErrProductiveRuntimeResponseTooLarge = errors.New(
		"productive runtime connector response exceeds its limit",
	)
	ErrProductiveRuntimeMalformedResponse = errors.New(
		"productive runtime connector returned malformed JSON",
	)
	ErrProductiveRuntimeBindingMismatch = errors.New(
		"productive runtime connector response binding mismatch",
	)
)

// ProductiveRuntimeOperation is a closed set negotiated during the
// authenticated bootstrap. It exposes no arbitrary endpoint, method, or
// payload selection to callers.
type ProductiveRuntimeOperation string

const (
	ProductiveRuntimeOperationPolicySnapshot      ProductiveRuntimeOperation = "policy_snapshot"
	ProductiveRuntimeOperationOutcomeIntake       ProductiveRuntimeOperation = "outcome_intake"
	ProductiveRuntimeOperationVerificationCatalog ProductiveRuntimeOperation = "verification_catalog"
	ProductiveRuntimeOperationSemantic            ProductiveRuntimeOperation = "semantic_evaluation"
	ProductiveRuntimeOperationReview              ProductiveRuntimeOperation = "review_evaluation"
	ProductiveRuntimeOperationPADGitBinding       ProductiveRuntimeOperation = "pad_git_binding"
	ProductiveRuntimeOperationObserveDelivery     ProductiveRuntimeOperation = "observe_delivery"
	ProductiveRuntimeOperationBranchCAS           ProductiveRuntimeOperation = "branch_cas"
	ProductiveRuntimeOperationMergePR             ProductiveRuntimeOperation = "merge_pull_request"
)

var productiveRuntimeOperationsV1 = []ProductiveRuntimeOperation{
	ProductiveRuntimeOperationPolicySnapshot,
	ProductiveRuntimeOperationOutcomeIntake,
	ProductiveRuntimeOperationVerificationCatalog,
	ProductiveRuntimeOperationSemantic,
	ProductiveRuntimeOperationReview,
	ProductiveRuntimeOperationPADGitBinding,
	ProductiveRuntimeOperationObserveDelivery,
	ProductiveRuntimeOperationBranchCAS,
	ProductiveRuntimeOperationMergePR,
}

var productivePolicyRoutesV1 = []deliveryadmission.Route{
	deliveryadmission.RoutePRWithIssue,
	deliveryadmission.RoutePRWithoutIssue,
	deliveryadmission.RouteDirectMain,
	deliveryadmission.RouteEmergency,
}

// ProductiveRuntimeOperationsV1 returns the exact operation set required by
// ProductiveRuntimeConnector. The returned slice is defensive.
func ProductiveRuntimeOperationsV1() []ProductiveRuntimeOperation {
	return append([]ProductiveRuntimeOperation(nil), productiveRuntimeOperationsV1...)
}

// ProductiveRuntimeConnectorConfig contains only owner-supplied connection and
// identity facts. The bearer token is deliberately a separate constructor
// argument so it is not embedded in a printable configuration value.
type ProductiveRuntimeConnectorConfig struct {
	EndpointURL      string
	RepositoryRef    string
	AgentID          model.AgentID
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	MaxResponseBytes int64
}

// ProductiveRuntimeHandshake is the immutable result used by effective
// capability advertisement. ConnectorSessionRef is issued by the authenticated
// peer and bound to the exact repository and agent in every subsequent call.
type ProductiveRuntimeHandshake struct {
	Schema              string                       `json:"schema"`
	RepositoryRef       string                       `json:"repositoryRef"`
	AgentID             model.AgentID                `json:"agentId"`
	ConnectorSessionRef string                       `json:"connectorSessionRef"`
	Operations          []ProductiveRuntimeOperation `json:"operations"`
}

func (handshake ProductiveRuntimeHandshake) Validate() error {
	if handshake.Schema != ProductiveRuntimeHandshakeSchemaV1 ||
		!validPADImmutableRef(handshake.RepositoryRef) ||
		!validProductiveRuntimeAgent(handshake.AgentID) ||
		!validProductiveRuntimeToken(handshake.ConnectorSessionRef) ||
		!exactProductiveRuntimeOperations(handshake.Operations) {
		return fmt.Errorf(
			"%w: invalid authenticated handshake",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	return nil
}

// ProductivePolicySnapshot binds a monotonically increasing owner policy view
// to the exact authenticated connector session. Equal revisions are accepted
// only when SnapshotRef and content are identical; regressions fail closed.
type ProductivePolicySnapshot struct {
	Schema              string                          `json:"schema"`
	RepositoryRef       string                          `json:"repositoryRef"`
	AgentID             model.AgentID                   `json:"agentId"`
	ConnectorSessionRef string                          `json:"connectorSessionRef"`
	Revision            uint64                          `json:"revision"`
	Policies            []deliveryadmission.RoutePolicy `json:"policies"`
	SnapshotRef         string                          `json:"snapshotRef"`
}

type productivePolicySnapshotProjection struct {
	Schema              string                          `json:"schema"`
	RepositoryRef       string                          `json:"repositoryRef"`
	AgentID             model.AgentID                   `json:"agentId"`
	ConnectorSessionRef string                          `json:"connectorSessionRef"`
	Revision            uint64                          `json:"revision"`
	Policies            []deliveryadmission.RoutePolicy `json:"policies"`
}

// NewProductivePolicySnapshot constructs the canonical wire snapshot expected
// from a connector authority. It requires one policy for every PAD route and
// sorts those policies into PAD's canonical route order before binding.
func NewProductivePolicySnapshot(
	repositoryRef string,
	agentID model.AgentID,
	connectorSessionRef string,
	revision uint64,
	policies []deliveryadmission.RoutePolicy,
) (ProductivePolicySnapshot, error) {
	canonicalPolicies := append(
		[]deliveryadmission.RoutePolicy(nil),
		policies...,
	)
	sort.Slice(canonicalPolicies, func(left, right int) bool {
		return productivePolicyRouteIndex(canonicalPolicies[left].Route) <
			productivePolicyRouteIndex(canonicalPolicies[right].Route)
	})
	snapshot := ProductivePolicySnapshot{
		Schema:              ProductivePolicySnapshotSchemaV1,
		RepositoryRef:       repositoryRef,
		AgentID:             agentID,
		ConnectorSessionRef: connectorSessionRef,
		Revision:            revision,
		Policies:            canonicalPolicies,
	}
	ref, err := productiveRuntimeDigest(snapshot.projection())
	if err != nil {
		return ProductivePolicySnapshot{}, err
	}
	snapshot.SnapshotRef = ref
	if err := snapshot.Validate(
		repositoryRef,
		agentID,
		connectorSessionRef,
	); err != nil {
		return ProductivePolicySnapshot{}, err
	}
	return snapshot, nil
}

func (snapshot ProductivePolicySnapshot) projection() productivePolicySnapshotProjection {
	return productivePolicySnapshotProjection{
		Schema:              snapshot.Schema,
		RepositoryRef:       snapshot.RepositoryRef,
		AgentID:             snapshot.AgentID,
		ConnectorSessionRef: snapshot.ConnectorSessionRef,
		Revision:            snapshot.Revision,
		Policies: append(
			[]deliveryadmission.RoutePolicy(nil),
			snapshot.Policies...,
		),
	}
}

func (snapshot ProductivePolicySnapshot) Validate(
	repositoryRef string,
	agentID model.AgentID,
	connectorSessionRef string,
) error {
	if snapshot.Schema != ProductivePolicySnapshotSchemaV1 ||
		snapshot.RepositoryRef != repositoryRef ||
		snapshot.AgentID != agentID ||
		snapshot.ConnectorSessionRef != connectorSessionRef ||
		snapshot.Revision == 0 {
		return fmt.Errorf(
			"%w: invalid productive policy snapshot header",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	if err := validateProductivePolicySet(
		snapshot.Revision,
		snapshot.Policies,
	); err != nil {
		return err
	}
	want, err := productiveRuntimeDigest(snapshot.projection())
	if err != nil {
		return err
	}
	if snapshot.SnapshotRef != want {
		return fmt.Errorf(
			"%w: productive policy snapshot digest mismatch",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	return nil
}

func validateProductivePolicySet(
	revision uint64,
	policies []deliveryadmission.RoutePolicy,
) error {
	if revision == 0 ||
		len(policies) != len(productivePolicyRoutesV1) {
		return fmt.Errorf(
			"%w: productive policy set is incomplete",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	wantRevision := fmt.Sprintf("snapshot:%d", revision)
	for index, policy := range policies {
		if err := policy.Validate(); err != nil {
			return fmt.Errorf(
				"%w: invalid productive route policy: %v",
				ErrProductiveRuntimeBindingMismatch,
				err,
			)
		}
		if policy.Route != productivePolicyRoutesV1[index] ||
			policy.Revision != wantRevision {
			return fmt.Errorf(
				"%w: productive policy route or revision is not canonical",
				ErrProductiveRuntimeBindingMismatch,
			)
		}
	}
	return nil
}

func productivePolicyRouteIndex(route deliveryadmission.Route) int {
	for index, expected := range productivePolicyRoutesV1 {
		if route == expected {
			return index
		}
	}
	return len(productivePolicyRoutesV1)
}

// ProductiveRuntimeConnector is the complete authenticated authority boundary
// consumed by productive composition. Existing validation adapters still wrap
// its PAD binding and hosting ports.
type ProductiveRuntimeConnector interface {
	OwnerOutcomeIntakeAuthority
	SemanticEvaluatorPort
	PADGitBindingAuthority
	HostingAuthority

	ResolvePolicySnapshot(context.Context) (ProductivePolicySnapshot, error)
	RepositoryRef() string
	AgentID() model.AgentID
	ConnectorSessionRef() string
	Handshake() ProductiveRuntimeHandshake
}

// ProductiveRuntimeBootstrapRequest is the authenticated bootstrap preimage.
type ProductiveRuntimeBootstrapRequest struct {
	Schema        string        `json:"schema"`
	RepositoryRef string        `json:"repositoryRef"`
	AgentID       model.AgentID `json:"agentId"`
	Nonce         string        `json:"nonce"`
	RequestDigest string        `json:"requestDigest"`
}

type productiveRuntimeBootstrapRequestProjection struct {
	Schema        string        `json:"schema"`
	RepositoryRef string        `json:"repositoryRef"`
	AgentID       model.AgentID `json:"agentId"`
	Nonce         string        `json:"nonce"`
}

func (request ProductiveRuntimeBootstrapRequest) projection() productiveRuntimeBootstrapRequestProjection {
	return productiveRuntimeBootstrapRequestProjection{
		Schema:        request.Schema,
		RepositoryRef: request.RepositoryRef,
		AgentID:       request.AgentID,
		Nonce:         request.Nonce,
	}
}

func (request ProductiveRuntimeBootstrapRequest) Validate() error {
	if request.Schema != ProductiveRuntimeBootstrapRequestSchemaV1 ||
		!validPADImmutableRef(request.RepositoryRef) ||
		!validProductiveRuntimeAgent(request.AgentID) ||
		!validProductiveRuntimeNonce(request.Nonce) {
		return fmt.Errorf(
			"%w: invalid bootstrap request",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	want, err := productiveRuntimeDigest(request.projection())
	if err != nil {
		return err
	}
	if request.RequestDigest != want {
		return fmt.Errorf(
			"%w: bootstrap request digest mismatch",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	return nil
}

// ProductiveRuntimeBootstrapResponse binds the server-issued session and exact
// supported operation set to one bootstrap request.
type ProductiveRuntimeBootstrapResponse struct {
	Schema              string                       `json:"schema"`
	RepositoryRef       string                       `json:"repositoryRef"`
	AgentID             model.AgentID                `json:"agentId"`
	Nonce               string                       `json:"nonce"`
	RequestDigest       string                       `json:"requestDigest"`
	ConnectorSessionRef string                       `json:"connectorSessionRef"`
	Operations          []ProductiveRuntimeOperation `json:"operations"`
	ResponseDigest      string                       `json:"responseDigest"`
}

type productiveRuntimeBootstrapResponseProjection struct {
	Schema              string                       `json:"schema"`
	RepositoryRef       string                       `json:"repositoryRef"`
	AgentID             model.AgentID                `json:"agentId"`
	Nonce               string                       `json:"nonce"`
	RequestDigest       string                       `json:"requestDigest"`
	ConnectorSessionRef string                       `json:"connectorSessionRef"`
	Operations          []ProductiveRuntimeOperation `json:"operations"`
}

func (response ProductiveRuntimeBootstrapResponse) projection() productiveRuntimeBootstrapResponseProjection {
	return productiveRuntimeBootstrapResponseProjection{
		Schema:              response.Schema,
		RepositoryRef:       response.RepositoryRef,
		AgentID:             response.AgentID,
		Nonce:               response.Nonce,
		RequestDigest:       response.RequestDigest,
		ConnectorSessionRef: response.ConnectorSessionRef,
		Operations: append(
			[]ProductiveRuntimeOperation(nil),
			response.Operations...,
		),
	}
}

// NewProductiveRuntimeBootstrapResponse is provided for typed connector
// authorities and deterministic conformance servers.
func NewProductiveRuntimeBootstrapResponse(
	request ProductiveRuntimeBootstrapRequest,
	connectorSessionRef string,
) (ProductiveRuntimeBootstrapResponse, error) {
	if err := request.Validate(); err != nil {
		return ProductiveRuntimeBootstrapResponse{}, err
	}
	response := ProductiveRuntimeBootstrapResponse{
		Schema:              ProductiveRuntimeBootstrapResponseSchemaV1,
		RepositoryRef:       request.RepositoryRef,
		AgentID:             request.AgentID,
		Nonce:               request.Nonce,
		RequestDigest:       request.RequestDigest,
		ConnectorSessionRef: connectorSessionRef,
		Operations:          ProductiveRuntimeOperationsV1(),
	}
	if !validProductiveRuntimeToken(connectorSessionRef) {
		return ProductiveRuntimeBootstrapResponse{}, fmt.Errorf(
			"%w: invalid connector session",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	digest, err := productiveRuntimeDigest(response.projection())
	if err != nil {
		return ProductiveRuntimeBootstrapResponse{}, err
	}
	response.ResponseDigest = digest
	return response, nil
}

func (response ProductiveRuntimeBootstrapResponse) ValidateFor(
	request ProductiveRuntimeBootstrapRequest,
) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if response.Schema != ProductiveRuntimeBootstrapResponseSchemaV1 ||
		response.RepositoryRef != request.RepositoryRef ||
		response.AgentID != request.AgentID ||
		response.Nonce != request.Nonce ||
		response.RequestDigest != request.RequestDigest ||
		!validProductiveRuntimeToken(response.ConnectorSessionRef) ||
		!exactProductiveRuntimeOperations(response.Operations) {
		return fmt.Errorf(
			"%w: bootstrap response does not match request",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	want, err := productiveRuntimeDigest(response.projection())
	if err != nil {
		return err
	}
	if response.ResponseDigest != want {
		return fmt.Errorf(
			"%w: bootstrap response digest mismatch",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	return nil
}

// ProductiveRuntimeRequest is the closed typed RPC envelope. Payload is always
// produced from one method-specific Go type by HTTPSProductiveRuntimeConnector.
type ProductiveRuntimeRequest struct {
	Schema              string                     `json:"schema"`
	Operation           ProductiveRuntimeOperation `json:"operation"`
	RepositoryRef       string                     `json:"repositoryRef"`
	AgentID             model.AgentID              `json:"agentId"`
	ConnectorSessionRef string                     `json:"connectorSessionRef"`
	Nonce               string                     `json:"nonce"`
	Payload             json.RawMessage            `json:"payload"`
	RequestDigest       string                     `json:"requestDigest"`
}

type productiveRuntimeRequestProjection struct {
	Schema              string                     `json:"schema"`
	Operation           ProductiveRuntimeOperation `json:"operation"`
	RepositoryRef       string                     `json:"repositoryRef"`
	AgentID             model.AgentID              `json:"agentId"`
	ConnectorSessionRef string                     `json:"connectorSessionRef"`
	Nonce               string                     `json:"nonce"`
	Payload             json.RawMessage            `json:"payload"`
}

func (request ProductiveRuntimeRequest) projection() productiveRuntimeRequestProjection {
	return productiveRuntimeRequestProjection{
		Schema:              request.Schema,
		Operation:           request.Operation,
		RepositoryRef:       request.RepositoryRef,
		AgentID:             request.AgentID,
		ConnectorSessionRef: request.ConnectorSessionRef,
		Nonce:               request.Nonce,
		Payload:             append(json.RawMessage(nil), request.Payload...),
	}
}

func (request ProductiveRuntimeRequest) Validate() error {
	if request.Schema != ProductiveRuntimeRequestSchemaV1 ||
		!knownProductiveRuntimeOperation(request.Operation) ||
		!validPADImmutableRef(request.RepositoryRef) ||
		!validProductiveRuntimeAgent(request.AgentID) ||
		!validProductiveRuntimeToken(request.ConnectorSessionRef) ||
		!validProductiveRuntimeNonce(request.Nonce) ||
		len(request.Payload) == 0 ||
		!json.Valid(request.Payload) {
		return fmt.Errorf(
			"%w: invalid productive runtime request",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	want, err := productiveRuntimeDigest(request.projection())
	if err != nil {
		return err
	}
	if request.RequestDigest != want {
		return fmt.Errorf(
			"%w: productive runtime request digest mismatch",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	return nil
}

// ProductiveRuntimeResponse binds one typed payload to the exact request
// operation, repository, agent, session, nonce, and digest.
type ProductiveRuntimeResponse struct {
	Schema              string                     `json:"schema"`
	Operation           ProductiveRuntimeOperation `json:"operation"`
	RepositoryRef       string                     `json:"repositoryRef"`
	AgentID             model.AgentID              `json:"agentId"`
	ConnectorSessionRef string                     `json:"connectorSessionRef"`
	Nonce               string                     `json:"nonce"`
	RequestDigest       string                     `json:"requestDigest"`
	Payload             json.RawMessage            `json:"payload"`
	ResponseDigest      string                     `json:"responseDigest"`
}

type productiveRuntimeResponseProjection struct {
	Schema              string                     `json:"schema"`
	Operation           ProductiveRuntimeOperation `json:"operation"`
	RepositoryRef       string                     `json:"repositoryRef"`
	AgentID             model.AgentID              `json:"agentId"`
	ConnectorSessionRef string                     `json:"connectorSessionRef"`
	Nonce               string                     `json:"nonce"`
	RequestDigest       string                     `json:"requestDigest"`
	Payload             json.RawMessage            `json:"payload"`
}

func (response ProductiveRuntimeResponse) projection() productiveRuntimeResponseProjection {
	return productiveRuntimeResponseProjection{
		Schema:              response.Schema,
		Operation:           response.Operation,
		RepositoryRef:       response.RepositoryRef,
		AgentID:             response.AgentID,
		ConnectorSessionRef: response.ConnectorSessionRef,
		Nonce:               response.Nonce,
		RequestDigest:       response.RequestDigest,
		Payload:             append(json.RawMessage(nil), response.Payload...),
	}
}

// NewProductiveRuntimeResponse builds an exact response for a typed connector
// authority or deterministic conformance server.
func NewProductiveRuntimeResponse(
	request ProductiveRuntimeRequest,
	payload any,
) (ProductiveRuntimeResponse, error) {
	if err := request.Validate(); err != nil {
		return ProductiveRuntimeResponse{}, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ProductiveRuntimeResponse{}, err
	}
	response := ProductiveRuntimeResponse{
		Schema:              ProductiveRuntimeResponseSchemaV1,
		Operation:           request.Operation,
		RepositoryRef:       request.RepositoryRef,
		AgentID:             request.AgentID,
		ConnectorSessionRef: request.ConnectorSessionRef,
		Nonce:               request.Nonce,
		RequestDigest:       request.RequestDigest,
		Payload:             encoded,
	}
	response.ResponseDigest, err = productiveRuntimeDigest(response.projection())
	if err != nil {
		return ProductiveRuntimeResponse{}, err
	}
	return response, nil
}

func (response ProductiveRuntimeResponse) ValidateFor(
	request ProductiveRuntimeRequest,
) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if response.Schema != ProductiveRuntimeResponseSchemaV1 ||
		response.Operation != request.Operation ||
		response.RepositoryRef != request.RepositoryRef ||
		response.AgentID != request.AgentID ||
		response.ConnectorSessionRef != request.ConnectorSessionRef ||
		response.Nonce != request.Nonce ||
		response.RequestDigest != request.RequestDigest ||
		len(response.Payload) == 0 ||
		!json.Valid(response.Payload) {
		return fmt.Errorf(
			"%w: productive runtime response does not match request",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	want, err := productiveRuntimeDigest(response.projection())
	if err != nil {
		return err
	}
	if response.ResponseDigest != want {
		return fmt.Errorf(
			"%w: productive runtime response digest mismatch",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	return nil
}

type productiveOutcomeIntakeRequest struct {
	Context OwnerOutcomeContext `json:"context"`
	Request OutcomeStartRequest `json:"request"`
}

type productiveSemanticEvaluationRequest struct {
	Ticket  evidence.ActionTicket       `json:"ticket"`
	Process hostruntime.ProcessEvidence `json:"process"`
}

type productivePADGitBindingRequest struct {
	Candidate   deliveryadmission.CandidateBinding   `json:"candidate"`
	Destination deliveryadmission.DestinationBinding `json:"destination"`
	Mechanism   deliveryadmission.Mechanism          `json:"mechanism"`
}

// HTTPSProductiveRuntimeConnector is an immutable authenticated client. It
// never selects a generic URL, method, argv, Git force mode, or fallback.
type HTTPSProductiveRuntimeConnector struct {
	endpoint         *url.URL
	bearerToken      string
	client           *http.Client
	requestTimeout   time.Duration
	maxResponseBytes int64

	handshake ProductiveRuntimeHandshake

	policyMu          sync.Mutex
	policyRevision    uint64
	policySnapshotRef string
}

func NewHTTPSProductiveRuntimeConnector(
	ctx context.Context,
	config ProductiveRuntimeConnectorConfig,
	bearerToken string,
) (*HTTPSProductiveRuntimeConnector, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoint, timeout, responseLimit, client, err := validateProductiveRuntimeConfig(
		config,
		bearerToken,
	)
	if err != nil {
		return nil, err
	}
	connector := &HTTPSProductiveRuntimeConnector{
		endpoint:         endpoint,
		bearerToken:      bearerToken,
		client:           client,
		requestTimeout:   timeout,
		maxResponseBytes: responseLimit,
	}
	request, err := newProductiveRuntimeBootstrapRequest(
		config.RepositoryRef,
		config.AgentID,
	)
	if err != nil {
		return nil, err
	}
	var response ProductiveRuntimeBootstrapResponse
	if err := connector.exchange(
		ctx,
		ProductiveRuntimeBootstrapPathV1,
		request,
		&response,
	); err != nil {
		return nil, fmt.Errorf(
			"%w: bootstrap: %w",
			ErrProductiveRuntimeUnavailable,
			err,
		)
	}
	if err := response.ValidateFor(request); err != nil {
		return nil, err
	}
	connector.handshake = ProductiveRuntimeHandshake{
		Schema:              ProductiveRuntimeHandshakeSchemaV1,
		RepositoryRef:       response.RepositoryRef,
		AgentID:             response.AgentID,
		ConnectorSessionRef: response.ConnectorSessionRef,
		Operations: append(
			[]ProductiveRuntimeOperation(nil),
			response.Operations...,
		),
	}
	if err := connector.handshake.Validate(); err != nil {
		return nil, err
	}
	return connector, nil
}

func (connector *HTTPSProductiveRuntimeConnector) RepositoryRef() string {
	if connector == nil {
		return ""
	}
	return connector.handshake.RepositoryRef
}

func (connector *HTTPSProductiveRuntimeConnector) AgentID() model.AgentID {
	if connector == nil {
		return ""
	}
	return connector.handshake.AgentID
}

func (connector *HTTPSProductiveRuntimeConnector) ConnectorSessionRef() string {
	if connector == nil {
		return ""
	}
	return connector.handshake.ConnectorSessionRef
}

func (connector *HTTPSProductiveRuntimeConnector) Handshake() ProductiveRuntimeHandshake {
	if connector == nil {
		return ProductiveRuntimeHandshake{}
	}
	handshake := connector.handshake
	handshake.Operations = append(
		[]ProductiveRuntimeOperation(nil),
		connector.handshake.Operations...,
	)
	return handshake
}

func (connector *HTTPSProductiveRuntimeConnector) ResolvePolicySnapshot(
	ctx context.Context,
) (ProductivePolicySnapshot, error) {
	var snapshot ProductivePolicySnapshot
	if err := connector.call(
		ctx,
		ProductiveRuntimeOperationPolicySnapshot,
		struct{}{},
		&snapshot,
	); err != nil {
		return ProductivePolicySnapshot{}, err
	}
	if err := snapshot.Validate(
		connector.RepositoryRef(),
		connector.AgentID(),
		connector.ConnectorSessionRef(),
	); err != nil {
		return ProductivePolicySnapshot{}, err
	}
	connector.policyMu.Lock()
	defer connector.policyMu.Unlock()
	if snapshot.Revision < connector.policyRevision ||
		(snapshot.Revision == connector.policyRevision &&
			connector.policySnapshotRef != "" &&
			snapshot.SnapshotRef != connector.policySnapshotRef) {
		return ProductivePolicySnapshot{}, fmt.Errorf(
			"%w: productive policy snapshot revision regressed or forked",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	connector.policyRevision = snapshot.Revision
	connector.policySnapshotRef = snapshot.SnapshotRef
	return snapshot, nil
}

func (connector *HTTPSProductiveRuntimeConnector) ResolveOutcomeIntake(
	ctx context.Context,
	ownerContext OwnerOutcomeContext,
	request OutcomeStartRequest,
) (OwnerOutcomeIntake, error) {
	if err := connector.ready(ctx); err != nil {
		return OwnerOutcomeIntake{}, err
	}
	if ownerContext.RepositoryRef != connector.RepositoryRef() {
		return OwnerOutcomeIntake{}, fmt.Errorf(
			"%w: outcome intake repository mismatch",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	if ownerContext.RepositoryRoot == "" ||
		ownerContext.RepositoryRoot != strings.TrimSpace(ownerContext.RepositoryRoot) ||
		len(ownerContext.RepositoryRoot) > maximumProductiveRuntimeTokenBytes ||
		strings.ContainsRune(ownerContext.RepositoryRoot, '\x00') {
		return OwnerOutcomeIntake{}, fmt.Errorf(
			"%w: invalid owner repository root",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	if err := request.validate(); err != nil {
		return OwnerOutcomeIntake{}, err
	}
	var intake OwnerOutcomeIntake
	if err := connector.call(
		ctx,
		ProductiveRuntimeOperationOutcomeIntake,
		productiveOutcomeIntakeRequest{
			Context: ownerContext,
			Request: request,
		},
		&intake,
	); err != nil {
		return OwnerOutcomeIntake{}, err
	}
	if err := intake.validateStructure(
		connector.RepositoryRef(),
		request,
	); err != nil {
		return OwnerOutcomeIntake{}, fmt.Errorf(
			"%w: %v",
			ErrProductiveRuntimeBindingMismatch,
			err,
		)
	}
	return intake, nil
}

func (connector *HTTPSProductiveRuntimeConnector) EvaluateSemanticExecution(
	ctx context.Context,
	ticket evidence.ActionTicket,
	process hostruntime.ProcessEvidence,
) (SemanticEvaluation, error) {
	if err := connector.ready(ctx); err != nil {
		return SemanticEvaluation{}, err
	}
	if err := ticket.Validate(); err != nil {
		return SemanticEvaluation{}, err
	}
	if err := ticket.RequestBinding.ValidateProcessEvidence(process); err != nil {
		return SemanticEvaluation{}, err
	}
	var evaluation SemanticEvaluation
	if err := connector.call(
		ctx,
		ProductiveRuntimeOperationSemantic,
		productiveSemanticEvaluationRequest{
			Ticket:  ticket,
			Process: process,
		},
		&evaluation,
	); err != nil {
		return SemanticEvaluation{}, err
	}
	if err := evaluation.validateFor(ticket, process); err != nil {
		return SemanticEvaluation{}, fmt.Errorf(
			"%w: %v",
			ErrProductiveRuntimeBindingMismatch,
			err,
		)
	}
	return evaluation, nil
}

func (connector *HTTPSProductiveRuntimeConnector) ResolvePADGitBinding(
	ctx context.Context,
	candidate deliveryadmission.CandidateBinding,
	destination deliveryadmission.DestinationBinding,
	mechanism deliveryadmission.Mechanism,
) (PADGitBinding, error) {
	if err := connector.ready(ctx); err != nil {
		return PADGitBinding{}, err
	}
	if destination.RepositoryRef != connector.RepositoryRef() ||
		!validPADCandidateBinding(candidate) ||
		!validPADDestinationBinding(destination) ||
		(mechanism != deliveryadmission.MechanismFastForwardOnly &&
			mechanism != deliveryadmission.MechanismPullRequest) {
		return PADGitBinding{}, fmt.Errorf(
			"%w: invalid PAD Git binding request",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	var binding PADGitBinding
	if err := connector.call(
		ctx,
		ProductiveRuntimeOperationPADGitBinding,
		productivePADGitBindingRequest{
			Candidate:   candidate,
			Destination: destination,
			Mechanism:   mechanism,
		},
		&binding,
	); err != nil {
		return PADGitBinding{}, err
	}
	if err := binding.Validate(candidate, destination, mechanism); err != nil {
		return PADGitBinding{}, fmt.Errorf(
			"%w: %v",
			ErrProductiveRuntimeBindingMismatch,
			err,
		)
	}
	return binding, nil
}

func (connector *HTTPSProductiveRuntimeConnector) ObserveDelivery(
	ctx context.Context,
	request HostingObservationRequest,
) (HostingDeliveryObservation, error) {
	if err := connector.ready(ctx); err != nil {
		return HostingDeliveryObservation{}, err
	}
	if request.Binding.Destination.RepositoryRef != connector.RepositoryRef() {
		return HostingDeliveryObservation{}, fmt.Errorf(
			"%w: hosting observation repository mismatch",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	if err := request.Validate(); err != nil {
		return HostingDeliveryObservation{}, err
	}
	var observation HostingDeliveryObservation
	if err := connector.call(
		ctx,
		ProductiveRuntimeOperationObserveDelivery,
		request,
		&observation,
	); err != nil {
		return HostingDeliveryObservation{}, err
	}
	if err := observation.Validate(request); err != nil {
		return HostingDeliveryObservation{}, fmt.Errorf(
			"%w: %v",
			ErrProductiveRuntimeBindingMismatch,
			err,
		)
	}
	return observation, nil
}

func (connector *HTTPSProductiveRuntimeConnector) CompareAndSwapBranch(
	ctx context.Context,
	request HostingBranchCASRequest,
) (HostingBranchCASReceipt, error) {
	if err := connector.ready(ctx); err != nil {
		return HostingBranchCASReceipt{}, err
	}
	if request.RepositoryRef != connector.RepositoryRef() {
		return HostingBranchCASReceipt{}, fmt.Errorf(
			"%w: branch CAS repository mismatch",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	if err := request.Validate(); err != nil {
		return HostingBranchCASReceipt{}, err
	}
	var receipt HostingBranchCASReceipt
	if err := connector.call(
		ctx,
		ProductiveRuntimeOperationBranchCAS,
		request,
		&receipt,
	); err != nil {
		return HostingBranchCASReceipt{}, err
	}
	if err := receipt.Validate(request); err != nil {
		return HostingBranchCASReceipt{}, fmt.Errorf(
			"%w: %v",
			ErrProductiveRuntimeBindingMismatch,
			err,
		)
	}
	return receipt, nil
}

func (connector *HTTPSProductiveRuntimeConnector) MergePullRequest(
	ctx context.Context,
	request HostingPullRequestMergeRequest,
) (HostingPullRequestMergeReceipt, error) {
	if err := connector.ready(ctx); err != nil {
		return HostingPullRequestMergeReceipt{}, err
	}
	if request.RepositoryRef != connector.RepositoryRef() {
		return HostingPullRequestMergeReceipt{}, fmt.Errorf(
			"%w: pull-request merge repository mismatch",
			ErrProductiveRuntimeBindingMismatch,
		)
	}
	if err := request.Validate(); err != nil {
		return HostingPullRequestMergeReceipt{}, err
	}
	var receipt HostingPullRequestMergeReceipt
	if err := connector.call(
		ctx,
		ProductiveRuntimeOperationMergePR,
		request,
		&receipt,
	); err != nil {
		return HostingPullRequestMergeReceipt{}, err
	}
	if err := receipt.Validate(request); err != nil {
		return HostingPullRequestMergeReceipt{}, fmt.Errorf(
			"%w: %v",
			ErrProductiveRuntimeBindingMismatch,
			err,
		)
	}
	return receipt, nil
}

func (connector *HTTPSProductiveRuntimeConnector) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if connector == nil ||
		connector.endpoint == nil ||
		connector.client == nil ||
		connector.bearerToken == "" {
		return ErrProductiveRuntimeUnavailable
	}
	return connector.handshake.Validate()
}

func (connector *HTTPSProductiveRuntimeConnector) call(
	ctx context.Context,
	operation ProductiveRuntimeOperation,
	payload any,
	output any,
) error {
	if err := connector.ready(ctx); err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode productive runtime request: %w", err)
	}
	nonce, err := newProductiveRuntimeNonce()
	if err != nil {
		return err
	}
	request := ProductiveRuntimeRequest{
		Schema:              ProductiveRuntimeRequestSchemaV1,
		Operation:           operation,
		RepositoryRef:       connector.RepositoryRef(),
		AgentID:             connector.AgentID(),
		ConnectorSessionRef: connector.ConnectorSessionRef(),
		Nonce:               nonce,
		Payload:             encoded,
	}
	request.RequestDigest, err = productiveRuntimeDigest(request.projection())
	if err != nil {
		return err
	}
	if err := request.Validate(); err != nil {
		return err
	}
	var response ProductiveRuntimeResponse
	if err := connector.exchange(
		ctx,
		ProductiveRuntimeCallPathV1,
		request,
		&response,
	); err != nil {
		return fmt.Errorf(
			"%w: %s: %w",
			ErrProductiveRuntimeUnavailable,
			operation,
			err,
		)
	}
	if err := response.ValidateFor(request); err != nil {
		return err
	}
	if err := decodeStrictProductiveRuntimeJSON(response.Payload, output); err != nil {
		return err
	}
	return nil
}

func (connector *HTTPSProductiveRuntimeConnector) exchange(
	ctx context.Context,
	path string,
	request any,
	response any,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode productive runtime envelope: %w", err)
	}
	if len(payload) == 0 || len(payload) > maximumProductiveRuntimeRequestBytes {
		return fmt.Errorf(
			"%w: request exceeds its fixed limit",
			ErrProductiveRuntimeInvalidConfig,
		)
	}
	requestContext, cancel := context.WithTimeout(ctx, connector.requestTimeout)
	defer cancel()
	endpoint := *connector.endpoint
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawPath = ""
	httpRequest, err := http.NewRequestWithContext(
		requestContext,
		http.MethodPost,
		endpoint.String(),
		bytes.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("create productive runtime request: %w", err)
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+connector.bearerToken)
	httpRequest.Header.Set("User-Agent", "gentle-ai-productive-runtime/v1")
	httpResponse, err := connector.client.Do(httpRequest)
	if err != nil {
		if requestContext.Err() != nil {
			return requestContext.Err()
		}
		if errors.Is(err, ErrProductiveRuntimeRedirect) {
			return ErrProductiveRuntimeRedirect
		}
		return fmt.Errorf("perform productive runtime request: %w", err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < http.StatusOK ||
		httpResponse.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf(
			"%w: HTTP %d",
			ErrProductiveRuntimeHTTPStatus,
			httpResponse.StatusCode,
		)
	}
	mediaType, _, err := mime.ParseMediaType(
		httpResponse.Header.Get("Content-Type"),
	)
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf(
			"%w: response content type is not application/json",
			ErrProductiveRuntimeMalformedResponse,
		)
	}
	if httpResponse.ContentLength > connector.maxResponseBytes {
		return ErrProductiveRuntimeResponseTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(
		httpResponse.Body,
		connector.maxResponseBytes+1,
	))
	if err != nil {
		return fmt.Errorf(
			"%w: response body read failed",
			ErrProductiveRuntimeMalformedResponse,
		)
	}
	if int64(len(body)) > connector.maxResponseBytes {
		return ErrProductiveRuntimeResponseTooLarge
	}
	if err := decodeStrictProductiveRuntimeJSON(body, response); err != nil {
		return err
	}
	return nil
}

func validateProductiveRuntimeConfig(
	config ProductiveRuntimeConnectorConfig,
	bearerToken string,
) (*url.URL, time.Duration, int64, *http.Client, error) {
	endpoint, err := url.Parse(config.EndpointURL)
	if err != nil ||
		endpoint.Scheme != "https" ||
		endpoint.Host == "" ||
		endpoint.User != nil ||
		endpoint.RawQuery != "" ||
		endpoint.Fragment != "" ||
		endpoint.Opaque != "" {
		return nil, 0, 0, nil, fmt.Errorf(
			"%w: endpoint must be an absolute HTTPS URL without credentials, query, or fragment",
			ErrProductiveRuntimeInvalidConfig,
		)
	}
	if !validPADImmutableRef(config.RepositoryRef) ||
		!validProductiveRuntimeAgent(config.AgentID) {
		return nil, 0, 0, nil, fmt.Errorf(
			"%w: repository or agent binding is invalid",
			ErrProductiveRuntimeInvalidConfig,
		)
	}
	if !validProductiveRuntimeBearerToken(bearerToken) {
		return nil, 0, 0, nil, fmt.Errorf(
			"%w: bearer credential is invalid",
			ErrProductiveRuntimeInvalidConfig,
		)
	}
	timeout := config.RequestTimeout
	if timeout == 0 {
		timeout = defaultProductiveRuntimeRequestTimeout
	}
	if timeout < time.Millisecond || timeout > 2*time.Minute {
		return nil, 0, 0, nil, fmt.Errorf(
			"%w: request timeout is outside the supported range",
			ErrProductiveRuntimeInvalidConfig,
		)
	}
	responseLimit := config.MaxResponseBytes
	if responseLimit == 0 {
		responseLimit = defaultProductiveRuntimeResponseBytes
	}
	if responseLimit < 1 ||
		responseLimit > maximumProductiveRuntimeResponseBytes {
		return nil, 0, 0, nil, fmt.Errorf(
			"%w: response limit is outside the supported range",
			ErrProductiveRuntimeInvalidConfig,
		)
	}
	baseClient := config.HTTPClient
	if baseClient == nil {
		baseClient = &http.Client{}
	}
	client := *baseClient
	client.Timeout = timeout
	client.CheckRedirect = func(
		_ *http.Request,
		_ []*http.Request,
	) error {
		return ErrProductiveRuntimeRedirect
	}
	return endpoint, timeout, responseLimit, &client, nil
}

func newProductiveRuntimeBootstrapRequest(
	repositoryRef string,
	agentID model.AgentID,
) (ProductiveRuntimeBootstrapRequest, error) {
	nonce, err := newProductiveRuntimeNonce()
	if err != nil {
		return ProductiveRuntimeBootstrapRequest{}, err
	}
	request := ProductiveRuntimeBootstrapRequest{
		Schema:        ProductiveRuntimeBootstrapRequestSchemaV1,
		RepositoryRef: repositoryRef,
		AgentID:       agentID,
		Nonce:         nonce,
	}
	request.RequestDigest, err = productiveRuntimeDigest(request.projection())
	if err != nil {
		return ProductiveRuntimeBootstrapRequest{}, err
	}
	return request, request.Validate()
}

func newProductiveRuntimeNonce() (string, error) {
	var nonce [32]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return "", fmt.Errorf(
			"%w: generate request nonce",
			ErrProductiveRuntimeUnavailable,
		)
	}
	return "nonce:" + hex.EncodeToString(nonce[:]), nil
}

func productiveRuntimeDigest(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode productive runtime digest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func decodeStrictProductiveRuntimeJSON(payload []byte, output any) error {
	if len(payload) == 0 || output == nil {
		return ErrProductiveRuntimeMalformedResponse
	}
	if err := rejectProductiveRuntimeDuplicateJSONFields(payload); err != nil {
		return ErrProductiveRuntimeMalformedResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return ErrProductiveRuntimeMalformedResponse
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrProductiveRuntimeMalformedResponse
	}
	return nil
}

func rejectProductiveRuntimeDuplicateJSONFields(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := consumeProductiveRuntimeJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func consumeProductiveRuntimeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("JSON object field is not a string")
			}
			folded := foldProductiveRuntimeJSONName(name)
			if _, duplicate := seen[folded]; duplicate {
				return errors.New(
					"JSON object contains a duplicate field",
				)
			}
			seen[folded] = struct{}{}
			if err := consumeProductiveRuntimeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("JSON object is truncated")
		}
	case '[':
		for decoder.More() {
			if err := consumeProductiveRuntimeJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("JSON array is truncated")
		}
	default:
		return errors.New("JSON contains an unexpected delimiter")
	}
	return nil
}

// foldProductiveRuntimeJSONName returns the same equivalence classes as
// strings.EqualFold and encoding/json's folded field-name lookup, but in a
// stable form suitable for an O(1) duplicate-key set.
func foldProductiveRuntimeJSONName(name string) string {
	folded := make([]byte, 0, len(name))
	for len(name) > 0 {
		if character := name[0]; character < utf8.RuneSelf {
			if 'a' <= character && character <= 'z' {
				character -= 'a' - 'A'
			}
			folded = append(folded, character)
			name = name[1:]
			continue
		}
		character, size := utf8.DecodeRuneInString(name)
		folded = utf8.AppendRune(
			folded,
			foldProductiveRuntimeJSONRune(character),
		)
		name = name[size:]
	}
	return string(folded)
}

func foldProductiveRuntimeJSONRune(character rune) rune {
	for {
		next := unicode.SimpleFold(character)
		if next <= character {
			return next
		}
		character = next
	}
}

func exactProductiveRuntimeOperations(
	operations []ProductiveRuntimeOperation,
) bool {
	if len(operations) != len(productiveRuntimeOperationsV1) {
		return false
	}
	for index := range productiveRuntimeOperationsV1 {
		if operations[index] != productiveRuntimeOperationsV1[index] {
			return false
		}
	}
	return true
}

func knownProductiveRuntimeOperation(
	operation ProductiveRuntimeOperation,
) bool {
	for _, expected := range productiveRuntimeOperationsV1 {
		if operation == expected {
			return true
		}
	}
	return false
}

func validProductiveRuntimeAgent(agentID model.AgentID) bool {
	switch agentID {
	case model.AgentClaudeCode,
		model.AgentOpenCode,
		model.AgentKilocode,
		model.AgentGeminiCLI,
		model.AgentCursor,
		model.AgentVSCodeCopilot,
		model.AgentCodex,
		model.AgentAntigravity,
		model.AgentWindsurf,
		model.AgentKimi,
		model.AgentQwenCode,
		model.AgentKiroIDE,
		model.AgentOpenClaw,
		model.AgentPi,
		model.AgentTrae,
		model.AgentHermes:
		return true
	default:
		return false
	}
}

func validProductiveRuntimeBearerToken(token string) bool {
	if token == "" ||
		len(token) > maximumProductiveRuntimeTokenBytes ||
		token != strings.TrimSpace(token) {
		return false
	}
	for _, character := range token {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func validProductiveRuntimeToken(token string) bool {
	return validProductiveRuntimeBearerToken(token)
}

func validProductiveRuntimeNonce(nonce string) bool {
	if len(nonce) != len("nonce:")+64 ||
		!strings.HasPrefix(nonce, "nonce:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(nonce, "nonce:"))
	return err == nil
}

var (
	_ ProductiveRuntimeConnector  = (*HTTPSProductiveRuntimeConnector)(nil)
	_ OwnerOutcomeIntakeAuthority = (*HTTPSProductiveRuntimeConnector)(nil)
	_ SemanticEvaluatorPort       = (*HTTPSProductiveRuntimeConnector)(nil)
	_ PADGitBindingAuthority      = (*HTTPSProductiveRuntimeConnector)(nil)
	_ HostingAuthority            = (*HTTPSProductiveRuntimeConnector)(nil)
)
