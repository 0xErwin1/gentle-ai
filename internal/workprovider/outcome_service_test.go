package workprovider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/sddstatus"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

type stubOwnerOutcomeIntakeAuthority struct {
	intake   OwnerOutcomeIntake
	err      error
	calls    int
	contexts []OwnerOutcomeContext
	requests []OutcomeStartRequest
}

func (authority *stubOwnerOutcomeIntakeAuthority) ResolveOutcomeIntake(
	_ context.Context,
	ownerContext OwnerOutcomeContext,
	request OutcomeStartRequest,
) (OwnerOutcomeIntake, error) {
	authority.calls++
	authority.contexts = append(authority.contexts, ownerContext)
	authority.requests = append(authority.requests, request)
	return authority.intake, authority.err
}

type sequenceOwnerActivationResolver struct {
	modes []ActivationMode
	calls int
}

func (resolver *sequenceOwnerActivationResolver) ResolveActivation(
	_ context.Context,
	_ string,
) (ActivationMode, error) {
	if len(resolver.modes) == 0 {
		return ActivationDisabled, errors.New("activation sequence is empty")
	}
	index := resolver.calls
	resolver.calls++
	if index >= len(resolver.modes) {
		index = len(resolver.modes) - 1
	}
	return resolver.modes[index], nil
}

func TestDefaultOutcomeServiceUnsetFailsBeforeIntakeOrPublication(
	t *testing.T,
) {
	restoreDefaultActivationEnvironment(t)
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	providerRoot := defaultProviderRoot(t, repo)
	intake := &stubOwnerOutcomeIntakeAuthority{
		intake: ownerOutcomeTestIntake(
			"outcome-unset",
			deliveryadmission.RouteDirectMain,
		),
	}
	service, err := NewDefaultOutcomeService(
		ctx,
		repo,
		model.AgentCodex,
		intake,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(providerRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"outcome service construction published provider storage: %v",
			err,
		)
	}

	_, err = service.StartOutcome(ctx, OutcomeStartRequest{
		Outcome: "Apply the requested bounded change.",
	})
	if !errors.Is(err, ErrCapabilityReadOnly) {
		t.Fatalf("unset outcome start error = %v", err)
	}
	if intake.calls != 0 {
		t.Fatalf("read-only outcome start called owner intake %d times", intake.calls)
	}
	if _, err := os.Lstat(providerRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only outcome start published provider storage: %v", err)
	}
}

func TestOutcomeServiceLeavesOwnerTypedNoWriteOutcomeUnmanaged(
	t *testing.T,
) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	providerRoot := defaultProviderRoot(t, repo)
	intakeValue := ownerOutcomeTestIntake(
		"outcome-read-only",
		deliveryadmission.RouteDirectMain,
	)
	intakeValue.RoutingFacts = workrun.ImplementationRouteInput{
		ReadIntent:     workrun.ReadIntentDecideOrVerify,
		ReadFileCount:  1,
		WriteIntent:    workrun.WriteIntentNone,
		WriteFileCount: 0,
	}
	intake := &stubOwnerOutcomeIntakeAuthority{intake: intakeValue}
	service, err := NewProductionOutcomeService(
		ctx,
		repo,
		model.AgentCodex,
		intake,
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.StartOutcome(ctx, OutcomeStartRequest{
		Outcome: "Explain whether the current document is correct.",
	})
	if !errors.Is(err, ErrOutcomeNotManaged) {
		t.Fatalf("read-only outcome error = %v", err)
	}
	if intake.calls != 1 {
		t.Fatalf("owner intake calls = %d, want 1", intake.calls)
	}
	if _, err := os.Lstat(providerRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unmanaged outcome published provider storage: %v", err)
	}
}

