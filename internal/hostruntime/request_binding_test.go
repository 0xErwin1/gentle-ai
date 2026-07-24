package hostruntime

import (
	"context"
	"encoding/json"
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
