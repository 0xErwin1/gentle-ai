package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A declared rule adds to the guardrails gentle-ai ships. JSON merge replaces an
// array wholesale, so without an explicit union a document that denies one more
// command would delete every deny it did not restate.
func TestDeclaredPermissionsAddToShippedGuardrails(t *testing.T) {
	shipped, _ := renderDocument(t, `{"version":"v1","selection":{"agents":["claude-code"],"components":["permissions"]}}`)
	baseline := claudePermissions(t, shipped)

	stage, _ := renderDocument(t, `{"version":"v1","selection":{"agents":["claude-code"],"components":["permissions"],"permissions":{"deny":["Bash(curl *)"],"ask":["Edit(*.tf)"]}}}`)
	declared := claudePermissions(t, stage)

	if len(baseline["deny"]) == 0 {
		t.Fatal("the shipped overlay wrote no deny rules, so this proves nothing")
	}
	for _, rule := range baseline["deny"] {
		if !slices.Contains(declared["deny"], rule) {
			t.Errorf("declaring a rule dropped the shipped deny %q", rule)
		}
	}
	if !slices.Contains(declared["deny"], "Bash(curl *)") {
		t.Error("the declared deny is absent")
	}
	if !slices.Contains(declared["ask"], "Edit(*.tf)") {
		t.Error("the declared ask is absent")
	}
}

// A declared MCP server reaches the adapter's own settings shape.
func TestDeclaredMCPServerIsRendered(t *testing.T) {
	stage, result := renderDocument(t, `{"version":"v1","selection":{"agents":["opencode"],"mcpServers":{"atlas":{"command":"atlas","args":["mcp"]}}}}`)

	if codes := diagnosticCodes(t, result); len(codes) != 0 {
		t.Fatalf("diagnostics = %v, want none", codes)
	}

	contents, err := os.ReadFile(filepath.Join(stage, ".config", "opencode", "opencode.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings struct {
		MCP map[string]any `json:"mcp"`
	}
	if err := json.Unmarshal(contents, &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if _, declared := settings.MCP["atlas"]; !declared {
		t.Errorf("declared MCP server is absent: %s", contents)
	}
}

func claudePermissions(t *testing.T, stage string) map[string][]string {
	t.Helper()

	contents, err := os.ReadFile(filepath.Join(stage, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings struct {
		Permissions map[string]json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal(contents, &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}

	rules := map[string][]string{}
	for key, raw := range settings.Permissions {
		var values []string
		if json.Unmarshal(raw, &values) == nil {
			rules[key] = values
		}
	}

	return rules
}
