package pi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v3/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
	"github.com/gentleman-programming/gentle-ai/v3/internal/system"
)

func TestAdapterIdentityAndCapabilities(t *testing.T) {
	a := NewAdapter()

	if got := a.Agent(); got != model.AgentPi {
		t.Fatalf("Agent() = %q, want %q", got, model.AgentPi)
	}
	if got := a.Tier(); got != model.TierFull {
		t.Fatalf("Tier() = %q, want %q", got, model.TierFull)
	}

	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"SupportsSkills", a.SupportsSkills(), false},
		{"SupportsMCP", a.SupportsMCP(), true},
		{"SupportsSystemPrompt", a.SupportsSystemPrompt(), false},
		{"SupportsSlashCommands", a.SupportsSlashCommands(), false},
		{"SupportsOutputStyles", a.SupportsOutputStyles(), false},
		{"SupportsSubAgents", a.SupportsSubAgents(), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestAdapterPaths(t *testing.T) {
	a := NewAdapter()
	homeDir := t.TempDir()
	piDir := filepath.Join(homeDir, ".pi")
	piAgentDir := filepath.Join(piDir, "agent")

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"GlobalConfigDir", a.GlobalConfigDir(homeDir), piDir},
		{"SystemPromptDir", a.SystemPromptDir(homeDir), piAgentDir},
		{"SystemPromptFile", a.SystemPromptFile(homeDir), filepath.Join(piAgentDir, "APPEND_SYSTEM.md")},
		{"SkillsDir", a.SkillsDir(homeDir), ""},
		{"SettingsPath", a.SettingsPath(homeDir), filepath.Join(piAgentDir, "settings.json")},
		{"CommandsDir", a.CommandsDir(homeDir), ""},
		{"MCPConfigPath", a.MCPConfigPath(homeDir, "context7"), filepath.Join(piAgentDir, "mcp.json")},
		{"OutputStyleDir", a.OutputStyleDir(homeDir), ""},
		{"SubAgentsDir", a.SubAgentsDir(homeDir), ""},
		{"EmbeddedSubAgentsDir", a.EmbeddedSubAgentsDir(), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestAgentConfigPathHonorsPiCodingAgentDir(t *testing.T) {
	homeDir := t.TempDir()
	defaultPath := filepath.Join(homeDir, ".pi", "agent")

	t.Run("unset uses default", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "")
		if got := AgentConfigPath(homeDir); got != defaultPath {
			t.Fatalf("AgentConfigPath() = %q, want %q", got, defaultPath)
		}
	})

	t.Run("blank is ignored", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "   ")
		if got := AgentConfigPath(homeDir); got != defaultPath {
			t.Fatalf("AgentConfigPath() = %q, want %q", got, defaultPath)
		}
	})

	t.Run("absolute override wins", func(t *testing.T) {
		configured := filepath.Join(homeDir, "isolated-agent")
		t.Setenv("PI_CODING_AGENT_DIR", configured)
		if got := AgentConfigPath(homeDir); got != configured {
			t.Fatalf("AgentConfigPath() = %q, want %q", got, configured)
		}
	})

	t.Run("tilde override expands against home", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "~/gentle-shell/agent")
		want := filepath.Join(homeDir, "gentle-shell", "agent")
		if got := AgentConfigPath(homeDir); got != want {
			t.Fatalf("AgentConfigPath() = %q, want %q", got, want)
		}
	})

	t.Run("bare tilde override expands to home", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "~")
		if got := AgentConfigPath(homeDir); got != homeDir {
			t.Fatalf("AgentConfigPath() = %q, want %q", got, homeDir)
		}
	})

	t.Run("relative override resolves against cwd", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "relative-pi-agent")
		wantAbs, err := filepath.Abs("relative-pi-agent")
		if err != nil {
			t.Fatal(err)
		}
		if got := AgentConfigPath(homeDir); got != wantAbs {
			t.Fatalf("AgentConfigPath() = %q, want %q", got, wantAbs)
		}
	})

	t.Run("relative override falls back to default agent dir when cwd resolution fails", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "relative-pi-agent")
		restore := resolveAbsPath
		resolveAbsPath = func(string) (string, error) { return "", fmt.Errorf("getwd unavailable") }
		t.Cleanup(func() { resolveAbsPath = restore })

		want := filepath.Join(homeDir, ".pi", "agent")
		if got := AgentConfigPath(homeDir); got != want {
			t.Fatalf("AgentConfigPath() = %q, want %q", got, want)
		}
	})
}

