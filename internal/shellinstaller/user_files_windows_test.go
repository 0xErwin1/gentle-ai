//go:build windows

package shellinstaller

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserWindowsExclusiveFilesPreservePreimages(t *testing.T) {
	root := t.TempDir()
	if err := userWindowsPrivate(root); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "settings.json")
	if err := userWindowsWrite(name, []byte("personal preimage\n")); err != nil {
		t.Fatal(err)
	}
	if err := userWindowsWrite(name, []byte("replacement")); err == nil {
		t.Fatal("exclusive creation replaced a preimage")
	}
	got, err := userWindowsRead(name, 32)
	if err != nil || string(got) != "personal preimage\n" {
		t.Fatalf("preimage/readback differs: %q %v", got, err)
	}
	if _, err := userWindowsRead(name, 2); err == nil {
		t.Fatal("oversized read admitted")
	}
}

func TestUserWindowsSelectionRejectsAliasesAndShellPaths(t *testing.T) {
	root := t.TempDir()
	if err := userWindowsPrivate(root); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"relative", `\\server\share`, root + `\..`, root + "%PATH%", root + "\n"} {
		if _, err := userWindowsIdentity(bad, true); err == nil {
			t.Fatalf("noncanonical selection admitted: %q", bad)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skip("negative reparse test needs Windows symlink permission; other path negatives ran")
	}
	if _, err := userWindowsIdentity(alias, true); err == nil {
		t.Fatal("directory symlink admitted")
	}
}
