package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// An undeclared orchestrator must stay absent from the encoded document. A
// struct value has no empty state for encoding/json, so a non-pointer field
// would publish an object of zero values and assert an assignment the user
// never made.
func TestUndeclaredOrchestratorIsOmitted(t *testing.T) {
	document := Document{
		Version:   CurrentVersion,
		Selection: Selection{Profiles: []Profile{{Name: "name-only"}}},
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}

	if strings.Contains(string(encoded), "orchestrator") {
		t.Errorf("encoded document publishes an undeclared orchestrator: %s", encoded)
	}
}

func TestDeclaredOrchestratorIsPreserved(t *testing.T) {
	document := `{"version":"v1","selection":{"profiles":[{"name":"cheap","orchestrator":{"provider":"anthropic","model":"claude-haiku","effort":"low"}}]}}`

	state, diagnostics := Decode([]byte(document))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	restored := FromSelection(Project(state))

	orchestrator := restored.Selection.Profiles[0].Orchestrator
	if orchestrator == nil {
		t.Fatal("declared orchestrator was dropped")
	}
	if *orchestrator != (ModelAssignment{Provider: "anthropic", Model: "claude-haiku", Effort: "low"}) {
		t.Errorf("Orchestrator = %+v", *orchestrator)
	}
}

// A profile carrying no orchestrator must survive the round trip without one
// being invented for it.
func TestUndeclaredOrchestratorSurvivesRoundTrip(t *testing.T) {
	document := `{"version":"v1","selection":{"profiles":[{"name":"name-only"}]}}`

	state, diagnostics := Decode([]byte(document))
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}

	restored := FromSelection(Project(state))

	if orchestrator := restored.Selection.Profiles[0].Orchestrator; orchestrator != nil {
		t.Errorf("Orchestrator = %+v, want nil", *orchestrator)
	}
}
