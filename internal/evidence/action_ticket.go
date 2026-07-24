// Package evidence owns immutable action tickets, provider-captured execution
// evidence, typed diagnostics, and their content-addressed admission store.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
)

const (
	ActionTicketSchemaV1 = "gentle-ai.action-ticket/v1"
	ActionTicketSchemaV2 = "gentle-ai.action-ticket/v2"
	ActionTicketSchema   = ActionTicketSchemaV2
)

const (
	actionExecutionDomainV1 = "gentle-ai.action-ticket-execution/v1"
	actionExecutionDomainV2 = "gentle-ai.action-ticket-execution/v2"
)

var (
	sha256RefPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	canonicalID      = regexp.MustCompile(`^[a-z0-9]+(?:[._/-][a-z0-9]+)*$`)
)

// ActionTicketRequest is the owner input used to issue one exact HCR request.
// Every ref is immutable; no model-authored shell or ambient environment is
// resolved by this package.
type ActionTicketRequest struct {
	IssuerRef              string
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
	// SemanticRequirementRef is an opaque owner-issued definition of what a
	// successful exit means for this exact candidate and capability. Without
	// it, a v2 zero exit remains process evidence but cannot become semantic
	// completion.
	SemanticRequirementRef string
	// SemanticAuthority is the public identity of the owner-held authority
	// allowed to issue a typed verdict for SemanticRequirementRef.
	SemanticAuthority SemanticAuthorityIdentity
}

// ActionTicket is the immutable, versioned execution authorization preimage.
// It persists literal inputs because exact durable replay must never consult
// ambient argv, cwd, or environment. Ticket files are provider-private and
// stored with owner-only permissions.
type ActionTicket struct {
	Schema                 string                   `json:"schema"`
	TicketRef              string                   `json:"ticketRef"`
	IssuerRef              string                   `json:"issuerRef"`
	SubjectRef             string                   `json:"subjectRef"`
	CandidateRef           string                   `json:"candidateRef"`
	VerificationContextRef string                   `json:"verificationContextRef"`
	ExpectedRevision       string                   `json:"expectedRevision"`
	Slot                   string                   `json:"slot"`
	ExecutionID            string                   `json:"executionId"`
	Program                string                   `json:"program"`
	Args                   []string                 `json:"args"`
	CWD                    string                   `json:"cwd"`
	Environment            map[string]string        `json:"environment"`
	DeadlineNanos          int64                    `json:"deadlineNanos"`
	OutputLimits           hostruntime.StreamLimits `json:"outputLimits"`
	Capability             string                   `json:"capability"`
	RedactionLiterals      []string                 `json:"redactionLiterals"`
	SemanticRequirementRef string                   `json:"semanticRequirementRef,omitempty"`
	SemanticAuthorityIdentity
	RequestBinding hostruntime.RequestBinding `json:"requestBinding"`
}

// NewActionTicket issues a deterministic ticket. ExecutionID is derived from
// the complete subject and request, so process evidence cannot be replayed
// under another candidate, revision, or slot even when argv is identical.
func NewActionTicket(request ActionTicketRequest) (ActionTicket, error) {
	request.Args = append([]string{}, request.Args...)
	request.Environment = cloneEnvironment(request.Environment)
	request.RedactionLiterals = canonicalRedactions(request.RedactionLiterals)

	ticket := ActionTicket{
		Schema:    ActionTicketSchemaV2,
		IssuerRef: request.IssuerRef, SubjectRef: request.SubjectRef,
		CandidateRef: request.CandidateRef, VerificationContextRef: request.VerificationContextRef,
		ExpectedRevision: request.ExpectedRevision, Slot: request.Slot,
		Program: request.Program, Args: request.Args, CWD: request.CWD,
		Environment: request.Environment, DeadlineNanos: int64(request.Deadline),
		OutputLimits: request.OutputLimits, Capability: request.Capability,
		RedactionLiterals:         request.RedactionLiterals,
		SemanticRequirementRef:    request.SemanticRequirementRef,
		SemanticAuthorityIdentity: request.SemanticAuthority,
	}
	if err := ticket.validateInputs(); err != nil {
		return ActionTicket{}, err
	}
	executionDigest, err := actionTicketExecutionDigest(ticket)
	if err != nil {
		return ActionTicket{}, err
	}
	ticket.ExecutionID = "evidence-" + strings.TrimPrefix(executionDigest, "sha256:")
	binding, err := hostruntime.BindExecStep(ticket.execStep())
	if err != nil {
		return ActionTicket{}, fmt.Errorf("bind action ticket host request: %w", err)
	}
	ticket.RequestBinding = binding
	ticket.TicketRef, err = actionTicketDigest(ticket)
	if err != nil {
		return ActionTicket{}, err
	}
	if err := ticket.Validate(); err != nil {
		return ActionTicket{}, err
	}
	payload, err := canonicalPayload(ticket)
	if err != nil {
		return ActionTicket{}, err
	}
	if len(payload) > maxTicketRecordBytes {
		return ActionTicket{}, errors.New("action ticket exceeds the durable record limit")
	}
	return ticket, nil
}

