package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var finalizeActionEligibilityPreflightCapability = &Capability{
	Verb:  []string{"review", "finalize"},
	Flags: []string{"--action-eligibility", "--next-transition", "--contract", "--cwd"},
}

// issue2906Journeys proves the missing contract is rejected before FINALIZE
// can inspect or mutate an authority, and never creates a defect report.
func issue2906Journeys() []Journey {
	return []Journey{{
		ID:     "j99-issue-2906-finalize-missing-contract",
		Title:  "FINALIZE action outputs without a contract are preflight refusals",
		Source: "#2906: missing FINALIZE --contract is input validation, not an unknown outcome",
		Steps: []Step{
			{Name: "fixture: repo", Fixture: baseRepo},
			{
				Name:      "action eligibility and next transition require a contract",
				Requires:  finalizeActionEligibilityPreflightCapability,
				Composite: assertIssue2906MissingContractPreflight,
			},
		},
	}}
}

func assertIssue2906MissingContractPreflight(run *journeyRun) error {
	cases := []struct {
		name  string
		flags []string
	}{
		{name: "action eligibility only", flags: []string{"--action-eligibility"}},
		{name: "next transition only", flags: []string{"--next-transition"}},
		{name: "both outputs", flags: []string{"--action-eligibility", "--next-transition"}},
	}
	for _, test := range cases {
		args := append([]string{"review", "finalize"}, test.flags...)
		args = append(args, "--cwd", run.sandbox.Repo)
		observation := run.run(args, false)
		if observation.ExitCode == 0 {
			return fmt.Errorf("%s accepted missing --contract", test.name)
		}
		want := "Error: --action-eligibility and --next-transition require --contract " + reviewContract
		if !strings.Contains(observation.Stderr, want) {
			return fmt.Errorf("%s diagnostic = %q, want %q", test.name, strings.TrimSpace(observation.Stderr), want)
		}
		if strings.Contains(observation.Stdout+observation.Stderr, "operation_outcome_unknown") ||
			strings.Contains(observation.Stdout+observation.Stderr, "defect report") {
			return fmt.Errorf("%s emitted generic fault or defect-report text", test.name)
		}
	}

	authorityRoot := filepath.Join(run.sandbox.Repo, ".git", "gentle-ai")
	if _, err := os.Stat(authorityRoot); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("missing-contract preflight created authority state: %v", err)
	}
	return nil
}
