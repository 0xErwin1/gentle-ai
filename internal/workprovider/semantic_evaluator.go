package workprovider

import (
	"context"
	"errors"

	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
)

const SemanticEvaluationSchemaV1 = "gentle-ai.semantic-evaluation/v1"

type SemanticEvaluationOutcome string

const (
	SemanticEvaluationPassed    SemanticEvaluationOutcome = "passed"
	SemanticEvaluationNotProven SemanticEvaluationOutcome = "not_proven"
)

// SemanticEvaluation is an owner evaluator's exact typed decision. It is not
// itself durable authority: EPD validates these bindings, restores its private
// repository signer, and creates the signed SemanticVerdict.
type SemanticEvaluation struct {
	Schema               string
	RequirementRef       string
	CandidateRef         string
	RequestDigest        string
	ToolchainIdentityRef string
	StdoutRawDigest      string
	StderrRawDigest      string
	Outcome              SemanticEvaluationOutcome
}

func (evaluation SemanticEvaluation) validateFor(
	ticket evidence.ActionTicket,
	process hostruntime.ProcessEvidence,
) error {
	if evaluation.Schema != SemanticEvaluationSchemaV1 ||
		(evaluation.Outcome != SemanticEvaluationPassed &&
			evaluation.Outcome != SemanticEvaluationNotProven) {
		return errors.New("semantic evaluator returned an unsupported decision")
	}
	if evaluation.RequirementRef != ticket.SemanticRequirementRef ||
		evaluation.CandidateRef != ticket.CandidateRef ||
		evaluation.RequestDigest != process.RequestDigest ||
		evaluation.ToolchainIdentityRef != process.ToolchainIdentityRef ||
		evaluation.StdoutRawDigest != process.Stdout.RawDigest ||
		evaluation.StderrRawDigest != process.Stderr.RawDigest {
		return errors.New(
			"semantic evaluator decision does not bind the exact execution",
		)
	}
	return nil
}

// SemanticEvaluatorPort owns the meaning of one exact RAR requirement. HCR
// process facts, exit status, and stdout text cannot implement this port by
// themselves. A nil port is safe and leaves successful process evidence
// semantically incomplete.
type SemanticEvaluatorPort interface {
	EvaluateSemanticExecution(
		context.Context,
		evidence.ActionTicket,
		hostruntime.ProcessEvidence,
	) (SemanticEvaluation, error)
}
