package tui

import (
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/planner"
	"github.com/gentleman-programming/gentle-ai/v2/internal/system"
)

func TestTUIProductionNormalizePlannerCanonicalizesDuplicateAgents(t *testing.T) {
	ui := NewModel(system.DetectionResult{}, "dev")
	ui.Selection.Agents = []model.AgentID{model.AgentOpenCode, model.AgentOpenCode}

	ui.buildDependencyPlan()

	if ui.Err != nil {
		t.Fatal(ui.Err)
	}
	want := []model.AgentID{model.AgentOpenCode}
	if !reflect.DeepEqual(ui.DependencyPlan.Agents, want) {
		t.Fatalf("planner agents = %#v, want %#v", ui.DependencyPlan.Agents, want)
	}
}

func TestTUIProductionNormalizeRejectsInvalidSelection(t *testing.T) {
	ui := NewModel(system.DetectionResult{}, "dev")
	ui.Selection.Components = []model.ComponentID{"unsupported"}

	ui.buildDependencyPlan()

	if ui.Err == nil {
		t.Fatal("buildDependencyPlan() error = nil, want invalid selection rejection")
	}
	if !reflect.DeepEqual(ui.DependencyPlan, planner.ResolvedPlan{}) {
		t.Fatalf("planner result = %#v, want no plan", ui.DependencyPlan)
	}
}
