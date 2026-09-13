// Package config defines the versioned, provider-neutral desired-state contract.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/gentleman-programming/gentle-ai/v2/internal/catalog"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

const (
	CurrentVersion = "v1"
	legacyVersion  = "v0"
)

type Severity string

const (
	// Error rejects the document: it declares something that cannot be
	// delivered, so acting on it would produce an installation the operator did
	// not ask for.
	Error Severity = "error"

	// Warning delivers the document and reports what part of it one adapter
	// could not take. Rejecting instead would make a capability that varies by
	// adapter unusable the moment an installation has more than one client,
	// while staying silent is the failure this severity exists to prevent.
	Warning Severity = "warning"
)

type Diagnostic struct {
	Code     string   `json:"code"`
	Path     string   `json:"path"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

type Selection struct {
	Agents     []model.AgentID     `json:"agents,omitempty"`
	Components []model.ComponentID `json:"components,omitempty"`
	Skills     []model.SkillID     `json:"skills,omitempty"`
	Persona    model.PersonaID     `json:"persona,omitempty"`
	Preset     model.PresetID      `json:"preset,omitempty"`
	SDDMode    model.SDDModeID     `json:"sddMode,omitempty"`
	StrictTDD  bool                `json:"strictTDD,omitempty"`

	// Providers hold every choice that is specific to one client: its own
	// model vocabulary, the background sub-agent policy it reads, and the
	// named profiles it supports. Grouping them here instead of one flat field
	// per provider per concept keeps a provider's block self-contained.
	Providers map[model.AgentID]ProviderSelection `json:"providers,omitempty"`

	CodexCarrilModelAssignments map[string]string `json:"codexCarrilModelAssignments,omitempty"`
	CodexPhaseModelAssignments  map[string]string `json:"codexPhaseModelAssignments,omitempty"`

	ClaudePhaseAssignments map[string]ClaudePhaseAssignment `json:"claudePhaseAssignments,omitempty"`
	CodexOrchestrator      *CodexOrchestratorAssignment     `json:"codexOrchestrator,omitempty"`

	CommunityTools  []model.CommunityToolID           `json:"communityTools,omitempty"`
	OpenCodePlugins []model.OpenCodeCommunityPluginID `json:"openCodePlugins,omitempty"`

	// Scope and Channel stay unresolved when omitted so the flag and the
	// environment keep their turn; only a declared value overrides them.
	Scope   model.InstallScope   `json:"scope,omitempty"`
	Channel model.InstallChannel `json:"channel,omitempty"`

	// RDDMode governs the global review kill switch only. The clone-local
	// override stays out of the contract on purpose: it exists so that no
	// repository can ship or force a review policy onto a clone.
	RDDMode model.RDDMode `json:"rddMode,omitempty"`
}

type Document struct {
	Version   string    `json:"version"`
	Selection Selection `json:"selection"`
}

type DesiredState struct {
	Version   string    `json:"version"`
	Selection Selection `json:"selection"`
}

// supersededSelectionFields maps a pre-2.4.0 flat selection key to the
// nested providers path that replaced it. A document that still uses one of
// these keys gets a diagnostic naming its replacement instead of the generic
// unknown-field diagnostic, because the contract is unreleased with a single
// consumer: there is no compatibility shim and no silent migration.
var supersededSelectionFields = map[string]string{
	"backgroundIntent":       "selection.providers.opencode.backgroundIntent",
	"piBackgroundIntent":     "selection.providers.pi.backgroundIntent",
	"modelAssignments":       "selection.providers.opencode.models",
	"claudeModelAssignments": "selection.providers.claude-code.models",
	"kiroModelAssignments":   "selection.providers.kiro-ide.models",
	"piModelAssignments":     "selection.providers.pi.models",
	"piModelFamily":          "selection.providers.pi.modelFamily",
	"codexModelAssignments":  "selection.providers.codex.models",
	"modelPresets":           "selection.providers.<id>.modelPreset",
	"profiles":               "selection.providers.<id>.profiles",
	"sddProfileStrategy":     "selection.providers.opencode.profileStrategy",
}

// supersededFieldDiagnostics detects a pre-2.4.0 flat document before strict
// decoding runs, so it reports exactly what changed instead of a generic
// unknown-field diagnostic. It stays silent on malformed input and lets the
// strict decoder below report that.
func supersededFieldDiagnostics(input []byte) []Diagnostic {
	var probe struct {
		Selection map[string]json.RawMessage `json:"selection"`
	}
	if err := json.Unmarshal(input, &probe); err != nil || len(probe.Selection) == 0 {
		return nil
	}

	keys := make([]string, 0, len(probe.Selection))
	for key := range probe.Selection {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	diagnostics := make([]Diagnostic, 0)
	for _, key := range keys {
		replacement, ok := supersededSelectionFields[key]
		if !ok {
			continue
		}
		diagnostics = append(diagnostics, diagnostic(
			"config.document.superseded-field",
			"selection."+key,
			fmt.Sprintf("selection.%s was replaced by %s; rewrite the document under the nested providers shape", key, replacement),
		))
	}
	return diagnostics
}

// Decode converts a JSON document into canonical desired state without side effects.
func Decode(input []byte) (DesiredState, []Diagnostic) {
	if diagnostics := supersededFieldDiagnostics(input); len(diagnostics) > 0 {
		return DesiredState{}, diagnostics
	}

	var document Document
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return DesiredState{}, []Diagnostic{decodeDiagnostic(err)}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return DesiredState{}, []Diagnostic{diagnostic("config.document.malformed", "$", "configuration must be valid JSON")}
	}

	return Normalize(document)
}

func decodeDiagnostic(err error) Diagnostic {
	if _, ok := err.(*json.UnmarshalTypeError); ok {
		return diagnostic("config.document.malformed", "$", "configuration must be valid JSON")
	}
	if bytes.Contains([]byte(err.Error()), []byte("unknown field")) {
		return diagnostic("config.document.unknown-field", "$", "configuration contains an unknown field")
	}
	return diagnostic("config.document.malformed", "$", "configuration must be valid JSON")
}

// Admit invokes action only when decoding and validation complete without diagnostics.
func Admit(input []byte, action func(DesiredState)) []Diagnostic {
	state, diagnostics := Decode(input)
	if len(diagnostics) == 0 {
		action(state)
	}
	return diagnostics
}

// Normalize migrates, defaults, validates, and canonicalizes desired state.
func Normalize(document Document) (DesiredState, []Diagnostic) {
	diagnostics := make([]Diagnostic, 0)
	version := document.Version
	switch version {
	case legacyVersion:
		version = CurrentVersion
	case CurrentVersion:
	default:
		diagnostics = append(diagnostics, diagnostic("config.version.unsupported", "$.version", fmt.Sprintf("supported versions: %s", CurrentVersion)))
		version = ""
	}

	selection := normalizeSelection(document.Selection, &diagnostics)
	if version == "" {
		return DesiredState{}, diagnostics
	}

	return DesiredState{
		Version:   version,
		Selection: selection,
	}, diagnostics
}

// Project provides the existing planner and installer semantic selection.
func Project(state DesiredState) model.Selection {
	selection := model.Selection{
		Agents:     append([]model.AgentID(nil), state.Selection.Agents...),
		Components: append([]model.ComponentID(nil), state.Selection.Components...),
		Skills:     append([]model.SkillID(nil), state.Selection.Skills...),
		Persona:    state.Selection.Persona,
		Preset:     state.Selection.Preset,
		SDDMode:    state.Selection.SDDMode,
		StrictTDD:  state.Selection.StrictTDD,

		CodexCarrilModelAssignments: copyMap(state.Selection.CodexCarrilModelAssignments),
		CodexPhaseModelAssignments:  copyMap(state.Selection.CodexPhaseModelAssignments),
		ClaudePhaseAssignments:      claudePhasesToModel(state.Selection.ClaudePhaseAssignments),
		CodexOrchestratorAssignment: codexOrchestratorToModel(state.Selection.CodexOrchestrator),
		CommunityTools:              append([]model.CommunityToolID(nil), state.Selection.CommunityTools...),
		OpenCodePlugins:             append([]model.OpenCodeCommunityPluginID(nil), state.Selection.OpenCodePlugins...),
		Scope:                       state.Selection.Scope,
		Channel:                     state.Selection.Channel,
		RDDMode:                     state.Selection.RDDMode,
	}

	projectProviders(state.Selection.Providers, &selection)

	return withModelPresets(selection)
}

// withModelPresets materialises each named profile into the maps the renderers
// read, leaving anything the document assigned explicitly alone.
//
// A profile is kept in the document by name rather than by its resolved values:
// spelling the values out pins today's matrix, so a profile gentle-ai retunes
// silently stops being the profile the operator asked for.
func withModelPresets(selection model.Selection) model.Selection {
	for provider, preset := range selection.ModelPresets {
		switch model.AgentID(provider) {
		case model.AgentClaudeCode:
			if len(selection.ClaudeModelAssignments) == 0 {
				selection.ClaudeModelAssignments = model.ClaudeModelPresetAssignments(preset)
			}

		case model.AgentKiroIDE:
			if len(selection.KiroModelAssignments) == 0 {
				selection.KiroModelAssignments = model.KiroModelPresetAssignments(preset)
			}

		case model.AgentCodex:
			if len(selection.CodexModelAssignments) == 0 {
				selection.CodexModelAssignments = codexPresetEfforts(preset)
			}
			if len(selection.CodexCarrilModelAssignments) == 0 {
				selection.CodexCarrilModelAssignments = model.CodexCarrilModelsForPreset(preset)
			}
			if selection.CodexOrchestratorAssignment == nil {
				selection.CodexOrchestratorAssignment = model.CodexPresetOrchestratorAssignment(preset)
			}
		}
	}

	return selection
}

func codexPresetEfforts(preset string) map[string]model.CodexEffort {
	switch model.CodexPresetKey(preset) {
	case model.CodexPresetLowCost:
		return model.CodexModelPresetLowCost()
	case model.CodexPresetPowerful:
		return model.CodexModelPresetPowerful()
	default:
		return model.CodexModelPresetRecommended()
	}
}

// FromSelection wraps existing flag and interactive choices in the shared domain.
func FromSelection(selection model.Selection) DesiredState {
	return DesiredState{Version: CurrentVersion, Selection: Selection{
		Agents: selection.Agents, Components: selection.Components, Skills: selection.Skills,
		Persona: selection.Persona, Preset: selection.Preset, SDDMode: selection.SDDMode,
		StrictTDD: selection.StrictTDD,

		Providers: providersFromModel(selection),

		CodexCarrilModelAssignments: copyMap(selection.CodexCarrilModelAssignments),
		CodexPhaseModelAssignments:  copyMap(selection.CodexPhaseModelAssignments),
		ClaudePhaseAssignments:      claudePhasesFromModel(selection.ClaudePhaseAssignments),
		CodexOrchestrator:           codexOrchestratorFromModel(selection.CodexOrchestratorAssignment),
		CommunityTools:              selection.CommunityTools,
		OpenCodePlugins:             selection.OpenCodePlugins,
		Scope:                       selection.Scope,
		Channel:                     selection.Channel,
		RDDMode:                     selection.RDDMode,
	}}
}

// NormalizeSelection routes existing workflow selections through the desired-state contract.
func NormalizeSelection(selection model.Selection) (model.Selection, []Diagnostic) {
	preserveUnsetPersona := selection.Persona == ""

	state, diagnostics := Normalize(Document{
		Version:   CurrentVersion,
		Selection: FromSelection(selection).Selection,
	})
	if len(diagnostics) != 0 {
		return model.Selection{}, diagnostics
	}

	projected := Project(state)
	selection.Agents = projected.Agents
	selection.Components = projected.Components
	selection.Skills = projected.Skills
	selection.Preset = projected.Preset
	selection.SDDMode = projected.SDDMode
	selection.SDDProfileStrategy = projected.SDDProfileStrategy
	selection.StrictTDD = projected.StrictTDD
	selection.Profiles = projected.Profiles
	selection.BackgroundIntent = projected.BackgroundIntent
	selection.PiBackgroundIntent = projected.PiBackgroundIntent
	selection.ModelAssignments = projected.ModelAssignments
	selection.ClaudeModelAssignments = projected.ClaudeModelAssignments
	selection.KiroModelAssignments = projected.KiroModelAssignments
	selection.CodexModelAssignments = projected.CodexModelAssignments
	selection.CodexCarrilModelAssignments = projected.CodexCarrilModelAssignments
	selection.CodexPhaseModelAssignments = projected.CodexPhaseModelAssignments
	selection.ClaudePhaseAssignments = projected.ClaudePhaseAssignments
	selection.CodexOrchestratorAssignment = projected.CodexOrchestratorAssignment
	selection.CommunityTools = projected.CommunityTools
	selection.OpenCodePlugins = projected.OpenCodePlugins
	selection.Scope = projected.Scope
	selection.Channel = projected.Channel
	selection.RDDMode = projected.RDDMode
	if !preserveUnsetPersona {
		selection.Persona = projected.Persona
	}

	return selection, nil
}

func normalizeSelection(selection Selection, diagnostics *[]Diagnostic) Selection {
	if selection.Persona == "" {
		selection.Persona = model.PersonaGentleman
	}
	if selection.Preset == "" {
		selection.Preset = model.PresetFullGentleman
	}
	if len(selection.Components) == 0 {
		selection.Components = model.ComponentsForPreset(selection.Preset, selection.Persona)
	}

	if selection.Scope != "" && !selection.Scope.Valid() {
		*diagnostics = append(*diagnostics, diagnostic("config.scope.unsupported", "$.selection.scope", fmt.Sprintf("unsupported scope %q; use global or workspace", selection.Scope)))
	}
	if selection.RDDMode != "" && !selection.RDDMode.Valid() {
		*diagnostics = append(*diagnostics, diagnostic("config.rdd-mode.unsupported", "$.selection.rddMode", fmt.Sprintf("unsupported review mode %q; use on or off", selection.RDDMode)))
	}
	if selection.Channel != "" && !selection.Channel.Valid() {
		*diagnostics = append(*diagnostics, diagnostic("config.channel.unsupported", "$.selection.channel", fmt.Sprintf("unsupported channel %q; use stable or beta", selection.Channel)))
	}

	validateProviders(selection, diagnostics)
	validateCodexModelSurfaces(selection, diagnostics)
	validateStructuredAssignments(selection, diagnostics)

	selection.Agents = unique(selection.Agents)
	selection.Components = unique(selection.Components)
	selection.Skills = unique(selection.Skills)
	selection.CommunityTools = unique(selection.CommunityTools)
	selection.OpenCodePlugins = unique(selection.OpenCodePlugins)

	for _, tool := range selection.CommunityTools {
		if tool != model.CommunityToolCodeGraph {
			*diagnostics = append(*diagnostics, diagnostic("config.community-tool.unsupported", "$.selection.communityTools", fmt.Sprintf("unsupported community tool %q", tool)))
		}
	}

	for _, plugin := range selection.OpenCodePlugins {
		switch plugin {
		case model.OpenCodePluginSubAgentStatusline, model.OpenCodePluginSDDEngramManage, model.OpenCodePluginGentleLogo:
		default:
			*diagnostics = append(*diagnostics, diagnostic("config.opencode-plugin.unsupported", "$.selection.openCodePlugins", fmt.Sprintf("unsupported OpenCode plugin %q", plugin)))
		}
	}

	known := make(map[model.SkillID]struct{}, len(catalog.MVPSkills()))
	for _, skill := range catalog.MVPSkills() {
		known[skill.ID] = struct{}{}
	}
	for _, skill := range selection.Skills {
		if _, ok := known[skill]; !ok {
			*diagnostics = append(*diagnostics, diagnostic("config.skill.unsupported", "$.selection.skills", fmt.Sprintf("unsupported skill %q", skill)))
		}
	}

	for _, agent := range selection.Agents {
		if !catalog.IsSupportedAgent(agent) {
			*diagnostics = append(*diagnostics, diagnostic("config.agent.unsupported", "$.selection.agents", fmt.Sprintf("unsupported agent %q", agent)))
		}
	}

	allowed := make(map[model.ComponentID]struct{}, len(catalog.MVPComponents()))
	for _, component := range catalog.MVPComponents() {
		allowed[component.ID] = struct{}{}
	}
	for _, component := range selection.Components {
		if _, ok := allowed[component]; !ok {
			*diagnostics = append(*diagnostics, diagnostic("config.component.unsupported", "$.selection.components", fmt.Sprintf("unsupported component %q", component)))
		}
	}

	return selection
}

// modelPresetNames are the profiles each provider offers. A provider absent
// from this table expresses no profile at all: its models are assigned
// directly, and naming one for it would look configured and do nothing.
var modelPresetNames = map[model.AgentID][]string{
	model.AgentClaudeCode: {
		string(model.ClaudePresetBalanced), string(model.ClaudePresetPerformance),
		string(model.ClaudePresetEconomy), string(model.ClaudePresetDiversity),
	},
	model.AgentKiroIDE: {
		string(model.KiroPresetBalanced), string(model.KiroPresetPerformance),
		string(model.KiroPresetEconomy), string(model.KiroPresetOpenWeight),
	},
	model.AgentCodex: {
		string(model.CodexPresetLowCost), string(model.CodexPresetRecommended),
		string(model.CodexPresetPowerful),
	},
}

func diagnostic(code, path, message string) Diagnostic {
	return Diagnostic{Code: code, Path: path, Severity: Error, Message: message}
}

func unique[T comparable](values []T) []T {
	seen := make(map[T]struct{}, len(values))
	result := make([]T, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
