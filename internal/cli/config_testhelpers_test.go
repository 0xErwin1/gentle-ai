package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renderDocument stages a document and returns the stage directory and the
// decoded render result, so a caller can assert on both what was written and
// what diagnostics came back.
func renderDocument(t *testing.T, document string) (string, map[string]any) {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	output := new(bytes.Buffer)
	// A rejected document reports its diagnostics and then fails, so a caller
	// asserting on those diagnostics is asserting on a failing run.
	if err := RunConfig([]string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, output); err != nil && !strings.HasPrefix(err.Error(), "configuration rejected:") {
		t.Fatalf("render: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v\n%s", err, output)
	}

	return stage, result
}

func diagnosticCodes(t *testing.T, result map[string]any) []string {
	t.Helper()

	raw, _ := result["diagnostics"].([]any)
	codes := make([]string, 0, len(raw))
	for _, entry := range raw {
		diagnostic, _ := entry.(map[string]any)
		code, _ := diagnostic["code"].(string)
		codes = append(codes, code)
	}

	return codes
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