// Validate proves that every raw ticket input still matches both its HCR
// request projection and its content identity.
func (ticket ActionTicket) Validate() error {
	switch ticket.Schema {
	case ActionTicketSchemaV1:
		if ticket.SemanticRequirementRef != "" ||
			!ticket.SemanticAuthorityIdentity.IsZero() ||
			ticket.RequestBinding.Schema != hostruntime.RequestBindingSchemaV1 {
			return errors.New("legacy action ticket carries v2 semantic bindings")
		}
	case ActionTicketSchemaV2:
		if ticket.RequestBinding.Schema != hostruntime.RequestBindingSchemaV2 {
			return errors.New("v2 action ticket is missing its host request binding")
		}
	default:
		return errors.New("unsupported action ticket schema")
	}
	if err := ticket.validateInputs(); err != nil {
		return err
	}
	executionDigest, err := actionTicketExecutionDigest(ticket)
	if err != nil {
		return err
	}
	if ticket.ExecutionID != "evidence-"+strings.TrimPrefix(executionDigest, "sha256:") {
		return errors.New("action ticket execution ID does not bind its exact subject and request")
	}
	if err := hostruntime.ValidateBoundExecStep(
		ticket.execStep(),
		ticket.RequestBinding,
	); err != nil {
		return fmt.Errorf("validate action ticket host request binding: %w", err)
	}
	want, err := actionTicketDigest(ticket)
	if err != nil {
		return err
	}
	if ticket.TicketRef != want {
		return errors.New("action ticket reference does not match its canonical content")
	}
	return nil
}

// ExecStep returns a defensive exact projection for HCR. The caller may
// execute it but cannot use this conversion to change ticket authority.
func (ticket ActionTicket) ExecStep() (hostruntime.ExecStep, error) {
	if err := ticket.Validate(); err != nil {
		return hostruntime.ExecStep{}, err
	}
	return ticket.execStep(), nil
}

// SlotBindingRef identifies the once-only subject/revision/slot reservation.
// A content store uses it to reject a different ticket for the same slot.
func (ticket ActionTicket) SlotBindingRef() (string, error) {
	if err := ticket.Validate(); err != nil {
		return "", err
	}
	preimage := struct {
		Schema                 string `json:"schema"`
		SubjectRef             string `json:"subjectRef"`
		CandidateRef           string `json:"candidateRef"`
		VerificationContextRef string `json:"verificationContextRef"`
		ExpectedRevision       string `json:"expectedRevision"`
		Slot                   string `json:"slot"`
	}{
		Schema: "gentle-ai.action-ticket-slot/v1", SubjectRef: ticket.SubjectRef,
		CandidateRef:           ticket.CandidateRef,
		VerificationContextRef: ticket.VerificationContextRef,
		ExpectedRevision:       ticket.ExpectedRevision, Slot: ticket.Slot,
	}
	return digestJSON(preimage)
}

