package config

import (
	"slices"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestDecodeRDDMode(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		want      model.RDDMode
		wantCodes []string
	}{
		{
			name:     "accepts an explicit on",
			document: `{"version":"v1","selection":{"rddMode":"on"}}`,
			want:     model.RDDModeOn,
		},
		{
			name:     "accepts an explicit off",
			document: `{"version":"v1","selection":{"rddMode":"off"}}`,
			want:     model.RDDModeOff,
		},
		{
			name:     "leaves an omitted mode unresolved rather than deciding review policy by silence",
			document: `{"version":"v1","selection":{}}`,
			want:     "",
		},
		{
			name:      "rejects an unsupported mode",
			document:  `{"version":"v1","selection":{"rddMode":"maybe"}}`,
			wantCodes: []string{"config.rdd-mode.unsupported"},
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

			if state.Selection.RDDMode != test.want {
				t.Errorf("rddMode = %q, want %q", state.Selection.RDDMode, test.want)
			}
		})
	}
}

func TestRDDModeSurvivesSelectionRoundTrip(t *testing.T) {
	state, diagnostics := Decode([]byte(`{"version":"v1","selection":{"rddMode":"off"}}`))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	if got := FromSelection(Project(state)).Selection.RDDMode; got != model.RDDModeOff {
		t.Errorf("RDDMode after round trip = %q, want %q", got, model.RDDModeOff)
	}
}
