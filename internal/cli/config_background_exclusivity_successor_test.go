package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/pipeline"
	"github.com/gentleman-programming/gentle-ai/v2/internal/system"
)

func TestConfigInstallBackgroundSubagentsIsExclusive(t *testing.T) {
	configPath := writeBackgroundExclusivityConfig(t)

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "without dry run", args: []string{"--config", configPath, "--opencode-background-subagents=on"}},
		{name: "with dry run", args: []string{"--config", configPath, "--opencode-background-subagents=on", "--dry-run"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseInstallFlags(test.args)
			assertConfigFlagsExclusive(t, err)
		})
	}
}

func TestConfigSyncBackgroundSubagentsIsExclusive(t *testing.T) {
	configPath := writeBackgroundExclusivityConfig(t)

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "without dry run", args: []string{"--config", configPath, "--opencode-background-subagents=on"}},
		{name: "with dry run", args: []string{"--config", configPath, "--opencode-background-subagents=on", "--dry-run"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseSyncFlags(test.args)
			assertConfigFlagsExclusive(t, err)
		})
	}
}

func TestConfigInstallBackgroundSubagentsRejectsBeforePlanning(t *testing.T) {
	home := t.TempDir()
	configPath := writeBackgroundExclusivityConfig(t)
	originalHome := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = originalHome })

	result, err := RunInstall([]string{"--config", configPath, "--opencode-background-subagents=on", "--dry-run"}, system.DetectionResult{})
	assertBackgroundExclusivityRejectsBeforePlanning(t, err, result.Plan, home)
}

func TestConfigSyncBackgroundSubagentsRejectsBeforePlanning(t *testing.T) {
	home := t.TempDir()
	configPath := writeBackgroundExclusivityConfig(t)
	originalHome := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = originalHome })

	result, err := RunSync([]string{"--config", configPath, "--opencode-background-subagents=on", "--dry-run"})
	assertBackgroundExclusivityRejectsBeforePlanning(t, err, result.Plan, home)
}

func writeBackgroundExclusivityConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "desired.json")
	if err := os.WriteFile(path, []byte(`{"version":"v1","selection":{"agents":["opencode"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	return path
}

func assertConfigFlagsExclusive(t *testing.T, err error) {
	t.Helper()

	if err == nil || !strings.Contains(err.Error(), "config.flags.exclusive") {
		t.Fatalf("error = %v, want config.flags.exclusive before planning", err)
	}
}

func assertBackgroundExclusivityRejectsBeforePlanning(t *testing.T, err error, plan pipeline.StagePlan, destination string) {
	t.Helper()

	if err == nil {
		t.Fatalf("config accepted and planned %d steps, want config.flags.exclusive before planning", len(plan.Prepare)+len(plan.Apply))
	}
	assertConfigFlagsExclusive(t, err)

	if len(plan.Prepare)+len(plan.Apply) != 0 {
		t.Fatalf("planner steps = %d, want 0", len(plan.Prepare)+len(plan.Apply))
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("destination entries = %v, want unchanged empty destination", entries)
	}
}
