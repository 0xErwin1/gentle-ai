package config

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
)

func TestExportPreservesRepresentableState(t *testing.T) {
	state := DesiredState{
		Version:   CurrentVersion,
		Selection: Selection{Agents: []model.AgentID{model.AgentOpenCode}},
		Roles:     []Role{{ID: "writer"}},
	}

	result := Export(state)

	if !result.Lossless {
		t.Fatalf("Export() lossless = false, diagnostics = %v", result.Diagnostics)
	}
	if result.Document.Version != CurrentVersion || len(result.Document.Roles) != 1 {
		t.Fatalf("document = %#v, want representable state", result.Document)
	}
}
