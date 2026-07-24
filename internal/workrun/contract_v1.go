// Package workrun defines route-neutral work contracts. This file intentionally
// contains no store, router, command, or mutation implementation.
package workrun

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	WorkRoutingCapabilityV1    = "gentle-ai.work-routing/v1"
	WorkCapabilitiesContractV1 = "gentle-ai.work-capabilities/v1"
	WorkStartContractV1        = "gentle-ai.work-start/v1"
	WorkStatusContractV1       = "gentle-ai.work-status/v1"
	WorkTransitionContractV1   = "gentle-ai.work-transition/v1"
	WorkDiagnosticSchemaV1     = "gentle-ai.work-diagnostic/v1"
)

type RouteDecision string

const (
	RouteDecisionDirectInline    RouteDecision = "direct_inline"
	RouteDecisionDelegatedDirect RouteDecision = "delegated_direct"
	RouteDecisionProposeSDD      RouteDecision = "propose_sdd"
)

type ImplementationRoute string

const (
	ImplementationRouteDirectInline    ImplementationRoute = "direct_inline"
	ImplementationRouteDelegatedDirect ImplementationRoute = "delegated_direct"
	ImplementationRouteSDD             ImplementationRoute = "sdd"
)

type PublicState string

const (
	PublicStateWorking           PublicState = "working"
	PublicStateChecking          PublicState = "checking"
	PublicStateReady             PublicState = "ready"
	PublicStateNeedsYourDecision PublicState = "needs_your_decision"
)

type VerificationOutcome string

const (
	VerificationPending     VerificationOutcome = "pending"
	VerificationNotRequired VerificationOutcome = "not_required"
	VerificationComplete    VerificationOutcome = "complete"
	VerificationFailed      VerificationOutcome = "failed"
	VerificationPartial     VerificationOutcome = "partial"
	VerificationUnavailable VerificationOutcome = "unavailable"
)

type VerificationSummaryV1 struct {
	Outcome    VerificationOutcome `json:"outcome"`
	ResultRefs []string            `json:"resultRefs"`
}

// AuthorizedTransitionV1 is owner-issued. Consumers may submit only its
// opaque reference and expected revision; they do not reconstruct policy.
type AuthorizedTransitionV1 struct {
	Contract                   string `json:"contract"`
	Operation                  string `json:"operation"`
	AuthorizationRef           string `json:"authorizationRef"`
	ExpectedRevision           string `json:"expectedRevision"`
	CandidateRef               string `json:"candidateRef"`
	ActionTicketRef            string `json:"actionTicketRef"`
	ApplicableAuthorizationRef string `json:"applicableAuthorizationRef"`
}

type WorkStatusV1 struct {
	Schema               string                  `json:"schema"`
	Contract             string                  `json:"contract"`
	WorkRunID            string                  `json:"workRunId"`
	Revision             string                  `json:"revision"`
	PublicState          PublicState             `json:"publicState"`
	RouteDecision        RouteDecision           `json:"routeDecision"`
	ImplementationRoute  ImplementationRoute     `json:"implementationRoute,omitempty"`
	SDDRunRef            string                  `json:"sddRunRef,omitempty"`
	Verification         VerificationSummaryV1   `json:"verification"`
	DeliveryIntentRef    string                  `json:"deliveryIntentRef,omitempty"`
	ReviewReceiptRef     string                  `json:"reviewReceiptRef,omitempty"`
	AuthorizedTransition *AuthorizedTransitionV1 `json:"authorizedTransition,omitempty"`
}

type WorkTransitionV1 struct {
	Schema                   string                  `json:"schema"`
	Contract                 string                  `json:"contract"`
	WorkRunID                string                  `json:"workRunId"`
	PreviousRevision         string                  `json:"previousRevision"`
	Revision                 string                  `json:"revision"`
	AppliedAuthorizationRef  string                  `json:"appliedAuthorizationRef"`
	PublicState              PublicState             `json:"publicState"`
	NextAuthorizedTransition *AuthorizedTransitionV1 `json:"nextAuthorizedTransition,omitempty"`
}

type DiagnosticOperation string

const (
	DiagnosticOperationWorkCapabilities    DiagnosticOperation = "work.capabilities"
	DiagnosticOperationWorkStart           DiagnosticOperation = "work.start"
	DiagnosticOperationWorkStatus          DiagnosticOperation = "work.status"
	DiagnosticOperationWorkTransitionApply DiagnosticOperation = "work.transition.apply"
)

type WorkDiagnosticV1 struct {
	Schema             string              `json:"schema"`
	Operation          DiagnosticOperation `json:"operation"`
	Code               string              `json:"code"`
	Message            string              `json:"message"`
	RequestedContract  string              `json:"requestedContract"`
	SupportedContracts []string            `json:"supportedContracts"`
	MutationOutcome    string              `json:"mutationOutcome"`
	NextAction         string              `json:"nextAction"`
}

var workRevisionPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (status WorkStatusV1) Validate() error {
	if status.Schema != WorkStatusContractV1 || status.Contract != WorkStatusContractV1 {
		return errors.New("work status must use gentle-ai.work-status/v1")
	}
	if err := validateCanonical("workRunId", status.WorkRunID); err != nil {
		return err
	}
	if !workRevisionPattern.MatchString(status.Revision) {
		return errors.New("work status revision must be sha256")
	}
	if !validPublicState(status.PublicState) {
		return fmt.Errorf("unsupported public state %q", status.PublicState)
	}
	if err := validateRoute(status.RouteDecision, status.ImplementationRoute, status.SDDRunRef); err != nil {
		return err
	}
	if err := status.Verification.validate(); err != nil {
		return err
	}
	for name, ref := range map[string]string{
		"deliveryIntentRef": status.DeliveryIntentRef,
		"reviewReceiptRef":  status.ReviewReceiptRef,
	} {
		if ref != "" {
			if err := validateCanonical(name, ref); err != nil {
				return err
			}
		}
	}
	if status.AuthorizedTransition != nil {
		if err := status.AuthorizedTransition.validate(status.Revision); err != nil {
			return err
		}
	}
	return nil
}

