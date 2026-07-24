package hostruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBindExecStepMatchesExecutorEvidence(t *testing.T) {
	t.Parallel()

	step := helperStep(t, t.TempDir(), "exit", "0")
	step.Env["BINDING_VALUE"] = "literal"
	binding, err := BindExecStep(step)
	if err != nil {
		t.Fatalf("BindExecStep() error = %v", err)
	}
	evidence, err := NewExecutor().execute(context.Background(), step)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if err := binding.ValidateProcessEvidence(evidence); err != nil {
		t.Fatalf("ValidateProcessEvidence() error = %v", err)
	}
}

func TestProcessEvidenceProvenanceRejectsTerminalFactMutation(t *testing.T) {
	t.Parallel()

	step := helperStep(t, t.TempDir(), "exit", "0")
	evidence, err := NewExecutor().execute(context.Background(), step)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ProcessEvidence)
	}{
		{name: "exit code", mutate: func(value *ProcessEvidence) { value.ExitCode = 1 }},
		{name: "timestamp", mutate: func(value *ProcessEvidence) {
			value.FinishedAt = value.FinishedAt.Add(time.Nanosecond)
		}},
		{name: "raw digest", mutate: func(value *ProcessEvidence) {
			value.Stdout.RawDigest = digestValues("forged", "raw")
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			changed := evidence
			test.mutate(&changed)
			if err := ValidateProcessEvidence(changed); err == nil {
				t.Fatal("ValidateProcessEvidence() accepted mutated terminal facts")
			}
		})
	}
}

func TestDurableProcessRecordHasNoLiveProvenanceAndRequiresCleanupScope(t *testing.T) {
	t.Parallel()

	step := helperStep(t, t.TempDir(), "exit", "0")
	evidence, err := NewExecutor().execute(context.Background(), step)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var replayed ProcessEvidence
	if err := json.Unmarshal(payload, &replayed); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProcessEvidenceRecord(replayed); err != nil {
		t.Fatalf("ValidateProcessEvidenceRecord() error = %v", err)
	}
	if err := ValidateProcessEvidence(replayed); err == nil {
		t.Fatal("serialized process record retained live HCR provenance")
	}

	replayed.CleanupScope = ""
	replayed = sealProcessEvidence(replayed)
	if err := ValidateProcessEvidence(replayed); err == nil {
		t.Fatal("complete terminal evidence accepted a missing cleanup scope")
	}
}

func TestRequestBindingRejectsDifferentExactRequest(t *testing.T) {
	t.Parallel()

	step := helperStep(t, t.TempDir(), "exit", "0")
	binding, err := BindExecStep(step)
	if err != nil {
		t.Fatalf("BindExecStep() error = %v", err)
	}
	changed := step
	changed.Args = append([]string(nil), step.Args...)
	changed.Args[len(changed.Args)-1] = "23"
	evidence, err := NewExecutor().execute(context.Background(), changed)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if err := binding.ValidateProcessEvidence(evidence); err == nil {
		t.Fatal("ValidateProcessEvidence() accepted evidence from a different argv")
	}
}

