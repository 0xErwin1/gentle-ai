package workprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

const (
	productiveStartAuthoritySchema    = "gentle-ai.productive-start-authority/v1"
	productiveAdvanceAttemptSchema    = "gentle-ai.productive-advance-attempt/v1"
	productiveAdvanceDiagnosticSchema = "gentle-ai.productive-advance-diagnostic/v1"
	productiveDeliveryResultSchema    = "gentle-ai.productive-delivery-result/v1"
	productiveDeliveryResultDomain    = "gentle-ai.productive-delivery-result-ref/v1"
	maximumProductiveAdvanceRecord    = 1 << 20
)

type productiveStartAuthority struct {
	Schema            string   `json:"schema"`
	RepositoryRef     string   `json:"repositoryRef"`
	WorkRunID         string   `json:"workRunId"`
	DeliveryIntentRef string   `json:"deliveryIntentRef"`
	Outcome           string   `json:"outcome"`
	ScopeDigest       string   `json:"scopeDigest"`
	BaseRevision      string   `json:"baseRevision"`
	ScopeSelectors    []string `json:"scopeSelectors"`
}

type productiveAdvanceDiagnostic struct {
	Schema                   string                            `json:"schema"`
	RepositoryRef            string                            `json:"repositoryRef"`
	WorkRunID                string                            `json:"workRunId"`
	WorkRevision             string                            `json:"workRevision"`
	DeliveryIntentRef        string                            `json:"deliveryIntentRef"`
	Handoff                  *workrun.ImplementationHandoff    `json:"handoff,omitempty"`
	VerificationResultRef    string                            `json:"verificationResultRef,omitempty"`
	ReviewReceiptRef         string                            `json:"reviewReceiptRef,omitempty"`
	DeliveryAuthorizationRef string                            `json:"deliveryAuthorizationRef,omitempty"`
	Code                     workrun.WorkAdvanceDiagnosticCode `json:"code"`
	Message                  string                            `json:"message"`
}

type productiveAdvanceAttempt struct {
	Schema           string `json:"schema"`
	RepositoryRef    string `json:"repositoryRef"`
	WorkRunID        string `json:"workRunId"`
	ExpectedRevision string `json:"expectedRevision"`
}

type productiveAdvanceDecision struct {
	Schema           string `json:"schema"`
	RepositoryRef    string `json:"repositoryRef"`
	WorkRunID        string `json:"workRunId"`
	ExpectedRevision string `json:"expectedRevision"`
	DecisionRef      string `json:"decisionRef"`
}

type productiveDeliveryResultRecord struct {
	Schema                string                            `json:"schema"`
	RepositoryRef         string                            `json:"repositoryRef"`
	WorkRunID             string                            `json:"workRunId"`
	ResultRef             string                            `json:"resultRef"`
	AuthorizationRef      string                            `json:"authorizationRef"`
	DeliveryIntentRef     string                            `json:"deliveryIntentRef"`
	CandidateRef          string                            `json:"candidateRef"`
	ReviewReceiptRef      string                            `json:"reviewReceiptRef"`
	VerificationResultRef string                            `json:"verificationResultRef"`
	Execution             deliveryadmission.ExecutionResult `json:"execution"`
}

type productiveAdvanceStore struct {
	lease     *reviewtransaction.RepositoryIdentityLease
	root      string
	workRunID string
	pad       *PADWorkRunAdapter
}

func openProductiveAdvanceStore(
	ctx context.Context,
	lease *reviewtransaction.RepositoryIdentityLease,
	workRunID string,
) (productiveAdvanceStore, error) {
	if lease == nil || !workRunIDPattern.MatchString(workRunID) {
		return productiveAdvanceStore{}, errors.New(
			"productive advance store requires a repository lease and work run",
		)
	}
	if err := lease.Validate(ctx); err != nil {
		return productiveAdvanceStore{}, err
	}
	root := productiveAdvanceStoreRoot(lease, workRunID)
	if err := ensureProductiveAdvanceDirectory(
		lease.Identity().GitCommonDir,
		root,
	); err != nil {
		return productiveAdvanceStore{}, err
	}
	return productiveAdvanceStore{lease: lease, root: root, workRunID: workRunID}, nil
}

