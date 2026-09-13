package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents/pi"
)

// Pi resolves a theme by name against the themes its own packages ship, and no
// package ships one called "gentleman": gentle-pi's is "Gentle". Writing the
// name anyway leaves Pi refusing to start into the configured theme and falling
// back, which is a worse outcome than leaving the setting to Pi.
func TestInjectLeavesThePiThemeToPi(t *testing.T) {
	home := t.TempDir()
	adapter := pi.NewAdapter()

	settingsPath := adapter.SettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"ayu-dark"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Inject(home, adapter)
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if result.Changed {
		t.Errorf("injecting a theme into Pi reported a change")
	}

	content, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "gentleman") {
		t.Errorf("Pi settings carry a theme Pi cannot resolve: %s", content)
	}
	if !strings.Contains(string(content), "ayu-dark") {
		t.Errorf("the theme Pi was configured with was replaced: %s", content)
	}
}
