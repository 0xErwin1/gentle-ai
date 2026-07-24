package workrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

var (
	ErrAuthorityPortUnavailable      = errors.New("work run owner authority port is unavailable")
	ErrAuthorityBindingMismatch      = errors.New("work run owner authority binding does not match")
	ErrDeliveryAuthorizationInactive = errors.New("delivery authorization is not live")
)

// PADAuthorityPort resolves delivery facts only from PAD-owned storage. The
// Live method must re-evaluate policy and expiry with PAD's trusted clock on
// every call and return ErrDeliveryAuthorizationInactive when no longer live.
// A hash-shaped caller value is never sufficient authority.
type PADAuthorityPort interface {
	ResolveDeliveryIntent(context.Context, string) (DeliveryIntentAuthority, error)
	ResolveLiveDeliveryAuthorization(
		context.Context,
		string,
	) (DeliveryAuthorizationAuthority, error)
}

// DeliveryResultAuthorityPort resolves the durable terminal PAD execution
// fact after its one-shot authorization has been consumed.
type DeliveryResultAuthorityPort interface {
	ResolveDeliveryResult(
		context.Context,
		string,
	) (DeliveryResultAuthority, error)
}

// ProductiveExecutionResultAuthorityPort resolves the exact terminal PAD
// ExecutionResult that a productive advance is about to journal. Unlike the
// later delivery projection, this authority covers every terminal outcome:
// succeeded, failed, and indeterminate.
type ProductiveExecutionResultAuthorityPort interface {
	ResolveProductiveExecutionResult(
		context.Context,
		string,
	) (ProductiveExecutionResultAuthority, error)
}

// ProductiveDiagnosticAuthorityPort resolves the owner-authored reason that
// terminally stops productive convergence. WorkRun must validate the complete
// source-stage binding before it journals the immutable reference; accepting a
// hash-shaped caller value would let an arbitrary internal caller wedge a run.
type ProductiveDiagnosticAuthorityPort interface {
	ResolveProductiveDiagnostic(
		context.Context,
		string,
	) (ProductiveDiagnosticAuthority, error)
}

// ProductiveReconciliationAuthorityPort resolves the single owner-authored
// reconciliation of a terminal productive blocker. The caller supplies only
// the immutable authority reference; outcome and any terminal evidence remain
// owner output.
type ProductiveReconciliationAuthorityPort interface {
	ResolveProductiveReconciliation(
		context.Context,
		string,
	) (ProductiveReconciliationAuthority, error)
}

type ProductiveDiagnosticAuthority struct {
	Diagnostic                   WorkAdvanceDiagnosticV1
	RepositoryRef                string
	WorkRunID                    string
	SourceRevision               string
	DeliveryIntentRef            string
	Handoff                      *ImplementationHandoff
	VerificationResultRef        string
	ReviewReceiptRef             string
	DeliveryAuthorizationRef     string
	ProductiveExecutionResultRef string
}

