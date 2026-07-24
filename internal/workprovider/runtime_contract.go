package workprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const maximumOutcomeStartEnvelopeBytes = maximumOutcomeRequestBytes + 1024

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
	if diagnostic := unsupportedContractDiagnostic(
		workrun.DiagnosticOperationWorkCapabilities,
		request.Contract,
		workrun.WorkCapabilitiesContractV1,
		"The requested work capabilities contract is unsupported.",
	); diagnostic != nil {
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
	if diagnostic := unsupportedContractDiagnostic(
		workrun.DiagnosticOperationWorkStart,
		request.Contract,
		workrun.WorkStartContractV1,
		"The requested work start contract is unsupported.",
	); diagnostic != nil {
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
			if err := decoder.Decode(&request.ExplicitSDDRequested); err != nil {
				return OutcomeStartRequest{}, fmt.Errorf(
					"decode work-start explicit SDD intent: %w",
					err,
				)
			}
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
