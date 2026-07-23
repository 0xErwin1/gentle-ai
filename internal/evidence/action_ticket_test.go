package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
)

func TestActionTicketBindsExactRequestAndReturnsDefensiveStep(t *testing.T) {
	t.Parallel()

	request := actionTicketRequest(t, "exit", "0")
	request.Environment["B"] = "2"
	request.Environment["A"] = "1"
	request.RedactionLiterals = []string{"secret", "longer-secret", "secret"}
	ticket, err := NewActionTicket(request)
	if err != nil {
		t.Fatalf("NewActionTicket() error = %v", err)
	}
	if err := ticket.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !validSHA256Ref(ticket.TicketRef) ||
		!strings.HasPrefix(ticket.ExecutionID, "evidence-") ||
		ticket.RequestBinding.RequestDigest == "" {
		t.Fatalf("ticket identities are incomplete: %#v", ticket)
	}
	if got, want := ticket.RedactionLiterals, []string{"longer-secret", "secret"}; !equalStrings(got, want) {
		t.Fatalf("RedactionLiterals = %#v, want %#v", got, want)
	}

	step, err := ticket.ExecStep()
	if err != nil {
		t.Fatalf("ExecStep() error = %v", err)
	}
	step.Args[0] = "mutated"
	step.Env["A"] = "mutated"
	step.Redaction.Literals[0] = "mutated"
	if ticket.Args[0] == "mutated" || ticket.Environment["A"] == "mutated" ||
		ticket.RedactionLiterals[0] == "mutated" {
		t.Fatal("ExecStep() aliases immutable ticket inputs")
	}

	reordered := request
	reordered.Environment = map[string]string{"A": "1", "B": "2", "GO_WANT_EVIDENCE_HELPER": "1"}
	addWindowsSystemRoot(reordered.Environment)
	reordered.RedactionLiterals = []string{"longer-secret", "secret"}
	second, err := NewActionTicket(reordered)
	if err != nil {
		t.Fatalf("NewActionTicket(reordered) error = %v", err)
	}
	if second.TicketRef != ticket.TicketRef || second.RequestBinding != ticket.RequestBinding {
		t.Fatal("ticket identity changes with map insertion order or equivalent redaction input")
	}

	payload, err := json.Marshal(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"environment"`) ||
		!strings.Contains(string(payload), `"args"`) {
		t.Fatal("durable action ticket omitted exact execution inputs")
	}
}

func TestActionTicketRejectsTamperedBindings(t *testing.T) {
	t.Parallel()

	ticket := mustActionTicket(t, "exit", "0")
	tests := []struct {
		name   string
		mutate func(*ActionTicket)
	}{
		{name: "subject", mutate: func(value *ActionTicket) { value.SubjectRef = testRef("other-subject") }},
		{name: "candidate", mutate: func(value *ActionTicket) { value.CandidateRef = testRef("other-candidate") }},
		{name: "revision", mutate: func(value *ActionTicket) { value.ExpectedRevision = testRef("other-revision") }},
		{name: "slot", mutate: func(value *ActionTicket) { value.Slot = "Other Slot" }},
		{name: "program", mutate: func(value *ActionTicket) { value.Program = filepath.Join("relative", "program") }},
		{name: "argv", mutate: func(value *ActionTicket) { value.Args = append(value.Args, "extra") }},
		{name: "environment", mutate: func(value *ActionTicket) { value.Environment["NEW"] = "value" }},
		{name: "deadline", mutate: func(value *ActionTicket) { value.DeadlineNanos++ }},
		{name: "limits", mutate: func(value *ActionTicket) { value.OutputLimits.StdoutBytes++ }},
		{name: "capability", mutate: func(value *ActionTicket) { value.Capability = "Bad Capability" }},
		{name: "request binding", mutate: func(value *ActionTicket) { value.RequestBinding.RequestDigest = testRef("other-request") }},
		{name: "ticket ref", mutate: func(value *ActionTicket) { value.TicketRef = testRef("other-ticket") }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mutated := cloneTicket(ticket)
			test.mutate(&mutated)
			if err := mutated.Validate(); err == nil {
				t.Fatal("Validate() accepted a tampered ticket")
			}
		})
	}
}

func TestActionTicketPreservesExactPathsAndRejectsNormalizedAuthorityInputs(t *testing.T) {
	t.Parallel()

	request := actionTicketRequest(t, "exit", "0")
	request.Program += " "
	request.CWD += " "
	ticket, err := NewActionTicket(request)
	if err != nil {
		t.Fatalf("NewActionTicket() exact paths error = %v", err)
	}
	if ticket.Program != request.Program || ticket.CWD != request.CWD {
		t.Fatalf("exact paths were normalized: program %q cwd %q", ticket.Program, ticket.CWD)
	}

	for _, mutate := range []func(*ActionTicketRequest){
		func(value *ActionTicketRequest) { value.SubjectRef = " " + value.SubjectRef },
		func(value *ActionTicketRequest) { value.Slot = " " + value.Slot },
		func(value *ActionTicketRequest) { value.Capability += " " },
	} {
		invalid := actionTicketRequest(t, "exit", "0")
		mutate(&invalid)
		if _, err := NewActionTicket(invalid); err == nil {
			t.Fatal("NewActionTicket() silently normalized an exact authority input")
		}
	}
}

func TestAdmitExecutionRejectsCrossSubjectReplay(t *testing.T) {
	t.Parallel()

	request := actionTicketRequest(t, "exit", "0")
	first, err := NewActionTicket(request)
	if err != nil {
		t.Fatal(err)
	}
	request.SubjectRef = testRef("different-subject")
	second, err := NewActionTicket(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ExecutionID == second.ExecutionID ||
		first.RequestBinding.RequestDigest == second.RequestBinding.RequestDigest {
		t.Fatal("subject change did not change the exact HCR request binding")
	}
	process := executeTicket(t, context.Background(), first, false)
	if _, err := AdmitExecution(first, process); err != nil {
		t.Fatalf("AdmitExecution(first) error = %v", err)
	}
	_, err = AdmitExecution(second, process)
	var admissionErr *AdmissionError
	if !errors.As(err, &admissionErr) || admissionErr.Code != AdmissionBindingMismatch {
		t.Fatalf("cross-subject replay error = %v, want binding mismatch", err)
	}
}

func TestExecutionAdmissionDerivesConservativeTerminalOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		ticket    func(*testing.T) ActionTicket
		execute   func(*testing.T, ActionTicket) hostruntime.ProcessEvidence
		want      ExecutionOutcome
		wantCause hostruntime.TerminalCause
	}{
		{
			name:   "zero exit is complete",
			ticket: func(t *testing.T) ActionTicket { return mustActionTicket(t, "exit", "0") },
			execute: func(t *testing.T, ticket ActionTicket) hostruntime.ProcessEvidence {
				return executeTicket(t, context.Background(), ticket, false)
			},
			want: ExecutionComplete, wantCause: hostruntime.TerminalExited,
		},
		{
			name:   "nonzero exit is failed",
			ticket: func(t *testing.T) ActionTicket { return mustActionTicket(t, "exit", "23") },
			execute: func(t *testing.T, ticket ActionTicket) hostruntime.ProcessEvidence {
				return executeTicket(t, context.Background(), ticket, false)
			},
			want: ExecutionFailed, wantCause: hostruntime.TerminalExited,
		},
		{
			name: "truncated output is incomplete",
			ticket: func(t *testing.T) ActionTicket {
				request := actionTicketRequest(t, "stream", "8192")
				request.OutputLimits = hostruntime.StreamLimits{StdoutBytes: 31, StderrBytes: 29}
				ticket, err := NewActionTicket(request)
				if err != nil {
					t.Fatal(err)
				}
				return ticket
			},
			execute: func(t *testing.T, ticket ActionTicket) hostruntime.ProcessEvidence {
				return executeTicket(t, context.Background(), ticket, false)
			},
			want: ExecutionIncomplete, wantCause: hostruntime.TerminalOutputTruncated,
		},
		{
			name: "deadline is incomplete",
			ticket: func(t *testing.T) ActionTicket {
				request := actionTicketRequest(t, "sleep", "500")
				request.Deadline = 25 * time.Millisecond
				ticket, err := NewActionTicket(request)
				if err != nil {
					t.Fatal(err)
				}
				return ticket
			},
			execute: func(t *testing.T, ticket ActionTicket) hostruntime.ProcessEvidence {
				return executeTicket(t, context.Background(), ticket, false)
			},
			want: ExecutionIncomplete, wantCause: hostruntime.TerminalDeadlineExceeded,
		},
		{
			name:   "cancellation is incomplete",
			ticket: func(t *testing.T) ActionTicket { return mustActionTicket(t, "exit", "0") },
			execute: func(t *testing.T, ticket ActionTicket) hostruntime.ProcessEvidence {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return executeTicket(t, ctx, ticket, false)
			},
			want: ExecutionIncomplete, wantCause: hostruntime.TerminalCancelled,
		},
		{
			name: "spawn failure is incomplete",
			ticket: func(t *testing.T) ActionTicket {
				request := actionTicketRequest(t, "exit", "0")
				request.Program = filepath.Join(t.TempDir(), "missing-program")
				ticket, err := NewActionTicket(request)
				if err != nil {
					t.Fatal(err)
				}
				return ticket
			},
			execute: func(t *testing.T, ticket ActionTicket) hostruntime.ProcessEvidence {
				return executeTicket(t, context.Background(), ticket, true)
			},
			want: ExecutionIncomplete, wantCause: hostruntime.TerminalSpawnFailed,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ticket := test.ticket(t)
			process := test.execute(t, ticket)
			evidence, err := AdmitExecution(ticket, process)
			if err != nil {
				t.Fatalf("AdmitExecution() error = %v", err)
			}
			if evidence.Outcome != test.want || evidence.Process.TerminalCause != test.wantCause {
				t.Fatalf("outcome/cause = %q/%q, want %q/%q",
					evidence.Outcome, evidence.Process.TerminalCause, test.want, test.wantCause)
			}
			if err := evidence.ValidateFor(ticket); err != nil {
				t.Fatalf("ValidateFor() error = %v", err)
			}
		})
	}
}

func TestExecutionEvidenceRejectsOutcomeUpgradeAndProcessTampering(t *testing.T) {
	t.Parallel()

	ticket := mustActionTicket(t, "exit", "23")
	process := executeTicket(t, context.Background(), ticket, false)
	evidence, err := AdmitExecution(ticket, process)
	if err != nil {
		t.Fatal(err)
	}
	upgraded := evidence
	upgraded.Outcome = ExecutionComplete
	upgraded.EvidenceRef, _ = executionEvidenceDigest(upgraded)
	if err := upgraded.Validate(); err == nil {
		t.Fatal("Validate() accepted nonzero execution as complete")
	}

	tampered := process
	tampered.RequestDigest = testRef("forged-request")
	_, err = AdmitExecution(ticket, tampered)
	var admissionErr *AdmissionError
	if !errors.As(err, &admissionErr) || admissionErr.Code != AdmissionInvalidEvidence {
		t.Fatalf("tampered request error = %v, want invalid provenance", err)
	}

	tests := []struct {
		name   string
		mutate func(*hostruntime.ProcessEvidence)
	}{
		{name: "exit code", mutate: func(value *hostruntime.ProcessEvidence) { value.ExitCode = 0 }},
		{name: "timestamp", mutate: func(value *hostruntime.ProcessEvidence) {
			value.FinishedAt = value.FinishedAt.Add(time.Nanosecond)
		}},
		{name: "raw digest", mutate: func(value *hostruntime.ProcessEvidence) {
			value.Stdout.RawDigest = testRef("forged-raw")
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			changed := process
			test.mutate(&changed)
			_, err := AdmitExecution(ticket, changed)
			if !errors.As(err, &admissionErr) || admissionErr.Code != AdmissionInvalidEvidence {
				t.Fatalf("mutated ProcessEvidence error = %v, want invalid provenance", err)
			}
		})
	}
}

func actionTicketRequest(t *testing.T, mode string, args ...string) ActionTicketRequest {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{"-test.run=^TestEvidenceHelperProcess$", "--", mode}
	argv = append(argv, args...)
	environment := map[string]string{"GO_WANT_EVIDENCE_HELPER": "1"}
	addWindowsSystemRoot(environment)
	return ActionTicketRequest{
		IssuerRef: testRef("issuer"), SubjectRef: testRef("subject"),
		CandidateRef:           testRef("candidate"),
		VerificationContextRef: testRef("verification-context"),
		ExpectedRevision:       testRef("revision"), Slot: "verify.unit",
		Program: program, Args: argv, CWD: t.TempDir(), Environment: environment,
		Deadline: 5 * time.Second,
		OutputLimits: hostruntime.StreamLimits{
			StdoutBytes: 1 << 20, StderrBytes: 1 << 20,
		},
		Capability: "go.test",
	}
}

func mustActionTicket(t *testing.T, mode string, args ...string) ActionTicket {
	t.Helper()
	ticket, err := NewActionTicket(actionTicketRequest(t, mode, args...))
	if err != nil {
		t.Fatalf("NewActionTicket() error = %v", err)
	}
	return ticket
}

func cloneTicket(ticket ActionTicket) ActionTicket {
	cloned := ticket
	cloned.Args = append([]string{}, ticket.Args...)
	cloned.Environment = cloneEnvironment(ticket.Environment)
	cloned.RedactionLiterals = append([]string{}, ticket.RedactionLiterals...)
	return cloned
}

func requestFromTicket(ticket ActionTicket) ActionTicketRequest {
	return ActionTicketRequest{
		IssuerRef: ticket.IssuerRef, SubjectRef: ticket.SubjectRef,
		CandidateRef:           ticket.CandidateRef,
		VerificationContextRef: ticket.VerificationContextRef,
		ExpectedRevision:       ticket.ExpectedRevision, Slot: ticket.Slot,
		Program: ticket.Program, Args: append([]string{}, ticket.Args...),
		CWD: ticket.CWD, Environment: cloneEnvironment(ticket.Environment),
		Deadline: time.Duration(ticket.DeadlineNanos), OutputLimits: ticket.OutputLimits,
		Capability:        ticket.Capability,
		RedactionLiterals: append([]string{}, ticket.RedactionLiterals...),
	}
}

func executeTicket(t *testing.T, ctx context.Context, ticket ActionTicket, wantError bool) hostruntime.ProcessEvidence {
	t.Helper()
	step, err := ticket.ExecStep()
	if err != nil {
		t.Fatal(err)
	}
	process, err := hostruntime.NewExecutor().Execute(ctx, step)
	if wantError && err == nil {
		t.Fatal("Execute() error = nil, want typed terminal error")
	}
	if !wantError && err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return process
}

func testRef(value string) string {
	sum := fmt.Sprintf("%x", []byte(value))
	if len(sum) < 64 {
		sum += strings.Repeat("0", 64-len(sum))
	}
	return "sha256:" + sum[:64]
}

func addWindowsSystemRoot(environment map[string]string) {
	if runtime.GOOS == "windows" {
		environment["SYSTEMROOT"] = os.Getenv("SYSTEMROOT")
	}
}

func TestEvidenceHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_EVIDENCE_HELPER") != "1" {
		return
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
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
	case "exit":
		code, _ := strconv.Atoi(args[0])
		os.Exit(code)
	case "stream":
		count, _ := strconv.Atoi(args[0])
		_, _ = os.Stdout.WriteString(strings.Repeat("o", count))
		_, _ = os.Stderr.WriteString(strings.Repeat("e", count))
	case "sleep":
		milliseconds, _ := strconv.Atoi(args[0])
		time.Sleep(time.Duration(milliseconds) * time.Millisecond)
	default:
		os.Exit(98)
	}
	os.Exit(0)
}
