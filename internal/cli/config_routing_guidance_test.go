package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v3/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v3/internal/catalog"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/agentguidance"
	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
)

// Routing guidance is installed for every agent that can hold it, never because
// a component was selected: an agent without it has no way to choose between
// direct, delegated and proposed work. The renderer staged components only, so a
// declaratively rendered home silently lost the guidance the installer always
// writes -- and with it the mandatory ODD protocol.
//
// The comparison is against the injector the installer itself calls, so this
// test states parity rather than a second copy of the expected prose.
func TestStagedRoutingGuidanceMatchesTheInstaller(t *testing.T) {
	for _, entry := range catalog.AllAgents() {
		agent := entry.ID
		adapter, err := agents.NewAdapter(agent)
		if err != nil || agent == model.AgentPi || !adapter.SupportsSystemPrompt() {
			continue
		}

		t.Run(string(agent), func(t *testing.T) {
			want := installerRoutingBlock(t, agent)
			document := `{"version":"v1","selection":{"agents":["` + string(agent) + `"]}}`
			stage, _ := renderDocument(t, document)

			delivered := stagedRoutingText(t, stage, adapter, agent)
			// Shared injection can place agent-specific assignments between these sections.
			// Compare each installer-owned section without requiring adjacency.
			marker := "<!-- gentle-ai:remote-authorization -->"
			sections := strings.SplitN(want, marker, 2)
			if len(sections) != 2 {
				t.Fatalf("installer guidance for %q lacks remote authorization section", agent)
			}
			for _, section := range []string{strings.TrimSpace(sections[0]), marker + sections[1]} {
				if !strings.Contains(delivered, section) {
					t.Errorf("staged tree for %q does not carry an installer guidance section\n--- want ---\n%s\n--- staged ---\n%s", agent, section, delivered)
				}
			}
		})
	}
}

// The ODD protocol is the reason this injection exists: it is what tells an
// agent that organic driven development is the default and where a feature's
// task document lives. Asserted separately from the parity check above so a
// future change to the injector cannot quietly drop it from an install.
func TestStagedClaudeGuidanceCarriesTheODDProtocol(t *testing.T) {
	stage, _ := renderDocument(t, `{"version":"v1","selection":{"agents":["claude-code"]}}`)

	content, err := os.ReadFile(filepath.Join(stage, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read staged CLAUDE.md: %v (staged %v)", err, stagedFiles(t, stage))
	}

	for _, want := range []string{
		"Organic Driven Development (ODD) is the predefined workflow of this orchestrator.",
		"### ODD protocol (MANDATORY, in this order, on every request)",
		"**Authorize.**",
		"odd/tasks/<feature-name>.md",
		"odd/<feature-name>/tasks",
	} {
		if !strings.Contains(string(content), want) {
			t.Errorf("staged CLAUDE.md does not mention %q; the render is not carrying the ODD protocol", want)
		}
	}
}

// Pi is the one agent the installer deliberately does not write guidance for:
// gentle-pi owns the Pi parent's instructions and the installer only retires
// legacy sections from it. Staging one would describe an installation that does
// not exist.
func TestStagedRoutingGuidanceSkipsPi(t *testing.T) {
	stage, _ := renderDocument(t, `{"version":"v1","selection":{"agents":["pi"]}}`)

	marker := "<!-- gentle-ai:" + agentguidance.RoutingSectionID + " -->"
	for _, path := range stagedFiles(t, stage) {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(content), marker) {
			t.Errorf("%s carries routing guidance, but gentle-pi owns the Pi parent's guidance", path)
		}
	}
}

// installerRoutingBlock is the exact block an install writes: the injector's
// rendering with the remote-authorization boundary it appends before delivery.
func installerRoutingBlock(t *testing.T, agent model.AgentID) string {
	t.Helper()

	rendered, err := agentguidance.RenderRouting(agent)
	if err != nil {
		t.Fatalf("render routing guidance for %q: %v", agent, err)
	}

	return strings.TrimSpace(agentguidance.InjectRemoteAuthorization(rendered))
}

// stagedRoutingText returns the delivered guidance where the adapter keeps it,
// which differs by delivery kind: a marker section in the system prompt file, a
// standalone Jinja module, or the managed orchestrator prompt inside a settings
// document.
func stagedRoutingText(t *testing.T, stage string, adapter agents.Adapter, agent model.AgentID) string {
	t.Helper()

	switch {
	case agentguidance.DeliversThroughOrchestratorPrompt(agent):
		path := adapter.SettingsPath(stage)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read staged settings %q: %v (staged %v)", path, err, stagedFiles(t, stage))
		}

		return managedOrchestratorPromptFromJSON(t, content)

	case adapter.SystemPromptStrategy() == model.StrategyJinjaModules:
		path := filepath.Join(adapter.GlobalConfigDir(stage), agentguidance.RoutingSectionID+".md")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read staged routing module %q: %v (staged %v)", path, err, stagedFiles(t, stage))
		}

		return string(content)

	default:
		path := adapter.SystemPromptFile(stage)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read staged system prompt %q: %v (staged %v)", path, err, stagedFiles(t, stage))
		}

		return string(content)
	}
}

// managedOrchestratorPromptFromJSON finds the guidance inside a staged settings
// document without importing the shape of the orchestrator definition: the
// string that carries the section is the one that matters, wherever it sits.
func managedOrchestratorPromptFromJSON(t *testing.T, content []byte) string {
	t.Helper()

	var document map[string]any
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatalf("decode staged settings: %v\n%s", err, content)
	}

	var found string
	var walk func(value any) bool
	walk = func(value any) bool {
		switch typed := value.(type) {
		case string:
			if strings.Contains(typed, agentguidance.RoutingSectionID) {
				found = typed

				return true
			}

		case map[string]any:
			for _, child := range typed {
				if walk(child) {
					return true
				}
			}

		case []any:
			for _, child := range typed {
				if walk(child) {
					return true
				}
			}
		}

		return false
	}
	walk(document)

	if found == "" {
		t.Fatalf("staged settings carry no %q section:\n%s", agentguidance.RoutingSectionID, content)
	}

	return found
}
