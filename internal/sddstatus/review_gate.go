package sddstatus

import (
	"fmt"
	"os"
	"strings"
)

func readSpecCounts(paths []string) (SpecCounts, error) {
	contents := make([]string, 0, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return SpecCounts{}, err
		}
		contents = append(contents, string(content))
	}
	return countSpecRequirementsAndScenarios(contents), nil
}

func readVerifyResult(path string, counts SpecCounts) (verifyResultEvaluation, error) {
	if path == "" {
		return verifyResultEvaluation{Reason: "verify result is missing"}, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return verifyResultEvaluation{}, err
	}
	return parseVerifyResult(string(content), counts), nil
}

func readText(path string) string {
	if path == "" {
		return ""
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(content)
}

// remediationFailedEvidenceRevision is the single source resolveBoundedRemediation
// and nativeRuntimeInstructions both call for "the failed evidence revision
// remediation must name" (#4481). It reports the ledger's own chain revision
// when the native runtime ledger holds an unremediated failure in the verify
// evidence's lineage, and otherwise keeps the verify-report's own revision --
// the same fallback status always used before a runtime ledger existed.
func remediationFailedEvidenceRevision(runtimeStatus *RuntimeStatus, verifyEvidenceRevision string) string {
	if runtimeStatus == nil {
		return verifyEvidenceRevision
	}
	if chainEvidence, ok := runtimeChainFailedEvidenceForVerify(*runtimeStatus, verifyEvidenceRevision); ok {
		return chainEvidence
	}
	return verifyEvidenceRevision
}

// resolveBoundedRemediation preserves failed-evidence truth without importing
// review authority. The deferred runtime codecs still validate any historical
// evidence they own; status only reports whether ordinary SDD evidence requires
// or completed remediation.
func resolveBoundedRemediation(required bool, verify verifyResultEvaluation, applyProgress string, runtimeStatus *RuntimeStatus) RemediationState {
	if !required {
		return RemediationState{}
	}
	if verify.EvidenceRevision == "" && strings.Contains(verify.Reason, "evidence_revision") {
		return RemediationState{Reason: fmt.Sprintf("verify evidence cannot enter remediation: %s", verify.Reason)}
	}
	revision := remediationFailedEvidenceRevision(runtimeStatus, verify.EvidenceRevision)
	state := RemediationState{
		Required:               true,
		FailedEvidenceRevision: revision,
		Reason:                 fmt.Sprintf("verify evidence requires independent SDD remediation for %s: %s", revision, verify.Reason),
	}
	evaluation := parseRemediationResult(applyProgress, revision)
	state.Complete = evaluation.Complete
	state.Required = !evaluation.Complete
	if state.Complete {
		state.Reason = ""
	}
	return state
}
