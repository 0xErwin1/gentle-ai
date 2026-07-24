package workprovider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/deliveryadmission"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestDefaultControllerUsesProductionCompositionWithoutPublishingOnStatus(
	t *testing.T,
) {
	restoreDefaultActivationEnvironment(t)
	repo := initPADAdapterGitRepository(t)
	providerRoot := defaultProviderRoot(t, repo)

	controller := NewDefaultController()
	_, err := controller.Status(context.Background(), StatusRequest{
		Repo:      repo,
		WorkRunID: "missing-default-run",
		Contract:  workrun.WorkStatusContractV1,
	})
	if !errors.Is(err, workrun.ErrWorkRunNotStarted) {
		t.Fatalf("default status error = %v", err)
	}
	if _, err := os.Lstat(providerRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"default status published provider root %q or hid an error: %v",
			providerRoot,
			err,
		)
	}
}

func TestDefaultControllerReadOnlyKillSwitchBlocksBeforeRepositoryOpen(
	t *testing.T,
) {
	t.Setenv(WorkRoutingModeEnvironment, string(ActivationReadOnly))
	repo := initPADAdapterGitRepository(t)
	providerRoot := defaultProviderRoot(t, repo)

	_, err := NewDefaultController().Apply(context.Background(), ApplyRequest{
		Repo:             repo,
		WorkRunID:        "read-only-default-run",
		Contract:         workrun.WorkTransitionContractV1,
		AuthorizationRef: defaultTestRef("authorization"),
		ExpectedRevision: defaultTestRef("revision"),
	})
	if !errors.Is(err, ErrCapabilityReadOnly) {
		t.Fatalf("read-only default apply error = %v", err)
	}
	if _, err := os.Lstat(providerRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"read-only apply published provider root %q or hid an error: %v",
			providerRoot,
			err,
		)
	}
}