func TestOutcomeServiceLeavesExactZeroReadAndWriteOutcomeUnmanaged(
	t *testing.T,
) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	providerRoot := defaultProviderRoot(t, repo)
	intakeValue := ownerOutcomeTestIntake(
		"outcome-zero-read-write",
		deliveryadmission.RouteDirectMain,
	)
	intakeValue.RoutingFacts = workrun.ImplementationRouteInput{
		ReadIntent:     workrun.ReadIntentNone,
		ReadFileCount:  0,
		WriteIntent:    workrun.WriteIntentNone,
		WriteFileCount: 0,
	}
	if err := workrun.ValidateImplementationRouteInput(
		intakeValue.RoutingFacts,
	); err != nil {
		t.Fatalf("zero read/write structural facts = %v", err)
	}
	if _, err := workrun.DecideImplementationRoute(
		intakeValue.RoutingFacts,
	); !errors.Is(err, workrun.ErrInsufficientRoutingFacts) {
		t.Fatalf("zero read/write route decision = %v", err)
	}
	intake := &stubOwnerOutcomeIntakeAuthority{intake: intakeValue}
	service, err := NewProductionOutcomeService(
		ctx,
		repo,
		model.AgentCodex,
		intake,
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.StartOutcome(ctx, OutcomeStartRequest{
		Outcome: "Explain the current state without changing it.",
	})
	if !errors.Is(err, ErrOutcomeNotManaged) {
		t.Fatalf("zero read/write unmanaged outcome = %v", err)
	}
	if _, err := os.Lstat(providerRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("zero read/write outcome published provider storage: %v", err)
	}
}

func TestOutcomeServiceDoesNotDowngradeAmbiguousWriteIntent(
	t *testing.T,
) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	providerRoot := defaultProviderRoot(t, repo)
	intakeValue := ownerOutcomeTestIntake(
		"outcome-ambiguous-write",
		deliveryadmission.RouteDirectMain,
	)
	intakeValue.RoutingFacts = workrun.ImplementationRouteInput{
		ReadIntent:    workrun.ReadIntentDecideOrVerify,
		ReadFileCount: 1,
	}
	intake := &stubOwnerOutcomeIntakeAuthority{intake: intakeValue}
	service, err := NewProductionOutcomeService(
		ctx,
		repo,
		model.AgentCodex,
		intake,
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.StartOutcome(ctx, OutcomeStartRequest{
		Outcome: "Explain whether the current document is correct.",
	})
	if err == nil || errors.Is(err, ErrOutcomeNotManaged) ||
		!strings.Contains(err.Error(), "explicitly classify write intent") {
		t.Fatalf("ambiguous write-intent error = %v", err)
	}
	if _, err := os.Lstat(providerRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ambiguous outcome published provider storage: %v", err)
	}
}

