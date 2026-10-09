package cli

import (
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v4/internal/backup"
	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
)

// RestoreManagedBackup keeps generic restore defaults unchanged. A managed
// standalone restore may also use the current validated Codex home when an
// entry belongs there. Authority comes from the environment, never the manifest;
// RestoreService still enforces containment and symlink-escape checks.
func RestoreManagedBackup(manifest backup.Manifest) error {
	homeDir, err := backup.UserHomeDirFn()
	if err != nil {
		return err
	}
	roots := []string{homeDir}
	codexRoot := system.CodexConfigDir(homeDir)
	for _, entry := range manifest.Entries {
		if pathInsideCodexRoot(entry.OriginalPath, codexRoot) {
			if err := system.ValidateCodexHome(homeDir); err != nil {
				return err
			}
			roots = append(roots, codexRoot)
			break
		}
	}
	return (backup.RestoreService{Roots: roots}).Restore(manifest)
}

func pathInsideCodexRoot(path, root string) bool {
	if !filepath.IsAbs(path) || !filepath.IsAbs(root) {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
