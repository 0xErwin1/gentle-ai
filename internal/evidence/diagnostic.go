package evidence

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const DiagnosticSchema = "gentle-ai.evidence-diagnostic/v1"

type ProofCode string

const (
	ProofExecution          ProofCode = "execution"
	ProofRequestBinding     ProofCode = "request_binding"
	ProofCandidateFreshness ProofCode = "candidate_freshness"
	ProofOutputCompleteness ProofCode = "output_completeness"
	ProofProcessCleanup     ProofCode = "process_cleanup"
	ProofCapability         ProofCode = "capability"
	ProofEvidenceDurability ProofCode = "evidence_durability"
)

type ReasonCode string

const (
	ReasonNonzeroExit           ReasonCode = "nonzero_exit"
	ReasonDeadlineExceeded      ReasonCode = "deadline_exceeded"
	ReasonCancelled             ReasonCode = "cancelled"
	ReasonOutputTruncated       ReasonCode = "output_truncated"
	ReasonSpawnFailed           ReasonCode = "spawn_failed"
	ReasonProcessControlFailed  ReasonCode = "process_control_failed"
	ReasonCleanupFailed         ReasonCode = "cleanup_failed"
	ReasonBindingMismatch       ReasonCode = "binding_mismatch"
	ReasonCapabilityUnavailable ReasonCode = "capability_unavailable"
	ReasonImmutableConflict     ReasonCode = "immutable_conflict"
	ReasonEvidenceCorrupt       ReasonCode = "evidence_corrupt"
)

type DecisionCode string

const (
	DecisionNone                   DecisionCode = "none"
	DecisionRevise                 DecisionCode = "revise"
	DecisionDefer                  DecisionCode = "defer"
	DecisionSelectTrustedRunner    DecisionCode = "select_trusted_runner"
	DecisionRequestException       DecisionCode = "request_delivery_exception"
	DecisionMaintainerIntervention DecisionCode = "maintainer_intervention"
)

type DiagnosticInput struct {
	Operation                  string
	Proof                      ProofCode
	Reason                     ReasonCode
	SubjectRef                 string
	CandidateRef               string
	EvidenceRefs               []string
	ImplementationDecisionRef  string
	AvailableDeliveryRouteRefs []string
	DecisionNeeded             DecisionCode
	RemedyRefs                 []string
	Detail                     string
}

// Diagnostic is an owner-issued, content-bound explanation of a missing proof.
// Routes and remedies are references supplied by their owners; EPD reports
// them but does not authorize delivery or execute repair.
type Diagnostic struct {
	Schema                     string       `json:"schema"`
	DiagnosticRef              string       `json:"diagnosticRef"`
	Operation                  string       `json:"operation"`
	Proof                      ProofCode    `json:"proof"`
	Reason                     ReasonCode   `json:"reason"`
	SubjectRef                 string       `json:"subjectRef"`
	CandidateRef               string       `json:"candidateRef"`
	EvidenceRefs               []string     `json:"evidenceRefs"`
	ImplementationDecisionRef  string       `json:"implementationDecisionRef"`
	AvailableDeliveryRouteRefs []string     `json:"availableDeliveryRouteRefs"`
	DecisionNeeded             DecisionCode `json:"decisionNeeded"`
	RemedyRefs                 []string     `json:"remedyRefs"`
	Detail                     string       `json:"detail,omitempty"`
}

func NewDiagnostic(input DiagnosticInput) (Diagnostic, error) {
	diagnostic := Diagnostic{
		Schema: DiagnosticSchema, Operation: input.Operation,
		Proof: input.Proof, Reason: input.Reason,
		SubjectRef:                input.SubjectRef,
		CandidateRef:              input.CandidateRef,
		ImplementationDecisionRef: input.ImplementationDecisionRef,
		DecisionNeeded:            input.DecisionNeeded, Detail: strings.TrimSpace(input.Detail),
	}
	var err error
	if diagnostic.EvidenceRefs, err = canonicalRefs(input.EvidenceRefs); err != nil {
		return Diagnostic{}, fmt.Errorf("diagnostic evidence refs: %w", err)
	}
	if diagnostic.RemedyRefs, err = canonicalRefs(input.RemedyRefs); err != nil {
		return Diagnostic{}, fmt.Errorf("diagnostic remedy refs: %w", err)
	}
	if diagnostic.AvailableDeliveryRouteRefs, err = canonicalRefs(input.AvailableDeliveryRouteRefs); err != nil {
		return Diagnostic{}, fmt.Errorf("diagnostic available delivery route refs: %w", err)
	}
	diagnostic.DiagnosticRef, err = diagnosticDigest(diagnostic)
	if err != nil {
		return Diagnostic{}, err
	}
	if err := diagnostic.Validate(); err != nil {
		return Diagnostic{}, err
	}
	return diagnostic, nil
}

