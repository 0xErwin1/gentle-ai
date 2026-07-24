package workprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

// maximumOutcomeStartEnvelopeBytes is a transport cap, not the semantic
// outcome limit. Twelve bytes per code point admits the largest valid JSON
// representation: one supplementary Unicode scalar escaped as a surrogate
// pair, plus bounded envelope overhead.
const maximumOutcomeStartEnvelopeBytes = 12*maximumOutcomeRequestCodePoints + 1024

// WorkRoutingExposure is the effective runtime exposure returned by the
// authenticated capability handshake. It is deliberately separate from the
// immutable dormant ACI manifest.
type WorkRoutingExposure string

const (
	WorkRoutingDormant    WorkRoutingExposure = "dormant"
	WorkRoutingAdvertised WorkRoutingExposure = "advertised"
)

// RuntimeCapabilityClaimV1 is the exact common-work capability negotiated by a
// runtime consumer. ImplementationRouting is descriptive ACI data; it never
// authorizes or selects a route.
type RuntimeCapabilityClaimV1 struct {
	ID                    string                                        `json:"id"`
	Exposure              WorkRoutingExposure                           `json:"exposure"`
	ImplementationRouting capabilitymanifest.ImplementationRoutingFacts `json:"implementationRouting"`
}

// RuntimeContractSetV1 prevents consumers from inferring support from version,
// command presence, fields, or prose.
type RuntimeContractSetV1 struct {
	Start      string `json:"start"`
	Route      string `json:"route"`
	Advance    string `json:"advance"`
	Reconcile  string `json:"reconcile"`
	Status     string `json:"status"`
	Transition string `json:"transition"`
}

// RuntimeCapabilitiesV1 is the repository-bound result of the effective ACI
// handshake. An advertised result additionally binds the authenticated
// connector session that made productive owner composition available.
type RuntimeCapabilitiesV1 struct {
	Schema              string                   `json:"schema"`
	Contract            string                   `json:"contract"`
	RepositoryRef       string                   `json:"repositoryRef"`
	AgentID             model.AgentID            `json:"agentId"`
	WorkRouting         RuntimeCapabilityClaimV1 `json:"workRouting"`
	Contracts           RuntimeContractSetV1     `json:"contracts"`
	ConnectorSessionRef string                   `json:"connectorSessionRef,omitempty"`
}

func (capabilities RuntimeCapabilitiesV1) Validate() error {
	if capabilities.Schema != workrun.WorkCapabilitiesContractV1 ||
		capabilities.Contract != workrun.WorkCapabilitiesContractV1 {
		return errors.New(
			"runtime capabilities must use gentle-ai.work-capabilities/v1",
		)
	}
	if err := validateCanonical(
		"runtime capabilities repositoryRef",
		capabilities.RepositoryRef,
	); err != nil {
		return err
	}
	canonical, err := capabilitymanifest.ForAgent(capabilities.AgentID)
	if err != nil {
		return err
	}
	if capabilities.WorkRouting.ID != workrun.WorkRoutingCapabilityV1 ||
		capabilities.WorkRouting.ImplementationRouting !=
			canonical.ImplementationRouting {
		return errors.New("runtime capabilities do not match canonical ACI routing")
	}
	if capabilities.Contracts != (RuntimeContractSetV1{
		Start:      workrun.WorkStartContractV1,
		Route:      workrun.WorkRouteContractV1,
		Advance:    workrun.WorkAdvanceContractV1,
		Reconcile:  workrun.WorkReconcileContractV1,
		Status:     workrun.WorkStatusContractV1,
		Transition: workrun.WorkTransitionContractV1,
	}) {
		return errors.New("runtime capabilities contain an unsupported contract set")
	}
	switch capabilities.WorkRouting.Exposure {
	case WorkRoutingAdvertised:
		if err := validateCanonical(
			"runtime capabilities connectorSessionRef",
			capabilities.ConnectorSessionRef,
		); err != nil {
			return err
		}
	case WorkRoutingDormant:
		if capabilities.ConnectorSessionRef != "" {
			return errors.New(
				"dormant runtime capabilities cannot bind a connector session",
			)
		}
	default:
		return fmt.Errorf(
			"unsupported work-routing exposure %q",
			capabilities.WorkRouting.Exposure,
		)
	}
	return nil
}

