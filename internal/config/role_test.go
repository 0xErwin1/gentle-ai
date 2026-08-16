package config

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// A role is a settings entry for an adapter that composes agents inside one
// file, and a file for an adapter that keeps them in a directory. A file needs
// content, so a role has to be able to declare it; otherwise rendering one would
// mean inventing a description, a model and a prompt the document never gave.
func TestDecodeRoleBody(t *testing.T) {
	tests := []struct {
		name      string
		document  string
		wantCodes []string
		assert    func(*testing.T, DesiredState)
	}{
		{
			name:     "accepts a role that declares what a sub-agent needs",
			document: `{"version":"v1","roles":[{"id":"reviewer","description":"Reviews a diff","prompt":"You review changes.","tools":["Read","Grep"],"model":{"provider":"anthropic","model":"claude-sonnet","effort":"high"}}]}`,
			assert: func(t *testing.T, state DesiredState) {
				role := state.Roles[0]
				if role.Description != "Reviews a diff" {
					t.Errorf("Description = %q", role.Description)
				}
				if role.Prompt != "You review changes." {
					t.Errorf("Prompt = %q", role.Prompt)
				}
				if !slices.Equal(role.Tools, []string{"Read", "Grep"}) {
					t.Errorf("Tools = %v", role.Tools)
				}
				if role.Model == nil {
					t.Fatal("Model was dropped")
				}
				if want := (ModelAssignment{Provider: "anthropic", Model: "claude-sonnet", Effort: "high"}); *role.Model != want {
					t.Errorf("Model = %+v, want %+v", *role.Model, want)
				}
			},
		},
		{
			name:     "a role without a body stays valid for adapters that do not need one",
			document: `{"version":"v1","roles":[{"id":"reviewer"}]}`,
			assert: func(t *testing.T, state DesiredState) {
				if state.Roles[0].Model != nil {
					t.Errorf("Model = %+v, want nil", state.Roles[0].Model)
				}
			},
		},
		{
			name:      "rejects a role model missing its provider",
			document:  `{"version":"v1","roles":[{"id":"reviewer","model":{"model":"claude-sonnet"}}]}`,
			wantCodes: []string{"config.role.model.incomplete"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, diagnostics := Decode([]byte(test.document))

			codes := make([]string, 0, len(diagnostics))
			for _, diagnostic := range diagnostics {
				codes = append(codes, diagnostic.Code)
			}
			if !slices.Equal(codes, test.wantCodes) {
				t.Fatalf("diagnostics = %v, want %v", codes, test.wantCodes)
			}

			if test.assert != nil {
				test.assert(t, state)
			}
		})
	}
}

// An undeclared model must stay absent from the encoded document rather than
// publishing an object of empty strings, the same guarantee every other
// struct-valued field in this contract carries.
func TestUndeclaredRoleModelIsOmitted(t *testing.T) {
	encoded, err := json.Marshal(Document{Version: CurrentVersion, Roles: []Role{{ID: "reviewer"}}})
	if err != nil {
		t.Fatalf("encode document: %v", err)
	}

	if strings.Contains(string(encoded), "model") {
		t.Errorf("encoded document publishes an undeclared role model: %s", encoded)
	}
}
