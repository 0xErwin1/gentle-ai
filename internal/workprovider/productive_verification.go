package workprovider

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

const (
	ProductiveVerificationCatalogRequestSchemaV1 = "gentle-ai.productive-verification-catalog-request/v1"
	ProductiveVerificationCatalogSchemaV1        = "gentle-ai.productive-verification-catalog/v1"
	ProductiveReviewRequestSchemaV1              = "gentle-ai.productive-review-request/v1"
	ProductiveReviewResultSchemaV1               = "gentle-ai.productive-review-result/v1"

	maximumProductiveVerificationActions = 32
	maximumProductiveVerificationTokens  = 256
	maximumProductiveReviewEvidence      = 256
	maximumProductiveActionDeadline      = 30 * time.Minute
)

// ProductiveActiveVerificationAuthority is the authenticated owner boundary
// missing from the original passive-only productive composition. It selects
// exact executable requirements and performs real review against an immutable
// artifact subject. Neither operation can publish PASS authority by itself:
// EPD and RAR still admit and retain the resulting evidence.
type ProductiveActiveVerificationAuthority interface {
	ResolveVerificationCatalog(
		context.Context,
		ProductiveVerificationCatalogRequest,
	) (ProductiveVerificationCatalog, error)
	ReviewCandidate(
		context.Context,
		ProductiveReviewRequest,
	) (ProductiveReviewResult, error)
}

// ProductiveVerificationCatalogRequest binds owner action selection to one
// immutable candidate. Paths are included only to let the owner choose scoped
// checks; the native snapshot remains the authority for their canonical form.
type ProductiveVerificationCatalogRequest struct {
	Schema  string                                `json:"schema"`
	Subject reviewtransaction.VerificationSubject `json:"subject"`
	Paths   []string                              `json:"paths"`
	Risk    reviewtransaction.RiskAssessment      `json:"risk"`
}

func NewProductiveVerificationCatalogRequest(
	subject reviewtransaction.VerificationSubject,
	paths []string,
	risk reviewtransaction.RiskAssessment,
) (ProductiveVerificationCatalogRequest, error) {
	request := ProductiveVerificationCatalogRequest{
		Schema:  ProductiveVerificationCatalogRequestSchemaV1,
		Subject: subject,
		Paths:   append([]string(nil), paths...),
		Risk:    risk,
	}
	if err := request.Validate(); err != nil {
		return ProductiveVerificationCatalogRequest{}, err
	}
	return request, nil
}

func (request ProductiveVerificationCatalogRequest) Validate() error {
	if request.Schema != ProductiveVerificationCatalogRequestSchemaV1 {
		return errors.New("unsupported productive verification catalog request")
	}
	if err := request.Subject.Validate(); err != nil {
		return err
	}
	if len(request.Paths) == 0 ||
		!canonicalProductiveLogicalPaths(request.Paths) {
		return errors.New(
			"productive verification catalog request paths must be canonical",
		)
	}
	switch request.Risk.Level {
	case reviewtransaction.RiskLow,
		reviewtransaction.RiskMedium,
		reviewtransaction.RiskHigh:
	default:
		return errors.New(
			"productive verification catalog request requires native risk",
		)
	}
	if request.Risk.ChangedLines < 0 || request.Risk.Reasons == nil {
		return errors.New(
			"productive verification catalog request risk is incomplete",
		)
	}
	return nil
}

// ProductiveVerificationAction is owner-selected literal process input. Cost
// and RequiresConsent are coordination facts, never launch authority.
type ProductiveVerificationAction struct {
	ID                   string                             `json:"id"`
	Program              string                             `json:"program"`
	Args                 []string                           `json:"args"`
	CWD                  string                             `json:"cwd"`
	Environment          map[string]string                  `json:"environment"`
	Capability           string                             `json:"capability"`
	Cost                 reviewtransaction.VerificationCost `json:"cost"`
	RequiresConsent      bool                               `json:"requiresConsent"`
	DeadlineMilliseconds int64                              `json:"deadlineMilliseconds"`
	OutputLimits         hostruntime.StreamLimits           `json:"outputLimits"`
	RedactionLiterals    []string                           `json:"redactionLiterals"`
}