// RuntimeOutcome is the narrow productive use case consumed by the machine
// contracts. The normal caller can only negotiate capability or submit the
// outcome-first request.
type RuntimeOutcome interface {
	Capabilities(context.Context) (RuntimeCapabilitiesV1, error)
	StartOutcome(context.Context, OutcomeStartRequest) (workrun.WorkStatusV1, error)
	DecideRoute(
		context.Context,
		string,
		string,
		workrun.WorkRouteChoice,
	) (workrun.WorkRouteV1, error)
	BindSDDRoute(
		context.Context,
		string,
		string,
		string,
	) (workrun.WorkRouteV1, error)
	AdvanceOutcome(context.Context, string, string) (workrun.WorkAdvanceV1, error)
	ReconcileOutcome(
		context.Context,
		string,
		string,
		string,
	) (workrun.WorkReconcileV1, error)
}

type RuntimeOutcomeOpener interface {
	OpenRuntimeOutcome(context.Context, string) (RuntimeOutcome, error)
}

// RuntimeController keeps contract negotiation ahead of repository access,
// connector bootstrap, payload validation, and mutation.
type RuntimeController struct {
	opener RuntimeOutcomeOpener
}

func NewRuntimeController(opener RuntimeOutcomeOpener) RuntimeController {
	return RuntimeController{opener: opener}
}

type RuntimeCapabilitiesRequest struct {
	Repo     string
	Contract string
}

type RuntimeCapabilitiesResult struct {
	Capabilities *RuntimeCapabilitiesV1
	Diagnostic   *workrun.WorkDiagnosticV1
}

func (result RuntimeCapabilitiesResult) Output() any {
	if result.Diagnostic != nil {
		return *result.Diagnostic
	}
	if result.Capabilities != nil {
		return *result.Capabilities
	}
	return nil
}

func (controller RuntimeController) Capabilities(
	ctx context.Context,
	request RuntimeCapabilitiesRequest,
) (RuntimeCapabilitiesResult, error) {
	diagnostic, err := unsupportedContractDiagnostic(
		workrun.DiagnosticOperationWorkCapabilities,
		request.Contract,
		workrun.WorkCapabilitiesContractV1,
		"The requested work capabilities contract is unsupported.",
	)
	if err != nil {
		return RuntimeCapabilitiesResult{}, err
	}
	if diagnostic != nil {
		return RuntimeCapabilitiesResult{Diagnostic: diagnostic}, nil
	}
	runtime, err := controller.open(ctx, request.Repo)
	if err != nil {
		return RuntimeCapabilitiesResult{}, err
	}
	capabilities, err := runtime.Capabilities(ctx)
	if err != nil {
		return RuntimeCapabilitiesResult{}, err
	}
	if err := capabilities.Validate(); err != nil {
		return RuntimeCapabilitiesResult{}, fmt.Errorf(
			"validate productive runtime capabilities: %w",
			err,
		)
	}
	return RuntimeCapabilitiesResult{Capabilities: &capabilities}, nil
}

type RuntimeStartRequest struct {
	Repo     string
	Contract string
	Payload  []byte
}

type RuntimeStartResult struct {
	Status     *workrun.WorkStatusV1
	Diagnostic *workrun.WorkDiagnosticV1
}

type RuntimeRouteDecisionRequest struct {
	Repo             string
	WorkRunID        string
	ExpectedRevision string
	Contract         string
	Choice           workrun.WorkRouteChoice
}

type RuntimeRouteBindSDDRequest struct {
	Repo             string
	WorkRunID        string
	ExpectedRevision string
	Contract         string
	RunRef           string
}

type RuntimeRouteResult struct {
	Route      *workrun.WorkRouteV1
	Diagnostic *workrun.WorkDiagnosticV1
}

func (result RuntimeRouteResult) Output() any {
	if result.Diagnostic != nil {
		return *result.Diagnostic
	}
	if result.Route != nil {
		return *result.Route
	}
	return nil
}

type RuntimeAdvanceRequest struct {
	Repo             string
	WorkRunID        string
	ExpectedRevision string
	Contract         string
}

type RuntimeAdvanceResult struct {
	Advance    *workrun.WorkAdvanceV1
	Diagnostic *workrun.WorkDiagnosticV1
}