// Validate accepts either the exact pre-mutation source state or the exact
// terminal replay state. The source revision cryptographically binds every
// WorkRun fact not copied into this narrow projection.
func (authority ProductiveDiagnosticAuthority) Validate(
	requestedRef string,
	repositoryRef string,
	state WorkRunState,
) error {
	if err := authority.Diagnostic.Validate(); err != nil {
		return err
	}
	if authority.Diagnostic.Ref != requestedRef ||
		!validSHA256Ref(authority.RepositoryRef) ||
		authority.RepositoryRef != repositoryRef ||
		!workRunIDPattern.MatchString(authority.WorkRunID) ||
		authority.WorkRunID != state.WorkRunID ||
		!validSHA256Ref(authority.SourceRevision) ||
		!validSHA256Ref(state.ProductiveAdvanceSourceRevision) ||
		!validSHA256Ref(authority.DeliveryIntentRef) ||
		authority.DeliveryIntentRef != state.DeliveryIntentRef ||
		!reflect.DeepEqual(authority.Handoff, state.Handoff) ||
		authority.VerificationResultRef != state.VerificationResultRef ||
		authority.ReviewReceiptRef != state.ReviewReceiptRef ||
		authority.DeliveryAuthorizationRef != state.DeliveryAuthorizationRef ||
		authority.ProductiveExecutionResultRef !=
			state.ProductiveExecutionResultRef ||
		state.DeliveryResultRef != "" {
		return fmt.Errorf(
			"%w: productive diagnostic stage",
			ErrAuthorityBindingMismatch,
		)
	}
	if authority.Handoff != nil {
		if err := authority.Handoff.Validate(); err != nil {
			return err
		}
	}
	for _, ref := range []string{
		authority.VerificationResultRef,
		authority.ReviewReceiptRef,
		authority.DeliveryAuthorizationRef,
		authority.ProductiveExecutionResultRef,
	} {
		if ref != "" && !validSHA256Ref(ref) {
			return errors.New(
				"productive diagnostic authority has invalid stage references",
			)
		}
	}
	switch {
	case state.ProductiveBlockerRef == "":
		if authority.SourceRevision != state.Revision ||
			!productiveBlockerBindable(state) {
			return fmt.Errorf(
				"%w: productive diagnostic source",
				ErrAuthorityBindingMismatch,
			)
		}
	case state.ProductiveBlockerRef == requestedRef:
		if authority.SourceRevision != state.ProductiveBlockerSourceRevision {
			return fmt.Errorf(
				"%w: productive diagnostic terminal replay",
				ErrAuthorityBindingMismatch,
			)
		}
	default:
		return fmt.Errorf(
			"%w: productive diagnostic terminal reference",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

type ProductiveReconciliationAuthority struct {
	ReconciliationRef            string
	RepositoryRef                string
	WorkRunID                    string
	SourceRevision               string
	AdvanceSourceRevision        string
	OriginalDiagnosticRef        string
	HistoricalAdvanceRef         string
	DeliveryIntentRef            string
	Handoff                      *ImplementationHandoff
	VerificationResultRef        string
	ReviewReceiptRef             string
	DeliveryAuthorizationRef     string
	ProductiveExecutionResultRef string
	Outcome                      WorkReconcileOutcome
	Diagnostic                   *WorkAdvanceDiagnosticV1
	DeliveryResultRef            string
}

// Validate accepts only the exact blocked pre-state or the exact replayed
// reconciliation post-state. SourceRevision binds the complete blocked
// WorkRun record while the narrow stage projection prevents an authority from
// being transplanted to another repository, run, diagnostic, or delivery
// stage.
func (authority ProductiveReconciliationAuthority) Validate(
	requestedRef string,
	repositoryRef string,
	state WorkRunState,
) error {
	if !validSHA256Ref(authority.ReconciliationRef) ||
		authority.ReconciliationRef != requestedRef ||
		!validSHA256Ref(authority.RepositoryRef) ||
		authority.RepositoryRef != repositoryRef ||
		!validSHA256Ref(repositoryRef) ||
		!workRunIDPattern.MatchString(authority.WorkRunID) ||
		authority.WorkRunID != state.WorkRunID ||
		!validSHA256Ref(authority.SourceRevision) ||
		!validSHA256Ref(authority.AdvanceSourceRevision) ||
		authority.AdvanceSourceRevision !=
			state.ProductiveAdvanceSourceRevision ||
		!validSHA256Ref(authority.OriginalDiagnosticRef) ||
		authority.OriginalDiagnosticRef != state.ProductiveBlockerRef ||
		!validSHA256Ref(authority.HistoricalAdvanceRef) ||
		!validSHA256Ref(state.ProductiveBlockerSourceRevision) ||
		!validSHA256Ref(authority.DeliveryIntentRef) ||
		authority.DeliveryIntentRef != state.DeliveryIntentRef ||
		!reflect.DeepEqual(authority.Handoff, state.Handoff) ||
		authority.VerificationResultRef != state.VerificationResultRef ||
		authority.ReviewReceiptRef != state.ReviewReceiptRef ||
		authority.DeliveryAuthorizationRef !=
			state.DeliveryAuthorizationRef ||
		authority.ProductiveExecutionResultRef !=
			state.ProductiveExecutionResultRef {
		return fmt.Errorf(
			"%w: productive reconciliation stage",
			ErrAuthorityBindingMismatch,
		)
	}
	if authority.Handoff != nil {
		if err := authority.Handoff.Validate(); err != nil {
			return err
		}
	}
	for _, ref := range []string{
		authority.VerificationResultRef,
		authority.ReviewReceiptRef,
		authority.DeliveryAuthorizationRef,
		authority.ProductiveExecutionResultRef,
	} {
		if ref != "" && !validSHA256Ref(ref) {
			return errors.New(
				"productive reconciliation authority has invalid stage references",
			)
		}
	}
	if err := authority.validateOutcome(); err != nil {
		return err
	}
	switch {
	case state.ProductiveReconciliationRef == "":
		if authority.SourceRevision != state.Revision ||
			state.ProductiveReconciliationSourceRevision != "" ||
			state.ProductiveReconciliationOutcome != "" ||
			state.DeliveryResultRef != "" {
			return fmt.Errorf(
				"%w: productive reconciliation source",
				ErrAuthorityBindingMismatch,
			)
		}
	case state.ProductiveReconciliationRef == requestedRef:
		if !validSHA256Ref(state.Revision) ||
			state.Revision == authority.SourceRevision ||
			state.ProductiveReconciliationSourceRevision !=
				authority.SourceRevision ||
			state.ProductiveReconciliationOutcome != authority.Outcome ||
			state.DeliveryResultRef != authority.DeliveryResultRef {
			return fmt.Errorf(
				"%w: productive reconciliation replay",
				ErrAuthorityBindingMismatch,
			)
		}
	default:
		return fmt.Errorf(
			"%w: productive reconciliation reference",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

func (authority ProductiveReconciliationAuthority) validateOutcome() error {
	switch authority.Outcome {
	case WorkReconcileDeliveryConfirmed:
		if authority.Diagnostic != nil ||
			!validSHA256Ref(
				authority.ProductiveExecutionResultRef,
			) ||
			!validSHA256Ref(authority.DeliveryResultRef) {
			return errors.New(
				"delivery-confirmed reconciliation requires only a delivery result",
			)
		}
	case WorkReconcileNoDeliveryConfirmed:
		if authority.DeliveryResultRef != "" ||
			authority.Diagnostic == nil {
			return errors.New(
				"no-delivery reconciliation requires only an owner diagnostic",
			)
		}
		if err := authority.Diagnostic.Validate(); err != nil {
			return err
		}
		if authority.Diagnostic.Code !=
			WorkAdvanceDiagnosticDeliveryNotCompleted ||
			authority.Diagnostic.NextAction !=
				WorkAdvanceNextActionStartFresh {
			return fmt.Errorf(
				"%w: no-delivery reconciliation outcome",
				ErrAuthorityBindingMismatch,
			)
		}
	case WorkReconcileManualResolution:
		if authority.DeliveryResultRef != "" ||
			authority.Diagnostic == nil {
			return errors.New(
				"manual reconciliation requires only an owner diagnostic",
			)
		}
		if err := authority.Diagnostic.Validate(); err != nil {
			return err
		}
		if authority.Diagnostic.Code !=
			WorkAdvanceDiagnosticManualResolutionRequired ||
			authority.Diagnostic.NextAction !=
				WorkAdvanceNextActionManual {
			return fmt.Errorf(
				"%w: manual reconciliation outcome",
				ErrAuthorityBindingMismatch,
			)
		}
	default:
		return fmt.Errorf(
			"unsupported productive reconciliation outcome %q",
			authority.Outcome,
		)
	}
	return nil
}

// ProductiveExecutionResultAuthority is the complete owner record behind the
// raw PAD ExecutionResultRef committed to WorkRun. ResultRef is deliberately
// the content identity of Execution itself, not of this surrounding record,
// so PAD, the authorization-use ledger, and WorkRun all compare the same
// terminal fact while the remaining fields prevent cross-run transplantation.
type ProductiveExecutionResultAuthority struct {
	ResultRef                string
	RepositoryRef            string
	WorkRunID                string
	SourceRevision           string
	DeliveryIntentRef        string
	Handoff                  *ImplementationHandoff
	VerificationResultRef    string
	ReviewReceiptRef         string
	DeliveryAuthorizationRef string
	CommandRef               string
	Execution                deliveryadmission.ExecutionResult
}

// Validate accepts either the exact source state before the result event or
// any later state carrying the same immutable result/source pair. Every
// terminal field is validated and ResultRef is recomputed from the canonical
// ExecutionResult bytes; a hash-shaped caller value is never authority.
func (authority ProductiveExecutionResultAuthority) Validate(
	requestedRef string,
	repositoryRef string,
	state WorkRunState,
) error {
	recomputed, err := productiveExecutionResultRef(authority.Execution)
	if err != nil {
		return err
	}
	if !validSHA256Ref(requestedRef) ||
		authority.ResultRef != requestedRef ||
		recomputed != requestedRef ||
		!validSHA256Ref(authority.RepositoryRef) ||
		authority.RepositoryRef != repositoryRef ||
		!validSHA256Ref(repositoryRef) ||
		!workRunIDPattern.MatchString(authority.WorkRunID) ||
		authority.WorkRunID != state.WorkRunID ||
		!validSHA256Ref(authority.SourceRevision) ||
		!validSHA256Ref(authority.DeliveryIntentRef) ||
		authority.DeliveryIntentRef != state.DeliveryIntentRef ||
		!reflect.DeepEqual(authority.Handoff, state.Handoff) ||
		!validSHA256Ref(authority.VerificationResultRef) ||
		authority.VerificationResultRef != state.VerificationResultRef ||
		!validSHA256Ref(authority.ReviewReceiptRef) ||
		authority.ReviewReceiptRef != state.ReviewReceiptRef ||
		!validSHA256Ref(authority.DeliveryAuthorizationRef) ||
		authority.DeliveryAuthorizationRef !=
			state.DeliveryAuthorizationRef ||
		!validSHA256Ref(authority.CommandRef) ||
		authority.Execution.CommandRef != authority.CommandRef ||
		authority.Execution.AuthorizationRef !=
			authority.DeliveryAuthorizationRef {
		return fmt.Errorf(
			"%w: productive execution result stage",
			ErrAuthorityBindingMismatch,
		)
	}
	if authority.Handoff == nil {
		return fmt.Errorf(
			"%w: productive execution result handoff",
			ErrAuthorityBindingMismatch,
		)
	}
	if err := authority.Handoff.Validate(); err != nil {
		return err
	}
	if authority.Execution.Schema !=
		deliveryadmission.ExecutionResultContractV1 ||
		authority.Execution.Candidate.Ref !=
			"work-run:"+authority.WorkRunID ||
		authority.Execution.Candidate.Digest !=
			authority.Handoff.CandidateRef ||
		!validSHA256Ref(authority.Execution.Candidate.Digest) ||
		!validSHA256Ref(authority.Execution.EvidenceRef) ||
		authority.Execution.CompletedAt <= 0 {
		return fmt.Errorf(
			"%w: productive execution result content",
			ErrAuthorityBindingMismatch,
		)
	}
	switch authority.Execution.Route {
	case deliveryadmission.RoutePRWithIssue,
		deliveryadmission.RoutePRWithoutIssue,
		deliveryadmission.RouteDirectMain,
		deliveryadmission.RouteEmergency:
	default:
		return fmt.Errorf(
			"%w: productive execution result route",
			ErrAuthorityBindingMismatch,
		)
	}
	switch authority.Execution.Outcome {
	case deliveryadmission.ExecutionSucceeded:
		if !validOpaqueRef(authority.Execution.DeliveryRef) {
			return fmt.Errorf(
				"%w: productive succeeded execution result",
				ErrAuthorityBindingMismatch,
			)
		}
	case deliveryadmission.ExecutionFailed,
		deliveryadmission.ExecutionIndeterminate:
		if authority.Execution.DeliveryRef != "" {
			return fmt.Errorf(
				"%w: productive unsuccessful execution result",
				ErrAuthorityBindingMismatch,
			)
		}
	default:
		return fmt.Errorf(
			"%w: productive execution result outcome",
			ErrAuthorityBindingMismatch,
		)
	}
	if !validSHA256Ref(state.ProductiveAdvanceSourceRevision) {
		return fmt.Errorf(
			"%w: productive execution result advance",
			ErrAuthorityBindingMismatch,
		)
	}
	switch {
	case state.ProductiveExecutionResultRef == "":
		if state.ProductiveExecutionResultSourceRevision != "" ||
			authority.SourceRevision != state.Revision ||
			state.ProductiveBlockerRef != "" ||
			state.ProductiveReconciliationRef != "" ||
			state.DeliveryResultRef != "" {
			return fmt.Errorf(
				"%w: productive execution result source",
				ErrAuthorityBindingMismatch,
			)
		}
	case state.ProductiveExecutionResultRef == requestedRef:
		if state.ProductiveExecutionResultSourceRevision !=
			authority.SourceRevision ||
			state.Revision == authority.SourceRevision {
			return fmt.Errorf(
				"%w: productive execution result replay",
				ErrAuthorityBindingMismatch,
			)
		}
	default:
		return fmt.Errorf(
			"%w: productive execution result reference",
			ErrAuthorityBindingMismatch,
		)
	}
	if state.DeliveryResultRef != "" &&
		authority.Execution.Outcome !=
			deliveryadmission.ExecutionSucceeded {
		return fmt.Errorf(
			"%w: delivery projection differs from productive execution result",
			ErrAuthorityBindingMismatch,
		)
	}
	if state.ProductiveReconciliationOutcome ==
		WorkReconcileDeliveryConfirmed &&
		authority.Execution.Outcome !=
			deliveryadmission.ExecutionSucceeded {
		return fmt.Errorf(
			"%w: delivery reconciliation differs from productive execution result",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

func productiveExecutionResultRef(
	result deliveryadmission.ExecutionResult,
) (string, error) {
	payload, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf(
			"canonicalize productive execution result: %w",
			err,
		)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

type DeliveryResultAuthority struct {
	ResultRef             string `json:"result_ref"`
	ExecutionResultRef    string `json:"execution_result_ref"`
	AuthorizationRef      string `json:"authorization_ref"`
	DeliveryIntentRef     string `json:"delivery_intent_ref"`
	CandidateRef          string `json:"candidate_ref"`
	ReviewReceiptRef      string `json:"review_receipt_ref"`
	VerificationResultRef string `json:"verification_result_ref"`
	DeliveryRef           string `json:"delivery_ref"`
	CompletedAt           int64  `json:"completed_at"`
}

func (authority DeliveryResultAuthority) Validate(
	requestedRef string,
	state WorkRunState,
) error {
	for _, ref := range []string{
		authority.ResultRef,
		authority.ExecutionResultRef,
		authority.AuthorizationRef,
		authority.DeliveryIntentRef,
		authority.CandidateRef,
		authority.ReviewReceiptRef,
		authority.VerificationResultRef,
	} {
		if !validSHA256Ref(ref) {
			return errors.New(
				"delivery result authority has invalid immutable references",
			)
		}
	}
	if authority.ResultRef != requestedRef ||
		authority.CompletedAt <= 0 ||
		!validOpaqueRef(authority.DeliveryRef) ||
		state.Handoff == nil ||
		authority.ExecutionResultRef !=
			state.ProductiveExecutionResultRef ||
		authority.AuthorizationRef != state.DeliveryAuthorizationRef ||
		authority.DeliveryIntentRef != state.DeliveryIntentRef ||
		authority.CandidateRef != state.Handoff.CandidateRef ||
		authority.ReviewReceiptRef != state.ReviewReceiptRef ||
		authority.VerificationResultRef != state.VerificationResultRef {
		return fmt.Errorf(
			"%w: terminal delivery result",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

type DeliveryIntentAuthority struct {
	IntentRef string
}

func (authority DeliveryIntentAuthority) Validate(ref string) error {
	if !validSHA256Ref(authority.IntentRef) || authority.IntentRef != ref {
		return fmt.Errorf("%w: delivery intent", ErrAuthorityBindingMismatch)
	}
	return nil
}

// DeliveryRouteReevaluationAuthorityPort resolves one immutable PAD
// route-reevaluation object. WorkRun never accepts a replacement intent
// directly from its caller: PAD must prove the exact source/target lineage and
// that all terminal content authorities were preserved.
type DeliveryRouteReevaluationAuthorityPort interface {
	ResolveDeliveryRouteReevaluation(
		context.Context,
		string,
	) (DeliveryRouteReevaluationAuthority, error)
}

type DeliveryRouteReevaluationAuthority struct {
	ReevaluationRef            string
	RepositoryRef              string
	SourceDecisionRef          string
	TargetAdmissionDecisionRef string
	TargetDecisionRef          string
	SourceDeliveryIntentRef    string
	TargetDeliveryIntentRef    string
	SourceRoute                string
	TargetRoute                string
	Mechanism                  string
	ScopeDigest                string
	CandidateRef               string
	ReviewReceiptRef           string
	VerificationResultRef      string
}

func (authority DeliveryRouteReevaluationAuthority) Validate(
	ref string,
	state WorkRunState,
) error {
	if err := authority.validateHeader(ref); err != nil {
		return err
	}
	if state.Handoff == nil ||
		authority.SourceDeliveryIntentRef != state.DeliveryIntentRef ||
		authority.ScopeDigest != state.Handoff.ScopeDigest ||
		authority.CandidateRef != state.Handoff.CandidateRef ||
		authority.VerificationResultRef != state.VerificationResultRef ||
		authority.ReviewReceiptRef != state.ReviewReceiptRef {
		return fmt.Errorf(
			"%w: delivery route reevaluation terminal facts",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

func (authority DeliveryRouteReevaluationAuthority) validateTargetBinding(
	ref string,
	state WorkRunState,
) error {
	if err := authority.validateHeader(ref); err != nil {
		return err
	}
	if state.Handoff == nil ||
		authority.TargetDeliveryIntentRef != state.DeliveryIntentRef ||
		authority.ScopeDigest != state.Handoff.ScopeDigest ||
		authority.CandidateRef != state.Handoff.CandidateRef ||
		authority.VerificationResultRef != state.VerificationResultRef ||
		authority.ReviewReceiptRef != state.ReviewReceiptRef {
		return fmt.Errorf(
			"%w: bound delivery route reevaluation terminal facts",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

func (authority DeliveryRouteReevaluationAuthority) validateHeader(
	ref string,
) error {
	for _, immutableRef := range []string{
		authority.ReevaluationRef,
		authority.RepositoryRef,
		authority.SourceDecisionRef,
		authority.TargetAdmissionDecisionRef,
		authority.TargetDecisionRef,
		authority.SourceDeliveryIntentRef,
		authority.TargetDeliveryIntentRef,
		authority.ScopeDigest,
		authority.CandidateRef,
		authority.ReviewReceiptRef,
		authority.VerificationResultRef,
	} {
		if !validSHA256Ref(immutableRef) {
			return errors.New(
				"delivery route reevaluation authority has invalid immutable references",
			)
		}
	}
	if authority.ReevaluationRef != ref ||
		authority.SourceDecisionRef == authority.TargetDecisionRef ||
		authority.SourceDeliveryIntentRef == authority.TargetDeliveryIntentRef ||
		authority.SourceRoute == "" ||
		authority.TargetRoute == "" ||
		authority.SourceRoute == authority.TargetRoute ||
		authority.Mechanism == "" {
		return fmt.Errorf(
			"%w: delivery route reevaluation header",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

type DeliveryAuthorizationKind string

const (
	DeliveryAuthorizationNormal    DeliveryAuthorizationKind = "normal"
	DeliveryAuthorizationException DeliveryAuthorizationKind = "exception"
)

// DeliveryAuthorizationInactiveError represents an owner-confirmed or locally
// observed loss of liveness. Binding mismatches and malformed immutable facts
// use different errors so callers never hide corruption as a normal expiry.
type DeliveryAuthorizationInactiveError struct {
	AuthorizationRef string
	Reason           string
}

func (err *DeliveryAuthorizationInactiveError) Error() string {
	if err == nil {
		return ErrDeliveryAuthorizationInactive.Error()
	}
	if err.Reason == "" {
		return fmt.Sprintf(
			"%v: %s",
			ErrDeliveryAuthorizationInactive,
			err.AuthorizationRef,
		)
	}
	return fmt.Sprintf(
		"%v: %s: %s",
		ErrDeliveryAuthorizationInactive,
		err.AuthorizationRef,
		err.Reason,
	)
}

func (err *DeliveryAuthorizationInactiveError) Unwrap() error {
	return ErrDeliveryAuthorizationInactive
}

// DeliveryAuthorizationAuthority is the minimum WorkRun-owned projection of a
// PAD authorization. Its immutable ref remains the only persisted value.
type DeliveryAuthorizationAuthority struct {
	AuthorizationRef      string                    `json:"authorization_ref"`
	DecisionRef           string                    `json:"decision_ref"`
	Kind                  DeliveryAuthorizationKind `json:"kind"`
	DeliveryIntentRef     string                    `json:"delivery_intent_ref"`
	CandidateRef          string                    `json:"candidate_ref"`
	ReviewReceiptRef      string                    `json:"review_receipt_ref"`
	VerificationResultRef string                    `json:"verification_result_ref"`
	ValidatedAt           int64                     `json:"validated_at"`
	ObservedAt            int64                     `json:"observed_at"`
	ExpiresAt             int64                     `json:"expires_at"`
}

func (authority DeliveryAuthorizationAuthority) Validate(
	requestedRef string,
	state WorkRunState,
) error {
	for _, ref := range []string{
		authority.AuthorizationRef,
		authority.DecisionRef,
		authority.DeliveryIntentRef,
		authority.CandidateRef,
		authority.ReviewReceiptRef,
		authority.VerificationResultRef,
	} {
		if !validSHA256Ref(ref) {
			return errors.New(
				"delivery authorization authority has invalid immutable references",
			)
		}
	}
	if authority.AuthorizationRef != requestedRef {
		return fmt.Errorf(
			"%w: delivery authorization reference",
			ErrAuthorityBindingMismatch,
		)
	}
	switch authority.Kind {
	case DeliveryAuthorizationNormal, DeliveryAuthorizationException:
	default:
		return fmt.Errorf(
			"unsupported delivery authorization kind %q",
			authority.Kind,
		)
	}
	if authority.ValidatedAt <= 0 ||
		authority.ExpiresAt <= authority.ValidatedAt ||
		authority.ObservedAt < authority.ValidatedAt ||
		authority.ObservedAt >= authority.ExpiresAt {
		return errors.New("delivery authorization has an invalid validity window")
	}
	if state.Handoff == nil ||
		authority.DeliveryIntentRef != state.DeliveryIntentRef ||
		authority.CandidateRef != state.Handoff.CandidateRef ||
		authority.VerificationResultRef != state.VerificationResultRef ||
		authority.ReviewReceiptRef != state.ReviewReceiptRef {
		return fmt.Errorf(
			"%w: delivery authorization terminal facts",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

// ExplicitSDDRequestAuthorityPort resolves the owner-authored request that
// selects SDD before an implementation route decision exists. The authority
// must be durable and independently content-addressed; it cannot depend on the
// digest of the decision that later references it.
type ExplicitSDDRequestAuthorityPort interface {
	ResolveExplicitSDDRequest(
		context.Context,
		string,
	) (ExplicitSDDRequestAuthority, error)
}

type ExplicitSDDRequestAuthority struct {
	AuthorityRef      string
	WorkRunID         string
	DeliveryIntentRef string
}

func (authority ExplicitSDDRequestAuthority) Validate(
	ref string,
	workRunID string,
	deliveryIntentRef string,
) error {
	if !validSHA256Ref(authority.AuthorityRef) ||
		authority.AuthorityRef != ref ||
		authority.WorkRunID != workRunID ||
		!validSHA256Ref(authority.DeliveryIntentRef) ||
		authority.DeliveryIntentRef != deliveryIntentRef {
		return fmt.Errorf(
			"%w: explicit SDD request",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

// MutationCompletionAuthorityPort resolves the existing MMI completion record.
// WorkRun deliberately defines only a narrow projection so it neither imports
// the mutationintegrity package nor creates a second completion lifecycle.
type MutationCompletionAuthorityPort interface {
	ResolveMutationCompletion(
		context.Context,
		string,
	) (MutationCompletionAuthority, error)
}

type MutationCompletionAuthority struct {
	CompletionRef string
	RepositoryRef string
	WorkRunID     string
	Route         string
	ScopeDigest   string
	Snapshot      reviewtransaction.Snapshot
}

func (authority MutationCompletionAuthority) Validate(
	completionRef string,
	workRunID string,
	handoff ImplementationHandoff,
) error {
	for _, ref := range []string{
		authority.CompletionRef,
		authority.RepositoryRef,
		authority.ScopeDigest,
	} {
		if !validSHA256Ref(ref) {
			return errors.New(
				"mutation completion authority has invalid immutable references",
			)
		}
	}
	if authority.CompletionRef != completionRef ||
		authority.CompletionRef != handoff.MutationCompletionRef ||
		authority.WorkRunID != workRunID ||
		authority.Route != string(handoff.Route) ||
		authority.ScopeDigest != handoff.ScopeDigest {
		return fmt.Errorf(
			"%w: mutation completion",
			ErrAuthorityBindingMismatch,
		)
	}
	subject, err := reviewtransaction.VerificationSubjectFromSnapshot(
		authority.Snapshot,
	)
	if err != nil {
		return err
	}
	if subject != handoff.Subject ||
		subject.SnapshotIdentity != handoff.CandidateRef {
		return fmt.Errorf(
			"%w: mutation completion snapshot",
			ErrAuthorityBindingMismatch,
		)
	}
	return nil
}

// RouteAuthorityPort resolves accepted proposals and safe reroutes from the
// provider-owned decision repository. Explicit pre-route requests use the
// separate ExplicitSDDRequestAuthorityPort.
type RouteAuthorityPort interface {
	ResolveRouteSelection(context.Context, string) (RouteSelectionAuthority, error)
}

type RouteSelectionAuthority struct {
	DecisionRef            string
	PendingDecisionDigest  string
	SelectedRoute          ImplementationRoute
	SelectedDecisionDigest string
}

func (authority RouteSelectionAuthority) Validate(
	ref string,
	pendingDigest string,
	selectedRoute ImplementationRoute,
	selectedDecisionDigest string,
) error {
	if !validSHA256Ref(authority.DecisionRef) || authority.DecisionRef != ref ||
		!validSHA256Ref(authority.PendingDecisionDigest) ||
		authority.PendingDecisionDigest != pendingDigest ||
		authority.SelectedRoute != selectedRoute {
		return fmt.Errorf("%w: implementation route selection", ErrAuthorityBindingMismatch)
	}
	if selectedDecisionDigest == "" {
		if authority.SelectedDecisionDigest != "" {
			return fmt.Errorf("%w: unexpected selected route decision", ErrAuthorityBindingMismatch)
		}
		return nil
	}
	if !validSHA256Ref(authority.SelectedDecisionDigest) ||
		authority.SelectedDecisionDigest != selectedDecisionDigest {
		return fmt.Errorf("%w: selected route decision", ErrAuthorityBindingMismatch)
	}
	return nil
}

// SDDAuthorityPort resolves an accepted SDD run and idempotently binds the
// common verification reservation into the exact SDD phase attempt. WorkRun
// never copies the SDD attempt ledger.
type SDDAuthorityPort interface {
	ResolveRun(context.Context, string) (SDDRunAuthority, error)
	BindVerificationReservation(context.Context, SDDReservationBinding) error
}

type SDDRunAuthority struct {
	RunRef             string
	WorkRunID          string
	RouteAcceptanceRef string
}

func (authority SDDRunAuthority) Validate(
	runRef string,
	workRunID string,
	acceptanceRef string,
) error {
	if !validOpaqueRef(authority.RunRef) || authority.RunRef != runRef ||
		authority.WorkRunID != workRunID ||
		!validSHA256Ref(authority.RouteAcceptanceRef) ||
		authority.RouteAcceptanceRef != acceptanceRef {
		return fmt.Errorf("%w: SDD run", ErrAuthorityBindingMismatch)
	}
	return nil
}

type SDDReservationBinding struct {
	WorkRunID       string
	WorkRunRevision string
	SDDRunRef       string
	ReservationRef  string
	ActionTicketRef string
}

func (binding SDDReservationBinding) Validate() error {
	if !workRunIDPattern.MatchString(binding.WorkRunID) ||
		!validSHA256Ref(binding.WorkRunRevision) ||
		!validOpaqueRef(binding.SDDRunRef) ||
		!validSHA256Ref(binding.ReservationRef) ||
		!validSHA256Ref(binding.ActionTicketRef) {
		return errors.New("invalid SDD verification reservation binding")
	}
	return nil
}

// VerificationAuthorityPort resolves every RAR-owned plan, result, and receipt
// preimage plus provider-owned forecast/consent observations. A review receipt
// is resolved by its exact receipt/result pair and then revalidated against the
// WorkRun candidate. WorkRun persists only immutable references.
type VerificationAuthorityPort interface {
	ResolveForecast(context.Context, string) (VerificationForecastAuthority, error)
	ResolveDisposition(context.Context, string) (VerificationDispositionAuthority, error)
	ResolveResult(context.Context, string) (VerificationResultAuthority, error)
	ResolveReviewReceipt(
		ctx context.Context,
		receiptRef string,
		resultRef string,
	) (ReviewReceiptAuthority, error)
}

type VerificationForecastAuthority struct {
	AvailabilityRef string
	Applicability   reviewtransaction.VerificationApplicability
	Registry        reviewtransaction.VerificationPlanRegistry
	Plan            reviewtransaction.VerificationPlan
	PlanRevisionRef string
	Availability    ForecastAvailability
	DiagnosticRefs  []string
}

func (authority VerificationForecastAuthority) Validate() error {
	if !validSHA256Ref(authority.AvailabilityRef) ||
		!validSHA256Ref(authority.PlanRevisionRef) {
		return errors.New("verification forecast authority has invalid immutable references")
	}
	if err := reviewtransaction.ValidateVerificationPlan(
		authority.Applicability,
		authority.Registry,
		authority.Plan,
	); err != nil {
		return err
	}
	if !isCanonicalSHA256Refs(authority.DiagnosticRefs) {
		return errors.New("verification forecast authority diagnostics must be canonical")
	}
	switch authority.Availability {
	case ForecastAvailable, ForecastNotRequired, ForecastPartial,
		ForecastUnavailable, ForecastUnknown:
	default:
		return fmt.Errorf("unsupported owner forecast availability %q", authority.Availability)
	}
	return nil
}

func (authority VerificationForecastAuthority) MatchesInput(
	input VerificationForecastInput,
) error {
	if err := authority.Validate(); err != nil {
		return err
	}
	if authority.AvailabilityRef != input.AvailabilityRef ||
		authority.Applicability.Digest != input.Applicability.Digest ||
		authority.Registry.Digest != input.Registry.Digest ||
		authority.Plan.Digest != input.Plan.Digest ||
		authority.PlanRevisionRef != input.PlanRevisionRef ||
		authority.Availability != input.Availability ||
		!equalStrings(authority.DiagnosticRefs, input.DiagnosticRefs) {
		return fmt.Errorf("%w: verification forecast", ErrAuthorityBindingMismatch)
	}
	return nil
}

type VerificationDispositionAuthority struct {
	DecisionRef    string
	ForecastDigest string
	AssumptionsRef string
	Kind           VerificationDispositionKind
	ActorRef       string
	RunnerRef      string
}

func (authority VerificationDispositionAuthority) Validate(
	forecast VerificationForecast,
) error {
	for _, ref := range []string{
		authority.DecisionRef,
		authority.ForecastDigest,
		authority.AssumptionsRef,
		authority.ActorRef,
	} {
		if !validSHA256Ref(ref) {
			return errors.New("verification disposition authority has invalid immutable references")
		}
	}
	if authority.ForecastDigest != forecast.Digest {
		return fmt.Errorf("%w: verification disposition forecast", ErrAuthorityBindingMismatch)
	}
	disposition, err := newVerificationDisposition(
		forecast,
		authority.Kind,
		authority.AssumptionsRef,
		authority.ActorRef,
		authority.DecisionRef,
		authority.RunnerRef,
	)
	if err != nil {
		return err
	}
	return disposition.ValidateFor(forecast)
}

type VerificationResultAuthority struct {
	Applicability reviewtransaction.VerificationApplicability
	Registry      reviewtransaction.VerificationPlanRegistry
	Plan          reviewtransaction.VerificationPlan
	Result        reviewtransaction.VerificationResultRef
}

func (authority VerificationResultAuthority) Validate(ref string) error {
	if authority.Result.ResultRef != ref {
		return fmt.Errorf("%w: verification result", ErrAuthorityBindingMismatch)
	}
	if err := reviewtransaction.ValidateVerificationResultRef(
		authority.Applicability,
		authority.Registry,
		authority.Plan,
		authority.Result,
	); err != nil {
		return err
	}
	return nil
}

type ReviewReceiptAuthority struct {
	ReceiptRef            string
	CandidateRef          string
	VerificationResultRef string
}

func (authority ReviewReceiptAuthority) Validate(
	receiptRef string,
	candidateRef string,
	resultRef string,
) error {
	for _, ref := range []string{
		receiptRef,
		candidateRef,
		resultRef,
		authority.ReceiptRef,
		authority.CandidateRef,
		authority.VerificationResultRef,
	} {
		if !validSHA256Ref(ref) {
			return fmt.Errorf(
				"%w: terminal review receipt contains an invalid immutable reference",
				ErrAuthorityBindingMismatch,
			)
		}
	}
	if authority.ReceiptRef != receiptRef ||
		authority.CandidateRef != candidateRef ||
		authority.VerificationResultRef != resultRef {
		return fmt.Errorf("%w: terminal review receipt", ErrAuthorityBindingMismatch)
	}
	return nil
}

// LaunchAuthorityPort is the HCR-owned activation seam. It creates an opaque
// capability only after WorkRun has durably reserved the exact action. The
// claim callback is invoked by HCR immediately before process creation.
type LaunchAuthorityPort interface {
	ActivateLaunch(
		context.Context,
		hostruntime.LaunchBinding,
		hostruntime.LaunchClaim,
	) (*hostruntime.LaunchCapability, error)
}

type AuthorityPorts struct {
	PAD                       PADAuthorityPort
	DeliveryResult            DeliveryResultAuthorityPort
	ProductiveExecutionResult ProductiveExecutionResultAuthorityPort
	ProductiveDiagnostic      ProductiveDiagnosticAuthorityPort
	ProductiveReconciliation  ProductiveReconciliationAuthorityPort
	DeliveryRoute             DeliveryRouteReevaluationAuthorityPort
	ExplicitSDDRequest        ExplicitSDDRequestAuthorityPort
	MutationCompletion        MutationCompletionAuthorityPort
	Route                     RouteAuthorityPort
	SDD                       SDDAuthorityPort
	Verification              VerificationAuthorityPort
	Launch                    LaunchAuthorityPort
}
