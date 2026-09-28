package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
)

// TestAgentBuilderSkillsDirIn_KimiPrefersCurrentLayout verifies that the
// agent builder routes generated skills to the native kimi-code v0.11+
// skills root when ~/.kimi-code exists as a directory (issue #782).
func TestAgentBuilderSkillsDirIn_KimiPrefersCurrentLayout(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".kimi-code"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := agentBuilderSkillsDirIn(home, model.AgentKimi)
	if !ok {
		t.Fatal("agentBuilderSkillsDirIn(kimi) ok = false, want true")
	}
	if want := filepath.Join(home, ".kimi-code", "skills"); got != want {
		t.Errorf("agentBuilderSkillsDirIn(kimi) = %q, want %q", got, want)
	}
}

// TestAgentBuilderSkillsDirIn_KimiLegacyFallback verifies that without a
// ~/.kimi-code directory the agent builder keeps the shared legacy skills
// path for Kimi.
func TestAgentBuilderSkillsDirIn_KimiLegacyFallback(t *testing.T) {
	home := t.TempDir()

	got, ok := agentBuilderSkillsDirIn(home, model.AgentKimi)
	if !ok {
		t.Fatal("agentBuilderSkillsDirIn(kimi) ok = false, want true")
	}
	if want := filepath.Join(home, ".config", "agents", "skills"); got != want {
		t.Errorf("agentBuilderSkillsDirIn(kimi) = %q, want %q", got, want)
	}
}

// TestAgentBuilderSkillsDirIn_KimiCodeFileIsNotCurrentLayout verifies that a
// plain file named .kimi-code is not treated as the v0.11+ layout.
func TestAgentBuilderSkillsDirIn_KimiCodeFileIsNotCurrentLayout(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".kimi-code"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, _ := agentBuilderSkillsDirIn(home, model.AgentKimi)
	if want := filepath.Join(home, ".config", "agents", "skills"); got != want {
		t.Errorf("agentBuilderSkillsDirIn(kimi) = %q, want legacy %q", got, want)
	}
}
