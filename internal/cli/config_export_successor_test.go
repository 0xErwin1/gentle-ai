package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
)

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
				Agents           []string `json:"agents"`
				BackgroundIntent string   `json:"backgroundIntent"`
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
	if got, want := result.Document.Selection.BackgroundIntent, "on"; got != want {
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