func (ticket ActionTicket) validateInputs() error {
	for name, ref := range map[string]string{
		"issuerRef": ticket.IssuerRef, "subjectRef": ticket.SubjectRef,
		"candidateRef": ticket.CandidateRef, "verificationContextRef": ticket.VerificationContextRef,
		"expectedRevision": ticket.ExpectedRevision,
	} {
		if !validSHA256Ref(ref) {
			return fmt.Errorf("action ticket %s must be a lowercase SHA-256 reference", name)
		}
	}
	if !validCanonicalID(ticket.Slot) {
		return errors.New("action ticket slot must be a canonical identifier")
	}
	if !validCanonicalID(ticket.Capability) {
		return errors.New("action ticket capability must be a canonical identifier")
	}
	if ticket.SemanticRequirementRef != "" &&
		!validSHA256Ref(ticket.SemanticRequirementRef) {
		return errors.New("action ticket semantic requirement must be a lowercase SHA-256 reference")
	}
	if ticket.SemanticRequirementRef == "" {
		if !ticket.SemanticAuthorityIdentity.IsZero() {
			return errors.New("action ticket semantic authority has no requirement")
		}
	} else if err := ticket.SemanticAuthorityIdentity.validateFor(
		ticket.IssuerRef,
		ticket.SemanticRequirementRef,
	); err != nil {
		return err
	}
	if ticket.Program == "" || !filepath.IsAbs(ticket.Program) || filepath.Clean(ticket.Program) != ticket.Program {
		return errors.New("action ticket program must be an absolute canonical path")
	}
	if ticket.CWD == "" || !filepath.IsAbs(ticket.CWD) || filepath.Clean(ticket.CWD) != ticket.CWD {
		return errors.New("action ticket cwd must be an absolute canonical path")
	}
	if ticket.Args == nil {
		return errors.New("action ticket args must be an explicit array")
	}
	for index, argument := range ticket.Args {
		if strings.ContainsRune(argument, '\x00') {
			return fmt.Errorf("action ticket args[%d] contains NUL", index)
		}
	}
	if ticket.Environment == nil {
		return errors.New("action ticket environment must be an explicit object")
	}
	for name, value := range ticket.Environment {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "=\x00") {
			return errors.New("action ticket environment contains an invalid name")
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("action ticket environment %q contains NUL", name)
		}
	}
	if ticket.DeadlineNanos <= 0 {
		return errors.New("action ticket deadline must be positive")
	}
	if ticket.OutputLimits.StdoutBytes <= 0 ||
		ticket.OutputLimits.StdoutBytes > hostruntime.MaxRetainedStreamBytes {
		return errors.New("action ticket stdout limit is outside HCR bounds")
	}
	if ticket.OutputLimits.StderrBytes <= 0 ||
		ticket.OutputLimits.StderrBytes > hostruntime.MaxRetainedStreamBytes {
		return errors.New("action ticket stderr limit is outside HCR bounds")
	}
	if ticket.OutputLimits.StdoutBytes+ticket.OutputLimits.StderrBytes > hostruntime.MaxRetainedOutputBytes {
		return errors.New("action ticket combined output limit is outside HCR bounds")
	}
	if ticket.RedactionLiterals == nil {
		return errors.New("action ticket redactions must be an explicit array")
	}
	if canonical := canonicalRedactions(ticket.RedactionLiterals); !equalStrings(canonical, ticket.RedactionLiterals) {
		return errors.New("action ticket redactions must be non-empty, unique, and canonical")
	}
	for _, literal := range ticket.RedactionLiterals {
		if literal == "" {
			return errors.New("action ticket redactions must not contain empty literals")
		}
	}
	return nil
}

func (ticket ActionTicket) execStep() hostruntime.ExecStep {
	step := hostruntime.ExecStep{
		ExecutionID: ticket.ExecutionID,
		Program:     ticket.Program,
		Args:        append([]string{}, ticket.Args...),
		CWD:         ticket.CWD,
		Env:         cloneEnvironment(ticket.Environment),
		Deadline:    time.Duration(ticket.DeadlineNanos),
		Limits:      ticket.OutputLimits,
		Redaction: hostruntime.RedactionPlan{
			Literals: append([]string{}, ticket.RedactionLiterals...),
		},
		SemanticRequirementRef: ticket.SemanticRequirementRef,
	}
	if ticket.Schema == ActionTicketSchemaV1 {
		step.EvidenceVersion = 1
	} else {
		step.EvidenceVersion = 2
		step.ToolchainIdentity = ticket.RequestBinding.ToolchainIdentity
	}
	return step
}

