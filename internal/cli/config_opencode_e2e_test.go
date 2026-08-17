package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configdomain "github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/render"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
)

func TestConfigOpenCodeEndToEnd(t *testing.T) {
	home, destination := t.TempDir(), t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")
	writeConfigDocument(t, configPath, `{"version":"v0","selection":{"agents":["opencode"]},"roles":[{"id":"writer","renderedName":"writer-v1"},{"id":"reviewer","references":["writer"]}]}`)

	assertConfigOutput(t, []string{"validate", "--config", configPath}, `"diagnostics": []`)

	renderDestination := t.TempDir()
	writeUserSettings(t, renderDestination)
	renderStage := t.TempDir()
	assertConfigOutput(t, []string{"render", "--config", configPath, "--destination", renderDestination, "--stage", renderStage}, `"operation": "render"`)
	rendered, err := os.ReadFile(filepath.Join(renderStage, ".config", "opencode", "opencode.json"))
	if err != nil || !strings.Contains(string(rendered), `"theme": "user"`) {
		t.Fatalf("rendered user content = %s, %v", rendered, err)
	}
	for _, operation := range []string{"plan", "diff"} {
		assertConfigOutput(t, []string{operation, "--config", configPath, "--destination", renderDestination, "--stage", t.TempDir()}, `"kind": "create"`)
	}
	assertConfigOutput(t, []string{"export", "--config", configPath}, `"version": "v1"`)

	runConfigMutation(t, "apply", configPath, home, destination)
	desired, err := state.ReadDesired(home)
	if err != nil || desired.Version != configdomain.CurrentVersion || desired.Roles[0].RenderedName != "writer-v1" {
		t.Fatalf("persisted desired = %#v, %v", desired, err)
	}
	// The manifest covers the staged component tree as well, so what this pins
	// is that both declared roles are owned inside the settings file.
	manifest, err := state.ReadManifest(home, destination)
	if err != nil {
		t.Fatalf("read persisted manifest: %v", err)
	}
	owned := 0
	for _, resource := range manifest.Resources {
		if resource.Path == ".config/opencode/opencode.json" {
			owned++
		}
	}
	if owned != 2 {
		t.Fatalf("owned settings resources = %d, want the two declared roles", owned)
	}

	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]},"roles":[{"id":"writer","renderedName":"writer-v2"},{"id":"reviewer","references":["writer"]}]}`)
	runConfigMutation(t, "reconcile", configPath, home, destination)
	settingsPath := filepath.Join(destination, ".config", "opencode", "opencode.json")
	settings, err := os.ReadFile(settingsPath)
	if err != nil || strings.Contains(string(settings), "writer-v1") || !strings.Contains(string(settings), `"writer-v2"`) {
		t.Fatalf("reconciled settings = %s, %v", settings, err)
	}

	beforeInvalid := append([]byte(nil), settings...)
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"],"unknown":true}}`)
	assertConfigOutput(t, []string{"apply", "--config", configPath, "--home", home, "--destination", destination, "--stage", t.TempDir()}, "config.document.unknown-field")
	afterInvalid, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(afterInvalid, beforeInvalid) {
		t.Fatalf("invalid input changed managed settings = %q, %v", afterInvalid, err)
	}

	rollbackHome, rollbackDestination := t.TempDir(), t.TempDir()
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]}}`)
	originalWriteConfigState := writeConfigState
	writeConfigState = func(string, string, configdomain.DesiredState, render.Manifest) error {
		return errors.New("store unavailable")
	}
	t.Cleanup(func() { writeConfigState = originalWriteConfigState })

	var output bytes.Buffer
	err = RunConfig([]string{"apply", "--config", configPath, "--home", rollbackHome, "--destination", rollbackDestination, "--stage", t.TempDir()}, &output)
	if err == nil || !strings.Contains(err.Error(), "persist reconciliation") {
		t.Fatalf("rollback apply error = %v, want persistence failure", err)
	}
	if _, err := os.Stat(filepath.Join(rollbackDestination, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Fatalf("rollback left managed file: %v", err)
	}
}

func assertConfigOutput(t *testing.T, args []string, want string) {
	t.Helper()

	// A rejected document reports its diagnostics on stdout and then fails, so
	// the caller asserting on a diagnostic is asserting on a failing run.
	var output bytes.Buffer
	err := RunConfig(args, &output)
	if err != nil && !strings.HasPrefix(err.Error(), "configuration rejected:") {
		t.Fatalf("RunConfig(%s) error = %v", args[0], err)
	}
	if !strings.Contains(output.String(), want) {
		t.Fatalf("RunConfig(%s) output = %s, want %q", args[0], output.String(), want)
	}
}
