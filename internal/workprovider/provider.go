// Package workprovider is the route-neutral application boundary for common
// work status and owner-authorized transitions.
package workprovider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

var (
	ErrRepositoryUnavailable  = errors.New("work provider repository is unavailable")
	ErrCapabilityDisabled     = errors.New("work-routing capability is disabled")
	ErrCapabilityReadOnly     = errors.New("work-routing capability is read-only")
	ErrRecoveryOnly           = errors.New("work-routing capability permits recovery or terminalization only")
	ErrAuthorizationNotFound  = errors.New("owner-issued work authorization was not found")
	ErrAuthorizationStale     = errors.New("owner-issued work authorization is stale")
	ErrAuthorizationMismatch  = errors.New("owner-issued work authorization does not match the request")
	ErrProviderResultMismatch = errors.New("work provider result does not match the request")
)

var (
	workRunIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	revisionPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	sddRunRefPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// AuthorizationClass is owner metadata resolved from immutable provider
// storage. It is never accepted from a CLI or consumer request.
type AuthorizationClass string

const (
	AuthorizationGeneral         AuthorizationClass = "general"
	AuthorizationRecovery        AuthorizationClass = "recovery"
	AuthorizationTerminalization AuthorizationClass = "terminalization"
)

// ResolvedAuthorization contains only immutable public bindings. The owner
// repository retains the private payload, policy preimages, and exact command.
type ResolvedAuthorization struct {
	WorkRunID                  string
	AuthorizationRef           string
	ExpectedRevision           string
	CandidateRef               string
	ActionTicketRef            string
	ApplicableAuthorizationRef string
	Class                      AuthorizationClass
}

func (authorization ResolvedAuthorization) Validate() error {
	if !workRunIDPattern.MatchString(authorization.WorkRunID) {
		return errors.New("authorization work run identifier must be canonical")
	}
	for name, value := range map[string]string{
		"authorizationRef":           authorization.AuthorizationRef,
		"candidateRef":               authorization.CandidateRef,
		"actionTicketRef":            authorization.ActionTicketRef,
		"applicableAuthorizationRef": authorization.ApplicableAuthorizationRef,
	} {
		if err := validateCanonical(name, value); err != nil {
			return err
		}
	}
	if !revisionPattern.MatchString(authorization.ExpectedRevision) {
		return errors.New("authorization expected revision must be a lowercase SHA-256 reference")
	}
	switch authorization.Class {
	case AuthorizationGeneral, AuthorizationRecovery, AuthorizationTerminalization:
		return nil
	default:
		return fmt.Errorf("unsupported owner authorization class %q", authorization.Class)
	}
}

func (authorization ResolvedAuthorization) matches(
	workRunID string,
	transition workrun.AuthorizedTransitionV1,
) bool {
	return authorization.WorkRunID == workRunID &&
		authorization.AuthorizationRef == transition.AuthorizationRef &&
		authorization.ExpectedRevision == transition.ExpectedRevision &&
		authorization.CandidateRef == transition.CandidateRef &&
		authorization.ActionTicketRef == transition.ActionTicketRef &&
		authorization.ApplicableAuthorizationRef == transition.ApplicableAuthorizationRef
}

func (authorization ResolvedAuthorization) recoverySafe() bool {
	return authorization.Class == AuthorizationRecovery ||
		authorization.Class == AuthorizationTerminalization
}

// Repository is implemented by the provider-owned composition layer. Status
// and ResolveAuthorization are read-only: pending authorizations must already
// exist in owner storage and polling cannot issue one. ResolveAuthorization
// validates liveness, expiry, and exact owner bindings before returning
// metadata. ApplyAuthorized receives no consumer-authored payload.
type Repository interface {
	Status(context.Context) (workrun.WorkStatusV1, error)
	ResolveAuthorization(context.Context, string) (ResolvedAuthorization, error)
	ApplyAuthorized(context.Context, string, string) (workrun.WorkTransitionV1, error)
}

type RepositoryOpener interface {
	OpenRepository(context.Context, string, string) (Repository, error)
}

type Controller struct {
	opener     RepositoryOpener
	activation ActivationResolver
}

func NewController(opener RepositoryOpener, activation ActivationResolver) Controller {
	return Controller{opener: opener, activation: activation}
}

type StatusRequest struct {
	Repo      string
	WorkRunID string
	Contract  string
}

type StatusResult struct {
	Status     *workrun.WorkStatusV1
	Diagnostic *workrun.WorkDiagnosticV1
}

func (result StatusResult) Output() any {
	if result.Diagnostic != nil {
		return *result.Diagnostic
	}
	if result.Status != nil {
		return *result.Status
	}
	return nil
}

type ApplyRequest struct {
	Repo             string
	WorkRunID        string
	Contract         string
	AuthorizationRef string
	ExpectedRevision string
}

type ApplyResult struct {
	Transition *workrun.WorkTransitionV1
	Diagnostic *workrun.WorkDiagnosticV1
}

func (result ApplyResult) Output() any {
	if result.Diagnostic != nil {
		return *result.Diagnostic
	}
	if result.Transition != nil {
		return *result.Transition
	}
	return nil
}

// Status performs contract negotiation before request validation, activation,
// or repository opening. Unsupported contracts are therefore provably
// read-only even when every other request field is malformed.
func (controller Controller) Status(
	ctx context.Context,
	request StatusRequest,
) (StatusResult, error) {
	diagnostic, err := unsupportedContractDiagnostic(
		workrun.DiagnosticOperationWorkStatus,
		request.Contract,
		workrun.WorkStatusContractV1,
		"The requested work status contract is unsupported.",
	)
	if err != nil {
		return StatusResult{}, err
	}
	if diagnostic != nil {
		return StatusResult{Diagnostic: diagnostic}, nil
	}
	if err := validateStatusRequest(request); err != nil {
		return StatusResult{}, err
	}
	mode, err := controller.resolveActivation(ctx, request.Repo)
	if err != nil {
		return StatusResult{}, err
	}
	if mode == ActivationDisabled {
		return StatusResult{}, ErrCapabilityDisabled
	}
	repository, err := controller.open(ctx, request.Repo, request.WorkRunID)
	if err != nil {
		return StatusResult{}, err
	}
	status, err := repository.Status(ctx)
	if err != nil {
		return StatusResult{}, err
	}
	if err := status.Validate(); err != nil {
		return StatusResult{}, fmt.Errorf("validate owner work status: %w", err)
	}
	if status.WorkRunID != request.WorkRunID {
		return StatusResult{}, fmt.Errorf("%w: status work run", ErrProviderResultMismatch)
	}

	switch mode {
	case ActivationReadOnly:
		status.AuthorizedTransition = nil
	case ActivationRecoveryOnly, ActivationEnabled:
		status.AuthorizedTransition, err = controller.effectiveTransition(
			ctx,
			repository,
			status.WorkRunID,
			status.AuthorizedTransition,
			mode,
		)
		if err != nil {
			return StatusResult{}, err
		}
	}
	if err := status.Validate(); err != nil {
		return StatusResult{}, fmt.Errorf("validate effective work status: %w", err)
	}
	return StatusResult{Status: &status}, nil
}

// Apply accepts exactly an opaque owner authorization reference and the
// expected WorkRun revision. All transition kind, payload, ticket contents,
// policy, and argv remain inside the injected owner repository.
func (controller Controller) Apply(
	ctx context.Context,
	request ApplyRequest,
) (ApplyResult, error) {
	diagnostic, err := unsupportedContractDiagnostic(
		workrun.DiagnosticOperationWorkTransitionApply,
		request.Contract,
		workrun.WorkTransitionContractV1,
		"The requested work transition contract is unsupported.",
	)
	if err != nil {
		return ApplyResult{}, err
	}
	if diagnostic != nil {
		return ApplyResult{Diagnostic: diagnostic}, nil
	}
	if err := validateApplyRequest(request); err != nil {
		return ApplyResult{}, err
	}
	mode, err := controller.resolveActivation(ctx, request.Repo)
	if err != nil {
		return ApplyResult{}, err
	}
	switch mode {
	case ActivationDisabled:
		return ApplyResult{}, ErrCapabilityDisabled
	case ActivationReadOnly:
		return ApplyResult{}, ErrCapabilityReadOnly
	}

	repository, err := controller.open(ctx, request.Repo, request.WorkRunID)
	if err != nil {
		return ApplyResult{}, err
	}
	authorization, err := repository.ResolveAuthorization(ctx, request.AuthorizationRef)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("resolve owner work authorization: %w", err)
	}
	if err := authorization.Validate(); err != nil {
		return ApplyResult{}, fmt.Errorf("%w: %v", ErrAuthorizationMismatch, err)
	}
	if authorization.AuthorizationRef != request.AuthorizationRef ||
		authorization.WorkRunID != request.WorkRunID ||
		authorization.ExpectedRevision != request.ExpectedRevision {
		return ApplyResult{}, ErrAuthorizationMismatch
	}
	if mode == ActivationRecoveryOnly && !authorization.recoverySafe() {
		return ApplyResult{}, ErrRecoveryOnly
	}

	transition, err := repository.ApplyAuthorized(
		ctx,
		request.AuthorizationRef,
		request.ExpectedRevision,
	)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := transition.Validate(); err != nil {
		return ApplyResult{}, fmt.Errorf("validate owner work transition: %w", err)
	}
	if transition.WorkRunID != request.WorkRunID ||
		transition.PreviousRevision != request.ExpectedRevision ||
		transition.AppliedAuthorizationRef != request.AuthorizationRef {
		return ApplyResult{}, ErrProviderResultMismatch
	}
	return ApplyResult{Transition: &transition}, nil
}

