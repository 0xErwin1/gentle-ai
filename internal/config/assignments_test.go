package config

import (
	"slices"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestDecodeModelAssignments(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		wantCodes []string
		assert    func(*testing.T, DesiredState)
	}{
		{
			name:     "accepts provider-qualified assignments per phase",
			document: `{"version":"v1","selection":{"modelAssignments":{"sdd-apply":{"provider":"anthropic","model":"claude-sonnet","effort":"high"}}}}`,
			assert: func(t *testing.T, state DesiredState) {
				got := state.Selection.ModelAssignments["sdd-apply"]
				if want := (ModelAssignment{Provider: "anthropic", Model: "claude-sonnet", Effort: "high"}); got != want {
					t.Errorf("modelAssignments[sdd-apply] = %+v, want %+v", got, want)
				}
			},
		},
		{
			name:      "rejects an assignment missing its provider",
			document:  `{"version":"v1","selection":{"modelAssignments":{"sdd-apply":{"model":"claude-sonnet"}}}}`,
			wantCodes: []string{"config.model-assignment.incomplete"},
		},
		{
			name:      "rejects an assignment missing its model",
			document:  `{"version":"v1","selection":{"modelAssignments":{"sdd-apply":{"provider":"anthropic"}}}}`,
			wantCodes: []string{"config.model-assignment.incomplete"},
		},
		{
			name:     "accepts Claude aliases per phase",
			document: `{"version":"v1","selection":{"claudeModelAssignments":{"sdd-apply":"opus"}}}`,
			assert: func(t *testing.T, state DesiredState) {
				if got := state.Selection.ClaudeModelAssignments["sdd-apply"]; got != model.ClaudeModelOpus {
					t.Errorf("claudeModelAssignments[sdd-apply] = %q, want %q", got, model.ClaudeModelOpus)
				}
			},
		},
		{
			name:      "rejects an unsupported Claude alias",
			document:  `{"version":"v1","selection":{"claudeModelAssignments":{"sdd-apply":"gemini"}}}`,
			wantCodes: []string{"config.claude-model.unsupported"},
		},
		{
			name:      "rejects an unsupported Kiro alias",
			document:  `{"version":"v1","selection":{"kiroModelAssignments":{"sdd-apply":"gemini"}}}`,
			wantCodes: []string{"config.kiro-model.unsupported"},
		},
		{
			name:     "accepts Codex efforts per phase",
			document: `{"version":"v1","selection":{"codexModelAssignments":{"sdd-apply":"xhigh"}}}`,
			assert: func(t *testing.T, state DesiredState) {
				if got := state.Selection.CodexModelAssignments["sdd-apply"]; got != model.CodexEffortXHigh {
					t.Errorf("codexModelAssignments[sdd-apply] = %q, want %q", got, model.CodexEffortXHigh)
				}
			},
		},
		{
			name:      "rejects an unsupported Codex effort",
			document:  `{"version":"v1","selection":{"codexModelAssignments":{"sdd-apply":"extreme"}}}`,
			wantCodes: []string{"config.codex-effort.unsupported"},
		},
		{
			name:     "accepts Codex carril and per-phase model ids",
			document: `{"version":"v1","selection":{"codexCarrilModelAssignments":{"sdd-strong":"gpt-5.6-sol"},"codexPhaseModelAssignments":{"sdd-apply":"gpt-5.6-luna"}}}`,
			assert: func(t *testing.T, state DesiredState) {
				if got := state.Selection.CodexCarrilModelAssignments["sdd-strong"]; got != "gpt-5.6-sol" {
					t.Errorf("codexCarrilModelAssignments[sdd-strong] = %q", got)
				}
				if got := state.Selection.CodexPhaseModelAssignments["sdd-apply"]; got != "gpt-5.6-luna" {
					t.Errorf("codexPhaseModelAssignments[sdd-apply] = %q", got)
				}
			},
		},
		{
			name:      "rejects an empty model id rather than storing a blank assignment",
			document:  `{"version":"v1","selection":{"codexPhaseModelAssignments":{"sdd-apply":""}}}`,
			wantCodes: []string{"config.codex-model.empty"},
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

// Every assignment surface must survive the trip into the shared semantic model
// and back, otherwise a declared value would validate and then be discarded.
func TestAssignmentsSurviveSelectionRoundTrip(t *testing.T) {
	document := `{"version":"v1","selection":{
		"modelAssignments":{"sdd-apply":{"provider":"anthropic","model":"claude-sonnet","effort":"high"}},
		"claudeModelAssignments":{"sdd-verify":"haiku"},
		"kiroModelAssignments":{"sdd-spec":"deepseek"},
		"codexModelAssignments":{"sdd-tasks":"low"},
		"codexCarrilModelAssignments":{"sdd-mid":"gpt-5.6-luna"},
		"codexPhaseModelAssignments":{"sdd-design":"gpt-5.6-sol"}}}`

	state, diagnostics := Decode([]byte(document))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	restored := FromSelection(Project(state)).Selection

	if got := restored.ModelAssignments["sdd-apply"]; got != (ModelAssignment{Provider: "anthropic", Model: "claude-sonnet", Effort: "high"}) {
		t.Errorf("ModelAssignments = %+v", got)
	}
	if got := restored.ClaudeModelAssignments["sdd-verify"]; got != model.ClaudeModelHaiku {
		t.Errorf("ClaudeModelAssignments = %q", got)
	}
	if got := restored.KiroModelAssignments["sdd-spec"]; got != model.KiroModelDeepSeek {
		t.Errorf("KiroModelAssignments = %q", got)
	}
	if got := restored.CodexModelAssignments["sdd-tasks"]; got != model.CodexEffortLow {
		t.Errorf("CodexModelAssignments = %q", got)
	}
	if got := restored.CodexCarrilModelAssignments["sdd-mid"]; got != "gpt-5.6-luna" {
		t.Errorf("CodexCarrilModelAssignments = %q", got)
	}
	if got := restored.CodexPhaseModelAssignments["sdd-design"]; got != "gpt-5.6-sol" {
		t.Errorf("CodexPhaseModelAssignments = %q", got)
	}
}
