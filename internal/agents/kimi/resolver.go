package kimi

import (
	"path/filepath"
)

const (
	// LegacyConfigDirName is the config root of the legacy Python/uv-based
	// Kimi CLI (~/.kimi).
	LegacyConfigDirName = ".kimi"

	// CurrentConfigDirName is the config root of the Node.js kimi-code CLI
	// v0.11+ (~/.kimi-code).
	CurrentConfigDirName = ".kimi-code"
)

// ConfigLayout identifies which Kimi CLI layout a resolved config root uses.
type ConfigLayout int

const (
	// LayoutLegacy is the Python/uv Kimi CLI layout rooted at ~/.kimi.
	// It keeps YAML agents (kimi --agent-file) and the shared skills path.
	LayoutLegacy ConfigLayout = iota

	// LayoutCurrent is the Node.js kimi-code v0.11+ layout rooted at
	// ~/.kimi-code. YAML agents were removed upstream in favor of native
	// Agent Skills under ~/.kimi-code/skills.
	LayoutCurrent
)

// statFunc reports the filesystem state of a path (exists / is directory).
type statFunc func(string) statResult

// resolveConfigRoot returns the Kimi config root for homeDir and the layout
// it represents. The current ~/.kimi-code root is preferred only when it
// exists as a directory; a plain file named .kimi-code is not a v0.11+ layout
// and is ignored. Everything else falls back to the legacy ~/.kimi root.
func resolveConfigRoot(statPath statFunc, homeDir string) (string, ConfigLayout) {
	current := filepath.Join(homeDir, CurrentConfigDirName)
	if stat := statPath(current); stat.err == nil && stat.isDir {
		return current, LayoutCurrent
	}
	return filepath.Join(homeDir, LegacyConfigDirName), LayoutLegacy
}
