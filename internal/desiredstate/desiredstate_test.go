package desiredstate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/render"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
)

func TestWriteDesiredAndManifestRestoresBothStoresOnFailure(t *testing.T) {
	home := t.TempDir()
	identity, destination, stage := "opencode", t.TempDir(), t.TempDir()
	if err := WriteDesiredAndManifest(home, identity, config.DesiredState{Version: "old"}, render.Manifest{}); err != nil {
		t.Fatal(err)
	}
	if desired, err := ReadDesired(home); err != nil || desired.Version != "old" {
		t.Fatalf("ReadDesired() = %#v, %v", desired, err)
	}
	if manifest, err := ReadManifest(home, identity); err != nil || len(manifest.Resources) != 0 {
		t.Fatalf("ReadManifest() = %#v, %v", manifest, err)
	}
	if _, err := os.Stat(state.Path(home)); !os.IsNotExist(err) {
		t.Fatalf("InstallState path changed by desired stores: %v", err)
	}
	desiredBefore, _ := os.ReadFile(DesiredPath(home))
	manifestBefore, _ := os.ReadFile(ManifestPath(home, identity))
	original := writeAtomic
	writeAtomic = func(path string, data []byte) error {
		if path == ManifestPath(home, identity) {
			return errors.New("injected persistence failure")
		}
		return original(path, data)
	}
	t.Cleanup(func() { writeAtomic = original })
	if err := os.WriteFile(filepath.Join(stage, "config.json"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(destination, "config.json")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := render.Apply(render.ApplyRequest{Snapshot: render.Snapshot{Stage: stage}, Destination: destination, Plan: render.ReconcilePlan{Operations: []render.Operation{{Kind: render.Update, Path: "config.json"}}}, Persist: func() error {
		return WriteDesiredAndManifest(home, identity, config.DesiredState{Version: "new"}, render.Manifest{Resources: []render.Resource{{Path: "new"}}})
	}})
	if err == nil {
		t.Fatal("Apply() error = nil, want injected persistence failure")
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Fatalf("destination was not restored: %s", got)
	}
	if got, _ := os.ReadFile(DesiredPath(home)); string(got) != string(desiredBefore) {
		t.Fatalf("desired state was not restored: %s", got)
	}
	if got, _ := os.ReadFile(ManifestPath(home, identity)); string(got) != string(manifestBefore) {
		t.Fatalf("manifest was not restored: %s", got)
	}
}
