package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configdomain "github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/render"
)

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
