// Package config defines the versioned, provider-neutral desired-state contract.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/gentleman-programming/gentle-ai/v2/internal/catalog"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

const (
	CurrentVersion = "v1"
	legacyVersion  = "v0"
)

type Severity string

const Error Severity = "error"

type Diagnostic struct {
	Code     string   `json:"code"`
	Path     string   `json:"path"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

type RoleID string
type RoleRef RoleID

type Role struct {
	ID           RoleID    `json:"id"`
	RenderedName string    `json:"renderedName,omitempty"`
	References   []RoleRef `json:"references,omitempty"`
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
	Profiles           []model.Profile            `json:"profiles,omitempty"`
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
	return model.Selection{
		Agents:             append([]model.AgentID(nil), state.Selection.Agents...),
		Components:         append([]model.ComponentID(nil), state.Selection.Components...),
		Skills:             append([]model.SkillID(nil), state.Selection.Skills...),
		Persona:            state.Selection.Persona,
		Preset:             state.Selection.Preset,
		SDDMode:            state.Selection.SDDMode,
		SDDProfileStrategy: state.Selection.SDDProfileStrategy,
		StrictTDD:          state.Selection.StrictTDD,
		Profiles:           append([]model.Profile(nil), state.Selection.Profiles...),
	}
}

// FromSelection wraps existing flag and interactive choices in the shared domain.
func FromSelection(selection model.Selection) DesiredState {
	return DesiredState{Version: CurrentVersion, Selection: Selection{
		Agents: selection.Agents, Components: selection.Components, Skills: selection.Skills,
		Persona: selection.Persona, Preset: selection.Preset, SDDMode: selection.SDDMode,
		SDDProfileStrategy: selection.SDDProfileStrategy, StrictTDD: selection.StrictTDD,
		Profiles: selection.Profiles,
	}}
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

	selection.Agents = unique(selection.Agents)
	selection.Components = unique(selection.Components)
	selection.Skills = unique(selection.Skills)

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
