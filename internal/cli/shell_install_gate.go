package cli

import "github.com/gentleman-programming/gentle-ai/v3/internal/shellinstaller"

// ShellCandidateRefusalKind cannot represent an approval or execution state.
type ShellCandidateRefusalKind string

const ShellCandidateRefusalNotAuthorized ShellCandidateRefusalKind = "not-authorized"

// ShellCandidateNotAuthorized is the terminal result of this distinct draft
// seam. It is not a generic Pi package install failure or a consent artifact.
type ShellCandidateNotAuthorized struct {
	Kind ShellCandidateRefusalKind
}

func (r ShellCandidateNotAuthorized) Error() string {
	return "shell install candidate not authorized; this draft gate cannot execute; run gentle-ai shell install --help for the independently qualified installer"
}

// GateShellInstallCandidate only describes and refuses. It has no paths, home,
// resolver, state, backup, command runner, generic install, or pipeline access.
func GateShellInstallCandidate(profile shellinstaller.Profile) (shellinstaller.DraftPlan, error) {
	draft, err := shellinstaller.BuildDraftPlan(profile)
	if err != nil {
		return shellinstaller.DraftPlan{}, err
	}
	return draft, ShellCandidateNotAuthorized{Kind: ShellCandidateRefusalNotAuthorized}
}
