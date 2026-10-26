package cli

import (
	"context"
	"os"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"github.com/gentleman-programming/gentle-ai/v4/internal/opencode"
)

// The imperative pipeline classifies the V2 logo omission as skipped; staging
// the same component declaratively must accept it the same way rather than
// rejecting the whole document.
func TestStageOpenCodeLogoOnV2IsSkippedWithoutWrites(t *testing.T) {
	old := opencode.VersionRunnerOverride
	t.Cleanup(func() { opencode.VersionRunnerOverride = old })
	opencode.VersionRunnerOverride = func(context.Context, opencode.Command) (opencode.CommandOutput, error) {
		return opencode.CommandOutput{Stdout: []byte("2.1.1")}, nil
	}

	stageRoot := t.TempDir()
	if err := (configurationStager{}).stageComponent(model.ComponentOpenCodeGentleLogo, stageRoot, model.Selection{}, nil); err != nil {
		t.Fatalf("stageComponent() error = %v, want the V2 omission treated as skipped", err)
	}

	entries, err := os.ReadDir(stageRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("skipped logo staged files: %v %v", entries, err)
	}
}

func TestStageOpenCodeLogoOnV1StillStagesThePlugin(t *testing.T) {
	old := opencode.VersionRunnerOverride
	t.Cleanup(func() { opencode.VersionRunnerOverride = old })
	opencode.VersionRunnerOverride = func(context.Context, opencode.Command) (opencode.CommandOutput, error) {
		return opencode.CommandOutput{Stdout: []byte("1.18.29")}, nil
	}

	stageRoot := t.TempDir()
	if err := (configurationStager{}).stageComponent(model.ComponentOpenCodeGentleLogo, stageRoot, model.Selection{}, nil); err != nil {
		t.Fatalf("stageComponent() error = %v", err)
	}

	if _, err := os.Stat(stageRoot + "/.config/opencode/tui-plugins/gentle-logo.tsx"); err != nil {
		t.Fatalf("logo plugin not staged on OpenCode 1.x: %v", err)
	}
}
