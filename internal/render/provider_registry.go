package render

import "github.com/gentleman-programming/gentle-ai/v2/internal/model"

// providers maps a declared adapter to the renderer that stages its assets.
// An adapter absent from this map has no rendering support yet, which the
// caller must surface rather than substituting another adapter's output.
var providers = map[model.AgentID]Provider{
	model.AgentOpenCode: OpenCodeProvider{},
}

// ProviderFor returns the renderer for one declared adapter.
func ProviderFor(agent model.AgentID) (Provider, bool) {
	provider, ok := providers[agent]

	return provider, ok
}
