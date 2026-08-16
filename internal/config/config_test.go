package config

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestDecode(t *testing.T) {
	tests := []struct {
		name        string
		document    string
		wantVersion string
		wantCodes   []string
		want        model.Selection
	}{
		{
			name:        "applies defaults to current document",
			document:    `{"version":"v1","selection":{"agents":["opencode"]}}`,
			wantVersion: "v1",
			want: model.Selection{
				Agents:     []model.AgentID{model.AgentOpenCode},
				Persona:    model.PersonaGentleman,
				Preset:     model.PresetFullGentleman,
				Components: model.ComponentsForPreset(model.PresetFullGentleman, model.PersonaGentleman),
			},
		},
		{
			name:        "migrates legacy document",
			document:    `{"version":"v0","selection":{"persona":"neutral","components":["engram"]}}`,
			wantVersion: "v1",
			want: model.Selection{
				Persona:    model.PersonaNeutral,
				Preset:     model.PresetFullGentleman,
				Components: []model.ComponentID{model.ComponentEngram},
			},
		},
		{
			name:      "reports malformed and unknown input with stable diagnostics",
			document:  `{"version":"v9","roles":[{"id":"writer","references":["missing"]}]}`,
			wantCodes: []string{"config.version.unsupported", "config.role.reference.unresolved"},
		},
		{
			name:      "reports malformed json",
			document:  `{`,
			wantCodes: []string{"config.document.malformed"},
		},
		{
			name:      "rejects unknown fields",
			document:  `{"version":"v1","selection":{"agents":["opencode"],"unknown":true}}`,
			wantCodes: []string{"config.document.unknown-field"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, diagnostics := Decode([]byte(tt.document))

			if state.Version != tt.wantVersion {
				t.Fatalf("version = %q, want %q", state.Version, tt.wantVersion)
			}
			if got := Project(state); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("selection = %#v, want %#v", got, tt.want)
			}
			if got := diagnosticCodes(diagnostics); !slices.Equal(got, tt.wantCodes) {
				t.Fatalf("diagnostics = %v, want %v", got, tt.wantCodes)
			}
		})
	}
}

func TestAdmitRejectsInvalidDocumentBeforeAction(t *testing.T) {
	invoked := false
	diagnostics := Admit([]byte(`{"version":"v9"}`), func(DesiredState) {
		invoked = true
	})

	if invoked {
		t.Fatal("action invoked for invalid document")
	}
	if got := diagnosticCodes(diagnostics); !slices.Equal(got, []string{"config.version.unsupported"}) {
		t.Fatalf("diagnostics = %v", got)
	}
}

func TestAdmitInvokesActionForValidDocument(t *testing.T) {
	var admitted DesiredState
	diagnostics := Admit([]byte(`{"version":"v1"}`), func(state DesiredState) {
		admitted = state
	})

	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	if admitted.Version != CurrentVersion {
		t.Fatalf("admitted version = %q, want %q", admitted.Version, CurrentVersion)
	}
}

func TestNormalizeRejectsInvalidRolesAndCanonicalizesSelections(t *testing.T) {
	state, diagnostics := Normalize(Document{
		Version:   CurrentVersion,
		Selection: Selection{Agents: []model.AgentID{model.AgentOpenCode, model.AgentOpenCode}},
		Roles: []Role{
			{ID: "writer", RenderedName: "writer"},
			{ID: "writer", RenderedName: "writer-2"},
			{ID: "reviewer", References: []RoleRef{"missing"}},
		},
		Extensions: map[string]json.RawMessage{"opencode": json.RawMessage(`{"model":"x"}`)},
	})

	if state.Version != CurrentVersion {
		t.Fatalf("version = %q, want %q", state.Version, CurrentVersion)
	}
	if got := Project(state).Agents; !reflect.DeepEqual(got, []model.AgentID{model.AgentOpenCode}) {
		t.Fatalf("agents = %v, want one opencode", got)
	}
	if got := diagnosticCodes(diagnostics); !slices.Equal(got, []string{"config.role.duplicate", "config.role.reference.unresolved"}) {
		t.Fatalf("diagnostics = %v", got)
	}
	if string(state.Extensions["opencode"]) != `{"model":"x"}` {
		t.Fatalf("extension = %s", state.Extensions["opencode"])
	}
}

func diagnosticCodes(diagnostics []Diagnostic) []string {
	codes := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}