// existingProductiveAdvanceStore derives the owner path without creating or
// opening it. A read-only status path reaches the filesystem only if WorkRun
// already binds a terminal result that must be resolved.
func existingProductiveAdvanceStore(
	lease *reviewtransaction.RepositoryIdentityLease,
	workRunID string,
) (productiveAdvanceStore, error) {
	if lease == nil || !workRunIDPattern.MatchString(workRunID) {
		return productiveAdvanceStore{}, errors.New(
			"productive advance store requires a repository lease and work run",
		)
	}
	return productiveAdvanceStore{
		lease:     lease,
		root:      productiveAdvanceStoreRoot(lease, workRunID),
		workRunID: workRunID,
	}, nil
}

func productiveAdvanceStoreRoot(
	lease *reviewtransaction.RepositoryIdentityLease,
	workRunID string,
) string {
	return filepath.Join(
		lease.Identity().GitCommonDir,
		"gentle-ai",
		"productive-advance",
		"v1",
		lease.StorageKey(),
		workRunID,
	)
}

func (store productiveAdvanceStore) publishStartAuthority(
	ctx context.Context,
	authority productiveStartAuthority,
) error {
	if err := store.validate(ctx); err != nil {
		return err
	}
	selectors, err := canonicalProductiveSelectors(authority.ScopeSelectors)
	if err != nil {
		return err
	}
	authority.ScopeSelectors = selectors
	if authority.Schema != productiveStartAuthoritySchema ||
		authority.RepositoryRef != store.lease.Identity().RepositoryRef ||
		authority.WorkRunID != store.workRunID ||
		!validPADImmutableRef(authority.DeliveryIntentRef) ||
		(OutcomeStartRequest{Outcome: authority.Outcome}).validate() != nil ||
		!validPADImmutableRef(authority.ScopeDigest) ||
		validateOwnerOutcomeToken("base revision", authority.BaseRevision) != nil {
		return errors.New("productive start authority is invalid")
	}
	return store.publishJSON(ctx, "start-authority.json", authority)
}

func (store productiveAdvanceStore) startAuthority(
	ctx context.Context,
) (productiveStartAuthority, error) {
	var authority productiveStartAuthority
	if err := store.readJSON(ctx, "start-authority.json", &authority); err != nil {
		return productiveStartAuthority{}, err
	}
	selectors, err := canonicalProductiveSelectors(authority.ScopeSelectors)
	if err != nil ||
		!equalProductiveStrings(selectors, authority.ScopeSelectors) ||
		authority.Schema != productiveStartAuthoritySchema ||
		authority.RepositoryRef != store.lease.Identity().RepositoryRef ||
		authority.WorkRunID != store.workRunID ||
		!validPADImmutableRef(authority.DeliveryIntentRef) ||
		(OutcomeStartRequest{Outcome: authority.Outcome}).validate() != nil ||
		!validPADImmutableRef(authority.ScopeDigest) ||
		validateOwnerOutcomeToken("base revision", authority.BaseRevision) != nil {
		return productiveStartAuthority{}, errors.New(
			"productive start authority is corrupt",
		)
	}
	return authority, nil
}

func (store productiveAdvanceStore) publishDiagnostic(
	ctx context.Context,
	state workrun.WorkRunState,
	code workrun.WorkAdvanceDiagnosticCode,
) (workrun.WorkAdvanceDiagnosticV1, error) {
	if err := store.validate(ctx); err != nil {
		return workrun.WorkAdvanceDiagnosticV1{}, err
	}
	message, ok := workrun.WorkAdvanceDiagnosticMessage(code)
	if !ok {
		return workrun.WorkAdvanceDiagnosticV1{}, errors.New(
			"productive diagnostic code is unsupported",
		)
	}
	diagnostic := productiveAdvanceDiagnostic{
		Schema:                   productiveAdvanceDiagnosticSchema,
		RepositoryRef:            store.lease.Identity().RepositoryRef,
		WorkRunID:                store.workRunID,
		WorkRevision:             state.Revision,
		DeliveryIntentRef:        state.DeliveryIntentRef,
		Handoff:                  cloneProductiveHandoff(state.Handoff),
		VerificationResultRef:    state.VerificationResultRef,
		ReviewReceiptRef:         state.ReviewReceiptRef,
		DeliveryAuthorizationRef: state.DeliveryAuthorizationRef,
		Code:                     code,
		Message:                  message,
	}
	ref, err := productiveAdvanceDiagnosticReference(diagnostic)
	if err != nil {
		return workrun.WorkAdvanceDiagnosticV1{}, err
	}
	if err := store.publishJSON(
		ctx,
		"diagnostic-"+productiveAdvanceRevisionKey(ref)+".json",
		diagnostic,
	); err != nil {
		return workrun.WorkAdvanceDiagnosticV1{}, err
	}
	resolved, err := store.resolveDiagnostic(ctx, ref)
	if err != nil {
		return workrun.WorkAdvanceDiagnosticV1{}, err
	}
	if resolved.Code != code || resolved.Message != message {
		return workrun.WorkAdvanceDiagnosticV1{}, errors.New(
			"productive diagnostic durability check failed",
		)
	}
	public := workrun.WorkAdvanceDiagnosticV1{
		Ref: ref, Code: code, Message: message,
	}
	if err := public.Validate(); err != nil {
		return workrun.WorkAdvanceDiagnosticV1{}, err
	}
	return public, nil
}

