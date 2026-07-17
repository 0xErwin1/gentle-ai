package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/pipeline"
	"github.com/gentleman-programming/gentle-ai/internal/planner"
)

func writeStale(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompatibilitySkillsRefreshStepRefreshesExistingOrdinaryAndSDDAssets(t *testing.T) {
	home := t.TempDir()
	skillsDir := filepath.Join(home, ".agents", "skills")
	staleFiles := []string{
		filepath.Join(skillsDir, "go-testing", "SKILL.md"),
		filepath.Join(skillsDir, "sdd-apply", "SKILL.md"),
		filepath.Join(skillsDir, "judgment-day", "SKILL.md"),
		filepath.Join(skillsDir, "_shared", "persistence-contract.md"),
	}
	for _, path := range staleFiles {
		writeStale(t, path)
	}
	step := compatibilitySkillsRefreshStep{homeDir: home, components: []model.ComponentID{model.ComponentSkills, model.ComponentSDD}, selection: model.Selection{Skills: []model.SkillID{model.SkillGoTesting}}}
	if err := step.Run(); err != nil {
		t.Fatal(err)
	}
	for _, path := range staleFiles {
		if content, err := os.ReadFile(path); err != nil || string(content) == "stale" {
			t.Errorf("%q was not refreshed: %v", path, err)
		}
	}
}

func TestRunSyncDryRunMatchesZeroAgentCompatibilityRefresh(t *testing.T) {
	home := t.TempDir()
	setSyncTestHome(t, home)
	if noRefresh, err := RunSync([]string{"--dry-run"}); err != nil || !noRefresh.NoOp {
		t.Fatalf("zero-agent dry-run without compatibility work: no-op=%t, err=%v", noRefresh.NoOp, err)
	}
	path := filepath.Join(home, ".agents", "skills", "go-testing", "SKILL.md")
	writeStale(t, path)
	dryRun, err := RunSync([]string{"--dry-run"})
	if err != nil || dryRun.NoOp || !slices.ContainsFunc(dryRun.Plan.Apply, func(step pipeline.Step) bool { return step.ID() == "sync:compatibility-skills-refresh" }) {
		t.Fatalf("compatibility refresh plan missing without agents: no-op=%t, err=%v", dryRun.NoOp, err)
	}
	result, err := RunSyncWithSelection(home, model.Selection{Components: []model.ComponentID{model.ComponentSkills}, Skills: []model.SkillID{model.SkillGoTesting}})
	content, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || string(content) == "stale" || result.NoOp {
		t.Fatalf("compatibility skill was not refreshed without agents; no-op=%t, sync=%v, read=%v", result.NoOp, err, readErr)
	}
}

func TestCompatibilitySkillsRefreshRequiresPhysicalDirectory(t *testing.T) {
	selection := model.Selection{Skills: []model.SkillID{model.SkillGoTesting}}
	t.Run("absent", func(t *testing.T) {
		home := t.TempDir()
		step := compatibilitySkillsRefreshStep{homeDir: home, components: []model.ComponentID{model.ComponentSkills, model.ComponentSDD}, selection: selection}
		if err := step.Run(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(home, ".agents", "skills")); !os.IsNotExist(err) {
			t.Fatalf("compatibility directory was created: %v", err)
		}
	})
	t.Run("root symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation may require elevated privileges")
		}
		home, target := t.TempDir(), t.TempDir()
		path := filepath.Join(target, "go-testing", "SKILL.md")
		writeStale(t, path)
		if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(home, ".agents", "skills")); err != nil {
			t.Fatal(err)
		}
		step := compatibilitySkillsRefreshStep{homeDir: home, components: []model.ComponentID{model.ComponentSkills}, selection: selection}
		if err := step.Run(); err != nil {
			t.Fatal(err)
		}
		if content, err := os.ReadFile(path); err != nil || string(content) != "stale" {
			t.Fatalf("symlink target changed without backup: %v", err)
		}
	})
}

func TestCompatibilitySkillFilesAreInstallAndSyncBackupTargets(t *testing.T) {
	home := t.TempDir()
	skillsDir := filepath.Join(home, ".agents", "skills")
	files := []string{
		filepath.Join(skillsDir, "go-testing", "SKILL.md"),
		filepath.Join(skillsDir, "go-testing", "references", "example.md"),
		filepath.Join(skillsDir, "sdd-apply", "SKILL.md"),
		filepath.Join(skillsDir, "_shared", "persistence-contract.md"),
	}
	for _, path := range files {
		writeStale(t, path)
	}
	selection := model.Selection{Components: []model.ComponentID{model.ComponentSkills, model.ComponentSDD}, Skills: []model.SkillID{model.SkillGoTesting}}
	resolved := planner.ResolvedPlan{OrderedComponents: selection.Components}
	for _, targets := range [][]string{backupTargets(home, "", ScopeGlobal, selection, resolved), syncBackupTargets(home, "", selection, nil)} {
		for _, path := range files {
			if !slices.Contains(targets, path) {
				t.Errorf("backup targets missing %q; targets=%v", path, targets)
			}
		}
	}
}

func TestStagePlansRefreshCompatibilitySkillsOncePerOperation(t *testing.T) {
	selection := model.Selection{Agents: []model.AgentID{model.AgentClaudeCode, model.AgentCodex}, Components: []model.ComponentID{model.ComponentSkills, model.ComponentSDD}, Skills: []model.SkillID{model.SkillGoTesting}}
	resolved := planner.ResolvedPlan{Agents: selection.Agents, OrderedComponents: selection.Components}
	plans := []pipeline.StagePlan{
		(&installRuntime{selection: selection, resolved: resolved, state: &runtimeState{}}).stagePlan(),
		(&syncRuntime{selection: selection, agentIDs: selection.Agents, state: &runtimeState{}}).stagePlan(),
	}
	for _, plan := range plans {
		count := 0
		for _, step := range plan.Apply {
			if step.ID() == "component:compatibility-skills-refresh" || step.ID() == "sync:compatibility-skills-refresh" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("compatibility refresh count=%d, want 1", count)
		}
	}
}
