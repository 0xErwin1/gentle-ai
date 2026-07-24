package evidence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
)

const (
	// SemanticRequirementSchemaV1 identifies the path-free preimage used to
	// bind one semantic obligation to an exact capability, command request,
	// and owner-observed toolchain. RequestDigest and ExecutionID are
	// deliberately outside this preimage because both already include
	// SemanticRequirementRef.
	SemanticRequirementSchemaV1 = "gentle-ai.semantic-requirement/v1"
)

// DeriveSemanticRequirementRef returns the deterministic owner requirement
// for one exact HCR request. It consumes only the independent request
// identities named in the semantic-requirement contract; in particular it
// does not consume binding.RequestDigest, binding.ExecutionID, or an existing
// binding.SemanticRequirementRef.
func DeriveSemanticRequirementRef(
	capability string,
	binding hostruntime.RequestBinding,
) (string, error) {
	if !validCanonicalID(capability) {
		return "", errors.New(
			"semantic requirement capability must be a canonical identifier",
		)
	}
	if binding.Schema != hostruntime.RequestBindingSchemaV2 {
		return "", errors.New(
			"semantic requirement requires a v2 host request binding",
		)
	}
	for name, ref := range map[string]string{
		"programDigest":        binding.ProgramDigest,
		"argvDigest":           binding.ArgvDigest,
		"cwdDigest":            binding.CWDDigest,
		"environmentDigest":    binding.EnvironmentDigest,
		"toolchainIdentityRef": binding.ToolchainIdentityRef,
	} {
		if !validSHA256Ref(ref) {
			return "", fmt.Errorf(
				"semantic requirement %s must be a lowercase SHA-256 reference",
				name,
			)
		}
	}
	if err := binding.ToolchainIdentity.Validate(); err != nil {
		return "", fmt.Errorf(
			"semantic requirement toolchain identity is invalid: %w",
			err,
		)
	}
	if binding.ToolchainState != hostruntime.ToolchainIdentityObserved {
		return "", errors.New(
			"semantic requirement requires an observed executable toolchain",
		)
	}
	if binding.ProgramDigest != binding.ToolchainProgramDigest {
		return "", errors.New(
			"semantic requirement program does not match its toolchain identity",
		)
	}
	return digestJSON(struct {
		Schema               string `json:"schema"`
		Capability           string `json:"capability"`
		ProgramDigest        string `json:"programDigest"`
		ArgvDigest           string `json:"argvDigest"`
		CWDDigest            string `json:"cwdDigest"`
		EnvironmentDigest    string `json:"environmentDigest"`
		ToolchainIdentityRef string `json:"toolchainIdentityRef"`
	}{
		Schema:               SemanticRequirementSchemaV1,
		Capability:           capability,
		ProgramDigest:        binding.ProgramDigest,
		ArgvDigest:           binding.ArgvDigest,
		CWDDigest:            binding.CWDDigest,
		EnvironmentDigest:    binding.EnvironmentDigest,
		ToolchainIdentityRef: binding.ToolchainIdentityRef,
	})
}

// SemanticActionTicketRequest contains only the exact non-authority inputs
// accepted by the productive EPD owner. The repository issuer, semantic
// requirement, and signing identity are intentionally absent: Store derives
// all three from its live repository lease and durable owner seed.
type SemanticActionTicketRequest struct {
	SubjectRef             string
	CandidateRef           string
	VerificationContextRef string
	ExpectedRevision       string
	Slot                   string
	Program                string
	Args                   []string
	CWD                    string
	Environment            map[string]string
	Deadline               time.Duration
	OutputLimits           hostruntime.StreamLimits
	Capability             string
	RedactionLiterals      []string
}

// IssueSemanticActionTicket derives and publishes one v2 ticket through the
// EPD owner boundary. Issuance observes the toolchain once to derive the
// acyclic requirement and once more while sealing the final HCR request. A
// change between those observations fails closed rather than issuing a ticket
// under a stale requirement.
func (store Store) IssueSemanticActionTicket(
	ctx context.Context,
	request SemanticActionTicketRequest,
) (ActionTicket, error) {
	if ctx == nil {
		return ActionTicket{}, errors.New(
			"semantic action ticket context is nil",
		)
	}
	if err := ctx.Err(); err != nil {
		return ActionTicket{}, err
	}
	if err := store.validate(); err != nil {
		return ActionTicket{}, err
	}
	if err := store.lease.Validate(ctx); err != nil {
		return ActionTicket{}, err
	}

	ownerRequest := request.actionTicketRequest(store.RepositoryRef())
	provisional, err := NewActionTicket(ownerRequest)
	if err != nil {
		return ActionTicket{}, err
	}
	requirementRef, err := DeriveSemanticRequirementRef(
		provisional.Capability,
		provisional.RequestBinding,
	)
	if err != nil {
		return ActionTicket{}, err
	}
	if err := ctx.Err(); err != nil {
		return ActionTicket{}, err
	}
	authority, err := store.SemanticAuthority(ctx, requirementRef)
	if err != nil {
		return ActionTicket{}, err
	}

	ownerRequest.SemanticRequirementRef = requirementRef
	ownerRequest.SemanticAuthority = authority.Identity()
	ticket, err := NewActionTicket(ownerRequest)
	if err != nil {
		return ActionTicket{}, err
	}
	sealedRequirementRef, err := DeriveSemanticRequirementRef(
		ticket.Capability,
		ticket.RequestBinding,
	)
	if err != nil {
		return ActionTicket{}, err
	}
	if sealedRequirementRef != requirementRef {
		return ActionTicket{}, fmt.Errorf(
			"semantic action ticket toolchain changed during owner issuance: %w",
			hostruntime.ErrToolchainIdentityChanged,
		)
	}
	if ticket.Schema != ActionTicketSchemaV2 ||
		ticket.IssuerRef != store.RepositoryRef() ||
		ticket.SemanticRequirementRef != requirementRef ||
		ticket.SemanticAuthorityIdentity != authority.Identity() {
		return ActionTicket{}, errors.New(
			"semantic action ticket does not bind its owner-derived authority",
		)
	}
	if err := ctx.Err(); err != nil {
		return ActionTicket{}, err
	}
	if err := store.lease.Validate(ctx); err != nil {
		return ActionTicket{}, err
	}
	if _, err := store.PutTicket(ticket); err != nil {
		return ActionTicket{}, err
	}
	return ticket, nil
}

func (request SemanticActionTicketRequest) actionTicketRequest(
	issuerRef string,
) ActionTicketRequest {
	return ActionTicketRequest{
		IssuerRef:              issuerRef,
		SubjectRef:             request.SubjectRef,
		CandidateRef:           request.CandidateRef,
		VerificationContextRef: request.VerificationContextRef,
		ExpectedRevision:       request.ExpectedRevision,
		Slot:                   request.Slot,
		Program:                request.Program,
		Args:                   append([]string(nil), request.Args...),
		CWD:                    request.CWD,
		Environment:            cloneEnvironment(request.Environment),
		Deadline:               request.Deadline,
		OutputLimits:           request.OutputLimits,
		Capability:             request.Capability,
		RedactionLiterals:      append([]string(nil), request.RedactionLiterals...),
	}
}
