package workrun

import (
	"context"
	"errors"
	"fmt"
)

var ErrEvidencePortUnavailable = errors.New("work run evidence port is unavailable")

// EvidencePort is the only EPD dependency required by the WorkRun journal.
// Implementations must return bindings only after validating the owner-issued
// opaque admission seal. Raw argv, environment, output, and seal internals
// never cross this port.
type EvidencePort interface {
	ReadActionTicket(context.Context, string) (AdmittedActionTicket, error)
	ReadExecutionEvidence(context.Context, string) (AdmittedExecutionEvidence, error)
}

type AdmittedActionTicket struct {
	TicketRef              string
	SubjectRef             string
	CandidateRef           string
	VerificationContextRef string
	ExpectedRevision       string
	Slot                   string
	Capability             string
	SlotBindingRef         string
	HostRequestDigest      string
}

func (ticket AdmittedActionTicket) Validate() error {
	for name, ref := range map[string]string{
		"ticketRef": ticket.TicketRef, "subjectRef": ticket.SubjectRef,
		"candidateRef": ticket.CandidateRef, "verificationContextRef": ticket.VerificationContextRef,
		"expectedRevision": ticket.ExpectedRevision, "slotBindingRef": ticket.SlotBindingRef,
		"hostRequestDigest": ticket.HostRequestDigest,
	} {
		if !validSHA256Ref(ref) {
			return fmt.Errorf("admitted action ticket %s must be an immutable SHA-256 reference", name)
		}
	}
	if !validIdentifier(ticket.Slot) || !validIdentifier(ticket.Capability) {
		return errors.New("admitted action ticket slot and capability must be canonical identifiers")
	}
	return nil
}

type AdmittedExecutionEvidence struct {
	EvidenceRef            string
	TicketRef              string
	SlotBindingRef         string
	SubjectRef             string
	CandidateRef           string
	VerificationContextRef string
	ExpectedRevision       string
	Slot                   string
	Capability             string
	Complete               bool
}

func (execution AdmittedExecutionEvidence) Validate() error {
	for name, ref := range map[string]string{
		"evidenceRef": execution.EvidenceRef, "ticketRef": execution.TicketRef,
		"slotBindingRef": execution.SlotBindingRef, "subjectRef": execution.SubjectRef,
		"candidateRef": execution.CandidateRef, "verificationContextRef": execution.VerificationContextRef,
		"expectedRevision": execution.ExpectedRevision,
	} {
		if !validSHA256Ref(ref) {
			return fmt.Errorf("admitted execution evidence %s must be an immutable SHA-256 reference", name)
		}
	}
	if !validIdentifier(execution.Slot) || !validIdentifier(execution.Capability) {
		return errors.New("admitted execution evidence slot and capability must be canonical identifiers")
	}
	return nil
}
