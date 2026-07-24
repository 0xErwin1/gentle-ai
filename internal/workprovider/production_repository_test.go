package workprovider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/evidence"
	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type productionTestPAD struct {
	intentRef string
}

func (pad productionTestPAD) ResolveDeliveryIntent(
	_ context.Context,
	ref string,
) (workrun.DeliveryIntentAuthority, error) {
	if ref != pad.intentRef {
		return workrun.DeliveryIntentAuthority{}, os.ErrNotExist
	}
	return workrun.DeliveryIntentAuthority{IntentRef: ref}, nil
}

func (productionTestPAD) ResolveLiveDeliveryAuthorization(
	context.Context,
	string,
) (workrun.DeliveryAuthorizationAuthority, error) {
	return workrun.DeliveryAuthorizationAuthority{},
		workrun.ErrDeliveryAuthorizationInactive
}

type productionRepositoryOpener struct {
	repository Repository
}

func (opener productionRepositoryOpener) OpenRepository(
	context.Context,
	string,
	string,
) (Repository, error) {
	return opener.repository, nil
}

func TestProductionRepositoryExecutesOnceAndReplaysExactReceipt(t *testing.T) {
	if os.Getenv("GENTLE_AI_PRODUCTION_HELPER") == "1" {
		marker := os.Getenv("GENTLE_AI_PRODUCTION_MARKER")
		file, err := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(91)
		}
		_, err = file.WriteString("launch\n")
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			os.Exit(92)
		}
		return
	}

	ctx := context.Background()
	repo := initProductionRepositoryGit(t)
	applicability, registry, plan, snapshot := productionVerificationPlan(t, repo)
	rar, err := reviewtransaction.OpenRARAuthorityRepository(ctx, repo)
	if err != nil {
		t.Fatalf("OpenRARAuthorityRepository() error = %v", err)
	}
	planAuthority, err := rar.PublishPlan(ctx, reviewtransaction.RARPlanPublication{
		Snapshot:      snapshot,
		Applicability: applicability,
		Registry:      registry,
		Plan:          plan,
	})
	if err != nil {
		t.Fatalf("PublishPlan() error = %v", err)
	}

	const workRunID = "production-once"
	coordination, err := OpenCoordinationAuthorityStore(
		ctx,
		repo,
		workRunID,
		rar,
	)
	if err != nil {
		t.Fatalf("OpenCoordinationAuthorityStore() error = %v", err)
	}
	executor := hostruntime.NewExecutor()
	intentRef := testRevision("1")
	store, err := workrun.OpenWorkRunStore(ctx, repo, workRunID)
	if err != nil {
		t.Fatalf("OpenWorkRunStore() error = %v", err)
	}
	store = store.WithAuthorityPorts(workrun.AuthorityPorts{
		PAD: productionTestPAD{intentRef: intentRef},
		Verification: RARWorkRunAuthority{
			Coordination: coordination,
			RAR:          rar,
		},
		Launch: executor,
	})
	route, err := workrun.DecideImplementationRoute(workrun.ImplementationRouteInput{
		WriteIntent:    workrun.WriteIntentAtomicMechanical,
		WriteFileCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Start(ctx, workrun.StartRequest{
		RequestID:         "production-start",
		RouteDecision:     route,
		DeliveryIntentRef: intentRef,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	requirements := make([]string, 0, len(plan.Obligations))
	for _, obligation := range plan.Obligations {
		requirements = append(requirements, obligation.RequirementRef)
	}
	handoff, err := workrun.NewImplementationHandoff(
		workrun.ImplementationRouteDirectInline,
		testRevision("2"),
		plan.Subject,
		requirements,
		[]string{},
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err = store.BindImplementationHandoff(
		ctx,
		workrun.BindImplementationHandoffRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "production-handoff",
			Handoff:          handoff,
		},
	)
	if err != nil {
		t.Fatalf("BindImplementationHandoff() error = %v", err)
	}
	forecastRecord, err := coordination.PublishForecast(
		ctx,
		ForecastAuthorityPublication{
			WorkRunID:       workRunID,
			Handoff:         handoff,
			PlanRevisionRef: planAuthority.AuthorityRef,
			Availability:    workrun.ForecastAvailable,
			DiagnosticRefs:  []string{},
		},
	)
	if err != nil {
		t.Fatalf("PublishForecast() error = %v", err)
	}
	forecastInput := workrun.VerificationForecastInput{
		WorkRunID:       workRunID,
		Handoff:         handoff,
		Applicability:   applicability,
		Registry:        registry,
		Plan:            plan,
		PlanRevisionRef: planAuthority.AuthorityRef,
		Availability:    workrun.ForecastAvailable,
		AvailabilityRef: forecastRecord.AuthorityRef,
		DiagnosticRefs:  []string{},
	}
	state, err = store.RecordVerificationForecast(
		ctx,
		workrun.RecordVerificationForecastRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "production-forecast",
			Input:            forecastInput,
		},
	)
	if err != nil {
		t.Fatalf("RecordVerificationForecast() error = %v", err)
	}
	assumptionsRef := testRevision("3")
	actorRef := testRevision("4")
	dispositionRecord, err := coordination.PublishDisposition(
		ctx,
		DispositionAuthorityPublication{
			WorkRunID:      workRunID,
			Handoff:        handoff,
			Forecast:       *state.Forecast,
			Kind:           workrun.DispositionRun,
			AssumptionsRef: assumptionsRef,
			ActorRef:       actorRef,
		},
	)
	if err != nil {
		t.Fatalf("PublishDisposition() error = %v", err)
	}
	state, err = store.RecordVerificationDisposition(
		ctx,
		workrun.RecordVerificationDispositionRequest{
			ExpectedRevision: state.Revision,
			RequestID:        "production-disposition",
			Kind:             workrun.DispositionRun,
			AssumptionsRef:   assumptionsRef,
			ActorRef:         actorRef,
			DecisionRef:      dispositionRecord.AuthorityRef,
		},
	)
	if err != nil {
		t.Fatalf("RecordVerificationDisposition() error = %v", err)
	}

	evidenceStore, err := evidence.OpenStore(ctx, repo)
	if err != nil {
		t.Fatalf("evidence.OpenStore() error = %v", err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "launches.log")
	obligation := plan.Obligations[0]
	ticket, err := evidence.NewActionTicket(evidence.ActionTicketRequest{
		IssuerRef:              actorRef,
		SubjectRef:             state.Forecast.SubjectRef,
		CandidateRef:           handoff.CandidateRef,
		VerificationContextRef: state.Forecast.Digest,
		ExpectedRevision:       planAuthority.AuthorityRef,
		Slot:                   obligation.ID,
		Program:                executable,
		Args:                   []string{"-test.run=TestProductionRepositoryExecutesOnceAndReplaysExactReceipt"},
		CWD:                    repo,
		Environment: map[string]string{
			"GENTLE_AI_PRODUCTION_HELPER": "1",
			"GENTLE_AI_PRODUCTION_MARKER": marker,
		},
		Deadline: 10 * time.Second,
		OutputLimits: hostruntime.StreamLimits{
			StdoutBytes: 1024,
			StderrBytes: 1024,
		},
		Capability:        obligation.CapabilityRef,
		RedactionLiterals: []string{},
	})
	if err != nil {
		t.Fatalf("NewActionTicket() error = %v", err)
	}
	if _, err := evidenceStore.PutTicket(ticket); err != nil {
		t.Fatalf("PutTicket() error = %v", err)
	}

	transitions, err := OpenTransitionAuthorityStore(ctx, repo, workRunID)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := NewVerificationTransitionAuthorization(
		VerificationTransitionAuthorizationRequest{
			WorkRunID:                  workRunID,
			ExpectedRevision:           state.Revision,
			CandidateRef:               handoff.CandidateRef,
			ActionTicketRef:            ticket.TicketRef,
			ApplicableAuthorizationRef: planAuthority.AuthorityRef,
			Class:                      AuthorizationGeneral,
			IssuedAt:                   10,
			ExpiresAt:                  100,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := transitions.PublishAuthorization(ctx, authorization); err != nil {
		t.Fatalf("PublishAuthorization() error = %v", err)
	}
	repository := &ProductionRepository{
		WorkRun:     store,
		Transitions: transitions,
		Plans:       rar,
		Executor:    executor,
		OpenEvidence: func(context.Context) (evidence.Store, error) {
			return evidenceStore, nil
		},
		Now: func() time.Time { return time.Unix(20, 0).UTC() },
	}
	controller := NewController(
		productionRepositoryOpener{repository: repository},
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	status, err := controller.Status(ctx, StatusRequest{
		Repo:      repo,
		WorkRunID: workRunID,
		Contract:  workrun.WorkStatusContractV1,
	})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Status == nil ||
		status.Status.AuthorizedTransition == nil ||
		status.Status.AuthorizedTransition.AuthorizationRef != authorization.AuthorizationRef {
		t.Fatalf("Status() = %#v", status)
	}
	applyRequest := ApplyRequest{
		Repo:             repo,
		WorkRunID:        workRunID,
		Contract:         workrun.WorkTransitionContractV1,
		AuthorizationRef: authorization.AuthorizationRef,
		ExpectedRevision: authorization.ExpectedRevision,
	}
	first, err := controller.Apply(ctx, applyRequest)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	second, err := controller.Apply(ctx, applyRequest)
	if err != nil {
		t.Fatalf("Apply(replay) error = %v", err)
	}
	if first.Transition == nil || second.Transition == nil ||
		*first.Transition != *second.Transition {
		t.Fatalf("transition replay differs: %#v / %#v", first, second)
	}
	payload, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read launch marker: %v", err)
	}
	if strings.Count(string(payload), "launch\n") != 1 {
		t.Fatalf("process launched %q times, marker = %q", payload, payload)
	}
	if _, err := evidenceStore.ReadExecutionForTicket(ticket.TicketRef); err != nil {
		t.Fatalf("admitted execution missing: %v", err)
	}
	after, err := controller.Status(ctx, StatusRequest{
		Repo:      repo,
		WorkRunID: workRunID,
		Contract:  workrun.WorkStatusContractV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.Status == nil || after.Status.AuthorizedTransition != nil {
		t.Fatalf("completed authorization was re-advertised: %#v", after)
	}
}

func TestProductionRepositoryRejectsClaimedLaunchWithoutEPD(t *testing.T) {
	repository := &ProductionRepository{}
	if _, err := repository.ApplyAuthorized(
		context.Background(),
		testRevision("a"),
		testRevision("b"),
	); !errors.Is(err, ErrRepositoryUnavailable) {
		t.Fatalf("uninitialized ApplyAuthorized() error = %v", err)
	}
}

func initProductionRepositoryGit(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runProductionGit(t, repo, "init", "-q")
	runProductionGit(t, repo, "config", "user.email", "organic@example.com")
	runProductionGit(t, repo, "config", "user.name", "Organic Recovery")
	if err := os.WriteFile(
		filepath.Join(repo, "README.md"),
		[]byte("# base\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	runProductionGit(t, repo, "add", "--", "README.md")
	runProductionGit(t, repo, "commit", "-qm", "base")
	return repo
}

func productionVerificationPlan(
	t *testing.T,
	repo string,
) (
	reviewtransaction.VerificationApplicability,
	reviewtransaction.VerificationPlanRegistry,
	reviewtransaction.VerificationPlan,
	reviewtransaction.Snapshot,
) {
	t.Helper()
	const logicalPath = "main.go"
	if err := os.WriteFile(
		filepath.Join(repo, logicalPath),
		[]byte("package main\n\nfunc main() {}\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	registry, err := reviewtransaction.NewVerificationPlanRegistry(
		testRevision("5"),
		[]string{},
		[]reviewtransaction.VerificationObligation{{
			ID:             "unit.test",
			RequirementRef: testRevision("6"),
			ArgvRef:        testRevision("7"),
			CapabilityRef:  "go.test",
			Cost:           reviewtransaction.VerificationCostQuick,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	builder := reviewtransaction.SnapshotBuilder{Repo: repo}
	snapshot, err := builder.Build(context.Background(), reviewtransaction.Target{
		Kind:              reviewtransaction.TargetCurrentChanges,
		IntendedUntracked: []string{logicalPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	applicability, err := builder.ClassifyVerificationApplicability(
		context.Background(),
		snapshot,
		registry,
		[]string{},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := reviewtransaction.BuildVerificationPlan(applicability, registry)
	if err != nil {
		t.Fatal(err)
	}
	return applicability, registry, plan, snapshot
}

func runProductionGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	command.Env = append(os.Environ(), "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
