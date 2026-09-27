package shellinstaller

// MissingProof names evidence a future installer must obtain independently.
// A string here is never evidence that a proof exists or has been checked.
type MissingProof string

const (
	ProofIndependentSource MissingProof = "independent-source"
	ProofPhysicalInstance  MissingProof = "physical-instance"
	ProofBoundConsent      MissingProof = "instance-bound-consent"
	ProofRollback          MissingProof = "rollback"
	ProofExecutedBytes     MissingProof = "executed-bytes"
	ProofIndependentReady  MissingProof = "independent-ready"
)

// DraftPlan contains product intent and missing evidence only. It has no
// executable action, source, home, approval, or Ready state.
type DraftPlan struct {
	Intent        CommandModeIntent
	MissingProofs [6]MissingProof
}

// BuildDraftPlan describes both command modes without calling Profile.Validate
// (which still refuses gentle-shell) or resolving any local configuration.
func BuildDraftPlan(profile Profile) (DraftPlan, error) {
	intent, err := profile.DescribeCommandModeIntent()
	if err != nil {
		return DraftPlan{}, err
	}
	return DraftPlan{
		Intent: intent,
		MissingProofs: [6]MissingProof{
			ProofIndependentSource,
			ProofPhysicalInstance,
			ProofBoundConsent,
			ProofRollback,
			ProofExecutedBytes,
			ProofIndependentReady,
		},
	}, nil
}