func TestProductiveOwnerPreflightCapabilityMatrix(t *testing.T) {
	tests := []struct {
		name      string
		agent     model.AgentID
		mode      ActivationMode
		wantError error
	}{
		{
			name:  "capable provider",
			agent: model.AgentCodex,
			mode:  ActivationEnabled,
		},
		{
			name:      "incapable unknown provider",
			agent:     model.AgentID("unknown-provider"),
			mode:      ActivationEnabled,
			wantError: capabilitymanifest.ErrUnsupportedAgent,
		},
		{
			name:      "disabled",
			agent:     model.AgentCodex,
			mode:      ActivationDisabled,
			wantError: ErrCapabilityDisabled,
		},
		{
			name:      "read only",
			agent:     model.AgentCodex,
			mode:      ActivationReadOnly,
			wantError: ErrCapabilityReadOnly,
		},
		{
			name:      "recovery only",
			agent:     model.AgentCodex,
			mode:      ActivationRecoveryOnly,
			wantError: ErrRecoveryOnly,
		},
		{
			name:      "invalid activation",
			agent:     model.AgentCodex,
			mode:      ActivationMode("unknown"),
			wantError: ErrInvalidActivationMode,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			repo := initPADAdapterGitRepository(t)
			providerRoot := defaultProviderRoot(t, repo)
			activation := &sequenceOwnerActivationResolver{
				modes: []ActivationMode{test.mode},
			}
			factory, err := NewProductionOwnerCoordinatorFactory(
				ctx,
				repo,
				test.agent,
				activation,
			)
			if err != nil {
				t.Fatal(err)
			}
			preflight, err := factory.preflightProductiveOwner(ctx)
			if test.wantError != nil {
				if !errors.Is(err, test.wantError) {
					t.Fatalf("productive preflight error = %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if preflight.agent != test.agent ||
					preflight.mode != ActivationEnabled ||
					!preflight.manifest.Advertises(
						capabilitymanifest.ContractWorkRoutingV1,
					) {
					t.Fatalf("productive preflight = %#v", preflight)
				}
			}
			if activation.calls != 1 {
				t.Fatalf("activation resolutions = %d, want 1", activation.calls)
			}
			if _, err := os.Lstat(providerRoot); !errors.Is(
				err,
				os.ErrNotExist,
			) {
				t.Fatalf("productive preflight published provider storage: %v", err)
			}
		})
	}
}

func TestOutcomeACIPreflightRejectsBeforeIntakeOrStorage(t *testing.T) {
	tests := []struct {
		name      string
		agent     model.AgentID
		mode      ActivationMode
		wantError error
	}{
		{
			name:      "unknown provider",
			agent:     model.AgentID("unknown-provider"),
			mode:      ActivationEnabled,
			wantError: capabilitymanifest.ErrUnsupportedAgent,
		},
		{
			name:      "disabled",
			agent:     model.AgentCodex,
			mode:      ActivationDisabled,
			wantError: ErrCapabilityDisabled,
		},
		{
			name:      "read only",
			agent:     model.AgentCodex,
			mode:      ActivationReadOnly,
			wantError: ErrCapabilityReadOnly,
		},
		{
			name:      "recovery only",
			agent:     model.AgentCodex,
			mode:      ActivationRecoveryOnly,
			wantError: ErrRecoveryOnly,
		},
		{
			name:      "invalid activation",
			agent:     model.AgentCodex,
			mode:      ActivationMode("unknown"),
			wantError: ErrInvalidActivationMode,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			repo := initPADAdapterGitRepository(t)
			providerRoot := defaultProviderRoot(t, repo)
			intake := &stubOwnerOutcomeIntakeAuthority{
				intake: ownerOutcomeTestIntake(
					"outcome-aci-"+strings.ReplaceAll(test.name, " ", "-"),
					deliveryadmission.RouteDirectMain,
				),
			}
			activation := &sequenceOwnerActivationResolver{
				modes: []ActivationMode{test.mode},
			}
			service, err := NewProductionOutcomeService(
				ctx,
				repo,
				test.agent,
				intake,
				activation,
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.StartOutcome(ctx, OutcomeStartRequest{
				Outcome: "Apply one bounded change.",
			})
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Outcome ACI preflight error = %v", err)
			}
			if activation.calls != 1 {
				t.Fatalf("activation resolutions = %d, want 1", activation.calls)
			}
			if intake.calls != 0 {
				t.Fatalf("rejected ACI preflight called intake %d times", intake.calls)
			}
			if _, err := os.Lstat(providerRoot); !errors.Is(
				err,
				os.ErrNotExist,
			) {
				t.Fatalf("rejected ACI preflight published provider storage: %v", err)
			}
		})
	}
}

func TestOutcomeACIIntegrationPreservesExistingV1Contracts(t *testing.T) {
	if capabilitymanifest.SchemaV1 !=
		capabilitymanifest.SchemaVersion(
			"gentle-ai.agent-capability-manifest/v1",
		) ||
		capabilitymanifest.ContractWorkRoutingV1 !=
			capabilitymanifest.ContractID("gentle-ai.work-routing/v1") ||
		workrun.WorkStatusContractV1 != "gentle-ai.work-status/v1" ||
		workrun.WorkTransitionContractV1 != "gentle-ai.work-transition/v1" ||
		sddstatus.SchemaName != "gentle-ai.sdd-status" ||
		sddstatus.SchemaVersion != 1 ||
		sddstatus.StatusContractV1 != "gentle-ai.sdd-status/v1" {
		t.Fatal("productive ACI integration changed a frozen v1 contract identity")
	}
	if _, exists := reflect.TypeOf(OutcomeStartRequest{}).FieldByName(
		"Capability",
	); exists {
		t.Fatal("outcome caller must not select a capability")
	}
	if _, exists := reflect.TypeOf(OutcomeStartRequest{}).FieldByName(
		"AgentID",
	); exists {
		t.Fatal("outcome caller must not select the provider agent")
	}
}