func (diagnostic Diagnostic) Validate() error {
	if diagnostic.Schema != DiagnosticSchema {
		return errors.New("unsupported evidence diagnostic schema")
	}
	if !validSHA256Ref(diagnostic.DiagnosticRef) ||
		!validSHA256Ref(diagnostic.SubjectRef) ||
		!validSHA256Ref(diagnostic.CandidateRef) ||
		!validSHA256Ref(diagnostic.ImplementationDecisionRef) {
		return errors.New("evidence diagnostic requires exact diagnostic, subject, candidate, and owner decision refs")
	}
	if !validCanonicalID(diagnostic.Operation) {
		return errors.New("evidence diagnostic operation must be a canonical identifier")
	}
	if !validProofCode(diagnostic.Proof) || !validReasonCode(diagnostic.Reason) ||
		!validDecisionCode(diagnostic.DecisionNeeded) {
		return errors.New("evidence diagnostic contains an unsupported typed code")
	}
	if diagnostic.EvidenceRefs == nil || diagnostic.RemedyRefs == nil ||
		diagnostic.AvailableDeliveryRouteRefs == nil {
		return errors.New("evidence diagnostic collections must be explicit arrays")
	}
	if refs, err := canonicalRefs(diagnostic.EvidenceRefs); err != nil ||
		!equalStrings(refs, diagnostic.EvidenceRefs) {
		return errors.New("evidence diagnostic evidence refs must be canonical")
	}
	if refs, err := canonicalRefs(diagnostic.RemedyRefs); err != nil ||
		!equalStrings(refs, diagnostic.RemedyRefs) {
		return errors.New("evidence diagnostic remedy refs must be canonical")
	}
	if refs, err := canonicalRefs(diagnostic.AvailableDeliveryRouteRefs); err != nil ||
		!equalStrings(refs, diagnostic.AvailableDeliveryRouteRefs) {
		return errors.New("evidence diagnostic delivery route refs must be canonical")
	}
	if diagnostic.Detail != strings.TrimSpace(diagnostic.Detail) ||
		len(diagnostic.Detail) > 2048 || !utf8.ValidString(diagnostic.Detail) ||
		strings.ContainsRune(diagnostic.Detail, '\x00') {
		return errors.New("evidence diagnostic detail is not canonical bounded UTF-8")
	}
	want, err := diagnosticDigest(diagnostic)
	if err != nil {
		return err
	}
	if diagnostic.DiagnosticRef != want {
		return errors.New("evidence diagnostic ref does not match its canonical content")
	}
	return nil
}

func diagnosticDigest(diagnostic Diagnostic) (string, error) {
	preimage := diagnostic
	preimage.DiagnosticRef = ""
	return digestJSON(preimage)
}

func validProofCode(code ProofCode) bool {
	switch code {
	case ProofExecution, ProofRequestBinding, ProofCandidateFreshness,
		ProofOutputCompleteness, ProofProcessCleanup, ProofCapability,
		ProofEvidenceDurability:
		return true
	default:
		return false
	}
}

func validReasonCode(code ReasonCode) bool {
	switch code {
	case ReasonNonzeroExit, ReasonDeadlineExceeded, ReasonCancelled,
		ReasonOutputTruncated, ReasonSpawnFailed, ReasonProcessControlFailed,
		ReasonCleanupFailed, ReasonBindingMismatch, ReasonCapabilityUnavailable,
		ReasonImmutableConflict, ReasonEvidenceCorrupt:
		return true
	default:
		return false
	}
}

func validDecisionCode(code DecisionCode) bool {
	switch code {
	case DecisionNone, DecisionRevise, DecisionDefer, DecisionSelectTrustedRunner,
		DecisionRequestException, DecisionMaintainerIntervention:
		return true
	default:
		return false
	}
}

func canonicalRefs(values []string) ([]string, error) {
	canonical := append([]string{}, values...)
	for _, value := range canonical {
		if !validSHA256Ref(strings.TrimSpace(value)) || value != strings.TrimSpace(value) {
			return nil, errors.New("value must be a lowercase SHA-256 ref")
		}
	}
	sort.Strings(canonical)
	for index := 1; index < len(canonical); index++ {
		if canonical[index-1] == canonical[index] {
			return nil, errors.New("values must be unique")
		}
	}
	return canonical, nil
}

func canonicalIDs(values []string) ([]string, error) {
	canonical := append([]string{}, values...)
	for _, value := range canonical {
		if !validCanonicalID(value) {
			return nil, errors.New("value must be a canonical identifier")
		}
	}
	sort.Strings(canonical)
	for index := 1; index < len(canonical); index++ {
		if canonical[index-1] == canonical[index] {
			return nil, errors.New("values must be unique")
		}
	}
	return canonical, nil
}
