package main

import (
	"fmt"
	"path/filepath"
	"regexp"
)

// untrackedRecoveryLoopCandidatePath is the untracked file born during the
// attempt after a clean, undeclared begin -- exactly the shape #4040's
// reporters hit: the attempt's own product appears as untracked bytes while
// it runs, and a later settlement must account for it.
const untrackedRecoveryLoopCandidatePath = "docs/4040-recovered.md"

var sdd4040FinishCapability = &Capability{
	Verb:  []string{"sdd-attempt", "finish"},
	Flags: []string{"--untracked-scope", "--expected-untracked-inventory", "--intended-untracked"},
}

var untrackedRecoveryLoopDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// driveUntrackedInventoryRecoveryLoop reproduces issue #4040. Refusals from
// intendedUntrackedScopeForTarget and the runtime ledger name
// `gentle-ai review status --next-transition` as the recovery route for the
// digest they demand, but before this fix that route only ever published the
// digest inside next_transition.collect.inputs[].arguments -- a slot that
// stops firing the moment RDD is disabled (this journey deliberately leaves
// RDD at its default disabled state via Review: reviewUntouched) or the
// caller has already declared. The fix publishes eligible_untracked_inventory
// unconditionally at the STATUS top level instead (design decision 2), which
// this journey reads and feeds straight back into a successful
// `sdd-attempt finish`.
func driveUntrackedInventoryRecoveryLoop(r *journeyRun) error {
	status, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	if begin := r.run(sddAttemptArgs(r, "begin", status.Revision, "bench-4040-begin", sddObjective...), false); begin.ExitCode != 0 {
		return fmt.Errorf("#4040 clean begin exit=%d: %s", begin.ExitCode, firstLine(begin.Stderr))
	}

	// The attempt's own product, born untracked while it runs.
	if err := r.sandbox.write(filepath.Join(r.sandbox.Repo, untrackedRecoveryLoopCandidatePath), "recovered by #4040\n"); err != nil {
		return err
	}

	// #4040's exact symptom: the recovery route the refusal names must
	// itself publish the digest, discoverable at the TOP level -- not the
	// collect argument journeys_sdd_untracked.go already exercises.
	review, err := readStatusForContract(r, reviewContractV2)
	if err != nil {
		return err
	}
	if review.Schema != statusSchemaV7 {
		return fmt.Errorf("#4040 recovery STATUS schema = %q, want %q", review.Schema, statusSchemaV7)
	}
	if !untrackedRecoveryLoopDigestPattern.MatchString(review.EligibleUntrackedInventory) {
		return fmt.Errorf("#4040 recovery STATUS did not publish a top-level eligible_untracked_inventory digest: %q", review.EligibleUntrackedInventory)
	}

	if status, err = readRuntimeStatus(r); err != nil {
		return err
	}
	finished := r.run(sddAttemptArgs(r, "finish", status.Revision, "bench-4040-finish",
		append([]string{
			"--outcome", "failed", "--evidence-revision", sddFailedEvidence,
			"--untracked-scope", "select", "--intended-untracked", untrackedRecoveryLoopCandidatePath,
			"--expected-untracked-inventory", review.EligibleUntrackedInventory,
		}, sddTerminalEvidence...)...), false)
	if finished.ExitCode != 0 {
		return fmt.Errorf("#4040 declared finish exit=%d: %s", finished.ExitCode, firstLine(finished.Stderr))
	}

	var final struct {
		Attempts []struct {
			Outcome           string   `json:"outcome"`
			IntendedUntracked []string `json:"intended_untracked"`
		} `json:"attempts"`
	}
	if err := proveJSON(r.sandbox, &final, "sdd-attempt", "status", "--cwd", r.sandbox.Repo, "--change", sddChange); err != nil {
		return err
	}
	if len(final.Attempts) != 1 || final.Attempts[0].Outcome != "failed" ||
		len(final.Attempts[0].IntendedUntracked) != 1 || final.Attempts[0].IntendedUntracked[0] != untrackedRecoveryLoopCandidatePath {
		return fmt.Errorf("#4040 declared finish did not record the recovered selection: %#v", final)
	}
	return nil
}

func untrackedInventoryRecoveryLoopJourneys() []Journey {
	return []Journey{{
		ID:     "j4040-untracked-inventory-recovery-loop",
		Review: reviewUntouched,
		Title:  "#4040: a finish refusal's named recovery route now publishes the digest it demands, even with RDD disabled",
		Source: "issue #4040: intendedUntrackedScopeForTarget/ValidateIntendedUntrackedSelection refusals name `gentle-ai review status --next-transition` as the recovery route, but that route only published the digest inside a collect argument that RDD-disabled and post-declaration paths both suppress; fixed by publishing eligible_untracked_inventory unconditionally at the status/v7 top level",
		Steps: []Step{
			{Name: "fixture: runtime repository", Fixture: sddRuntimeRepo},
			{Name: "clean begin, born-during untracked candidate, top-level STATUS recovers the digest, declared finish succeeds", Requires: sdd4040FinishCapability, Composite: driveUntrackedInventoryRecoveryLoop},
		},
	}}
}