func TestExecuteRejectsStaleOwnerObservedToolchainBeforeLaunch(t *testing.T) {
	t.Parallel()

	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	program := copiedHelperPath(t.TempDir())
	if err := os.WriteFile(program, payload, 0o700); err != nil {
		t.Fatal(err)
	}
	step := helperStep(t, t.TempDir(), "exit", "0")
	step.Program = program
	step.SemanticRequirementRef = digestValues("owner-requirement", "unit")
	binding, err := BindExecStep(step)
	if err != nil {
		t.Fatal(err)
	}
	if binding.ToolchainState != ToolchainIdentityObserved {
		t.Fatalf("toolchain observation = %#v", binding.ToolchainIdentity)
	}
	step.ToolchainIdentity = binding.ToolchainIdentity
	if err := os.WriteFile(program, append(payload, '\n'), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewExecutor().execute(context.Background(), step); !errors.Is(
		err,
		ErrToolchainIdentityChanged,
	) {
		t.Fatalf("stale toolchain execution error = %v", err)
	}
}

func TestExecuteAuthorizedReobservesToolchainAfterClaimBeforeStart(t *testing.T) {
	t.Parallel()

	step, original, binding := copiedBoundHelperStep(t, "sleep", "30000")
	executor := NewExecutor()
	claimed := false
	capability, err := executor.ActivateLaunch(
		context.Background(),
		LaunchBinding{
			Schema:          LaunchBindingSchemaV1,
			WorkRunID:       "toolchain-pre-start",
			ReservationRef:  digestValues("reservation", "toolchain-pre-start"),
			ActionTicketRef: digestValues("ticket", "toolchain-pre-start"),
			RequestDigest:   binding.RequestDigest,
		},
		func(context.Context) error {
			claimed = true
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var mutationErr error
	executor.testHooks.beforeFinalPreStartObservation = func() {
		mutationErr = replaceCopiedHelper(step.Program, original)
	}
	evidence, err := executor.ExecuteAuthorized(
		context.Background(),
		step,
		capability,
	)
	if mutationErr != nil {
		t.Fatal(mutationErr)
	}
	var executionErr *ExecutionError
	if !claimed ||
		!errors.As(err, &executionErr) ||
		executionErr.Kind != TerminalToolchainChanged {
		t.Fatalf("claimed/error = %v/%v", claimed, err)
	}
	if evidence.TerminalCause != TerminalToolchainChanged ||
		evidence.CleanupScope != "not_started" ||
		!evidence.CleanupComplete ||
		evidence.ExitCode != -1 {
		t.Fatalf("pre-start toolchain evidence = %#v", evidence)
	}
	if err := ValidateProcessEvidence(evidence); err != nil {
		t.Fatalf("toolchain-change evidence validation error = %v", err)
	}
}

func TestExecuteAuthorizedTerminatesWhenToolchainChangesInFinalStartWindow(t *testing.T) {
	t.Parallel()

	step, original, binding := copiedBoundHelperStep(t, "sleep", "30000")
	executor := NewExecutor()
	capability, err := executor.ActivateLaunch(
		context.Background(),
		LaunchBinding{
			Schema:          LaunchBindingSchemaV1,
			WorkRunID:       "toolchain-start-window",
			ReservationRef:  digestValues("reservation", "toolchain-start-window"),
			ActionTicketRef: digestValues("ticket", "toolchain-start-window"),
			RequestDigest:   binding.RequestDigest,
		},
		func(context.Context) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	var mutationErr error
	executor.testHooks.afterFinalPreStartObservation = func() {
		mutationErr = replaceCopiedHelper(step.Program, original)
	}
	evidence, err := executor.ExecuteAuthorized(
		context.Background(),
		step,
		capability,
	)
	if mutationErr != nil {
		t.Fatal(mutationErr)
	}
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) ||
		executionErr.Kind != TerminalToolchainChanged {
		t.Fatalf("start-window error = %v", err)
	}
	if evidence.TerminalCause != TerminalToolchainChanged ||
		!completedCleanupScope(evidence.CleanupScope) ||
		!evidence.CleanupComplete {
		t.Fatalf("post-start toolchain evidence = %#v", evidence)
	}
	if err := ValidateProcessEvidence(evidence); err != nil {
		t.Fatalf("toolchain-change evidence validation error = %v", err)
	}
}

func TestExecuteAuthorizedRejectsEvidenceWhenToolchainChangesBeforeAcceptance(t *testing.T) {
	t.Parallel()

	step, original, binding := copiedBoundHelperStep(t, "exit", "0")
	executor := NewExecutor()
	capability, err := executor.ActivateLaunch(
		context.Background(),
		LaunchBinding{
			Schema:          LaunchBindingSchemaV1,
			WorkRunID:       "toolchain-before-acceptance",
			ReservationRef:  digestValues("reservation", "toolchain-before-acceptance"),
			ActionTicketRef: digestValues("ticket", "toolchain-before-acceptance"),
			RequestDigest:   binding.RequestDigest,
		},
		func(context.Context) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	var mutationErr error
	executor.testHooks.beforeFinalEvidenceObservation = func() {
		mutationErr = replaceCopiedHelper(step.Program, original)
	}
	evidence, err := executor.ExecuteAuthorized(
		context.Background(),
		step,
		capability,
	)
	if mutationErr != nil {
		t.Fatal(mutationErr)
	}
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) ||
		executionErr.Kind != TerminalToolchainChanged ||
		evidence.TerminalCause != TerminalToolchainChanged ||
		!evidence.CleanupComplete {
		t.Fatalf("pre-acceptance evidence/error = %#v/%v", evidence, err)
	}
	if err := ValidateProcessEvidence(evidence); err != nil {
		t.Fatalf("toolchain-change evidence validation error = %v", err)
	}
}

func TestLegacyV1RequestAndProcessRecordsRetainExactShape(t *testing.T) {
	t.Parallel()

	step := helperStep(t, t.TempDir(), "exit", "0")
	step.EvidenceVersion = 1
	binding, err := BindExecStep(step)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := NewExecutor().execute(context.Background(), step)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Schema != RequestBindingSchemaV1 ||
		evidence.SchemaVersion != 1 ||
		!binding.ToolchainIdentity.IsZero() ||
		!evidence.ToolchainIdentity.IsZero() {
		t.Fatalf("legacy binding/evidence gained v2 fields: %#v %#v", binding, evidence)
	}
	for name, value := range map[string]any{
		"binding":  binding,
		"evidence": evidence,
	} {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if string(payload) == "" ||
			strings.Contains(string(payload), "toolchainIdentity") ||
			strings.Contains(string(payload), "semanticRequirementRef") {
			t.Fatalf("%s v1 JSON gained v2 fields: %s", name, payload)
		}
	}
	if err := binding.ValidateProcessEvidence(evidence); err != nil {
		t.Fatalf("legacy binding validation error = %v", err)
	}
}

func copiedBoundHelperStep(
	t *testing.T,
	mode string,
	args ...string,
) (ExecStep, []byte, RequestBinding) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	program := copiedHelperPath(t.TempDir())
	if err := os.WriteFile(program, payload, 0o700); err != nil {
		t.Fatal(err)
	}
	step := helperStep(t, t.TempDir(), mode, args...)
	step.Program = program
	binding, err := BindExecStep(step)
	if err != nil {
		t.Fatal(err)
	}
	step.ToolchainIdentity = binding.ToolchainIdentity
	return step, payload, binding
}

func copiedHelperPath(directory string) string {
	name := "copied-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(directory, name)
}

func replaceCopiedHelper(program string, original []byte) error {
	replacement := program + ".replacement"
	if err := os.WriteFile(
		replacement,
		append(append([]byte{}, original...), '\n'),
		0o700,
	); err != nil {
		return err
	}
	if err := os.Remove(program); err != nil {
		return err
	}
	return os.Rename(replacement, program)
}
