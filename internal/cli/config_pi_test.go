package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi reads its persona from a small runtime config rather than from the prompt
// file every other adapter appends to, so the shared persona injection writes
// nothing for it. The imperative installer knows this and branches; the
// renderer did not, which made a declared persona vanish for a Pi installation
// without a diagnostic to say so.
func TestRenderWritesThePiPersonaConfig(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["pi"],"components":["persona"],"persona":"neutral"}}`
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	assertConfigOutput(t, []string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, `"operation": "render"`)

	personaPath := filepath.Join(stage, ".pi", "gentle-ai", "persona.json")
	content, err := os.ReadFile(personaPath)
	if err != nil {
		t.Fatalf("read staged Pi persona: %v (staged %v)", err, stagedFiles(t, stage))
	}

	var config struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatalf("decode Pi persona: %v\n%s", err, content)
	}
	if config.Mode != "neutral" {
		t.Errorf("mode = %q, want %q", config.Mode, "neutral")
	}
}

// Pi's harness is not files: it is a stack of packages its own tool installs.
// A manifest that lists only the staged configuration describes an installation
// that cannot work, and leaves a consumer rendering the tree with no way to
// learn what else the document asked for.
func TestRenderManifestCarriesThePiPackageStack(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["pi"]}}`
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	output := new(bytes.Buffer)
	if err := RunConfig([]string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", t.TempDir()}, output); err != nil {
		t.Fatalf("render: %v", err)
	}

	var result struct {
		Manifest struct {
			Resources []struct {
				Path     string     `json:"path"`
				Selector string     `json:"selector"`
				Agent    string     `json:"agent"`
				Commands [][]string `json:"commands"`
			} `json:"resources"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\n%s", err, output)
	}

	var commands [][]string
	for _, resource := range result.Manifest.Resources {
		if resource.Selector == "provision" && resource.Agent == "pi" {
			commands = resource.Commands
		}
	}
	if len(commands) == 0 {
		t.Fatalf("no Pi provisioning in manifest: %s", output)
	}

	flattened := make([]string, 0, len(commands))
	for _, command := range commands {
		flattened = append(flattened, strings.Join(command, " "))
	}
	joined := strings.Join(flattened, "\n")
	for _, want := range []string{"pi install npm:gentle-pi", "pi install npm:gentle-engram"} {
		if !strings.Contains(joined, want) {
			t.Errorf("provisioning does not run %q:\n%s", want, joined)
		}
	}
}

// Planning reports what it will not perform. Provisioning an agent's packages
// is not writing bytes, so the plan has to name it rather than let a caller
// read a clean plan as a complete installation, and it must name the agent
// instead of reporting an empty component id.
func TestPlanReportsPendingPiProvisioning(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["pi"]}}`
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	output := new(bytes.Buffer)
	if err := RunConfig([]string{"plan", "--config", configPath, "--home", t.TempDir(), "--destination", t.TempDir(), "--stage", t.TempDir()}, output); err != nil {
		t.Fatalf("plan: %v", err)
	}

	var result struct {
		PendingProvisioning      []string `json:"pendingProvisioning"`
		PendingAgentProvisioning []string `json:"pendingAgentProvisioning"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\n%s", err, output)
	}

	for _, component := range result.PendingProvisioning {
		if component == "" {
			t.Errorf("pending provisioning reports an unnamed component: %s", output)
		}
	}
	if len(result.PendingAgentProvisioning) != 1 || result.PendingAgentProvisioning[0] != "pi" {
		t.Errorf("pendingAgentProvisioning = %v, want [pi]\n%s", result.PendingAgentProvisioning, output)
	}
}

// Every other adapter's install command installs the client itself, which no
// document ever asked Gentle AI to do. Carrying those would turn rendering a
// configuration into installing an editor.
func TestRenderManifestOmitsClientInstallationForOtherAdapters(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["opencode"]}}`
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	output := new(bytes.Buffer)
	if err := RunConfig([]string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", t.TempDir()}, output); err != nil {
		t.Fatalf("render: %v", err)
	}

	if strings.Contains(output.String(), `"commands"`) {
		t.Errorf("manifest carries installation commands for OpenCode:\n%s", output)
	}
}

// A custom persona is the operator's own, so Gentle AI writes no Pi persona
// config for it. Staging one would hand gentle-pi a mode the operator never
// chose, and it would do so on every rebuild.
func TestRenderLeavesACustomPiPersonaAlone(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["pi"],"components":["persona"],"persona":"custom"}}`
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	assertConfigOutput(t, []string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, `"operation": "render"`)

	for _, path := range stagedFiles(t, stage) {
		if strings.Contains(path, filepath.Join("gentle-ai", "persona.json")) {
			t.Errorf("a custom persona staged %q", path)
		}
	}
}
