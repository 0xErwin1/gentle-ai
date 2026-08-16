package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigReadOnlyCommandsBypassSystemAndLeaveDestinationUntouched(t *testing.T) {
	destination := t.TempDir()
	livePath := filepath.Join(destination, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(livePath), 0o755); err != nil {
		t.Fatal(err)
	}
	live := []byte(`{"theme":"user"}`)
	if err := os.WriteFile(livePath, live, 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":"v1","selection":{"agents":["opencode"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	oldEnsure, oldDetect := ensureCurrentOSSupported, detectSystem
	t.Cleanup(func() { ensureCurrentOSSupported, detectSystem = oldEnsure, oldDetect })
	called := false
	ensureCurrentOSSupported = func() error { called = true; return nil }

	for _, operation := range []string{"validate", "render", "plan", "diff"} {
		var output bytes.Buffer
		err := RunArgs([]string{"config", operation, "--config", configPath, "--destination", destination, "--stage", t.TempDir()}, &output)
		if err != nil {
			t.Fatalf("config %s: %v", operation, err)
		}
		if !strings.Contains(output.String(), `"operation"`) {
			t.Fatalf("config %s output = %s", operation, output.String())
		}
	}
	if called {
		t.Fatal("read-only config command invoked system detection")
	}
	if got, err := os.ReadFile(livePath); err != nil || !bytes.Equal(got, live) {
		t.Fatalf("live destination changed: %q, %v", got, err)
	}
}

func TestConfigExportAndSemanticFlagRejectionBypassSystemDetection(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":"v1","selection":{"agents":["opencode"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	oldEnsure, oldDetect := ensureCurrentOSSupported, detectSystem
	defer func() { ensureCurrentOSSupported, detectSystem = oldEnsure, oldDetect }()
	called := false
	ensureCurrentOSSupported = func() error { called = true; return nil }

	var output bytes.Buffer
	if err := RunArgs([]string{"config", "export", "--config", configPath, "--home", home}, &output); err != nil {
		t.Fatalf("RunArgs(config export) error = %v", err)
	}
	if err := RunArgs([]string{"install", "--config", configPath, "--agent", "opencode"}, &output); err == nil || !strings.Contains(err.Error(), "config.flags.exclusive") {
		t.Fatalf("RunArgs(install config with semantic flag) error = %v", err)
	}
	if called {
		t.Fatal("config export or semantic rejection invoked system detection")
	}
}