func (action ProductiveVerificationAction) Validate() error {
	if action.ID == "" ||
		action.ID != strings.TrimSpace(action.ID) ||
		action.Capability == "" ||
		action.Capability != strings.TrimSpace(action.Capability) ||
		strings.ContainsAny(action.ID, " \t\r\n\x00") ||
		strings.ContainsAny(action.Capability, " \t\r\n\x00") {
		return errors.New(
			"productive verification action requires canonical identifiers",
		)
	}
	if !filepath.IsAbs(action.Program) ||
		!filepath.IsAbs(action.CWD) ||
		action.Args == nil ||
		action.Environment == nil ||
		action.RedactionLiterals == nil {
		return errors.New(
			"productive verification action requires explicit absolute process input",
		)
	}
	if action.DeadlineMilliseconds <= 0 ||
		action.DeadlineMilliseconds >
			maximumProductiveActionDeadline.Milliseconds() {
		return errors.New(
			"productive verification action deadline is outside the native bound",
		)
	}
	if action.OutputLimits.StdoutBytes <= 0 ||
		action.OutputLimits.StderrBytes <= 0 ||
		action.OutputLimits.StdoutBytes > hostruntime.MaxRetainedStreamBytes ||
		action.OutputLimits.StderrBytes > hostruntime.MaxRetainedStreamBytes ||
		action.OutputLimits.StdoutBytes+action.OutputLimits.StderrBytes >
			hostruntime.MaxRetainedOutputBytes {
		return errors.New(
			"productive verification action output limits are invalid",
		)
	}
	switch action.Cost {
	case reviewtransaction.VerificationCostQuick,
		reviewtransaction.VerificationCostLong,
		reviewtransaction.VerificationCostVeryLong,
		reviewtransaction.VerificationCostUnknown:
	default:
		return errors.New(
			"productive verification action cost is unsupported",
		)
	}
	if len(action.Args) > maximumProductiveVerificationTokens ||
		len(action.Environment) > maximumProductiveVerificationTokens ||
		len(action.RedactionLiterals) > maximumProductiveVerificationTokens {
		return errors.New(
			"productive verification action exceeds its structural bound",
		)
	}
	redactions := make(map[string]struct{}, len(action.RedactionLiterals))
	for _, argument := range action.Args {
		if strings.ContainsRune(argument, '\x00') {
			return errors.New(
				"productive verification action argument contains NUL",
			)
		}
	}
	for name, value := range action.Environment {
		if name == "" || strings.ContainsAny(name, "=\x00") ||
			strings.ContainsRune(value, '\x00') {
			return errors.New(
				"productive verification action environment is invalid",
			)
		}
	}
	for _, literal := range action.RedactionLiterals {
		if literal == "" || strings.ContainsRune(literal, '\x00') {
			return errors.New(
				"productive verification action redaction is invalid",
			)
		}
		if _, duplicate := redactions[literal]; duplicate {
			return errors.New(
				"productive verification action redactions must be unique",
			)
		}
		redactions[literal] = struct{}{}
	}
	return nil
}

func (action ProductiveVerificationAction) deadline() time.Duration {
	return time.Duration(action.DeadlineMilliseconds) * time.Millisecond
}

type ProductiveVerificationCatalog struct {
	Schema  string                                `json:"schema"`
	Subject reviewtransaction.VerificationSubject `json:"subject"`
	Actions []ProductiveVerificationAction        `json:"actions"`
}

func (catalog ProductiveVerificationCatalog) ValidateFor(
	request ProductiveVerificationCatalogRequest,
) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if catalog.Schema != ProductiveVerificationCatalogSchemaV1 ||
		catalog.Subject != request.Subject ||
		catalog.Actions == nil ||
		len(catalog.Actions) > maximumProductiveVerificationActions {
		return errors.New(
			"productive verification catalog does not bind its request",
		)
	}
	for index, action := range catalog.Actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf(
				"productive verification action %d: %w",
				index,
				err,
			)
		}
		if index > 0 && catalog.Actions[index-1].ID >= action.ID {
			return errors.New(
				"productive verification catalog actions must be sorted with unique IDs",
			)
		}
	}
	return nil
}

// ProductiveReviewRequest contains every byte and manifest entry needed for a
// reviewer to inspect the exact frozen candidate. RAR later re-derives and
// admits the result; the connector response alone is not review authority.
type ProductiveReviewRequest struct {
	Schema              string                                       `json:"schema"`
	Subject             reviewtransaction.ArtifactSubject            `json:"subject"`
	CandidateDiff       reviewtransaction.FrozenCandidateDiff        `json:"candidateDiff"`
	ChangedPathManifest []reviewtransaction.ChangedPathManifestEntry `json:"changedPathManifest"`
}

