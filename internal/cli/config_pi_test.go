package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configdomain "github.com/gentleman-programming/gentle-ai/v3/internal/config"
	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
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

// A document that overrides where a Pi package comes from must reach the
// rendered manifest's provision commands, so a consumer installs gentle-pi
// from its git main or gentle-engram from a locally built path instead of
// npm, without changing the shape or count of the provisioning commands.
func TestRenderManifestSubstitutesOverriddenPiPackageSources(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["pi"],"providers":{"pi":{"packages":{"gentle-pi":"git:github.com/Gentleman-Programming/gentle-pi@abc123","gentle-engram":"/nix/store/xyz-gentle-engram-pi"}}}}}`
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
	if len(commands) != 7 {
		t.Fatalf("Pi provisioning commands = %v, want 7 unchanged in order and count: %s", commands, output)
	}

	flattened := make([]string, 0, len(commands))
	for _, command := range commands {
		flattened = append(flattened, strings.Join(command, " "))
	}
	joined := strings.Join(flattened, "\n")
	for _, want := range []string{
		"pi install git:github.com/Gentleman-Programming/gentle-pi@abc123",
		"pi install /nix/store/xyz-gentle-engram-pi",
		"/nix/store/xyz-gentle-engram-pi/bin/pi-engram init",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("provisioning does not run %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "pi install npm:gentle-pi\n") || strings.Contains(joined, "pi install npm:gentle-engram\n") {
		t.Errorf("provisioning still runs the un-overridden npm package:\n%s", joined)
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

// A model.Selection naming a gentle-engram override Pi's adapter cannot
// accept (constructed directly, bypassing the Decode diagnostics that
// normally refuse it) must surface as an error naming the agent, not vanish
// from the returned resources: agentProvisioning's err != nil guard used to
// treat this rejection exactly like "this adapter installs nothing," so the
// Pi provision resource silently disappeared from the manifest while
// rendering still reported success.
func TestAgentProvisioningReturnsErrorForUnsupportedGentleEngramOverride(t *testing.T) {
	selection := model.Selection{
		Agents: []model.AgentID{model.AgentPi},
		PiPackageSources: map[string]string{
			"gentle-engram": "git:github.com/x/y",
		},
	}

	resources, err := agentProvisioning(selection)
	if err == nil {
		t.Fatalf("agentProvisioning() error = nil, want an error naming pi; resources = %#v", resources)
	}
	if !strings.Contains(err.Error(), "pi") {
		t.Errorf("agentProvisioning() error = %q, want it to name pi", err)
	}
}

// The same rejection must reach ProvisionedResources's caller instead of
// being swallowed on the way there, since config render otherwise exits 0
// with a manifest that silently omits Pi's provisioning.
func TestProvisionedResourcesPropagatesProvisioningError(t *testing.T) {
	state := configdomain.DesiredState{
		Version: "v1",
		Selection: configdomain.Selection{
			Agents: []model.AgentID{model.AgentPi},
			Providers: map[model.AgentID]configdomain.ProviderSelection{
				model.AgentPi: {
					Packages: map[string]string{
						"gentle-engram": "git:github.com/x/y",
					},
				},
			},
		},
	}

	stager := configurationStager{}
	resources, err := stager.ProvisionedResources(state)
	if err == nil {
		t.Fatalf("ProvisionedResources() error = nil, want an error naming pi; resources = %#v", resources)
	}
	if !strings.Contains(err.Error(), "pi") {
		t.Errorf("ProvisionedResources() error = %q, want it to name pi", err)
	}
}