func TestProductionRepositoryOpenerProjectsReadyProductiveExecution(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"default-ready-execution",
		productiveAdvanceTestCASSucceed,
		false,
	)
	ctx := context.Background()
	terminal, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := workrun.OpenWorkRunStore(
		ctx,
		fixture.repo,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err := raw.Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.ProductiveExecutionResultRef == "" ||
		state.DeliveryResultRef == "" ||
		terminal.Status.PublicState != workrun.PublicStateReady {
		t.Fatalf(
			"productive Ready fixture lacks terminal authority: state=%#v status=%#v",
			state,
			terminal.Status,
		)
	}

	repository, err := (ProductionRepositoryOpener{}).OpenRepository(
		ctx,
		fixture.repo,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	status, err := repository.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(status, terminal.Status) {
		t.Fatalf(
			"default productive Ready projection differs:\nwant %#v\ngot  %#v",
			terminal.Status,
			status,
		)
	}
}

func TestProductionRepositoryOpenerProjectsReconciledTerminal(
	t *testing.T,
) {
	tests := []struct {
		name          string
		outcome       workrun.WorkReconcileOutcome
		wantExecution bool
		wantDelivery  bool
		setup         func(*testing.T) (
			productiveAdvanceTestFixture,
			workrun.WorkAdvanceV1,
		)
	}{
		{
			name:    "manual",
			outcome: workrun.WorkReconcileManualResolution,
			setup: func(t *testing.T) (
				productiveAdvanceTestFixture,
				workrun.WorkAdvanceV1,
			) {
				fixture := newProductiveAdvanceTestFixture(
					t,
					"default-reconciled-manual",
					productiveAdvanceTestCASIndeterminate,
					false,
				)
				blocked, err := fixture.runtime.AdvanceOutcome(
					context.Background(),
					fixture.workRunID,
					fixture.start.Revision,
				)
				if err != nil {
					t.Fatal(err)
				}
				return fixture, blocked
			},
		},
		{
			name:    "no delivery",
			outcome: workrun.WorkReconcileNoDeliveryConfirmed,
			setup: func(t *testing.T) (
				productiveAdvanceTestFixture,
				workrun.WorkAdvanceV1,
			) {
				fixture := newProductiveAdvanceTestFixture(
					t,
					"default-reconciled-no-delivery",
					productiveAdvanceTestCASSucceed,
					false,
				)
				return fixture,
					blockProductiveReconciliationBeforeEffect(t, fixture)
			},
		},
		{
			name:          "delivery confirmed",
			outcome:       workrun.WorkReconcileDeliveryConfirmed,
			wantExecution: true,
			wantDelivery:  true,
			setup: func(t *testing.T) (
				productiveAdvanceTestFixture,
				workrun.WorkAdvanceV1,
			) {
				fixture := newProductiveAdvanceTestFixture(
					t,
					"default-reconciled-delivery",
					productiveAdvanceTestCASSucceed,
					true,
				)
				return fixture,
					blockProductiveReconciliationAfterDurableSuccess(
						t,
						fixture,
					)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, blocked := test.setup(t)
			ctx := context.Background()
			reconciled, err := fixture.runtime.ReconcileOutcome(
				ctx,
				fixture.workRunID,
				blocked.Status.Revision,
				blocked.Diagnostic.Ref,
			)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := workrun.OpenWorkRunStore(
				ctx,
				fixture.repo,
				fixture.workRunID,
			)
			if err != nil {
				t.Fatal(err)
			}
			state, err := raw.Status()
			if err != nil {
				t.Fatal(err)
			}
			if state.ProductiveReconciliationRef == "" ||
				(state.ProductiveExecutionResultRef != "") !=
					test.wantExecution ||
				(state.DeliveryResultRef != "") != test.wantDelivery ||
				reconciled.Outcome != test.outcome {
				t.Fatalf(
					"productive reconciliation fixture lacks exact authority: state=%#v result=%#v",
					state,
					reconciled,
				)
			}

			repository, err := (ProductionRepositoryOpener{}).
				OpenRepository(
					ctx,
					fixture.repo,
					fixture.workRunID,
				)
			if err != nil {
				t.Fatal(err)
			}
			status, err := repository.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(status, reconciled.Status) {
				t.Fatalf(
					"default reconciled projection differs:\nwant %#v\ngot  %#v",
					reconciled.Status,
					status,
				)
			}
		})
	}
}

func TestProductionRepositoryOpenerBindsAfterDeliveryRouteReevaluation(
	t *testing.T,
) {
	fixture := newOwnerProductiveRouteReevaluationFixture(
		t,
		"default-delivery-route",
	)
	ctx := context.Background()
	changed, err := fixture.coordinator.ReevaluateDeliveryRoute(
		ctx,
		fixture.request(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if changed.State.DeliveryRouteReevaluationRef == "" {
		t.Fatalf("productive route fixture = %#v", changed)
	}
	decision, err := fixture.owner.pad.ResolveDeliveryDecision(
		ctx,
		changed.Reevaluation.DecisionRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := deliveryadmission.IssueAuthorization(
		ctx,
		fixture.owner.pad,
		fixture.owner.coordinator.rar,
		deliveryadmission.AuthorizationIssueRequest{
			DecisionRef:  changed.Reevaluation.DecisionRef,
			Nonce:        "nonce:default-delivery-route",
			AuthorityRef: decision.AuthorityRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorizationRef, err := fixture.owner.pad.PublishAuthorization(
		ctx,
		authorization,
	)
	if err != nil {
		t.Fatal(err)
	}

	opened, err := (ProductionRepositoryOpener{}).OpenRepository(
		ctx,
		fixture.owner.repo,
		fixture.owner.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	production, ok := opened.(*ProductionRepository)
	if !ok {
		t.Fatalf("default repository type = %T", opened)
	}
	bound, err := production.WorkRun.BindDeliveryAuthorization(
		ctx,
		workrun.BindDeliveryAuthorizationRequest{
			ExpectedRevision: changed.State.Revision,
			RequestID:        "default-delivery-route-authorization",
			AuthorizationRef: authorizationRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.DeliveryRouteReevaluationRef != changed.ReevaluationRef ||
		bound.DeliveryIntentRef !=
			fixture.targetAdmission.Admission.IntentRef ||
		bound.DeliveryAuthorizationRef != authorizationRef {
		t.Fatalf("default route authorization binding = %#v", bound)
	}
}

func TestReadOnlyWorkRunRepositoryOpenerProjectsReadyAndRejectsMutation(
	t *testing.T,
) {
	fixture := newProductiveAdvanceTestFixture(
		t,
		"read-only-ready-execution",
		productiveAdvanceTestCASSucceed,
		false,
	)
	ctx := context.Background()
	terminal, err := fixture.runtime.AdvanceOutcome(
		ctx,
		fixture.workRunID,
		fixture.start.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := workrun.OpenWorkRunStore(
		ctx,
		fixture.repo,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	before, err := raw.Status()
	if err != nil {
		t.Fatal(err)
	}
	if before.ProductiveExecutionResultRef == "" ||
		before.DeliveryResultRef == "" {
		t.Fatalf("read-only Ready fixture = %#v", before)
	}

	repository, err := (ReadOnlyWorkRunRepositoryOpener{}).OpenRepository(
		ctx,
		fixture.repo,
		fixture.workRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	status, err := repository.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(status, terminal.Status) ||
		status.PublicState != workrun.PublicStateReady ||
		status.AuthorizedTransition != nil {
		t.Fatalf(
			"read-only productive Ready projection:\nwant %#v\ngot  %#v",
			terminal.Status,
			status,
		)
	}
	if _, err := repository.ResolveAuthorization(
		ctx,
		defaultTestRef("read-only-authorization"),
	); !errors.Is(err, ErrAuthorizationNotFound) {
		t.Fatalf("read-only authorization resolution = %v", err)
	}
	if _, err := repository.ApplyAuthorized(
		ctx,
		defaultTestRef("read-only-authorization"),
		before.Revision,
	); !errors.Is(err, ErrCapabilityReadOnly) {
		t.Fatalf("read-only apply = %v", err)
	}
	after, err := raw.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf(
			"read-only rejection mutated WorkRun:\nbefore %#v\nafter  %#v",
			before,
			after,
		)
	}
}

func restoreDefaultActivationEnvironment(t *testing.T) {
	t.Helper()
	value, exists := os.LookupEnv(WorkRoutingModeEnvironment)
	if err := os.Unsetenv(WorkRoutingModeEnvironment); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var err error
		if exists {
			err = os.Setenv(WorkRoutingModeEnvironment, value)
		} else {
			err = os.Unsetenv(WorkRoutingModeEnvironment)
		}
		if err != nil {
			t.Errorf("restore activation environment: %v", err)
		}
	})
}

func defaultProviderRoot(t *testing.T, repo string) string {
	t.Helper()
	output, err := exec.Command(
		"git",
		"-C",
		repo,
		"rev-parse",
		"--path-format=absolute",
		"--git-common-dir",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(
		filepath.Clean(strings.TrimSpace(string(output))),
		"gentle-ai",
	)
}

func defaultTestRef(seed string) string {
	return testPADRef("default-" + seed)
}
