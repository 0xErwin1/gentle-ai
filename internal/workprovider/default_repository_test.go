package workprovider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