func (store productiveAdvanceStore) resolveDiagnostic(
	ctx context.Context,
	ref string,
) (productiveAdvanceDiagnostic, error) {
	if !validPADImmutableRef(ref) {
		return productiveAdvanceDiagnostic{}, errors.New(
			"productive diagnostic reference is invalid",
		)
	}
	var diagnostic productiveAdvanceDiagnostic
	if err := store.readJSON(
		ctx,
		"diagnostic-"+productiveAdvanceRevisionKey(ref)+".json",
		&diagnostic,
	); err != nil {
		return productiveAdvanceDiagnostic{}, err
	}
	computed, err := productiveAdvanceDiagnosticReference(diagnostic)
	if err != nil ||
		diagnostic.Schema != productiveAdvanceDiagnosticSchema ||
		diagnostic.RepositoryRef != store.lease.Identity().RepositoryRef ||
		diagnostic.WorkRunID != store.workRunID ||
		computed != ref {
		return productiveAdvanceDiagnostic{}, errors.New(
			"productive diagnostic record is corrupt",
		)
	}
	return diagnostic, nil
}

func (store productiveAdvanceStore) ResolveProductiveDiagnostic(
	ctx context.Context,
	ref string,
) (workrun.ProductiveDiagnosticAuthority, error) {
	diagnostic, err := store.resolveDiagnostic(ctx, ref)
	if err != nil {
		return workrun.ProductiveDiagnosticAuthority{}, err
	}
	return workrun.ProductiveDiagnosticAuthority{
		Diagnostic: workrun.WorkAdvanceDiagnosticV1{
			Ref:     ref,
			Code:    diagnostic.Code,
			Message: diagnostic.Message,
		},
		RepositoryRef:            diagnostic.RepositoryRef,
		WorkRunID:                diagnostic.WorkRunID,
		SourceRevision:           diagnostic.WorkRevision,
		DeliveryIntentRef:        diagnostic.DeliveryIntentRef,
		Handoff:                  cloneProductiveHandoff(diagnostic.Handoff),
		VerificationResultRef:    diagnostic.VerificationResultRef,
		ReviewReceiptRef:         diagnostic.ReviewReceiptRef,
		DeliveryAuthorizationRef: diagnostic.DeliveryAuthorizationRef,
	}, nil
}

func (store productiveAdvanceStore) resolveDiagnosticForState(
	ctx context.Context,
	ref string,
	state workrun.WorkRunState,
) (productiveAdvanceDiagnostic, error) {
	diagnostic, err := store.resolveDiagnostic(ctx, ref)
	if err != nil {
		return productiveAdvanceDiagnostic{}, err
	}
	if state.WorkRunID != store.workRunID ||
		state.ProductiveBlockerRef != ref ||
		state.ProductiveBlockerSourceRevision != diagnostic.WorkRevision ||
		state.DeliveryIntentRef != diagnostic.DeliveryIntentRef ||
		!reflect.DeepEqual(state.Handoff, diagnostic.Handoff) ||
		state.VerificationResultRef != diagnostic.VerificationResultRef ||
		state.ReviewReceiptRef != diagnostic.ReviewReceiptRef ||
		state.DeliveryAuthorizationRef != diagnostic.DeliveryAuthorizationRef {
		return productiveAdvanceDiagnostic{}, errors.New(
			"productive diagnostic does not bind the terminal WorkRun stage",
		)
	}
	return diagnostic, nil
}

