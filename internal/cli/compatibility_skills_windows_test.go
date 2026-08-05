//go:build windows

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/backup"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func createWindowsJunction(t *testing.T, link, target string) {
	t.Helper()
	output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("create junction %q -> %q: %v: %s", link, target, err, output)
	}
}

func TestRunSyncWithSelectionRefusesWindowsCompatibilityRootAndParentJunctions(t *testing.T) {
	selection := model.Selection{
		Agents:     []model.AgentID{model.AgentOpenCode},
		Components: []model.ComponentID{model.ComponentSkills},
		Skills:     []model.SkillID{model.SkillGoTesting},
		Persona:    model.PersonaNeutral,
	}
	for _, tc := range []struct {
		name       string
		linkParent bool
	}{
		{name: "compatibility root"},
		{name: "compatibility parent", linkParent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, outside := t.TempDir(), t.TempDir()
			targetSkills := outside
			link := filepath.Join(home, ".agents", "skills")
			if tc.linkParent {
				targetSkills = filepath.Join(outside, "skills")
				link = filepath.Join(home, ".agents")
			}
			if err := os.MkdirAll(targetSkills, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			createWindowsJunction(t, link, outside)
			writeStale(t, filepath.Join(targetSkills, "go-testing", "SKILL.md"))

			pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "model-variants.ts")
			writeStale(t, pluginPath)
			_, err := RunSyncWithSelection(home, selection)
			if err == nil || !strings.Contains(err.Error(), "must be a physical directory") {
				t.Fatalf("RunSyncWithSelection() error = %v, want compatibility reparse refusal", err)
			}
			for _, path := range []string{
				filepath.Join(targetSkills, "go-testing", "SKILL.md"),
				pluginPath,
			} {
				content, readErr := os.ReadFile(path)
				if readErr != nil || string(content) != "stale" {
					t.Fatalf("refusal mutated %q: content=%q error=%v", path, content, readErr)
				}
			}
		})
	}
}

func TestCompatibilityRefreshRefusesWindowsNestedJunction(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	skillsDir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	createWindowsJunction(t, filepath.Join(skillsDir, "go-testing"), outside)
	path := filepath.Join(outside, "SKILL.md")
	writeStale(t, path)

	err := (compatibilitySkillsRefreshStep{
		homeDir:    home,
		components: []model.ComponentID{model.ComponentSkills},
		selection:  model.Selection{Skills: []model.SkillID{model.SkillGoTesting}},
	}).Run()
	if err == nil || !strings.Contains(err.Error(), "must be a physical directory") {
		t.Fatalf("Run() error = %v, want nested compatibility reparse refusal", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil || string(content) != "stale" {
		t.Fatalf("nested junction target was mutated: content=%q error=%v", content, readErr)
	}
}

func TestRollbackRefusesWindowsCompatibilityJunctionSwap(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	skillsDir := filepath.Join(home, ".agents", "skills")
	created := filepath.Join(skillsDir, "go-testing", "references", "examples.md")

	manifest, err := backup.NewSnapshotter().Create(filepath.Join(home, "snapshot"), []string{created})
	if err != nil {
		t.Fatal(err)
	}
	writeStale(t, created)
	if err := os.Rename(filepath.Join(skillsDir, "go-testing"), filepath.Join(skillsDir, "go-testing-original")); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "references", "examples.md")
	writeStale(t, outsideFile)
	createWindowsJunction(t, filepath.Join(skillsDir, "go-testing"), outside)

	err = (rollbackRestoreStep{
		state:   &runtimeState{manifest: manifest},
		homeDir: home,
	}).Rollback()
	if err == nil {
		t.Fatal("Rollback() succeeded after a compatibility subtree junction swap")
	}
	content, readErr := os.ReadFile(outsideFile)
	if readErr != nil || string(content) != "stale" {
		t.Fatalf("rollback mutated junction target: content=%q error=%v", content, readErr)
	}
}
