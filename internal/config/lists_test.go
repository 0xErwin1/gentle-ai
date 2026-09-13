package config

import (
	"slices"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestDecodeOptionalLists(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		wantCodes []string
		assert    func(*testing.T, DesiredState)
	}{
		{
			name:     "accepts known community tools",
			document: `{"version":"v1","selection":{"communityTools":["codegraph"]}}`,
			assert: func(t *testing.T, state DesiredState) {
				if got := state.Selection.CommunityTools; !slices.Equal(got, []model.CommunityToolID{model.CommunityToolCodeGraph}) {
					t.Errorf("communityTools = %v", got)
				}
			},
		},
		{
			name:      "rejects an unknown community tool",
			document:  `{"version":"v1","selection":{"communityTools":["not-a-tool"]}}`,
			wantCodes: []string{"config.community-tool.unsupported"},
		},
		{
			name:     "accepts known OpenCode plugins",
			document: `{"version":"v1","selection":{"openCodePlugins":["gentle-logo","sub-agent-statusline"]}}`,
			assert: func(t *testing.T, state DesiredState) {
				want := []model.OpenCodeCommunityPluginID{model.OpenCodePluginGentleLogo, model.OpenCodePluginSubAgentStatusline}
				if got := state.Selection.OpenCodePlugins; !slices.Equal(got, want) {
					t.Errorf("openCodePlugins = %v, want %v", got, want)
				}
			},
		},
		{
			name:      "rejects an unknown OpenCode plugin",
			document:  `{"version":"v1","selection":{"openCodePlugins":["not-a-plugin"]}}`,
			wantCodes: []string{"config.opencode-plugin.unsupported"},
		},
		{
			name:     "collapses a repeated entry rather than acting on it twice",
			document: `{"version":"v1","selection":{"communityTools":["codegraph","codegraph"],"openCodePlugins":["gentle-logo","gentle-logo"]}}`,
			assert: func(t *testing.T, state DesiredState) {
				if got := len(state.Selection.CommunityTools); got != 1 {
					t.Errorf("communityTools length = %d, want 1", got)
				}
				if got := len(state.Selection.OpenCodePlugins); got != 1 {
					t.Errorf("openCodePlugins length = %d, want 1", got)
				}
			},
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

func TestOptionalListsSurviveSelectionRoundTrip(t *testing.T) {
	document := `{"version":"v1","selection":{"communityTools":["codegraph"],"openCodePlugins":["gentle-logo"]}}`

	state, diagnostics := Decode([]byte(document))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	restored := FromSelection(Project(state)).Selection

	if !slices.Equal(restored.CommunityTools, []model.CommunityToolID{model.CommunityToolCodeGraph}) {
		t.Errorf("CommunityTools = %v", restored.CommunityTools)
	}
	if !slices.Equal(restored.OpenCodePlugins, []model.OpenCodeCommunityPluginID{model.OpenCodePluginGentleLogo}) {
		t.Errorf("OpenCodePlugins = %v", restored.OpenCodePlugins)
	}
}