func productiveAdvanceDiagnosticReference(
	diagnostic productiveAdvanceDiagnostic,
) (string, error) {
	if diagnostic.Schema != productiveAdvanceDiagnosticSchema ||
		!validPADImmutableRef(diagnostic.RepositoryRef) ||
		!workRunIDPattern.MatchString(diagnostic.WorkRunID) ||
		!revisionPattern.MatchString(diagnostic.WorkRevision) ||
		!validPADImmutableRef(diagnostic.DeliveryIntentRef) ||
		diagnostic.Code == "" ||
		diagnostic.Message == "" ||
		strings.ContainsAny(string(diagnostic.Code)+diagnostic.Message, "\x00\r\n") {
		return "", errors.New("productive diagnostic is invalid")
	}
	message, ok := workrun.WorkAdvanceDiagnosticMessage(diagnostic.Code)
	if !ok || diagnostic.Message != message || len(diagnostic.Message) > 240 {
		return "", errors.New("productive diagnostic message is invalid")
	}
	if diagnostic.Handoff != nil {
		if err := diagnostic.Handoff.Validate(); err != nil {
			return "", err
		}
	}
	for _, ref := range []string{
		diagnostic.VerificationResultRef,
		diagnostic.ReviewReceiptRef,
		diagnostic.DeliveryAuthorizationRef,
	} {
		if ref != "" && !validPADImmutableRef(ref) {
			return "", errors.New(
				"productive diagnostic stage reference is invalid",
			)
		}
	}
	payload, err := json.Marshal(diagnostic)
	if err != nil {
		return "", err
	}
	return productiveAdvanceSHA256(payload), nil
}

func cloneProductiveHandoff(
	handoff *workrun.ImplementationHandoff,
) *workrun.ImplementationHandoff {
	if handoff == nil {
		return nil
	}
	clone := *handoff
	if handoff.DeclaredObligationRefs == nil {
		clone.DeclaredObligationRefs = nil
	} else {
		clone.DeclaredObligationRefs = make(
			[]string,
			len(handoff.DeclaredObligationRefs),
		)
		copy(
			clone.DeclaredObligationRefs,
			handoff.DeclaredObligationRefs,
		)
	}
	if handoff.EvidenceRefs == nil {
		clone.EvidenceRefs = nil
	} else {
		clone.EvidenceRefs = make([]string, len(handoff.EvidenceRefs))
		copy(clone.EvidenceRefs, handoff.EvidenceRefs)
	}
	return &clone
}

func (store productiveAdvanceStore) beginAttempt(
	ctx context.Context,
	expectedRevision string,
) error {
	if !revisionPattern.MatchString(expectedRevision) {
		return errors.New("productive advance expected revision is invalid")
	}
	return store.publishJSON(ctx, store.attemptName(expectedRevision), productiveAdvanceAttempt{
		Schema:           productiveAdvanceAttemptSchema,
		RepositoryRef:    store.lease.Identity().RepositoryRef,
		WorkRunID:        store.workRunID,
		ExpectedRevision: expectedRevision,
	})
}

func (store productiveAdvanceStore) attemptExists(
	ctx context.Context,
	expectedRevision string,
) (bool, error) {
	var attempt productiveAdvanceAttempt
	err := store.readJSON(ctx, store.attemptName(expectedRevision), &attempt)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if attempt.Schema != productiveAdvanceAttemptSchema ||
		attempt.RepositoryRef != store.lease.Identity().RepositoryRef ||
		attempt.WorkRunID != store.workRunID ||
		attempt.ExpectedRevision != expectedRevision {
		return false, errors.New("productive advance attempt is corrupt")
	}
	return true, nil
}

func (store productiveAdvanceStore) publishResult(
	ctx context.Context,
	result workrun.WorkAdvanceV1,
) error {
	if err := result.Validate(); err != nil {
		return err
	}
	return store.publishJSON(ctx, store.resultName(result.PreviousRevision), result)
}

func (store productiveAdvanceStore) publishDecision(
	ctx context.Context,
	expectedRevision string,
	decisionRef string,
) error {
	if !revisionPattern.MatchString(expectedRevision) ||
		!validPADImmutableRef(decisionRef) {
		return errors.New("productive advance decision is invalid")
	}
	return store.publishJSON(
		ctx,
		"decision-"+productiveAdvanceRevisionKey(expectedRevision)+".json",
		productiveAdvanceDecision{
			Schema:           "gentle-ai.productive-advance-decision/v1",
			RepositoryRef:    store.lease.Identity().RepositoryRef,
			WorkRunID:        store.workRunID,
			ExpectedRevision: expectedRevision,
			DecisionRef:      decisionRef,
		},
	)
}

