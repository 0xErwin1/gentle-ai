package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewerprovider"
	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

const (
	reviewProviderRoleLens              = "lens"
	reviewProviderRoleRefuter           = "refuter"
	reviewProviderRoleTargetedValidator = "targeted-validator"

	reviewProviderTransportCapability = "gentle-ai.provider-transport/v1"
	reviewProviderResultLimit         = 4 << 20
)

// reviewProviderRoleContract is the Go-owned registry for every provider role.
// Runtime adapters receive only its materialized Invocation and never select a
// prompt, schema, byte policy, admission rule, or durable slot.
type reviewProviderRoleContract struct {
	ID                   string
	RequestSchemaID      string
	ResultSchemaID       string
	ResultSchema         []byte
	Slot                 string
	RequiredCapabilities []string
}

func reviewProviderRoleContractFor(role string) (reviewProviderRoleContract, error) {
	var contract reviewProviderRoleContract
	switch role {
	case reviewProviderRoleLens:
		contract = reviewProviderRoleContract{
			ID: reviewProviderRoleLens, RequestSchemaID: "gentle-ai.review-lens-context/v1",
			ResultSchemaID: "https://gentle-ai.dev/schema/review/reviewer/v1",
			ResultSchema:   []byte(reviewtransaction.ReviewerResultSchema), Slot: "lens",
			RequiredCapabilities: []string{reviewProviderTransportCapability},
		}
	case reviewProviderRoleRefuter:
		contract = reviewProviderRoleContract{
			ID: reviewProviderRoleRefuter, RequestSchemaID: "gentle-ai.review-refuter-request/v1",
			ResultSchemaID: "https://gentle-ai.dev/schema/review/refuter/v1",
			ResultSchema:   append([]byte(nil), reviewInputSchemas["refuter"]...), Slot: "refuter",
			RequiredCapabilities: []string{reviewProviderTransportCapability},
		}
	case reviewProviderRoleTargetedValidator:
		contract = reviewProviderRoleContract{
			ID: reviewProviderRoleTargetedValidator, RequestSchemaID: reviewtransaction.TargetedValidationRequestSchema,
			ResultSchemaID: "https://gentle-ai.dev/schema/review/validator/v1",
			ResultSchema:   append([]byte(nil), reviewInputSchemas["validator"]...), Slot: "targeted-validator",
			RequiredCapabilities: []string{reviewProviderTransportCapability},
		}
	default:
		return reviewProviderRoleContract{}, fmt.Errorf("unsupported review provider role %q; add its Go-owned contract before it can be invoked", role) // refusal:by-design world-action: provider roles are compiled authority, not runtime-selected extensions
	}
	if !json.Valid(contract.ResultSchema) {
		return reviewProviderRoleContract{}, fmt.Errorf("review provider role %q has invalid result schema; repair the compiled contract before invoking this role", role) // refusal:by-design world-action: invalid Go-owned schema cannot be fixed by a runtime retry
	}
	return contract, nil
}

func reviewProviderRoleContracts() []reviewProviderRoleContract {
	roles := []string{reviewProviderRoleLens, reviewProviderRoleRefuter, reviewProviderRoleTargetedValidator}
	contracts := make([]reviewProviderRoleContract, 0, len(roles))
	for _, role := range roles {
		contract, err := reviewProviderRoleContractFor(role)
		if err != nil {
			panic(err)
		}
		contracts = append(contracts, contract)
	}
	return contracts
}

// reviewProviderRequest is the materialized lens invocation. Its fields remain
// inside Go; adapters receive only Invocation.
type reviewProviderRequest struct {
	Store      reviewtransaction.CompactStore
	Binding    reviewLensContextBinding
	Subject    reviewtransaction.ArtifactSubject
	Frozen     reviewtransaction.FrozenCandidateContext
	Invocation reviewerprovider.Invocation
}

// reviewProviderMaterialize deliberately delegates to the current lens-context
// assembly. It does not emit a delivery descriptor or mutate review authority.
func reviewProviderMaterialize(ctx context.Context, deps reviewLensContextDeps, repositoryContext, lens string) (request reviewProviderRequest, err error) {
	authority, err := resolveReviewLensAuthority(ctx, deps, repositoryContext, lens)
	if err != nil {
		return reviewProviderRequest{}, err
	}
	prompt, err := reviewLensContextAssemble(ctx, deps, authority.Binding, authority.Subject, authority.Frozen, authority.Inspector)
	if err != nil {
		return reviewLensContextCleanup(ctx, reviewProviderRequest{}, err, func() error { return deps.close(authority.Inspector) })
	}
	request = reviewProviderRequest{
		Store: authority.Store, Binding: authority.Binding, Subject: authority.Subject, Frozen: authority.Frozen,
		Invocation: reviewerprovider.NewInvocation(prompt),
	}
	return reviewLensContextCleanup(ctx, request, nil, func() error { return deps.close(authority.Inspector) })
}

// reviewProviderExtractRoleRaw is the common raw-output boundary. It refuses
// malformed, oversized, or multiple objects before role-specific decoding.
func reviewProviderExtractRoleRaw(role string, raw []byte) ([]byte, error) {
	contract, err := reviewProviderRoleContractFor(role)
	if err != nil {
		return nil, err
	}
	if err := validateReviewerResultPayload(raw); err != nil {
		return nil, err
	}
	payload, decision, err := reviewtransaction.ExtractBoundedSingleJSONObject(raw, reviewProviderResultLimit)
	if err != nil {
		return nil, fmt.Errorf("%s provider result admission %s: %w", contract.ID, decision, err)
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, errors.New("review provider result contains no JSON object; return exactly one JSON object and retry capture") // refusal:by-design operator-knowledge: the runtime must provide its final structured result
	}
	return payload, nil
}

// reviewProviderAdmitLensRaw preserves the native pre-capture semantics while
// keeping durable capture outside this materialization and admission slice.
func reviewProviderAdmitLensRaw(ctx context.Context, root string, state reviewtransaction.CompactState, revision string, frozen reviewtransaction.FrozenCandidateContext, subject reviewtransaction.ArtifactSubject, raw []byte) (reviewtransaction.ReviewerResult, error) {
	payload, err := reviewProviderExtractRoleRaw(reviewProviderRoleLens, raw)
	if err != nil {
		return reviewtransaction.ReviewerResult{}, err
	}
	result, err := reviewtransaction.ValidateReviewerResult(payload, subject, frozen.ChangedPathManifest)
	if err != nil {
		return reviewtransaction.ReviewerResult{}, err
	}
	if _, _, err := reviewtransaction.AdmitArtifact(ctx, reviewtransaction.ArtifactAdmissionRequest{
		ExpectedSubject: subject, FrozenContext: frozen, EchoedSubjectHash: result.SubjectHash,
		Inspection: result.Inspection, Result: reviewtransaction.LensResult{Lens: result.Lens, Findings: result.Findings, Evidence: result.Evidence},
		RawPayload: raw, CanonicalPayload: payload,
	}); err != nil {
		return reviewtransaction.ReviewerResult{}, err
	}
	return result, nil
}
