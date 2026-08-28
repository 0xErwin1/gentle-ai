package sddstatus

import (
	"path/filepath"
	"testing"
)

// #3814 / #3636: the artifact store a workspace DECLARES is authoritative.
// Before this change the store was inferred from data presence -- resolveEngramStatus
// returned ok=false whenever it found zero Engram changes, so the caller fell
// through to the OpenSpec branch and reported artifactStore: openspec no matter
// what openspec/config.yaml declared. A declaration that only takes effect when
// the declared store already holds data is not a declaration.

func declareArtifactStore(t *testing.T, root, mode string) {
	t.Helper()
	write(t, filepath.Join(root, "openspec", "config.yaml"), "sdd:\n  artifact_store: "+mode+"\n")
}

// TestDeclaredHybridStoreIsReported pins #3636's exact repro: a workspace that
// declares hybrid must not be reported as openspec.
func TestDeclaredHybridStoreIsReported(t *testing.T) {
	root := t.TempDir()
	seedReadyChange(t, root, "add-auth", "- [x] 1.1 Work\n")
	declareArtifactStore(t, root, "hybrid")
	defer stubEngramExport(t, nil)()

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "add-auth"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if status.ArtifactStore != ArtifactStore("hybrid") {
		t.Errorf("ArtifactStore = %q, want hybrid", status.ArtifactStore)
	}
}

// TestDeclaredEngramStoreSurvivesEmptyEngram pins the fall-through: declaring
// engram while Engram holds nothing must report engram, not silently degrade
// to the OpenSpec artifacts that happen to be on disk.
func TestDeclaredEngramStoreSurvivesEmptyEngram(t *testing.T) {
	root := t.TempDir()
	seedReadyChange(t, root, "add-auth", "- [x] 1.1 Work\n")
	declareArtifactStore(t, root, "engram")
	defer stubEngramExport(t, nil)()

	status, err := Resolve(ResolveOptions{CWD: root})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if status.ArtifactStore != ArtifactStoreEngram {
		t.Errorf("ArtifactStore = %q, want engram", status.ArtifactStore)
	}
}

// TestDeclaredOpenSpecStoreBeatsEngramDataPresence pins the other direction: a
// workspace that declares openspec must stay openspec even when Engram data
// and a .engram directory are both present.
func TestDeclaredOpenSpecStoreBeatsEngramDataPresence(t *testing.T) {
	root := t.TempDir()
	seedReadyChange(t, root, "add-auth", "- [x] 1.1 Work\n")
	declareArtifactStore(t, root, "openspec")
	mkdir(t, filepath.Join(root, ".engram"))
	// ENGRAM_PROJECT must match the fixture observations' project, otherwise
	// inferEngramProject falls back to the temp directory name, no change
	// matches, and this test would pass without ever exercising the conflict.
	t.Setenv("ENGRAM_PROJECT", "gentle-ai")
	defer stubEngramExport(t, engramPlanningRoute("add-auth", "tasks"))()

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "add-auth"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if status.ArtifactStore != ArtifactStoreOpenSpec {
		t.Errorf("ArtifactStore = %q, want openspec", status.ArtifactStore)
	}
}