func (result RuntimeAdvanceResult) Output() any {
	if result.Diagnostic != nil {
		return *result.Diagnostic
	}
	if result.Advance != nil {
		return *result.Advance
	}
	return nil
}

type RuntimeReconcileRequest struct {
	Repo             string
	WorkRunID        string
	ExpectedRevision string
	DiagnosticRef    string
	Contract         string
}

type RuntimeReconcileResult struct {
	Reconcile  *workrun.WorkReconcileV1
	Diagnostic *workrun.WorkDiagnosticV1
}

func (result RuntimeReconcileResult) Output() any {
	if result.Diagnostic != nil {
		return *result.Diagnostic
	}
	if result.Reconcile != nil {
		return *result.Reconcile
	}
	return nil
}

// DecideRoute submits only the human accept/decline choice. The productive
// runtime derives the pending proposal and any safe replacement route from
// owner authority already bound at START.
func (controller RuntimeController) DecideRoute(
	ctx context.Context,
	request RuntimeRouteDecisionRequest,
) (RuntimeRouteResult, error) {
	diagnostic, err := unsupportedContractDiagnostic(
		workrun.DiagnosticOperationWorkRoute,
		request.Contract,
		workrun.WorkRouteContractV1,
		"The requested work route contract is unsupported.",
	)
	if err != nil {
		return RuntimeRouteResult{}, err
	}
	if diagnostic != nil {
		return RuntimeRouteResult{Diagnostic: diagnostic}, nil
	}
	if err := validateRuntimeRouteEnvelope(
		request.Repo,
		request.WorkRunID,
		request.ExpectedRevision,
	); err != nil {
		return RuntimeRouteResult{}, err
	}
	if request.Choice != workrun.WorkRouteChoiceAcceptSDD &&
		request.Choice != workrun.WorkRouteChoiceDeclineSDD {
		return RuntimeRouteResult{}, errors.New(
			"work-route decide requires --choice accept_sdd or decline_sdd",
		)
	}
	runtime, err := controller.open(ctx, request.Repo)
	if err != nil {
		return RuntimeRouteResult{}, err
	}
	route, err := runtime.DecideRoute(
		ctx,
		request.WorkRunID,
		request.ExpectedRevision,
		request.Choice,
	)
	if err != nil {
		return RuntimeRouteResult{}, err
	}
	if err := validateRuntimeRouteResult(
		route,
		request.WorkRunID,
		request.ExpectedRevision,
		workrun.WorkRouteOperationDecideSDD,
	); err != nil {
		return RuntimeRouteResult{}, err
	}
	if route.Choice != request.Choice {
		return RuntimeRouteResult{}, ErrProviderResultMismatch
	}
	return RuntimeRouteResult{Route: &route}, nil
}

// BindSDDRoute binds only a caller-named, already-existing SDD runtime. The
// accepted route authority remains private and is derived by the provider.
func (controller RuntimeController) BindSDDRoute(
	ctx context.Context,
	request RuntimeRouteBindSDDRequest,
) (RuntimeRouteResult, error) {
	diagnostic, err := unsupportedContractDiagnostic(
		workrun.DiagnosticOperationWorkRoute,
		request.Contract,
		workrun.WorkRouteContractV1,
		"The requested work route contract is unsupported.",
	)
	if err != nil {
		return RuntimeRouteResult{}, err
	}
	if diagnostic != nil {
		return RuntimeRouteResult{Diagnostic: diagnostic}, nil
	}
	if err := validateRuntimeRouteEnvelope(
		request.Repo,
		request.WorkRunID,
		request.ExpectedRevision,
	); err != nil {
		return RuntimeRouteResult{}, err
	}
	if !validNativeSDDRunRef(request.RunRef) {
		return RuntimeRouteResult{}, errors.New(
			"work-route bind-sdd requires a canonical existing SDD run reference",
		)
	}
	runtime, err := controller.open(ctx, request.Repo)
	if err != nil {
		return RuntimeRouteResult{}, err
	}
	route, err := runtime.BindSDDRoute(
		ctx,
		request.WorkRunID,
		request.ExpectedRevision,
		request.RunRef,
	)
	if err != nil {
		return RuntimeRouteResult{}, err
	}
	if err := validateRuntimeRouteResult(
		route,
		request.WorkRunID,
		request.ExpectedRevision,
		workrun.WorkRouteOperationBindSDD,
	); err != nil {
		return RuntimeRouteResult{}, err
	}
	if route.Status.SDDRunRef != request.RunRef {
		return RuntimeRouteResult{}, ErrProviderResultMismatch
	}
	return RuntimeRouteResult{Route: &route}, nil
}