func TestProductionOwnerFactoryRetainsReadOnlySharedLease(t *testing.T) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	providerRoot := defaultProviderRoot(t, repo)
	factory, err := NewProductionOwnerCoordinatorFactory(
		ctx,
		repo,
		model.AgentCodex,
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := factory.repositoryIdentityLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lease != factory.lease ||
		lease.Identity().RepositoryRef != factory.RepositoryRef() ||
		lease.Identity().RepositoryRoot != factory.repositoryRoot() {
		t.Fatal("production owner factory did not retain one exact repository lease")
	}
	if _, err := os.Lstat(providerRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only owner factory published provider storage: %v", err)
	}
}

func TestProductionOwnerCoordinatorRechecksKillSwitchAfterOpen(t *testing.T) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	activation := &sequenceOwnerActivationResolver{
		modes: []ActivationMode{
			ActivationEnabled,
			ActivationEnabled,
			ActivationDisabled,
		},
	}
	factory, err := NewProductionOwnerCoordinatorFactory(
		ctx,
		repo,
		model.AgentCodex,
		activation,
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := factory.Open(ctx, "kill-switch-after-open")
	if err != nil {
		t.Fatal(err)
	}
	root := defaultProviderRoot(t, repo)
	before := ownerTreeFingerprint(t, root)
	if _, err := coordinator.StartWork(
		ctx,
		OwnerStartWorkRequest{},
	); !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("mutation after kill-switch disable error = %v", err)
	}
	if activation.calls != 3 {
		t.Fatalf("activation resolutions = %d, want 3", activation.calls)
	}
	if after := ownerTreeFingerprint(t, root); after != before {
		t.Fatalf(
			"disabled mutation changed owner stores:\nbefore %s\nafter  %s",
			before,
			after,
		)
	}
}

func TestOutcomeRechecksKillSwitchAfterIntakeBeforeOpeningStores(t *testing.T) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	providerRoot := defaultProviderRoot(t, repo)
	intake := &stubOwnerOutcomeIntakeAuthority{
		intake: ownerOutcomeTestIntake(
			"kill-switch-after-intake",
			deliveryadmission.RouteDirectMain,
		),
	}
	activation := &sequenceOwnerActivationResolver{
		modes: []ActivationMode{
			ActivationEnabled,
			ActivationDisabled,
		},
	}
	service, err := NewProductionOutcomeService(
		ctx,
		repo,
		model.AgentCodex,
		intake,
		activation,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartOutcome(ctx, OutcomeStartRequest{
		Outcome: "Apply one bounded change.",
	}); !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("post-intake kill-switch error = %v", err)
	}
	if intake.calls != 1 {
		t.Fatalf("owner intake calls = %d, want 1", intake.calls)
	}
	if activation.calls != 2 {
		t.Fatalf("activation resolutions = %d, want 2", activation.calls)
	}
	if _, err := os.Lstat(providerRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"post-intake disabled activation published provider storage: %v",
			err,
		)
	}
}

func TestOutcomeStartRequestExposesNoAuthorityBearingInputs(t *testing.T) {
	requestType := reflect.TypeOf(OutcomeStartRequest{})
	want := []string{"Outcome", "ExplicitSDDRequested"}
	if requestType.NumField() != len(want) {
		t.Fatalf("outcome request fields = %d, want %d", requestType.NumField(), len(want))
	}
	for index, name := range want {
		if requestType.Field(index).Name != name {
			t.Fatalf(
				"outcome request field %d = %q, want %q",
				index,
				requestType.Field(index).Name,
				name,
			)
		}
	}
	for index := 0; index < requestType.NumField(); index++ {
		lower := strings.ToLower(requestType.Field(index).Name)
		for _, forbidden := range []string{
			"ref",
			"digest",
			"hash",
			"policy",
			"argv",
			"command",
			"route",
			"workrun",
		} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf(
					"outcome request exposes authority-bearing field %q",
					requestType.Field(index).Name,
				)
			}
		}
	}
}

