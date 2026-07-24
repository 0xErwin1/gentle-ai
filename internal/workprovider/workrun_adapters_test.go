package workprovider

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestEvidenceWorkRunPortProjectsOnlyAdmittedOwnerRecords(t *testing.T) {
	repo := initWorkRunAdapterRepository(t)
	store, err := evidence.OpenStore(context.Background(), repo)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	ticket, err := evidence.NewActionTicket(evidence.ActionTicketRequest{
		IssuerRef: testRevision("1"), SubjectRef: testRevision("2"),
		CandidateRef: testRevision("3"), VerificationContextRef: testRevision("4"),
		ExpectedRevision: testRevision("5"), Slot: "adapter-proof",
		Program: executable, Args: []string{"-test.run=TestWorkRunAdapterHelperProcess"},
		CWD: repo, Environment: map[string]string{"GENTLE_AI_ADAPTER_HELPER": "1"},
		Deadline: 10 * time.Second,
		OutputLimits: hostruntime.StreamLimits{
			StdoutBytes: 1024, StderrBytes: 1024,
		},
		Capability:        "go-test",
		RedactionLiterals: []string{"adapter-secret"},
	})
	if err != nil {
		t.Fatalf("NewActionTicket() error = %v", err)
	}
	if _, err := store.PutTicket(ticket); err != nil {
		t.Fatalf("PutTicket() error = %v", err)
	}

	port := EvidenceWorkRunPort{Store: store}
	projectedTicket, err := port.ReadActionTicket(context.Background(), ticket.TicketRef)
	if err != nil {
		t.Fatalf("ReadActionTicket() error = %v", err)
	}
	slotRef, _ := ticket.SlotBindingRef()
	if projectedTicket.TicketRef != ticket.TicketRef ||
		projectedTicket.SlotBindingRef != slotRef ||
		projectedTicket.HostRequestDigest != ticket.RequestBinding.RequestDigest {
		t.Fatalf("projected ticket = %#v", projectedTicket)
	}

	executor := hostruntime.NewExecutor()
	capability, err := executor.ActivateLaunch(
		context.Background(),
		hostruntime.LaunchBinding{
			Schema: hostruntime.LaunchBindingSchemaV1, WorkRunID: "adapter-work",
			ReservationRef: testRevision("6"), ActionTicketRef: ticket.TicketRef,
			RequestDigest: ticket.RequestBinding.RequestDigest,
		},
		func(context.Context) error { return nil },
	)
	if err != nil {
		t.Fatalf("ActivateLaunch() error = %v", err)
	}
	step, err := ticket.ExecStep()
	if err != nil {
		t.Fatalf("ExecStep() error = %v", err)
	}
	process, err := executor.ExecuteAuthorized(context.Background(), step, capability)
	if err != nil {
		t.Fatalf("ExecuteAuthorized() error = %v", err)
	}
	admitted, err := store.AdmitAndStore(ticket, process)
	if err != nil {
		t.Fatalf("AdmitAndStore() error = %v", err)
	}
	projectedExecution, err := port.ReadExecutionEvidence(
		context.Background(),
		admitted.EvidenceRef,
	)
	if err != nil {
		t.Fatalf("ReadExecutionEvidence() error = %v", err)
	}
	if projectedExecution.EvidenceRef != admitted.EvidenceRef ||
		projectedExecution.TicketRef != ticket.TicketRef ||
		projectedExecution.Complete != (admitted.Outcome == evidence.ExecutionComplete) {
		t.Fatalf("projected execution = %#v", projectedExecution)
	}
}

func TestWorkRunAdapterHelperProcess(t *testing.T) {
	if os.Getenv("GENTLE_AI_ADAPTER_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("adapter-secret redacted\n")
}

func TestProjectRARResultAuthorityRejectsRequestedResultMismatch(t *testing.T) {
	authority := reviewtransaction.RARVerificationAuthority{}
	if _, err := projectRARResultAuthority(authority, testRevision("a")); err == nil {
		t.Fatal("projectRARResultAuthority() accepted a malformed owner authority")
	}
}

func TestRARWorkRunAuthorityFailsClosedWithoutOwners(t *testing.T) {
	port := RARWorkRunAuthority{}
	if _, err := port.ResolveForecast(context.Background(), testRevision("a")); err == nil {
		t.Fatal("ResolveForecast() accepted a missing coordination owner")
	}
	if _, err := port.ResolveDisposition(context.Background(), testRevision("b")); err == nil {
		t.Fatal("ResolveDisposition() accepted a missing coordination owner")
	}
	if _, err := port.ResolveResult(context.Background(), testRevision("c")); err == nil {
		t.Fatal("ResolveResult() accepted a missing RAR owner")
	}
	if _, err := port.ResolveReviewReceipt(
		context.Background(), testRevision("d"), testRevision("e"),
	); err == nil {
		t.Fatal("ResolveReviewReceipt() accepted a missing RAR owner")
	}
}

func initWorkRunAdapterRepository(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "init", "-q", repo)
	command.Env = append(os.Environ(), "LC_ALL=C")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	return repo
}

var _ workrun.EvidencePort = EvidenceWorkRunPort{}