func validateRuntimeRouteEnvelope(
	repo string,
	workRunID string,
	expectedRevision string,
) error {
	if err := validateRepository(repo); err != nil {
		return err
	}
	if !workRunIDPattern.MatchString(workRunID) {
		return errors.New(
			"work-route requires a canonical --work-run identifier",
		)
	}
	if !revisionPattern.MatchString(expectedRevision) {
		return errors.New(
			"work-route requires a lowercase SHA-256 --expected-revision",
		)
	}
	return nil
}

func validateRuntimeRouteResult(
	route workrun.WorkRouteV1,
	workRunID string,
	expectedRevision string,
	operation workrun.WorkRouteOperation,
) error {
	if err := route.Validate(); err != nil {
		return fmt.Errorf("validate productive runtime route: %w", err)
	}
	if route.Status.WorkRunID != workRunID ||
		route.PreviousRevision != expectedRevision ||
		route.Operation != operation {
		return ErrProviderResultMismatch
	}
	return nil
}

// Advance performs one provider-owned bounded convergence attempt. Consumers
// never repeat internal phases or reconstruct owner inputs.
func (controller RuntimeController) Advance(
	ctx context.Context,
	request RuntimeAdvanceRequest,
) (RuntimeAdvanceResult, error) {
	diagnostic, err := unsupportedContractDiagnostic(
		workrun.DiagnosticOperationWorkAdvance,
		request.Contract,
		workrun.WorkAdvanceContractV1,
		"The requested work advance contract is unsupported.",
	)
	if err != nil {
		return RuntimeAdvanceResult{}, err
	}
	if diagnostic != nil {
		return RuntimeAdvanceResult{Diagnostic: diagnostic}, nil
	}
	if !workRunIDPattern.MatchString(request.WorkRunID) {
		return RuntimeAdvanceResult{}, errors.New(
			"work-advance requires a canonical --work-run identifier",
		)
	}
	if !revisionPattern.MatchString(request.ExpectedRevision) {
		return RuntimeAdvanceResult{}, errors.New(
			"work-advance requires a lowercase SHA-256 --expected-revision",
		)
	}
	runtime, err := controller.open(ctx, request.Repo)
	if err != nil {
		return RuntimeAdvanceResult{}, err
	}
	advance, err := runtime.AdvanceOutcome(
		ctx,
		request.WorkRunID,
		request.ExpectedRevision,
	)
	if err != nil {
		return RuntimeAdvanceResult{}, err
	}
	if err := advance.Validate(); err != nil {
		return RuntimeAdvanceResult{}, fmt.Errorf(
			"validate productive runtime advance: %w",
			err,
		)
	}
	if advance.Status.WorkRunID != request.WorkRunID ||
		advance.PreviousRevision != request.ExpectedRevision {
		return RuntimeAdvanceResult{}, ErrProviderResultMismatch
	}
	return RuntimeAdvanceResult{Advance: &advance}, nil
}

