package render

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
)

// A second adapter owns its own paths. The renderer must attribute the
// selectors an adapter declares to that adapter's artifact, not to whichever
// path the first implementation happened to use.
type fakeProvider struct{}

const fakeSettingsPath = ".config/fake/settings.json"

func (fakeProvider) Render(config.DesiredState, map[string][]byte) ([]ArtifactContent, error) {
	return []ArtifactContent{{Path: fakeSettingsPath, Contents: []byte(`{"role":{"reviewer":{}}}`)}}, nil
}

func (fakeProvider) Selectors(state config.DesiredState) map[string][]string {
	selectors := make([]string, 0, len(state.Roles))
	for _, role := range state.Roles {
		selectors = append(selectors, "/role/"+string(role.ID))
	}

	return map[string][]string{fakeSettingsPath: selectors}
}

func TestSelectorsFollowTheDeclaringAdapter(t *testing.T) {
	stage := t.TempDir()

	snapshot, err := New(fakeProvider{}).Render(Request{
		State:       config.DesiredState{Version: config.CurrentVersion, Roles: []config.Role{{ID: "reviewer"}}},
		Destination: t.TempDir(),
		StageRoot:   stage,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if got := snapshot.ManagedSelectors[fakeSettingsPath]; len(got) != 1 || got[0] != "/role/reviewer" {
		t.Errorf("selectors for %s = %v, want the declared role", fakeSettingsPath, got)
	}
	if got, ok := snapshot.ManagedSelectors[openCodeSettingsPath]; ok {
		t.Errorf("selectors were attributed to an adapter that rendered nothing: %v", got)
	}
}

// A provider that does not decompose its artifacts owns each one whole, and the
// manifest must not attempt to parse them as another adapter's schema.
func TestUndecomposedArtifactsAreOwnedWhole(t *testing.T) {
	stage := t.TempDir()

	snapshot, err := New(fakeProvider{}).Render(Request{
		State:       config.DesiredState{Version: config.CurrentVersion, Roles: []config.Role{{ID: "reviewer"}}},
		Destination: t.TempDir(),
		StageRoot:   stage,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	manifest, err := ManifestFor(snapshot)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}

	if len(manifest.Resources) != 1 {
		t.Fatalf("resources = %+v, want exactly one", manifest.Resources)
	}
	if got := manifest.Resources[0]; got.Path != fakeSettingsPath || got.Selector != "file" {
		t.Errorf("resource = %+v, want the whole file", got)
	}
}
