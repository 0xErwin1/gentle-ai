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
