package permissions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents/claude"
)

func TestInjectDeclaredAddsRulesToExistingPermissions(t *testing.T) {
	home := t.TempDir()
	adapter := claude.NewAdapter()
	settingsPath := adapter.SettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{
  "permissions": {
    "allow": ["Bash(existing)"],
    "deny": ["Read(existing)"],
    "ask": ["Edit(existing)"],
    "defaultMode": "default"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := InjectDeclared(home, adapter, Declared{
		Allow: []string{"Bash(declared)"},
		Deny:  []string{"Read(declared)"},
		Ask:   []string{"Edit(declared)"},
	})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Permissions struct {
			Allow       []string `json:"allow"`
			Deny        []string `json:"deny"`
			Ask         []string `json:"ask"`
			DefaultMode string   `json:"defaultMode"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(settings.Permissions.Allow, []string{"Bash(existing)", "Bash(declared)"}) {
		t.Errorf("allow = %v", settings.Permissions.Allow)
	}
	if !reflect.DeepEqual(settings.Permissions.Deny, []string{"Read(existing)", "Read(declared)"}) {
		t.Errorf("deny = %v", settings.Permissions.Deny)
	}
	if !reflect.DeepEqual(settings.Permissions.Ask, []string{"Edit(existing)", "Edit(declared)"}) {
		t.Errorf("ask = %v", settings.Permissions.Ask)
	}
	if settings.Permissions.DefaultMode != "default" {
		t.Errorf("defaultMode = %q, want existing value", settings.Permissions.DefaultMode)
	}
}
