package config_test

import (
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/planner"
	"github.com/gentleman-programming/gentle-ai/v2/internal/system"
)

func TestFromSelectionPreservesInstallAndSyncPlannerSemantics(t *testing.T) {
	install, err := cli.NormalizeInstallFlags(cli.InstallFlags{
		Agents:     []string{"opencode"},
		Components: []string{"engram", "sdd"},
		Persona:    "neutral",
		Preset:     "custom",
	}, system.DetectionResult{})
	if err != nil {
		t.Fatal(err)
	}

	sync := cli.BuildSyncSelection(cli.SyncFlags{SDDMode: "single"}, []model.AgentID{model.AgentOpenCode})

	for _, selection := range []model.Selection{install.Selection, sync} {
		state := config.FromSelection(selection)
		projected := config.Project(state)
		if !reflect.DeepEqual(projected, selection) {
			t.Fatalf("projected = %#v, want %#v", projected, selection)
		}

		gotPlan, err := planner.NewResolver(planner.MVPGraph()).Resolve(projected)
		if err != nil {
			t.Fatal(err)
		}
		wantPlan, err := planner.NewResolver(planner.MVPGraph()).Resolve(selection)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotPlan, wantPlan) {
			t.Fatalf("plan = %#v, want %#v", gotPlan, wantPlan)
		}
	}
}
