package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

// An undeclared orchestrator must stay absent from the encoded document. A
// struct value has no empty state for encoding/json, so a non-pointer field
// would publish an object of zero values and assert an assignment the user
// never made.
func TestUndeclaredOrchestratorIsOmitted(t *testing.T) {
	document := Document{
		Version: CurrentVersion,
		Selection: Selection{Providers: map[model.AgentID]ProviderSelection{
			model.AgentOpenCode: {Profiles: map[string]ProviderProfile{"name-only": {}}},
		}},
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}

	if strings.Contains(string(encoded), "orchestrator") {
		t.Errorf("encoded document publishes an undeclared orchestrator: %s", encoded)
	}
}

// The SDD profile runtime is retired upstream, so a document that still
// declares named profiles is refused with the retirement diagnostic instead
// of being silently ignored.
func TestDeclaredProfilesAreRetired(t *testing.T) {
	document := `{"version":"v1","selection":{"providers":{"opencode":{"profiles":{"cheap":{"orchestrator":{"provider":"anthropic","model":"claude-haiku","effort":"low"}}}}}}}`

	_, diagnostics := Decode([]byte(document))

	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %v, want exactly one", diagnostics)
	}
	if diagnostics[0].Code != "config.provider.profiles.retired" {
		t.Errorf("code = %q, want %q", diagnostics[0].Code, "config.provider.profiles.retired")
	}
	if diagnostics[0].Path != "$.selection.providers.opencode.profiles" {
		t.Errorf("path = %q, want the declared profiles path", diagnostics[0].Path)
	}
}

// The profile strategy runtime is retired with the profiles it governed, so a
// document that still declares it is refused the same way.
func TestDeclaredProfileStrategyIsRetired(t *testing.T) {
	document := `{"version":"v1","selection":{"providers":{"opencode":{"profileStrategy":"generated-multi"}}}}`

	_, diagnostics := Decode([]byte(document))

	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %v, want exactly one", diagnostics)
	}
	if diagnostics[0].Code != "config.provider.profile-strategy.retired" {
		t.Errorf("code = %q, want %q", diagnostics[0].Code, "config.provider.profile-strategy.retired")
	}
	if diagnostics[0].Path != "$.selection.providers.opencode.profileStrategy" {
		t.Errorf("path = %q, want the declared profileStrategy path", diagnostics[0].Path)
	}
}
