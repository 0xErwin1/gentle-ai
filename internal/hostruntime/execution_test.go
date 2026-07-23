package hostruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExecutePreservesLiteralArgumentsAndExactEnvironment(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	step := helperStep(t, cwd, "echo", `a;b`, `$(touch nope)`, `space value`, `*.go`)
	step.Env["DECLARED_VALUE"] = "visible"
	evidence, err := NewExecutor().Execute(context.Background(), step)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if evidence.TerminalCause != TerminalExited || evidence.ExitCode != 0 || !evidence.CleanupComplete {
		t.Fatalf("evidence = %#v", evidence)
	}

	var output struct {
		Args       []string `json:"args"`
		CWD        string   `json:"cwd"`
		Declared   string   `json:"declared"`
		Undeclared string   `json:"undeclared"`
	}
	if err := json.Unmarshal([]byte(evidence.Stdout.Retained), &output); err != nil {
		t.Fatalf("decode helper output: %v\n%s", err, evidence.Stdout.Retained)
	}
	wantArgs := []string{`a;b`, `$(touch nope)`, `space value`, `*.go`}
	if fmt.Sprint(output.Args) != fmt.Sprint(wantArgs) {
		t.Fatalf("args = %#v, want %#v", output.Args, wantArgs)
	}
	if output.CWD != cwd {
		t.Fatalf("cwd = %q, want %q", output.CWD, cwd)
	}
	if output.Declared != "visible" {
		t.Fatalf("declared env = %q", output.Declared)
	}
	if output.Undeclared != "" {
		t.Fatalf("undeclared inherited env leaked: %q", output.Undeclared)
	}
	if evidence.ProgramDigest == "" || evidence.ArgvDigest == "" ||
		evidence.CWDDigest == "" || evidence.EnvironmentDigest == "" ||
		evidence.RequestDigest == "" {
		t.Fatalf("missing request binding: %#v", evidence)
	}
}

func TestExecuteRecordsZeroAndNonzeroExitAsCoherentEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code int
	}{
		{name: "zero", code: 0},
		{name: "nonzero", code: 23},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			step := helperStep(t, t.TempDir(), "exit", strconv.Itoa(test.code))
			evidence, err := NewExecutor().Execute(context.Background(), step)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if evidence.TerminalCause != TerminalExited {
				t.Fatalf("TerminalCause = %q", evidence.TerminalCause)
			}
			if evidence.ExitCode != test.code {
				t.Fatalf("ExitCode = %d, want %d", evidence.ExitCode, test.code)
			}
			if !evidence.CleanupComplete {
				t.Fatal("CleanupComplete = false")
			}
		})
	}
}

func TestExecuteDrainsAndClassifiesTruncatedStreams(t *testing.T) {
	t.Parallel()

	step := helperStep(t, t.TempDir(), "streams", "1048576")
	step.Limits = StreamLimits{StdoutBytes: 31, StderrBytes: 29}
	evidence, err := NewExecutor().Execute(context.Background(), step)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if evidence.TerminalCause != TerminalOutputTruncated {
		t.Fatalf("TerminalCause = %q", evidence.TerminalCause)
	}
	if !evidence.Stdout.Truncated || !evidence.Stderr.Truncated {
		t.Fatalf("truncation = stdout:%v stderr:%v", evidence.Stdout.Truncated, evidence.Stderr.Truncated)
	}
	if evidence.Stdout.RawBytes != 1048576 || evidence.Stderr.RawBytes != 1048576 {
		t.Fatalf("raw bytes = stdout:%d stderr:%d", evidence.Stdout.RawBytes, evidence.Stderr.RawBytes)
	}
	if evidence.Stdout.Retained != "[TRUNCATED]" || evidence.Stderr.Retained != "[TRUNCATED]" {
		t.Fatalf("truncated output was retained: stdout:%q stderr:%q", evidence.Stdout.Retained, evidence.Stderr.Retained)
	}
	if !evidence.CleanupComplete {
		t.Fatal("CleanupComplete = false")
	}
}