func (store productiveAdvanceStore) decision(
	ctx context.Context,
	expectedRevision string,
) (string, bool, error) {
	var value productiveAdvanceDecision
	err := store.readJSON(
		ctx,
		"decision-"+productiveAdvanceRevisionKey(expectedRevision)+".json",
		&value,
	)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if value.Schema != "gentle-ai.productive-advance-decision/v1" ||
		value.RepositoryRef != store.lease.Identity().RepositoryRef ||
		value.WorkRunID != store.workRunID ||
		value.ExpectedRevision != expectedRevision ||
		!validPADImmutableRef(value.DecisionRef) {
		return "", false, errors.New("productive advance decision is corrupt")
	}
	return value.DecisionRef, true, nil
}

// publishDeliveryResult records an immutable owner index of the already
// validated PAD ExecutionResult. It is not a caller publication and contains
// no independently authored outcome.
func (store productiveAdvanceStore) publishDeliveryResult(
	ctx context.Context,
	state workrun.WorkRunState,
	execution deliveryadmission.ExecutionResult,
) (string, error) {
	if err := store.validate(ctx); err != nil {
		return "", err
	}
	admitted, err := store.resolveAdmittedDelivery(ctx, state.DeliveryIntentRef)
	if err != nil {
		return "", err
	}
	if state.WorkRunID != store.workRunID ||
		state.Handoff == nil ||
		!validPADImmutableRef(state.DeliveryIntentRef) ||
		!validPADImmutableRef(state.ReviewReceiptRef) ||
		!validPADImmutableRef(state.VerificationResultRef) ||
		execution.Schema != deliveryadmission.ExecutionResultContractV1 ||
		execution.Outcome != deliveryadmission.ExecutionSucceeded ||
		execution.AuthorizationRef != state.DeliveryAuthorizationRef ||
		execution.Route != admitted.Decision.Route ||
		execution.Candidate.Ref != "work-run:"+state.WorkRunID ||
		execution.Candidate.Digest != state.Handoff.CandidateRef ||
		!validPADImmutableRef(execution.EvidenceRef) ||
		!validPADImmutableRef(execution.CommandRef) ||
		!validPADHostingToken(execution.DeliveryRef) ||
		execution.CompletedAt <= 0 {
		return "", errors.New(
			"PAD execution result does not bind the terminal WorkRun",
		)
	}
	record := productiveDeliveryResultRecord{
		Schema:                productiveDeliveryResultSchema,
		RepositoryRef:         store.lease.Identity().RepositoryRef,
		WorkRunID:             state.WorkRunID,
		AuthorizationRef:      execution.AuthorizationRef,
		DeliveryIntentRef:     state.DeliveryIntentRef,
		CandidateRef:          state.Handoff.CandidateRef,
		ReviewReceiptRef:      state.ReviewReceiptRef,
		VerificationResultRef: state.VerificationResultRef,
		Execution:             execution,
	}
	record.ResultRef = productiveDeliveryResultReference(record)
	if err := store.publishJSON(
		ctx,
		"delivery-result-"+productiveAdvanceRevisionKey(record.ResultRef)+".json",
		record,
	); err != nil {
		return "", err
	}
	return record.ResultRef, nil
}

func (store productiveAdvanceStore) ResolveDeliveryResult(
	ctx context.Context,
	resultRef string,
) (workrun.DeliveryResultAuthority, error) {
	if !validPADImmutableRef(resultRef) {
		return workrun.DeliveryResultAuthority{}, errors.New(
			"delivery result reference is invalid",
		)
	}
	var record productiveDeliveryResultRecord
	if err := store.readJSON(
		ctx,
		"delivery-result-"+productiveAdvanceRevisionKey(resultRef)+".json",
		&record,
	); err != nil {
		return workrun.DeliveryResultAuthority{}, err
	}
	admitted, admittedErr := store.resolveAdmittedDelivery(
		ctx,
		record.DeliveryIntentRef,
	)
	if admittedErr != nil ||
		record.Schema != productiveDeliveryResultSchema ||
		record.RepositoryRef != store.lease.Identity().RepositoryRef ||
		record.WorkRunID != store.workRunID ||
		record.ResultRef != resultRef ||
		productiveDeliveryResultReference(record) != resultRef ||
		!validPADImmutableRef(record.AuthorizationRef) ||
		!validPADImmutableRef(record.DeliveryIntentRef) ||
		!validPADImmutableRef(record.CandidateRef) ||
		!validPADImmutableRef(record.ReviewReceiptRef) ||
		!validPADImmutableRef(record.VerificationResultRef) ||
		!validProductiveExecutionResult(record.Execution) ||
		record.Execution.AuthorizationRef != record.AuthorizationRef ||
		record.Execution.Route != admitted.Decision.Route ||
		record.Execution.Candidate.Ref != "work-run:"+store.workRunID ||
		record.Execution.Candidate.Digest != record.CandidateRef {
		return workrun.DeliveryResultAuthority{}, errors.New(
			"productive delivery result record is corrupt",
		)
	}
	return workrun.DeliveryResultAuthority{
		ResultRef: record.ResultRef, AuthorizationRef: record.AuthorizationRef,
		DeliveryIntentRef:     record.DeliveryIntentRef,
		CandidateRef:          record.CandidateRef,
		ReviewReceiptRef:      record.ReviewReceiptRef,
		VerificationResultRef: record.VerificationResultRef,
		DeliveryRef:           record.Execution.DeliveryRef,
		CompletedAt:           record.Execution.CompletedAt,
	}, nil
}