func TestAdapterPathsFollowConfiguredAgentDirectory(t *testing.T) {
	a := NewAdapter()
	homeDir := t.TempDir()
	piDir := filepath.Join(homeDir, ".pi")
	configured := filepath.Join(t.TempDir(), "isolated-home", "agent")
	t.Setenv("PI_CODING_AGENT_DIR", configured)

	tests := []struct {
		name string
		got  string
		want string
	}{
		// GlobalConfigDir never follows the override: it always stays the
		// homeDir/.pi parent root, even while PI_CODING_AGENT_DIR relocates
		// the agent-owned paths below.
		{"GlobalConfigDir", a.GlobalConfigDir(homeDir), piDir},
		{"SystemPromptDir", a.SystemPromptDir(homeDir), configured},
		{"SystemPromptFile", a.SystemPromptFile(homeDir), filepath.Join(configured, "APPEND_SYSTEM.md")},
		{"SettingsPath", a.SettingsPath(homeDir), filepath.Join(configured, "settings.json")},
		{"MCPConfigPath", a.MCPConfigPath(homeDir, "context7"), filepath.Join(configured, "mcp.json")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestProvisionEngramMCPWritesNothingOnFreshHome(t *testing.T) {
	a := NewAdapter()
	home := t.TempDir()

	changed, paths, err := a.ProvisionEngramMCP(home)
	if err != nil {
		t.Fatalf("ProvisionEngramMCP() error = %v", err)
	}
	if changed || len(paths) != 0 {
		t.Fatalf("ProvisionEngramMCP() = (changed %v, paths %v), want no writes (Pi Engram is native-only, not MCP)", changed, paths)
	}
	for _, path := range []string{
		filepath.Join(home, ".pi", "agent", "settings.json"),
		filepath.Join(home, ".pi", "agent", "npm", "package.json"),
		filepath.Join(home, ".pi", "agent", "mcp.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stat %q err = %v, want IsNotExist", path, err)
		}
	}
}

func TestProvisionEngramMCPTargetsConfiguredAgentDirectoryAndLeavesRealHomeUntouched(t *testing.T) {
	a := NewAdapter()
	realHome := t.TempDir()
	override := filepath.Join(t.TempDir(), "gentle-shell-home", "agent")
	t.Setenv("PI_CODING_AGENT_DIR", override)
	if err := os.MkdirAll(override, 0o755); err != nil {
		t.Fatalf("MkdirAll(override agent dir) error = %v", err)
	}
	writeFile := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	writeFile(filepath.Join(override, "settings.json"), `{"packages":["npm:pi-mcp-adapter@2.6.0"]}`)
	writeFile(filepath.Join(override, "npm", "package.json"), `{"dependencies":{"pi-mcp-adapter":"^2.6.0"}}`)

	changed, paths, err := a.ProvisionEngramMCP(realHome)
	if err != nil {
		t.Fatalf("ProvisionEngramMCP() error = %v", err)
	}
	if !changed {
		t.Fatalf("ProvisionEngramMCP() changed = false, want true")
	}

	wantSettings := filepath.Join(override, "settings.json")
	wantNPMPackage := filepath.Join(override, "npm", "package.json")
	if !reflect.DeepEqual(paths, []string{wantSettings, wantNPMPackage}) {
		t.Fatalf("ProvisionEngramMCP() paths = %v, want [%q %q]", paths, wantSettings, wantNPMPackage)
	}

	settingsBody, err := os.ReadFile(wantSettings)
	if err != nil {
		t.Fatalf("ReadFile(settings) error = %v", err)
	}
	if strings.Contains(string(settingsBody), "pi-mcp-adapter") {
		t.Fatalf("settings.json = %s, want the retired pi-mcp-adapter package pruned", settingsBody)
	}

	npmBody, err := os.ReadFile(wantNPMPackage)
	if err != nil {
		t.Fatalf("ReadFile(npm package.json) error = %v", err)
	}
	if strings.Contains(string(npmBody), "pi-mcp-adapter") {
		t.Fatalf("npm/package.json = %s, want the retired pi-mcp-adapter dependency pruned", npmBody)
	}

	if _, err := os.Stat(filepath.Join(realHome, ".pi")); !os.IsNotExist(err) {
		t.Fatalf("real home .pi dir stat err = %v, want IsNotExist (real home must stay untouched by the override)", err)
	}
}

func TestCodeGraphPathsResolveConfiguredAgentDirectory(t *testing.T) {
	home := t.TempDir()
	configured := filepath.Join(home, "custom-pi")
	t.Setenv("PI_CODING_AGENT_DIR", configured)

	paths := CodeGraphPaths(home)
	if paths.AgentDir != configured {
		t.Fatalf("AgentDir = %q, want %q", paths.AgentDir, configured)
	}
	if paths.MCPConfig != filepath.Join(configured, "mcp.json") {
		t.Fatalf("MCPConfig = %q", paths.MCPConfig)
	}
	sum := sha256.Sum256([]byte(filepath.Clean(configured)))
	wantManifest := filepath.Join(home, ".gentle-ai", fmt.Sprintf("pi-codegraph-%x.json", sum[:8]))
	if paths.Manifest != wantManifest {
		t.Fatalf("Manifest = %q, want %q", paths.Manifest, wantManifest)
	}
}

func TestCodeGraphPathsDefaultAgentDirectory(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	home := t.TempDir()
	paths := CodeGraphPaths(home)
	wantAgentDir := filepath.Join(home, ".pi", "agent")
	if paths.AgentDir != wantAgentDir {
		t.Fatalf("AgentDir = %q, want %q", paths.AgentDir, wantAgentDir)
	}
	wantManifest := filepath.Join(home, ".gentle-ai", "pi-codegraph.json")
	if paths.Manifest != wantManifest {
		t.Fatalf("Manifest = %q, want %q", paths.Manifest, wantManifest)
	}
}

func TestCodeGraphPathsKeepsAgentDirectoryWhenProjectMCPOverrides(t *testing.T) {
	home := t.TempDir()
	configured := filepath.Join(home, "custom-pi")
	workspace := filepath.Join(home, "project")
	t.Setenv("PI_CODING_AGENT_DIR", configured)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".mcp.json"), []byte(`{"mcpServers":{"codegraph":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	paths := CodeGraphPaths(home)
	effective, err := EffectiveCodeGraphMCPPath(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if paths.AgentDir != configured || effective != filepath.Join(workspace, ".mcp.json") {
		t.Fatalf("agent=%q effective=%q, want configured agent and project config", paths.AgentDir, effective)
	}
}

func TestDiscoverCodeGraphChildrenUsesProjectOverrideAndPreservesPackageSource(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "project")
	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(home, ".pi", "agent", "subagents", "worker.md"), "---\ntools: bash\n---\npackage worker\n")
	mustWrite(filepath.Join(workspace, ".pi", "subagents", "worker.md"), "---\ntools: bash, mcp\n---\nproject worker\n")
	mustWrite(filepath.Join(home, ".pi", "agent", "agents", "reader.md"), "---\ntools: read\n---\nreader\n")
	mustWrite(filepath.Join(home, ".pi", "agent", "node_modules", "gentle-pi", "subagents", "package-worker.md"), "---\ntools: bash\n---\npackage worker\n")

	children, err := DiscoverCodeGraphChildren(home, workspace)
	if err != nil {
		t.Fatalf("DiscoverCodeGraphChildren() error = %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("children = %#v, want three effective children", children)
	}
	if children[0].Name != "package-worker" || children[1].Name != "reader" || children[2].Name != "worker" {
		t.Fatalf("children = %#v, want sorted package-worker, reader, and worker", children)
	}
	if !children[0].PackageOwned || children[0].Target == children[0].Source {
		t.Fatalf("package worker = %#v, want owned overlay", children[0])
	}
	if children[2].Source != filepath.Join(workspace, ".pi", "subagents", "worker.md") || children[2].PackageOwned {
		t.Fatalf("worker = %#v, want project effective child", children[2])
	}
}

func TestDiscoverCodeGraphChildrenReturnsUnreadableDirectoryError(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent", "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := piWalkDir
	piWalkDir = func(path string, walkFn fs.WalkDirFunc) error {
		return &fs.PathError{Op: "readdir", Path: path, Err: fs.ErrPermission}
	}
	t.Cleanup(func() { piWalkDir = previous })

	_, err := DiscoverCodeGraphChildren(home, "")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("DiscoverCodeGraphChildren() error = %v, want unreadable-directory error", err)
	}
}

func TestDiscoverCodeGraphChildrenUsesNormalizedRuntimeIdentity(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "project")
	for path, body := range map[string]string{
		filepath.Join(home, ".pi", "agent", "subagents", "Worker.md"): "---\ntools: bash\n---\nuser\n",
		filepath.Join(workspace, ".pi", "subagents", "worker.md"):     "---\ntools: bash\n---\nproject\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	children, err := DiscoverCodeGraphChildren(home, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].Source != filepath.Join(workspace, ".pi", "subagents", "worker.md") {
		t.Fatalf("children = %#v, want one project runtime identity", children)
	}
}

func TestAdapterDetectUsesPiBinaryAndConfigPath(t *testing.T) {
	homeDir := t.TempDir()
	configDir := filepath.Join(homeDir, ".pi", "agent")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	a := &Adapter{
		lookPath: func(file string) (string, error) {
			if file != "pi" {
				t.Fatalf("lookPath called with %q, want pi", file)
			}
			return "/usr/local/bin/pi", nil
		},
		statPath: defaultStat,
	}

	installed, binaryPath, configPath, configFound, err := a.Detect(context.Background(), homeDir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !installed {
		t.Fatalf("Detect() installed = false, want true")
	}
	if binaryPath != "/usr/local/bin/pi" {
		t.Fatalf("Detect() binaryPath = %q, want /usr/local/bin/pi", binaryPath)
	}
	if configPath != configDir {
		t.Fatalf("Detect() configPath = %q, want %q", configPath, configDir)
	}
	if !configFound {
		t.Fatalf("Detect() configFound = false, want true")
	}
}

func TestAdapterDetectMissingPiBinary(t *testing.T) {
	homeDir := t.TempDir()
	a := &Adapter{
		lookPath: func(file string) (string, error) {
			return "", os.ErrNotExist
		},
		statPath: defaultStat,
	}

	installed, binaryPath, configPath, configFound, err := a.Detect(context.Background(), homeDir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if installed {
		t.Fatalf("Detect() installed = true, want false")
	}
	if binaryPath != "" {
		t.Fatalf("Detect() binaryPath = %q, want empty", binaryPath)
	}
	if configPath != filepath.Join(homeDir, ".pi", "agent") {
		t.Fatalf("Detect() configPath = %q, want ~/.pi/agent under home", configPath)
	}
	if configFound {
		t.Fatalf("Detect() configFound = true, want false")
	}
}

func TestManagedPackageSourcesReturnsCanonicalCopy(t *testing.T) {
	want := []string{
		"npm:gentle-pi",
		"npm:gentle-engram",
		"npm:pi-web-access",
		"npm:pi-btw",
	}
	got := ManagedPackageSources()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ManagedPackageSources() = %v, want %v", got, want)
	}
	got[0] = "changed"
	if sources := ManagedPackageSources(); sources[0] != want[0] {
		t.Fatalf("ManagedPackageSources() exposed mutable adapter state: %v", sources)
	}
}

func TestUninstallPackageSourcesIncludesRetiredMCPAdapter(t *testing.T) {
	got := UninstallPackageSources()
	want := append(ManagedPackageSources(), "npm:pi-mcp-adapter")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UninstallPackageSources() = %v, want %v", got, want)
	}
	for _, source := range ManagedPackageSources() {
		if !slices.Contains(got, source) {
			t.Fatalf("UninstallPackageSources() = %v, want every managed source retained", got)
		}
	}
}

func TestAdapterInstallCommandSequenceUsesNpmWhenPnpmIsUnavailable(t *testing.T) {
	a := &Adapter{
		lookPath: func(file string) (string, error) {
			if file == "pnpm" {
				return "", os.ErrNotExist
			}
			return "/usr/local/bin/" + file, nil
		},
		statPath: defaultStat,
	}
	commands, err := a.InstallCommand(system.PlatformProfile{})
	if err != nil {
		t.Fatalf("InstallCommand() error = %v", err)
	}

	want := [][]string{
		{"pi", "install", "npm:gentle-pi"},
		{"pi", "install", "npm:gentle-engram"},
		{"npm", "exec", "--yes", "--package", "gentle-engram@latest", "--", "pi-engram", "init"},
		{"pi", "install", "npm:pi-web-access"},
		{"pi", "install", "npm:pi-btw"},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("InstallCommand() = %#v, want %#v", commands, want)
	}
}

func TestAdapterInstallCommandSequenceUsesNpmForEngramInitWhenPnpmIsAvailable(t *testing.T) {
	a := &Adapter{
		lookPath: func(file string) (string, error) {
			if file == "pnpm" {
				return "/usr/local/bin/pnpm", nil
			}
			return "", os.ErrNotExist
		},
		statPath: defaultStat,
	}
	commands, err := a.InstallCommand(system.PlatformProfile{})
	if err != nil {
		t.Fatalf("InstallCommand() error = %v", err)
	}

	want := []string{"npm", "exec", "--yes", "--package", "gentle-engram@latest", "--", "pi-engram", "init"}
	if !reflect.DeepEqual(commands[2], want) {
		t.Fatalf("InstallCommand()[2] = %#v, want %#v", commands[2], want)
	}
}

func TestRetainPiPackagesKeepsSubagentsPackageWhileGentlePiIsPinnedBelowGentleAgents(t *testing.T) {
	kept := retainPiPackages([]any{"npm:gentle-pi@2.4.0", "npm:pi-subagents-j0k3r@1.5.13"})
	if !reflect.DeepEqual(kept, []any{"npm:gentle-pi@2.4.0", "npm:pi-subagents-j0k3r@1.5.13"}) {
		t.Fatalf("retainPiPackages() with an old gentle-pi pin = %v, want the subagents package kept", kept)
	}
	dropped := retainPiPackages([]any{"npm:gentle-pi@2.5.0", "npm:pi-subagents-j0k3r"})
	if !reflect.DeepEqual(dropped, []any{"npm:gentle-pi@2.5.0"}) {
		t.Fatalf("retainPiPackages() with gentle-pi 2.5.0 = %v, want the subagents package dropped", dropped)
	}
}

func TestMergePiSettingsFileRemovesRetiredCompanionPackages(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings dir) error = %v", err)
	}
	initial := `{
  "packages": [
    "npm:@juicesharp/rpiv-todo",
    "npm:@juicesharp/rpiv-todo@2.9.0",
    "npm:pi-subagents-j0k3r",
    "npm:pi-subagents-j0k3r@1.5.13",
    "npm:@juicesharp/rpiv-ask-user-question",
    "npm:other@1.0.0"
  ]
}`
	if err := os.WriteFile(settingsPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile(settings) error = %v", err)
	}

	applyPiSettingsPrune(t, settingsPath)

	var settings struct {
		Packages []string `json:"packages"`
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(settings) error = %v", err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("Unmarshal(settings) error = %v", err)
	}
	if !reflect.DeepEqual(settings.Packages, []string{"npm:other@1.0.0"}) {
		t.Fatalf("packages = %#v, want the retired todo, subagents-j0k3r, and ask-user-question packages gone and the rest untouched", settings.Packages)
	}
}

func TestMergePiSettingsFileRemovesLegacySubagentPackages(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings dir) error = %v", err)
	}
	initial := `{
  "theme": "kanagawa",
  "packages": [
    "npm:pi-subagents",
    "npm:pi-subagents@1.0.0",
    "vendor/pi-subagents",
    "vendor/pi-subagents-fixed@0.0.1",
    "npm:pi-web-access",
    "npm:other@1.0.0"
  ]
}`
	if err := os.WriteFile(settingsPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile(settings) error = %v", err)
	}

	applyPiSettingsPrune(t, settingsPath)

	var settings struct {
		Packages []string `json:"packages"`
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("ReadFile(settings) error = %v", err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("Unmarshal(settings) error = %v", err)
	}

	for _, forbidden := range []string{"npm:pi-subagents", "npm:pi-subagents@1.0.0", "vendor/pi-subagents", "vendor/pi-subagents-fixed@0.0.1"} {
		for _, pkg := range settings.Packages {
			if pkg == forbidden {
				t.Fatalf("packages still contains legacy subagent package %q: %#v", forbidden, settings.Packages)
			}
		}
	}
	if !reflect.DeepEqual(settings.Packages, []string{"npm:pi-web-access", "npm:other@1.0.0"}) {
		t.Fatalf("packages = %#v", settings.Packages)
	}
}

func TestProvisionEngramMCPRetiresAdapterAndMigratesLegacyServers(t *testing.T) {
	a := NewAdapter()
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pi", "agent")
	settingsPath := filepath.Join(agentDir, "settings.json")
	npmPackagePath := filepath.Join(agentDir, "npm", "package.json")
	mcpPath := filepath.Join(agentDir, "mcp.json")
	legacyPath := filepath.Join(agentDir, "mcp-adapter.json")

	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	mustWrite(settingsPath, `{"theme":"kanagawa","packages":["npm:gentle-pi@2.5.0","npm:pi-mcp-adapter@2.6.0"]}`)
	mustWrite(npmPackagePath, `{"name":"pi-user","dependencies":{"left-pad":"^1.0.0","pi-mcp-adapter":"^2.6.0"}}`)
	mustWrite(legacyPath, `{"mcpServers":{"context7":{"command":"npx"},"engram":{"command":"/opt/engram"}}}`)

	changed, paths, err := a.ProvisionEngramMCP(home)
	if err != nil {
		t.Fatalf("ProvisionEngramMCP() error = %v", err)
	}
	if !changed {
		t.Fatalf("ProvisionEngramMCP() changed = false, want the adapter retired and servers migrated")
	}
	wantPaths := []string{settingsPath, npmPackagePath, mcpPath}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("ProvisionEngramMCP() paths = %v, want %v", paths, wantPaths)
	}

	settings := readTestJSON(t, settingsPath)
	if got, _ := settings["packages"].([]any); len(got) != 1 || got[0] != "npm:gentle-pi@2.5.0" {
		t.Fatalf("settings packages = %#v, want only npm:gentle-pi@2.5.0", settings["packages"])
	}
	npmPackage := readTestJSON(t, npmPackagePath)
	dependencies, _ := npmPackage["dependencies"].(map[string]any)
	if _, present := dependencies["pi-mcp-adapter"]; present {
		t.Fatalf("npm dependencies = %#v, want pi-mcp-adapter removed", dependencies)
	}
	if _, present := dependencies["left-pad"]; !present {
		t.Fatalf("npm dependencies = %#v, want left-pad preserved", dependencies)
	}

	migrated := readTestJSON(t, mcpPath)
	servers, _ := migrated["mcpServers"].(map[string]any)
	if servers["context7"] == nil || servers["engram"] == nil {
		t.Fatalf("mcp.json mcpServers = %#v, want context7 and engram migrated from mcp-adapter.json", servers)
	}
	if !reflect.DeepEqual(servers["engram"], map[string]any{"command": "/opt/engram"}) {
		t.Fatalf("migrated engram server = %#v, want the user's legacy definition untouched", servers["engram"])
	}

	// mcp-adapter.json is never modified or removed, so the user can roll back.
	legacyBody, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("ReadFile(mcp-adapter.json) error = %v", err)
	}
	if string(legacyBody) != `{"mcpServers":{"context7":{"command":"npx"},"engram":{"command":"/opt/engram"}}}` {
		t.Fatalf("mcp-adapter.json = %s, want byte-identical original", legacyBody)
	}

	// Second run: nothing left to change.
	secondChanged, secondPaths, err := a.ProvisionEngramMCP(home)
	if err != nil {
		t.Fatalf("ProvisionEngramMCP() second error = %v", err)
	}
	if secondChanged || len(secondPaths) != 0 {
		t.Fatalf("ProvisionEngramMCP() second = (changed %v, paths %v), want idempotent no-op", secondChanged, secondPaths)
	}
}

func TestProvisionEngramMCPMigrationPreservesExistingMCPServers(t *testing.T) {
	a := NewAdapter()
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pi", "agent")
	mcpPath := filepath.Join(agentDir, "mcp.json")

	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	mustWrite(filepath.Join(agentDir, "mcp-adapter.json"), `{"mcpServers":{"context7":{"command":"legacy-npx"},"user-server":{"command":"uvx"}}}`)
	mustWrite(mcpPath, `{"mcpServers":{"context7":{"command":"user-npx"},"other":{"command":"node"}}}`)

	changed, paths, err := a.ProvisionEngramMCP(home)
	if err != nil {
		t.Fatalf("ProvisionEngramMCP() error = %v", err)
	}
	if !changed || len(paths) != 1 || paths[0] != mcpPath {
		t.Fatalf("ProvisionEngramMCP() = (changed %v, paths %v), want only %q written", changed, paths, mcpPath)
	}

	migrated := readTestJSON(t, mcpPath)
	servers, _ := migrated["mcpServers"].(map[string]any)
	if !reflect.DeepEqual(servers["context7"], map[string]any{"command": "user-npx"}) {
		t.Fatalf("context7 = %#v, want the existing mcp.json entry to win over the legacy one", servers["context7"])
	}
	if !reflect.DeepEqual(servers["user-server"], map[string]any{"command": "uvx"}) {
		t.Fatalf("user-server = %#v, want the legacy-only entry migrated", servers["user-server"])
	}
	if !reflect.DeepEqual(servers["other"], map[string]any{"command": "node"}) {
		t.Fatalf("other = %#v, want the unrelated native entry preserved", servers["other"])
	}
}

func TestProvisionEngramMCPFailsSafelyOnMalformedInput(t *testing.T) {
	a := NewAdapter()
	wellFormed := map[string]string{
		"settings.json":                      `{"packages":["npm:pi-mcp-adapter"]}`,
		filepath.Join("npm", "package.json"): `{"dependencies":{"pi-mcp-adapter":"^2.6.0"}}`,
		"mcp-adapter.json":                   `{"mcpServers":{"context7":{"command":"npx"}}}`,
		"mcp.json":                           `{"mcpServers":{}}`,
	}
	malformed := map[string]string{
		"settings.json":                      `{"packages":`,
		filepath.Join("npm", "package.json"): `{"dependencies":`,
		"mcp-adapter.json":                   `{"mcpServers":`,
		"mcp.json":                           `{"mcpServers":["context7"`,
	}

	for name := range wellFormed {
		t.Run(filepath.Base(name), func(t *testing.T) {
			home := t.TempDir()
			agentDir := filepath.Join(home, ".pi", "agent")
			if err := os.MkdirAll(filepath.Join(agentDir, "npm"), 0o755); err != nil {
				t.Fatal(err)
			}
			for companion, body := range wellFormed {
				if companion == name {
					continue
				}
				if err := os.WriteFile(filepath.Join(agentDir, companion), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			malformedBody := malformed[name]
			path := filepath.Join(agentDir, name)
			if err := os.WriteFile(path, []byte(malformedBody), 0o644); err != nil {
				t.Fatal(err)
			}

			changed, paths, err := a.ProvisionEngramMCP(home)
			if err == nil {
				t.Fatalf("ProvisionEngramMCP() with malformed %s = (changed %v, paths %v, nil error), want a fail-closed error", name, changed, paths)
			}
			if changed || len(paths) != 0 {
				t.Fatalf("ProvisionEngramMCP() with malformed %s = (changed %v, paths %v), want no partial writes reported", name, changed, paths)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != malformedBody {
				t.Fatalf("malformed %s = %s, want byte-identical original (no data loss)", name, data)
			}
			// Every other participating file must also survive byte-for-byte:
			// a malformed later file must not leave an earlier step (the
			// adapter pruning) already applied with no migrated servers.
			for companion, body := range wellFormed {
				if companion == name {
					continue
				}
				data, err := os.ReadFile(filepath.Join(agentDir, companion))
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != body {
					t.Fatalf("companion %s = %s, want byte-identical original after the failed migration", companion, data)
				}
			}
		})
	}
}

func TestProvisionEngramMCPRestoresCompanionsWhenALateWriteFails(t *testing.T) {
	a := NewAdapter()
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pi", "agent")
	settingsPath := filepath.Join(agentDir, "settings.json")
	npmPackagePath := filepath.Join(agentDir, "npm", "package.json")
	mcpPath := filepath.Join(agentDir, "mcp.json")

	origSettings := `{"theme":"kanagawa","packages":["npm:gentle-pi@2.5.0","npm:pi-mcp-adapter@2.6.0"]}`
	origNPM := `{"name":"pi-user","dependencies":{"left-pad":"^1.0.0","pi-mcp-adapter":"^2.6.0"}}`
	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	mustWrite(settingsPath, origSettings)
	mustWrite(npmPackagePath, origNPM)
	mustWrite(filepath.Join(agentDir, "mcp-adapter.json"), `{"mcpServers":{"context7":{"command":"npx"}}}`)

	restore := writePiJSONFileAtomic
	writePiJSONFileAtomic = func(path string, content []byte, perm fs.FileMode) (filemerge.WriteResult, error) {
		if path == mcpPath {
			return filemerge.WriteResult{}, fmt.Errorf("injected late write failure for %q", path)
		}
		return restore(path, content, perm)
	}
	t.Cleanup(func() { writePiJSONFileAtomic = restore })

	changed, paths, err := a.ProvisionEngramMCP(home)
	if err == nil {
		t.Fatalf("ProvisionEngramMCP() = nil error, want the injected late write failure surfaced")
	}
	// Rollback succeeded, so the reported metadata must describe disk: the
	// migration changed nothing that survives.
	if changed || len(paths) != 0 {
		t.Fatalf("ProvisionEngramMCP() = (changed %v, paths %v), want no surviving writes after rollback", changed, paths)
	}
	legacyPath := filepath.Join(agentDir, "mcp-adapter.json")
	for name, want := range map[string]string{
		settingsPath:   origSettings,
		npmPackagePath: origNPM,
		legacyPath:     `{"mcpServers":{"context7":{"command":"npx"}}}`,
	} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s = %s, want byte-identical original after rollback", name, data)
		}
	}
	if _, err := os.Stat(mcpPath); !os.IsNotExist(err) {
		t.Fatalf("mcp.json stat err = %v, want IsNotExist (migration must not create it when it fails)", err)
	}
}

func TestProvisionEngramMCPRollbacksAWriteThatLandedDespiteItsError(t *testing.T) {
	a := NewAdapter()
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pi", "agent")
	settingsPath := filepath.Join(agentDir, "settings.json")
	npmPackagePath := filepath.Join(agentDir, "npm", "package.json")
	mcpPath := filepath.Join(agentDir, "mcp.json")

	origSettings := `{"theme":"kanagawa","packages":["npm:gentle-pi@2.5.0","npm:pi-mcp-adapter@2.6.0"]}`
	origNPM := `{"name":"pi-user","dependencies":{"left-pad":"^1.0.0","pi-mcp-adapter":"^2.6.0"}}`
	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	mustWrite(settingsPath, origSettings)
	mustWrite(npmPackagePath, origNPM)
	mustWrite(filepath.Join(agentDir, "mcp-adapter.json"), `{"mcpServers":{"context7":{"command":"npx"}}}`)

	restore := writePiJSONFileAtomic
	writePiJSONFileAtomic = func(path string, content []byte, perm fs.FileMode) (filemerge.WriteResult, error) {
		if path == mcpPath {
			// #1676 shape: the rename landed but a later durability step failed,
			// so the writer reports Changed=true alongside the error.
			if _, err := restore(path, content, perm); err != nil {
				t.Fatalf("seed landed write error = %v", err)
			}
			return filemerge.WriteResult{Changed: true, Created: true}, fmt.Errorf("injected landed write failure for %q", path)
		}
		return restore(path, content, perm)
	}
	t.Cleanup(func() { writePiJSONFileAtomic = restore })

	changed, paths, err := a.ProvisionEngramMCP(home)
	if err == nil {
		t.Fatalf("ProvisionEngramMCP() = nil error, want the injected landed write failure surfaced")
	}
	if changed || len(paths) != 0 {
		t.Fatalf("ProvisionEngramMCP() = (changed %v, paths %v), want no surviving writes after rollback", changed, paths)
	}
	if _, err := os.Stat(mcpPath); !os.IsNotExist(err) {
		t.Fatalf("mcp.json stat err = %v, want IsNotExist (a landed-but-failed write must still be rolled back)", err)
	}
	for path, want := range map[string]string{
		settingsPath:   origSettings,
		npmPackagePath: origNPM,
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s = %s, want byte-identical original after rollback", path, data)
		}
	}
}

func TestProvisionEngramMCPReportsUnrestorablePathsWhenRollbackFails(t *testing.T) {
	a := NewAdapter()
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pi", "agent")
	settingsPath := filepath.Join(agentDir, "settings.json")
	npmPackagePath := filepath.Join(agentDir, "npm", "package.json")
	mcpPath := filepath.Join(agentDir, "mcp.json")

	origSettings := `{"theme":"kanagawa","packages":["npm:gentle-pi@2.5.0","npm:pi-mcp-adapter@2.6.0"]}`
	origNPM := `{"name":"pi-user","dependencies":{"left-pad":"^1.0.0","pi-mcp-adapter":"^2.6.0"}}`
	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	mustWrite(settingsPath, origSettings)
	mustWrite(npmPackagePath, origNPM)
	mustWrite(filepath.Join(agentDir, "mcp-adapter.json"), `{"mcpServers":{"context7":{"command":"npx"}}}`)

	restore := writePiJSONFileAtomic
	writePiJSONFileAtomic = func(path string, content []byte, perm fs.FileMode) (filemerge.WriteResult, error) {
		if path == mcpPath {
			return filemerge.WriteResult{}, fmt.Errorf("injected late write failure for %q", path)
		}
		// The rollback restore of settings.json replays the exact original
		// bytes; fail that too so one file stays migrated.
		if path == settingsPath && bytes.Equal(content, []byte(origSettings)) {
			return filemerge.WriteResult{}, fmt.Errorf("injected rollback failure for %q", path)
		}
		return restore(path, content, perm)
	}
	t.Cleanup(func() { writePiJSONFileAtomic = restore })

	changed, paths, err := a.ProvisionEngramMCP(home)
	if err == nil {
		t.Fatalf("ProvisionEngramMCP() = nil error, want the combined write and rollback failure surfaced")
	}
	if !strings.Contains(err.Error(), settingsPath) {
		t.Fatalf("ProvisionEngramMCP() error = %v, want it to name the unrestorable path %q", err, settingsPath)
	}
	// Truthful metadata: settings.json could not be restored and still holds
	// the pruned adapter state; npm/package.json was restored.
	if !changed {
		t.Fatalf("ProvisionEngramMCP() changed = false, want true while %q keeps migrated bytes", settingsPath)
	}
	if !reflect.DeepEqual(paths, []string{settingsPath}) {
		t.Fatalf("ProvisionEngramMCP() paths = %v, want only the unrestorable %q", paths, settingsPath)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "pi-mcp-adapter") {
		t.Fatalf("settings.json = %s, want the pruned state the truthful report describes", data)
	}
	npmData, err := os.ReadFile(npmPackagePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(npmData) != origNPM {
		t.Fatalf("npm/package.json = %s, want byte-identical original after its restore succeeded", npmData)
	}
	if _, err := os.Stat(mcpPath); !os.IsNotExist(err) {
		t.Fatalf("mcp.json stat err = %v, want IsNotExist", err)
	}
}

// TestProvisionEngramMCPReportsUncertainDurabilityWhenARestoreLandsButErrors
// covers the restore-side twin of the #1676 landed-write shape: a rollback
// restore whose rename lands but whose durability step (parent-dir fsync)
// fails reports Changed=true alongside the error. Disk already holds the
// original bytes, so the report must not claim the file still holds migrated
// bytes — and must not claim a clean, durable restoration either.
func TestProvisionEngramMCPReportsUncertainDurabilityWhenARestoreLandsButErrors(t *testing.T) {
	a := NewAdapter()
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pi", "agent")
	settingsPath := filepath.Join(agentDir, "settings.json")
	npmPackagePath := filepath.Join(agentDir, "npm", "package.json")
	mcpPath := filepath.Join(agentDir, "mcp.json")

	origSettings := `{"theme":"kanagawa","packages":["npm:gentle-pi@2.5.0","npm:pi-mcp-adapter@2.6.0"]}`
	origNPM := `{"name":"pi-user","dependencies":{"left-pad":"^1.0.0","pi-mcp-adapter":"^2.6.0"}}`
	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	mustWrite(settingsPath, origSettings)
	mustWrite(npmPackagePath, origNPM)
	mustWrite(filepath.Join(agentDir, "mcp-adapter.json"), `{"mcpServers":{"context7":{"command":"npx"}}}`)

	restore := writePiJSONFileAtomic
	writePiJSONFileAtomic = func(path string, content []byte, perm fs.FileMode) (filemerge.WriteResult, error) {
		if path == mcpPath {
			return filemerge.WriteResult{}, fmt.Errorf("injected late write failure for %q", path)
		}
		// The rollback restore of settings.json replays the exact original
		// bytes; land it but fail the durability step.
		if path == settingsPath && bytes.Equal(content, []byte(origSettings)) {
			if _, err := restore(path, content, perm); err != nil {
				t.Fatalf("seed landed restore error = %v", err)
			}
			return filemerge.WriteResult{Changed: true}, fmt.Errorf("injected restore durability failure for %q", path)
		}
		return restore(path, content, perm)
	}
	t.Cleanup(func() { writePiJSONFileAtomic = restore })

	changed, paths, err := a.ProvisionEngramMCP(home)
	if err == nil {
		t.Fatalf("ProvisionEngramMCP() = nil error, want the restore durability uncertainty surfaced")
	}
	if !strings.Contains(err.Error(), settingsPath) {
		t.Fatalf("ProvisionEngramMCP() error = %v, want it to name the uncertainly restored path %q", err, settingsPath)
	}
	if !strings.Contains(err.Error(), "durability") {
		t.Fatalf("ProvisionEngramMCP() error = %v, want it to report the unconfirmed durability", err)
	}
	if strings.Contains(err.Error(), "could not fully roll back") {
		t.Fatalf("ProvisionEngramMCP() error = %v, want no claim that %q still holds migrated bytes", err, settingsPath)
	}
	// Truthful metadata: disk holds the original bytes, so no migrated change
	// survives and no path may be listed as unrestorable.
	if changed || len(paths) != 0 {
		t.Fatalf("ProvisionEngramMCP() = (changed %v, paths %v), want no surviving writes when disk holds the original bytes", changed, paths)
	}
	for path, want := range map[string]string{
		settingsPath:   origSettings,
		npmPackagePath: origNPM,
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s = %s, want byte-identical original after the landed restore", path, data)
		}
	}
	if _, err := os.Stat(mcpPath); !os.IsNotExist(err) {
		t.Fatalf("mcp.json stat err = %v, want IsNotExist", err)
	}
}

// TestProvisionEngramMCPSparesAConcurrentlyReplacedCreatedMCPConfig covers
// rollback removal of a newly created mcp.json when a concurrent writer has
// already replaced it: the removal must verify the expected bytes and a
// regular file type before unlinking, never delete what it did not write,
// and never recurse.
func TestProvisionEngramMCPSparesAConcurrentlyReplacedCreatedMCPConfig(t *testing.T) {
	const unrelatedBody = `{"mcpServers":{"unrelated":{"command":"other"}}}`

	for name, replaceWith := range map[string]func(t *testing.T, path string){
		"unrelated content": func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte(unrelatedBody), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"non-regular file": func(t *testing.T, path string) {
			t.Helper()
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := NewAdapter()
			home := t.TempDir()
			agentDir := filepath.Join(home, ".pi", "agent")
			settingsPath := filepath.Join(agentDir, "settings.json")
			npmPackagePath := filepath.Join(agentDir, "npm", "package.json")
			mcpPath := filepath.Join(agentDir, "mcp.json")

			origSettings := `{"theme":"kanagawa","packages":["npm:gentle-pi@2.5.0","npm:pi-mcp-adapter@2.6.0"]}`
			origNPM := `{"name":"pi-user","dependencies":{"left-pad":"^1.0.0","pi-mcp-adapter":"^2.6.0"}}`
			mustWrite := func(path, body string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("MkdirAll(%q) error = %v", path, err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatalf("WriteFile(%q) error = %v", path, err)
				}
			}
			mustWrite(settingsPath, origSettings)
			mustWrite(npmPackagePath, origNPM)
			mustWrite(filepath.Join(agentDir, "mcp-adapter.json"), `{"mcpServers":{"context7":{"command":"npx"}}}`)

			restore := writePiJSONFileAtomic
			writePiJSONFileAtomic = func(path string, content []byte, perm fs.FileMode) (filemerge.WriteResult, error) {
				if path == mcpPath {
					// The migration's write lands, then a concurrent writer
					// replaces the file before rollback can remove it.
					if _, err := restore(path, content, perm); err != nil {
						t.Fatalf("seed landed write error = %v", err)
					}
					replaceWith(t, mcpPath)
					return filemerge.WriteResult{Changed: true, Created: true}, fmt.Errorf("injected landed write failure for %q", path)
				}
				return restore(path, content, perm)
			}
			t.Cleanup(func() { writePiJSONFileAtomic = restore })

			changed, paths, err := a.ProvisionEngramMCP(home)
			if err == nil {
				t.Fatalf("ProvisionEngramMCP() = nil error, want the concurrent replacement surfaced")
			}
			if !strings.Contains(err.Error(), mcpPath) {
				t.Fatalf("ProvisionEngramMCP() error = %v, want it to name the replaced path %q", err, mcpPath)
			}
			if strings.Contains(err.Error(), "could not fully roll back") {
				t.Fatalf("ProvisionEngramMCP() error = %v, want no claim that %q still holds migrated bytes", err, mcpPath)
			}
			// The replacement is not the migration's to delete, and it holds no
			// migrated bytes, so metadata stays clean while the error explains
			// why removal was skipped.
			if changed || len(paths) != 0 {
				t.Fatalf("ProvisionEngramMCP() = (changed %v, paths %v), want no surviving writes after sparing the replacement", changed, paths)
			}
			info, err := os.Lstat(mcpPath)
			if err != nil {
				t.Fatalf("mcp.json stat err = %v, want the concurrent replacement left in place", err)
			}
			if info.Mode().IsRegular() {
				data, err := os.ReadFile(mcpPath)
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != unrelatedBody {
					t.Fatalf("mcp.json = %s, want the unrelated replacement left untouched", data)
				}
			}
			for path, want := range map[string]string{
				settingsPath:   origSettings,
				npmPackagePath: origNPM,
			} {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != want {
					t.Fatalf("%s = %s, want byte-identical original after rollback", path, data)
				}
			}
		})
	}
}

func TestProvisionEngramMCPRejectsNonObjectMCPServers(t *testing.T) {
	a := NewAdapter()
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pi", "agent")
	mcpPath := filepath.Join(agentDir, "mcp.json")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "mcp-adapter.json"), []byte(`{"mcpServers":{"context7":{"command":"npx"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mcpBody := `{"mcpServers":["context7"]}`
	if err := os.WriteFile(mcpPath, []byte(mcpBody), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, _, err := a.ProvisionEngramMCP(home)
	if err == nil {
		t.Fatalf("ProvisionEngramMCP() with non-object mcpServers = nil error, want a fail-closed error")
	}
	if changed {
		t.Fatalf("ProvisionEngramMCP() with non-object mcpServers changed = true, want no writes")
	}
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != mcpBody {
		t.Fatalf("mcp.json = %s, want byte-identical original", data)
	}
}

func TestProvisionEngramMCPCreatesMCPConfigOnlyForMigration(t *testing.T) {
	a := NewAdapter()
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(filepath.Join(agentDir, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"packages":["npm:pi-mcp-adapter@2.6.0"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "npm", "package.json"), []byte(`{"dependencies":{"pi-mcp-adapter":"^2.6.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, paths, err := a.ProvisionEngramMCP(home)
	if err != nil {
		t.Fatalf("ProvisionEngramMCP() error = %v", err)
	}
	if !changed {
		t.Fatalf("ProvisionEngramMCP() changed = false, want the adapter pruned")
	}
	if slices.Contains(paths, filepath.Join(agentDir, "mcp.json")) {
		t.Fatalf("paths = %v, want no mcp.json created when there is no legacy server to migrate", paths)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("mcp.json stat err = %v, want IsNotExist (created only to hold migrated servers)", err)
	}
}

func applyPiSettingsPrune(t *testing.T, path string) {
	t.Helper()
	plan, err := planPrunePiSettingsFile(path)
	if err != nil {
		t.Fatalf("planPrunePiSettingsFile() error = %v", err)
	}
	if plan.content == nil {
		t.Fatalf("planPrunePiSettingsFile() planned no rewrite, want retired packages dropped")
	}
	if _, err := writePiJSONFileAtomic(path, plan.content, 0o644); err != nil {
		t.Fatalf("write pruned settings error = %v", err)
	}
}

func readTestJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", path, err)
	}
	return object
}
