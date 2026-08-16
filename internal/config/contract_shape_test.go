package config

import (
	"encoding/json"
	"fmt"
	"testing"
	"unicode"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// The document is a versioned public schema, so every key a user writes must be
// one this package names deliberately. An untagged Go type reaching the encoder
// publishes its identifiers instead, which turns any later rename into a silent
// breaking change for every configuration file in existence.
func TestPublicDocumentKeysAreContractNames(t *testing.T) {
	document := Document{
		Version: CurrentVersion,
		Selection: Selection{
			Agents:             []model.AgentID{model.AgentOpenCode},
			Components:         []model.ComponentID{model.ComponentEngram},
			Skills:             []model.SkillID{model.SkillSDDApply},
			Persona:            model.PersonaGentleman,
			Preset:             model.PresetFullGentleman,
			SDDMode:            model.SDDModeSingle,
			SDDProfileStrategy: model.SDDProfileStrategyGeneratedMulti,
			StrictTDD:          true,
			BackgroundIntent:   model.OpenCodeBackgroundOn,
			Profiles: []Profile{{
				Name:             "cheap",
				Orchestrator:     ModelAssignment{Provider: "anthropic", Model: "claude-haiku", Effort: "low"},
				PhaseAssignments: map[string]ModelAssignment{"sdd-apply": {Provider: "anthropic", Model: "claude-sonnet"}},
			}},
		},
		Roles: []Role{{ID: "reviewer", RenderedName: "code-reviewer", References: []RoleRef{"reviewer"}}},
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}

	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode document: %v", err)
	}

	for _, key := range objectKeys(decoded, "$") {
		first := []rune(key.name)[0]
		if unicode.IsUpper(first) {
			t.Errorf("%s publishes %q, a Go identifier; give its type an explicit json tag", key.path, key.name)
		}
	}
}

// A profile written with contract names must survive the round trip through the
// internal model, which uses different identifiers for the same intent.
func TestProfileSurvivesModelRoundTrip(t *testing.T) {
	document := `{"version":"v1","selection":{"profiles":[{"name":"cheap","orchestrator":{"provider":"anthropic","model":"claude-haiku","effort":"low"},"phaseAssignments":{"sdd-apply":{"provider":"anthropic","model":"claude-sonnet","effort":"high"}}}]}}`

	state, diagnostics := Decode([]byte(document))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	restored := FromSelection(Project(state))

	if len(restored.Selection.Profiles) != 1 {
		t.Fatalf("profiles = %d, want 1", len(restored.Selection.Profiles))
	}

	profile := restored.Selection.Profiles[0]
	if profile.Name != "cheap" {
		t.Errorf("Name = %q, want %q", profile.Name, "cheap")
	}
	if profile.Orchestrator != (ModelAssignment{Provider: "anthropic", Model: "claude-haiku", Effort: "low"}) {
		t.Errorf("Orchestrator = %+v", profile.Orchestrator)
	}
	if got, want := profile.PhaseAssignments["sdd-apply"], (ModelAssignment{Provider: "anthropic", Model: "claude-sonnet", Effort: "high"}); got != want {
		t.Errorf("PhaseAssignments[sdd-apply] = %+v, want %+v", got, want)
	}
}

// The historical capitalised spelling is not a supported contract name, so a
// document written against the leaked identifiers must be rejected rather than
// silently accepted alongside the real one.
func TestLeakedGoIdentifiersAreRejected(t *testing.T) {
	document := `{"version":"v1","selection":{"profiles":[{"Name":"cheap","OrchestratorModel":{"ProviderID":"anthropic","ModelID":"claude-haiku"}}]}}`

	_, diagnostics := Decode([]byte(document))

	if len(diagnostics) == 0 {
		t.Fatal("expected the leaked Go spelling to be rejected")
	}
	if diagnostics[0].Code != "config.document.unknown-field" {
		t.Errorf("code = %q, want %q", diagnostics[0].Code, "config.document.unknown-field")
	}
}

type documentKey struct {
	path string
	name string
}

func objectKeys(value any, path string) []documentKey {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]documentKey, 0, len(typed))
		for name, nested := range typed {
			keys = append(keys, documentKey{path: path, name: name})
			keys = append(keys, objectKeys(nested, fmt.Sprintf("%s.%s", path, name))...)
		}
		return keys

	case []any:
		keys := make([]documentKey, 0)
		for index, nested := range typed {
			keys = append(keys, objectKeys(nested, fmt.Sprintf("%s[%d]", path, index))...)
		}
		return keys

	default:
		return nil
	}
}
