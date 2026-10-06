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

func TestUserWindowsOfficialGoFixtureBangIsDataNotShellSelection(t *testing.T) {
	root := t.TempDir()
	if err := userWindowsPrivate(root); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "rsc.io_!c!g!o_v1.0.0.txt")
	const content = "authenticated Go fixture\n"
	if err := userWindowsWrite(name, []byte(content)); err != nil {
		t.Fatalf("official Go fixture name refused: %v", err)
	}
	got, err := userWindowsRead(name, int64(len(content)))
	if err != nil || string(got) != content {
		t.Fatalf("fixture readback differs: %q %v", got, err)
	}
	if err := userWindowsWrite(name, []byte("replacement")); err == nil {
		t.Fatal("fixture preimage was replaced")
	}
	badDirectory := filepath.Join(root, "directory!selection")
	if err := os.Mkdir(badDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := userWindowsIdentity(badDirectory, true); err == nil {
		t.Fatal("bang directory was admitted as a selection")
	}
	inside := filepath.Join(badDirectory, "ordinary.txt")
	if err := os.WriteFile(inside, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := userWindowsIdentity(inside, true); err == nil {
		t.Fatal("bang ancestor directory was admitted")
	}
	if _, err := InspectUserInstall(UserInstallRequest{Destination: filepath.Join(root, "Owned!Shell"), Mode: "separate"}); err == nil {
		t.Fatal("bang destination was admitted to command-binding review")
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
