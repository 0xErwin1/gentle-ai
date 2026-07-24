package hostruntime

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"hash"
	"strconv"
	"strings"
	"time"
)

const RequestBindingSchema = "gentle-ai.host-request-binding/v1"

// RequestBinding is the non-secret, immutable projection of one validated
// ExecStep. Consumers can compare ProcessEvidence with the exact request HCR
// accepted without reproducing HCR's canonicalization or digest preimages.
type RequestBinding struct {
	Schema            string `json:"schema"`
	ExecutionID       string `json:"executionId"`
	RequestDigest     string `json:"requestDigest"`
	ProgramDigest     string `json:"programDigest"`
	ArgvDigest        string `json:"argvDigest"`
	CWDDigest         string `json:"cwdDigest"`
	EnvironmentDigest string `json:"environmentDigest"`
}

// BindExecStep validates and canonicalizes step through the same boundary used
// by Executor.Execute, then returns only its non-secret request identities.
func BindExecStep(step ExecStep) (RequestBinding, error) {
	canonical, err := validateStep(step)
	if err != nil {
		return RequestBinding{}, err
	}
	return requestBindingForCanonical(canonical), nil
}

func requestBindingForCanonical(canonical ExecStep) RequestBinding {
	evidence := newProcessEvidence(canonical)
	return RequestBinding{
		Schema:            RequestBindingSchema,
		ExecutionID:       evidence.ExecutionID,
		RequestDigest:     evidence.RequestDigest,
		ProgramDigest:     evidence.ProgramDigest,
		ArgvDigest:        evidence.ArgvDigest,
		CWDDigest:         evidence.CWDDigest,
		EnvironmentDigest: evidence.EnvironmentDigest,
	}
}

// ValidateProcessEvidence confirms that evidence was produced for exactly this
// HCR request. It intentionally does not classify the terminal result.
func (binding RequestBinding) ValidateProcessEvidence(evidence ProcessEvidence) error {
	if err := binding.ValidateProcessEvidenceRecord(evidence); err != nil {
		return err
	}
	return ValidateProcessEvidence(evidence)
}

// ValidateProcessEvidenceRecord validates the durable, non-secret structure
// and exact request identities without claiming live HCR provenance.
func (binding RequestBinding) ValidateProcessEvidenceRecord(evidence ProcessEvidence) error {
	if binding.Schema != RequestBindingSchema {
		return errors.New("unsupported host request binding schema")
	}
	if evidence.SchemaName != "gentle-ai.host-process-evidence" || evidence.SchemaVersion != 1 {
		return errors.New("unsupported host process evidence schema")
	}
	if evidence.ExecutionID != binding.ExecutionID ||
		evidence.RequestDigest != binding.RequestDigest ||
		evidence.ProgramDigest != binding.ProgramDigest ||
		evidence.ArgvDigest != binding.ArgvDigest ||
		evidence.CWDDigest != binding.CWDDigest ||
		evidence.EnvironmentDigest != binding.EnvironmentDigest {
		return errors.New("host process evidence does not match the exact request binding")
	}
	return ValidateProcessEvidenceRecord(evidence)
}

// ValidateProcessEvidence accepts only an unchanged value returned by HCR.
// Exported fields remain serializable, but callers cannot forge provenance
// after mutating terminal facts.
func ValidateProcessEvidence(evidence ProcessEvidence) error {
	if err := ValidateProcessEvidenceRecord(evidence); err != nil {
		return err
	}
	want := processEvidenceProvenance(evidence)
	if subtle.ConstantTimeCompare(evidence.provenanceSeal[:], want[:]) != 1 {
		return errors.New("host process evidence provenance is invalid")
	}
	return nil
}

