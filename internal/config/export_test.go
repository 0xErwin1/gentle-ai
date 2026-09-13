package config

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestExportPreservesRepresentableState(t *testing.T) {
	state := DesiredState{
		Version:   CurrentVersion,
		Selection: Selection{Agents: []model.AgentID{model.AgentOpenCode}},
	}

	result := Export(state)

	if !result.Lossless {
		t.Fatalf("Export() lossless = false, diagnostics = %v", result.Diagnostics)
	}
	if result.Document.Version != CurrentVersion || len(result.Document.Selection.Agents) != 1 {
		t.Fatalf("document = %#v, want representable state", result.Document)
	}
}
