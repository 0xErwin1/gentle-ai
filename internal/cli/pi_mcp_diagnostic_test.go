package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSyncPiWarnsWithoutReenablingBuiltinMCP(t *testing.T) {
	home := setupPiSyncHost(t)
	dir := filepath.Join(home, "isolated-pi")
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	settingsPath := filepath.Join(dir, "settings.json")
	mustWriteFile(t, settingsPath, []byte(`{"packages":["npm:pi-mcp-adapter"],"extensions":["./custom.ts","-builtin:mcp"]}`))
	legacy := `{"mcpServers":{"context7":{"command":"npx","args":["context7-mcp"]}}}`
	mustWriteFile(t, filepath.Join(dir, "mcp-adapter.json"), []byte(legacy))
	mustWriteFile(t, filepath.Join(dir, "npm", "package.json"), []byte(`{"dependencies":{"pi-mcp-adapter":"5.0.0"}}`))
	for i := 0; i < 2; i++ {
		result, err := RunSyncWithSelection(home, piEngramSyncSelection())
		if err != nil {
			t.Fatal(err)
		}
		report := RenderSyncReport(result)
		warning := "- WARNING: Pi built-in MCP is disabled in " + settingsPath + "; pi-mcp-adapter is absent, so servers in " + filepath.Join(dir, "mcp.json") + " will not load. If you want these servers enabled, remove -builtin:mcp from extensions in " + settingsPath + ", then restart Pi and run `gentle-ai doctor`. If MCP is intentionally disabled, keep the setting."
		if strings.Count(report, warning) != 1 {
			t.Fatalf("sync %d missing exact warning:\n%s", i, report)
		}
		var settings struct {
			Packages   []string `json:"packages"`
			Extensions []string `json:"extensions"`
		}
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
		if strings.Join(settings.Extensions, ",") != "./custom.ts,-builtin:mcp" {
			t.Fatalf("user extensions changed: %s", data)
		}
		for _, pkg := range settings.Packages {
			if strings.Contains(pkg, "pi-mcp-adapter") {
				t.Fatalf("adapter not retired: %s", data)
			}
		}
		data, err = os.ReadFile(filepath.Join(dir, "mcp.json"))
		if err != nil {
			t.Fatal(err)
		}
		var migrated map[string]any
		if err := json.Unmarshal(data, &migrated); err != nil {
			t.Fatal(err)
		}
		if len(migrated["mcpServers"].(map[string]any)) != 1 {
			t.Fatalf("unexpected servers: %s", data)
		}
		data, err = os.ReadFile(filepath.Join(dir, "mcp-adapter.json"))
		if err != nil || string(data) != legacy {
			t.Fatalf("legacy changed: %s, %v", data, err)
		}
	}
}

func TestRunDoctorPiMCPDiagnosticIsReadOnly(t *testing.T) {
	for _, tt := range []struct {
		name, settings, npm, mcp string
		warn                     bool
	}{
		{"disabled with servers", `{"extensions":["-builtin:mcp"]}`, `{}`, `{"mcpServers":{"context7":{"command":"npx"}}}`, true},
		{"explicitly enabled", `{"extensions":["+builtin:mcp"]}`, `{}`, `{"mcpServers":{"context7":{}}}`, false},
		{"no servers", `{"extensions":["-builtin:mcp"]}`, `{}`, `{"mcpServers":{}}`, false},
		{"adapter in settings", `{"extensions":["-builtin:mcp"],"packages":[{"source":"npm:pi-mcp-adapter@5.0.0"}]}`, `{}`, `{"mcpServers":{"context7":{}}}`, false},
		{"adapter in npm", `{"extensions":["-builtin:mcp"]}`, `{"dependencies":{"pi-mcp-adapter":"5.0.0"}}`, `{"mcpServers":{"context7":{}}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := setupPiSyncHost(t)
			dir := filepath.Join(home, ".pi", "agent")
			files := map[string]string{"settings.json": tt.settings, "npm/package.json": tt.npm, "mcp.json": tt.mcp}
			for name, body := range files {
				mustWriteFile(t, filepath.Join(dir, name), []byte(body))
			}
			mustWriteFile(t, filepath.Join(home, ".gentle-ai", "state.json"), []byte(`{"installed_agents":["pi"]}`))
			oldHome := osUserHomeDirDoctor
			osUserHomeDirDoctor = func() (string, error) { return home, nil }
			t.Cleanup(func() { osUserHomeDirDoctor = oldHome })
			oldSpace := availableBytesFn
			availableBytesFn = func(string) (int64, error) { return 1 << 30, nil }
			t.Cleanup(func() { availableBytesFn = oldSpace })
			oldLook, oldPath := lookPathFn, pathDirsFn
			lookPathFn = func(name string) (string, error) { return filepath.Join(home, "bin", name), nil }
			pathDirsFn = func() []string { return nil }
			t.Cleanup(func() { lookPathFn, pathDirsFn = oldLook, oldPath })
			var out bytes.Buffer
			if err := RunDoctor(context.Background(), &out); err != nil {
				t.Fatal(err)
			}
			var line string
			for _, candidate := range strings.Split(out.String(), "\n") {
				if strings.Contains(candidate, "pi:mcp") {
					line = candidate
				}
			}
			if line == "" {
				t.Fatalf("Pi check not registered:\n%s", out.String())
			}
			if strings.Contains(line, "[!!]") != tt.warn {
				t.Fatalf("wrong diagnostic: %s", line)
			}
			if tt.warn {
				want := "  [!!]  pi:mcp                         Pi built-in MCP is disabled in " + filepath.Join(dir, "settings.json") + "; pi-mcp-adapter is absent, so servers in " + filepath.Join(dir, "mcp.json") + " will not load. If you want these servers enabled, remove -builtin:mcp from extensions in " + filepath.Join(dir, "settings.json") + ", then restart Pi and run `gentle-ai doctor`. If MCP is intentionally disabled, keep the setting."
				if line != want {
					t.Fatalf("wrong warning:\n got %s\nwant %s", line, want)
				}
			}
			for name, want := range files {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || string(got) != want {
					t.Fatalf("doctor changed %s: %s, %v", name, got, err)
				}
			}
		})
	}
}