// Reconcile performs exactly one owner-only recovery attempt for a terminal
// diagnostic that explicitly requires reconciliation. The consumer supplies
// no outcome, effect, authority, policy, or fallback.
func (controller RuntimeController) Reconcile(
	ctx context.Context,
	request RuntimeReconcileRequest,
) (RuntimeReconcileResult, error) {
	diagnostic, err := unsupportedContractDiagnostic(
		workrun.DiagnosticOperationWorkReconcile,
		request.Contract,
		workrun.WorkReconcileContractV1,
		"The requested work reconcile contract is unsupported.",
	)
	if err != nil {
		return RuntimeReconcileResult{}, err
	}
	if diagnostic != nil {
		return RuntimeReconcileResult{Diagnostic: diagnostic}, nil
	}
	if err := validateRepository(request.Repo); err != nil {
		return RuntimeReconcileResult{}, err
	}
	if !workRunIDPattern.MatchString(request.WorkRunID) {
		return RuntimeReconcileResult{}, errors.New(
			"work-reconcile requires a canonical --work-run identifier",
		)
	}
	if !revisionPattern.MatchString(request.ExpectedRevision) {
		return RuntimeReconcileResult{}, errors.New(
			"work-reconcile requires a lowercase SHA-256 --expected-revision",
		)
	}
	if !revisionPattern.MatchString(request.DiagnosticRef) {
		return RuntimeReconcileResult{}, errors.New(
			"work-reconcile requires a lowercase SHA-256 --diagnostic-ref",
		)
	}
	runtime, err := controller.open(ctx, request.Repo)
	if err != nil {
		return RuntimeReconcileResult{}, err
	}
	reconcile, err := runtime.ReconcileOutcome(
		ctx,
		request.WorkRunID,
		request.ExpectedRevision,
		request.DiagnosticRef,
	)
	if err != nil {
		return RuntimeReconcileResult{}, err
	}
	if err := reconcile.Validate(); err != nil {
		return RuntimeReconcileResult{}, fmt.Errorf(
			"validate productive runtime reconcile: %w",
			err,
		)
	}
	if reconcile.Status.WorkRunID != request.WorkRunID ||
		reconcile.PreviousRevision != request.ExpectedRevision ||
		reconcile.DiagnosticRef != request.DiagnosticRef {
		return RuntimeReconcileResult{}, ErrProviderResultMismatch
	}
	return RuntimeReconcileResult{Reconcile: &reconcile}, nil
}

func (result RuntimeStartResult) Output() any {
	if result.Diagnostic != nil {
		return *result.Diagnostic
	}
	if result.Status != nil {
		return *result.Status
	}
	return nil
}

func (controller RuntimeController) Start(
	ctx context.Context,
	request RuntimeStartRequest,
) (RuntimeStartResult, error) {
	diagnostic, err := unsupportedContractDiagnostic(
		workrun.DiagnosticOperationWorkStart,
		request.Contract,
		workrun.WorkStartContractV1,
		"The requested work start contract is unsupported.",
	)
	if err != nil {
		return RuntimeStartResult{}, err
	}
	if diagnostic != nil {
		return RuntimeStartResult{Diagnostic: diagnostic}, nil
	}
	outcome, err := decodeOutcomeStartEnvelope(request.Payload)
	if err != nil {
		return RuntimeStartResult{}, err
	}
	runtime, err := controller.open(ctx, request.Repo)
	if err != nil {
		return RuntimeStartResult{}, err
	}
	status, err := runtime.StartOutcome(ctx, outcome)
	if errors.Is(err, ErrOutcomeNotManaged) {
		diagnostic := outcomeNotManagedDiagnostic()
		if validateErr := diagnostic.Validate(); validateErr != nil {
			return RuntimeStartResult{}, validateErr
		}
		return RuntimeStartResult{Diagnostic: &diagnostic}, nil
	}
	if err != nil {
		return RuntimeStartResult{}, err
	}
	if err := status.Validate(); err != nil {
		return RuntimeStartResult{}, fmt.Errorf(
			"validate productive runtime start status: %w",
			err,
		)
	}
	return RuntimeStartResult{Status: &status}, nil
}

func outcomeNotManagedDiagnostic() workrun.WorkDiagnosticV1 {
	return workrun.WorkDiagnosticV1{
		Schema:             workrun.WorkDiagnosticSchemaV1,
		Operation:          workrun.DiagnosticOperationWorkStart,
		Code:               "outcome_not_managed",
		Message:            "The owner classified this outcome as not requiring a managed write lifecycle.",
		RequestedContract:  workrun.WorkStartContractV1,
		SupportedContracts: []string{workrun.WorkStartContractV1},
		MutationOutcome:    "not_started",
		NextAction:         "continue_unmanaged",
	}
}

func (controller RuntimeController) open(
	ctx context.Context,
	repo string,
) (RuntimeOutcome, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if repo == "" || repo != strings.TrimSpace(repo) ||
		strings.ContainsRune(repo, '\x00') {
		return nil, errors.New("runtime request requires a canonical repository path")
	}
	if controller.opener == nil {
		return nil, fmt.Errorf(
			"%w: missing productive runtime opener",
			ErrRepositoryUnavailable,
		)
	}
	runtime, err := controller.opener.OpenRuntimeOutcome(ctx, repo)
	if err != nil {
		return nil, err
	}
	if runtime == nil {
		return nil, fmt.Errorf(
			"%w: productive runtime opener returned nil",
			ErrRepositoryUnavailable,
		)
	}
	return runtime, nil
}

