package config

import (
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// The interactive path builds a model.Selection and hands it to
// NormalizeSelection, so the contract is already the shared semantic model for
// it. What that is worth depends on the trip being lossless: a field the
// contract drops is a choice the operator made and the document cannot
// reproduce, which is exactly what "one source of truth" has to rule out.
func TestSelectionSurvivesTheContractRoundTrip(t *testing.T) {
	original := model.Selection{
		Agents:             []model.AgentID{model.AgentOpenCode},
		Components:         []model.ComponentID{model.ComponentSkills},
		Skills:             []model.SkillID{model.SkillSDDApply},
		Persona:            model.PersonaNeutral,
		Preset:             model.PresetFullGentleman,
		SDDMode:            model.SDDModeSingle,
		SDDProfileStrategy: model.SDDProfileStrategyGeneratedMulti,
		StrictTDD:          true,
		BackgroundIntent:   model.OpenCodeBackgroundOn,
		Scope:              model.InstallScopeWorkspace,
		Channel:            model.InstallChannelBeta,
		RDDMode:            model.RDDModeOn,
		CommunityTools:     []model.CommunityToolID{model.CommunityToolCodeGraph},
		OpenCodePlugins:    []model.OpenCodeCommunityPluginID{model.OpenCodePluginGentleLogo},

		ModelAssignments:            map[string]model.ModelAssignment{"sdd-apply": {ProviderID: "anthropic", ModelID: "claude-sonnet", Effort: "high"}},
		ClaudeModelAssignments:      map[string]model.ClaudeModelAlias{"sdd-apply": model.ClaudeModelOpus},
		ClaudePhaseAssignments:      map[string]model.ClaudePhaseAssignment{"sdd-apply": {Model: model.ClaudeModelOpus, Effort: model.ClaudeEffortHigh}},
		KiroModelAssignments:        map[string]model.KiroModelAlias{"sdd-apply": model.KiroModelDeepSeek},
		CodexModelAssignments:       map[string]model.CodexEffort{"sdd-apply": model.CodexEffortHigh},
		CodexOrchestratorAssignment: &model.CodexOrchestratorAssignment{Model: "gpt-5.6-sol", Effort: model.CodexEffortMedium},
		CodexCarrilModelAssignments: map[string]string{"sdd-mid": "gpt-5.6-luna"},
		CodexPhaseModelAssignments:  map[string]string{"sdd-apply": "gpt-5.6-sol"},
		Profiles:                    []model.Profile{{Name: "cheap", OrchestratorModel: model.ModelAssignment{ProviderID: "anthropic", ModelID: "claude-haiku"}}},

		MCPServers:       map[string]model.MCPServer{"atlas": {Command: "atlas", Args: []string{"mcp"}, Enabled: true}},
		Permissions:      &model.Permissions{Deny: []string{"Bash(curl *)"}},
		SkillAssignments: map[model.AgentID][]model.SkillID{model.AgentOpenCode: {model.SkillGoTesting}},
	}

	document := FromSelection(original)
	restored := Project(document)

	for index := 0; index < reflect.TypeOf(model.Selection{}).NumField(); index++ {
		field := reflect.TypeOf(model.Selection{}).Field(index)
		if unrepresentedBySelection[field.Name] {
			continue
		}

		before := reflect.ValueOf(original).Field(index).Interface()
		after := reflect.ValueOf(restored).Field(index).Interface()
		if !reflect.DeepEqual(before, after) {
			t.Errorf("%s does not survive the contract round trip:\n before %#v\n after  %#v", field.Name, before, after)
		}
	}
}

// These are not desired state: one is a deprecated flag the adapter now decides
// on its own, the other an imperative clear action a document expresses by
// omitting the assignment.
var unrepresentedBySelection = map[string]bool{
	"CodexMultiAgent":                  true,
	"ClearCodexOrchestratorAssignment": true,
}
