package config

import (
	"slices"
	"strings"
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
			document: `{"version":"v1","selection":{"providers":{"opencode":{"models":{"sdd-apply":{"provider":"anthropic","model":"claude-sonnet","effort":"high"}}}}}}`,
			assert: func(t *testing.T, state DesiredState) {
				got := Project(state).ModelAssignments["sdd-apply"]
				if want := (model.ModelAssignment{ProviderID: "anthropic", ModelID: "claude-sonnet", Effort: "high"}); got != want {
					t.Errorf("modelAssignments[sdd-apply] = %+v, want %+v", got, want)
				}
			},
		},
		{
			name:      "rejects an assignment missing its provider",
			document:  `{"version":"v1","selection":{"providers":{"opencode":{"models":{"sdd-apply":{"model":"claude-sonnet"}}}}}}`,
			wantCodes: []string{"config.model-assignment.incomplete"},
		},
		{
			name:      "rejects an assignment missing its model",
			document:  `{"version":"v1","selection":{"providers":{"opencode":{"models":{"sdd-apply":{"provider":"anthropic"}}}}}}`,
			wantCodes: []string{"config.model-assignment.incomplete"},
		},
		{
			name:     "accepts Claude aliases per phase",
			document: `{"version":"v1","selection":{"providers":{"claude-code":{"models":{"sdd-apply":"opus"}}}}}`,
			assert: func(t *testing.T, state DesiredState) {
				if got := Project(state).ClaudeModelAssignments["sdd-apply"]; got != model.ClaudeModelOpus {
					t.Errorf("claudeModelAssignments[sdd-apply] = %q, want %q", got, model.ClaudeModelOpus)
				}
			},
		},
		{
			name:      "rejects an unsupported Claude alias",
			document:  `{"version":"v1","selection":{"providers":{"claude-code":{"models":{"sdd-apply":"gemini"}}}}}`,
			wantCodes: []string{"config.claude-model.unsupported"},
		},
		{
			name:      "rejects an unsupported Kiro alias",
			document:  `{"version":"v1","selection":{"providers":{"kiro-ide":{"models":{"sdd-apply":"gemini"}}}}}`,
			wantCodes: []string{"config.kiro-model.unsupported"},
		},
		{
			name:     "accepts Codex efforts per phase",
			document: `{"version":"v1","selection":{"providers":{"codex":{"models":{"sdd-apply":"xhigh"}}}}}`,
			assert: func(t *testing.T, state DesiredState) {
				if got := Project(state).CodexModelAssignments["sdd-apply"]; got != model.CodexEffortXHigh {
					t.Errorf("codexModelAssignments[sdd-apply] = %q, want %q", got, model.CodexEffortXHigh)
				}
			},
		},
		{
			name:      "rejects an unsupported Codex effort",
			document:  `{"version":"v1","selection":{"providers":{"codex":{"models":{"sdd-apply":"extreme"}}}}}`,
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
		"providers":{
			"opencode":{"models":{"sdd-apply":{"provider":"anthropic","model":"claude-sonnet","effort":"high"}}},
			"claude-code":{"models":{"sdd-verify":"haiku"}},
			"kiro-ide":{"models":{"sdd-spec":"deepseek"}},
			"codex":{"models":{"sdd-tasks":"low"}}
		},
		"codexCarrilModelAssignments":{"sdd-mid":"gpt-5.6-luna"},
		"codexPhaseModelAssignments":{"sdd-design":"gpt-5.6-sol"}}}`

	state, diagnostics := Decode([]byte(document))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	restored := FromSelection(Project(state)).Selection

	opencode := restored.Providers[model.AgentOpenCode]
	var opencodeModels map[string]ModelAssignment
	if err := decodeStrict(opencode.Models, &opencodeModels); err != nil {
		t.Fatalf("decode opencode models: %v", err)
	}
	if got := opencodeModels["sdd-apply"]; got != (ModelAssignment{Provider: "anthropic", Model: "claude-sonnet", Effort: "high"}) {
		t.Errorf("ModelAssignments = %+v", got)
	}

	var claudeModels map[string]model.ClaudeModelAlias
	if err := decodeStrict(restored.Providers[model.AgentClaudeCode].Models, &claudeModels); err != nil {
		t.Fatalf("decode claude models: %v", err)
	}
	if got := claudeModels["sdd-verify"]; got != model.ClaudeModelHaiku {
		t.Errorf("ClaudeModelAssignments = %q", got)
	}

	var kiroModels map[string]model.KiroModelAlias
	if err := decodeStrict(restored.Providers[model.AgentKiroIDE].Models, &kiroModels); err != nil {
		t.Fatalf("decode kiro models: %v", err)
	}
	if got := kiroModels["sdd-spec"]; got != model.KiroModelDeepSeek {
		t.Errorf("KiroModelAssignments = %q", got)
	}

	var codexModels map[string]model.CodexEffort
	if err := decodeStrict(restored.Providers[model.AgentCodex].Models, &codexModels); err != nil {
		t.Fatalf("decode codex models: %v", err)
	}
	if got := codexModels["sdd-tasks"]; got != model.CodexEffortLow {
		t.Errorf("CodexModelAssignments = %q", got)
	}

	if got := restored.CodexCarrilModelAssignments["sdd-mid"]; got != "gpt-5.6-luna" {
		t.Errorf("CodexCarrilModelAssignments = %q", got)
	}
	if got := restored.CodexPhaseModelAssignments["sdd-design"]; got != "gpt-5.6-sol" {
		t.Errorf("CodexPhaseModelAssignments = %q", got)
	}
}

// A Pi profile's orchestrator and phase entries must be refused the same way
// an equivalent providers.pi.models entry already is: an unsafe model id, or
// an orchestrator missing its model.
func TestDecodeRejectsInvalidPiProfileValues(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		wantCodes []string
	}{
		{"unsafe orchestrator model id", `{"version":"v1","selection":{"providers":{"pi":{"profiles":{"deep":{"orchestrator":{"provider":"anthropic","model":"bad model"}}}}}}}`, []string{"config.pi-model.unsupported"}},
		{"orchestrator missing its model", `{"version":"v1","selection":{"providers":{"pi":{"profiles":{"deep":{"orchestrator":{"provider":"anthropic"}}}}}}}`, []string{"config.pi-profile.orchestrator-incomplete"}},
		{"unsafe phase assignment model id", `{"version":"v1","selection":{"providers":{"pi":{"profiles":{"deep":{"phaseAssignments":{"sdd-apply":{"provider":"anthropic","model":"bad model"}}}}}}}}`, []string{"config.pi-model.unsupported"}},
		{"phase assignment missing its model", `{"version":"v1","selection":{"providers":{"pi":{"profiles":{"deep":{"phaseAssignments":{"sdd-apply":{"provider":"anthropic"}}}}}}}}`, []string{"config.pi-profile.phase-incomplete"}},
		{"phase assignment missing its provider", `{"version":"v1","selection":{"providers":{"pi":{"profiles":{"deep":{"phaseAssignments":{"sdd-apply":{"model":"claude-haiku"}}}}}}}}`, []string{"config.pi-profile.phase-incomplete"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, diagnostics := Decode([]byte(test.document))
			codes := make([]string, 0, len(diagnostics))
			for _, d := range diagnostics {
				codes = append(codes, d.Code)
			}
			if !slices.Equal(codes, test.wantCodes) {
				t.Fatalf("diagnostics = %v, want %v", codes, test.wantCodes)
			}
		})
	}
}

// A Pi profile and its active selection must survive the round trip into the
// shared semantic model and back, the same way OpenCode profiles do.
func TestPiProfilesSurviveSelectionRoundTrip(t *testing.T) {
	document := `{"version":"v1","selection":{"providers":{"pi":{"activeProfile":"deep-work","profiles":{"deep-work":{"orchestrator":{"provider":"anthropic","model":"claude-sonnet","effort":"high"},"phaseAssignments":{"sdd-apply":{"provider":"anthropic","model":"claude-haiku"}}}}}}}}`

	state, diagnostics := Decode([]byte(document))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	pi := FromSelection(Project(state)).Selection.Providers[model.AgentPi]
	if pi.ActiveProfile != "deep-work" {
		t.Errorf("ActiveProfile = %q, want deep-work", pi.ActiveProfile)
	}
	profile, ok := pi.Profiles["deep-work"]
	if !ok {
		t.Fatal("Pi profile deep-work was dropped")
	}
	if profile.Orchestrator == nil || *profile.Orchestrator != (ModelAssignment{Provider: "anthropic", Model: "claude-sonnet", Effort: "high"}) {
		t.Errorf("Orchestrator = %+v", profile.Orchestrator)
	}
	if got := profile.PhaseAssignments["sdd-apply"]; got != (ModelAssignment{Provider: "anthropic", Model: "claude-haiku"}) {
		t.Errorf("PhaseAssignments[sdd-apply] = %+v", got)
	}
}

// A Pi package source override must be refused for any other provider, for a
// package name gentle-pi's own adapter does not install, and for a source that
// matches none of the shapes the adapter knows how to substitute.
func TestDecodeRejectsInvalidPiPackageOverrides(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		wantCodes []string
	}{
		{"packages on a non-pi provider", `{"version":"v1","selection":{"providers":{"opencode":{"packages":{"gentle-pi":"npm:gentle-pi@1.0.0"}}}}}`, []string{"config.provider.packages.unsupported-provider"}},
		{"unknown package name", `{"version":"v1","selection":{"providers":{"pi":{"packages":{"pi-subagents":"npm:pi-subagents@1.0.0"}}}}}`, []string{"config.pi-package.unknown"}},
		{"unsupported source shape", `{"version":"v1","selection":{"providers":{"pi":{"packages":{"gentle-pi":"1.0.0"}}}}}`, []string{"config.pi-package.source-unsupported"}},
		{"empty source", `{"version":"v1","selection":{"providers":{"pi":{"packages":{"gentle-pi":""}}}}}`, []string{"config.pi-package.source-unsupported"}},
		{"bare scheme prefix", `{"version":"v1","selection":{"providers":{"pi":{"packages":{"gentle-pi":"https://"}}}}}`, []string{"config.pi-package.source-unsupported"}},
		{"lone root slash", `{"version":"v1","selection":{"providers":{"pi":{"packages":{"gentle-pi":"/"}}}}}`, []string{"config.pi-package.source-unsupported"}},
		{"gentle-engram git shorthand", `{"version":"v1","selection":{"providers":{"pi":{"packages":{"gentle-engram":"git:github.com/Gentleman-Programming/gentle-engram@main"}}}}}`, []string{"config.pi-package.source-unsupported"}},
		{"gentle-engram https url", `{"version":"v1","selection":{"providers":{"pi":{"packages":{"gentle-engram":"https://example.com/gentle-engram.git"}}}}}`, []string{"config.pi-package.source-unsupported"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, diagnostics := Decode([]byte(test.document))
			codes := make([]string, 0, len(diagnostics))
			for _, d := range diagnostics {
				codes = append(codes, d.Code)
			}
			if !slices.Equal(codes, test.wantCodes) {
				t.Fatalf("diagnostics = %v, want %v", codes, test.wantCodes)
			}
		})
	}
}

// A Pi package source must accept every shape the adapter knows how to
// substitute: an npm spec with a version, a Pi git shorthand with a ref, a
// plain git URL, and an absolute local path.
func TestDecodeAcceptsEveryPiPackageSourceShape(t *testing.T) {
	tests := []string{
		"npm:gentle-pi@1.2.3",
		"git:github.com/Gentleman-Programming/gentle-pi@abc123",
		"https://example.com/gentle-pi.git",
		"ssh://git@example.com/gentle-pi.git",
		"/nix/store/xyz-gentle-engram-pi",
	}

	for _, source := range tests {
		t.Run(source, func(t *testing.T) {
			document := `{"version":"v1","selection":{"providers":{"pi":{"packages":{"gentle-pi":"` + source + `"}}}}}`
			_, diagnostics := Decode([]byte(document))
			if len(diagnostics) != 0 {
				t.Fatalf("unexpected diagnostics for source %q: %v", source, diagnostics)
			}
		})
	}
}

// Pi package source overrides must survive the trip into the shared semantic
// model and back, the same way every other Pi provider field already does.
func TestPiPackageSourcesSurviveSelectionRoundTrip(t *testing.T) {
	document := `{"version":"v1","selection":{"providers":{"pi":{"packages":{"gentle-pi":"git:github.com/Gentleman-Programming/gentle-pi@abc123","gentle-engram":"/nix/store/xyz-gentle-engram-pi"}}}}}`

	state, diagnostics := Decode([]byte(document))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	selection := Project(state)
	if got := selection.PiPackageSources["gentle-pi"]; got != "git:github.com/Gentleman-Programming/gentle-pi@abc123" {
		t.Errorf("PiPackageSources[gentle-pi] = %q", got)
	}
	if got := selection.PiPackageSources["gentle-engram"]; got != "/nix/store/xyz-gentle-engram-pi" {
		t.Errorf("PiPackageSources[gentle-engram] = %q", got)
	}

	restored := FromSelection(selection).Selection.Providers[model.AgentPi]
	if got := restored.Packages["gentle-pi"]; got != "git:github.com/Gentleman-Programming/gentle-pi@abc123" {
		t.Errorf("restored Packages[gentle-pi] = %q", got)
	}
	if got := restored.Packages["gentle-engram"]; got != "/nix/store/xyz-gentle-engram-pi" {
		t.Errorf("restored Packages[gentle-engram] = %q", got)
	}
}

// A document written for the pre-2.4.0 flat shape must be refused with one
// specific diagnostic per superseded key, naming its nested replacement,
// instead of the generic unknown-field diagnostic.
func TestDecodeRefusesSupersededFlatFields(t *testing.T) {
	tests := []struct {
		name         string
		document     string
		wantPath     string
		wantContains string
	}{
		{"modelAssignments", `{"version":"v1","selection":{"modelAssignments":{}}}`, "selection.modelAssignments", "providers.opencode.models"},
		{"claudeModelAssignments", `{"version":"v1","selection":{"claudeModelAssignments":{}}}`, "selection.claudeModelAssignments", "providers.claude-code.models"},
		{"kiroModelAssignments", `{"version":"v1","selection":{"kiroModelAssignments":{}}}`, "selection.kiroModelAssignments", "providers.kiro-ide.models"},
		{"piModelAssignments", `{"version":"v1","selection":{"piModelAssignments":{}}}`, "selection.piModelAssignments", "providers.pi.models"},
		{"codexModelAssignments", `{"version":"v1","selection":{"codexModelAssignments":{}}}`, "selection.codexModelAssignments", "providers.codex.models"},
		{"piModelFamily", `{"version":"v1","selection":{"piModelFamily":"turbo"}}`, "selection.piModelFamily", "providers.pi.modelFamily"},
		{"backgroundIntent", `{"version":"v1","selection":{"backgroundIntent":"auto"}}`, "selection.backgroundIntent", "providers.opencode.backgroundIntent"},
		{"piBackgroundIntent", `{"version":"v1","selection":{"piBackgroundIntent":"auto"}}`, "selection.piBackgroundIntent", "providers.pi.backgroundIntent"},
		{"modelPresets", `{"version":"v1","selection":{"modelPresets":{}}}`, "selection.modelPresets", "providers.<id>.modelPreset"},
		{"profiles", `{"version":"v1","selection":{"profiles":{}}}`, "selection.profiles", "providers.<id>.profiles"},
		{"sddProfileStrategy", `{"version":"v1","selection":{"sddProfileStrategy":"aggressive"}}`, "selection.sddProfileStrategy", "providers.opencode.profileStrategy"},
		{"skillAssignments", `{"version":"v1","selection":{"skillAssignments":{}}}`, "selection.skillAssignments", "providers.<id>.skills"},
		{"mcpServerAssignments", `{"version":"v1","selection":{"mcpServerAssignments":{}}}`, "selection.mcpServerAssignments", "providers.<id>.mcpServers"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, diagnostics := Decode([]byte(test.document))
			if len(diagnostics) != 1 {
				t.Fatalf("diagnostics = %v, want exactly one", diagnostics)
			}
			got := diagnostics[0]
			if got.Code != "config.document.superseded-field" || got.Path != test.wantPath {
				t.Fatalf("diagnostic = %+v, want code config.document.superseded-field path %q", got, test.wantPath)
			}
			if !strings.Contains(got.Message, test.wantContains) {
				t.Errorf("message = %q, want it to name %q", got.Message, test.wantContains)
			}
		})
	}
}

// A representative old flat v1 document must be refused entirely, and the
// same document rewritten in the nested providers shape must decode cleanly.
func TestDecodeRefusesAnOldFlatDocumentAndAcceptsItsNestedRewrite(t *testing.T) {
	flat := `{"version":"v1","selection":{
		"modelAssignments":{"sdd-apply":{"provider":"anthropic","model":"claude-sonnet"}},
		"claudeModelAssignments":{"sdd-verify":"haiku"},
		"backgroundIntent":"auto"
	}}`
	_, diagnostics := Decode([]byte(flat))
	codes := make([]string, 0, len(diagnostics))
	for _, d := range diagnostics {
		codes = append(codes, d.Code)
	}
	wantCodes := []string{"config.document.superseded-field", "config.document.superseded-field", "config.document.superseded-field"}
	if !slices.Equal(codes, wantCodes) {
		t.Fatalf("diagnostics = %v, want %v", codes, wantCodes)
	}

	nested := `{"version":"v1","selection":{
		"providers":{
			"opencode":{"models":{"sdd-apply":{"provider":"anthropic","model":"claude-sonnet"}},"backgroundIntent":"auto"},
			"claude-code":{"models":{"sdd-verify":"haiku"}}
		}
	}}`
	_, diagnostics = Decode([]byte(nested))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics for nested rewrite: %v", diagnostics)
	}
}