func TestOutcomeServiceRejectsOwnerAuthoredExplicitRefBeforePublication(
	t *testing.T,
) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	providerRoot := defaultProviderRoot(t, repo)
	ownerIntake := ownerOutcomeTestIntake(
		"outcome-forged-sdd",
		deliveryadmission.RouteDirectMain,
	)
	ownerIntake.RoutingFacts.ExplicitSDDRequestRef =
		ownerTestRef("caller-authored-explicit-sdd")
	intake := &stubOwnerOutcomeIntakeAuthority{intake: ownerIntake}
	service, err := NewProductionOutcomeService(
		ctx,
		repo,
		model.AgentCodex,
		intake,
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.StartOutcome(ctx, OutcomeStartRequest{
		Outcome:              "Use the explicit SDD route.",
		ExplicitSDDRequested: true,
	})
	if err == nil || !strings.Contains(
		err.Error(),
		"cannot provide an explicit SDD authority reference",
	) {
		t.Fatalf("owner-authored explicit SDD ref error = %v", err)
	}
	if intake.calls != 1 {
		t.Fatalf("owner intake calls = %d, want 1", intake.calls)
	}
	if _, err := os.Lstat(providerRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid owner intake published provider storage: %v", err)
	}
}

func TestProductionOutcomeServiceAdmitsAndStartsDirectMain(t *testing.T) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	ownerIntake := ownerOutcomeTestIntake(
		"outcome-direct-main",
		deliveryadmission.RouteDirectMain,
	)
	repositoryRef, policyRef := provisionOutcomeLivePolicy(
		t,
		repo,
		deliveryadmission.RouteDirectMain,
		false,
	)
	intake := &stubOwnerOutcomeIntakeAuthority{intake: ownerIntake}
	activation := &sequenceOwnerActivationResolver{
		modes: []ActivationMode{ActivationEnabled},
	}
	service, err := NewProductionOutcomeService(
		ctx,
		repo,
		model.AgentCodex,
		intake,
		activation,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := OutcomeStartRequest{
		Outcome: "Apply one already-understood mechanical fix directly.",
	}
	status, err := service.StartOutcome(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := status.Validate(); err != nil {
		t.Fatal(err)
	}
	if status.WorkRunID != ownerIntake.WorkRunID ||
		status.PublicState != workrun.PublicStateWorking ||
		status.RouteDecision != workrun.RouteDecisionDirectInline ||
		status.ImplementationRoute != workrun.ImplementationRouteDirectInline ||
		status.DeliveryIntentRef == "" ||
		status.SDDRunRef != "" {
		t.Fatalf("direct-main outcome status = %#v", status)
	}
	if activation.calls != 5 {
		t.Fatalf(
			"one productive outcome start resolved activation %d times",
			activation.calls,
		)
	}
	if intake.calls != 1 ||
		len(intake.contexts) != 1 ||
		intake.contexts[0].RepositoryRef != repositoryRef ||
		intake.contexts[0].RepositoryRoot != repo ||
		!reflect.DeepEqual(intake.requests[0], request) {
		t.Fatalf(
			"owner intake invocation = calls:%d contexts:%#v requests:%#v",
			intake.calls,
			intake.contexts,
			intake.requests,
		)
	}

	authority, err := NewPADRepositoryAuthority(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	pad, err := deliveryadmission.OpenTrustedRepository(
		ctx,
		authority,
		authority.RepositoryRef(),
	)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := pad.ResolveAdmittedDecision(ctx, status.DeliveryIntentRef)
	if err != nil {
		_ = pad.Close()
		t.Fatal(err)
	}
	if err := pad.Close(); err != nil {
		t.Fatal(err)
	}
	wantScope, err := outcomeScopeDigest(
		repositoryRef,
		request.Outcome,
		ownerIntake.ScopeSelectors,
	)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.Decision.Intent.ScopeDigest != wantScope ||
		admitted.Decision.Intent.Destination.RepositoryRef != repositoryRef ||
		admitted.Decision.PolicyRef != policyRef ||
		admitted.Decision.Authority.Provenance !=
			deliveryadmission.ProvenanceMaintainerControl ||
		admitted.Decision.GovernanceRef != "" {
		t.Fatalf("derived PAD admission = %#v", admitted)
	}

	root := defaultProviderRoot(t, repo)
	beforeReplay := ownerTreeFingerprint(t, root)
	beforeReplayState := outcomeTreeState(t, root)
	replayed, err := service.StartOutcome(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, status) {
		t.Fatalf("outcome replay = %#v, want %#v", replayed, status)
	}
	if activation.calls != 10 {
		t.Fatalf(
			"two outcome operations resolved activation %d times",
			activation.calls,
		)
	}
	if afterReplay := ownerTreeFingerprint(t, root); afterReplay != beforeReplay {
		t.Fatalf(
			"outcome replay mutated owner stores:\nbefore %s\nafter  %s\nchanges:\n%s",
			beforeReplay,
			afterReplay,
			outcomeTreeDiff(beforeReplayState, outcomeTreeState(t, root)),
		)
	}

	beforeConflict := ownerTreeFingerprint(t, root)
	if _, err := service.StartOutcome(ctx, OutcomeStartRequest{
		Outcome: "Apply a materially different direct change.",
	}); !errors.Is(err, workrun.ErrWorkRunAlreadyStarted) {
		t.Fatalf("different outcome replay error = %v", err)
	}
	if afterConflict := ownerTreeFingerprint(
		t,
		root,
	); afterConflict != beforeConflict {
		t.Fatalf(
			"different outcome conflict mutated owner stores:\nbefore %s\nafter  %s",
			beforeConflict,
			afterConflict,
		)
	}
}

func outcomeTreeState(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	if err := filepath.Walk(root, func(
		path string,
		info os.FileInfo,
		err error,
	) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = fmt.Sprintf(
			"%s|%d|%d",
			info.Mode(),
			info.Size(),
			info.ModTime().UnixNano(),
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func outcomeTreeDiff(before, after map[string]string) string {
	var changes []string
	for path, value := range before {
		if after[path] != value {
			changes = append(
				changes,
				fmt.Sprintf("%s: %s -> %s", path, value, after[path]),
			)
		}
	}
	for path, value := range after {
		if _, exists := before[path]; !exists {
			changes = append(changes, fmt.Sprintf("%s: <absent> -> %s", path, value))
		}
	}
	return strings.Join(changes, "\n")
}

func TestProductionOutcomeServiceDerivesExplicitSDDAuthority(t *testing.T) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	ownerIntake := ownerOutcomeTestIntake(
		"outcome-explicit-sdd",
		deliveryadmission.RoutePRWithoutIssue,
	)
	provisionOutcomeLivePolicy(
		t,
		repo,
		deliveryadmission.RoutePRWithoutIssue,
		false,
	)
	intake := &stubOwnerOutcomeIntakeAuthority{intake: ownerIntake}
	service, err := NewProductionOutcomeService(
		ctx,
		repo,
		model.AgentCodex,
		intake,
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.StartOutcome(ctx, OutcomeStartRequest{
		Outcome:              "Prepare this change through SDD.",
		ExplicitSDDRequested: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.RouteDecision != workrun.RouteDecisionProposeSDD ||
		status.PublicState != workrun.PublicStateWorking ||
		status.ImplementationRoute != "" ||
		status.SDDRunRef != "" {
		t.Fatalf("explicit SDD public status = %#v", status)
	}
	coordinator, err := service.factory.Open(ctx, ownerIntake.WorkRunID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := coordinator.work.Status()
	if err != nil {
		t.Fatal(err)
	}
	explicitRef := state.RouteDecision.ExplicitSDDRequestRef
	if !validPADImmutableRef(explicitRef) ||
		state.ImplementationRoute != workrun.ImplementationRouteSDD ||
		state.RouteAcceptanceRef != explicitRef {
		t.Fatalf("explicit SDD owner state = %#v", state)
	}
	explicit, err := coordinator.coordination.ResolveExplicitSDDRequest(
		ctx,
		explicitRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.WorkRunID != ownerIntake.WorkRunID ||
		explicit.DeliveryIntentRef != status.DeliveryIntentRef ||
		explicit.AuthorityRef != explicitRef {
		t.Fatalf("derived explicit SDD authority = %#v", explicit)
	}
	if ownerIntake.RoutingFacts.ExplicitSDDRequestRef != "" {
		t.Fatal("owner intake supplied the derived explicit SDD reference")
	}
}

func TestProductionOutcomeServiceUsesPADGovernedDefaultRoute(t *testing.T) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	ownerIntake := ownerOutcomeTestIntake(
		"outcome-default-route",
		deliveryadmission.RoutePRWithIssue,
	)
	ownerIntake.Route = ""
	_, policyRef := provisionOutcomeLivePolicy(
		t,
		repo,
		deliveryadmission.RoutePRWithIssue,
		false,
	)
	intake := &stubOwnerOutcomeIntakeAuthority{intake: ownerIntake}
	service, err := NewProductionOutcomeService(
		ctx,
		repo,
		model.AgentCodex,
		intake,
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.StartOutcome(ctx, OutcomeStartRequest{
		Outcome: "Use the governed community delivery default.",
	})
	if err != nil {
		t.Fatal(err)
	}
	authority, err := NewPADRepositoryAuthority(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	pad, err := deliveryadmission.OpenTrustedRepository(
		ctx,
		authority,
		authority.RepositoryRef(),
	)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := pad.ResolveAdmittedDecision(ctx, status.DeliveryIntentRef)
	if err != nil {
		_ = pad.Close()
		t.Fatal(err)
	}
	if err := pad.Close(); err != nil {
		t.Fatal(err)
	}
	if admitted.Decision.Route != deliveryadmission.RoutePRWithIssue ||
		admitted.Decision.PolicyRef != policyRef ||
		admitted.Decision.GovernanceRef == "" ||
		intake.intake.Route != "" {
		t.Fatalf("PAD default-route admission = %#v", admitted)
	}
}

func TestProductionOutcomeServiceDefaultRouteNeverFallsBack(t *testing.T) {
	ctx := context.Background()
	repo := initPADAdapterGitRepository(t)
	ownerIntake := ownerOutcomeTestIntake(
		"outcome-default-no-fallback",
		deliveryadmission.RoutePRWithIssue,
	)
	ownerIntake.Route = ""
	provisionOutcomeLivePolicy(
		t,
		repo,
		deliveryadmission.RouteDirectMain,
		false,
	)
	service, err := NewProductionOutcomeService(
		ctx,
		repo,
		model.AgentCodex,
		&stubOwnerOutcomeIntakeAuthority{intake: ownerIntake},
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartOutcome(ctx, OutcomeStartRequest{
		Outcome: "Use the governed community delivery default.",
	}); err == nil || !strings.Contains(err.Error(), "live route policy") {
		t.Fatalf("missing default route policy error = %v", err)
	}
	coordinator, err := service.factory.Open(ctx, ownerIntake.WorkRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.work.Status(); !errors.Is(
		err,
		workrun.ErrWorkRunNotStarted,
	) {
		t.Fatalf("default-route failure started WorkRun: %v", err)
	}
}

func TestOwnerOutcomePreimagesCoverAllDeliveryRoutes(t *testing.T) {
	request := OutcomeStartRequest{Outcome: "Deliver through the selected route."}
	repositoryRef := ownerTestRef("outcome-route-repository")
	tests := []struct {
		route          deliveryadmission.Route
		wantGovernance bool
		wantRisk       bool
	}{
		{deliveryadmission.RoutePRWithIssue, true, false},
		{deliveryadmission.RoutePRWithoutIssue, false, false},
		{deliveryadmission.RouteDirectMain, false, false},
		{deliveryadmission.RouteEmergency, true, true},
	}
	for _, test := range tests {
		t.Run(string(test.route), func(t *testing.T) {
			intake := ownerOutcomeTestIntake(
				"outcome-route-"+string(test.route),
				test.route,
			)
			if err := intake.validate(repositoryRef, request); err != nil {
				t.Fatal(err)
			}
			requestedAt := time.Now().UTC().Unix()
			intake, err := intake.validateSelectedRoute(
				test.route,
				requestedAt,
			)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := deliveryadmission.NewRoutePolicy(
				"policy:"+string(test.route),
				"revision:1",
				test.route,
				true,
				false,
				false,
				3_600,
				0,
			)
			if err != nil {
				t.Fatal(err)
			}
			policyRef, err := policy.Ref()
			if err != nil {
				t.Fatal(err)
			}
			scopeDigest, err := outcomeScopeDigest(
				repositoryRef,
				request.Outcome,
				intake.ScopeSelectors,
			)
			if err != nil {
				t.Fatal(err)
			}
			intent, err := deliveryadmission.NewIntent(
				intake.Nonce,
				test.route,
				scopeDigest,
				deliveryadmission.DestinationBinding{
					RepositoryRef:    repositoryRef,
					TargetRef:        intake.Destination.TargetRef,
					ObservedRevision: intake.Destination.ObservedRevision,
					DefaultBranch:    intake.Destination.DefaultBranch,
				},
				policyRef,
				policy.Binding(),
				requestedAt,
			)
			if err != nil {
				t.Fatal(err)
			}
			preimages, err := buildOwnerOutcomePreimages(
				repositoryRef,
				policy,
				intent,
				intake,
			)
			if err != nil {
				t.Fatal(err)
			}
			intentRef, err := preimages.intent.Ref()
			if err != nil {
				t.Fatal(err)
			}
			if (preimages.governance != nil) != test.wantGovernance ||
				(preimages.residualRisk != nil) != test.wantRisk {
				t.Fatalf("route preimages = %#v", preimages)
			}
			if preimages.governance != nil &&
				preimages.governance.SubjectRef != intentRef {
				t.Fatalf(
					"governance subject = %q, want %q",
					preimages.governance.SubjectRef,
					intentRef,
				)
			}
			if preimages.residualRisk != nil &&
				preimages.residualRisk.SubjectRef != intentRef {
				t.Fatalf(
					"residual-risk subject = %q, want %q",
					preimages.residualRisk.SubjectRef,
					intentRef,
				)
			}
		})
	}
}

func ownerOutcomeTestIntake(
	workRunID string,
	route deliveryadmission.Route,
) OwnerOutcomeIntake {
	requestedAt := time.Now().UTC().Unix() - 1
	intake := OwnerOutcomeIntake{
		WorkRunID:      workRunID,
		Nonce:          "nonce:" + workRunID,
		Route:          route,
		ScopeSelectors: []string{"internal/workprovider/outcome_service.go"},
		Destination: OwnerDestinationInput{
			TargetRef:        "refs/heads/main",
			ObservedRevision: "git:base/1",
			DefaultBranch:    true,
		},
		PrimaryAuthority: OwnerAuthoritySignalInput{
			SignalID:   "signal:" + workRunID,
			IssuerRef:  "maintainer:owner",
			Provenance: deliveryadmission.ProvenanceMaintainerControl,
			ExpiresAt:  requestedAt + 3_600,
		},
		RoutingFacts: workrun.ImplementationRouteInput{
			WriteIntent:    workrun.WriteIntentAtomicMechanical,
			WriteFileCount: 1,
		},
	}
	switch route {
	case deliveryadmission.RoutePRWithIssue:
		intake.Governance = &OwnerGovernanceInput{
			DecisionID:       "decision:" + workRunID,
			ApprovedIssueRef: "issue:1794",
			ExpiresAt:        requestedAt + 3_600,
		}
	case deliveryadmission.RouteEmergency:
		intake.Governance = &OwnerGovernanceInput{
			DecisionID: "decision:" + workRunID,
			Reason:     "Immediate owner-approved recovery is required.",
			ExpiresAt:  requestedAt + 3_600,
			ResidualRisk: &OwnerResidualRiskInput{
				RiskID:      "risk:" + workRunID,
				Description: "Owner accepts the bounded residual recovery risk.",
				ExpiresAt:   requestedAt + 3_600,
			},
		}
	}
	return intake
}

func provisionOutcomeLivePolicy(
	t *testing.T,
	repo string,
	route deliveryadmission.Route,
	requireSecondMaintainer bool,
) (string, string) {
	t.Helper()
	ctx := context.Background()
	authority, err := NewPADRepositoryAuthority(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	pad, err := deliveryadmission.OpenTrustedRepository(
		ctx,
		authority,
		authority.RepositoryRef(),
	)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := deliveryadmission.NewRoutePolicy(
		"policy:outcome:"+string(route),
		"revision:1",
		route,
		true,
		false,
		requireSecondMaintainer,
		3_600,
		0,
	)
	if err != nil {
		_ = pad.Close()
		t.Fatal(err)
	}
	policyRef, err := pad.PublishRoutePolicy(ctx, policy)
	if err != nil {
		_ = pad.Close()
		t.Fatal(err)
	}
	if _, err := pad.PutLiveRoutePolicy(
		ctx,
		deliveryadmission.LiveRoutePolicyUpdate{
			Route:     route,
			PolicyRef: policyRef,
		},
	); err != nil {
		_ = pad.Close()
		t.Fatal(err)
	}
	if err := pad.Close(); err != nil {
		t.Fatal(err)
	}
	return authority.RepositoryRef(), policyRef
}
