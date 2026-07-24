package evidence

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
)

const (
	SemanticAuthorityIdentitySchemaV1 = "gentle-ai.semantic-authority-identity/v1"
	SemanticVerdictSchemaV1           = "gentle-ai.semantic-verdict/v1"
	SemanticAuthoritySeedBytes        = ed25519.SeedSize
)

type SemanticVerdictOutcome string

const SemanticVerdictPassed SemanticVerdictOutcome = "passed"

// SemanticAuthorityIdentity is the public, owner-bound verification identity
// persisted in an ActionTicket. The flattened optional fields preserve the
// exact legacy v1 JSON shape when the identity is absent.
type SemanticAuthorityIdentity struct {
	SemanticAuthoritySchema    string `json:"semanticAuthoritySchema,omitempty"`
	SemanticAuthorityRef       string `json:"semanticAuthorityRef,omitempty"`
	SemanticAuthorityPublicKey string `json:"semanticAuthorityPublicKey,omitempty"`
}

func (identity SemanticAuthorityIdentity) IsZero() bool {
	return identity == (SemanticAuthorityIdentity{})
}

func (identity SemanticAuthorityIdentity) validateFor(
	ownerRef string,
	requirementRef string,
) error {
	if identity.SemanticAuthoritySchema != SemanticAuthorityIdentitySchemaV1 ||
		!validSHA256Ref(identity.SemanticAuthorityRef) {
		return errors.New("semantic authority identity is incomplete")
	}
	publicKey, err := base64.RawStdEncoding.DecodeString(
		identity.SemanticAuthorityPublicKey,
	)
	if err != nil ||
		len(publicKey) != ed25519.PublicKeySize ||
		base64.RawStdEncoding.EncodeToString(publicKey) !=
			identity.SemanticAuthorityPublicKey {
		return errors.New("semantic authority public key is invalid")
	}
	want, err := semanticAuthorityIdentityRef(
		identity.SemanticAuthoritySchema,
		ownerRef,
		requirementRef,
		identity.SemanticAuthorityPublicKey,
	)
	if err != nil {
		return err
	}
	if identity.SemanticAuthorityRef != want {
		return errors.New("semantic authority identity does not bind its owner and requirement")
	}
	return nil
}

// SemanticAuthority is an owner-held signer. It does not execute commands or
// inspect prose: the owner invokes AttestPassed only after its semantic
// evaluator has reached a typed decision for the exact HCR process evidence.
type SemanticAuthority struct {
	identity       SemanticAuthorityIdentity
	ownerRef       string
	requirementRef string
	privateKey     ed25519.PrivateKey
}

// ProvisionSemanticAuthority reconstructs the same signer from a seed loaded
// from owner-controlled durable secret storage. The seed is never serialized
// into tickets or evidence. Production replay must provision the original
// seed; generating a fresh signer would create a different authority identity
// and is therefore rejected by the immutable ActionTicket.
func ProvisionSemanticAuthority(
	ownerRef string,
	requirementRef string,
	ownerSeed []byte,
) (*SemanticAuthority, error) {
	if !validSHA256Ref(ownerRef) || !validSHA256Ref(requirementRef) {
		return nil, errors.New("semantic authority owner and requirement must be lowercase SHA-256 references")
	}
	if len(ownerSeed) != SemanticAuthoritySeedBytes {
		return nil, errors.New("semantic authority requires a 32-byte owner seed")
	}
	nonzero := false
	for _, value := range ownerSeed {
		nonzero = nonzero || value != 0
	}
	if !nonzero {
		return nil, errors.New("semantic authority owner seed must not be all zero")
	}
	privateKey := ed25519.NewKeyFromSeed(
		append([]byte(nil), ownerSeed...),
	)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	encodedPublicKey := base64.RawStdEncoding.EncodeToString(publicKey)
	authorityRef, err := semanticAuthorityIdentityRef(
		SemanticAuthorityIdentitySchemaV1,
		ownerRef,
		requirementRef,
		encodedPublicKey,
	)
	if err != nil {
		return nil, err
	}
	return &SemanticAuthority{
		identity: SemanticAuthorityIdentity{
			SemanticAuthoritySchema:    SemanticAuthorityIdentitySchemaV1,
			SemanticAuthorityRef:       authorityRef,
			SemanticAuthorityPublicKey: encodedPublicKey,
		},
		ownerRef:       ownerRef,
		requirementRef: requirementRef,
		privateKey:     privateKey,
	}, nil
}

func (authority *SemanticAuthority) Identity() SemanticAuthorityIdentity {
	if authority == nil {
		return SemanticAuthorityIdentity{}
	}
	return authority.identity
}

