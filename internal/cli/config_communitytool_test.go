package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A community tool is wired into the clients by its own CLI, not by writing
// files. Rendering the guidance and stopping there leaves a document that
// declared a tool, reported success, and configured nothing the tool can see.
func TestRenderManifestCarriesCommunityToolWiring(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["opencode","claude-code"],"communityTools":["codegraph"]}}`
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
				Selector string     `json:"selector"`
				Tool     string     `json:"tool"`
				Commands [][]string `json:"commands"`
			} `json:"resources"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\n%s", err, output)
	}

	var commands [][]string
	for _, resource := range result.Manifest.Resources {
		if resource.Selector == "provision" && resource.Tool == "codegraph" {
			commands = resource.Commands
		}
	}
	if len(commands) == 0 {
		t.Fatalf("no CodeGraph provisioning in manifest: %s", output)
	}

	joined := strings.Join(commands[0], " ")
	// Claude Code takes CodeGraph's own target wiring. OpenCode does not: Gentle
	// AI reconciles that one itself, so naming it here would ask CodeGraph to
	// write what the render already owns.
	if want := "codegraph install --target claude --location global --yes"; joined != want {
		t.Errorf("command = %q, want %q", joined, want)
	}
}

// The targets come from what the document declared, never from what happens to
// be installed where the render runs: a document that renders different
// commands on two machines is not a declaration of anything.
func TestRenderCommunityToolWiringIgnoresTheLocalMachine(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["codex"],"communityTools":["codegraph"]}}`
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
				Tool     string     `json:"tool"`
				Commands [][]string `json:"commands"`
			} `json:"resources"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\n%s", err, output)
	}

	for _, resource := range result.Manifest.Resources {
		if resource.Tool != "codegraph" {
			continue
		}
		if want := "codegraph install --target codex --location global --yes"; strings.Join(resource.Commands[0], " ") != want {
			t.Errorf("command = %q, want %q", strings.Join(resource.Commands[0], " "), want)
		}
		return
	}

	t.Errorf("no CodeGraph provisioning for the declared adapter:\n%s", output)
}

// A document that declares no tool asks for no wiring, and one that declares a
// tool no adapter can take asks for none either.
func TestRenderOmitsCommunityToolWiringWithoutATarget(t *testing.T) {
	for name, document := range map[string]string{
		"no tool":   `{"version":"v1","selection":{"agents":["claude-code"]}}`,
		"no target": `{"version":"v1","selection":{"agents":["opencode"],"communityTools":["codegraph"]}}`,
	} {
		configPath := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
			t.Fatal(err)
		}

		output := new(bytes.Buffer)
		if err := RunConfig([]string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", t.TempDir()}, output); err != nil {
			t.Fatalf("%s render: %v", name, err)
		}
		if strings.Contains(output.String(), `"tool"`) {
			t.Errorf("%s: manifest carries tool provisioning:\n%s", name, output)
		}
	}
}
