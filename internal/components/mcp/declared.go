package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// Server is one MCP server as a document declares it. A local server runs a
// command; a remote one is reached at a URL. The two are mutually exclusive and
// the caller validates that before reaching here.
type Server struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
	Enabled bool
}

// InjectDeclared writes the servers a document declares through the adapter's
// own strategy. It shares the merge helpers with the built-in Context7 path
// rather than reimplementing each file format, so a declared server lands the
// same way a bundled one does.
func InjectDeclared(targetDir string, adapter agents.Adapter, servers []Server) (InjectionResult, error) {
	if !adapter.SupportsMCP() || len(servers) == 0 {
		return InjectionResult{}, nil
	}

	files := make([]string, 0, len(servers))
	changed := false

	for _, server := range servers {
		result, err := injectDeclaredServer(targetDir, adapter, server)
		if err != nil {
			return InjectionResult{}, err
		}
		changed = changed || result.Changed
		files = append(files, result.Files...)
	}

	return InjectionResult{Changed: changed, Files: files}, nil
}

func injectDeclaredServer(targetDir string, adapter agents.Adapter, server Server) (InjectionResult, error) {
	switch adapter.MCPStrategy() {
	case model.StrategyTOMLFile:
		return injectDeclaredTOML(targetDir, adapter, server)

	case model.StrategyMergeIntoYAML:
		return injectDeclaredYAML(targetDir, adapter, server)

	case model.StrategyMergeIntoSettings:
		return injectDeclaredJSON(adapter.SettingsPath(targetDir), server, openCodeShaped(adapter))

	case model.StrategySeparateMCPFiles, model.StrategyMCPConfigFile:
		return injectDeclaredJSON(adapter.MCPConfigPath(targetDir, server.Name), server, false)

	default:
		return InjectionResult{}, fmt.Errorf("mcp injector does not support MCP strategy %d for agent %q; declare the server for an adapter that supports it, then run gentle-ai config render again", adapter.MCPStrategy(), adapter.Agent())
	}
}

// openCodeShaped reports whether the adapter keeps MCP servers under an "mcp"
// key with a typed entry, rather than the "mcpServers" shape the others use.
func openCodeShaped(adapter agents.Adapter) bool {
	return adapter.Agent() == model.AgentOpenCode || adapter.Agent() == model.AgentKilocode
}

func injectDeclaredTOML(targetDir string, adapter agents.Adapter, server Server) (InjectionResult, error) {
	path := adapter.MCPConfigPath(targetDir, server.Name)
	if path == "" {
		return InjectionResult{}, nil
	}

	existing, err := osReadFile(path)
	if err != nil {
		return InjectionResult{}, fmt.Errorf("read TOML config %q: %w", path, err)
	}

	updated := filemerge.UpsertCodexMCPServerBlock(string(existing), server.Name, server.Command, server.Args)
	if server.URL != "" {
		updated = filemerge.UpsertCodexRemoteMCPServerBlock(string(existing), server.Name, server.URL, server.Headers)
	}

	write, err := filemerge.WriteFileAtomic(path, []byte(updated), 0o644)
	if err != nil {
		return InjectionResult{}, fmt.Errorf("write TOML config %q: %w", path, err)
	}

	return InjectionResult{Changed: write.Changed, Files: []string{path}}, nil
}

func injectDeclaredYAML(targetDir string, adapter agents.Adapter, server Server) (InjectionResult, error) {
	path := adapter.MCPConfigPath(targetDir, server.Name)
	if path == "" {
		return InjectionResult{}, nil
	}

	existing, err := osReadFile(path)
	if err != nil {
		return InjectionResult{}, fmt.Errorf("read YAML config %q: %w", path, err)
	}

	updated := filemerge.UpsertYAMLMCPServerBlock(string(existing), server.Name, server.Command, server.Args, server.Env)

	write, err := filemerge.WriteFileAtomic(path, []byte(updated), 0o644)
	if err != nil {
		return InjectionResult{}, fmt.Errorf("write YAML config %q: %w", path, err)
	}

	return InjectionResult{Changed: write.Changed, Files: []string{path}}, nil
}

func injectDeclaredJSON(path string, server Server, openCodeShape bool) (InjectionResult, error) {
	if path == "" {
		return InjectionResult{}, nil
	}

	overlay, err := declaredServerOverlay(server, openCodeShape)
	if err != nil {
		return InjectionResult{}, err
	}

	write, err := mergeJSONFile(path, overlay)
	if err != nil {
		return InjectionResult{}, err
	}

	return InjectionResult{Changed: write.Changed, Files: []string{path}}, nil
}

func declaredServerOverlay(server Server, openCodeShape bool) ([]byte, error) {
	entry := map[string]any{}
	if server.URL != "" {
		entry["type"] = "remote"
		entry["url"] = server.URL
	} else {
		entry["type"] = "local"
		entry["command"] = append([]string{server.Command}, server.Args...)
	}
	if len(server.Env) > 0 {
		entry["environment"] = server.Env
	}
	if len(server.Headers) > 0 {
		entry["headers"] = server.Headers
	}
	entry["enabled"] = server.Enabled

	key := "mcpServers"
	if openCodeShape {
		key = "mcp"
	} else {
		entry = plainServerEntry(server)
	}

	overlay, err := json.Marshal(map[string]any{key: map[string]any{server.Name: entry}})
	if err != nil {
		return nil, fmt.Errorf("encode declared MCP server %q: %w", server.Name, err)
	}

	return overlay, nil
}

// plainServerEntry is the shape every adapter outside the OpenCode family uses:
// a command with its arguments alongside, rather than one combined list.
func plainServerEntry(server Server) map[string]any {
	entry := map[string]any{}
	if server.URL != "" {
		entry["url"] = server.URL
	} else {
		entry["command"] = server.Command
		if len(server.Args) > 0 {
			entry["args"] = server.Args
		}
	}
	if len(server.Env) > 0 {
		entry["env"] = server.Env
	}
	if len(server.Headers) > 0 {
		entry["headers"] = server.Headers
	}

	return entry
}