// SemanticVerdict is a signed owner decision, not an interpretation of stdout.
// Its signature binds the exact candidate, command/cwd request, observed
// toolchain, and raw output identities admitted by HCR.
type SemanticVerdict struct {
	Schema               string                 `json:"schema"`
	VerdictRef           string                 `json:"verdictRef"`
	AuthorityRef         string                 `json:"authorityRef"`
	OwnerRef             string                 `json:"ownerRef"`
	RequirementRef       string                 `json:"requirementRef"`
	CandidateRef         string                 `json:"candidateRef"`
	RequestDigest        string                 `json:"requestDigest"`
	CWDDigest            string                 `json:"cwdDigest"`
	ToolchainIdentityRef string                 `json:"toolchainIdentityRef"`
	StdoutRawDigest      string                 `json:"stdoutRawDigest"`
	StderrRawDigest      string                 `json:"stderrRawDigest"`
	Outcome              SemanticVerdictOutcome `json:"outcome"`
	Signature            string                 `json:"signature"`
}

// AttestPassed signs an already-made owner decision for exact live HCR
// evidence. It deliberately does not infer PASS from exit status or text.
func (authority *SemanticAuthority) AttestPassed(
	ticket ActionTicket,
	process hostruntime.ProcessEvidence,
) (SemanticVerdict, error) {
	if authority == nil ||
		len(authority.privateKey) != ed25519.PrivateKeySize {
		return SemanticVerdict{}, errors.New("semantic authority is not initialized")
	}
	if ticket.SemanticAuthorityIdentity != authority.identity ||
		ticket.IssuerRef != authority.ownerRef ||
		ticket.SemanticRequirementRef != authority.requirementRef {
		return SemanticVerdict{}, errors.New("semantic authority does not own the exact action ticket requirement")
	}
	if err := ticket.RequestBinding.ValidateProcessEvidence(process); err != nil {
		return SemanticVerdict{}, err
	}
	if process.TerminalCause != hostruntime.TerminalExited ||
		process.ExitCode != 0 ||
		!process.CleanupComplete ||
		process.Stdout.Truncated ||
		process.Stderr.Truncated ||
		process.ToolchainState != hostruntime.ToolchainIdentityObserved {
		return SemanticVerdict{}, errors.New("semantic authority cannot attest incomplete process evidence")
	}
	verdict := SemanticVerdict{
		Schema: SemanticVerdictSchemaV1, AuthorityRef: authority.identity.SemanticAuthorityRef,
		OwnerRef: authority.ownerRef, RequirementRef: authority.requirementRef,
		CandidateRef: ticket.CandidateRef, RequestDigest: process.RequestDigest,
		CWDDigest: process.CWDDigest, ToolchainIdentityRef: process.ToolchainIdentityRef,
		StdoutRawDigest: process.Stdout.RawDigest, StderrRawDigest: process.Stderr.RawDigest,
		Outcome: SemanticVerdictPassed,
	}
	payload, err := semanticVerdictSigningPayload(verdict)
	if err != nil {
		return SemanticVerdict{}, err
	}
	verdict.Signature = base64.RawStdEncoding.EncodeToString(
		ed25519.Sign(authority.privateKey, payload),
	)
	verdict.VerdictRef, err = semanticVerdictRef(verdict)
	if err != nil {
		return SemanticVerdict{}, err
	}
	if err := verdict.ValidateFor(ticket, process); err != nil {
		return SemanticVerdict{}, err
	}
	return verdict, nil
}

// ValidateFor verifies live HCR provenance in addition to the durable signed
// verdict. Durable EPD replay uses validateStoredFor after its store has
// independently re-established process-record admission.
func (verdict SemanticVerdict) ValidateFor(
	ticket ActionTicket,
	process hostruntime.ProcessEvidence,
) error {
	if err := ticket.RequestBinding.ValidateProcessEvidence(process); err != nil {
		return err
	}
	return verdict.validateStoredFor(ticket, process)
}

