package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigApplyAndReconcileRequireOperationSpecificHomeResolution(t *testing.T) {
	for _, operation := range []string{"apply", "reconcile"} {
		t.Run(operation, func(t *testing.T) {
			destination := t.TempDir()
			configPath := filepath.Join(t.TempDir(), "desired.json")
			writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]}}`)

			settingsPath := filepath.Join(destination, ".config", "opencode", "opencode.json")
			if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
				t.Fatal(err)
			}
			const original = `{"theme":"user"}`
			if err := os.WriteFile(settingsPath, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}

			var output bytes.Buffer
			err := RunConfig([]string{operation, "--config", configPath, "--destination", destination, "--stage", t.TempDir()}, &output)
			want := "config " + operation + " requires --home for persisted desired state; run gentle-ai config " + operation + " --config <path> --home <path> --destination <path> --stage <path>"
			if err == nil || err.Error() != want {
				t.Fatalf("RunConfig(%s) error = %v, want %q", operation, err, want)
			}
			if strings.Contains(err.Error(), destination) || strings.Contains(err.Error(), configPath) {
				t.Fatalf("refusal exposes a real path: %q", err)
			}
			if output.Len() != 0 {
				t.Fatalf("RunConfig(%s) wrote stdout = %q, want empty", operation, output.String())
			}

			contents, readErr := os.ReadFile(settingsPath)
			if readErr != nil || string(contents) != original {
				t.Fatalf("missing-home refusal changed destination = %q, %v", contents, readErr)
			}
		})
	}
}
