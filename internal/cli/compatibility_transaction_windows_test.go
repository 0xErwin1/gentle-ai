//go:build windows

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/pipeline"
)

func createWindowsCompatibilityJunction(t *testing.T, link, target string) {
	t.Helper()
	output, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("create junction %q -> %q: %v: %s", link, target, err, output)
	}
}

func windowsCompatibilitySelection() model.Selection {
	return model.Selection{
		Agents:     []model.AgentID{model.AgentOpenCode},
		Components: []model.ComponentID{model.ComponentSkills},
		Skills:     []model.SkillID{model.SkillGoTesting},
		Persona:    model.PersonaNeutral,
	}
}

func installWindowsCompatibilityHook(t *testing.T, hook func(string)) {
	t.Helper()
	previous := windowsCompatibilityTransactionHook
	windowsCompatibilityTransactionHook = hook
	t.Cleanup(func() { windowsCompatibilityTransactionHook = previous })
}

func TestRunSyncWithSelectionRefreshesCompatibilityAndOpenCodeAssetsOnWindows(t *testing.T) {
	home := t.TempDir()
	compatibilityFiles := []string{
		filepath.Join(home, ".agents", "skills", "go-testing", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "go-testing", "references", "examples.md"),
	}
	for _, path := range compatibilityFiles {
		writeStale(t, path)
	}
	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "model-variants.ts")
	writeStale(t, pluginPath)

	if _, err := RunSyncWithSelection(home, windowsCompatibilitySelection()); err != nil {
		t.Fatalf("RunSyncWithSelection() error = %v", err)
	}
	for _, path := range append(compatibilityFiles, pluginPath) {
		content, err := os.ReadFile(path)
		if err != nil || string(content) == "stale" {
			t.Fatalf("managed asset %q was not refreshed: content=%q error=%v", path, content, err)
		}
	}
}

func assertWindowsCompatibilitySentinel(t *testing.T, path string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "outside" {
		t.Fatalf("outside sentinel %q changed: content=%q error=%v", path, content, err)
	}
}

func TestWindowsCompatibilityTransactionAllowsAbsentDirectory(t *testing.T) {
	home := t.TempDir()
	transaction, err := newCompatibilityRefreshTransaction(home, []model.ComponentID{model.ComponentSkills}, windowsCompatibilitySelection())
	if err != nil || transaction != nil {
		t.Fatalf("newCompatibilityRefreshTransaction() = %v, %v; want nil transaction and nil error", transaction, err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".agents", "skills")); !os.IsNotExist(statErr) {
		t.Fatalf("absent compatibility directory was created: %v", statErr)
	}
}

func TestWindowsCompatibilityTransactionRefusesRootParentAndNestedJunctions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		junctionAt string
	}{
		{name: "root", junctionAt: "skills"},
		{name: "parent", junctionAt: ".agents"},
		{name: "nested", junctionAt: "go-testing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, outside := t.TempDir(), t.TempDir()
			skillsDir := filepath.Join(home, ".agents", "skills")
			outsideSkill := filepath.Join(outside, "go-testing", "SKILL.md")
			switch tc.junctionAt {
			case "skills":
				if err := os.MkdirAll(filepath.Dir(skillsDir), 0o755); err != nil {
					t.Fatal(err)
				}
				createWindowsCompatibilityJunction(t, skillsDir, outside)
			case ".agents":
				if err := os.MkdirAll(home, 0o755); err != nil {
					t.Fatal(err)
				}
				createWindowsCompatibilityJunction(t, filepath.Join(home, ".agents"), outside)
				outsideSkill = filepath.Join(outside, "skills", "go-testing", "SKILL.md")
			case "go-testing":
				if err := os.MkdirAll(skillsDir, 0o755); err != nil {
					t.Fatal(err)
				}
				createWindowsCompatibilityJunction(t, filepath.Join(skillsDir, "go-testing"), outside)
			}
			if err := os.MkdirAll(filepath.Dir(outsideSkill), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(outsideSkill, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "model-variants.ts")
			writeStale(t, pluginPath)

			_, err := RunSyncWithSelection(home, windowsCompatibilitySelection())
			if err == nil || !strings.Contains(err.Error(), "physical directory") {
				t.Fatalf("RunSyncWithSelection() error = %v, want physical-directory refusal", err)
			}
			assertWindowsCompatibilitySentinel(t, outsideSkill)
			if content, readErr := os.ReadFile(pluginPath); readErr != nil || string(content) != "stale" {
				t.Fatalf("plugin refresh ran after compatibility refusal: content=%q error=%v", content, readErr)
			}
			if _, statErr := os.Stat(filepath.Join(home, ".gentle-ai", "backups")); !os.IsNotExist(statErr) {
				t.Fatalf("backup started after compatibility refusal: %v", statErr)
			}
		})
	}
}

