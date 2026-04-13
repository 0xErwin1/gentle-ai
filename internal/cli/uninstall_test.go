package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUninstallFlags(t *testing.T) {
	flags, err := ParseUninstallFlags([]string{"--dry-run", "--force", "--purge-memory", "--keep-backups"})
	if err != nil {
		t.Fatalf("ParseUninstallFlags() error = %v", err)
	}
	if !flags.DryRun || !flags.Force || !flags.PurgeMemory || !flags.KeepBackups {
		t.Fatalf("unexpected parsed flags: %+v", flags)
	}
}

func TestRunUninstallDryRunReportsTargets(t *testing.T) {
	home := t.TempDir()
	engramDir := filepath.Join(home, ".engram")
	if err := os.MkdirAll(engramDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".gentle-ai", "backups"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	var buf bytes.Buffer
	result, err := runUninstallWithHomeDir([]string{"--dry-run", "--force"}, &buf, strings.NewReader(""), home)
	if err != nil {
		t.Fatalf("runUninstallWithHomeDir() error = %v", err)
	}
	if !result.DryRun {
		t.Fatal("DryRun = false, want true")
	}
	if len(result.Targets) == 0 {
		t.Fatal("expected uninstall targets in dry-run")
	}
	if _, err := os.Stat(engramDir); err != nil {
		t.Fatalf("dry-run removed engram dir unexpectedly: %v", err)
	}
	output := RenderUninstallReport(result)
	if !strings.Contains(output, "Uninstall (dry-run)") {
		t.Fatalf("dry-run report missing header:\n%s", output)
	}
}

func TestRunUninstallRemovesManagedPaths(t *testing.T) {
	home := t.TempDir()
	paths := []string{
		filepath.Join(home, ".gentle-ai", "state.json"),
		filepath.Join(home, ".config", "gga", "config"),
		filepath.Join(home, ".config", "gga", "AGENTS.md"),
		filepath.Join(home, ".local", "share", "gga", "lib", "pr_mode.sh"),
		filepath.Join(home, ".config", "opencode", "commands", "sdd-init.md"),
		filepath.Join(home, ".config", "opencode", "skills", "sdd-init", "SKILL.md"),
		filepath.Join(home, ".local", "bin", "engram"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}

	result, err := runUninstallWithHomeDir([]string{"--force"}, ioDiscard{}, strings.NewReader(""), home)
	if err != nil {
		t.Fatalf("runUninstallWithHomeDir() error = %v", err)
	}
	if len(result.Removed) == 0 {
		t.Fatal("expected removed paths")
	}
	for _, path := range []string{
		filepath.Join(home, ".gentle-ai", "state.json"),
		filepath.Join(home, ".config", "gga", "config"),
		filepath.Join(home, ".config", "gga", "AGENTS.md"),
		filepath.Join(home, ".local", "share", "gga", "lib", "pr_mode.sh"),
		filepath.Join(home, ".config", "opencode", "commands", "sdd-init.md"),
		filepath.Join(home, ".config", "opencode", "skills", "sdd-init"),
		filepath.Join(home, ".local", "bin", "engram"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %q to be removed, stat err = %v", path, err)
		}
	}
}

func TestRunUninstallPreservesMemoryByDefault(t *testing.T) {
	home := t.TempDir()
	backupsDir := filepath.Join(home, ".gentle-ai", "backups")
	memoryDir := filepath.Join(home, ".engram")
	sharedSkillsDir := filepath.Join(home, ".config", "opencode", "skills")
	if err := os.MkdirAll(backupsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(backups) error = %v", err)
	}
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(memory) error = %v", err)
	}
	if err := os.MkdirAll(sharedSkillsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(shared skills) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gentle-ai", "state.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile(state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "engram.db"), []byte("memory"), 0o644); err != nil {
		t.Fatalf("WriteFile(memory) error = %v", err)
	}
	userSkill := filepath.Join(sharedSkillsDir, "my-custom-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatalf("MkdirAll(userSkill) error = %v", err)
	}
	if err := os.WriteFile(userSkill, []byte("custom"), 0o644); err != nil {
		t.Fatalf("WriteFile(userSkill) error = %v", err)
	}

	result, err := runUninstallWithHomeDir([]string{"--force", "--keep-backups"}, ioDiscard{}, strings.NewReader(""), home)
	if err != nil {
		t.Fatalf("runUninstallWithHomeDir() error = %v", err)
	}
	if _, err := os.Stat(backupsDir); err != nil {
		t.Fatalf("backups should be preserved: %v", err)
	}
	if _, err := os.Stat(memoryDir); err != nil {
		t.Fatalf("memory should be preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gentle-ai", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("state.json should be removed when keeping backups, stat err = %v", err)
	}
	if _, err := os.Stat(userSkill); err != nil {
		t.Fatalf("user-authored skill should be preserved: %v", err)
	}
	if !strings.Contains(RenderUninstallReport(result), "--purge-memory") {
		t.Fatalf("uninstall report should explain memory preservation opt-in:\n%s", RenderUninstallReport(result))
	}
}

func TestRunUninstallCancelledShowsCancelledMessage(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gentle-ai", "backups"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	// User types "no" at the confirmation prompt.
	var buf bytes.Buffer
	result, err := runUninstallWithHomeDir([]string{}, &buf, strings.NewReader("no\n"), home)
	if err != nil {
		t.Fatalf("runUninstallWithHomeDir() error = %v", err)
	}
	if !result.Cancelled {
		t.Fatal("Cancelled = false, want true")
	}
	output := RenderUninstallReport(result)
	if !strings.Contains(output, "cancelled") {
		t.Fatalf("cancelled report should say 'cancelled', got:\n%s", output)
	}
	// Targets should still be populated (planned but not executed).
	if len(result.Targets) == 0 {
		t.Fatal("cancelled result should still contain planned targets")
	}
	// Nothing should have been removed.
	if len(result.Removed) > 0 {
		t.Fatalf("cancelled result should not have removed paths, got: %v", result.Removed)
	}
}

func TestRunUninstallPurgeMemoryDeletesEngram(t *testing.T) {
	home := t.TempDir()
	memoryDir := filepath.Join(home, ".engram")
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(memory) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "engram.db"), []byte("memory"), 0o644); err != nil {
		t.Fatalf("WriteFile(memory) error = %v", err)
	}

	_, err := runUninstallWithHomeDir([]string{"--force", "--purge-memory"}, ioDiscard{}, strings.NewReader(""), home)
	if err != nil {
		t.Fatalf("runUninstallWithHomeDir() error = %v", err)
	}
	if _, err := os.Stat(memoryDir); !os.IsNotExist(err) {
		t.Fatalf("memory should be removed with --purge-memory, stat err = %v", err)
	}
}