func TestExecuteRedactsRetainedOutputWithoutChangingRawEvidence(t *testing.T) {
	t.Parallel()

	const secret = "super-secret-token"
	step := helperStep(t, t.TempDir(), "redact", secret)
	step.Redaction = RedactionPlan{Literals: []string{"secret", secret}}
	evidence, err := NewExecutor().Execute(context.Background(), step)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(evidence.Stdout.Retained, secret) ||
		strings.Contains(evidence.Stderr.Retained, secret) {
		t.Fatalf("secret leaked in retained output: %#v", evidence)
	}
	if !strings.Contains(evidence.Stdout.Retained, "[REDACTED]") ||
		!strings.Contains(evidence.Stderr.Retained, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %#v", evidence)
	}
	if evidence.Stdout.RawBytes == 0 || evidence.Stdout.RawDigest == "" {
		t.Fatalf("raw evidence missing: %#v", evidence.Stdout)
	}
}

func TestExecuteDistinguishesDeadlineAndCancellation(t *testing.T) {
	t.Parallel()

	t.Run("deadline", func(t *testing.T) {
		t.Parallel()
		step := helperStep(t, t.TempDir(), "sleep", "5000")
		step.Deadline = 75 * time.Millisecond
		evidence, err := NewExecutor().Execute(context.Background(), step)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if evidence.TerminalCause != TerminalDeadlineExceeded {
			t.Fatalf("TerminalCause = %q", evidence.TerminalCause)
		}
		if !evidence.CleanupComplete {
			t.Fatal("CleanupComplete = false")
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		step := helperStep(t, t.TempDir(), "sleep", "5000")
		step.Deadline = 5 * time.Second
		go func() {
			time.Sleep(75 * time.Millisecond)
			cancel()
		}()
		evidence, err := NewExecutor().Execute(ctx, step)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if evidence.TerminalCause != TerminalCancelled {
			t.Fatalf("TerminalCause = %q", evidence.TerminalCause)
		}
		if !evidence.CleanupComplete {
			t.Fatal("CleanupComplete = false")
		}
	})
}

func TestExecuteRejectsInvalidStepsBeforeStarting(t *testing.T) {
	t.Parallel()

	valid := helperStep(t, t.TempDir(), "exit", "0")
	tests := []struct {
		name   string
		mutate func(*ExecStep)
		field  string
	}{
		{name: "empty execution id", mutate: func(step *ExecStep) { step.ExecutionID = "" }, field: "executionId"},
		{name: "relative program", mutate: func(step *ExecStep) { step.Program = "helper" }, field: "program"},
		{name: "relative cwd", mutate: func(step *ExecStep) { step.CWD = "." }, field: "cwd"},
		{name: "missing deadline", mutate: func(step *ExecStep) { step.Deadline = 0 }, field: "deadline"},
		{name: "stdout limit", mutate: func(step *ExecStep) { step.Limits.StdoutBytes = 0 }, field: "limits.stdoutBytes"},
		{name: "stderr limit", mutate: func(step *ExecStep) { step.Limits.StderrBytes = 0 }, field: "limits.stderrBytes"},
		{name: "stdout hard maximum", mutate: func(step *ExecStep) { step.Limits.StdoutBytes = MaxRetainedStreamBytes + 1 }, field: "limits.stdoutBytes"},
		{name: "stderr hard maximum", mutate: func(step *ExecStep) { step.Limits.StderrBytes = MaxRetainedStreamBytes + 1 }, field: "limits.stderrBytes"},
		{name: "invalid env name", mutate: func(step *ExecStep) { step.Env["BAD=NAME"] = "value" }, field: "env"},
		{name: "empty redaction", mutate: func(step *ExecStep) { step.Redaction.Literals = []string{""} }, field: "redaction.literals[0]"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			step := valid
			step.Args = append([]string(nil), valid.Args...)
			step.Env = cloneEnvironment(valid.Env)
			test.mutate(&step)
			_, err := NewExecutor().Execute(context.Background(), step)
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("error = %v, want ValidationError", err)
			}
			if validationErr.Field != test.field {
				t.Fatalf("field = %q, want %q", validationErr.Field, test.field)
			}
		})
	}
}

func TestExecuteReportsSpawnFailureWithoutLeakingProgramPath(t *testing.T) {
	t.Parallel()

	step := helperStep(t, t.TempDir(), "exit", "0")
	step.Program = filepath.Join(t.TempDir(), "missing-secret-program")
	evidence, err := NewExecutor().Execute(context.Background(), step)
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) {
		t.Fatalf("error = %v, want ExecutionError", err)
	}
	if evidence.TerminalCause != TerminalSpawnFailed {
		t.Fatalf("TerminalCause = %q", evidence.TerminalCause)
	}
	if strings.Contains(err.Error(), step.Program) {
		t.Fatalf("public error leaked program path: %q", err)
	}
	if errors.Unwrap(err) != nil {
		t.Fatalf("typed execution error exposed raw OS cause: %v", errors.Unwrap(err))
	}
}

func TestExecuteDoesNotLaunchWithPreCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	step := helperStep(t, t.TempDir(), "redact", "must-not-run")
	evidence, err := NewExecutor().Execute(ctx, step)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if evidence.TerminalCause != TerminalCancelled ||
		evidence.CleanupScope != "not_started" ||
		!evidence.CleanupComplete ||
		evidence.Stdout.RawBytes != 0 {
		t.Fatalf("evidence = %#v", evidence)
	}
}

func TestClassifyTerminalCauseUsesRecordedWinnerNotLiveContext(t *testing.T) {
	t.Parallel()

	evidence := ProcessEvidence{
		Stdout: StreamEvidence{},
		Stderr: StreamEvidence{},
	}
	if got := classifyTerminalCause("", nil, evidence); got != TerminalExited {
		t.Fatalf("classifyTerminalCause() = %q, want %q", got, TerminalExited)
	}
	if got := classifyTerminalCause(TerminalDeadlineExceeded, nil, evidence); got != TerminalDeadlineExceeded {
		t.Fatalf("deadline classification = %q", got)
	}
}

func TestRequestDigestIsIndependentOfMapIterationOrder(t *testing.T) {
	t.Parallel()

	first := helperStep(t, t.TempDir(), "exit", "0")
	first.Env["A"] = "1"
	first.Env["B"] = "2"
	second := first
	second.Env = map[string]string{"B": "2", "A": "1", "GO_WANT_HOST_RUNTIME_HELPER": "1"}
	addWindowsSystemRoot(second.Env)
	if requestDigest(first) != requestDigest(second) {
		t.Fatalf("request digest changes with map insertion order")
	}
}

func helperStep(t *testing.T, cwd, mode string, args ...string) ExecStep {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	arguments := []string{"-test.run=^TestHostRuntimeHelperProcess$", "--", mode}
	arguments = append(arguments, args...)
	environment := map[string]string{"GO_WANT_HOST_RUNTIME_HELPER": "1"}
	addWindowsSystemRoot(environment)
	return ExecStep{
		ExecutionID: "test-" + strings.ReplaceAll(t.Name(), "/", "-"),
		Program:     program,
		Args:        arguments,
		CWD:         cwd,
		Env:         environment,
		Deadline:    5 * time.Second,
		Limits:      StreamLimits{StdoutBytes: 1 << 20, StderrBytes: 1 << 20},
	}
}

func addWindowsSystemRoot(environment map[string]string) {
	if runtime.GOOS == "windows" {
		environment["SYSTEMROOT"] = os.Getenv("SYSTEMROOT")
	}
}

func TestHostRuntimeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HOST_RUNTIME_HELPER") != "1" {
		return
	}
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(97)
	}
	mode := os.Args[separator+1]
	args := os.Args[separator+2:]
	switch mode {
	case "echo":
		cwd, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"args":       args,
			"cwd":        cwd,
			"declared":   os.Getenv("DECLARED_VALUE"),
			"undeclared": os.Getenv("HOME"),
		})
	case "exit":
		code, _ := strconv.Atoi(args[0])
		os.Exit(code)
	case "streams":
		count, _ := strconv.Atoi(args[0])
		_, _ = os.Stdout.WriteString(strings.Repeat("o", count))
		_, _ = os.Stderr.WriteString(strings.Repeat("e", count))
	case "redact":
		_, _ = fmt.Fprintf(os.Stdout, "stdout:%s\n", args[0])
		_, _ = fmt.Fprintf(os.Stderr, "stderr:%s\n", args[0])
	case "sleep":
		milliseconds, _ := strconv.Atoi(args[0])
		time.Sleep(time.Duration(milliseconds) * time.Millisecond)
	case "spawn-child":
		program, _ := os.Executable()
		child := exec.Command(program, "-test.run=^TestHostRuntimeHelperProcess$", "--", "sleep", "30000")
		child.Env = environmentEntries(map[string]string{"GO_WANT_HOST_RUNTIME_HELPER": "1"})
		if runtime.GOOS == "windows" {
			child.Env = append(child.Env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
		}
		if err := child.Start(); err != nil {
			os.Exit(96)
		}
		_, _ = fmt.Fprintln(os.Stdout, child.Process.Pid)
		time.Sleep(30 * time.Second)
	default:
		os.Exit(98)
	}
	os.Exit(0)
}
