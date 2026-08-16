package render

import (
	"github.com/gentleman-programming/gentle-ai/v2/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// bespokeProviders hold adapters whose roles live inside a composed settings
// file, where a role is an entry rather than a document. Every other adapter
// that expresses roles at all keeps them as files, which one generic provider
// covers by reading the layout from the adapter itself.
var bespokeProviders = map[model.AgentID]Provider{
	model.AgentOpenCode: OpenCodeProvider{},
}

// ProviderFor returns the renderer for one declared adapter. An adapter that
// cannot express roles has no renderer, which the caller reports rather than
// substituting another adapter's output.
func ProviderFor(agent model.AgentID) (Provider, bool) {
	if provider, ok := bespokeProviders[agent]; ok {
		return provider, true
	}

	adapter, err := agents.NewAdapter(agent)
	if err != nil || !adapter.SupportsSubAgents() {
		return nil, false
	}

	return NewRoleProvider(adapter), true
}
