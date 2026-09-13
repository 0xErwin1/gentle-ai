package tui

import (
	"fmt"

	configdomain "github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// normalizeSyncOverrides routes what an interactive picker produced through the
// same desired-state model a declared document travels, so a value chosen in
// the TUI receives the validation and canonicalisation a value written in a file
// receives. The interactive flow is another frontend over one model, not a
// second configuration path.
//
// Only the fields the override already carried are written back. Normalization
// fills defaults — persona, preset, the component set — and an override uses a
// zero value to mean "leave this alone", so copying the whole normalized
// selection would turn a model picker into a rewrite of the installation.
func normalizeSyncOverrides(overrides *model.SyncOverrides) (*model.SyncOverrides, error) {
	if overrides == nil {
		return nil, nil
	}

	normalized, diagnostics := configdomain.NormalizeSelection(selectionFromOverrides(*overrides))
	if len(diagnostics) != 0 {
		return nil, fmt.Errorf("%s: %s", diagnostics[0].Code, diagnostics[0].Message)
	}

	applied := *overrides
	if applied.ModelAssignments != nil {
		applied.ModelAssignments = normalized.ModelAssignments
	}
	if applied.ClaudeModelAssignments != nil {
		applied.ClaudeModelAssignments = normalized.ClaudeModelAssignments
	}
	if applied.ClaudePhaseAssignments != nil {
		applied.ClaudePhaseAssignments = normalized.ClaudePhaseAssignments
	}
	if applied.KiroModelAssignments != nil {
		applied.KiroModelAssignments = normalized.KiroModelAssignments
	}
	if applied.CodexModelAssignments != nil {
		applied.CodexModelAssignments = normalized.CodexModelAssignments
	}
	if applied.CodexOrchestratorAssignment != nil {
		applied.CodexOrchestratorAssignment = normalized.CodexOrchestratorAssignment
	}
	if applied.CodexCarrilModelAssignments != nil {
		applied.CodexCarrilModelAssignments = normalized.CodexCarrilModelAssignments
	}
	if applied.CodexPhaseModelAssignments != nil {
		applied.CodexPhaseModelAssignments = normalized.CodexPhaseModelAssignments
	}
	if applied.Profiles != nil {
		applied.Profiles = normalized.Profiles
	}

	return &applied, nil
}

// selectionFromOverrides projects an override into the semantic selection the
// contract validates. Persona is deliberately left unset: NormalizeSelection
// preserves an unset persona rather than defaulting it, which is what keeps a
// model picker from declaring one.
func selectionFromOverrides(overrides model.SyncOverrides) model.Selection {
	return model.Selection{
		Agents:                      overrides.TargetAgents,
		SDDMode:                     overrides.SDDMode,
		SDDProfileStrategy:          overrides.SDDProfileStrategy,
		ModelAssignments:            overrides.ModelAssignments,
		ClaudeModelAssignments:      overrides.ClaudeModelAssignments,
		ClaudePhaseAssignments:      overrides.ClaudePhaseAssignments,
		KiroModelAssignments:        overrides.KiroModelAssignments,
		CodexModelAssignments:       overrides.CodexModelAssignments,
		CodexOrchestratorAssignment: overrides.CodexOrchestratorAssignment,
		CodexCarrilModelAssignments: overrides.CodexCarrilModelAssignments,
		CodexPhaseModelAssignments:  overrides.CodexPhaseModelAssignments,
		Profiles:                    overrides.Profiles,
	}
}
