package config

import "github.com/gentleman-programming/gentle-ai/v2/internal/model"

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

		converted = append(converted, model.Profile{
			Name:              profile.Name,
			OrchestratorModel: orchestratorToModel(profile.Orchestrator),
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

		converted = append(converted, Profile{
			Name:             profile.Name,
			Orchestrator:     orchestratorFromModel(profile.OrchestratorModel),
			PhaseAssignments: phases,
		})
	}

	return converted
}

func orchestratorToModel(assignment *ModelAssignment) model.ModelAssignment {
	if assignment == nil {
		return model.ModelAssignment{}
	}

	return assignmentToModel(*assignment)
}

func orchestratorFromModel(assignment model.ModelAssignment) *ModelAssignment {
	if assignment == (model.ModelAssignment{}) {
		return nil
	}

	converted := assignmentFromModel(assignment)

	return &converted
}
