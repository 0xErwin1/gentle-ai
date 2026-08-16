package tui

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

// The detailed sync callback is the same sync seam as SyncFn, only with
// manual actions added to its result. Every interactive picker reaches sync
// through startSync, so both branches must route their overrides through
// normalizeSyncOverrides: a value chosen in a picker receives the validation
// a value written in a file receives, whichever callback delivers it.
func TestDetailedSyncNormalizesOverrides(t *testing.T) {
	reached := false
	m := Model{SyncDetailedFn: func(*model.SyncOverrides) ([]string, []string, error) {
		reached = true

		return nil, nil, nil
	}}

	command := m.startSync(&model.SyncOverrides{
		TargetAgents:           []model.AgentID{model.AgentClaudeCode},
		ClaudeModelAssignments: map[string]model.ClaudeModelAlias{"sdd-apply": "not-a-model"},
	})

	message, _ := command().(SyncDoneMsg)
	if message.Err == nil {
		t.Fatal("detailed sync accepted an override the contract rejects")
	}
	if reached {
		t.Error("the unsupported override reached the detailed sync function")
	}
}

// A detailed sync of a valid override still runs, with the normalized copy
// rather than the raw input.
func TestDetailedSyncRunsNormalizedOverrides(t *testing.T) {
	m := Model{SyncDetailedFn: func(overrides *model.SyncOverrides) ([]string, []string, error) {
		if overrides == nil {
			t.Fatal("detailed sync received nil overrides")
		}
		if overrides.TargetAgents == nil {
			t.Fatal("detailed sync received overrides without their agents")
		}
		return []string{"settings.json"}, nil, nil
	}}

	command := m.startSync(&model.SyncOverrides{
		TargetAgents: []model.AgentID{model.AgentClaudeCode},
	})

	message, _ := command().(SyncDoneMsg)
	if message.Err != nil {
		t.Fatalf("detailed sync failed on a valid override: %v", message.Err)
	}
	if len(message.Files) != 1 || message.Files[0] != "settings.json" {
		t.Errorf("Files = %v, want the detailed result", message.Files)
	}
}
