package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/backup"
)

func codexCLIHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "localappdata"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg-data"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "missing"))
	original := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = original })
	return home
}

func externalCodexHome(t *testing.T) (string, string) {
	t.Helper()
	home := codexCLIHome(t)
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", canonical)
	return home, canonical
}

func TestCodexHomeStandaloneRestoreExternalRoot(t *testing.T) {
	home, root := externalCodexHome(t)
	path := filepath.Join(root, "config.toml")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := backup.NewSnapshotter().Create(filepath.Join(home, ".gentle-ai", "backups", "codex-root"), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RunRestore([]string{"codex-root", "--yes"}, &output); err != nil {
		t.Fatalf("restore: %v; output=%s", err, output.String())
	}
	wantOutput := fmt.Sprintf("restore complete — restored backup %s (%s)\n", manifest.ID, manifest.DisplayLabel())
	if output.String() != wantOutput {
		t.Fatalf("restore output = %q, want %q", output.String(), wantOutput)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "before\n" {
		t.Fatalf("restored config = %q, %v", raw, err)
	}
}

func TestCodexHomeRestoreStillRejectsUnrelatedRoot(t *testing.T) {
	home, _ := externalCodexHome(t)
	path := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := backup.NewSnapshotter().Create(filepath.Join(home, ".gentle-ai", "backups", "unrelated"), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = RunRestore([]string{"unrelated", "--yes"}, &output)
	wantError := fmt.Sprintf("restore failed: manifest entry has invalid OriginalPath %q: must be an absolute path under an allowed root (%s)", path, home)
	if err == nil || err.Error() != wantError {
		t.Fatalf("restore error = %v, want %q", err, wantError)
	}
	if output.Len() != 0 {
		t.Fatalf("rejected restore output = %q, want empty", output.String())
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "after" {
		t.Fatalf("unrelated file changed = %q, %v", raw, err)
	}
}

func TestCodexHomeRestorePreservesOrdinaryHomeScope(t *testing.T) {
	home, _ := externalCodexHome(t)
	path := filepath.Join(home, "ordinary.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := backup.NewSnapshotter().Create(filepath.Join(home, ".gentle-ai", "backups", "ordinary"), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RunRestore([]string{"ordinary", "--yes"}, &output); err != nil {
		t.Fatalf("ordinary restore: %v", err)
	}
	wantOutput := fmt.Sprintf("restore complete — restored backup %s (%s)\n", manifest.ID, manifest.DisplayLabel())
	if output.String() != wantOutput {
		t.Fatalf("restore output = %q, want %q", output.String(), wantOutput)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "before" {
		t.Fatalf("ordinary restored file = %q, %v", raw, err)
	}
}
