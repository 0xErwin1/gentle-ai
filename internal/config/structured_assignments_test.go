package config

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestDecodeStructuredAssignments(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		wantCodes []string
		assert    func(*testing.T, DesiredState)
	}{
		{
			name:     "accepts a Claude phase assignment with a compatible effort",
			document: `{"version":"v1","selection":{"claudePhaseAssignments":{"sdd-apply":{"model":"opus","effort":"high"}}}}`,
			assert: func(t *testing.T, state DesiredState) {
				got := state.Selection.ClaudePhaseAssignments["sdd-apply"]
				if want := (ClaudePhaseAssignment{Model: "opus", Effort: "high"}); got != want {
					t.Errorf("claudePhaseAssignments[sdd-apply] = %+v, want %+v", got, want)
				}
			},
		},
		{
			name:     "accepts a Claude phase assignment without an effort",
			document: `{"version":"v1","selection":{"claudePhaseAssignments":{"sdd-apply":{"model":"sonnet"}}}}`,
			assert: func(t *testing.T, state DesiredState) {
				if got := state.Selection.ClaudePhaseAssignments["sdd-apply"].Effort; got != "" {
					t.Errorf("Effort = %q, want empty", got)
				}
			},
		},
		{
			name:      "rejects an unsupported Claude model",
			document:  `{"version":"v1","selection":{"claudePhaseAssignments":{"sdd-apply":{"model":"gemini"}}}}`,
			wantCodes: []string{"config.claude-phase.unsupported"},
		},
		{
			name:      "rejects an effort the model does not support",
			document:  `{"version":"v1","selection":{"claudePhaseAssignments":{"sdd-apply":{"model":"haiku","effort":"high"}}}}`,
			wantCodes: []string{"config.claude-phase.effort-unsupported"},
		},
		{
			name:     "accepts a Codex orchestrator assignment",
			document: `{"version":"v1","selection":{"codexOrchestrator":{"model":"gpt-5.6-sol","effort":"medium"}}}`,
			assert: func(t *testing.T, state DesiredState) {
				got := state.Selection.CodexOrchestrator
				if got == nil {
					t.Fatal("codexOrchestrator was dropped")
				}
				if want := (CodexOrchestratorAssignment{Model: "gpt-5.6-sol", Effort: "medium"}); *got != want {
					t.Errorf("codexOrchestrator = %+v, want %+v", *got, want)
				}
			},
		},
		{
			name:      "rejects a Codex orchestrator without a model",
			document:  `{"version":"v1","selection":{"codexOrchestrator":{"effort":"medium"}}}`,
			wantCodes: []string{"config.codex-orchestrator.incomplete"},
		},
		{
			name:      "rejects an unsupported Codex orchestrator effort",
			document:  `{"version":"v1","selection":{"codexOrchestrator":{"model":"gpt-5.6-sol","effort":"extreme"}}}`,
			wantCodes: []string{"config.codex-orchestrator.effort-unsupported"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, diagnostics := Decode([]byte(test.document))

			codes := make([]string, 0, len(diagnostics))
			for _, diagnostic := range diagnostics {
				codes = append(codes, diagnostic.Code)
			}
			if !slices.Equal(codes, test.wantCodes) {
				t.Fatalf("diagnostics = %v, want %v", codes, test.wantCodes)
			}

			if test.assert != nil {
				test.assert(t, state)
			}
		})
	}
}

func TestStructuredAssignmentsSurviveSelectionRoundTrip(t *testing.T) {
	document := `{"version":"v1","selection":{"claudePhaseAssignments":{"sdd-apply":{"model":"opus","effort":"high"}},"codexOrchestrator":{"model":"gpt-5.6-sol","effort":"medium"}}}`

	state, diagnostics := Decode([]byte(document))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	projected := Project(state)
	if got := projected.ClaudePhaseAssignments["sdd-apply"]; got.Model != model.ClaudeModelOpus || got.Effort != model.ClaudeEffortHigh {
		t.Errorf("projected ClaudePhaseAssignments = %+v", got)
	}
	if projected.CodexOrchestratorAssignment == nil {
		t.Fatal("projected CodexOrchestratorAssignment is nil")
	}
	if got := *projected.CodexOrchestratorAssignment; got.Model != "gpt-5.6-sol" || got.Effort != model.CodexEffortMedium {
		t.Errorf("projected CodexOrchestratorAssignment = %+v", got)
	}

	restored := FromSelection(projected).Selection
	if got := restored.ClaudePhaseAssignments["sdd-apply"]; got != (ClaudePhaseAssignment{Model: "opus", Effort: "high"}) {
		t.Errorf("restored ClaudePhaseAssignments = %+v", got)
	}
	if restored.CodexOrchestrator == nil || *restored.CodexOrchestrator != (CodexOrchestratorAssignment{Model: "gpt-5.6-sol", Effort: "medium"}) {
		t.Errorf("restored CodexOrchestrator = %+v", restored.CodexOrchestrator)
	}
}

// An undeclared orchestrator must stay absent, the same guarantee the profile
// orchestrator carries: a struct value would publish an object of zero values.
func TestUndeclaredCodexOrchestratorIsOmitted(t *testing.T) {
	encoded, err := json.Marshal(Document{Version: CurrentVersion, Selection: Selection{}})
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}

	if strings.Contains(string(encoded), "codexOrchestrator") {
		t.Errorf("encoded document publishes an undeclared orchestrator: %s", encoded)
	}
}
