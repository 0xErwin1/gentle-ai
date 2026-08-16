package config

import (
	"testing"
)

// Upstream retired the SDD component in v4: the SDD workflow ships as skills
// instead. A document that still requests the component must be refused
// clearly, while general planner fixtures stay on supported components.
func TestRetiredSDDComponentIsRefused(t *testing.T) {
	document := `{"version":"v1","selection":{"components":["sdd"]}}`

	_, diagnostics := Decode([]byte(document))
	if len(diagnostics) == 0 {
		t.Fatalf("Decode(components [sdd]) diagnostics = none, want a refusal")
	}
	if diagnostics[0].Code != "config.component.unsupported" {
		t.Fatalf("diagnostic code = %q, want config.component.unsupported", diagnostics[0].Code)
	}
}
