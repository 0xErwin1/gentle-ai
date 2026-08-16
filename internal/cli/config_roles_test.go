package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func renderDocument(t *testing.T, document string) (string, map[string]any) {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	output := new(bytes.Buffer)
	if err := RunConfig([]string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, output); err != nil {
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

// An adapter that keeps agents as files materialises a declared role as one,
// carrying only what the document declared.
func TestRoleIsRenderedForAnAdapterThatKeepsAgentsAsFiles(t *testing.T) {
	stage, result := renderDocument(t, `{"version":"v1","selection":{"agents":["claude-code"]},"roles":[{"id":"reviewer","description":"Reviews a diff","prompt":"You review changes.","tools":["Read"],"model":{"provider":"anthropic","model":"claude-sonnet","effort":"high"}}]}`)

	if codes := diagnosticCodes(t, result); len(codes) != 0 {
		t.Fatalf("diagnostics = %v, want none", codes)
	}

	contents, err := os.ReadFile(filepath.Join(stage, ".claude", "agents", "reviewer.md"))
	if err != nil {
		t.Fatalf("read rendered role: %v", err)
	}
	for _, want := range []string{"name: reviewer", "description: Reviews a diff", "model: claude-sonnet", "effort: high", "tools: Read", "You review changes."} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("rendered role is missing %q:\n%s", want, contents)
		}
	}
}

// An undeclared field stays out of the rendered file: inventing a default would
// make the agent disagree with the document that produced it.
func TestRenderedRoleOmitsWhatTheDocumentDidNotDeclare(t *testing.T) {
	stage, _ := renderDocument(t, `{"version":"v1","selection":{"agents":["claude-code"]},"roles":[{"id":"reviewer"}]}`)

	contents, err := os.ReadFile(filepath.Join(stage, ".claude", "agents", "reviewer.md"))
	if err != nil {
		t.Fatalf("read rendered role: %v", err)
	}
	for _, absent := range []string{"description:", "model:", "effort:", "tools:"} {
		if strings.Contains(string(contents), absent) {
			t.Errorf("rendered role invents %q:\n%s", absent, contents)
		}
	}
}

// An adapter with no notion of roles still renders everything else it supports.
// Refusing the whole document would make one unsupported concept cost an
// operator the configuration the adapter can actually take.
func TestAnAdapterWithoutRolesStillRendersItsComponents(t *testing.T) {
	stage, result := renderDocument(t, `{"version":"v1","selection":{"agents":["codex"],"components":["skills"],"skills":["go-testing"]}}`)

	if codes := diagnosticCodes(t, result); len(codes) != 0 {
		t.Fatalf("diagnostics = %v, want none", codes)
	}

	staged := stagedFiles(t, stage)
	if len(staged) == 0 {
		t.Error("an adapter without roles staged nothing at all")
	}
}

// Declaring a role for an adapter that cannot express one is reported, because
// silently dropping it would leave the operator believing it took effect.
func TestRoleDeclaredForAnAdapterThatCannotExpressOneIsReported(t *testing.T) {
	_, result := renderDocument(t, `{"version":"v1","selection":{"agents":["codex"]},"roles":[{"id":"reviewer"}]}`)

	codes := diagnosticCodes(t, result)
	if len(codes) != 1 || codes[0] != "config.role.unsupported-adapter" {
		t.Fatalf("diagnostics = %v, want config.role.unsupported-adapter", codes)
	}
}
