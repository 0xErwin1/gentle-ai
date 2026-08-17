package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configdomain "github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/desiredstate"
	"github.com/gentleman-programming/gentle-ai/v2/internal/render"
)

func TestConfigApplyAndReconcilePersistManagedState(t *testing.T) {
	home, destination := t.TempDir(), t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")

	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]},"roles":[{"id":"writer","renderedName":"writer-v1"},{"id":"reviewer","references":["writer"]}]}`)
	runConfigMutation(t, "apply", configPath, home, destination)

	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]},"roles":[{"id":"writer","renderedName":"writer-v2"},{"id":"reviewer","references":["writer"]}]}`)
	runConfigMutation(t, "reconcile", configPath, home, destination)

	settings, err := os.ReadFile(filepath.Join(destination, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(settings), "writer-v1") || !strings.Contains(string(settings), "writer-v2") {
		t.Fatalf("reconciled settings = %s, want only the renamed target", settings)
	}

	desired, err := desiredstate.ReadDesired(home)
	if err != nil || desired.Roles[0].RenderedName != "writer-v2" {
		t.Fatalf("persisted desired = %#v, %v", desired, err)
	}
	// The manifest also covers whatever tree the declared components stage, so
	// its size tracks the preset rather than this document. What this test pins
	// is the ownership the rename produced: the renamed agent is owned and the
	// old name is gone.
	manifest, err := desiredstate.ReadManifest(home, destination)
	if err != nil {
		t.Fatalf("read persisted manifest: %v", err)
	}
	owned := map[string]bool{}
	for _, resource := range manifest.Resources {
		if resource.Path == ".config/opencode/opencode.json" {
			owned[resource.Selector] = true
		}
	}
	if !owned["/agent/writer-v2"] || !owned["/agent/reviewer"] || owned["/agent/writer-v1"] {
		t.Fatalf("persisted settings ownership = %v, want the renamed target and no stale name", owned)
	}
}

func TestConfigApplyRejectsInvalidInputAndRollsBackPersistenceFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		config     string
		prepare    func(t *testing.T, destination string)
		persistErr bool
		wantErr    string
		wantOutput string
	}{
		{
			name:       "unknown field leaves user content untouched",
			config:     `{"version":"v1","selection":{"agents":["opencode"],"unknown":true}}`,
			prepare:    writeUserSettings,
			wantOutput: "config.document.unknown-field",
		},
		{
			name:       "persistence failure restores applied file",
			config:     `{"version":"v1","selection":{"agents":["opencode"]}}`,
			persistErr: true,
			wantErr:    "persist reconciliation",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, destination := t.TempDir(), t.TempDir()
			configPath := filepath.Join(t.TempDir(), "desired.json")
			writeConfigDocument(t, configPath, test.config)
			if test.prepare != nil {
				test.prepare(t, destination)
			}
			if test.persistErr {
				original := writeConfigState
				writeConfigState = func(string, string, configdomain.DesiredState, render.Manifest) error {
					return errors.New("store unavailable")
				}
				t.Cleanup(func() { writeConfigState = original })
			}

			var output bytes.Buffer
			err := RunConfig([]string{"apply", "--config", configPath, "--home", home, "--destination", destination, "--stage", t.TempDir()}, &output)
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("RunConfig(apply) error = %v, want %q", err, test.wantErr)
			}
			// A rejected document reports its diagnostics and then fails, so the
			// output is asserted on a run that also returns an error.
			if test.wantOutput != "" && !strings.Contains(output.String(), test.wantOutput) {
				t.Fatalf("RunConfig(apply) output/error = %s/%v, want %q", output.String(), err, test.wantOutput)
			}

			settingsPath := filepath.Join(destination, ".config", "opencode", "opencode.json")
			settings, readErr := os.ReadFile(settingsPath)
			if test.wantOutput != "" && (readErr != nil || string(settings) != `{"theme":"user"}`) {
				t.Fatalf("invalid input changed destination = %q, %v", settings, readErr)
			}
			if test.wantErr != "" && !os.IsNotExist(readErr) {
				t.Fatalf("rollback left destination = %q, %v", settings, readErr)
			}
		})
	}
}

func runConfigMutation(t *testing.T, operation, configPath, home, destination string) {
	t.Helper()
	var output bytes.Buffer
	if err := RunConfig([]string{operation, "--config", configPath, "--home", home, "--destination", destination, "--stage", t.TempDir()}, &output); err != nil {
		t.Fatalf("RunConfig(%s) error = %v", operation, err)
	}
}

func writeConfigDocument(t *testing.T, path, document string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeUserSettings(t *testing.T, destination string) {
	t.Helper()
	path := filepath.Join(destination, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"user"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}
