package config

import (
	"slices"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestDecodeBackgroundIntent(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		want      model.OpenCodeBackgroundIntent
		wantCodes []string
	}{
		{
			name:     "accepts an explicit on choice",
			document: `{"version":"v1","selection":{"providers":{"opencode":{"backgroundIntent":"on"}}}}`,
			want:     model.OpenCodeBackgroundOn,
		},
		{
			name:     "accepts an explicit off choice",
			document: `{"version":"v1","selection":{"providers":{"opencode":{"backgroundIntent":"off"}}}}`,
			want:     model.OpenCodeBackgroundOff,
		},
		{
			name:     "accepts an explicit auto choice",
			document: `{"version":"v1","selection":{"providers":{"opencode":{"backgroundIntent":"auto"}}}}`,
			want:     model.OpenCodeBackgroundAuto,
		},
		{
			name:     "leaves an omitted choice unresolved rather than defaulting it",
			document: `{"version":"v1","selection":{}}`,
			want:     "",
		},
		{
			name:      "rejects an unsupported value with a stable diagnostic",
			document:  `{"version":"v1","selection":{"providers":{"opencode":{"backgroundIntent":"maybe"}}}}`,
			wantCodes: []string{"config.background-intent.unsupported"},
		},
		{
			name:      "rejects the intent for a provider with no background policy",
			document:  `{"version":"v1","selection":{"providers":{"codex":{"backgroundIntent":"on"}}}}`,
			wantCodes: []string{"config.provider.background-intent.unsupported-provider"},
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
			if len(test.wantCodes) > 0 {
				return
			}

			if got := Project(state).BackgroundIntent; got != test.want {
				t.Errorf("BackgroundIntent = %q, want %q", got, test.want)
			}
		})
	}
}

// The declarative document is the single source of truth when it is used, so an
// explicit choice must survive the round trip that carries desired state into
// the workflows the interactive and flag paths share.
func TestBackgroundIntentSurvivesSelectionRoundTrip(t *testing.T) {
	state, diagnostics := Decode([]byte(`{"version":"v1","selection":{"providers":{"opencode":{"backgroundIntent":"on"}}}}`))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	restored := FromSelection(Project(state))

	if got := restored.Selection.Providers[model.AgentOpenCode].BackgroundIntent; got != string(model.OpenCodeBackgroundOn) {
		t.Errorf("BackgroundIntent after round trip = %q, want %q", got, model.OpenCodeBackgroundOn)
	}
}
