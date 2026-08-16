package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigExistingUserOpenCodePreservation(t *testing.T) {
	home, destination := t.TempDir(), t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]},"roles":[{"id":"writer","renderedName":"writer-v1"},{"id":"reviewer","references":["writer"]}]}`)

	path := filepath.Join(destination, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"user","agent":{"personal":{"references":[]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	runConfigMutation(t, "apply", configPath, home, destination)

	settings := readOpenCodeSettings(t, path)
	if settings["theme"] != "user" {
		t.Fatalf("theme = %#v, want user", settings["theme"])
	}
	agents := settings["agent"].(map[string]any)
	if _, ok := agents["personal"]; !ok {
		t.Fatalf("user agent removed: %#v", agents)
	}
	if _, ok := agents["writer-v1"]; !ok {
		t.Fatalf("managed writer missing: %#v", agents)
	}
}

func TestConfigReconcileIsDeterministic(t *testing.T) {
	home, destination := t.TempDir(), t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]},"roles":[{"id":"writer","renderedName":"writer"}]}`)

	runConfigMutation(t, "apply", configPath, home, destination)
	path := filepath.Join(destination, ".config", "opencode", "opencode.json")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	runConfigMutation(t, "reconcile", configPath, home, destination)
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("reconcile changed stable settings\nfirst=%s\nsecond=%s", first, second)
	}
}

func TestConfigRenameUsesExactParsedTarget(t *testing.T) {
	home, destination := t.TempDir(), t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]},"roles":[{"id":"writer","renderedName":"writer-v1"},{"id":"reviewer","references":["writer"]}]}`)
	runConfigMutation(t, "apply", configPath, home, destination)

	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]},"roles":[{"id":"writer","renderedName":"writer-v2"},{"id":"reviewer","references":["writer"]}]}`)
	runConfigMutation(t, "reconcile", configPath, home, destination)

	settings := readOpenCodeSettings(t, filepath.Join(destination, ".config", "opencode", "opencode.json"))
	agents := settings["agent"].(map[string]any)
	if _, ok := agents["writer-v1"]; ok {
		t.Fatalf("old writer remains: %#v", agents)
	}
	reviewer := agents["reviewer"].(map[string]any)
	if got, want := reviewer["references"], []any{"writer-v2"}; !equalJSON(got, want) {
		t.Fatalf("reviewer references = %#v, want %#v", got, want)
	}
}

func readOpenCodeSettings(t *testing.T, path string) map[string]any {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(contents, &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func equalJSON(left, right any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}
