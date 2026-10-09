package cli

import (
	"os"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/model"
)

func TestRunSyncWithSelectionPreservesComponentIntent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		components []model.ComponentID
	}{
		{name: "unset"},
		{name: "explicitly empty", components: []model.ComponentID{}},
		{name: "explicit component", components: []model.ComponentID{model.ComponentPersona}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			result, err := RunSyncWithSelection(home, model.Selection{
				Components: tc.components,
				Persona:    model.PersonaCustom,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.NoOp {
				t.Fatal("agent-free sync did not report a no-op")
			}
			if !reflect.DeepEqual(result.Selection.Components, tc.components) {
				t.Fatalf("components = %#v, want caller intent %#v", result.Selection.Components, tc.components)
			}
			entries, err := os.ReadDir(home)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("agent-free sync wrote files or counters: %v", entries)
			}
		})
	}
}
