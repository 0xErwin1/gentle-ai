package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
)

type presetsResult struct {
	Schema    string `json:"schema"`
	Providers map[string]struct {
		Presets map[string]map[string]struct {
			Model  string `json:"model"`
			Effort string `json:"effort"`
		} `json:"presets"`
	} `json:"providers"`
}

func runConfigPresets(t *testing.T, args ...string) presetsResult {
	t.Helper()

	var output bytes.Buffer
	if err := RunConfig(append([]string{"presets"}, args...), &output); err != nil {
		t.Fatalf("config presets: %v\n%s", err, output.String())
	}

	var result presetsResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode presets output: %v\n%s", err, output.String())
	}
	return result
}

// config presets reads no document: it prints gentle-ai's own preset tables
// verbatim, so it must round-trip the Go tables it is built from rather than
// a second, hand-maintained copy of them.
func TestConfigPresetsRoundTripsTheGoTables(t *testing.T) {
	result := runConfigPresets(t)

	if result.Schema != "gentle-ai.model-presets/v1" {
		t.Fatalf("schema = %q", result.Schema)
	}

	for _, provider := range []string{"claude-code", "codex", "kiro-ide"} {
		if _, ok := result.Providers[provider]; !ok {
			t.Errorf("providers is missing %q", provider)
		}
	}
	if _, ok := result.Providers["pi"]; ok {
		t.Error("providers carries pi, which has no gentle-ai model preset table")
	}

	claude := result.Providers["claude-code"].Presets
	for phase, alias := range model.ClaudeModelPresetBalanced() {
		got, ok := claude["balanced"][phase]
		if !ok {
			t.Fatalf("claude-code balanced is missing phase %q", phase)
		}
		if got.Model != string(alias) {
			t.Errorf("claude-code balanced[%s] = %q, want %q", phase, got.Model, alias)
		}
	}

	kiro := result.Providers["kiro-ide"].Presets
	for phase, alias := range model.KiroModelPresetEconomy() {
		got, ok := kiro["economy"][phase]
		if !ok {
			t.Fatalf("kiro-ide economy is missing phase %q", phase)
		}
		if got.Model != string(alias) {
			t.Errorf("kiro-ide economy[%s] = %q, want %q", phase, got.Model, alias)
		}
	}

	codex := result.Providers["codex"].Presets
	for phase, effort := range model.CodexModelPresetRecommended() {
		got, ok := codex["recommended"][phase]
		if !ok {
			t.Fatalf("codex recommended is missing phase %q", phase)
		}
		if got.Effort != string(effort) {
			t.Errorf("codex recommended[%s].effort = %q, want %q", phase, got.Effort, effort)
		}
	}
	orchestrator := model.CodexPresetOrchestratorAssignment(string(model.CodexPresetRecommended))
	if got := codex["recommended"]["orchestrator"]; got.Model != orchestrator.Model || got.Effort != string(orchestrator.Effort) {
		t.Errorf("codex recommended[orchestrator] = %+v, want {%s %s}", got, orchestrator.Model, orchestrator.Effort)
	}
}

// Every preset name the interactive picker offers must appear in the printed
// table, or the CLI and the TUI would silently disagree about what a
// provider's presets are named.
func TestConfigPresetsCoversEveryOfferedPresetName(t *testing.T) {
	result := runConfigPresets(t)

	cases := map[string][]string{
		"claude-code": {
			string(model.ClaudePresetBalanced), string(model.ClaudePresetPerformance),
			string(model.ClaudePresetEconomy), string(model.ClaudePresetDiversity),
		},
		"kiro-ide": {
			string(model.KiroPresetBalanced), string(model.KiroPresetPerformance),
			string(model.KiroPresetEconomy), string(model.KiroPresetOpenWeight),
		},
		"codex": {
			string(model.CodexPresetLowCost), string(model.CodexPresetRecommended),
			string(model.CodexPresetPowerful),
		},
	}

	for provider, names := range cases {
		presets := result.Providers[provider].Presets
		for _, name := range names {
			if _, ok := presets[name]; !ok {
				t.Errorf("%s is missing preset %q", provider, name)
			}
		}
	}
}

// --provider filters the document down to exactly the requested provider.
func TestConfigPresetsFiltersByProvider(t *testing.T) {
	result := runConfigPresets(t, "--provider", "codex")

	if len(result.Providers) != 1 {
		t.Fatalf("providers = %v, want exactly codex", result.Providers)
	}
	if _, ok := result.Providers["codex"]; !ok {
		t.Fatalf("providers = %v, want codex", result.Providers)
	}
}

// An unsupported provider is refused rather than silently returning nothing.
func TestConfigPresetsRefusesAnUnsupportedProvider(t *testing.T) {
	var output bytes.Buffer
	err := RunConfig([]string{"presets", "--provider", "pi"}, &output)
	if err == nil {
		t.Fatalf("expected an error, got output: %s", output.String())
	}
}

// The document's output is deterministic across repeated calls: no map
// iteration is allowed to leak into the printed order.
func TestConfigPresetsOutputIsDeterministic(t *testing.T) {
	var first, second bytes.Buffer
	if err := RunConfig([]string{"presets", "--json"}, &first); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := RunConfig([]string{"presets", "--json"}, &second); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first.String() != second.String() {
		t.Fatalf("output is not deterministic:\n%s\n---\n%s", first.String(), second.String())
	}
}
