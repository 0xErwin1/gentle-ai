package config

import (
	"fmt"
	"sort"

	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
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

func sortedKeys[V any](source map[string]V) []string {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

// ClaudePhaseAssignment is the contract form of a Claude model choice for one
// phase. An omitted effort means the model's own default.
type ClaudePhaseAssignment struct {
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
}

// CodexOrchestratorAssignment is the contract form of the top-level Codex
// session choice. It is a pointer on Selection because encoding/json defines no
// empty state for a struct value.
type CodexOrchestratorAssignment struct {
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
}

func claudePhasesToModel(assignments map[string]ClaudePhaseAssignment) map[string]model.ClaudePhaseAssignment {
	if assignments == nil {
		return nil
	}

	converted := make(map[string]model.ClaudePhaseAssignment, len(assignments))
	for phase, assignment := range assignments {
		converted[phase] = model.ClaudePhaseAssignment{
			Model:  model.ClaudeModelAlias(assignment.Model),
			Effort: model.ClaudeEffort(assignment.Effort),
		}
	}

	return converted
}

func claudePhasesFromModel(assignments map[string]model.ClaudePhaseAssignment) map[string]ClaudePhaseAssignment {
	if assignments == nil {
		return nil
	}

	converted := make(map[string]ClaudePhaseAssignment, len(assignments))
	for phase, assignment := range assignments {
		converted[phase] = ClaudePhaseAssignment{
			Model:  string(assignment.Model),
			Effort: string(assignment.Effort),
		}
	}

	return converted
}

func codexOrchestratorToModel(assignment *CodexOrchestratorAssignment) *model.CodexOrchestratorAssignment {
	if assignment == nil {
		return nil
	}

	return &model.CodexOrchestratorAssignment{
		Model:  assignment.Model,
		Effort: model.CodexEffort(assignment.Effort),
	}
}

func codexOrchestratorFromModel(assignment *model.CodexOrchestratorAssignment) *CodexOrchestratorAssignment {
	if assignment == nil {
		return nil
	}

	return &CodexOrchestratorAssignment{
		Model:  assignment.Model,
		Effort: string(assignment.Effort),
	}
}

// validateCodexModelSurfaces rejects an empty model id rather than storing a
// blank assignment, for the two Codex model-id surfaces that stay flat
// rather than moving under a provider block.
func validateCodexModelSurfaces(selection Selection, diagnostics *[]Diagnostic) {
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

// validateStructuredAssignments separates an unsupported model from an effort
// the model does not support, because the two need different corrections.
func validateStructuredAssignments(selection Selection, diagnostics *[]Diagnostic) {
	for _, phase := range sortedKeys(selection.ClaudePhaseAssignments) {
		assignment := selection.ClaudePhaseAssignments[phase]
		alias := model.ClaudeModelAlias(assignment.Model)
		path := "$.selection.claudePhaseAssignments." + phase

		if !alias.Valid() {
			*diagnostics = append(*diagnostics, diagnostic("config.claude-phase.unsupported", path, fmt.Sprintf("unsupported Claude model %q", assignment.Model)))
			continue
		}

		if !model.ClaudeEffortAllowedForModel(alias, model.ClaudeEffort(assignment.Effort)) {
			*diagnostics = append(*diagnostics, diagnostic("config.claude-phase.effort-unsupported", path, fmt.Sprintf("Claude model %q does not support effort %q", assignment.Model, assignment.Effort)))
		}
	}

	orchestrator := selection.CodexOrchestrator
	if orchestrator == nil {
		return
	}

	if orchestrator.Model == "" {
		*diagnostics = append(*diagnostics, diagnostic("config.codex-orchestrator.incomplete", "$.selection.codexOrchestrator", "a Codex orchestrator assignment requires a model id"))
		return
	}

	if effort := model.CodexEffort(orchestrator.Effort); orchestrator.Effort != "" && !effort.Valid() {
		*diagnostics = append(*diagnostics, diagnostic("config.codex-orchestrator.effort-unsupported", "$.selection.codexOrchestrator", fmt.Sprintf("unsupported Codex effort %q; use low, medium, high, or xhigh", orchestrator.Effort)))
	}
}
