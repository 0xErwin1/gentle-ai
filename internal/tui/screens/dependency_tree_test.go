package screens

import (
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/planner"
)

func TestRenderDependencyTreePiOnlyEmptyPlanShowsPiInstallCopy(t *testing.T) {
	selection := model.Selection{
		Agents: []model.AgentID{model.AgentPi},
		Preset: model.PresetFullGentleman,
	}
	plan := planner.ResolvedPlan{Agents: []model.AgentID{model.AgentPi}}

	out := RenderDependencyTree(plan, selection, 0)

	if strings.Contains(out, "No components selected yet.") {
		t.Fatalf("RenderDependencyTree() showed generic empty copy for Pi-only plan; output:\n%s", out)
	}
	for _, want := range []string{
		"Pi agent support will be installed.",
		"pi install npm:gentle-pi",
		"pi install npm:gentle-engram",
		"pi install npm:pi-subagents",
		"pi install npm:pi-intercom",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderDependencyTree() missing %q for Pi-only plan; output:\n%s", want, out)
		}
	}
}

func TestRenderDependencyTreeGenericEmptyPlanKeepsExistingCopy(t *testing.T) {
	selection := model.Selection{Preset: model.PresetFullGentleman}

	out := RenderDependencyTree(planner.ResolvedPlan{}, selection, 0)

	if !strings.Contains(out, "No components selected yet.") {
		t.Fatalf("RenderDependencyTree() missing generic empty copy; output:\n%s", out)
	}
	if strings.Contains(out, "Pi agent support will be installed.") {
		t.Fatalf("RenderDependencyTree() showed Pi copy for generic empty plan; output:\n%s", out)
	}
}

func TestRenderDependencyTreeMixedPiEmptyPlanKeepsGenericCopy(t *testing.T) {
	selection := model.Selection{
		Agents: []model.AgentID{model.AgentPi, model.AgentOpenCode},
		Preset: model.PresetFullGentleman,
	}
	plan := planner.ResolvedPlan{Agents: selection.Agents}

	out := RenderDependencyTree(plan, selection, 0)

	if !strings.Contains(out, "No components selected yet.") {
		t.Fatalf("RenderDependencyTree() missing generic empty copy for mixed Pi plan; output:\n%s", out)
	}
	if strings.Contains(out, "Pi agent support will be installed.") {
		t.Fatalf("RenderDependencyTree() showed exact Pi-only copy for mixed Pi plan; output:\n%s", out)
	}
}
