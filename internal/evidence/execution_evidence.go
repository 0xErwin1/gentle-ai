package evidence

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
)

const ExecutionEvidenceSchema = "gentle-ai.execution-evidence/v1"

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
// A failed command is coherent evidence, but only an untruncated zero exit with
// complete cleanup is complete. Every other terminal cause is incomplete.
type ExecutionEvidence struct {
	Schema                 string                      `json:"schema"`
	EvidenceRef            string                      `json:"evidenceRef"`
	TicketRef              string                      `json:"ticketRef"`
	SlotBindingRef         string                      `json:"slotBindingRef"`
	IssuerRef              string                      `json:"issuerRef"`
	SubjectRef             string                      `json:"subjectRef"`
	CandidateRef           string                      `json:"candidateRef"`
	VerificationContextRef string                      `json:"verificationContextRef"`
	ExpectedRevision       string                      `json:"expectedRevision"`
	Slot                   string                      `json:"slot"`
	Capability             string                      `json:"capability"`
	RequestBinding         hostruntime.RequestBinding  `json:"requestBinding"`
	Process                hostruntime.ProcessEvidence `json:"process"`
	Outcome                ExecutionOutcome            `json:"outcome"`
	admissionSeal          [sha256.Size]byte
}

// AdmitExecution validates the HCR-owned process facts against the exact
// provider-issued ticket and derives the only allowed monotonic outcome.
func AdmitExecution(ticket ActionTicket, process hostruntime.ProcessEvidence) (ExecutionEvidence, error) {
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
	envelope := ExecutionEvidence{
		Schema: ExecutionEvidenceSchema, TicketRef: ticket.TicketRef,
		SlotBindingRef: slotBinding, IssuerRef: ticket.IssuerRef,
		SubjectRef: ticket.SubjectRef, CandidateRef: ticket.CandidateRef,
		VerificationContextRef: ticket.VerificationContextRef,
		ExpectedRevision:       ticket.ExpectedRevision, Slot: ticket.Slot,
		Capability: ticket.Capability, RequestBinding: ticket.RequestBinding,
		Process: process, Outcome: executionOutcome(process),
	}
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
	if evidence.Schema != ExecutionEvidenceSchema {
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
	if evidence.Outcome != executionOutcome(evidence.Process) {
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
		evidence.RequestBinding != ticket.RequestBinding {
		return errors.New("execution evidence does not bind the exact action ticket")
	}
	return nil
}

func executionOutcome(process hostruntime.ProcessEvidence) ExecutionOutcome {
	if process.TerminalCause != hostruntime.TerminalExited ||
		!process.CleanupComplete ||
		process.Stdout.Truncated || process.Stderr.Truncated {
		return ExecutionIncomplete
	}
	if process.ExitCode == 0 {
		return ExecutionComplete
	}
	return ExecutionFailed
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
	return sha256.Sum256([]byte("gentle-ai.execution-evidence-admission/v1\x00" + evidence.EvidenceRef))
}
