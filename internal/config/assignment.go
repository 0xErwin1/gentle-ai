package config

import (
	"fmt"
	"sort"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// The contract owns its own spelling of every value a user writes. Internal
// model types carry no json tags, so publishing them directly would make each
// Go identifier part of the versioned schema and every later rename a silent
// breaking change for existing configuration files.

// ModelAssignment is the contract form of a provider-qualified model choice.
// An omitted effort means the provider default.
type ModelAssignment struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort,omitempty"`
}

// Profile is the contract form of a named SDD profile. Orchestrator is a
// pointer because encoding/json defines no empty state for a struct value: a
// non-pointer field would publish an object of zero values and assert an
// assignment the user never declared.
type Profile struct {
	Name             string                     `json:"name"`
	Orchestrator     *ModelAssignment           `json:"orchestrator,omitempty"`
	PhaseAssignments map[string]ModelAssignment `json:"phaseAssignments,omitempty"`
}

func assignmentToModel(assignment ModelAssignment) model.ModelAssignment {
	return model.ModelAssignment{
		ProviderID: assignment.Provider,
		ModelID:    assignment.Model,
		Effort:     assignment.Effort,
	}
}

func assignmentFromModel(assignment model.ModelAssignment) ModelAssignment {
	return ModelAssignment{
		Provider: assignment.ProviderID,
		Model:    assignment.ModelID,
		Effort:   assignment.Effort,
	}
}

func profilesToModel(profiles []Profile) []model.Profile {
	if profiles == nil {
		return nil
	}

	converted := make([]model.Profile, 0, len(profiles))
	for _, profile := range profiles {
		phases := make(map[string]model.ModelAssignment, len(profile.PhaseAssignments))
		for phase, assignment := range profile.PhaseAssignments {
			phases[phase] = assignmentToModel(assignment)
		}
		if len(phases) == 0 {
			phases = nil
		}

		orchestrator := model.ModelAssignment{}
		if profile.Orchestrator != nil {
			orchestrator = assignmentToModel(*profile.Orchestrator)
		}

		converted = append(converted, model.Profile{
			Name:              profile.Name,
			OrchestratorModel: orchestrator,
			PhaseAssignments:  phases,
		})
	}

	return converted
}

func profilesFromModel(profiles []model.Profile) []Profile {
	if profiles == nil {
		return nil
	}

	converted := make([]Profile, 0, len(profiles))
	for _, profile := range profiles {
		phases := make(map[string]ModelAssignment, len(profile.PhaseAssignments))
		for phase, assignment := range profile.PhaseAssignments {
			phases[phase] = assignmentFromModel(assignment)
		}
		if len(phases) == 0 {
			phases = nil
		}

		var orchestrator *ModelAssignment
		if profile.OrchestratorModel != (model.ModelAssignment{}) {
			assignment := assignmentFromModel(profile.OrchestratorModel)
			orchestrator = &assignment
		}

		converted = append(converted, Profile{
			Name:             profile.Name,
			Orchestrator:     orchestrator,
			PhaseAssignments: phases,
		})
	}

	return converted
}

func assignmentsToModel(assignments map[string]ModelAssignment) map[string]model.ModelAssignment {
	if assignments == nil {
		return nil
	}

	converted := make(map[string]model.ModelAssignment, len(assignments))
	for phase, assignment := range assignments {
		converted[phase] = assignmentToModel(assignment)
	}

	return converted
}

func assignmentsFromModel(assignments map[string]model.ModelAssignment) map[string]ModelAssignment {
	if assignments == nil {
		return nil
	}

	converted := make(map[string]ModelAssignment, len(assignments))
	for phase, assignment := range assignments {
		converted[phase] = assignmentFromModel(assignment)
	}

	return converted
}

func copyMap[K comparable, V any](source map[K]V) map[K]V {
	if source == nil {
		return nil
	}

	copied := make(map[K]V, len(source))
	for key, value := range source {
		copied[key] = value
	}

	return copied
}

// validateAssignments reports every unsupported value rather than the first, so
// one run surfaces the whole correction a document needs. Ordering is by phase
// name within each surface to keep diagnostics stable across runs.
func validateAssignments(selection Selection, diagnostics *[]Diagnostic) {
	for _, phase := range sortedKeys(selection.ModelAssignments) {
		assignment := selection.ModelAssignments[phase]
		if assignment.Provider == "" || assignment.Model == "" {
			*diagnostics = append(*diagnostics, diagnostic("config.model-assignment.incomplete", "$.selection.modelAssignments."+phase, "a model assignment requires both provider and model"))
		}
	}

	for _, phase := range sortedKeys(selection.ClaudeModelAssignments) {
		if alias := selection.ClaudeModelAssignments[phase]; !alias.Valid() {
			*diagnostics = append(*diagnostics, diagnostic("config.claude-model.unsupported", "$.selection.claudeModelAssignments."+phase, fmt.Sprintf("unsupported Claude model %q", alias)))
		}
	}

	for _, phase := range sortedKeys(selection.KiroModelAssignments) {
		if alias := selection.KiroModelAssignments[phase]; !alias.Valid() {
			*diagnostics = append(*diagnostics, diagnostic("config.kiro-model.unsupported", "$.selection.kiroModelAssignments."+phase, fmt.Sprintf("unsupported Kiro model %q", alias)))
		}
	}

	for _, phase := range sortedKeys(selection.CodexModelAssignments) {
		if effort := selection.CodexModelAssignments[phase]; !effort.Valid() {
			*diagnostics = append(*diagnostics, diagnostic("config.codex-effort.unsupported", "$.selection.codexModelAssignments."+phase, fmt.Sprintf("unsupported Codex effort %q; use low, medium, high, or xhigh", effort)))
		}
	}

	for _, surface := range []struct {
		path        string
		assignments map[string]string
	}{
		{path: "codexCarrilModelAssignments", assignments: selection.CodexCarrilModelAssignments},
		{path: "codexPhaseModelAssignments", assignments: selection.CodexPhaseModelAssignments},
	} {
		for _, phase := range sortedKeys(surface.assignments) {
			if surface.assignments[phase] == "" {
				*diagnostics = append(*diagnostics, diagnostic("config.codex-model.empty", "$.selection."+surface.path+"."+phase, "a Codex model assignment requires a model id"))
			}
		}
	}
}

func sortedKeys[V any](source map[string]V) []string {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}
