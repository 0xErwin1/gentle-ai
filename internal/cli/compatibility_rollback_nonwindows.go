//go:build !windows

package cli

import "github.com/gentleman-programming/gentle-ai/v2/internal/backup"

func restoreCompatibilityEntries(manifest backup.Manifest, _ string) (backup.Manifest, error) {
	return manifest, nil
}
