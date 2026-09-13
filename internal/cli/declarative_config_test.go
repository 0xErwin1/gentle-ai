package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
	"github.com/gentleman-programming/gentle-ai/v2/internal/system"
)

func TestConfigFlagsRejectSemanticSelectionAndKeepOperationalFlags(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "desired.json")
	if err := os.WriteFile(configPath, []byte(`{"version":"v1","selection":{"agents":["opencode"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		parse func([]string) error
		args  []string
	}{
		{"install rejects semantic agent", func(args []string) error { _, err := ParseInstallFlags(args); return err }, []string{"--config", configPath, "--agent", "opencode"}},
		{"sync rejects semantic mode", func(args []string) error { _, err := ParseSyncFlags(args); return err }, []string{"--config", configPath, "--sdd-mode", "single"}},
		{"install keeps dry run", func(args []string) error {
			flags, err := ParseInstallFlags(args)
			if err == nil && !flags.DryRun {
				t.Fatal("install dry-run was not retained")
			}
			return err
		}, []string{"--config", configPath, "--dry-run", "--scope", "workspace"}},
		{"sync keeps dry run", func(args []string) error {
			flags, err := ParseSyncFlags(args)
			if err == nil && !flags.DryRun {
				t.Fatal("sync dry-run was not retained")
			}
			return err
		}, []string{"--config", configPath, "--dry-run"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.parse(test.args)
			if strings.Contains(test.name, "rejects") {
				if err == nil || !strings.Contains(err.Error(), "config.flags.exclusive") {
					t.Fatalf("error = %v, want config exclusivity diagnostic", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse error = %v", err)
			}
		})
	}
}

func TestConfigBackedInstallAndSyncUseDesiredSelection(t *testing.T) {
	home := t.TempDir()
	configPath := writeDesiredConfig(t, `{"version":"v1","selection":{"agents":["opencode"],"persona":"neutral","components":["engram"]}}`)

	install, err := RunInstall([]string{"--config", configPath, "--dry-run"}, system.DetectionResult{})
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}
	if got := install.Selection.Persona; got != "neutral" {
		t.Fatalf("install persona = %q, want config selection", got)
	}

	originalHome := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = originalHome })

	sync, err := RunSync([]string{"--config", configPath, "--dry-run"})
	if err != nil {
		t.Fatalf("RunSync() error = %v", err)
	}
	if got := sync.Selection.Persona; got != "neutral" {
		t.Fatalf("sync persona = %q, want config selection", got)
	}
}

func TestConfigExportReportsLegacyLoss(t *testing.T) {
	for _, test := range []struct {
		name  string
		state state.InstallState
		codes []string
	}{
		{"operational", state.InstallState{InstalledAgents: []string{"opencode"}, SelectionConfigured: true, ManagedAssetDigest: "runtime-digest", InstalledBinaryVersion: "v2"}, []string{"config.export.loss.legacy-operational"}},
		{"ambiguous intent", state.InstallState{InstalledAgents: []string{"opencode"}}, []string{"config.export.loss.legacy-operational", "config.export.loss.ambiguous-intent"}},
		{"user owned", state.InstallState{InstalledAgents: []string{"opencode"}, SelectionConfigured: true, RDDMode: "off"}, []string{"config.export.loss.legacy-operational", "config.export.loss.user-owned"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if err := state.Write(home, test.state); err != nil {
				t.Fatal(err)
			}

			var output bytes.Buffer
			if err := RunConfig([]string{"export", "--home", home}, &output); err != nil {
				t.Fatalf("RunConfig(export) error = %v", err)
			}
			for _, code := range test.codes {
				if !strings.Contains(output.String(), code) {
					t.Fatalf("export = %s, want %s", output.String(), code)
				}
			}
			if !strings.Contains(output.String(), `"lossless": false`) {
				t.Fatalf("export = %s, want lossless false", output.String())
			}
		})
	}
}

func writeDesiredConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "desired.json")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
