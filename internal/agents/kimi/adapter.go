// Package kimi provides Kimi Code CLI agent integration.
//
// Integration Note:
// This adapter natively relies on Astral's `uv` package manager
// (`uv tool install kimi-cli`) to securely download and run Kimi CLI,
// avoiding upstream's pipe-to-shell bootstrap scripts.
package kimi

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/gentleman-programming/gentle-ai/v3/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/v3/internal/assets"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v3/internal/installcmd"
	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
	"github.com/gentleman-programming/gentle-ai/v3/internal/system"
)

var LookPathOverride = exec.LookPath

type statResult struct {
	isDir bool
	err   error
}

// Adapter implements agents.Adapter for Kimi Code CLI.
type Adapter struct {
	lookPath    func(string) (string, error)
	statPath    func(string) statResult
	pathExists  func(string) bool
	userHomeDir func() (string, error)
	resolver    installcmd.Resolver
}

// NewAdapter creates a new Kimi adapter instance.
func NewAdapter() *Adapter {
	return &Adapter{
		lookPath:    LookPathOverride,
		statPath:    defaultStat,
		pathExists:  defaultPathExists,
		userHomeDir: os.UserHomeDir,
		resolver:    installcmd.NewResolver(),
	}
}

// --- Identity ---

func (a *Adapter) Agent() model.AgentID {
	return model.AgentKimi
}

func (a *Adapter) Tier() model.SupportTier {
	return model.TierFull
}

// --- Detection ---

func (a *Adapter) Detect(_ context.Context, homeDir string) (bool, string, string, bool, error) {
	configPath, _ := a.configRoot(homeDir)

	binaryPath, err := a.findKimi()
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

// findKimi searches for kimi in PATH and official fallback locations.
func (a *Adapter) findKimi() (string, error) {
	if path, err := a.lookPath("kimi"); err == nil {
		return path, nil
	}

	home, err := a.userHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("kimi not found in PATH and home directory is unavailable")
	}

	fallbacks := []string{
		filepath.Join(home, ".local", "bin", binaryName()),
		filepath.Join(home, "bin", binaryName()),
	}
	if runtime.GOOS == "windows" {
		fallbacks = append(fallbacks,
			filepath.Join(home, "AppData", "Local", "Microsoft", "WinGet", "Links", "kimi.exe"),
			filepath.Join(home, "AppData", "Roaming", "uv", "bin", "kimi.exe"),
		)
	}

	for _, fb := range fallbacks {
		if a.pathExists(fb) {
			return fb, nil
		}
	}

	return "", fmt.Errorf("kimi not found in PATH or official install locations")
}

// --- Installation ---

func (a *Adapter) CapabilityManifest() capabilitymanifest.AgentCapabilityManifest {
	return capabilitymanifest.MustForAgent(model.AgentKimi)
}

func (a *Adapter) InstallCommand(profile system.PlatformProfile) ([][]string, error) {
	resolver := a.resolver
	if resolver == nil {
		resolver = installcmd.NewResolver()
	}
	return resolver.ResolveAgentInstall(profile, a.Agent())
}

// --- Config paths ---
//
// All path methods resolve through configRoot: the current ~/.kimi-code root
// (kimi-code v0.11+) is preferred when it exists as a directory, and the
// legacy ~/.kimi root is used otherwise.

// configRoot resolves the Kimi config root for homeDir using the adapter's
// testable stat seam.
func (a *Adapter) configRoot(homeDir string) (string, ConfigLayout) {
	statPath := a.statPath
	if statPath == nil {
		statPath = defaultStat
	}
	return resolveConfigRoot(statPath, homeDir)
}

func (a *Adapter) GlobalConfigDir(homeDir string) string {
	root, _ := a.configRoot(homeDir)
	return root
}

func (a *Adapter) SystemPromptDir(homeDir string) string {
	return a.GlobalConfigDir(homeDir)
}

func (a *Adapter) SystemPromptFile(homeDir string) string {
	return filepath.Join(a.GlobalConfigDir(homeDir), "KIMI.md")
}

// SkillsDir returns the skills directory path for homeDir.
//
// Kimi Code CLI supports native Agent Skills. For the current kimi-code
// v0.11+ layout (config root ~/.kimi-code) the native per-brand skills
// directory ~/.kimi-code/skills is used. For the legacy Python/uv layout the
// generic shared skills directory is kept:
//   - generic shared skills: ~/.config/agents/skills and ~/.agents/skills
//
// We intentionally use ~/.config/agents/skills for legacy installs as a
// cross-agent shared convention. Kimi discovers this directory natively as
// part of its generic skills group (the docs mark this path as
// "recommended").
//
// See: https://moonshotai.github.io/kimi-cli/en/customization/skills.html
func (a *Adapter) SkillsDir(homeDir string) string {
	root, layout := a.configRoot(homeDir)
	if layout == LayoutCurrent {
		return filepath.Join(root, "skills")
	}
	return filepath.Join(homeDir, ".config", "agents", "skills")
}