func (store productiveAdvanceStore) resolveAdmittedDelivery(
	ctx context.Context,
	intentRef string,
) (
	admitted deliveryadmission.AdmittedDecision,
	err error,
) {
	if store.pad == nil || !validPADImmutableRef(intentRef) {
		return deliveryadmission.AdmittedDecision{}, errors.New(
			"productive delivery result lacks PAD authority",
		)
	}
	opened, err := store.pad.openRepository(ctx)
	if err != nil {
		return deliveryadmission.AdmittedDecision{}, err
	}
	repository, ok := opened.(interface {
		ResolveAdmittedDecision(
			context.Context,
			string,
		) (deliveryadmission.AdmittedDecision, error)
	})
	if !ok {
		_ = opened.Close()
		return deliveryadmission.AdmittedDecision{}, errors.New(
			"PAD owner repository cannot resolve productive delivery intent",
		)
	}
	defer func() {
		if closeErr := opened.Close(); closeErr != nil {
			admitted = deliveryadmission.AdmittedDecision{}
			err = errors.Join(err, closeErr)
		}
	}()
	admitted, err = repository.ResolveAdmittedDecision(ctx, intentRef)
	if err != nil {
		return deliveryadmission.AdmittedDecision{}, err
	}
	if admitted.Decision.Disposition != deliveryadmission.AdmissionAdmitted ||
		admitted.Decision.IntentRef != intentRef ||
		admitted.Decision.Destination.RepositoryRef !=
			store.lease.Identity().RepositoryRef {
		return deliveryadmission.AdmittedDecision{}, errors.New(
			"productive delivery intent authority is corrupt",
		)
	}
	return admitted, nil
}

func productiveDeliveryResultReference(
	record productiveDeliveryResultRecord,
) string {
	preimage := struct {
		Schema                string                            `json:"schema"`
		RepositoryRef         string                            `json:"repositoryRef"`
		WorkRunID             string                            `json:"workRunId"`
		AuthorizationRef      string                            `json:"authorizationRef"`
		DeliveryIntentRef     string                            `json:"deliveryIntentRef"`
		CandidateRef          string                            `json:"candidateRef"`
		ReviewReceiptRef      string                            `json:"reviewReceiptRef"`
		VerificationResultRef string                            `json:"verificationResultRef"`
		Execution             deliveryadmission.ExecutionResult `json:"execution"`
	}{
		Schema:                record.Schema,
		RepositoryRef:         record.RepositoryRef,
		WorkRunID:             record.WorkRunID,
		AuthorizationRef:      record.AuthorizationRef,
		DeliveryIntentRef:     record.DeliveryIntentRef,
		CandidateRef:          record.CandidateRef,
		ReviewReceiptRef:      record.ReviewReceiptRef,
		VerificationResultRef: record.VerificationResultRef,
		Execution:             record.Execution,
	}
	return padDeliveryDigest(productiveDeliveryResultDomain, preimage)
}

func validProductiveExecutionResult(
	execution deliveryadmission.ExecutionResult,
) bool {
	return execution.Schema == deliveryadmission.ExecutionResultContractV1 &&
		execution.Outcome == deliveryadmission.ExecutionSucceeded &&
		validOutcomeDeliveryRoute(execution.Route) &&
		validPADImmutableRef(execution.CommandRef) &&
		validPADImmutableRef(execution.AuthorizationRef) &&
		validPADCandidateBinding(execution.Candidate) &&
		validPADHostingToken(execution.DeliveryRef) &&
		validPADImmutableRef(execution.EvidenceRef) &&
		execution.CompletedAt > 0
}

