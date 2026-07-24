package evidence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
)

func TestDeriveSemanticRequirementRefBindsOnlyIndependentExecutionIdentities(
	t *testing.T,
) {
	t.Parallel()

	request := actionTicketRequest(t, "exit", "0")
	ticket, err := NewActionTicket(request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := DeriveSemanticRequirementRef(
		ticket.Capability,
		ticket.RequestBinding,
	)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := DeriveSemanticRequirementRef(
		ticket.Capability,
		ticket.RequestBinding,
	)
	if err != nil || replayed != want {
		t.Fatalf(
			"deterministic requirement = %q, %v; want %q",
			replayed,
			err,
			want,
		)
	}

	excluded := []struct {
		name   string
		mutate func(*hostruntime.RequestBinding)
	}{
		{
			name: "execution ID",
			mutate: func(binding *hostruntime.RequestBinding) {
				binding.ExecutionID = "different-execution"
			},
		},
		{
			name: "request digest",
			mutate: func(binding *hostruntime.RequestBinding) {
				binding.RequestDigest = testRef("different-request")
			},
		},
		{
			name: "existing semantic requirement",
			mutate: func(binding *hostruntime.RequestBinding) {
				binding.SemanticRequirementRef = testRef(
					"different-semantic-requirement",
				)
			},
		},
	}
	for _, test := range excluded {
		test := test
		t.Run("excludes "+test.name, func(t *testing.T) {
			t.Parallel()
			binding := ticket.RequestBinding
			test.mutate(&binding)
			got, deriveErr := DeriveSemanticRequirementRef(
				ticket.Capability,
				binding,
			)
			if deriveErr != nil || got != want {
				t.Fatalf(
					"requirement after %s = %q, %v; want %q",
					test.name,
					got,
					deriveErr,
					want,
				)
			}
		})
	}

	included := []struct {
		name       string
		capability string
		mutate     func(*hostruntime.RequestBinding)
	}{
		{
			name:       "capability",
			capability: "go.integration",
			mutate:     func(*hostruntime.RequestBinding) {},
		},
		{
			name:       "argv",
			capability: ticket.Capability,
			mutate: func(binding *hostruntime.RequestBinding) {
				binding.ArgvDigest = testRef("different-argv")
			},
		},
		{
			name:       "cwd",
			capability: ticket.Capability,
			mutate: func(binding *hostruntime.RequestBinding) {
				binding.CWDDigest = testRef("different-cwd")
			},
		},
		{
			name:       "environment",
			capability: ticket.Capability,
			mutate: func(binding *hostruntime.RequestBinding) {
				binding.EnvironmentDigest = testRef("different-environment")
			},
		},
	}
	for _, test := range included {
		test := test
		t.Run("binds "+test.name, func(t *testing.T) {
			t.Parallel()
			binding := ticket.RequestBinding
			test.mutate(&binding)
			got, deriveErr := DeriveSemanticRequirementRef(
				test.capability,
				binding,
			)
			if deriveErr != nil {
				t.Fatal(deriveErr)
			}
			if got == want {
				t.Fatalf("%s change did not change requirement", test.name)
			}
		})
	}
}

func TestDeriveSemanticRequirementRefBindsProgramAndToolchainContent(
	t *testing.T,
) {
	t.Parallel()

	root := t.TempDir()
	program := filepath.Join(root, "owner-tool")
	if err := os.WriteFile(program, []byte("toolchain-one"), 0o700); err != nil {
		t.Fatal(err)
	}
	request := actionTicketRequest(t, "exit", "0")
	request.Program = program
	first, err := NewActionTicket(request)
	if err != nil {
		t.Fatal(err)
	}
	firstRef, err := DeriveSemanticRequirementRef(
		first.Capability,
		first.RequestBinding,
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(program, []byte("toolchain-two"), 0o700); err != nil {
		t.Fatal(err)
	}
	second, err := NewActionTicket(request)
	if err != nil {
		t.Fatal(err)
	}
	secondRef, err := DeriveSemanticRequirementRef(
		second.Capability,
		second.RequestBinding,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.RequestBinding.ProgramDigest != second.RequestBinding.ProgramDigest {
		t.Fatal("same program path changed its program digest")
	}
	if first.RequestBinding.ToolchainIdentityRef ==
		second.RequestBinding.ToolchainIdentityRef {
		t.Fatal("changed executable bytes preserved toolchain identity")
	}
	if firstRef == secondRef {
		t.Fatal("changed executable bytes preserved semantic requirement")
	}

	otherProgram := filepath.Join(root, "other-owner-tool")
	if err := os.WriteFile(
		otherProgram,
		[]byte("toolchain-two"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	request.Program = otherProgram
	third, err := NewActionTicket(request)
	if err != nil {
		t.Fatal(err)
	}
	thirdRef, err := DeriveSemanticRequirementRef(
		third.Capability,
		third.RequestBinding,
	)
	if err != nil {
		t.Fatal(err)
	}
	if thirdRef == secondRef {
		t.Fatal("changed program identity preserved semantic requirement")
	}
}

func TestDeriveSemanticRequirementRefRejectsIncoherentBindings(t *testing.T) {
	t.Parallel()

	ticket, err := NewActionTicket(actionTicketRequest(t, "exit", "0"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		capability string
		mutate     func(*hostruntime.RequestBinding)
	}{
		{
			name:       "invalid capability",
			capability: "Go Test",
			mutate:     func(*hostruntime.RequestBinding) {},
		},
		{
			name:       "legacy binding",
			capability: ticket.Capability,
			mutate: func(binding *hostruntime.RequestBinding) {
				binding.Schema = hostruntime.RequestBindingSchemaV1
			},
		},
		{
			name:       "missing argv digest",
			capability: ticket.Capability,
			mutate: func(binding *hostruntime.RequestBinding) {
				binding.ArgvDigest = ""
			},
		},
		{
			name:       "foreign program identity",
			capability: ticket.Capability,
			mutate: func(binding *hostruntime.RequestBinding) {
				binding.ProgramDigest = testRef("foreign-program")
			},
		},
		{
			name:       "tampered toolchain identity",
			capability: ticket.Capability,
			mutate: func(binding *hostruntime.RequestBinding) {
				binding.ToolchainIdentityRef = testRef("forged-toolchain")
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			binding := ticket.RequestBinding
			test.mutate(&binding)
			if _, deriveErr := DeriveSemanticRequirementRef(
				test.capability,
				binding,
			); deriveErr == nil {
				t.Fatal("DeriveSemanticRequirementRef() accepted unsafe input")
			}
		})
	}

	missing := actionTicketRequest(t, "exit", "0")
	missing.Program = filepath.Join(t.TempDir(), "missing-tool")
	unavailable, err := NewActionTicket(missing)
	if err != nil {
		t.Fatal(err)
	}
	if unavailable.RequestBinding.ToolchainState !=
		hostruntime.ToolchainIdentityUnavailable {
		t.Fatalf(
			"missing toolchain state = %q",
			unavailable.RequestBinding.ToolchainState,
		)
	}
	if _, err := DeriveSemanticRequirementRef(
		unavailable.Capability,
		unavailable.RequestBinding,
	); err == nil {
		t.Fatal("DeriveSemanticRequirementRef() accepted unavailable toolchain")
	}
}

func TestStoreIssuesAndReplaysOwnerSemanticActionTicket(t *testing.T) {
	t.Parallel()

	repo := initEvidenceRepository(t)
	store, err := OpenStore(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	base := actionTicketRequest(t, "output", "owner verified")
	request := semanticTicketRequest(base)
	ticket, err := store.IssueSemanticActionTicket(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatalf("IssueSemanticActionTicket() error = %v", err)
	}
	if ticket.Schema != ActionTicketSchemaV2 ||
		ticket.IssuerRef != store.RepositoryRef() ||
		ticket.SemanticRequirementRef == "" ||
		ticket.SemanticAuthorityIdentity.IsZero() {
		t.Fatalf("owner semantic ticket is incomplete: %#v", ticket)
	}
	wantRequirement, err := DeriveSemanticRequirementRef(
		ticket.Capability,
		ticket.RequestBinding,
	)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.SemanticRequirementRef != wantRequirement {
		t.Fatalf(
			"SemanticRequirementRef = %q, want %q",
			ticket.SemanticRequirementRef,
			wantRequirement,
		)
	}
	stored, err := store.ReadTicket(ticket.TicketRef)
	if err != nil {
		t.Fatalf("ReadTicket() error = %v", err)
	}
	if stored.TicketRef != ticket.TicketRef {
		t.Fatalf("stored ticket = %q, want %q", stored.TicketRef, ticket.TicketRef)
	}

	replayed, err := store.IssueSemanticActionTicket(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatalf("replayed IssueSemanticActionTicket() error = %v", err)
	}
	if replayed.TicketRef != ticket.TicketRef ||
		replayed.SemanticAuthorityIdentity !=
			ticket.SemanticAuthorityIdentity {
		t.Fatal("exact semantic ticket replay changed immutable owner identity")
	}

	authority, err := store.SemanticAuthority(
		context.Background(),
		ticket.SemanticRequirementRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	process := executeTicket(t, context.Background(), ticket, false)
	verdict, err := authority.AttestPassed(ticket, process)
	if err != nil {
		t.Fatalf("AttestPassed() error = %v", err)
	}
	admitted, err := store.AdmitAndStoreWithSemanticVerdict(
		ticket,
		process,
		verdict,
	)
	if err != nil {
		t.Fatalf("AdmitAndStoreWithSemanticVerdict() error = %v", err)
	}
	if admitted.Outcome != ExecutionComplete {
		t.Fatalf("owner semantic outcome = %q, want complete", admitted.Outcome)
	}
}

func TestStoreSemanticTicketRejectsUnavailableOrCancelledIssueWithoutTicket(
	t *testing.T,
) {
	t.Parallel()

	store, err := OpenStore(
		context.Background(),
		initEvidenceRepository(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	before, err := directoryFingerprint(store.Root())
	if err != nil {
		t.Fatal(err)
	}

	request := semanticTicketRequest(actionTicketRequest(t, "exit", "0"))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.IssueSemanticActionTicket(
		cancelled,
		request,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled issue error = %v", err)
	}
	request.Program = filepath.Join(t.TempDir(), "missing-tool")
	if _, err := store.IssueSemanticActionTicket(
		context.Background(),
		request,
	); err == nil {
		t.Fatal("IssueSemanticActionTicket() accepted unavailable toolchain")
	}

	after, err := directoryFingerprint(store.Root())
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("rejected semantic ticket issue mutated EPD")
	}
}

func semanticTicketRequest(
	request ActionTicketRequest,
) SemanticActionTicketRequest {
	return SemanticActionTicketRequest{
		SubjectRef:             request.SubjectRef,
		CandidateRef:           request.CandidateRef,
		VerificationContextRef: request.VerificationContextRef,
		ExpectedRevision:       request.ExpectedRevision,
		Slot:                   request.Slot,
		Program:                request.Program,
		Args:                   append([]string(nil), request.Args...),
		CWD:                    request.CWD,
		Environment:            cloneEnvironment(request.Environment),
		Deadline:               request.Deadline,
		OutputLimits:           request.OutputLimits,
		Capability:             request.Capability,
		RedactionLiterals:      append([]string(nil), request.RedactionLiterals...),
	}
}
