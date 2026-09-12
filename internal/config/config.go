// Package config defines the versioned, provider-neutral desired-state contract.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
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
	Agents     []model.AgentID     `json:"agents,omitempty"`
	Components []model.ComponentID `json:"components,omitempty"`
	Skills     []model.SkillID     `json:"skills,omitempty"`
	Persona    model.PersonaID     `json:"persona,omitempty"`
	Preset     model.PresetID      `json:"preset,omitempty"`
	SDDMode    model.SDDModeID     `json:"sddMode,omitempty"`
	StrictTDD  bool                `json:"strictTDD,omitempty"`

	// Providers hold every choice that is specific to one client: its own
	// model vocabulary, the background sub-agent policy it reads, the named
	// profiles it supports, and the skill/MCP overrides that replace the flat
	// lists for it. Grouping them here instead of one flat field per provider
	// per concept keeps a provider's block self-contained.
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

	// SkillExclusions remove skills from whatever the selection resolves to,
	// including the full set an omitted Skills list means. Without them the only
	// way to drop one skill is to restate every other, which makes the document
	// carry a copy of gentle-ai's catalogue and go stale the moment it grows.
	SkillExclusions []model.SkillID `json:"skillExclusions,omitempty"`

	// Permissions add to the guardrails gentle-ai ships rather than replacing
	// them, so declaring an allowance never quietly removes a shipped deny.
	Permissions *Permissions `json:"permissions,omitempty"`

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
	Version   string    `json:"version"`
	Selection Selection `json:"selection"`
	Roles     []Role    `json:"roles,omitempty"`
}

type DesiredState struct {
	Version   string    `json:"version"`
	Selection Selection `json:"selection"`
	Roles     []Role    `json:"roles,omitempty"`
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
	"skillAssignments":       "selection.providers.<id>.skills",
	"mcpServerAssignments":   "selection.providers.<id>.mcpServers",
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
	roles := normalizeRoles(document.Roles, &diagnostics)
	if version == "" {
		return DesiredState{}, diagnostics
	}

	return DesiredState{
		Version:   version,
		Selection: selection,
		Roles:     roles,
	}, diagnostics
}

// Project provides the existing planner and installer semantic selection.
func Project(state DesiredState) model.Selection {
	selection := model.Selection{
		Agents:          append([]model.AgentID(nil), state.Selection.Agents...),
		Components:      append([]model.ComponentID(nil), state.Selection.Components...),
		Skills:          append([]model.SkillID(nil), state.Selection.Skills...),
		SkillExclusions: append([]model.SkillID(nil), state.Selection.SkillExclusions...),
		Persona:         state.Selection.Persona,
		Preset:          state.Selection.Preset,
		SDDMode:         state.Selection.SDDMode,
		StrictTDD:       state.Selection.StrictTDD,

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
		SkillExclusions: selection.SkillExclusions,
		Persona:         selection.Persona, Preset: selection.Preset, SDDMode: selection.SDDMode,
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
		MCPServers:                  mcpServersFromModel(selection.MCPServers),
		Permissions:                 permissionsFromModel(selection.Permissions),
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
	validateProviders(selection, diagnostics)
	validateCodexModelSurfaces(selection, diagnostics)
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
	model.AgentPi: {
		string(model.PiPresetLowCost), string(model.PiPresetRecommended),
		string(model.PiPresetPowerful),
	},
}

// safePiModelID mirrors the pattern gentle-pi validates a model id against. It
// is duplicated rather than imported because it is gentle-pi's rule, not this
// contract's: what matters is refusing here what would be dropped there.
var safePiModelID = regexp.MustCompile(`^[A-Za-z0-9._~:@/+%-]+$`)

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