func (a *Adapter) SettingsPath(homeDir string) string {
	return filepath.Join(a.GlobalConfigDir(homeDir), "config.toml")
}

func (a *Adapter) CommandsDir(string) string {
	return ""
}

// --- Config strategies ---

func (a *Adapter) SystemPromptStrategy() model.SystemPromptStrategy {
	return model.StrategyJinjaModules
}

func (a *Adapter) MCPStrategy() model.MCPStrategy {
	return model.StrategyMCPConfigFile
}

// --- MCP ---

func (a *Adapter) MCPConfigPath(homeDir string, _ string) string {
	return filepath.Join(a.GlobalConfigDir(homeDir), "mcp.json")
}

// --- Optional capabilities ---

func (a *Adapter) SupportsOutputStyles() bool {
	return a.CapabilityManifest().Features.OutputStyles
}

func (a *Adapter) OutputStyleDir(_ string) string {
	return ""
}

func (a *Adapter) SupportsSlashCommands() bool {
	return a.CapabilityManifest().Features.SlashCommands
}

func (a *Adapter) SupportsSkills() bool {
	return a.CapabilityManifest().Features.Skills
}

func (a *Adapter) SupportsSystemPrompt() bool {
	return a.CapabilityManifest().Features.SystemPrompt
}

func (a *Adapter) SupportsMCP() bool {
	return a.CapabilityManifest().Features.MCP
}

// --- Sub-agent support (optional interface) ---
//
// Kimi uses YAML-based agent specs with separate .md system prompts.

func (a *Adapter) SupportsSubAgents() bool {
	return a.CapabilityManifest().Features.FileSubAgents
}

func (a *Adapter) SubAgentsDir(homeDir string) string {
	return filepath.Join(a.GlobalConfigDir(homeDir), "agents")
}

func (a *Adapter) EmbeddedSubAgentsDir() string {
	return "kimi/agents"
}

// PostInstallMessage returns launch guidance for the resolved Kimi layout.
// The legacy Python/uv layout keeps YAML agent instructions (--agent-file);
// the current kimi-code v0.11+ layout retired --agent-file upstream, so its
// guidance only points at the native skills root.
func (a *Adapter) PostInstallMessage(homeDir string) string {
	root, layout := a.configRoot(homeDir)

	if layout == LayoutCurrent {
		skillsRoot := filepath.Join(root, "skills")
		return fmt.Sprintf(`Kimi Code configured!

Usage:
  kimi --prompt "List skills"

Kimi Code v0.11+ discovers native Agent Skills automatically from the skills root; no agent-file launch step is required.

Skills root:
  "%s"`, skillsRoot)
	}

	gentlemanYaml := filepath.Join(root, "agents", "gentleman.yaml")
	skillsRoot := filepath.Join(homeDir, ".config", "agents", "skills")

	return fmt.Sprintf(`Kimi Code configured!

Usage:
  kimi --agent-file "%s"

Launch the gentleman agent for ODD guidance. Kimi also supports YAML agents in ~/.kimi/agents.

Skills root:
  "%s"`, gentlemanYaml, skillsRoot)
}

// --- Helpers ---

func defaultStat(path string) statResult {
	info, err := os.Stat(path)
	if err != nil {
		return statResult{err: err}
	}
	return statResult{isDir: info.IsDir()}
}

func defaultPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "kimi.exe"
	}
	return "kimi"
}

// BootstrapTemplate ensures the base KIMI.md template exists in the agent's config directory.
// It is used by the installation pipeline to provide the managed Kimi prompt
// even when optional components are not installed.
func (a *Adapter) BootstrapTemplate(homeDir string) error {
	kimiDir := a.GlobalConfigDir(homeDir)
	if err := os.MkdirAll(kimiDir, 0o755); err != nil {
		return fmt.Errorf("create kimi config dir: %w", err)
	}

	skeletonPath := a.SystemPromptFile(homeDir)

	// We always write the skeleton to ensure any missing includes are restored.
	// Since KIMI.md is the 'router' for modular Jinja components, it should
	// remain managed by the framework.
	content := assets.MustRead("kimi/KIMI.md")
	if _, err := filemerge.WriteFileAtomic(skeletonPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write KIMI.md skeleton: %w", err)
	}

	// Kimi considers config.toml a required file. We create an empty one if
	// it's missing to satisfy verification during a minimalist install.
	configPath := a.SettingsPath(homeDir)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if _, err := filemerge.WriteFileAtomic(configPath, []byte("# Kimi Code Config\n"), 0o644); err != nil {
			return err
		}
	}

	return nil
}
