//go:build linux

package shellinstaller

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func makePrivateJournalRoot(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "journal")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return parent, root
}

func TestOpenLinuxJournalRootPinsDescriptorIdentityAcrossRenameAndCloses(t *testing.T) {
	parent, path := makePrivateJournalRoot(t)
	root, err := OpenLinuxJournalRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	fd := root.fd
	defer root.Close()

	identity := root.Identity()
	if identity.Inode == 0 || identity.MountID == 0 {
		t.Fatalf("incomplete opened descriptor identity: %+v", identity)
	}
	var sx unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_NO_AUTOMOUNT,
		unix.STATX_BASIC_STATS|unix.STATX_MNT_ID, &sx); err != nil {
		t.Fatal(err)
	}
	if identity.Device != unix.Mkdev(sx.Dev_major, sx.Dev_minor) ||
		identity.Inode != sx.Ino || identity.MountID != sx.Mnt_id {
		t.Fatalf("identity was not read from the held descriptor: got %+v, statx=%+v", identity, sx)
	}

	// This proves the descriptor continues to reference its opened object;
	// it does not claim the descriptor pins the original pathname.
	renamedPath := filepath.Join(parent, "renamed-journal")
	if err := os.Rename(path, renamedPath); err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		t.Fatalf("descriptor did not survive rename: %v", err)
	}
	if st.Ino != identity.Inode || uint64(st.Dev) != identity.Device {
		t.Fatalf("descriptor identity changed across rename: got dev=%d ino=%d, want %+v", st.Dev, st.Ino, identity)
	}

	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fstat(fd, &st); !errors.Is(err, unix.EBADF) {
		t.Fatalf("Close left descriptor %d usable: %v", fd, err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
}

func TestOpenLinuxJournalRootRejectsInvalidPaths(t *testing.T) {
	cases := []struct {
		name string
		make func(*testing.T, string) string
	}{
		{"relative", func(_ *testing.T, _ string) string { return "relative/journal" }},
		{"root", func(_ *testing.T, _ string) string { return "/" }},
		{"unclean", func(_ *testing.T, parent string) string { return parent + "//journal" }},
		{"parent traversal", func(_ *testing.T, parent string) string {
			return parent + "/../" + filepath.Base(parent) + "/journal"
		}},
		{"embedded nul", func(_ *testing.T, parent string) string { return parent + "/journal\x00" }},
		{"missing", func(_ *testing.T, parent string) string { return filepath.Join(parent, "missing") }},
		{"nondirectory", func(t *testing.T, parent string) string {
			path := filepath.Join(parent, "file")
			if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			parent := t.TempDir()
			if _, err := OpenLinuxJournalRoot(tt.make(t, parent)); err == nil {
				t.Fatal("invalid journal root was accepted")
			}
		})
	}
}

func TestOpenLinuxJournalRootRejectsSymlinkRootAndAncestor(t *testing.T) {
	parent, rootPath := makePrivateJournalRoot(t)
	rootLink := filepath.Join(parent, "journal-link")
	if err := os.Symlink(rootPath, rootLink); err != nil {
		t.Fatal(err)
	}
	physicalParent := filepath.Join(parent, "physical")
	if err := os.Mkdir(physicalParent, 0o700); err != nil {
		t.Fatal(err)
	}
	physicalRoot := filepath.Join(physicalParent, "journal")
	if err := os.Mkdir(physicalRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	ancestorLink := filepath.Join(parent, "ancestor-link")
	if err := os.Symlink(physicalParent, ancestorLink); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{rootLink, filepath.Join(ancestorLink, "journal")} {
		t.Run(filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path), func(t *testing.T) {
			if _, err := OpenLinuxJournalRoot(path); err == nil {
				t.Fatal("symlinked journal root path was accepted")
			}
		})
	}
}

func TestOpenLinuxJournalRootRejectsGroupOrWorldAccess(t *testing.T) {
	_, path := makePrivateJournalRoot(t)
	if err := os.Chmod(path, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLinuxJournalRoot(path); err == nil {
		t.Fatal("group-accessible journal root was accepted")
	}
}

func TestOpenLinuxJournalRootRejectsOverlongPaths(t *testing.T) {
	path := "/" + strings.Repeat("a", 4096)
	if _, err := OpenLinuxJournalRoot(path); err == nil {
		t.Fatal("overlong journal root path was accepted")
	}
}
