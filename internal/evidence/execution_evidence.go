package evidence

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
)

const (
	ExecutionEvidenceSchemaV1 = "gentle-ai.execution-evidence/v1"
	ExecutionEvidenceSchemaV2 = "gentle-ai.execution-evidence/v2"
	ExecutionEvidenceSchema   = ExecutionEvidenceSchemaV2
)

type ExecutionOutcome string

const (
	ExecutionComplete   ExecutionOutcome = "complete"
	ExecutionFailed     ExecutionOutcome = "failed"
	ExecutionIncomplete ExecutionOutcome = "incomplete"
)

type AdmissionCode string

const (
	AdmissionInvalidTicket   AdmissionCode = "invalid_ticket"
	AdmissionBindingMismatch AdmissionCode = "binding_mismatch"
	AdmissionInvalidEvidence AdmissionCode = "invalid_process_evidence"
)

// AdmissionError is intentionally free of program paths, argv, environment,
// and retained output. Callers branch on Code instead of parsing prose.
type AdmissionError struct {
	Code  AdmissionCode
	Field string
}

func (err *AdmissionError) Error() string {
	if err.Field == "" {
		return fmt.Sprintf("execution evidence admission %s", err.Code)
	}
	return fmt.Sprintf("execution evidence admission %s at %s", err.Code, err.Field)
}

// ExecutionEvidence is the immutable EPD envelope for one exact ActionTicket.
// A failed command is coherent process evidence. In v2, even an untruncated
// zero exit is incomplete until a signed owner semantic verdict is admitted.
type ExecutionEvidence struct {
	Schema                 string `json:"schema"`
	EvidenceRef            string `json:"evidenceRef"`
	TicketRef              string `json:"ticketRef"`
	SlotBindingRef         string `json:"slotBindingRef"`
	IssuerRef              string `json:"issuerRef"`
	SubjectRef             string `json:"subjectRef"`
	CandidateRef           string `json:"candidateRef"`
	VerificationContextRef string `json:"verificationContextRef"`
	ExpectedRevision       string `json:"expectedRevision"`
	Slot                   string `json:"slot"`
	Capability             string `json:"capability"`
	SemanticAuthorityIdentity
	RequestBinding hostruntime.RequestBinding  `json:"requestBinding"`
	Process        hostruntime.ProcessEvidence `json:"process"`
	Outcome        ExecutionOutcome            `json:"outcome"`
	SemanticProof  *SemanticVerdict            `json:"semanticProof,omitempty"`
	admissionSeal  [sha256.Size]byte
}

// AdmitExecution validates the HCR-owned process facts against the exact
// provider-issued ticket and derives the only allowed monotonic outcome.
func AdmitExecution(ticket ActionTicket, process hostruntime.ProcessEvidence) (ExecutionEvidence, error) {
	return admitExecution(ticket, process, nil)
}

// AdmitExecutionWithSemanticVerdict is the only v2 path to semantic
// completion. The signed owner verdict is validated against the exact HCR
// process facts; stdout text and exit status never mint it implicitly.
func AdmitExecutionWithSemanticVerdict(
	ticket ActionTicket,
	process hostruntime.ProcessEvidence,
	verdict SemanticVerdict,
) (ExecutionEvidence, error) {
	return admitExecution(ticket, process, &verdict)
}

