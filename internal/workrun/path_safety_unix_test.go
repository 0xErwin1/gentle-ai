//go:build !windows

package workrun

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecurePrivateWorkRunPathRepairsUnixModes(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateWorkRunDirectory(directory); err == nil {
		t.Fatal("world-accessible directory was accepted")
	}
	if err := securePrivateWorkRunPath(directory); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("secured directory mode = %o, want 700", got)
	}

	file := filepath.Join(directory, "file")
	if err := os.WriteFile(file, []byte("private\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateWorkRunFile(file); err == nil {
		t.Fatal("world-accessible file was accepted")
	}
	if err := securePrivateWorkRunPath(file); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("secured file mode = %o, want 600", got)
	}
}