func (store productiveAdvanceStore) result(
	ctx context.Context,
	expectedRevision string,
	state workrun.WorkRunState,
	status workrun.WorkStatusV1,
) (workrun.WorkAdvanceV1, bool, error) {
	var result workrun.WorkAdvanceV1
	err := store.readJSON(ctx, store.resultName(expectedRevision), &result)
	if os.IsNotExist(err) {
		return workrun.WorkAdvanceV1{}, false, nil
	}
	if err != nil {
		return workrun.WorkAdvanceV1{}, false, err
	}
	if err := result.Validate(); err != nil ||
		result.PreviousRevision != expectedRevision ||
		result.Status.WorkRunID != store.workRunID ||
		state.WorkRunID != store.workRunID ||
		!reflect.DeepEqual(result.Status, status) {
		return workrun.WorkAdvanceV1{}, false, errors.New(
			"productive advance result is corrupt",
		)
	}
	switch result.Status.PublicState {
	case workrun.PublicStateReady:
		if state.DeliveryResultRef != result.DeliveryResultRef ||
			state.ProductiveBlockerRef != "" {
			return workrun.WorkAdvanceV1{}, false, errors.New(
				"productive advance result is not bound to the live WorkRun",
			)
		}
		authority, err := store.ResolveDeliveryResult(
			ctx,
			result.DeliveryResultRef,
		)
		if err != nil ||
			authority.Validate(result.DeliveryResultRef, state) != nil {
			return workrun.WorkAdvanceV1{}, false, errors.New(
				"productive advance result authority is unavailable",
			)
		}
	case workrun.PublicStateNeedsYourDecision:
		if result.Diagnostic == nil ||
			state.ProductiveBlockerRef != result.Diagnostic.Ref ||
			state.DeliveryResultRef != "" {
			return workrun.WorkAdvanceV1{}, false, errors.New(
				"productive advance result is not bound to the live WorkRun",
			)
		}
		diagnostic, err := store.resolveDiagnosticForState(
			ctx,
			result.Diagnostic.Ref,
			state,
		)
		if err != nil ||
			diagnostic.Code != result.Diagnostic.Code ||
			diagnostic.Message != result.Diagnostic.Message {
			return workrun.WorkAdvanceV1{}, false, errors.New(
				"productive advance diagnostic authority is unavailable",
			)
		}
	default:
		return workrun.WorkAdvanceV1{}, false, errors.New(
			"productive advance result is not terminal",
		)
	}
	return result, true, nil
}

func (store productiveAdvanceStore) attemptName(revision string) string {
	return "attempt-" + productiveAdvanceRevisionKey(revision) + ".json"
}

func (store productiveAdvanceStore) resultName(revision string) string {
	return "result-" + productiveAdvanceRevisionKey(revision) + ".json"
}

func productiveAdvanceRevisionKey(revision string) string {
	sum := sha256.Sum256([]byte("gentle-ai.productive-advance-revision/v1\x00" + revision))
	return hex.EncodeToString(sum[:])
}

func (store productiveAdvanceStore) validate(ctx context.Context) error {
	if store.lease == nil || store.root == "" ||
		!filepath.IsAbs(store.root) ||
		!workRunIDPattern.MatchString(store.workRunID) {
		return errors.New("productive advance store is not initialized")
	}
	if err := store.lease.Validate(ctx); err != nil {
		return err
	}
	if err := validatePrivateCoordinationDirectory(store.root); err != nil {
		if os.IsNotExist(err) {
			return err
		}
		return errors.New("productive advance store root is unsafe")
	}
	return nil
}

