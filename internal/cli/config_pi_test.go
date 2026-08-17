package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi reads its persona from a small runtime config rather than from the prompt
// file every other adapter appends to, so the shared persona injection writes
// nothing for it. The imperative installer knows this and branches; the
// renderer did not, which made a declared persona vanish for a Pi installation
// without a diagnostic to say so.
func TestRenderWritesThePiPersonaConfig(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["pi"],"components":["persona"],"persona":"neutral"}}`
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	assertConfigOutput(t, []string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, `"operation": "render"`)

	personaPath := filepath.Join(stage, ".pi", "gentle-ai", "persona.json")
	content, err := os.ReadFile(personaPath)
	if err != nil {
		t.Fatalf("read staged Pi persona: %v (staged %v)", err, stagedFiles(t, stage))
	}

	var config struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("decode Pi persona: %v\n%s", err, content)
	}
	if config.Mode != "neutral" {
		t.Errorf("mode = %q, want %q", config.Mode, "neutral")
	}
}

// A custom persona is the operator's own, so Gentle AI writes no Pi persona
// config for it. Staging one would hand gentle-pi a mode the operator never
// chose, and it would do so on every rebuild.
func TestRenderLeavesACustomPiPersonaAlone(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["pi"],"components":["persona"],"persona":"custom"}}`
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	assertConfigOutput(t, []string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, `"operation": "render"`)

	for _, path := range stagedFiles(t, stage) {
		if strings.Contains(path, filepath.Join("gentle-ai", "persona.json")) {
			t.Errorf("a custom persona staged %q", path)
		}
	}
}
