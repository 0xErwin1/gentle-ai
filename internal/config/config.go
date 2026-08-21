// Package config defines the versioned, provider-neutral desired-state contract.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"

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

type RoleID string
type RoleRef RoleID

// RoleMode is how a client addresses a role: primary is one the operator talks
// to, subagent is one another role delegates to.
type RoleMode string

const (
	RolePrimary  RoleMode = "primary"
	RoleSubagent RoleMode = "subagent"
)

type Role struct {
	ID           RoleID    `json:"id"`
	RenderedName string    `json:"renderedName,omitempty"`
	References   []RoleRef `json:"references,omitempty"`

	// Every adapter that expresses roles needs this content, whether it keeps
	// them as files or as entries in one settings file. Rendering a role
	// without them would mean inventing a description, a model and a prompt the
	// document never declared. Model is a pointer so an undeclared one stays
	// absent from the encoded document.
	Description string           `json:"description,omitempty"`
	Prompt      string           `json:"prompt,omitempty"`
	Tools       []string         `json:"tools,omitempty"`
	Model       *ModelAssignment `json:"model,omitempty"`

	// Mode and Hidden describe how a client presents the role: whether it is
	// something the operator addresses directly or something another role
	// delegates to, and whether it appears in the client's agent list. Adapters
	// that generate these today decide them from the role's purpose, so a
	// document that cannot say them can only be rendered by guessing.
	Mode   RoleMode `json:"mode,omitempty"`
	Hidden *bool    `json:"hidden,omitempty"`
}

type Selection struct {
	Agents             []model.AgentID            `json:"agents,omitempty"`
	Components         []model.ComponentID        `json:"components,omitempty"`
	Skills             []model.SkillID            `json:"skills,omitempty"`
	Persona            model.PersonaID            `json:"persona,omitempty"`
	Preset             model.PresetID             `json:"preset,omitempty"`
	SDDMode            model.SDDModeID            `json:"sddMode,omitempty"`
	SDDProfileStrategy model.SDDProfileStrategyID `json:"sddProfileStrategy,omitempty"`
	StrictTDD          bool                       `json:"strictTDD,omitempty"`
	Profiles           []Profile                  `json:"profiles,omitempty"`

	// BackgroundIntent stays unresolved when omitted. Defaulting it here would
	// turn silence into an explicit choice, and only an explicit choice is
	// persisted as managed state.
	BackgroundIntent model.OpenCodeBackgroundIntent `json:"backgroundIntent,omitempty"`

	// PiBackgroundIntent is the same choice for Pi and follows the same rule:
	// silence stays unresolved, because only an explicit choice is persisted.
	PiBackgroundIntent model.PiBackgroundIntent `json:"piBackgroundIntent,omitempty"`

	// Assignment maps are keyed by phase name. The document is the complete
	// desired state, so an omitted map declares no assignments rather than
	// leaving a previous choice untouched.
	ModelAssignments            map[string]ModelAssignment        `json:"modelAssignments,omitempty"`
	ClaudeModelAssignments      map[string]model.ClaudeModelAlias `json:"claudeModelAssignments,omitempty"`
	KiroModelAssignments        map[string]model.KiroModelAlias   `json:"kiroModelAssignments,omitempty"`
	PiModelAssignments          map[string]model.PiAgentRouting   `json:"piModelAssignments,omitempty"`
	PiModelFamily               model.AgentID                     `json:"piModelFamily,omitempty"`
	CodexModelAssignments       map[string]model.CodexEffort      `json:"codexModelAssignments,omitempty"`
	CodexCarrilModelAssignments map[string]string                 `json:"codexCarrilModelAssignments,omitempty"`
	CodexPhaseModelAssignments  map[string]string                 `json:"codexPhaseModelAssignments,omitempty"`

	ClaudePhaseAssignments map[string]ClaudePhaseAssignment `json:"claudePhaseAssignments,omitempty"`
	CodexOrchestrator      *CodexOrchestratorAssignment     `json:"codexOrchestrator,omitempty"`

	CommunityTools  []model.CommunityToolID           `json:"communityTools,omitempty"`
	OpenCodePlugins []model.OpenCodeCommunityPluginID `json:"openCodePlugins,omitempty"`

	// Scope and Channel stay unresolved when omitted so the flag and the
	// environment keep their turn; only a declared value overrides them.
	Scope   model.InstallScope   `json:"scope,omitempty"`
	Channel model.InstallChannel `json:"channel,omitempty"`

	// SkillExclusions remove skills from whatever the selection resolves to,
	// including the full set an omitted Skills list means. Without them the only
	// way to drop one skill is to restate every other, which makes the document
	// carry a copy of gentle-ai's catalogue and go stale the moment it grows.
	SkillExclusions []model.SkillID `json:"skillExclusions,omitempty"`

	// ModelPresets name one of gentle-ai's own model profiles per provider,
	// rather than restating the models and efforts each resolves to. Spelling
	// those out pins today's matrix into the document, so a profile gentle-ai
	// retunes stops being the profile the operator asked for.
	//
	// The map is keyed by provider because subscriptions are: an operator can
	// afford the expensive tier on one client and wants the cheap one on
	// another, which a single global profile cannot say.
	ModelPresets map[string]string `json:"modelPresets,omitempty"`

	// SkillAssignments override the flat skill list for one adapter. An adapter
	// without an entry takes the flat list, so the simple form keeps meaning
	// "every adapter" and the map is only needed when they must differ.
	SkillAssignments map[string][]model.SkillID `json:"skillAssignments,omitempty"`

	// Permissions add to the guardrails gentle-ai ships rather than replacing
	// them, so declaring an allowance never quietly removes a shipped deny.
	Permissions *Permissions `json:"permissions,omitempty"`

	// MCPServerAssignments override the flat server set for one adapter. A
	// client identifies itself to some servers, and an installation rarely gives
	// every client the same tools, so an adapter named here takes its own set
	// and the others keep the flat one.
	MCPServerAssignments map[string]map[string]MCPServer `json:"mcpServerAssignments,omitempty"`

	// MCPServers are keyed by server name. A local server runs a command; a
	// remote one is reached at a URL. Declaring both is rejected rather than
	// silently preferring one.
	MCPServers map[string]MCPServer `json:"mcpServers,omitempty"`

	// RDDMode governs the global review kill switch only. The clone-local
	// override stays out of the contract on purpose: it exists so that no
	// repository can ship or force a review policy onto a clone.
	RDDMode model.RDDMode `json:"rddMode,omitempty"`
}

