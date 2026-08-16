package config_test

import (
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/planner"
	"github.com/gentleman-programming/gentle-ai/v2/internal/system"
	"github.com/gentleman-programming/gentle-ai/v2/internal/tui"
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

func TestInteractiveTUIFlagAndDeclarativeParity(t *testing.T) {
	detection := system.DetectionResult{}
	tuiSelection := tui.NewModel(detection, "dev").Selection
	flags, err := cli.ParseInstallFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	flagInput, err := cli.NormalizeInstallFlags(flags, detection)
	if err != nil {
		t.Fatal(err)
	}
	declarative := config.Project(config.FromSelection(tuiSelection))

	for name, selection := range map[string]model.Selection{
		"interactive": tuiSelection,
		"tui":         tuiSelection,
		"flags":       flagInput.Selection,
		"declarative": declarative,
	} {
		t.Run(name, func(t *testing.T) {
			if !reflect.DeepEqual(selection, tuiSelection) {
				t.Fatalf("selection = %#v, want %#v", selection, tuiSelection)
			}
			got, err := planner.NewResolver(planner.MVPGraph()).Resolve(selection)
			if err != nil {
				t.Fatal(err)
			}
			want, err := planner.NewResolver(planner.MVPGraph()).Resolve(tuiSelection)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("plan = %#v, want %#v", got, want)
			}
		})
	}
}
