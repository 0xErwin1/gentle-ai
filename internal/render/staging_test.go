package render

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// Most adapters materialise a tree of files rather than one composed document,
// and the files they write depend on the adapter's own layout. Such an adapter
// writes into the stage and the renderer enumerates what appeared, so the
// manifest covers the whole tree without the engine predicting its shape.
type treeProvider struct{}

func (treeProvider) Render(config.DesiredState, map[string][]byte) ([]ArtifactContent, error) {
	return nil, nil
}

func (treeProvider) Stage(state config.DesiredState, stageRoot string) error {
	for _, skill := range state.Selection.Skills {
		path := filepath.Join(stageRoot, ".config", "tree", "skills", string(skill), "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("# "+string(skill)+"\n"), 0o644); err != nil {
			return err
		}
	}

	return nil
}

func TestStagedTreeIsEnumeratedIntoTheManifest(t *testing.T) {
	stage := t.TempDir()

	snapshot, err := New(treeProvider{}).Render(Request{
		State: config.DesiredState{
			Version:   config.CurrentVersion,
			Selection: config.Selection{Skills: []model.SkillID{"go-testing", "judgment-day"}},
		},
		Destination: t.TempDir(),
		StageRoot:   stage,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	paths := make([]string, 0, len(snapshot.Artifacts))
	for _, artifact := range snapshot.Artifacts {
		paths = append(paths, artifact.Path)
	}
	want := []string{
		".config/tree/skills/go-testing/SKILL.md",
		".config/tree/skills/judgment-day/SKILL.md",
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("artifacts = %v, want %v", paths, want)
	}

	manifest, err := ManifestFor(snapshot)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(manifest.Resources) != 2 {
		t.Errorf("resources = %+v, want one per staged file", manifest.Resources)
	}
}

// Repeated renders of identical state must enumerate identical trees, so a plan
// built from them reports no spurious change.
func TestStagedTreeEnumerationIsDeterministic(t *testing.T) {
	state := config.DesiredState{
		Version:   config.CurrentVersion,
		Selection: config.Selection{Skills: []model.SkillID{"judgment-day", "go-testing"}},
	}

	first, err := New(treeProvider{}).Render(Request{State: state, Destination: t.TempDir(), StageRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := New(treeProvider{}).Render(Request{State: state, Destination: t.TempDir(), StageRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("second render: %v", err)
	}

	firstManifest, err := ManifestFor(first)
	if err != nil {
		t.Fatalf("first manifest: %v", err)
	}
	secondManifest, err := ManifestFor(second)
	if err != nil {
		t.Fatalf("second manifest: %v", err)
	}

	// A resource carries the commands a provisioned agent runs, which makes it
	// a struct with a slice in it rather than a comparable one.
	if !reflect.DeepEqual(firstManifest.Resources, secondManifest.Resources) {
		t.Errorf("manifests differ across identical renders:\n%+v\n%+v", firstManifest.Resources, secondManifest.Resources)
	}
}
