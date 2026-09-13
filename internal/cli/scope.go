package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// InstallScope controls where agent-scoped config files (system prompts, skills/, agents/, etc.) are written.
// ScopeGlobal writes to the user's global config root for each selected agent.
// ScopeWorkspace writes to the current workspace config root for each selected agent.
type InstallScope = model.InstallScope

const (
	// ScopeGlobal writes to the global agent config dir (default, backward-compatible).
	ScopeGlobal = model.InstallScopeGlobal
	// ScopeWorkspace writes to the current workspace config root for each selected agent.
	ScopeWorkspace = model.InstallScopeWorkspace

	// scopeEnvVar is the environment variable that controls install scope.
	scopeEnvVar = "GENTLE_AI_INSTALL_SCOPE"
)

// ResolveInstallScope resolves the install scope from the flag value and env var.
// Priority: explicit flag > env var > default (global).
// An empty flagValue means the flag was not set.
func ResolveInstallScope(flagValue string) (InstallScope, error) {
	raw := strings.TrimSpace(flagValue)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(scopeEnvVar))
	}
	if raw == "" {
		return ScopeGlobal, nil
	}
	return parseInstallScope(raw)
}

func parseInstallScope(raw string) (InstallScope, error) {
	switch InstallScope(raw) {
	case ScopeGlobal, ScopeWorkspace:
		return InstallScope(raw), nil
	default:
		return "", fmt.Errorf("unsupported scope %q (valid: global, workspace)", raw)
	}
}

// ResolveAgentConfigDir returns the directory to use as the agent config root.
// When scope is ScopeWorkspace, workspaceDir is returned; otherwise homeDir is used.
// Both homeDir and workspaceDir must be non-empty absolute paths.
func ResolveAgentConfigDir(scope InstallScope, homeDir, workspaceDir string) string {
	if scope == ScopeWorkspace && strings.TrimSpace(workspaceDir) != "" {
		return workspaceDir
	}
	return homeDir
}

// applyDeclaredInstallTarget lets a declared scope or channel stand in for the
// flag, which cannot be passed alongside a document. An omitted declaration
// leaves whatever the flag and environment already resolved.
func applyDeclaredInstallTarget(input InstallInput, selection model.Selection) InstallInput {
	if selection.Scope != "" {
		input.Scope = selection.Scope
	}
	if selection.Channel != "" {
		input.Channel = selection.Channel
	}

	return input
}
