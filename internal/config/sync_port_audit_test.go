package config

import (
	"bytes"
	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
	"testing"
)

func TestSyncPortRefusesBeforeSideEffects(t *testing.T) {
	for _, tc := range []struct{ input, code, path, message string }{
		{`{"version":"v1","selection":{"providers":{"opencode":{"profileStrategy":"generated-multi"}}}}`, "config.provider.profile-strategy.retired", "$.selection.providers.opencode.profileStrategy", "SDD profile strategy is retired upstream; remove profileStrategy from the document"},
		{`{"version":"v1","selection":{"providers":{"opencode":{"profiles":{"named":{}}}}}}`, "config.provider.profiles.retired", "$.selection.providers.opencode.profiles", "SDD profiles are retired upstream; remove profiles from the document"},
		{`{"version":"v1","selection":{"providers":{"pi":{"profiles":{"constructor":{}}}}}}`, "config.pi-profile.name-reserved", "$.selection.providers.pi.profiles.constructor", `profile name "constructor" is reserved`},
		{`{"version":"v1","selection":{"providers":{"pi":{"activeProfile":"missing"}}}}`, "config.pi-active-profile.unresolved", "$.selection.providers.pi.activeProfile", `activeProfile "missing" does not name a declared profile`},
	} {
		t.Run(tc.code, func(t *testing.T) {
			input := []byte(tc.input)
			before := append([]byte(nil), input...)
			calls := 0
			ds := Admit(input, func(DesiredState) { calls++ })
			if calls != 0 || !bytes.Equal(input, before) || len(ds) != 1 {
				t.Fatalf("calls=%d diagnostics=%+v", calls, ds)
			}
			d := ds[0]
			if d.Code != tc.code || d.Path != tc.path || d.Message != tc.message || d.Severity != Error {
				t.Fatalf("diagnostic=%+v", d)
			}
		})
	}
}

func TestSyncPortPiProfileRemainsIndependent(t *testing.T) {
	input := []byte(`{"version":"v1","selection":{"agents":["pi"],"providers":{"pi":{"activeProfile":"work","profiles":{"work":{"phaseAssignments":{"sdd-apply":{"provider":"anthropic","model":"claude-sonnet","effort":"high"}}}},"models":{"sdd-apply":{"model":"openai/gpt-5","thinking":"max"}}}}}}`)
	state, ds := Decode(input)
	if len(ds) != 0 {
		t.Fatalf("diagnostics=%+v", ds)
	}
	selected := Project(state)
	if len(selected.PiAgentProfiles) != 1 || selected.PiActiveProfile != "work" {
		t.Fatalf("Pi profile lost: %+v", selected)
	}
	want := model.PiAgentRouting{Model: "openai/gpt-5", Thinking: model.PiThinkingMax}
	if got := selected.PiModelAssignments["sdd-apply"]; got != want {
		t.Fatalf("explicit routing=%+v want=%+v", got, want)
	}
	profile := FromSelection(selected).Selection.Providers[model.AgentPi].Profiles["work"]
	if profile.Orchestrator != nil || profile.PhaseAssignments["sdd-apply"].Model != "claude-sonnet" {
		t.Fatalf("profile=%+v", profile)
	}
}
