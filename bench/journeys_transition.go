package main

import (
	"fmt"
	"strings"
)

// Transition journeys: states reached by SEQUENCE, not by damage.
//
// Every other journey in this corpus runs with a fixed configuration decided
// before the first command: SDD on or off, RDD on or off, never changed
// mid-flight. The damaged-store axis covers states reached by corrupting files
// the CLI cannot produce. Between those two lies the gap this axis exists for:
// states reached by two ordinary operations that are each individually valid.
//
// #2830 is why. `begin`, `finish failed`, `rescope`, `rescope` are four
// ordinary calls with no damaged file and no mode change, and the fourth
// leaves the attempt ledger permanently unreadable on a published build. Zero
// journeys chained them, so the corpus was green while the product could brick
// a change. A user found it, not us.
//
// Each journey here ends by reading the ledger back. That last assertion is
// the invariant #2830 broke and nothing checked: a refusal may reject an
// operation, and must never leave the store unable to answer.

const transitionAxis = "transition"

func init() {
	RegisterAxis(Axis{
		Name:     transitionAxis,
		Title:    "Journeys that change mode or chain operations in the middle of a lifecycle",
		BlackBox: true,
		Properties: []string{
			"Every state here is reached through the CLI alone. No fixture authors a store file, which is what separates this axis from damaged-store: these are sequences a user can type, not corruption a user cannot cause.",
			"Each journey asserts the ledger is still READABLE after the sequence, not merely that the last command refused. A refusal that leaves the store unable to answer is a wedge, and that distinction is the reason this axis exists.",
			"The sequences are drawn from what operators actually do between phases: narrow a scope twice, change their mind about the scope, turn review off mid-change and keep delivering. None of them require an unusual repository.",
		},
		Journeys: transitionJourneys,
	})
}