func admitExecution(
	ticket ActionTicket,
	process hostruntime.ProcessEvidence,
	verdict *SemanticVerdict,
) (ExecutionEvidence, error) {
	if err := ticket.Validate(); err != nil {
		return ExecutionEvidence{}, &AdmissionError{Code: AdmissionInvalidTicket, Field: "ticket"}
	}
	if err := ticket.RequestBinding.ValidateProcessEvidence(process); err != nil {
		if hostruntime.ValidateProcessEvidence(process) != nil {
			return ExecutionEvidence{}, &AdmissionError{Code: AdmissionInvalidEvidence, Field: "process"}
		}
		return ExecutionEvidence{}, &AdmissionError{Code: AdmissionBindingMismatch, Field: "requestBinding"}
	}
	slotBinding, err := ticket.SlotBindingRef()
	if err != nil {
		return ExecutionEvidence{}, &AdmissionError{Code: AdmissionInvalidTicket, Field: "slotBinding"}
	}
	schema := ExecutionEvidenceSchemaV2
	if ticket.Schema == ActionTicketSchemaV1 {
		schema = ExecutionEvidenceSchemaV1
	}
	envelope := ExecutionEvidence{
		Schema: schema, TicketRef: ticket.TicketRef,
		SlotBindingRef: slotBinding, IssuerRef: ticket.IssuerRef,
		SubjectRef: ticket.SubjectRef, CandidateRef: ticket.CandidateRef,
		VerificationContextRef: ticket.VerificationContextRef,
		ExpectedRevision:       ticket.ExpectedRevision, Slot: ticket.Slot,
		Capability: ticket.Capability, RequestBinding: ticket.RequestBinding,
		Process:                   process,
		SemanticAuthorityIdentity: ticket.SemanticAuthorityIdentity,
	}
	if verdict != nil {
		if ticket.Schema != ActionTicketSchemaV2 ||
			verdict.ValidateFor(ticket, process) != nil {
			return ExecutionEvidence{}, &AdmissionError{
				Code: AdmissionInvalidEvidence, Field: "semanticVerdict",
			}
		}
		copied := *verdict
		envelope.SemanticProof = &copied
	}
	envelope.Outcome = executionOutcome(envelope.Schema, process, envelope.SemanticProof)
	envelope.EvidenceRef, err = executionEvidenceDigest(envelope)
	if err != nil {
		return ExecutionEvidence{}, &AdmissionError{Code: AdmissionInvalidEvidence, Field: "canonical"}
	}
	envelope = sealExecutionEvidence(envelope)
	if err := envelope.ValidateFor(ticket); err != nil {
		return ExecutionEvidence{}, &AdmissionError{Code: AdmissionInvalidEvidence, Field: "envelope"}
	}
	return envelope, nil
}

// Validate accepts only an envelope returned by AdmitExecution or trusted
// Store replay. JSON shape alone is never admission provenance.
func (evidence ExecutionEvidence) Validate() error {
	if err := evidence.validateStored(); err != nil {
		return err
	}
	want := executionAdmissionSeal(evidence)
	if subtle.ConstantTimeCompare(evidence.admissionSeal[:], want[:]) != 1 {
		return errors.New("execution evidence admission provenance is invalid")
	}
	return nil
}

func (evidence ExecutionEvidence) validateStored() error {
	switch evidence.Schema {
	case ExecutionEvidenceSchemaV1:
		if evidence.Process.SchemaVersion != 1 ||
			!evidence.SemanticAuthorityIdentity.IsZero() ||
			evidence.SemanticProof != nil {
			return errors.New("legacy execution evidence carries v2 semantic bindings")
		}
	case ExecutionEvidenceSchemaV2:
		if evidence.Process.SchemaVersion != 2 {
			return errors.New("v2 execution evidence requires v2 host process evidence")
		}
		if evidence.Process.SemanticRequirementRef == "" {
			if !evidence.SemanticAuthorityIdentity.IsZero() {
				return errors.New("v2 execution evidence carries a semantic authority without a requirement")
			}
		} else if err := evidence.SemanticAuthorityIdentity.validateFor(
			evidence.IssuerRef,
			evidence.Process.SemanticRequirementRef,
		); err != nil {
			return err
		}
	default:
		return errors.New("unsupported execution evidence schema")
	}
	for name, ref := range map[string]string{
		"evidenceRef": evidence.EvidenceRef, "ticketRef": evidence.TicketRef,
		"slotBindingRef": evidence.SlotBindingRef, "issuerRef": evidence.IssuerRef,
		"subjectRef": evidence.SubjectRef, "candidateRef": evidence.CandidateRef,
		"verificationContextRef": evidence.VerificationContextRef,
		"expectedRevision":       evidence.ExpectedRevision,
	} {
		if !validSHA256Ref(ref) {
			return fmt.Errorf("execution evidence %s must be a lowercase SHA-256 reference", name)
		}
	}
	if !validCanonicalID(evidence.Slot) || !validCanonicalID(evidence.Capability) {
		return errors.New("execution evidence slot and capability must be canonical identifiers")
	}
	if err := evidence.RequestBinding.ValidateProcessEvidenceRecord(evidence.Process); err != nil {
		return err
	}
	if err := validateSemanticProofForEvidence(evidence); err != nil {
		return err
	}
	if evidence.Outcome != executionOutcome(
		evidence.Schema,
		evidence.Process,
		evidence.SemanticProof,
	) {
		return errors.New("execution evidence outcome does not match terminal process facts")
	}
	want, err := executionEvidenceDigest(evidence)
	if err != nil {
		return err
	}
	if evidence.EvidenceRef != want {
		return errors.New("execution evidence reference does not match its canonical content")
	}
	return nil
}

