package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigBackgroundSubagentsProducesNoCommandPlan(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "desired.json")
	if err := os.WriteFile(configPath, []byte(`{"version":"v1","selection":{"agents":["opencode"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "install without dry run", args: []string{"install", "--config", configPath, "--opencode-background-subagents=on"}},
		{name: "install with dry run", args: []string{"install", "--config", configPath, "--opencode-background-subagents=on", "--dry-run"}},
		{name: "sync without dry run", args: []string{"sync", "--config", configPath, "--opencode-background-subagents=on"}},
		{name: "sync with dry run", args: []string{"sync", "--config", configPath, "--opencode-background-subagents=on", "--dry-run"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := RunArgs(test.args, &stdout)
			if err == nil || !strings.Contains(err.Error(), "config.flags.exclusive") {
				t.Fatalf("error = %v, want config.flags.exclusive", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want no plan output", stdout.String())
			}
		})
	}
}