func (transition WorkTransitionV1) Validate() error {
	if transition.Schema != WorkTransitionContractV1 || transition.Contract != WorkTransitionContractV1 {
		return errors.New("work transition must use gentle-ai.work-transition/v1")
	}
	if err := validateCanonical("workRunId", transition.WorkRunID); err != nil {
		return err
	}
	if !workRevisionPattern.MatchString(transition.PreviousRevision) || !workRevisionPattern.MatchString(transition.Revision) {
		return errors.New("work transition revisions must be sha256")
	}
	if transition.PreviousRevision == transition.Revision {
		return errors.New("work transition must advance revision")
	}
	if err := validateCanonical("appliedAuthorizationRef", transition.AppliedAuthorizationRef); err != nil {
		return err
	}
	if !validPublicState(transition.PublicState) {
		return fmt.Errorf("unsupported public state %q", transition.PublicState)
	}
	if transition.NextAuthorizedTransition != nil {
		if err := transition.NextAuthorizedTransition.validate(transition.Revision); err != nil {
			return err
		}
	}
	return nil
}

func (diagnostic WorkDiagnosticV1) Validate() error {
	if diagnostic.Schema != WorkDiagnosticSchemaV1 {
		return errors.New("work diagnostic must use gentle-ai.work-diagnostic/v1")
	}
	if diagnostic.Operation != DiagnosticOperationWorkCapabilities &&
		diagnostic.Operation != DiagnosticOperationWorkStart &&
		diagnostic.Operation != DiagnosticOperationWorkStatus &&
		diagnostic.Operation != DiagnosticOperationWorkTransitionApply {
		return fmt.Errorf("unsupported diagnostic operation %q", diagnostic.Operation)
	}
	if diagnostic.Code != "unsupported_contract" {
		return fmt.Errorf("unsupported diagnostic code %q", diagnostic.Code)
	}
	if err := validateCanonical("message", diagnostic.Message); err != nil {
		return err
	}
	if len(diagnostic.SupportedContracts) == 0 {
		return errors.New("supportedContracts must not be empty")
	}
	for _, contract := range diagnostic.SupportedContracts {
		if contract != WorkCapabilitiesContractV1 &&
			contract != WorkStartContractV1 &&
			contract != WorkStatusContractV1 &&
			contract != WorkTransitionContractV1 {
			return fmt.Errorf("unsupported advertised contract %q", contract)
		}
	}
	if diagnostic.MutationOutcome != "not_started" {
		return errors.New("unsupported-contract diagnostic must be read-only")
	}
	if diagnostic.NextAction != "select_supported_contract" {
		return errors.New("unsupported-contract diagnostic must select a supported contract")
	}
	return nil
}

func (summary VerificationSummaryV1) validate() error {
	switch summary.Outcome {
	case VerificationPending, VerificationNotRequired, VerificationComplete, VerificationFailed,
		VerificationPartial, VerificationUnavailable:
	default:
		return fmt.Errorf("unsupported verification outcome %q", summary.Outcome)
	}
	seen := make(map[string]struct{}, len(summary.ResultRefs))
	for _, ref := range summary.ResultRefs {
		if err := validateCanonical("verification result ref", ref); err != nil {
			return err
		}
		if _, exists := seen[ref]; exists {
			return fmt.Errorf("duplicate verification result ref %q", ref)
		}
		seen[ref] = struct{}{}
	}
	return nil
}

func (transition AuthorizedTransitionV1) validate(expectedRevision string) error {
	if transition.Contract != WorkTransitionContractV1 || transition.Operation != "apply" {
		return errors.New("authorized transition must bind gentle-ai.work-transition/v1 apply")
	}
	if !workRevisionPattern.MatchString(transition.ExpectedRevision) || transition.ExpectedRevision != expectedRevision {
		return errors.New("authorized transition expectedRevision must bind the current work revision")
	}
	for name, ref := range map[string]string{
		"authorizationRef":           transition.AuthorizationRef,
		"candidateRef":               transition.CandidateRef,
		"actionTicketRef":            transition.ActionTicketRef,
		"applicableAuthorizationRef": transition.ApplicableAuthorizationRef,
	} {
		if err := validateCanonical(name, ref); err != nil {
			return err
		}
	}
	return nil
}

func validateRoute(decision RouteDecision, selected ImplementationRoute, sddRunRef string) error {
	var expected ImplementationRoute
	switch decision {
	case RouteDecisionDirectInline:
		expected = ImplementationRouteDirectInline
	case RouteDecisionDelegatedDirect:
		expected = ImplementationRouteDelegatedDirect
	case RouteDecisionProposeSDD:
		expected = ImplementationRouteSDD
	default:
		return fmt.Errorf("unsupported route decision %q", decision)
	}
	if selected != "" && selected != expected {
		return fmt.Errorf("implementation route %q does not match route decision %q", selected, decision)
	}
	if selected == ImplementationRouteSDD {
		return validateCanonical("sddRunRef", sddRunRef)
	}
	if sddRunRef != "" {
		return errors.New("sddRunRef requires the selected sdd implementation route")
	}
	return nil
}

func validPublicState(state PublicState) bool {
	return state == PublicStateWorking || state == PublicStateChecking ||
		state == PublicStateReady || state == PublicStateNeedsYourDecision
}

func validateCanonical(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 240 ||
		strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s must be a non-empty canonical string", name)
	}
	return nil
}
