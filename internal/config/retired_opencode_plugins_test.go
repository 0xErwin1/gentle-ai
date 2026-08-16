package config

import (
	"testing"
)

// Upstream retired every shipped OpenCode plugin (Pi and OpenCode now provide
// the functionality natively, and installed extensions shadow built-in
// behavior). A document that explicitly requests openCodePlugins must be
// refused clearly instead of being silently accepted and dropped, and the
// request must never reach model.Selection.
func TestRetiredOpenCodePluginsAreRefused(t *testing.T) {
	document := `{"version":"v1","selection":{"openCodePlugins":["gentle-logo"]}}`

	state, diagnostics := Decode([]byte(document))
	if len(diagnostics) == 0 {
		t.Fatalf("Decode(openCodePlugins) diagnostics = none, want a retirement refusal")
	}
	if diagnostics[0].Code != "config.opencode-plugin.retired" {
		t.Fatalf("diagnostic code = %q, want config.opencode-plugin.retired", diagnostics[0].Code)
	}

	// The retired list cannot reach model.Selection at all: the field no
	// longer exists there (compile-enforced), so Project succeeding without
	// it is the no-carry guarantee.
	if projected := Project(state); len(projected.CommunityTools) != 0 {
		t.Fatalf("Project() unexpectedly carried community tools: %v", projected.CommunityTools)
	}
}

// An empty openCodePlugins array is a no-op: nothing is requested, nothing is
// refused.
func TestEmptyOpenCodePluginsListIsAccepted(t *testing.T) {
	document := `{"version":"v1","selection":{"openCodePlugins":[]}}`

	_, diagnostics := Decode([]byte(document))
	if len(diagnostics) != 0 {
		t.Fatalf("Decode(empty openCodePlugins) diagnostics = %v, want none", diagnostics)
	}
}
