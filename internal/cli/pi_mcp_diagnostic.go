package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	piagent "github.com/gentleman-programming/gentle-ai/v4/internal/agents/pi"
	"github.com/gentleman-programming/gentle-ai/v4/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v4/internal/doctor"
)

// inspectPiMCP is read-only: onboarding records that a setting was handled,
// not who authored it, so a migration cannot safely re-enable user servers.
func inspectPiMCP(homeDir string) (string, error) {
	dir := piagent.AgentConfigPath(homeDir)
	settingsPath := filepath.Join(dir, "settings.json")
	settings, err := readPiDiagnosticJSON(settingsPath)
	if err != nil {
		return "", err
	}
	disabled := false
	extensions, _ := settings["extensions"].([]any)
	for _, entry := range extensions {
		if entry == "-builtin:mcp" {
			disabled = true
		}
	}
	if !disabled || hasPiMCPAdapter(settings["packages"]) {
		return "", nil
	}
	mcpPath := filepath.Join(dir, "mcp.json")
	config, err := readPiDiagnosticJSON(mcpPath)
	if err != nil {
		return "", err
	}
	servers, _ := config["mcpServers"].(map[string]any)
	if len(servers) == 0 {
		return "", nil
	}
	manifest, err := readPiDiagnosticJSON(filepath.Join(dir, "npm", "package.json"))
	if err != nil {
		return "", err
	}
	dependencies, _ := manifest["dependencies"].(map[string]any)
	if _, present := dependencies["pi-mcp-adapter"]; present {
		return "", nil
	}
	return fmt.Sprintf("Pi built-in MCP is disabled in %s; pi-mcp-adapter is absent, so servers in %s will not load. If you want these servers enabled, remove -builtin:mcp from extensions in %s, then restart Pi and run `gentle-ai doctor`. If MCP is intentionally disabled, keep the setting.", settingsPath, mcpPath, settingsPath), nil
}

func hasPiMCPAdapter(packages any) bool {
	switch value := packages.(type) {
	case string:
		return value == "npm:pi-mcp-adapter" || strings.HasPrefix(value, "npm:pi-mcp-adapter@")
	case []any:
		for _, entry := range value {
			if hasPiMCPAdapter(entry) {
				return true
			}
		}
	case map[string]any:
		if source, ok := value["source"].(string); ok {
			return hasPiMCPAdapter(source)
		}
		for source := range value {
			if hasPiMCPAdapter(source) {
				return true
			}
		}
	}
	return false
}

func readPiDiagnosticJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", path, err)
	}
	object, err := filemerge.DecodeStrictJSONObject(data)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", path, err)
	}
	return object, nil
}

func checkPiMCP(homeDir string) CheckResult {
	const id doctor.CheckID = "pi:mcp"
	warning, err := inspectPiMCP(homeDir)
	if err != nil {
		return CheckResult{Name: id, Status: CheckStatusWarn, Detail: fmt.Sprintf("Pi MCP configuration could not be inspected: %v; inspect or repair it manually, then run `gentle-ai doctor`", err)}
	}
	if warning != "" {
		return CheckResult{Name: id, Status: CheckStatusWarn, Detail: warning}
	}
	return CheckResult{Name: id, Status: CheckStatusPass, Detail: "no configured Pi MCP servers blocked by -builtin:mcp without pi-mcp-adapter"}
}
