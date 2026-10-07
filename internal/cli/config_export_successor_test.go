package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/desiredstate"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/state"
)

func writeDesiredState(t *testing.T, home string, document []byte) {
	t.Helper()
	desired, diagnostics := decodeDesiredState(document)
	if len(diagnostics) != 0 {
		t.Fatalf("decode = %v, want no diagnostics", diagnostics)
	}
	if err := desiredstate.WriteDesired(home, desired); err != nil {
		t.Fatal(err)
	}
}

// The Codex service tier is a runtime-request choice: install state records
// the tier written to config.toml, and the declarative document cannot carry
// it. Export must say so instead of dropping the tier silently.
func TestConfigExportReportsPersistedCodexServiceTierLoss(t *testing.T) {
	home := t.TempDir()
	writeDesiredState(t, home, []byte(`{"version":"v1","selection":{"agents":["codex"]}}`))
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{"codex"}, SelectionConfigured: true, CodexServiceTier: "priority"}); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(filepath.Join(home, ".gentle-ai", "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	first := new(bytes.Buffer)
	if err := RunConfig([]string{"export", "--home", home}, first); err != nil {
		t.Fatal(err)
	}
	second := new(bytes.Buffer)
	if err := RunConfig([]string{"export", "--home", home}, second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("repeated export bytes differ\nfirst=%s\nsecond=%s", first.Bytes(), second.Bytes())
	}
	stateAfter, err := os.ReadFile(filepath.Join(home, ".gentle-ai", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Fatal("export rewrote install state")
	}

	var result struct {
		Document    json.RawMessage `json:"document"`
		Diagnostics []struct {
			Code    string `json:"code"`
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"diagnostics"`
		Lossless bool `json:"lossless"`
	}
	if err := json.Unmarshal(first.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(result.Diagnostics))
	for index, diagnostic := range result.Diagnostics {
		got[index] = diagnostic.Code + "|" + diagnostic.Path + "|" + diagnostic.Message
	}
	want := []string{
		"config.export.loss.codex-service-tier|$.codexServiceTier|persisted Codex service tier \"priority\" cannot be represented in the exported document; reconfigure it through gentle-ai's model picker",
	}
	if !equalStrings(got, want) {
		t.Fatalf("diagnostics = %v, want %v", got, want)
	}
	if result.Lossless {
		t.Fatal("export reported itself lossless while dropping the Codex service tier")
	}
	if bytes.Contains(result.Document, []byte("codexServiceTier")) {
		t.Fatal("exported document carries the Codex service tier")
	}
}

// An absent or standard tier is the default; reporting it would be spurious
// loss on every export.
func TestConfigExportWithoutTierChoiceStaysLossless(t *testing.T) {
	home := t.TempDir()
	writeDesiredState(t, home, []byte(`{"version":"v1","selection":{"agents":["codex"]}}`))
	if err := state.Write(home, state.InstallState{InstalledAgents: []string{"codex"}, SelectionConfigured: true}); err != nil {
		t.Fatal(err)
	}

	output := new(bytes.Buffer)
	if err := RunConfig([]string{"export", "--home", home}, output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
		Lossless bool `json:"lossless"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want none", result.Diagnostics)
	}
	if !result.Lossless {
		t.Fatal("export of a default tier reported itself lossy")
	}
}

func TestConfigExportLegacyReportsValueSpecificLossesDeterministically(t *testing.T) {
	home := t.TempDir()
	legacy := state.InstallState{
		InstalledAgents:          []string{"opencode"},
		SelectionConfigured:      true,
		CommunityTools:           []string{"codegraph"},
		CommunityToolsConfigured: true,
		ModelAssignments: map[string]state.ModelAssignmentState{
			"sdd-apply": {ProviderID: "anthropic", ModelID: "claude-sonnet", Effort: "high"},
		},
		BackgroundIntent: model.OpenCodeBackgroundOn,
	}
	if err := state.Write(home, legacy); err != nil {
		t.Fatal(err)
	}

	first := new(bytes.Buffer)
	if err := RunConfig([]string{"export", "--home", home}, first); err != nil {
		t.Fatal(err)
	}
	second := new(bytes.Buffer)
	if err := RunConfig([]string{"export", "--home", home}, second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("repeated export bytes differ\nfirst=%s\nsecond=%s", first.Bytes(), second.Bytes())
	}

	var result struct {
		Document struct {
			Selection struct {
				Agents    []string `json:"agents"`
				Providers map[string]struct {
					BackgroundIntent string `json:"backgroundIntent"`
				} `json:"providers"`
			} `json:"selection"`
		} `json:"document"`
		Diagnostics []struct {
			Code    string `json:"code"`
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(first.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if got, want := result.Document.Selection.Agents, []string{"opencode"}; !equalStrings(got, want) {
		t.Fatalf("exported agents = %v, want %v", got, want)
	}
	if got, want := result.Document.Selection.Providers["opencode"].BackgroundIntent, "on"; got != want {
		t.Fatalf("exported backgroundIntent = %q, want %q", got, want)
	}

	got := make([]string, len(result.Diagnostics))
	for index, diagnostic := range result.Diagnostics {
		got[index] = diagnostic.Code + "|" + diagnostic.Path + "|" + diagnostic.Message
	}
	want := []string{
		"config.export.loss.community-tool|$.community_tools[0]|legacy community tool \"codegraph\" cannot be represented; rerun gentle-ai install and select \"codegraph\"",
		"config.export.loss.model-assignment|$.model_assignments.sdd-apply|legacy model assignment \"sdd-apply=anthropic/claude-sonnet@high\" cannot be represented; reconfigure it through gentle-ai's model picker",
		"config.export.loss.legacy-operational|$|legacy install state omits runtime and provenance fields from desired configuration",
	}
	if !equalStrings(got, want) {
		t.Fatalf("diagnostics = %v, want %v", got, want)
	}
}

func TestConfigExportLegacySortsMultipleUnrepresentableValues(t *testing.T) {
	home := t.TempDir()
	legacy := state.InstallState{
		InstalledAgents:          []string{"opencode"},
		SelectionConfigured:      true,
		CommunityTools:           []string{"zeta-tool", "codegraph"},
		CommunityToolsConfigured: true,
		ModelAssignments: map[string]state.ModelAssignmentState{
			"sdd-verify": {ProviderID: "openai", ModelID: "gpt-5.6"},
			"sdd-apply":  {ProviderID: "anthropic", ModelID: "claude-sonnet", Effort: "high"},
		},
	}
	if err := state.Write(home, legacy); err != nil {
		t.Fatal(err)
	}

	output := new(bytes.Buffer)
	if err := RunConfig([]string{"export", "--home", home}, output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Diagnostics []struct {
			Code    string `json:"code"`
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(result.Diagnostics))
	for index, diagnostic := range result.Diagnostics {
		got[index] = diagnostic.Code + "|" + diagnostic.Path + "|" + diagnostic.Message
	}
	want := []string{
		"config.export.loss.community-tool|$.community_tools[0]|legacy community tool \"codegraph\" cannot be represented; rerun gentle-ai install and select \"codegraph\"",
		"config.export.loss.community-tool|$.community_tools[1]|legacy community tool \"zeta-tool\" cannot be represented; rerun gentle-ai install and select \"zeta-tool\"",
		"config.export.loss.model-assignment|$.model_assignments.sdd-apply|legacy model assignment \"sdd-apply=anthropic/claude-sonnet@high\" cannot be represented; reconfigure it through gentle-ai's model picker",
		"config.export.loss.model-assignment|$.model_assignments.sdd-verify|legacy model assignment \"sdd-verify=openai/gpt-5.6\" cannot be represented; reconfigure it through gentle-ai's model picker",
		"config.export.loss.legacy-operational|$|legacy install state omits runtime and provenance fields from desired configuration",
	}
	if !equalStrings(got, want) {
		t.Fatalf("diagnostics = %v, want %v", got, want)
	}
}

// A legacy home (no desired document) reports the tier loss in the same
// deterministic value-specific position.
func TestConfigExportLegacyReportsPersistedCodexServiceTierLoss(t *testing.T) {
	home := t.TempDir()
	legacy := state.InstallState{
		InstalledAgents:     []string{"opencode"},
		SelectionConfigured: true,
		CodexServiceTier:    "priority",
	}
	if err := state.Write(home, legacy); err != nil {
		t.Fatal(err)
	}

	output := new(bytes.Buffer)
	if err := RunConfig([]string{"export", "--home", home}, output); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
		Lossless bool `json:"lossless"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	codes := make([]string, len(result.Diagnostics))
	for index, diagnostic := range result.Diagnostics {
		codes[index] = diagnostic.Code
	}
	want := []string{
		"config.export.loss.codex-service-tier",
		"config.export.loss.legacy-operational",
	}
	if !equalStrings(codes, want) {
		t.Fatalf("diagnostics = %v, want %v", codes, want)
	}
	if result.Lossless {
		t.Fatal("legacy export reported itself lossless while dropping the Codex service tier")
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