func actionTicketExecutionDigest(ticket ActionTicket) (string, error) {
	if ticket.Schema == ActionTicketSchemaV1 {
		preimage := struct {
			Domain                 string                   `json:"domain"`
			IssuerRef              string                   `json:"issuerRef"`
			SubjectRef             string                   `json:"subjectRef"`
			CandidateRef           string                   `json:"candidateRef"`
			VerificationContextRef string                   `json:"verificationContextRef"`
			ExpectedRevision       string                   `json:"expectedRevision"`
			Slot                   string                   `json:"slot"`
			Program                string                   `json:"program"`
			Args                   []string                 `json:"args"`
			CWD                    string                   `json:"cwd"`
			Environment            map[string]string        `json:"environment"`
			DeadlineNanos          int64                    `json:"deadlineNanos"`
			OutputLimits           hostruntime.StreamLimits `json:"outputLimits"`
			Capability             string                   `json:"capability"`
			RedactionLiterals      []string                 `json:"redactionLiterals"`
		}{
			Domain: actionExecutionDomainV1, IssuerRef: ticket.IssuerRef,
			SubjectRef: ticket.SubjectRef, CandidateRef: ticket.CandidateRef,
			VerificationContextRef: ticket.VerificationContextRef,
			ExpectedRevision:       ticket.ExpectedRevision, Slot: ticket.Slot,
			Program: ticket.Program, Args: ticket.Args, CWD: ticket.CWD,
			Environment: ticket.Environment, DeadlineNanos: ticket.DeadlineNanos,
			OutputLimits: ticket.OutputLimits, Capability: ticket.Capability,
			RedactionLiterals: ticket.RedactionLiterals,
		}
		return digestJSON(preimage)
	}
	preimage := struct {
		Domain                 string                   `json:"domain"`
		IssuerRef              string                   `json:"issuerRef"`
		SubjectRef             string                   `json:"subjectRef"`
		CandidateRef           string                   `json:"candidateRef"`
		VerificationContextRef string                   `json:"verificationContextRef"`
		ExpectedRevision       string                   `json:"expectedRevision"`
		Slot                   string                   `json:"slot"`
		Program                string                   `json:"program"`
		Args                   []string                 `json:"args"`
		CWD                    string                   `json:"cwd"`
		Environment            map[string]string        `json:"environment"`
		DeadlineNanos          int64                    `json:"deadlineNanos"`
		OutputLimits           hostruntime.StreamLimits `json:"outputLimits"`
		Capability             string                   `json:"capability"`
		RedactionLiterals      []string                 `json:"redactionLiterals"`
		SemanticRequirementRef string                   `json:"semanticRequirementRef"`
		SemanticAuthorityRef   string                   `json:"semanticAuthorityRef"`
	}{
		Domain: actionExecutionDomainV2, IssuerRef: ticket.IssuerRef,
		SubjectRef: ticket.SubjectRef, CandidateRef: ticket.CandidateRef,
		VerificationContextRef: ticket.VerificationContextRef,
		ExpectedRevision:       ticket.ExpectedRevision, Slot: ticket.Slot,
		Program: ticket.Program, Args: ticket.Args, CWD: ticket.CWD,
		Environment: ticket.Environment, DeadlineNanos: ticket.DeadlineNanos,
		OutputLimits: ticket.OutputLimits, Capability: ticket.Capability,
		RedactionLiterals:      ticket.RedactionLiterals,
		SemanticRequirementRef: ticket.SemanticRequirementRef,
		SemanticAuthorityRef:   ticket.SemanticAuthorityRef,
	}
	return digestJSON(preimage)
}

func actionTicketDigest(ticket ActionTicket) (string, error) {
	preimage := ticket
	preimage.TicketRef = ""
	return digestJSON(preimage)
}

func canonicalRedactions(redactions []string) []string {
	seen := make(map[string]struct{}, len(redactions))
	canonical := make([]string, 0, len(redactions))
	for _, literal := range redactions {
		if _, ok := seen[literal]; ok {
			continue
		}
		seen[literal] = struct{}{}
		canonical = append(canonical, literal)
	}
	sort.Slice(canonical, func(i, j int) bool {
		if len(canonical[i]) == len(canonical[j]) {
			return canonical[i] < canonical[j]
		}
		return len(canonical[i]) > len(canonical[j])
	})
	return canonical
}

func cloneEnvironment(environment map[string]string) map[string]string {
	cloned := make(map[string]string, len(environment))
	for name, value := range environment {
		cloned[name] = value
	}
	return cloned
}

func digestJSON(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validSHA256Ref(value string) bool {
	return sha256RefPattern.MatchString(value)
}

func validCanonicalID(value string) bool {
	return len(value) > 0 && len(value) <= 160 &&
		canonicalID.MatchString(value) && !strings.Contains(value, "//")
}

func equalStrings(left, right []string) bool {
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