type Document struct {
	Version    string                     `json:"version"`
	Selection  Selection                  `json:"selection"`
	Roles      []Role                     `json:"roles,omitempty"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

type DesiredState struct {
	Version    string                     `json:"version"`
	Selection  Selection                  `json:"selection"`
	Roles      []Role                     `json:"roles,omitempty"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

// Decode converts a JSON document into canonical desired state without side effects.
func Decode(input []byte) (DesiredState, []Diagnostic) {
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
	roles := normalizeRoles(document.Roles, &diagnostics)
	validateExtensions(document, &diagnostics)
	if version == "" {
		return DesiredState{}, diagnostics
	}

	return DesiredState{
		Version:    version,
		Selection:  selection,
		Roles:      roles,
		Extensions: copyExtensions(document.Extensions),
	}, diagnostics
}

// Project provides the existing planner and installer semantic selection.
func Project(state DesiredState) model.Selection {
	return withModelPresets(model.Selection{
		Agents:             append([]model.AgentID(nil), state.Selection.Agents...),
		Components:         append([]model.ComponentID(nil), state.Selection.Components...),
		Skills:             append([]model.SkillID(nil), state.Selection.Skills...),
		SkillExclusions:    append([]model.SkillID(nil), state.Selection.SkillExclusions...),
		ModelPresets:       copyStringMap(state.Selection.ModelPresets),
		Persona:            state.Selection.Persona,
		Preset:             state.Selection.Preset,
		SDDMode:            state.Selection.SDDMode,
		SDDProfileStrategy: state.Selection.SDDProfileStrategy,
		StrictTDD:          state.Selection.StrictTDD,
		Profiles:           profilesToModel(state.Selection.Profiles),
		BackgroundIntent:   state.Selection.BackgroundIntent,
		PiBackgroundIntent: state.Selection.PiBackgroundIntent,

		ModelAssignments:            assignmentsToModel(state.Selection.ModelAssignments),
		ClaudeModelAssignments:      copyMap(state.Selection.ClaudeModelAssignments),
		KiroModelAssignments:        copyMap(state.Selection.KiroModelAssignments),
		PiModelAssignments:          copyMap(state.Selection.PiModelAssignments),
		PiModelFamily:               state.Selection.PiModelFamily,
		CodexModelAssignments:       copyMap(state.Selection.CodexModelAssignments),
		CodexCarrilModelAssignments: copyMap(state.Selection.CodexCarrilModelAssignments),
		CodexPhaseModelAssignments:  copyMap(state.Selection.CodexPhaseModelAssignments),
		ClaudePhaseAssignments:      claudePhasesToModel(state.Selection.ClaudePhaseAssignments),
		CodexOrchestratorAssignment: codexOrchestratorToModel(state.Selection.CodexOrchestrator),
		CommunityTools:              append([]model.CommunityToolID(nil), state.Selection.CommunityTools...),
		OpenCodePlugins:             append([]model.OpenCodeCommunityPluginID(nil), state.Selection.OpenCodePlugins...),
		Scope:                       state.Selection.Scope,
		Channel:                     state.Selection.Channel,
		RDDMode:                     state.Selection.RDDMode,
		MCPServers:                  mcpServersToModel(state.Selection.MCPServers),
		Permissions:                 permissionsToModel(state.Selection.Permissions),
		SkillAssignments:            skillAssignmentsToModel(state.Selection.SkillAssignments),
		MCPServerAssignments:        mcpAssignmentsToModel(state.Selection.MCPServerAssignments),
	})
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

		case model.AgentPi:
			// The profile is a floor, not a ceiling: it fills the agents the
			// document left alone and never displaces one it assigned. The
			// other providers replace the whole table instead, so one explicit
			// assignment silently drops the rest of their profile.
			routing := model.PiModelPresetAssignments(preset)
			// Naming the provider Pi runs on turns the profile from reasoning
			// levels into models as well, taken from that provider's own table.
			for agent, entry := range model.PiModelsForFamily(selection.PiModelFamily, preset) {
				routing[agent] = entry
			}
			for agent, entry := range selection.PiModelAssignments {
				routing[agent] = entry
			}
			selection.PiModelAssignments = routing

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
		SkillExclusions: selection.SkillExclusions, ModelPresets: copyStringMap(selection.ModelPresets),
		Persona: selection.Persona, Preset: selection.Preset, SDDMode: selection.SDDMode,
		SDDProfileStrategy: selection.SDDProfileStrategy, StrictTDD: selection.StrictTDD,
		Profiles: profilesFromModel(selection.Profiles), BackgroundIntent: selection.BackgroundIntent, PiBackgroundIntent: selection.PiBackgroundIntent,

		ModelAssignments:            assignmentsFromModel(selection.ModelAssignments),
		ClaudeModelAssignments:      copyMap(selection.ClaudeModelAssignments),
		KiroModelAssignments:        copyMap(selection.KiroModelAssignments),
		PiModelAssignments:          copyMap(selection.PiModelAssignments),
		PiModelFamily:               selection.PiModelFamily,
		CodexModelAssignments:       copyMap(selection.CodexModelAssignments),
		CodexCarrilModelAssignments: copyMap(selection.CodexCarrilModelAssignments),
		CodexPhaseModelAssignments:  copyMap(selection.CodexPhaseModelAssignments),
		ClaudePhaseAssignments:      claudePhasesFromModel(selection.ClaudePhaseAssignments),
		CodexOrchestrator:           codexOrchestratorFromModel(selection.CodexOrchestratorAssignment),
		CommunityTools:              selection.CommunityTools,
		OpenCodePlugins:             selection.OpenCodePlugins,
		Scope:                       selection.Scope,
		Channel:                     selection.Channel,
		RDDMode:                     selection.RDDMode,
		MCPServers:                  mcpServersFromModel(selection.MCPServers),
		Permissions:                 permissionsFromModel(selection.Permissions),
		SkillAssignments:            skillAssignmentsFromModel(selection.SkillAssignments),
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
	selection.PiModelAssignments = projected.PiModelAssignments
	selection.PiModelFamily = projected.PiModelFamily
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
	selection.MCPServers = projected.MCPServers
	selection.Permissions = projected.Permissions
	selection.SkillAssignments = projected.SkillAssignments
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

	if selection.PiBackgroundIntent != "" && !selection.PiBackgroundIntent.Valid() {
		*diagnostics = append(*diagnostics, diagnostic("config.pi-background-intent.unsupported", "$.selection.piBackgroundIntent", fmt.Sprintf("unsupported Pi background intent %q; use auto, on, or off", selection.PiBackgroundIntent)))
	}
	if selection.BackgroundIntent != "" && !selection.BackgroundIntent.Valid() {
		*diagnostics = append(*diagnostics, diagnostic("config.background-intent.unsupported", "$.selection.backgroundIntent", fmt.Sprintf("unsupported background intent %q; use auto, on, or off", selection.BackgroundIntent)))
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

	validateMCPServers(selection, diagnostics)
	validateSkillAssignments(selection, diagnostics)
	validateMCPAssignments(selection, diagnostics)
	validateAssignments(selection, diagnostics)
	validateStructuredAssignments(selection, diagnostics)

	selection.Agents = unique(selection.Agents)
	selection.Components = unique(selection.Components)
	selection.Skills = unique(selection.Skills)
	selection.SkillExclusions = unique(selection.SkillExclusions)
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
	for path, declared := range map[string][]model.SkillID{
		"$.selection.skills":          selection.Skills,
		"$.selection.skillExclusions": selection.SkillExclusions,
	} {
		for _, skill := range declared {
			if _, ok := known[skill]; !ok {
				*diagnostics = append(*diagnostics, diagnostic("config.skill.unsupported", path, fmt.Sprintf("unsupported skill %q", skill)))
			}
		}
	}

	validateModelPresets(selection, diagnostics)
	validatePiModelAssignments(selection, diagnostics)

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

// validateExtensions rejects an extension addressed to an adapter the document
// never declared. An extension is provider-specific by definition, so one whose
// provider is absent applies to nothing and would sit in the document reading
// as configuration that took effect.
func validateExtensions(state Document, diagnostics *[]Diagnostic) {
	declared := make(map[string]struct{}, len(state.Selection.Agents))
	for _, agent := range state.Selection.Agents {
		declared[string(agent)] = struct{}{}
	}

	providers := make([]string, 0, len(state.Extensions))
	for provider := range state.Extensions {
		providers = append(providers, provider)
	}
	sort.Strings(providers)

	for _, provider := range providers {
		if _, ok := declared[provider]; !ok {
			*diagnostics = append(*diagnostics, diagnostic("config.extension.undeclared-provider", "$.extensions."+provider, fmt.Sprintf("extension targets provider %q, which the document does not declare; add it to agents or remove the extension", provider)))
		}
	}
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
	model.AgentPi: {
		string(model.PiPresetLowCost), string(model.PiPresetRecommended),
		string(model.PiPresetPowerful),
	},
}

func validateModelPresets(selection Selection, diagnostics *[]Diagnostic) {
	providers := make([]string, 0, len(selection.ModelPresets))
	for provider := range selection.ModelPresets {
		providers = append(providers, provider)
	}
	sort.Strings(providers)

	for _, provider := range providers {
		path := "$.selection.modelPresets." + provider
		known, offered := modelPresetNames[model.AgentID(provider)]
		if !offered {
			*diagnostics = append(*diagnostics, diagnostic("config.model-preset.unsupported-provider", path, fmt.Sprintf("provider %q offers no model profiles; assign its models directly", provider)))
			continue
		}

		preset := selection.ModelPresets[provider]
		if !slices.Contains(known, preset) {
			*diagnostics = append(*diagnostics, diagnostic("config.model-preset.unsupported", path, fmt.Sprintf("unsupported %s model profile %q; use %s", provider, preset, strings.Join(known, ", "))))
		}
	}
}

// safePiModelID mirrors the pattern gentle-pi validates a model id against. It
// is duplicated rather than imported because it is gentle-pi's rule, not this
// contract's: what matters is refusing here what would be dropped there.
var safePiModelID = regexp.MustCompile(`^[A-Za-z0-9._~:@/+%-]+$`)

// validatePiModelAssignments refuses a routing gentle-pi would silently drop.
// It reads the file, discards every entry it cannot parse, and reports nothing,
// so a typo leaves an agent on the default model with no sign that a choice was
// ever made.
func validatePiModelAssignments(selection Selection, diagnostics *[]Diagnostic) {
	agents := make([]string, 0, len(selection.PiModelAssignments))
	for agent := range selection.PiModelAssignments {
		agents = append(agents, agent)
	}
	sort.Strings(agents)

	for _, agent := range agents {
		routing := selection.PiModelAssignments[agent]
		path := "$.selection.piModelAssignments." + agent

		if routing.Model == "" && routing.Thinking == "" {
			*diagnostics = append(*diagnostics, diagnostic("config.pi-model.empty", path, "a Pi routing assigns a model, a reasoning level, or both"))
			continue
		}
		if routing.Model != "" && !safePiModelID.MatchString(routing.Model) {
			*diagnostics = append(*diagnostics, diagnostic("config.pi-model.unsupported", path, fmt.Sprintf("unsupported Pi model id %q; gentle-pi accepts letters, digits and ._~:@/+%%-", routing.Model)))
		}
		if routing.Thinking != "" && !routing.Thinking.Valid() {
			*diagnostics = append(*diagnostics, diagnostic("config.pi-model.thinking-unsupported", path, fmt.Sprintf("unsupported Pi reasoning level %q; use off, minimal, low, medium, high, xhigh, or max", routing.Thinking)))
		}
	}
}

func validateMCPAssignments(selection Selection, diagnostics *[]Diagnostic) {
	declared := make(map[string]struct{}, len(selection.Agents))
	for _, agent := range selection.Agents {
		declared[string(agent)] = struct{}{}
	}

	adapters := make([]string, 0, len(selection.MCPServerAssignments))
	for adapter := range selection.MCPServerAssignments {
		adapters = append(adapters, adapter)
	}
	sort.Strings(adapters)

	for _, adapter := range adapters {
		if _, ok := declared[adapter]; !ok {
			*diagnostics = append(*diagnostics, diagnostic("config.mcp-assignment.undeclared-adapter", "$.selection.mcpServerAssignments."+adapter, fmt.Sprintf("adapter %q takes MCP servers but is not declared; add it to agents or remove the assignment", adapter)))
			continue
		}
		validateMCPServerSet(selection.MCPServerAssignments[adapter], "$.selection.mcpServerAssignments."+adapter, diagnostics)
	}
}

func normalizeRoles(roles []Role, diagnostics *[]Diagnostic) []Role {
	known := make(map[RoleID]struct{}, len(roles))
	for _, role := range roles {
		if role.ID == "" {
			*diagnostics = append(*diagnostics, diagnostic("config.role.invalid", "$.roles", "role id is required"))
			continue
		}
		if _, exists := known[role.ID]; exists {
			*diagnostics = append(*diagnostics, diagnostic("config.role.duplicate", "$.roles", fmt.Sprintf("duplicate role %q", role.ID)))
			continue
		}
		known[role.ID] = struct{}{}
	}
	for _, role := range roles {
		if role.Model != nil && (role.Model.Provider == "" || role.Model.Model == "") {
			*diagnostics = append(*diagnostics, diagnostic("config.role.model.incomplete", "$.roles."+string(role.ID)+".model", "a role model requires both provider and model"))
		}
		if role.Mode != "" && role.Mode != RolePrimary && role.Mode != RoleSubagent {
			*diagnostics = append(*diagnostics, diagnostic("config.role.mode.unsupported", "$.roles."+string(role.ID)+".mode", fmt.Sprintf("unsupported role mode %q; use %s or %s", role.Mode, RolePrimary, RoleSubagent)))
		}
	}
	for _, role := range roles {
		for _, reference := range role.References {
			if _, ok := known[RoleID(reference)]; !ok {
				*diagnostics = append(*diagnostics, diagnostic("config.role.reference.unresolved", "$.roles", fmt.Sprintf("unresolved role %q", reference)))
			}
		}
	}
	return append([]Role(nil), roles...)
}

func diagnostic(code, path, message string) Diagnostic {
	return Diagnostic{Code: code, Path: path, Severity: Error, Message: message}
}

func copyExtensions(extensions map[string]json.RawMessage) map[string]json.RawMessage {
	if len(extensions) == 0 {
		return nil
	}
	copy := make(map[string]json.RawMessage, len(extensions))
	for provider, value := range extensions {
		copy[provider] = append(json.RawMessage(nil), value...)
	}
	return copy
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
