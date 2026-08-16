package render

import (
	"sort"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/catalog"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// These adapters express no agent roles at all: they report
// SupportsSubAgents() == false and expose no settings block that composes them.
// A role declared for one of them has nothing to materialise, which the caller
// reports rather than silently dropping.
//
// The list may only shrink. An entry removed while the adapter still has no
// renderer fails the guard, and an adapter that gains one without losing its
// entry fails it too, so the list cannot drift out of step with the registry in
// either direction.
var providersNotYetWritten = map[model.AgentID]bool{
	model.AgentKilocode:      true,
	model.AgentGeminiCLI:     true,
	model.AgentVSCodeCopilot: true,
	model.AgentCodex:         true,
	model.AgentAntigravity:   true,
	model.AgentWindsurf:      true,
	model.AgentQwenCode:      true,
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