func (controller Controller) resolveActivation(
	ctx context.Context,
	repo string,
) (ActivationMode, error) {
	if controller.activation == nil {
		return ActivationDisabled, fmt.Errorf(
			"%w: missing activation resolver",
			ErrRepositoryUnavailable,
		)
	}
	mode, err := controller.activation.ResolveActivation(ctx, repo)
	if err != nil {
		return ActivationDisabled, err
	}
	if err := mode.Validate(); err != nil {
		return ActivationDisabled, err
	}
	return mode, nil
}

func (controller Controller) open(
	ctx context.Context,
	repo string,
	workRunID string,
) (Repository, error) {
	if controller.opener == nil {
		return nil, ErrRepositoryUnavailable
	}
	repository, err := controller.opener.OpenRepository(ctx, repo, workRunID)
	if err != nil {
		return nil, fmt.Errorf("open work provider repository: %w", err)
	}
	if repository == nil {
		return nil, ErrRepositoryUnavailable
	}
	return repository, nil
}

func (controller Controller) effectiveTransition(
	ctx context.Context,
	repository Repository,
	workRunID string,
	transition *workrun.AuthorizedTransitionV1,
	mode ActivationMode,
) (*workrun.AuthorizedTransitionV1, error) {
	if transition == nil {
		return nil, nil
	}
	authorization, err := repository.ResolveAuthorization(ctx, transition.AuthorizationRef)
	if err != nil {
		if errors.Is(err, ErrAuthorizationNotFound) ||
			errors.Is(err, ErrAuthorizationStale) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve advertised work authorization: %w", err)
	}
	if err := authorization.Validate(); err != nil ||
		!authorization.matches(workRunID, *transition) {
		return nil, nil
	}
	if mode == ActivationRecoveryOnly && !authorization.recoverySafe() {
		return nil, nil
	}
	copy := *transition
	return &copy, nil
}

