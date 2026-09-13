package tui

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// The interactive pickers are another frontend over the desired-state model, so
// what they produce goes through the same normalization a declared document
// does. Without it a value chosen in a picker skips the validation a value
// written in a file receives, and the two paths stop being one source of truth.
func TestInteractiveOverridesAreNormalized(t *testing.T) {
	normalized, err := normalizeSyncOverrides(&model.SyncOverrides{
		TargetAgents:           []model.AgentID{model.AgentClaudeCode},
		ClaudePhaseAssignments: map[string]model.ClaudePhaseAssignment{"sdd-apply": {Model: model.ClaudeModelOpus, Effort: model.ClaudeEffortHigh}},
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if got := normalized.ClaudePhaseAssignments["sdd-apply"].Model; got != model.ClaudeModelOpus {
		t.Errorf("ClaudePhaseAssignments survived as %q", got)
	}
}

// Normalization fills defaults a document would receive. Writing those back
// would turn "no override" into an override and make a picker for one model
// silently rewrite persona, preset and the component set.
func TestNormalizationDoesNotInventOverrides(t *testing.T) {
	normalized, err := normalizeSyncOverrides(&model.SyncOverrides{
		TargetAgents:         []model.AgentID{model.AgentKiroIDE},
		KiroModelAssignments: map[string]model.KiroModelAlias{"sdd-apply": model.KiroModelDeepSeek},
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}

	if normalized.SDDMode != "" {
		t.Errorf("SDDMode became %q, want no override", normalized.SDDMode)
	}
	if normalized.StrictTDD != nil {
		t.Errorf("StrictTDD became %v, want no override", *normalized.StrictTDD)
	}
	if len(normalized.Profiles) != 0 {
		t.Errorf("Profiles became %v, want no override", normalized.Profiles)
	}
	if normalized.ModelAssignments != nil {
		t.Errorf("ModelAssignments became %v, want no override", normalized.ModelAssignments)
	}
}

// An unsupported value is reported rather than reaching sync, which is the
// validation the declarative path already gives.
func TestInteractiveOverridesRejectAnUnsupportedValue(t *testing.T) {
	_, err := normalizeSyncOverrides(&model.SyncOverrides{
		TargetAgents:           []model.AgentID{model.AgentClaudeCode},
		ClaudeModelAssignments: map[string]model.ClaudeModelAlias{"sdd-apply": "not-a-model"},
	})

	if err == nil {
		t.Fatal("an unsupported model reached sync unreported")
	}
}

// A nil override is the ordinary full sync and must stay nil.
func TestNilOverridesStayNil(t *testing.T) {
	normalized, err := normalizeSyncOverrides(nil)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if normalized != nil {
		t.Errorf("nil overrides became %+v", normalized)
	}
}

// What the seam is worth today is rejection: normalization validates these
// fields without transforming their values, so forwarding the normalized copy
// rather than the original is not observable from here and this does not claim
// to check it. Should normalization ever start canonicalising an assignment,
// that difference becomes observable and needs its own test.
func TestSyncRefusesAnOverrideTheContractRejects(t *testing.T) {
	reached := false
	m := Model{SyncFn: func(*model.SyncOverrides) ([]string, error) {
		reached = true

		return nil, nil
	}}

	command := m.startSync(&model.SyncOverrides{
		TargetAgents:           []model.AgentID{model.AgentClaudeCode},
		ClaudeModelAssignments: map[string]model.ClaudeModelAlias{"sdd-apply": "not-a-model"},
	})

	message, _ := command().(SyncDoneMsg)
	if message.Err == nil {
		t.Fatal("sync accepted an override the contract rejects")
	}
	if reached {
		t.Error("the unsupported override reached the sync function")
	}
}
