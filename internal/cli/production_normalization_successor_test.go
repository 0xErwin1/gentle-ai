package cli

import (
	"os"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestSyncProductionNormalizeCanonicalizesDuplicateSelection(t *testing.T) {
	home := t.TempDir()

	result, err := RunSyncWithSelection(home, model.Selection{
		Skills: []model.SkillID{model.SkillSDDApply, model.SkillSDDApply},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []model.SkillID{model.SkillSDDApply}
	if !reflect.DeepEqual(result.Selection.Skills, want) {
		t.Fatalf("sync skills = %#v, want %#v", result.Selection.Skills, want)
	}
}

func TestSyncProductionNormalizeRejectsInvalidSelectionBeforeMutation(t *testing.T) {
	home := t.TempDir()

	_, err := RunSyncWithSelection(home, model.Selection{
		Agents:     []model.AgentID{model.AgentOpenCode},
		Components: []model.ComponentID{"unsupported"},
		Preset:     model.PresetCustom,
	})
	if err == nil {
		t.Fatal("RunSyncWithSelection() error = nil, want invalid selection rejection")
	}

	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("home entries = %v, want no mutation", entries)
	}
}
