package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// The document is a versioned public schema, so every key a user writes must be
// one this package names deliberately. An untagged Go type reaching the encoder
// publishes its identifiers instead, which turns any later rename into a silent
// breaking change for every configuration file in existence.
// The fixture below must stay fully populated: this guard can only inspect keys
// that something actually encoded, so a field left out of it is a field nobody
// checks. TestEveryContractFieldIsPopulated keeps that honest.
func TestPublicDocumentKeysAreContractNames(t *testing.T) {
	document := fullyPopulatedDocument()

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}

	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode document: %v", err)
	}

	// A map keyed by the operator holds names this package never chose: an
	// environment variable, a phase, a server. Judging those by the schema's
	// naming rule would fail a document for spelling its own key in capitals.
	for _, key := range objectKeys(decoded, "$") {
		if userKeyed(key.path) {
			continue
		}
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
	if profile.Orchestrator == nil || *profile.Orchestrator != (ModelAssignment{Provider: "anthropic", Model: "claude-haiku", Effort: "low"}) {
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

var userKeyedContainers = []string{
	"mcpServers", "env", "modelAssignments", "claudeModelAssignments",
	"kiroModelAssignments", "codexModelAssignments", "codexCarrilModelAssignments",
	"codexPhaseModelAssignments", "claudePhaseAssignments", "phaseAssignments",
	"skillAssignments", "extensions",
}

func userKeyed(path string) bool {
	segments := strings.Split(path, ".")
	for index := len(segments) - 1; index >= 0; index-- {
		segment := strings.Split(segments[index], "[")[0]
		for _, container := range userKeyedContainers {
			if segment == container {
				return index == len(segments)-1
			}
		}
	}

	return false
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

// fullyPopulatedDocument is the single fixture both shape guards inspect. It has
// to exercise every contract field, which the guard below enforces.
func fullyPopulatedDocument() Document {
	hidden := true

	return Document{
		Version: CurrentVersion,
		Selection: Selection{
			Agents:             []model.AgentID{model.AgentOpenCode},
			Components:         []model.ComponentID{model.ComponentEngram},
			Skills:             []model.SkillID{model.SkillSDDApply},
			SkillExclusions:    []model.SkillID{model.SkillGoTesting},
			CodexModelPreset:   string(model.CodexPresetRecommended),
			Persona:            model.PersonaGentleman,
			Preset:             model.PresetFullGentleman,
			SDDMode:            model.SDDModeSingle,
			SDDProfileStrategy: model.SDDProfileStrategyGeneratedMulti,
			StrictTDD:          true,
			BackgroundIntent:   model.OpenCodeBackgroundOn,
			PiBackgroundIntent: model.PiBackgroundOn,
			Scope:              model.InstallScopeWorkspace,
			Channel:            model.InstallChannelBeta,
			RDDMode:            model.RDDModeOn,
			CommunityTools:     []model.CommunityToolID{model.CommunityToolCodeGraph},
			OpenCodePlugins:    []model.OpenCodeCommunityPluginID{model.OpenCodePluginGentleLogo},
			ModelAssignments:   map[string]ModelAssignment{"sdd-apply": {Provider: "anthropic", Model: "claude-sonnet"}},

			ClaudeModelAssignments:      map[string]model.ClaudeModelAlias{"sdd-apply": model.ClaudeModelOpus},
			KiroModelAssignments:        map[string]model.KiroModelAlias{"sdd-apply": model.KiroModelDeepSeek},
			CodexModelAssignments:       map[string]model.CodexEffort{"sdd-apply": model.CodexEffortHigh},
			CodexCarrilModelAssignments: map[string]string{"sdd-mid": "gpt-5.6-luna"},
			CodexPhaseModelAssignments:  map[string]string{"sdd-apply": "gpt-5.6-sol"},
			ClaudePhaseAssignments:      map[string]ClaudePhaseAssignment{"sdd-apply": {Model: "opus", Effort: "high"}},
			CodexOrchestrator:           &CodexOrchestratorAssignment{Model: "gpt-5.6-sol", Effort: "medium"},
			MCPServers:                  map[string]MCPServer{"atlas": {Command: "atlas", Args: []string{"mcp"}, Env: map[string]string{"TOKEN": "x"}}},
			Permissions:                 &Permissions{Allow: []string{"Read(*)"}, Deny: []string{"Bash(curl *)"}, Ask: []string{"Edit(*.tf)"}},
			SkillAssignments:            map[string][]model.SkillID{"opencode": {model.SkillSDDApply}},
			Profiles: []Profile{{
				Name:             "cheap",
				Orchestrator:     &ModelAssignment{Provider: "anthropic", Model: "claude-haiku", Effort: "low"},
				PhaseAssignments: map[string]ModelAssignment{"sdd-apply": {Provider: "anthropic", Model: "claude-sonnet"}},
			}},
		},
		Roles: []Role{{
			ID: "reviewer", RenderedName: "code-reviewer", References: []RoleRef{"reviewer"},
			Description: "reviews", Prompt: "you review", Tools: []string{"Read"},
			Model:  &ModelAssignment{Provider: "anthropic", Model: "claude-sonnet"},
			Mode:   RoleSubagent,
			Hidden: &hidden,
		}},
	}
}

// A guard that inspects an encoded document only covers what the fixture
// populated. Reflection over the encoded result catches the field somebody adds
// and forgets to exercise, which is otherwise silent.
func TestEveryContractFieldIsPopulated(t *testing.T) {
	encoded, err := json.Marshal(fullyPopulatedDocument())
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode document: %v", err)
	}

	selection, _ := decoded["selection"].(map[string]any)
	for index := 0; index < reflect.TypeOf(Selection{}).NumField(); index++ {
		field := reflect.TypeOf(Selection{}).Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if _, present := selection[name]; !present {
			t.Errorf("selection.%s is absent from the fixture, so no shape guard covers it; populate it", name)
		}
	}

	roles, _ := decoded["roles"].([]any)
	if len(roles) == 0 {
		t.Fatal("fixture declares no role")
	}
	role, _ := roles[0].(map[string]any)
	for index := 0; index < reflect.TypeOf(Role{}).NumField(); index++ {
		field := reflect.TypeOf(Role{}).Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		if _, present := role[name]; !present {
			t.Errorf("roles[].%s is absent from the fixture, so no shape guard covers it; populate it", name)
		}
	}
}
