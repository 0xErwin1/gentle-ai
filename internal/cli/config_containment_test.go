package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v2/internal/catalog"
)

// Staging redirects an adapter's writes by handing it the staging root in place
// of the home directory. That only holds while an adapter derives its paths from
// the root it is given. Several read an environment variable instead — Windsurf
// and the VS Code family consult XDG_CONFIG_HOME — so for them the substitution
// is ignored and a render, which must not mutate anything, would write into the
// live configuration directory.
//
// This is checked here rather than trusted, because the protection that exists
// today is accidental: those adapters have no renderer yet, so staging never
// reaches them. Registering one would remove the accident.
func TestStagingPathsStayUnderTheStagingRoot(t *testing.T) {
	stage := t.TempDir()

	// The environment is what makes this bite: with XDG_CONFIG_HOME unset those
	// adapters fall back to a path under the root they were given, and the
	// escape disappears. Most Linux desktops set it, so the test sets it too.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())

	for _, agent := range catalog.AllAgents() {
		adapter, err := agents.NewAdapter(agent.ID)
		if err != nil {
			continue
		}

		for name, path := range map[string]string{
			"config dir":    adapter.GlobalConfigDir(stage),
			"settings":      adapter.SettingsPath(stage),
			"skills":        adapter.SkillsDir(stage),
			"system prompt": adapter.SystemPromptFile(stage),
			"commands":      adapter.CommandsDir(stage),
			"sub-agents":    adapter.SubAgentsDir(stage),
		} {
			if strings.TrimSpace(path) == "" {
				continue
			}
			if !underRoot(stage, path) {
				t.Errorf("%s: %s resolves to %q, outside the staging root; staging that adapter would write to the live system", agent.ID, name, path)
			}
		}
	}
}

func underRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}

	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