// ValidateFor rejects cross-ticket and cross-subject replay even when the
// underlying literal command happens to be identical.
func (evidence ExecutionEvidence) ValidateFor(ticket ActionTicket) error {
	if err := evidence.Validate(); err != nil {
		return err
	}
	return evidence.validateStoredFor(ticket)
}

func (evidence ExecutionEvidence) validateStoredFor(ticket ActionTicket) error {
	if err := ticket.Validate(); err != nil {
		return err
	}
	if err := evidence.validateStored(); err != nil {
		return err
	}
	slotBinding, err := ticket.SlotBindingRef()
	if err != nil {
		return err
	}
	if evidence.TicketRef != ticket.TicketRef ||
		evidence.SlotBindingRef != slotBinding ||
		evidence.IssuerRef != ticket.IssuerRef ||
		evidence.SubjectRef != ticket.SubjectRef ||
		evidence.CandidateRef != ticket.CandidateRef ||
		evidence.VerificationContextRef != ticket.VerificationContextRef ||
		evidence.ExpectedRevision != ticket.ExpectedRevision ||
		evidence.Slot != ticket.Slot ||
		evidence.Capability != ticket.Capability ||
		evidence.SemanticAuthorityIdentity != ticket.SemanticAuthorityIdentity ||
		evidence.RequestBinding != ticket.RequestBinding {
		return errors.New("execution evidence does not bind the exact action ticket")
	}
	if ticket.Schema == ActionTicketSchemaV1 &&
		evidence.Schema != ExecutionEvidenceSchemaV1 ||
		ticket.Schema == ActionTicketSchemaV2 &&
			evidence.Schema != ExecutionEvidenceSchemaV2 {
		return errors.New("execution evidence schema does not match its action ticket")
	}
	return nil
}

func executionOutcome(
	schema string,
	process hostruntime.ProcessEvidence,
	proof *SemanticVerdict,
) ExecutionOutcome {
	if process.TerminalCause != hostruntime.TerminalExited ||
		!process.CleanupComplete ||
		process.Stdout.Truncated || process.Stderr.Truncated {
		return ExecutionIncomplete
	}
	if process.ExitCode == 0 {
		if schema == ExecutionEvidenceSchemaV1 {
			return ExecutionComplete
		}
		if proof != nil {
			return ExecutionComplete
		}
		return ExecutionIncomplete
	}
	return ExecutionFailed
}

func validateSemanticProofForEvidence(evidence ExecutionEvidence) error {
	if evidence.SemanticProof == nil {
		return nil
	}
	proof := *evidence.SemanticProof
	ticketProjection := ActionTicket{
		Schema:                    ActionTicketSchemaV2,
		IssuerRef:                 evidence.IssuerRef,
		CandidateRef:              evidence.CandidateRef,
		SemanticRequirementRef:    evidence.Process.SemanticRequirementRef,
		SemanticAuthorityIdentity: evidence.SemanticAuthorityIdentity,
	}
	return proof.validateStoredFor(ticketProjection, evidence.Process)
}

func executionEvidenceDigest(evidence ExecutionEvidence) (string, error) {
	preimage := evidence
	preimage.EvidenceRef = ""
	return digestJSON(preimage)
}

func sealExecutionEvidence(evidence ExecutionEvidence) ExecutionEvidence {
	evidence.admissionSeal = executionAdmissionSeal(evidence)
	return evidence
}

func executionAdmissionSeal(evidence ExecutionEvidence) [sha256.Size]byte {
	domain := "gentle-ai.execution-evidence-admission/v2\x00"
	if evidence.Schema == ExecutionEvidenceSchemaV1 {
		domain = "gentle-ai.execution-evidence-admission/v1\x00"
	}
	return sha256.Sum256([]byte(domain + evidence.EvidenceRef))
}
