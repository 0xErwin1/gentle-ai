package opencode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const verboseCatalog = `custom/qwen/qwen3
{
  "id": "qwen/qwen3",
  "name": "Qwen 3",
  "capabilities": {"toolcall": true, "reasoning": true},
  "limit": {"context": 32768, "output": 4096},
  "cost": {"input": 0.2, "output": 0.8},
  "variants": {"high": {}, "low": {}}
}
other/plain
{"id":"plain","name":"Plain","capabilities":{"toolcall":false,"reasoning":false}}
`

func TestDiscoverCatalogMapsVerboseOutputAndProjectDirectory(t *testing.T) {
	var got Command
	runner := func(_ context.Context, command Command) (CommandOutput, error) {
		got = command
		return CommandOutput{Stdout: []byte(verboseCatalog)}, nil
	}

	providers, err := DiscoverCatalogWithRunner(context.Background(), `C:\work\project`, runner)
	if err != nil {
		t.Fatalf("DiscoverCatalogWithRunner() error = %v", err)
	}
	if got.Path != "opencode" || got.Dir != `C:\work\project` || strings.Join(got.Args, " ") != "models --verbose" {
		t.Fatalf("command = %+v, want opencode models --verbose in project directory", got)
	}
	model := providers["custom"].Models["qwen/qwen3"]
	if !model.ToolCall || !model.Reasoning || model.Limit.Context != 32768 || model.Cost.Output != 0.8 || strings.Join(model.Variants, ",") != "high,low" {
		t.Fatalf("runtime model = %+v", model)
	}
	if _, ok := providers["other"].Models["plain"]; !ok {
		t.Fatal("missing second provider model")
	}
}

func TestDiscoverCatalogRejectsInvalidOutput(t *testing.T) {
	tests := []struct {
		name string
		out  string
		kind CatalogErrorKind
	}{
		{"truncated JSON", "custom/model\n{\"id\":", CatalogErrorMalformed},
		{"unsupported record", "custom/model\n{\"name\":\"Model\"}", CatalogErrorUnsupportedSchema},
		{"missing tool capability", "custom/model\n{\"id\":\"model\",\"capabilities\":{}}", CatalogErrorUnsupportedSchema},
		{"incompatible tool capability", "custom/model\n{\"id\":\"model\",\"capabilities\":{\"toolcall\":\"true\"}}", CatalogErrorUnsupportedSchema},
		{"provider header mismatch", "custom/other\n{\"id\":\"model\",\"capabilities\":{\"toolcall\":true}}", CatalogErrorUnsupportedSchema},
		{"oversized output", strings.Repeat("x", maxCatalogOutput+1), CatalogErrorOutputTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DiscoverCatalogWithRunner(context.Background(), "project", func(context.Context, Command) (CommandOutput, error) {
				return CommandOutput{Stdout: []byte(tt.out)}, nil
			})
			var catalogErr *CatalogError
			if !errors.As(err, &catalogErr) || catalogErr.Kind != tt.kind {
				t.Fatalf("error = %v, want %v", err, tt.kind)
			}
		})
	}
}

func TestRunCatalogCommandCancelsOverflowingChild(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "catalog-helper")
	if runtime.GOOS == "windows" {
		helper += ".exe"
	}
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\nimport (\"fmt\"; \"strings\")\nfunc main() { for { fmt.Println(strings.Repeat(\"x\", 1024)) } }\n"), 0o600); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	if err := exec.Command("go", "build", "-o", helper, source).Run(); err != nil {
		t.Fatalf("build helper: %v", err)
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := runCatalogCommand(ctx, Command{Path: helper, OutputLimit: 128})
	var catalogErr *CatalogError
	if !errors.As(err, &catalogErr) || catalogErr.Kind != CatalogErrorOutputTooLarge {
		t.Fatalf("error = %v stdout=%d stderr=%d, want output_too_large", err, len(output.Stdout), len(output.Stderr))
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("overflowing helper ran for %v, want prompt cancellation", elapsed)
	}
}

func TestDiscoverCatalogClassifiesCommandFailuresAndEmptyCatalog(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind CatalogErrorKind
	}{
		{"missing binary", &exec.Error{Name: "opencode", Err: os.ErrNotExist}, CatalogErrorMissingBinary},
		{"path binary missing", &exec.Error{Name: "opencode", Err: exec.ErrNotFound}, CatalogErrorMissingBinary},
		{"non-zero exit", &exec.ExitError{}, CatalogErrorCommandFailed},
		{"timeout", context.DeadlineExceeded, CatalogErrorTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DiscoverCatalogWithRunner(context.Background(), "project", func(context.Context, Command) (CommandOutput, error) {
				return CommandOutput{}, tt.err
			})
			var catalogErr *CatalogError
			if !errors.As(err, &catalogErr) || catalogErr.Kind != tt.kind {
				t.Fatalf("error = %v, want %v", err, tt.kind)
			}
		})
	}
	providers, err := DiscoverCatalogWithRunner(context.Background(), "project", func(context.Context, Command) (CommandOutput, error) {
		return CommandOutput{}, nil
	})
	if err != nil || len(providers) != 0 {
		t.Fatalf("empty catalog = %v, %v; want empty successful catalog", providers, err)
	}
}
