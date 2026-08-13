package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// issue3094Journeys proves the public runtime contract from #3094: an
// interrupted attempt has no evidence revision, still closes exactly once,
// and an idempotent replay does not publish a second terminal record.
func issue3094Journeys() []Journey {
	return []Journey{{
		ID:     "j103-sdd-interrupted-settlement-omits-evidence",
		Title:  "Interrupted runtime settlement omits evidence and replays without a second record",
		Source: "https://github.com/Gentleman-Programming/gentle-ai/issues/3094",
		Steps: []Step{
			{Name: "fixture: repository with a committed OpenSpec change", Fixture: sddRuntimeRepo},
			{Name: "acquire through the public CLI", Requires: sddAttemptBeginCapability, Composite: issue3094Acquire},
			{Name: "observe the active running attempt with empty evidence", Requires: sddAttemptBeginCapability, Composite: issue3094ObserveActive},
			{Name: "settle interrupted without evidence", Requires: sddAttemptFinishCapability, Composite: issue3094SettleInterrupted},
			{Name: "verify no active attempt and one empty-evidence terminal record", Requires: sddAttemptFinishCapability, Composite: issue3094VerifyTerminal},
			{Name: "replay the identical settlement without publishing another record", Requires: sddAttemptFinishCapability, Composite: issue3094ReplaySettlement},
		},
	}}
}

func issue3094Run(r *journeyRun, args []string) (sddRuntimeStatus, error) {
	result := r.run(args, false)
	if result.ExitCode != 0 {
		return sddRuntimeStatus{}, fmt.Errorf("CLI %v failed: %s", args, firstLine(result.Stderr))
	}
	var status sddRuntimeStatus
	if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
		return status, fmt.Errorf("parse CLI %v: %w", args, err)
	}
	return status, nil
}

func issue3094Acquire(r *journeyRun) error {
	status, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	objective := append([]string{}, sddObjective[:len(sddObjective)-4]...)
	objective = append(objective, "--max-attempts", "2", "--max-changed-lines", "20")
	_, err = issue3094Run(r, sddAttemptArgs(r, "begin", status.Revision, "issue3094-begin", objective...))
	return err
}

func issue3094ObserveActive(r *journeyRun) error {
	status, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	if status.ActiveAttempt == nil || status.ActiveAttempt.Outcome != "running" || status.EvidenceRevision != "" {
		return fmt.Errorf("active status = %#v, want running with empty evidence", status)
	}
	return nil
}

func issue3094SettleInterrupted(r *journeyRun) error {
	status, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	if err := r.sandbox.write(filepath.Join(r.sandbox.Repo, ".issue3094-expected-revision"), status.Revision); err != nil {
		return err
	}
	legacyEvidence := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	interruptedArgs := append([]string{"--outcome", "interrupted", "--evidence-revision", legacyEvidence}, sddTerminalEvidence...)
	observation := r.run(sddAttemptArgs(r, "finish", status.Revision, "issue3094-finish", interruptedArgs...), false)
	if observation.ExitCode == 0 {
		return fmt.Errorf("interrupted settle with caller evidence was accepted")
	}
	unchanged, err := readRuntimeStatus(r)
	if err != nil || unchanged.Revision != status.Revision || unchanged.ActiveAttempt == nil {
		return fmt.Errorf("evidence-bearing interrupted refusal mutated runtime: before=%#v after=%#v err=%v", status, unchanged, err)
	}
	_, err = issue3094Run(r, sddAttemptArgs(r, "finish", status.Revision, "issue3094-finish", append([]string{"--outcome", "interrupted"}, sddTerminalEvidence...)...))
	return err
}

func issue3094VerifyTerminal(r *journeyRun) error {
	status, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	if status.ActiveAttempt != nil || status.EvidenceRevision != "" || len(status.Attempts) != 1 || status.Attempts[0].Outcome != "interrupted" || status.Attempts[0].EvidenceRevision != "" {
		return fmt.Errorf("terminal status = %#v, want one interrupted empty-evidence record", status)
	}
	return nil
}

func issue3094ReplaySettlement(r *journeyRun) error {
	status, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	expectedBytes, err := os.ReadFile(filepath.Join(r.sandbox.Repo, ".issue3094-expected-revision"))
	if err != nil {
		return err
	}
	if _, err = issue3094Run(r, sddAttemptArgs(r, "finish", string(expectedBytes), "issue3094-finish", append([]string{"--outcome", "interrupted"}, sddTerminalEvidence...)...)); err != nil {
		return fmt.Errorf("replayed settlement was not idempotent: %w", err)
	}
	final, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	if len(final.Attempts) != 1 || final.Revision != status.Revision || final.ActiveAttempt != nil || final.EvidenceRevision != "" {
		return fmt.Errorf("replayed status = %#v, want unchanged one-record terminal state", final)
	}
	return nil
}
