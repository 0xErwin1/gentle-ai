package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// overrideHomeForBackup swaps UserHomeDirFn and BackupRootFn to the
// supplied temp dir, restoring the originals at test cleanup. Reused
// from the existing restore_test.go but reproduced here for readability
// of the new test file.
func overrideHomeForBackup(t *testing.T, dir string) {
	t.Helper()
	origHome := UserHomeDirFn
	origRoot := BackupRootFn
	UserHomeDirFn = func() (string, error) { return dir, nil }
	BackupRootFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() {
		UserHomeDirFn = origHome
		BackupRootFn = origRoot
	})
}

// TestSnapshotterClassifiesPreExistingDirectory verifies that a pre-existing
// empty directory is snapshotted with Kind=directory, Existed=true and is
// not removed during restore (acceptance criterion 2).
func TestSnapshotterClassifiesPreExistingDirectory(t *testing.T) {
	home := t.TempDir()
	overrideHomeForBackup(t, home)

	// Create an empty directory.
	existingDir := filepath.Join(home, "existing-dir")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	snapshotter := Snapshotter{now: func() time.Time { return time.Now() }}
	backupDir := filepath.Join(home, "backup")

	manifest, err := snapshotter.Create(backupDir, []string{existingDir})
	if err != nil {
		t.Fatalf("Snapshotter.Create() error = %v", err)
	}

	if len(manifest.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(manifest.Entries))
	}
	entry := manifest.Entries[0]
	if entry.Kind != PathKindDirectory {
		t.Fatalf("entry.Kind = %q, want %q", entry.Kind, PathKindDirectory)
	}
	if !entry.Existed {
		t.Fatalf("entry.Existed = false, want true")
	}

	// Restore should not remove the pre-existing directory.
	// Simulate a restore cycle: the directory should still exist.
	if err := os.Remove(existingDir); err != nil {
		t.Fatalf("Remove() before restore error = %v", err)
	}

	service := RestoreService{}
	if err := service.Restore(manifest); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	info, err := os.Lstat(existingDir)
	if err != nil {
		t.Fatalf("Lstat() after restore error = %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %q to be a directory after restore", existingDir)
	}
}

// TestSnapshotterClassifiesPreExistingSymlinkDirectory verifies that a
// pre-existing symlink to a directory is snapshotted with
// Kind=symlink_directory, LinkTarget set to the relative target, and
// Existed=true. Restore recreates the symlink if missing (acceptance
// criterion 3).
func TestSnapshotterClassifiesPreExistingSymlinkDirectory(t *testing.T) {
	home := t.TempDir()
	overrideHomeForBackup(t, home)

	// Create an actual directory and a relative symlink pointing to it.
	existingDir := filepath.Join(home, "existing-dir")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	symlinkPath := filepath.Join(home, "existing-dir-link")
	// os.Symlink("existing-dir", "existing-dir-link") creates a relative
	// symlink when the target doesn't contain a path separator.
	if err := os.Symlink("existing-dir", symlinkPath); err != nil {
		t.Skipf("Symlink not available on this system: %v", err)
	}

	snapshotter := Snapshotter{now: func() time.Time { return time.Now() }}
	backupDir := filepath.Join(home, "backup")

	manifest, err := snapshotter.Create(backupDir, []string{existingDir, symlinkPath})
	if err != nil {
		t.Fatalf("Snapshotter.Create() error = %v", err)
	}

	// Find the symlink entry.
	var symlinkEntry ManifestEntry
	for _, e := range manifest.Entries {
		if e.OriginalPath == symlinkPath {
			symlinkEntry = e
			break
		}
	}
	if symlinkEntry.OriginalPath == "" {
		t.Fatalf("symlink entry not found in manifest")
	}
	if symlinkEntry.Kind != PathKindSymlinkDirectory {
		t.Fatalf("symlinkEntry.Kind = %q, want %q", symlinkEntry.Kind, PathKindSymlinkDirectory)
	}
	if symlinkEntry.LinkTarget != "existing-dir" {
		t.Fatalf("symlinkEntry.LinkTarget = %q, want %q", symlinkEntry.LinkTarget, "existing-dir")
	}
	if !symlinkEntry.Existed {
		t.Fatalf("symlinkEntry.Existed = false, want true")
	}

	// Restore should recreate the symlink if missing.
	// Simulate by removing the symlink and restoring.
	if err := os.Remove(symlinkPath); err != nil {
		t.Fatalf("Remove() symlink before restore error = %v", err)
	}

	service := RestoreService{}
	if err := service.Restore(manifest); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	info, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("Lstat() symlink after restore error = %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %q to be a symlink after restore", symlinkPath)
	}
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}
	if target != "existing-dir" {
		t.Fatalf("symlink target = %q, want %q", target, "existing-dir")
	}
}