func NewProductiveReviewRequest(
	subject reviewtransaction.ArtifactSubject,
	frozen reviewtransaction.FrozenCandidateContext,
) (ProductiveReviewRequest, error) {
	request := ProductiveReviewRequest{
		Schema:        ProductiveReviewRequestSchemaV1,
		Subject:       subject,
		CandidateDiff: frozen.CandidateDiff,
		ChangedPathManifest: append(
			[]reviewtransaction.ChangedPathManifestEntry(nil),
			frozen.ChangedPathManifest...,
		),
	}
	if err := request.Validate(); err != nil {
		return ProductiveReviewRequest{}, err
	}
	return request, nil
}

func (request ProductiveReviewRequest) Validate() error {
	if request.Schema != ProductiveReviewRequestSchemaV1 {
		return errors.New("unsupported productive review request")
	}
	if err := reviewtransaction.ValidateArtifactSubject(
		request.Subject,
	); err != nil {
		return err
	}
	if _, err := request.CandidateDiff.Bytes(); err != nil ||
		request.CandidateDiff.SHA256 != request.Subject.CandidateDiffSHA256 {
		return errors.New(
			"productive review request diff does not bind its artifact subject",
		)
	}
	digest, err := reviewtransaction.ChangedPathManifestDigest(
		request.ChangedPathManifest,
	)
	if err != nil ||
		digest != request.Subject.ChangedPathManifestSHA256 {
		return errors.New(
			"productive review request manifest does not bind its artifact subject",
		)
	}
	return nil
}

type ProductiveReviewResult struct {
	Schema      string                               `json:"schema"`
	SubjectHash string                               `json:"subjectHash"`
	Inspection  reviewtransaction.ArtifactInspection `json:"inspection"`
	Findings    []reviewtransaction.Finding          `json:"findings"`
	Evidence    []string                             `json:"evidence"`
}

func (result ProductiveReviewResult) ValidateFor(
	request ProductiveReviewRequest,
) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if result.Schema != ProductiveReviewResultSchemaV1 ||
		result.SubjectHash != request.Subject.SubjectHash ||
		result.Findings == nil ||
		result.Evidence == nil ||
		len(result.Findings) > maximumProductiveReviewEvidence ||
		len(result.Evidence) > maximumProductiveReviewEvidence {
		return errors.New(
			"productive reviewer result does not bind its request",
		)
	}
	return nil
}

func (connector *HTTPSProductiveRuntimeConnector) ResolveVerificationCatalog(
	ctx context.Context,
	request ProductiveVerificationCatalogRequest,
) (ProductiveVerificationCatalog, error) {
	if err := connector.ready(ctx); err != nil {
		return ProductiveVerificationCatalog{}, err
	}
	if err := request.Validate(); err != nil {
		return ProductiveVerificationCatalog{}, err
	}
	var catalog ProductiveVerificationCatalog
	if err := connector.call(
		ctx,
		ProductiveRuntimeOperationVerificationCatalog,
		request,
		&catalog,
	); err != nil {
		return ProductiveVerificationCatalog{}, err
	}
	if err := catalog.ValidateFor(request); err != nil {
		return ProductiveVerificationCatalog{}, fmt.Errorf(
			"%w: %v",
			ErrProductiveRuntimeBindingMismatch,
			err,
		)
	}
	return catalog, nil
}

func (connector *HTTPSProductiveRuntimeConnector) ReviewCandidate(
	ctx context.Context,
	request ProductiveReviewRequest,
) (ProductiveReviewResult, error) {
	if err := connector.ready(ctx); err != nil {
		return ProductiveReviewResult{}, err
	}
	if err := request.Validate(); err != nil {
		return ProductiveReviewResult{}, err
	}
	var result ProductiveReviewResult
	if err := connector.call(
		ctx,
		ProductiveRuntimeOperationReview,
		request,
		&result,
	); err != nil {
		return ProductiveReviewResult{}, err
	}
	if err := result.ValidateFor(request); err != nil {
		return ProductiveReviewResult{}, fmt.Errorf(
			"%w: %v",
			ErrProductiveRuntimeBindingMismatch,
			err,
		)
	}
	return result, nil
}

func canonicalProductiveLogicalPaths(values []string) bool {
	if values == nil {
		return false
	}
	canonical := append([]string(nil), values...)
	sort.Strings(canonical)
	for index, value := range canonical {
		if value == "" ||
			value != path.Clean(value) ||
			value == "." ||
			value == ".." ||
			strings.HasPrefix(value, "../") ||
			strings.HasPrefix(value, "/") ||
			strings.ContainsAny(value, "\\\x00") ||
			index > 0 && canonical[index-1] == value {
			return false
		}
	}
	for index := range values {
		if values[index] != canonical[index] {
			return false
		}
	}
	return true
}

var _ ProductiveActiveVerificationAuthority = (*HTTPSProductiveRuntimeConnector)(nil)
