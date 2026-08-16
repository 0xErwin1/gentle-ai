package render

import (
	"sort"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/catalog"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// Issue #3248 asks the declarative contract to cover every target adapter, so a
// supported adapter without a renderer is unfinished work, not a design choice.
// This list is that work, in code rather than in a document: it may only shrink,
// and every run reports what is left. An entry removed without a provider fails
// the guard, and a provider added without removing its entry fails it too, so
// the list cannot drift out of step with the registry in either direction.
var providersNotYetWritten = map[model.AgentID]bool{
	model.AgentClaudeCode:    true,
	model.AgentKilocode:      true,
	model.AgentGeminiCLI:     true,
	model.AgentCursor:        true,
	model.AgentVSCodeCopilot: true,
	model.AgentCodex:         true,
	model.AgentAntigravity:   true,
	model.AgentWindsurf:      true,
	model.AgentKimi:          true,
	model.AgentQwenCode:      true,
	model.AgentKiroIDE:       true,
	model.AgentOpenClaw:      true,
	model.AgentPi:            true,
	model.AgentTrae:          true,
	model.AgentHermes:        true,
}

func TestEverySupportedAdapterHasAProvider(t *testing.T) {
	remaining := make([]string, 0, len(providersNotYetWritten))

	for _, agent := range catalog.AllAgents() {
		_, hasProvider := ProviderFor(agent.ID)
		pending := providersNotYetWritten[agent.ID]

		switch {
		case hasProvider && pending:
			t.Errorf("%s has a provider but is still listed as not yet written; remove its entry", agent.ID)

		case !hasProvider && !pending:
			t.Errorf("%s is a supported adapter with no provider and no entry; write the provider, or record it as remaining work", agent.ID)

		case !hasProvider:
			remaining = append(remaining, string(agent.ID))
		}
	}

	for entry := range providersNotYetWritten {
		if !catalog.IsSupportedAgent(entry) {
			t.Errorf("%s is listed as remaining work but is not a supported adapter; remove the entry", entry)
		}
	}

	sort.Strings(remaining)
	t.Logf("adapters still without a renderer: %d", len(remaining))
	for _, agent := range remaining {
		t.Logf("  %s", agent)
	}
}