func (verdict SemanticVerdict) validateStoredFor(
	ticket ActionTicket,
	process hostruntime.ProcessEvidence,
) error {
	if err := ticket.SemanticAuthorityIdentity.validateFor(
		ticket.IssuerRef,
		ticket.SemanticRequirementRef,
	); err != nil {
		return err
	}
	for _, ref := range []string{
		verdict.VerdictRef,
		verdict.AuthorityRef,
		verdict.OwnerRef,
		verdict.RequirementRef,
		verdict.CandidateRef,
		verdict.RequestDigest,
		verdict.CWDDigest,
		verdict.ToolchainIdentityRef,
		verdict.StdoutRawDigest,
		verdict.StderrRawDigest,
	} {
		if !validSHA256Ref(ref) {
			return errors.New("semantic verdict requires lowercase SHA-256 identities")
		}
	}
	if verdict.Schema != SemanticVerdictSchemaV1 ||
		verdict.Outcome != SemanticVerdictPassed {
		return errors.New("unsupported semantic verdict")
	}
	if verdict.AuthorityRef != ticket.SemanticAuthorityRef ||
		verdict.OwnerRef != ticket.IssuerRef ||
		verdict.RequirementRef != ticket.SemanticRequirementRef ||
		verdict.CandidateRef != ticket.CandidateRef ||
		verdict.RequestDigest != process.RequestDigest ||
		verdict.CWDDigest != process.CWDDigest ||
		verdict.ToolchainIdentityRef != process.ToolchainIdentityRef ||
		verdict.StdoutRawDigest != process.Stdout.RawDigest ||
		verdict.StderrRawDigest != process.Stderr.RawDigest ||
		process.ToolchainState != hostruntime.ToolchainIdentityObserved ||
		process.TerminalCause != hostruntime.TerminalExited ||
		process.ExitCode != 0 ||
		!process.CleanupComplete ||
		process.Stdout.Truncated ||
		process.Stderr.Truncated {
		return errors.New("semantic verdict does not bind the exact successful execution")
	}
	signature, err := base64.RawStdEncoding.DecodeString(verdict.Signature)
	if err != nil ||
		len(signature) != ed25519.SignatureSize ||
		base64.RawStdEncoding.EncodeToString(signature) != verdict.Signature {
		return errors.New("semantic verdict signature is invalid")
	}
	publicKey, err := base64.RawStdEncoding.DecodeString(
		ticket.SemanticAuthorityPublicKey,
	)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("semantic authority public key is invalid")
	}
	payload, err := semanticVerdictSigningPayload(verdict)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return errors.New("semantic verdict signature is invalid")
	}
	want, err := semanticVerdictRef(verdict)
	if err != nil {
		return err
	}
	if verdict.VerdictRef != want {
		return errors.New("semantic verdict reference does not match its signed content")
	}
	return nil
}

func semanticAuthorityIdentityRef(
	schema string,
	ownerRef string,
	requirementRef string,
	publicKey string,
) (string, error) {
	return digestJSON(struct {
		Domain         string `json:"domain"`
		Schema         string `json:"schema"`
		OwnerRef       string `json:"ownerRef"`
		RequirementRef string `json:"requirementRef"`
		PublicKey      string `json:"publicKey"`
	}{
		Domain: "gentle-ai.semantic-authority-identity/v1", Schema: schema,
		OwnerRef: ownerRef, RequirementRef: requirementRef, PublicKey: publicKey,
	})
}

func semanticVerdictSigningPayload(verdict SemanticVerdict) ([]byte, error) {
	return json.Marshal(struct {
		Domain               string                 `json:"domain"`
		Schema               string                 `json:"schema"`
		AuthorityRef         string                 `json:"authorityRef"`
		OwnerRef             string                 `json:"ownerRef"`
		RequirementRef       string                 `json:"requirementRef"`
		CandidateRef         string                 `json:"candidateRef"`
		RequestDigest        string                 `json:"requestDigest"`
		CWDDigest            string                 `json:"cwdDigest"`
		ToolchainIdentityRef string                 `json:"toolchainIdentityRef"`
		StdoutRawDigest      string                 `json:"stdoutRawDigest"`
		StderrRawDigest      string                 `json:"stderrRawDigest"`
		Outcome              SemanticVerdictOutcome `json:"outcome"`
	}{
		Domain: "gentle-ai.semantic-verdict-signature/v1", Schema: verdict.Schema,
		AuthorityRef: verdict.AuthorityRef, OwnerRef: verdict.OwnerRef,
		RequirementRef: verdict.RequirementRef, CandidateRef: verdict.CandidateRef,
		RequestDigest: verdict.RequestDigest, CWDDigest: verdict.CWDDigest,
		ToolchainIdentityRef: verdict.ToolchainIdentityRef,
		StdoutRawDigest:      verdict.StdoutRawDigest, StderrRawDigest: verdict.StderrRawDigest,
		Outcome: verdict.Outcome,
	})
}

func semanticVerdictRef(verdict SemanticVerdict) (string, error) {
	preimage := verdict
	preimage.VerdictRef = ""
	return digestJSON(preimage)
}
