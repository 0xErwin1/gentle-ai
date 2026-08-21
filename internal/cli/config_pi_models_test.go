package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func renderPiModels(t *testing.T, selection string) map[string]struct {
	Model    string `json:"model"`
	Thinking string `json:"thinking"`
} {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":"v1","selection":`+selection+`}`), 0o644); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	assertConfigOutput(t, []string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, `"operation": "render"`)

	content, err := os.ReadFile(filepath.Join(stage, ".pi", "gentle-ai", "models.json"))
	if err != nil {
		t.Fatalf("read staged Pi models: %v (staged %v)", err, stagedFiles(t, stage))
	}

	var routing map[string]struct {
		Model    string `json:"model"`
		Thinking string `json:"thinking"`
	}
	if err := json.Unmarshal(content, &routing); err != nil {
		t.Fatalf("decode Pi models: %v\n%s", err, content)
	}

	return routing
}

// Naming a profile is the point: an operator who has to assign every agent by
// hand to get a working routing has not been given a profile at all.
func TestRenderExpandsThePiModelPreset(t *testing.T) {
	routing := renderPiModels(t, `{"agents":["pi"],"modelPresets":{"pi":"low-cost"}}`)

	if len(routing) == 0 {
		t.Fatalf("the profile expanded to nothing")
	}
	for agent, entry := range routing {
		if entry.Thinking == "" {
			t.Errorf("%s carries no reasoning level", agent)
		}
	}
}

// A profile is a starting point, not a ceiling. An assignment the document made
// itself wins over the one the profile would have given that agent, and leaves
// every other agent on the profile.
func TestRenderLetsAPiAssignmentOverrideTheProfile(t *testing.T) {
	routing := renderPiModels(t, `{"agents":["pi"],"modelPresets":{"pi":"low-cost"},"piModelAssignments":{"sdd-apply":{"model":"openai-codex/gpt-5.6-sol","thinking":"max"}}}`)

	if got := routing["sdd-apply"].Model; got != "openai-codex/gpt-5.6-sol" {
		t.Errorf("sdd-apply model = %q, want the declared one", got)
	}
	if got := routing["sdd-apply"].Thinking; got != "max" {
		t.Errorf("sdd-apply thinking = %q, want max", got)
	}
	if len(routing) < 2 {
		t.Errorf("overriding one agent dropped the rest of the profile: %v", routing)
	}
}

// Without a profile the document is the whole routing, and a document that
// mentions no models writes no file rather than an empty one.
func TestRenderWritesOnlyTheDeclaredPiAssignments(t *testing.T) {
	routing := renderPiModels(t, `{"agents":["pi"],"piModelAssignments":{"sdd-explore":{"model":"moonshotai/kimi-k3"}}}`)

	if len(routing) != 1 || routing["sdd-explore"].Model != "moonshotai/kimi-k3" {
		t.Errorf("routing = %v, want only the declared assignment", routing)
	}
}

// gentle-pi drops an entry it cannot read without saying so, which turns a typo
// into an agent quietly running on the wrong model. The contract is the last
// place that can still name it.
func TestConfigRefusesAnInvalidPiRouting(t *testing.T) {
	for name, document := range map[string]string{
		"unknown level": `{"version":"v1","selection":{"agents":["pi"],"piModelAssignments":{"sdd-apply":{"thinking":"ludicrous"}}}}`,
		"unsafe model":  `{"version":"v1","selection":{"agents":["pi"],"piModelAssignments":{"sdd-apply":{"model":"gpt 5 with spaces"}}}}`,
		"empty entry":   `{"version":"v1","selection":{"agents":["pi"],"piModelAssignments":{"sdd-apply":{}}}}`,
	} {
		configPath := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
			t.Fatal(err)
		}

		output := new(bytes.Buffer)
		err := RunConfig([]string{"validate", "--config", configPath}, output)
		if err == nil {
			t.Errorf("%s was accepted:\n%s", name, output)
			continue
		}
		if !strings.Contains(output.String(), "config.pi-model") {
			t.Errorf("%s was refused without naming the field:\n%s", name, output)
		}
	}
}