func decodeOutcomeStartEnvelope(payload []byte) (OutcomeStartRequest, error) {
	if len(payload) == 0 || len(payload) > maximumOutcomeStartEnvelopeBytes {
		return OutcomeStartRequest{}, errors.New(
			"work-start requires one bounded JSON request on stdin",
		)
	}
	if !utf8.Valid(payload) {
		return OutcomeStartRequest{}, errors.New(
			"work-start requires valid UTF-8 JSON on stdin",
		)
	}
	if err := validateJSONSurrogatePairs(payload); err != nil {
		return OutcomeStartRequest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil {
		return OutcomeStartRequest{}, fmt.Errorf("decode work-start request: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return OutcomeStartRequest{}, errors.New(
			"work-start request must be one JSON object",
		)
	}
	var request OutcomeStartRequest
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return OutcomeStartRequest{}, fmt.Errorf(
				"decode work-start request field: %w",
				err,
			)
		}
		name, ok := token.(string)
		if !ok {
			return OutcomeStartRequest{}, errors.New(
				"work-start request contains a non-string field name",
			)
		}
		if _, duplicate := seen[name]; duplicate {
			return OutcomeStartRequest{}, fmt.Errorf(
				"work-start request repeats field %q",
				name,
			)
		}
		seen[name] = struct{}{}
		switch name {
		case "outcome":
			if err := decoder.Decode(&request.Outcome); err != nil {
				return OutcomeStartRequest{}, fmt.Errorf(
					"decode work-start outcome: %w",
					err,
				)
			}
		case "explicitSddRequested":
			var explicit *bool
			if err := decoder.Decode(&explicit); err != nil {
				return OutcomeStartRequest{}, fmt.Errorf(
					"decode work-start explicit SDD intent: %w",
					err,
				)
			}
			if explicit == nil {
				return OutcomeStartRequest{}, errors.New(
					"work-start explicitSddRequested must be boolean when present",
				)
			}
			request.ExplicitSDDRequested = *explicit
		default:
			return OutcomeStartRequest{}, fmt.Errorf(
				"work-start request contains unknown field %q",
				name,
			)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return OutcomeStartRequest{}, fmt.Errorf("close work-start request: %w", err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return OutcomeStartRequest{}, fmt.Errorf(
				"work-start request contains trailing JSON token %v",
				token,
			)
		}
		return OutcomeStartRequest{}, fmt.Errorf(
			"work-start request contains trailing data: %w",
			err,
		)
	}
	if err := request.validate(); err != nil {
		return OutcomeStartRequest{}, err
	}
	return request, nil
}

func validateJSONSurrogatePairs(payload []byte) error {
	inString := false
	for index := 0; index < len(payload); index++ {
		switch payload[index] {
		case '"':
			inString = !inString
		case '\\':
			if !inString || index+1 >= len(payload) {
				continue
			}
			if payload[index+1] != 'u' {
				index++
				continue
			}
			value, ok := decodeJSONUnicodeEscape(payload, index)
			if !ok {
				continue
			}
			switch {
			case value >= 0xD800 && value <= 0xDBFF:
				low, paired := decodeJSONUnicodeEscape(payload, index+6)
				if !paired || low < 0xDC00 || low > 0xDFFF {
					return errors.New(
						"work-start request contains an unpaired Unicode surrogate escape",
					)
				}
				index += 11
			case value >= 0xDC00 && value <= 0xDFFF:
				return errors.New(
					"work-start request contains an unpaired Unicode surrogate escape",
				)
			default:
				index += 5
			}
		}
	}
	return nil
}

func decodeJSONUnicodeEscape(payload []byte, start int) (rune, bool) {
	if start < 0 || start+6 > len(payload) ||
		payload[start] != '\\' || payload[start+1] != 'u' {
		return 0, false
	}
	var value rune
	for _, digit := range payload[start+2 : start+6] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value += rune(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value += rune(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value += rune(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}
