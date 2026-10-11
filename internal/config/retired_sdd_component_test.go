package config

import (
	"slices"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

// Upstream retired the SDD component: legacy install state still carries it,
// and install and sync treat it as a marker that writes nothing. Bridging an
// in-memory selection through the contract must not turn that marker into a
// refusal, or every sync of a home installed before the retirement fails
// before it writes anything.
func TestNormalizeSelectionPassesTheRetiredSDDMarkerThrough(t *testing.T) {
	selection := model.Selection{
		Agents:     []model.AgentID{model.AgentOpenCode},
		Components: []model.ComponentID{model.ComponentGGA, model.ComponentSDD},
	}

	normalized, diagnostics := NormalizeSelection(selection)
	if len(diagnostics) != 0 {
		t.Fatalf("NormalizeSelection(legacy sdd) diagnostics = %v, want none", diagnostics)
	}
	if !slices.Contains(normalized.Components, model.ComponentGGA) {
		t.Fatalf("NormalizeSelection dropped a supported component: %v", normalized.Components)
	}
	if !slices.Contains(normalized.Components, model.ComponentSDD) {
		t.Fatalf("NormalizeSelection dropped the legacy sdd marker the installer still reads: %v", normalized.Components)
	}

	// The marker alone selects nothing: it must not read as an empty list the
	// contract fills with preset defaults, which would write components the
	// legacy home never chose.
	onlyMarker, diagnostics := NormalizeSelection(model.Selection{
		Agents:     []model.AgentID{model.AgentOpenCode},
		Components: []model.ComponentID{model.ComponentSDD},
	})
	if len(diagnostics) != 0 {
		t.Fatalf("NormalizeSelection(only sdd) diagnostics = %v, want none", diagnostics)
	}
	if !slices.Equal(onlyMarker.Components, []model.ComponentID{model.ComponentSDD}) {
		t.Fatalf("NormalizeSelection(only sdd) components = %v, want exactly [sdd]", onlyMarker.Components)
	}
}

// A declarative document is new input, not legacy state: naming the retired
// component there is still refused.
func TestDocumentDeclaringTheRetiredSDDComponentIsRefused(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["opencode"],"components":["sdd"]}}`

	_, diagnostics := Decode([]byte(document))
	if len(diagnostics) == 0 {
		t.Fatalf("Decode(components [sdd]) diagnostics = none, want a refusal")
	}
	if diagnostics[0].Code != "config.component.unsupported" {
		t.Fatalf("diagnostic code = %q, want config.component.unsupported", diagnostics[0].Code)
	}
}
