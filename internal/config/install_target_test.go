package config

import (
	"slices"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestDecodeInstallTarget(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		wantCodes []string
		assert    func(*testing.T, DesiredState)
	}{
		{
			name:     "accepts a workspace scope",
			document: `{"version":"v1","selection":{"scope":"workspace"}}`,
			assert: func(t *testing.T, state DesiredState) {
				if got := state.Selection.Scope; got != model.InstallScopeWorkspace {
					t.Errorf("scope = %q, want %q", got, model.InstallScopeWorkspace)
				}
			},
		},
		{
			name:     "leaves an omitted scope unresolved so the flag and environment still apply",
			document: `{"version":"v1","selection":{}}`,
			assert: func(t *testing.T, state DesiredState) {
				if got := state.Selection.Scope; got != "" {
					t.Errorf("scope = %q, want empty", got)
				}
			},
		},
		{
			name:      "rejects an unsupported scope",
			document:  `{"version":"v1","selection":{"scope":"everywhere"}}`,
			wantCodes: []string{"config.scope.unsupported"},
		},
		{
			name:     "accepts a beta channel",
			document: `{"version":"v1","selection":{"channel":"beta"}}`,
			assert: func(t *testing.T, state DesiredState) {
				if got := state.Selection.Channel; got != model.InstallChannelBeta {
					t.Errorf("channel = %q, want %q", got, model.InstallChannelBeta)
				}
			},
		},
		{
			name:      "rejects an unsupported channel",
			document:  `{"version":"v1","selection":{"channel":"experimental"}}`,
			wantCodes: []string{"config.channel.unsupported"},
		},
		{
			name:      "reports both when scope and channel are wrong",
			document:  `{"version":"v1","selection":{"scope":"everywhere","channel":"experimental"}}`,
			wantCodes: []string{"config.scope.unsupported", "config.channel.unsupported"},
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

func TestInstallTargetSurvivesSelectionRoundTrip(t *testing.T) {
	document := `{"version":"v1","selection":{"scope":"workspace","channel":"beta"}}`

	state, diagnostics := Decode([]byte(document))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	restored := FromSelection(Project(state)).Selection

	if restored.Scope != model.InstallScopeWorkspace {
		t.Errorf("Scope = %q", restored.Scope)
	}
	if restored.Channel != model.InstallChannelBeta {
		t.Errorf("Channel = %q", restored.Channel)
	}
}
