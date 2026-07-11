package opencode

import (
	"os"
	"path/filepath"
	"strings"
)

func ConfigPath(homeDir string) string {
	if xdgConfigHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdgConfigHome != "" {
		return filepath.Join(xdgConfigHome, "opencode")
	}
	return filepath.Join(homeDir, ".config", "opencode")
}
