//go:build !windows

package reviewtransaction

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// breakRARPrivateDirectoryMode makes every directory this package creates come
// back group- and world-readable, the way WSL DrvFS, exFAT, and SMB mounts
// without POSIX extensions behave: the mode handed to mkdir(2) is accepted and
// then ignored. No mode argument can produce this on ext4 or tmpfs, because
// mkdir only ever removes bits, so the primitive is swapped instead.
func breakRARPrivateDirectoryMode(t *testing.T) {
	t.Helper()
	previous := rarPrivateDirectoryMkdir
	rarPrivateDirectoryMkdir = func(name string, _ fs.FileMode) error {
		return os.Mkdir(name, 0o755)
	}
	t.Cleanup(func() { rarPrivateDirectoryMkdir = previous })
}

// breakRARPrivateDirectoryChmod completes the simulation for the mounts that
// also swallow chmod(2): the call reports success and changes nothing, so the
// directory can never be made safe from inside this process.
func breakRARPrivateDirectoryChmod(t *testing.T) {
	t.Helper()
	previous := rarPrivateDirectoryChmod
	rarPrivateDirectoryChmod = func(string, fs.FileMode) error { return nil }
	t.Cleanup(func() { rarPrivateDirectoryChmod = previous })
}

func TestCreatePrivateRARDirectorySecuresAModeThatDidNotStick(t *testing.T) {
	root := resolvedTempDir(t)
	path := filepath.Join(root, "v1")
	breakRARPrivateDirectoryMode(t)

	created, err := createPrivateRARDirectory(path)
	if err != nil {
		t.Fatalf("createPrivateRARDirectory on a filesystem that ignores the mkdir mode = %v, want the directory secured", err)
	}
	if !created {
		t.Fatalf("createPrivateRARDirectory created = false, want true for a directory it made")
	}
	info, statErr := os.Lstat(path)
	if statErr != nil {
		t.Fatalf("stat secured directory: %v", statErr)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("secured directory mode = %04o, want 0700", info.Mode().Perm())
	}
	if err := validatePrivateRARDirectory(path); err != nil {
		t.Fatalf("validate secured directory: %v", err)
	}
}

func TestCreatePrivateRARDirectoryKeepsTheUnsafePathItNames(t *testing.T) {
	root := resolvedTempDir(t)
	path := filepath.Join(root, "v1")
	breakRARPrivateDirectoryMode(t)
	breakRARPrivateDirectoryChmod(t)

	created, err := createPrivateRARDirectory(path)
	if created {
		t.Fatalf("createPrivateRARDirectory created = true for a directory it could not make safe")
	}
	var unsafePath *UnsafeRARPathError
	if !errors.As(err, &unsafePath) {
		t.Fatalf("createPrivateRARDirectory on an unrepairable filesystem = %v, want *UnsafeRARPathError", err)
	}
	if unsafePath.Path != path || !unsafePath.Directory {
		t.Fatalf("refusal names %q (directory=%t), want %q (directory=true)", unsafePath.Path, unsafePath.Directory, path)
	}
	// The refusal tells the operator to restore the mode on this exact path.
	// Removing it turned that instruction into "chmod: cannot access ...: No
	// such file or directory", which is the whole of issue #3285.
	if _, statErr := os.Lstat(unsafePath.Path); statErr != nil {
		t.Fatalf("refused path %q is gone, so the printed repair cannot run: %v", unsafePath.Path, statErr)
	}
	// Refusing is still the point: an unsafe path that cannot be made safe
	// must never be reported as usable.
	if validateErr := validatePrivateRARDirectory(path); validateErr == nil {
		t.Fatalf("validatePrivateRARDirectory accepted a world-readable directory")
	}
}

func TestCreatePrivateRARDirectoryNeverRepairsADirectoryItDidNotCreate(t *testing.T) {
	root := resolvedTempDir(t)
	path := filepath.Join(root, "v1")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	created, err := createPrivateRARDirectory(path)
	if created || err == nil {
		t.Fatalf("createPrivateRARDirectory on a pre-existing unsafe directory = (%t, %v), want (false, refusal)", created, err)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil {
		t.Fatalf("stat pre-existing directory: %v", statErr)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("pre-existing directory mode = %04o, want it left at 0755; this product does not rewrite permissions it refuses to trust", info.Mode().Perm())
	}
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	// The private-path walk opens every ancestor with O_NOFOLLOW, so a
	// symlinked temporary root would fail for a reason unrelated to the mode.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