func (store productiveAdvanceStore) publishJSON(
	ctx context.Context,
	name string,
	value any,
) error {
	if err := store.validate(ctx); err != nil {
		return err
	}
	if name == "" || filepath.Base(name) != name {
		return errors.New("productive advance record name is unsafe")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if len(payload) > maximumProductiveAdvanceRecord {
		return errors.New("productive advance record exceeds its limit")
	}
	path := filepath.Join(store.root, name)
	if err := publishPrivateCoordinationImmutable(path, payload); err != nil {
		return err
	}
	published, err := readProductiveAuthorityFile(
		path,
		maximumProductiveAdvanceRecord,
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(published, payload) {
		return errors.New("productive advance immutable record conflict")
	}
	return nil
}

func (store productiveAdvanceStore) readJSON(
	ctx context.Context,
	name string,
	target any,
) error {
	if err := store.validate(ctx); err != nil {
		return err
	}
	if name == "" || filepath.Base(name) != name {
		return errors.New("productive advance record name is unsafe")
	}
	path := filepath.Join(store.root, name)
	payload, err := readProductiveAuthorityFile(
		path,
		maximumProductiveAdvanceRecord,
	)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("productive advance record is corrupt")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("productive advance record has trailing data")
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return err
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(canonical, payload) {
		return errors.New("productive advance record is not canonical")
	}
	return nil
}

func readProductiveAuthorityFile(path string, maximum int64) ([]byte, error) {
	return readProductiveFile(path, maximum, true)
}

func readProductiveRegularFile(path string, maximum int64) ([]byte, error) {
	return readProductiveFile(path, maximum, false)
}

func readProductiveFile(
	path string,
	maximum int64,
	private bool,
) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if maximum <= 0 ||
		coordinationPathUnsafe(path, before) ||
		!before.Mode().IsRegular() ||
		before.Mode()&os.ModeSymlink != 0 ||
		(private && !privateCoordinationPathSafe(path, before)) ||
		(private && !productiveAdvanceSingleLink(before)) ||
		before.Size() < 1 ||
		before.Size() > maximum {
		return nil, errors.New(
			"productive advance record is not a bounded regular file",
		)
	}
	file, err := openCoordinationPathNoFollow(path, false)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil ||
		!opened.Mode().IsRegular() ||
		opened.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(before, opened) ||
		(private && !privateOpenCoordinationPathSafe(file, opened)) ||
		(private && !productiveAdvanceOpenedSingleLink(file)) {
		return nil, errors.New(
			"productive advance record changed while opening",
		)
	}
	payload, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maximum {
		return nil, errors.New("productive advance record exceeds its limit")
	}
	after, err := os.Lstat(path)
	if err != nil ||
		!after.Mode().IsRegular() ||
		after.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(opened, after) ||
		(private && !privateCoordinationPathSafe(path, after)) ||
		(private && !privateOpenCoordinationPathSafe(file, opened)) ||
		(private && !productiveAdvanceSingleLink(after)) ||
		(private && !productiveAdvanceOpenedSingleLink(file)) ||
		after.Size() != int64(len(payload)) {
		return nil, errors.New(
			"productive advance record changed while reading",
		)
	}
	return payload, nil
}

func ensureProductiveAdvanceDirectory(base, target string) error {
	base = filepath.Clean(base)
	target = filepath.Clean(target)
	relative, err := filepath.Rel(base, target)
	if err != nil ||
		relative == "." ||
		relative == ".." ||
		filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("productive advance store escaped Git common directory")
	}
	if err := validateSharedCoordinationDirectory(base); err != nil {
		return err
	}
	current := base
	for index, part := range strings.Split(
		relative,
		string(filepath.Separator),
	) {
		if part == "" || part == "." || part == ".." {
			return errors.New("productive advance directory is unsafe")
		}
		parent := current
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		switch {
		case os.IsNotExist(err):
			if index == 0 {
				err = os.Mkdir(current, 0o755)
				if os.IsExist(err) {
					err = nil
				}
			} else {
				_, err = createPrivateCoordinationDirectory(current)
			}
			if err != nil {
				return err
			}
			if err := reviewtransaction.SyncReviewDirectory(parent); err != nil {
				return err
			}
			info, err = os.Lstat(current)
			if err != nil {
				return err
			}
		case err != nil:
			return err
		}
		if index == 0 {
			if err := validateSharedCoordinationDirectory(current); err != nil {
				return fmt.Errorf(
					"validate shared productive advance root: %w",
					err,
				)
			}
			continue
		}
		if info == nil || !info.IsDir() {
			return fmt.Errorf("unsafe productive advance directory %q", current)
		}
		if err := validatePrivateCoordinationDirectory(current); err != nil {
			return fmt.Errorf(
				"validate private productive advance directory: %w",
				err,
			)
		}
	}
	return nil
}

func canonicalProductiveSelectors(values []string) ([]string, error) {
	result := append([]string(nil), values...)
	for index, value := range result {
		clean := filepath.ToSlash(filepath.Clean(value))
		if value == "" || value != clean || filepath.IsAbs(value) ||
			clean == "." || clean == ".." ||
			strings.HasPrefix(clean, "../") ||
			strings.ContainsRune(value, '\x00') ||
			strings.Contains(value, "\\") {
			return nil, errors.New(
				"productive scope selectors must be concrete canonical paths",
			)
		}
		result[index] = clean
	}
	sort.Strings(result)
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, errors.New("productive scope selectors contain duplicates")
		}
	}
	return result, nil
}

func equalProductiveStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