// ValidateProcessEvidenceRecord validates durable structural invariants only.
// It never proves that the value was returned by a live HCR execution.
func ValidateProcessEvidenceRecord(evidence ProcessEvidence) error {
	if evidence.SchemaName != "gentle-ai.host-process-evidence" || evidence.SchemaVersion != 1 {
		return errors.New("unsupported host process evidence schema")
	}
	if evidence.ExecutionID == "" || !validEvidenceDigest(evidence.RequestDigest) ||
		!validEvidenceDigest(evidence.ProgramDigest) || !validEvidenceDigest(evidence.ArgvDigest) ||
		!validEvidenceDigest(evidence.CWDDigest) || !validEvidenceDigest(evidence.EnvironmentDigest) {
		return errors.New("host process evidence request binding is incomplete")
	}
	if evidence.StartedAt.IsZero() || evidence.FinishedAt.IsZero() ||
		evidence.StartedAt.Location() != time.UTC || evidence.FinishedAt.Location() != time.UTC ||
		evidence.FinishedAt.Before(evidence.StartedAt) {
		return errors.New("host process evidence timestamps are invalid")
	}
	if evidence.ExitCode < -1 {
		return errors.New("host process evidence exit code is invalid")
	}
	if err := validateStreamEvidence(evidence.Stdout); err != nil {
		return errors.New("host process stdout evidence is invalid")
	}
	if err := validateStreamEvidence(evidence.Stderr); err != nil {
		return errors.New("host process stderr evidence is invalid")
	}
	truncated := evidence.Stdout.Truncated || evidence.Stderr.Truncated
	switch evidence.TerminalCause {
	case TerminalExited:
		if evidence.ExitCode < 0 || truncated || !evidence.CleanupComplete ||
			!completedCleanupScope(evidence.CleanupScope) {
			return errors.New("exited host process evidence is incoherent")
		}
	case TerminalOutputTruncated:
		if !truncated || !evidence.CleanupComplete {
			return errors.New("truncated host process evidence is incoherent")
		}
	case TerminalCleanupFailed:
		if evidence.CleanupComplete {
			return errors.New("cleanup-failed host process evidence is incoherent")
		}
	case TerminalDeadlineExceeded, TerminalCancelled, TerminalSpawnFailed, TerminalControlFailed:
	default:
		return errors.New("host process evidence terminal cause is invalid")
	}
	return nil
}

func completedCleanupScope(scope string) bool {
	return scope == "process_group" || scope == "job_object"
}

func sealProcessEvidence(evidence ProcessEvidence) ProcessEvidence {
	evidence.provenanceSeal = processEvidenceProvenance(evidence)
	return evidence
}

func processEvidenceProvenance(evidence ProcessEvidence) [sha256.Size]byte {
	hash := sha256.New()
	writeValue(hash, "gentle-ai.host-process-evidence-provenance/v1")
	writeValue(hash, evidence.SchemaName)
	writeValue(hash, strconv.Itoa(evidence.SchemaVersion))
	writeValue(hash, evidence.ExecutionID)
	writeValue(hash, evidence.RequestDigest)
	writeValue(hash, evidence.ProgramDigest)
	writeValue(hash, evidence.ArgvDigest)
	writeValue(hash, evidence.CWDDigest)
	writeValue(hash, evidence.EnvironmentDigest)
	writeValue(hash, evidence.StartedAt.UTC().Format(time.RFC3339Nano))
	writeValue(hash, evidence.FinishedAt.UTC().Format(time.RFC3339Nano))
	writeValue(hash, string(evidence.TerminalCause))
	writeValue(hash, strconv.Itoa(evidence.ExitCode))
	writeStreamEvidence(hash, evidence.Stdout)
	writeStreamEvidence(hash, evidence.Stderr)
	writeValue(hash, evidence.CleanupScope)
	writeValue(hash, strconv.FormatBool(evidence.CleanupComplete))
	var seal [sha256.Size]byte
	copy(seal[:], hash.Sum(nil))
	return seal
}

func writeStreamEvidence(writer hash.Hash, evidence StreamEvidence) {
	writeValue(writer, evidence.RawDigest)
	writeValue(writer, strconv.FormatInt(evidence.RawBytes, 10))
	writeValue(writer, evidence.Retained)
	writeValue(writer, evidence.RetainedDigest)
	writeValue(writer, strconv.FormatBool(evidence.Truncated))
}

func validateStreamEvidence(evidence StreamEvidence) error {
	if evidence.RawBytes < 0 || !validEvidenceDigest(evidence.RawDigest) ||
		!validEvidenceDigest(evidence.RetainedDigest) {
		return errors.New("stream evidence identity is incomplete")
	}
	if evidence.RetainedDigest != digestValues("gentle-ai.host-retained-output/v1", evidence.Retained) {
		return errors.New("stream retained digest does not match")
	}
	if evidence.Truncated && evidence.Retained != "[TRUNCATED]" {
		return errors.New("truncated stream retained bytes are unsafe")
	}
	return nil
}

func validEvidenceDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	encoded := strings.TrimPrefix(value, "sha256:")
	decoded, err := hex.DecodeString(encoded)
	return err == nil && len(decoded) == sha256.Size && encoded == strings.ToLower(encoded)
}