func unsupportedContractDiagnostic(
	operation workrun.DiagnosticOperation,
	requested string,
	supported string,
	message string,
) (*workrun.WorkDiagnosticV1, error) {
	if requested == supported {
		return nil, nil
	}
	diagnostic := workrun.WorkDiagnosticV1{
		Schema:             workrun.WorkDiagnosticSchemaV1,
		Operation:          operation,
		Code:               "unsupported_contract",
		Message:            message,
		RequestedContract:  requested,
		SupportedContracts: []string{supported},
		MutationOutcome:    "not_started",
		NextAction:         "select_supported_contract",
	}
	if err := diagnostic.Validate(); err != nil {
		return nil, fmt.Errorf("invalid requested work contract: %w", err)
	}
	return &diagnostic, nil
}

func validateStatusRequest(request StatusRequest) error {
	if err := validateRepository(request.Repo); err != nil {
		return err
	}
	if !workRunIDPattern.MatchString(request.WorkRunID) {
		return errors.New("work-status requires a canonical --work-run identifier")
	}
	return nil
}

func validateApplyRequest(request ApplyRequest) error {
	if err := validateRepository(request.Repo); err != nil {
		return err
	}
	if !workRunIDPattern.MatchString(request.WorkRunID) {
		return errors.New("work-transition apply requires a canonical --work-run identifier")
	}
	if err := validateCanonical("authorizationRef", request.AuthorizationRef); err != nil {
		return fmt.Errorf("work-transition apply: %w", err)
	}
	if !revisionPattern.MatchString(request.ExpectedRevision) {
		return errors.New("work-transition apply requires --expected-revision as lowercase SHA-256")
	}
	return nil
}

func validateRepository(repo string) error {
	if repo == "" || repo != strings.TrimSpace(repo) ||
		strings.ContainsAny(repo, "\r\n\x00") {
		return errors.New("work provider requires a canonical --cwd")
	}
	return nil
}

func validateCanonical(name string, value string) error {
	if value == "" || !utf8.ValidString(value) ||
		!hasCanonicalTextEdges(value) ||
		utf8.RuneCountInString(value) > 240 ||
		strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s must be a non-empty canonical string", name)
	}
	return nil
}

func hasCanonicalTextEdges(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	return !isPublicEdgeWhitespace(first) && !isPublicEdgeWhitespace(last)
}

func isPublicEdgeWhitespace(value rune) bool {
	return unicode.IsSpace(value) || value == '\uFEFF'
}
