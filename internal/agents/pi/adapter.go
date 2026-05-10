// Package pi provides Pi CLI agent integration.
package pi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/system"
)

type statResult struct {
	isDir bool
	err   error
}

// Adapter implements agents.Adapter for Pi.
type Adapter struct {
	lookPath func(string) (string, error)
	statPath func(string) statResult
}

// NewAdapter creates a Pi adapter instance.
func NewAdapter() *Adapter {
	return &Adapter{
		lookPath: exec.LookPath,
		statPath: defaultStat,
	}
}

func (a *Adapter) Agent() model.AgentID { return model.AgentPi }

func (a *Adapter) Tier() model.SupportTier { return model.TierFull }

func (a *Adapter) Detect(_ context.Context, homeDir string) (bool, string, string, bool, error) {
	configPath := ConfigPath(homeDir)
	binaryPath, err := a.lookPath("pi")
	installed := err == nil && binaryPath != ""

	stat := a.statPath(configPath)
	if stat.err != nil {
		if os.IsNotExist(stat.err) {
			return installed, binaryPath, configPath, false, nil
		}
		return false, "", "", false, stat.err
	}

	return installed, binaryPath, configPath, stat.isDir, nil
}

func (a *Adapter) SupportsAutoInstall() bool { return true }

func (a *Adapter) InstallCommand(system.PlatformProfile) ([][]string, error) {
	return [][]string{
		{"pi", "install", "npm:gentle-pi"},
		{"pi", "install", "npm:gentle-engram"},
		{"pi", "install", "npm:pi-subagents"},
		{"pi", "install", "npm:pi-intercom"},
	}, nil
}

func (a *Adapter) GlobalConfigDir(homeDir string) string { return ConfigPath(homeDir) }

func (a *Adapter) SystemPromptDir(string) string { return "" }

func (a *Adapter) SystemPromptFile(string) string { return "" }

func (a *Adapter) SkillsDir(string) string { return "" }

func (a *Adapter) SettingsPath(homeDir string) string { return ConfigPath(homeDir) }

func (a *Adapter) SystemPromptStrategy() model.SystemPromptStrategy {
	return model.StrategyAppendToFile
}

func (a *Adapter) MCPStrategy() model.MCPStrategy { return model.StrategyMCPConfigFile }

func (a *Adapter) MCPConfigPath(string, string) string { return "" }

func (a *Adapter) SupportsOutputStyles() bool { return false }

func (a *Adapter) OutputStyleDir(string) string { return "" }

func (a *Adapter) SupportsSlashCommands() bool { return false }

func (a *Adapter) CommandsDir(string) string { return "" }

func (a *Adapter) SupportsSubAgents() bool { return false }

func (a *Adapter) SubAgentsDir(string) string { return "" }

func (a *Adapter) EmbeddedSubAgentsDir() string { return "" }

func (a *Adapter) SupportsSkills() bool { return false }

func (a *Adapter) SupportsSystemPrompt() bool { return false }

func (a *Adapter) SupportsMCP() bool { return false }

// ConfigPath returns Pi's global config directory path.
func ConfigPath(homeDir string) string { return filepath.Join(homeDir, ".pi") }

func defaultStat(path string) statResult {
	info, err := os.Stat(path)
	if err != nil {
		return statResult{err: err}
	}
	return statResult{isDir: info.IsDir()}
}