func transitionJourneys() []Journey {
	return []Journey{
		{
			ID:     "tr01-consecutive-rescope-keeps-the-ledger-readable",
			Title:  "Two rescopes in a row: whatever the second one answers, the ledger must still answer",
			Source: "community report #2830 on published 2.4.0-rc.1",
			// The reported shape, reproduced through the CLI only. The first
			// rescope succeeds. The second is where write-time preconditions
			// and replay-time preconditions disagree: replay requires the last
			// attempt to belong to the CURRENT objective, and after the first
			// rescope it belongs to the previous generation, so the record is
			// committed and then rejected by its own validator.
			//
			// This journey deliberately does NOT assert that the second rescope
			// succeeds. Whether consecutive rescopes are supported is a product
			// decision that is still open. What is not open is the last step:
			// after any answer, `status` must still work. That is the assertion
			// that would have caught #2830 before it shipped.
			Steps: []Step{
				{Name: "fixture: repository with a committed OpenSpec change", Fixture: sddRuntimeRepo},
				{Name: "begin an objective and fail it with the workspace unchanged",
					Requires: sddAttemptBeginCapability, Composite: transitionBeginAndFail},
				{Name: "narrow the objective once", Requires: sddAttemptResetCapability,
					Composite: transitionRescope("bench-rescope-one", "narrower work unit one")},
				{Name: "narrow it again, which is where the report lands",
					Requires:  sddAttemptResetCapability,
					Composite: transitionRescope("bench-rescope-two", "narrower work unit two")},
				{Name: "the ledger still answers", Composite: transitionProveLedgerReadable},
			},
		},
		{
			ID:     "tr02-reset-after-rescope-keeps-the-ledger-readable",
			Title:  "Narrow, spend the budget, then reset: the widening route, driven end to end",
			Source: "#2769's documented exhaust-to-widen route + #2830's write-versus-replay surface",
			// What this proves, stated narrowly because two decoys narrowed it:
			// the exhaust-to-widen route that #2769's refusals advertise can be
			// walked end to end. Narrow the objective, spend the narrowed
			// budget, land on decision-required, reset from there.
			//
			// It does NOT guard the wedge class tr01 measures, and the honest
			// reason is worth keeping. An earlier version ran `reset` straight
			// after the rescope; injecting #2830's exact asymmetry into reset's
			// replay changed nothing, because reset is refused at write time in
			// that state and never reaches the publication path a wedge lives
			// on. Rebuilt to reach a reset that genuinely publishes, the same
			// injected asymmetry still does not fire, because by then the last
			// attempt belongs to the current objective and the condition is
			// unreachable. Reset does not have rescope's asymmetry.
			//
			// The assertion does bite: while the exhaust loop was misconfigured
			// against the pre-rescope work unit, every command "ran", nothing
			// errored, and this journey failed on the state it never reached.
			Steps: []Step{
				{Name: "fixture: repository with a committed OpenSpec change", Fixture: sddRuntimeRepo},
				{Name: "begin an objective and fail it with the workspace unchanged",
					Requires: sddAttemptBeginCapability, Composite: transitionBeginAndFail},
				{Name: "narrow the objective", Requires: sddAttemptResetCapability,
					Composite: transitionRescope("bench-rescope-before-reset", "narrower before reset")},
				{Name: "spend the narrowed objective's remaining attempts",
					Requires: sddAttemptBeginCapability, Composite: transitionExhaustAttempts},
				{Name: "reset from decision-required, which is the route the refusals name",
					Requires: sddAttemptResetCapability, Composite: transitionReset("bench-reset-after-rescope")},
				{Name: "the ledger still answers", Composite: transitionProveLedgerReadable},
			},
		},
		{
			ID:     "tr03-review-disabled-mid-change-keeps-sdd-moving",
			Title:  "Turn review off in the middle of a change: SDD must keep working under ordinary policy",
			Source: "maintainer-named coverage gap: no journey changes mode mid-lifecycle",
			// The kill switch is user-owned and documented as always available.
			// Every journey until now decided it before the first command, so
			// nothing proved a change already in flight survives the switch.
			//
			// The claim under test is the product's own: disabling review hands
			// delivery to ordinary repository policy and never wedges the work
			// that was already underway. If SDD cannot continue after the
			// switch, the switch is not the escape hatch it is advertised as.
			Steps: []Step{
				{Name: "fixture: repository with a committed OpenSpec change", Fixture: sddRuntimeRepo},
				{Name: "begin an objective and fail it with the workspace unchanged",
					Requires: sddAttemptBeginCapability, Composite: transitionBeginAndFail},
				{Name: "turn review off for this repository only", Requires: modeCapability,
					Args: productArgs("review", "mode", "disable", "--scope", "clone")},
				{Name: "the ledger still answers with review off", Composite: transitionProveLedgerReadable},
				{Name: "and the work continues: begin the next attempt",
					Requires: sddAttemptBeginCapability, Composite: transitionBeginAgain},
			},
		},
	}
}

// transitionBeginAndFail reaches the state every journey here builds on: one
// terminal failed attempt with the workspace untouched, which is the zero-drift
// shape rescope admits.
func transitionBeginAndFail(r *journeyRun) error {
	status, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	r.run(sddAttemptArgs(r, "begin", status.Revision, "bench-transition-begin", sddObjective...), false)

	if status, err = readRuntimeStatus(r); err != nil {
		return err
	}
	r.run(sddAttemptArgs(r, "finish", status.Revision, "bench-transition-finish",
		append([]string{"--outcome", "failed", "--evidence-revision", sddFailedEvidence}, sddTerminalEvidence...)...), false)

	status, err = readRuntimeStatus(r)
	if err != nil {
		return err
	}
	if len(status.Attempts) != 1 {
		return fmt.Errorf("expected exactly one terminal attempt, got %d", len(status.Attempts))
	}
	return nil
}