func TestWindowsCompatibilityTransactionCannotFollowJunctionSwapBeforeSnapshot(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	skillsDir := filepath.Join(home, ".agents", "skills")
	writeStale(t, filepath.Join(skillsDir, "go-testing", "SKILL.md"))
	sentinel := filepath.Join(outside, "go-testing", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	swapped := false
	installWindowsCompatibilityHook(t, func(boundary string) {
		if boundary != compatibilityBeforeSnapshot || swapped {
			return
		}
		swapped = true
		if err := os.Rename(skillsDir, skillsDir+"-original"); err != nil {
			t.Fatal(err)
		}
		createWindowsCompatibilityJunction(t, skillsDir, outside)
	})
	transaction, err := newCompatibilityRefreshTransaction(home, []model.ComponentID{model.ComponentSkills}, windowsCompatibilitySelection())
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Close()
	if !swapped {
		t.Fatal("test did not reach the pre-snapshot boundary")
	}
	if err := transaction.Run(); err != nil {
		t.Fatal(err)
	}
	assertWindowsCompatibilitySentinel(t, sentinel)
}

func TestWindowsCompatibilityTransactionCannotFollowJunctionSwapBetweenSnapshotAndWrite(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	skillsDir := filepath.Join(home, ".agents", "skills")
	writeStale(t, filepath.Join(skillsDir, "go-testing", "SKILL.md"))
	sentinel := filepath.Join(outside, "go-testing", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	transaction, err := newCompatibilityRefreshTransaction(home, []model.ComponentID{model.ComponentSkills}, windowsCompatibilitySelection())
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Close()
	swapped := false
	installWindowsCompatibilityHook(t, func(boundary string) {
		if boundary != compatibilityBeforeWrite || swapped {
			return
		}
		swapped = true
		if err := os.Rename(skillsDir, skillsDir+"-original"); err != nil {
			t.Fatal(err)
		}
		createWindowsCompatibilityJunction(t, skillsDir, outside)
	})
	if err := transaction.Run(); err != nil {
		t.Fatal(err)
	}
	if !swapped {
		t.Fatal("test did not reach the post-snapshot boundary")
	}
	assertWindowsCompatibilitySentinel(t, sentinel)
}

func TestWindowsCompatibilityTransactionRollbackCannotFollowJunctionSwap(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	skillsDir := filepath.Join(home, ".agents", "skills")
	existing := filepath.Join(skillsDir, "go-testing", "SKILL.md")
	writeStale(t, existing)
	if err := os.Chmod(existing, 0o600); err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "go-testing", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}

	transaction, err := newCompatibilityRefreshTransaction(home, []model.ComponentID{model.ComponentSkills}, windowsCompatibilitySelection())
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Close()
	swapped := false
	installWindowsCompatibilityHook(t, func(boundary string) {
		if boundary != compatibilityAfterPublish || swapped {
			return
		}
		swapped = true
		if err := os.Rename(skillsDir, skillsDir+"-original"); err != nil {
			t.Fatal(err)
		}
		createWindowsCompatibilityJunction(t, skillsDir, outside)
	})
	if err := transaction.Run(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !swapped {
		t.Fatal("test did not reach the post-publication boundary")
	}
	originalExisting := filepath.Join(skillsDir+"-original", "go-testing", "SKILL.md")
	originalCreated := filepath.Join(skillsDir+"-original", "go-testing", "references", "examples.md")
	if content, readErr := os.ReadFile(originalExisting); readErr != nil || string(content) != "stale" {
		t.Fatalf("rollback did not restore original bytes: content=%q error=%v", content, readErr)
	}
	if info, statErr := os.Stat(originalExisting); statErr != nil || info.Mode().Perm() != beforeInfo.Mode().Perm() {
		t.Fatalf("rollback did not restore original mode: info=%v error=%v", info, statErr)
	}
	if _, statErr := os.Stat(originalCreated); !os.IsNotExist(statErr) {
		t.Fatalf("rollback left a transaction-created compatibility file: %v", statErr)
	}
	assertWindowsCompatibilitySentinel(t, sentinel)
}

func TestWindowsCompatibilityTransactionRollbackRemovesCreatedFilesAfterDuplicateBackup(t *testing.T) {
	home := temporaryUserHome(t)
	existing := filepath.Join(home, ".agents", "skills", "go-testing", "SKILL.md")
	created := filepath.Join(home, ".agents", "skills", "go-testing", "references", "examples.md")
	writeStale(t, existing)
	selection := model.Selection{Components: []model.ComponentID{model.ComponentSkills}, Skills: []model.SkillID{model.SkillGoTesting}}

	first, err := newSyncRuntime(home, selection)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.stagePlan().Prepare[0].Run(); err != nil {
		t.Fatal(err)
	}
	first.state.cleanupRollbackSnapshot()
	first.state.cleanupCompatibilityTransaction()

	runtime, err := newSyncRuntime(home, selection)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.state.cleanupCompatibilityTransaction()
	plan := runtime.stagePlan()
	plan.Apply = append(plan.Apply, failingCompatibilityStep{})
	result := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy()).Execute(plan)
	if result.Err == nil {
		t.Fatal("pipeline unexpectedly succeeded")
	}
	if content, readErr := os.ReadFile(existing); readErr != nil || string(content) != "stale" {
		t.Fatalf("rollback did not restore existing file: content=%q error=%v", content, readErr)
	}
	if _, statErr := os.Stat(created); !os.IsNotExist(statErr) {
		t.Fatalf("rollback left newly created compatibility file: %v", statErr)
	}
}
