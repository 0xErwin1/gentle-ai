//go:build windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gentleman-programming/gentle-ai/v2/internal/backup"
)

// restoreCompatibilityEntries restores compatibility paths through the
// descriptor-anchored writer and returns the entries safe for generic restore.
func restoreCompatibilityEntries(manifest backup.Manifest, homeDir string) (backup.Manifest, error) {
	skillDir := filepath.Join(homeDir, ".agents", "skills")
	compatibility, remaining := splitCompatibilityEntries(manifest, skillDir)
	if len(compatibility.Entries) == 0 {
		return manifest, nil
	}

	writer, err := newCompatibilityDirectoryWriter(homeDir, skillDir)
	if err != nil {
		return backup.Manifest{}, fmt.Errorf("open compatibility rollback root: %w", err)
	}
	defer writer.Close()

	snapshots, cleanupSnapshots, err := compatibilityRollbackSnapshots(compatibility)
	if err != nil {
		return backup.Manifest{}, err
	}
	defer cleanupSnapshots()

	for _, entry := range compatibility.Entries {
		if !entry.Existed {
			if err := writer.Remove(entry.OriginalPath); err != nil {
				return backup.Manifest{}, fmt.Errorf("remove compatibility rollback destination %q: %w", entry.OriginalPath, err)
			}
			continue
		}

		snapshotPath, ok := snapshots[filepath.ToSlash(entry.SnapshotPath)]
		if !ok {
			// refusal:by-design operator-knowledge: a transaction snapshot missing its declared entry cannot be safely reconstructed during rollback.
			return backup.Manifest{}, fmt.Errorf("compatibility rollback snapshot for %q is missing", entry.OriginalPath)
		}
		content, err := os.ReadFile(snapshotPath)
		if err != nil {
			return backup.Manifest{}, fmt.Errorf("read compatibility rollback snapshot %q: %w", snapshotPath, err)
		}
		if _, err := writer.Write(entry.OriginalPath, content, os.FileMode(entry.Mode)); err != nil {
			return backup.Manifest{}, fmt.Errorf("restore compatibility rollback destination %q: %w", entry.OriginalPath, err)
		}
	}

	return remaining, nil
}

func splitCompatibilityEntries(manifest backup.Manifest, skillDir string) (backup.Manifest, backup.Manifest) {
	compatibility := manifest
	compatibility.Entries = nil
	remaining := manifest
	remaining.Entries = nil
	for _, entry := range manifest.Entries {
		if _, err := compatibilityPathParts(skillDir, entry.OriginalPath); err == nil {
			compatibility.Entries = append(compatibility.Entries, entry)
			continue
		}
		remaining.Entries = append(remaining.Entries, entry)
	}
	return compatibility, remaining
}

func compatibilityRollbackSnapshots(manifest backup.Manifest) (map[string]string, func(), error) {
	snapshots := make(map[string]string)
	for _, entry := range manifest.Entries {
		if entry.Existed && !manifest.Compressed {
			// refusal:by-design operator-knowledge: compatibility rollback only accepts current transaction archives, not legacy plain snapshot layouts.
			return nil, nil, fmt.Errorf("compatibility rollback for %q has an unsupported uncompressed snapshot", entry.OriginalPath)
		}
	}
	if !manifest.Compressed {
		return snapshots, func() {}, nil
	}

	tempDir, err := os.MkdirTemp("", "gentle-ai-compatibility-rollback-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create compatibility rollback snapshot directory: %w", err)
	}
	entries, err := backup.ExtractArchive(filepath.Join(manifest.RootDir, backup.ArchiveFilename), tempDir)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, nil, fmt.Errorf("extract compatibility rollback snapshot: %w", err)
	}
	for _, entry := range entries {
		snapshots[entry.RelPath] = entry.SourcePath
	}
	return snapshots, func() { _ = os.RemoveAll(tempDir) }, nil
}
