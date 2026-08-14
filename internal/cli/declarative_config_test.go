package cli

import (
	"bytes"
	"encoding/json"
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
	home := t.TempDir()
	if err := state.Write(home, state.InstallState{
		InstalledAgents:        []string{"opencode"},
		SelectionConfigured:    true,
		ManagedAssetDigest:     "runtime-digest",
		InstalledBinaryVersion: "v2",
	}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := RunConfig([]string{"export", "--home", home}, &output); err != nil {
		t.Fatalf("RunConfig(export) error = %v", err)
	}

	var result struct {
		Lossless    bool `json:"lossless"`
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Lossless || len(result.Diagnostics) == 0 || result.Diagnostics[0].Code != "config.export.loss.legacy-operational" {
		t.Fatalf("export = %s, want loss diagnostic", output.String())
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