// TestRestoreRejectsUnsafeSymlinkTarget verifies that an absolute symlink
// target in the manifest is rejected outright during restore, preventing
// a tampered manifest from pointing outside the user home (acceptance
// criterion 3).
func TestRestoreRejectsUnsafeSymlinkTarget(t *testing.T) {
	home := t.TempDir()
	overrideHomeForBackup(t, home)

	manifest := Manifest{
		Compressed: false,
		Entries: []ManifestEntry{
			{
				OriginalPath: filepath.Join(home, "evil-link"),
				Existed:      true,
				Kind:         PathKindSymlinkDirectory,
				LinkTarget:   "/etc/passwd",
				Mode:         uint32(os.ModeSymlink | 0o777),
			},
		},
	}

	service := RestoreService{}
	err := service.Restore(manifest)
	if err == nil {
		t.Fatal("Restore() expected error for absolute symlink target, got nil")
	}
	if !contains(err.Error(), "absolute targets are not allowed") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "absolute targets are not allowed")
	}
}

// TestRestoreRejectsParentTraversalSymlinkTarget verifies that a symlink
// target containing ".." segments that would escape the link's parent
// directory is rejected during restore (acceptance criterion 3).
func TestRestoreRejectsParentTraversalSymlinkTarget(t *testing.T) {
	home := t.TempDir()
	overrideHomeForBackup(t, home)

	manifest := Manifest{
		Compressed: false,
		Entries: []ManifestEntry{
			{
				OriginalPath: filepath.Join(home, "traversal-link"),
				Existed:      true,
				Kind:         PathKindSymlinkDirectory,
				LinkTarget:   "../etc/passwd",
				Mode:         uint32(os.ModeSymlink | 0o777),
			},
		},
	}

	service := RestoreService{}
	err := service.Restore(manifest)
	if err == nil {
		t.Fatal("Restore() expected error for parent-traversal symlink target, got nil")
	}
	if !contains(err.Error(), "..") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "..")
	}
}

// TestLegacyManifestWithUnknownKindDoesNotDelete verifies that a manifest
// entry with Kind="" (the zero value, representing a legacy manifest
// written before PathKind existed) and Existed=false does NOT cause
// the file at OriginalPath to be deleted during restore. This is the
// safe compatibility policy for acceptance criterion 4.
func TestLegacyManifestWithUnknownKindDoesNotDelete(t *testing.T) {
	home := t.TempDir()
	overrideHomeForBackup(t, home)

	// Create a real file on disk that was NOT in the snapshot.
	keepFile := filepath.Join(home, "pre-existing", "keepme.txt")
	if err := os.MkdirAll(filepath.Dir(keepFile), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(keepFile, []byte("i was here before the backup\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Manifest with Kind="" (zero value = legacy unknown) and Existed=false.
	// This represents a pre-existing file whose type we cannot prove.
	manifest := Manifest{
		Compressed: false,
		Entries: []ManifestEntry{
			{
				OriginalPath: keepFile,
				Existed:      false,
				Kind:         PathKindUnknown, // zero value = legacy unknown
			},
		},
	}

	service := RestoreService{}
	if err := service.Restore(manifest); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	// The file should STILL exist — we never delete what we cannot prove.
	content, err := os.ReadFile(keepFile)
	if err != nil {
		t.Fatalf("ReadFile() after restore error = %v", err)
	}
	if string(content) != "i was here before the backup\n" {
		t.Fatalf("file content = %q, want %q", string(content), "i was here before the backup\n")
	}
}

// contains reports whether substr is within s.
func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