// transitionRescope narrows the objective. It tolerates a refusal: whether the
// product admits this call is the open question, and the journey measures what
// happens to the store either way rather than demanding one answer.
func transitionRescope(requestID, workUnit string) func(*journeyRun) error {
	return func(r *journeyRun) error {
		status, err := readRuntimeStatus(r)
		if err != nil {
			return err
		}
		r.run(sddAttemptArgs(r, "rescope", status.Revision, requestID,
			"--work-unit", workUnit,
			"--evidence-goal", "bench proves the narrowed objective",
			"--max-attempts", "6", "--max-changed-lines", "600",
			"--reason", "the benchmark narrows this objective",
			"--actor", "bench maintainer"), false)
		return nil
	}
}

// narrowedObjective is the scope transitionRescope publishes. The exhaust loop
// must begin against THIS, not sddObjective: begin with a work unit other than
// the current objective's is refused as an objective change, so a loop using
// the original scope spins without ever creating an attempt. That mistake is
// worth naming because it is invisible from the outside: every command "ran",
// nothing errored, and the journey simply never reached the state it claimed.
var narrowedObjective = []string{
	"--work-unit", "narrower before reset",
	"--evidence-goal", "bench proves the narrowed objective",
	"--max-attempts", "6", "--max-changed-lines", "600",
}

// transitionExhaustAttempts drives the narrowed objective to decision-required
// by failing attempts until the ledger stops offering begin. That is the state
// where reset genuinely publishes, which is what makes tr02 able to observe a
// wedge at all: an operation refused before publication cannot leave a bad
// record behind, so a journey that only reaches a refusal measures nothing.
func transitionExhaustAttempts(r *journeyRun) error {
	for attempt := 0; attempt < 8; attempt++ {
		status, err := readRuntimeStatus(r)
		if err != nil {
			return err
		}
		if status.NextAction != "begin" {
			return nil
		}
		id := fmt.Sprintf("bench-exhaust-%d", attempt)
		r.run(sddAttemptArgs(r, "begin", status.Revision, id+"-begin", narrowedObjective...), false)

		if status, err = readRuntimeStatus(r); err != nil {
			return err
		}
		r.run(sddAttemptArgs(r, "finish", status.Revision, id+"-finish",
			append([]string{"--outcome", "failed", "--evidence-revision", sddFailedEvidence}, sddTerminalEvidence...)...), false)
	}
	return fmt.Errorf("the objective never reached decision-required after eight attempts")
}

// transitionReset consumes the post-rescope state through reset instead.
func transitionReset(requestID string) func(*journeyRun) error {
	return func(r *journeyRun) error {
		status, err := readRuntimeStatus(r)
		if err != nil {
			return err
		}
		r.run(sddAttemptArgs(r, "reset", status.Revision, requestID,
			"--reason", "the benchmark changes the objective",
			"--actor", "bench maintainer"), false)
		return nil
	}
}

// transitionBeginAgain proves the lifecycle still moves, not merely that the
// store parses. A readable ledger that admits no next operation is still stuck.
func transitionBeginAgain(r *journeyRun) error {
	status, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	r.run(sddAttemptArgs(r, "begin", status.Revision, "bench-transition-begin-again", sddObjective...), false)
	return nil
}

// transitionProveLedgerReadable is this axis's whole point.
//
// `sdd-attempt status` is read-only and must answer from any state a sequence
// of ordinary commands can reach. #2830 is exactly this assertion failing: the
// second rescope committed a record its own replay rejects, so every later read
// walked into it and the change could not be continued or abandoned.
//
// It reads through the product rather than the store files, so it measures what
// an operator sees.
func transitionProveLedgerReadable(r *journeyRun) error {
	observation := r.run([]string{"sdd-attempt", "status", "--cwd", r.sandbox.Repo, "--change", sddChange}, false)
	if observation.ExitCode != 0 {
		return fmt.Errorf("the ledger stopped answering after this sequence: exit %d: %s",
			observation.ExitCode, firstLine(observation.Stderr))
	}
	if !strings.Contains(observation.Stdout, "\"revision\"") {
		return fmt.Errorf("sdd-attempt status answered without a revision: %s", firstLine(observation.Stdout))
	}
	return nil
}
