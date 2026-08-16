package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rendering a document that targets an adapter with no provider used to emit
// OpenCode output and report no diagnostics, which is a silently wrong answer:
// the operator receives configuration for a client they did not declare.
func TestRenderRefusesAnAdapterWithoutAProvider(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["claude-code"]},"roles":[{"id":"reviewer"}]}`
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	output := new(bytes.Buffer)
	if err := RunConfig([]string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, output); err != nil {
		t.Fatalf("render: %v", err)
	}

	var result struct {
		Diagnostics []struct {
			Code    string `json:"code"`
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"diagnostics"`
		Manifest any `json:"manifest"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\n%s", err, output)
	}

	if len(result.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one", result.Diagnostics)
	}
	if got, want := result.Diagnostics[0].Code, "config.provider.unavailable"; got != want {
		t.Errorf("code = %q, want %q", got, want)
	}
	if !strings.Contains(result.Diagnostics[0].Message, "claude-code") {
		t.Errorf("message %q does not name the adapter", result.Diagnostics[0].Message)
	}
	if result.Manifest != nil {
		t.Errorf("a refused render must produce no manifest, got %v", result.Manifest)
	}

	staged := stagedFiles(t, stage)
	if len(staged) != 0 {
		t.Errorf("a refused render must stage nothing, got %v", staged)
	}
}

// A declared adapter that does have a provider keeps rendering exactly as before.
func TestRenderUsesTheProviderOfTheDeclaredAdapter(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["opencode"]},"roles":[{"id":"reviewer"}]}`
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	output := new(bytes.Buffer)
	if err := RunConfig([]string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, output); err != nil {
		t.Fatalf("render: %v", err)
	}

	if !strings.Contains(output.String(), `"manifest"`) {
		t.Fatalf("expected a manifest, got %s", output)
	}

	staged := stagedFiles(t, stage)
	if len(staged) != 1 || !strings.HasSuffix(staged[0], "opencode.json") {
		t.Errorf("staged = %v, want the OpenCode settings file", staged)
	}
}

func stagedFiles(t *testing.T, root string) []string {
	t.Helper()

	files := make([]string, 0)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk stage: %v", err)
	}

	return files
}
