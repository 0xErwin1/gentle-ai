package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/releaseartifact"
)

// writeFixtureFile creates a file with content at root/relSlash, creating
// parent directories as needed.
func writeFixtureFile(t *testing.T, root, relSlash, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relSlash))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildFixtureRoot creates a minimal repository-shaped tree: contracts under
// two namespaces/versions, the release artifact's own doc, and LICENSE.
func buildFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, "contracts/review-integration/v1/schemas/capabilities.schema.json", `{"$id":"v1"}`+"\n")
	writeFixtureFile(t, root, "contracts/review-integration/v2/schemas/capabilities.schema.json", `{"$id":"v2"}`+"\n")
	writeFixtureFile(t, root, "contracts/release-artifact/v1/schemas/artifact-manifest.schema.json", `{"$id":"artifact"}`+"\n")
	writeFixtureFile(t, root, "contracts/release-artifact/v1/fixtures/artifact-manifest.fixture.json", `{"fixture":true}`+"\n")
	writeFixtureFile(t, root, "docs/release-artifact.md", "# Release Artifact\n")
	writeFixtureFile(t, root, "LICENSE", "MIT License fixture\n")
	return root
}

func testConfig(root, stagingDir string) runConfig {
	return runConfig{
		Root: root, StagingDir: stagingDir,
		Repository: "Gentleman-Programming/gentle-ai",
		Tag:        "v9.9.9", Version: "9.9.9",
		Commit: strings.Repeat("a", 40),
	}
}

// recomputeTreeDigestFromStagedFiles independently walks stagingDir (never
// reusing this package's own staging code) and recomputes the tree digest
// exactly as a consumer verifying the archive after extraction would.
func recomputeTreeDigestFromStagedFiles(t *testing.T, stagingDir string) (string, []releaseartifact.Entry) {
	t.Helper()
	var entries []releaseartifact.Entry
	err := filepath.Walk(stagingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stagingDir, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if relSlash == releaseartifact.ManifestFileName {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		entries = append(entries, releaseartifact.Entry{
			Path: relSlash, Type: releaseartifact.EntryTypeFile, Mode: releaseartifact.EntryMode,
			Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:]),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk staging dir: %v", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	digest, err := releaseartifact.TreeDigest(entries)
	if err != nil {
		t.Fatalf("recompute tree digest: %v", err)
	}
	return digest, entries
}

func TestRunStagesPayloadAndEmitsValidatingManifest(t *testing.T) {
	root := buildFixtureRoot(t)
	stagingDir := filepath.Join(t.TempDir(), "release-assets")
	cfg := testConfig(root, stagingDir)

	var stdout, stderr bytes.Buffer
	if code := run(cfg, &stdout, &stderr); code != 0 {
		t.Fatalf("run() = %d, want 0; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	manifestPath := filepath.Join(stagingDir, releaseartifact.ManifestFileName)
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read emitted manifest: %v", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	var manifest releaseartifact.Manifest
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("decode emitted manifest: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("emitted manifest failed Manifest.Validate(): %v", err)
	}

	if manifest.Release.Repository != cfg.Repository || manifest.Release.Tag != cfg.Tag ||
		manifest.Release.Version != cfg.Version || manifest.Release.Commit != cfg.Commit {
		t.Fatalf("manifest release identity = %+v, want it to bind cfg %+v", manifest.Release, cfg)
	}
	wantAsset := "gentle-ai_" + cfg.Version + "_assets.tar.gz"
	if manifest.Archive.Asset != wantAsset {
		t.Fatalf("manifest.Archive.Asset = %q, want %q", manifest.Archive.Asset, wantAsset)
	}
	if manifest.Layout.Version != 1 {
		t.Fatalf("manifest.Layout.Version = %d, want 1", manifest.Layout.Version)
	}

	// Recompute the tree digest independently from the actual staged files
	// on disk and compare against what the manifest declares — this is the
	// integration assertion the design's Testing Strategy requires.
	recomputedDigest, recomputedEntries := recomputeTreeDigestFromStagedFiles(t, stagingDir)
	if recomputedDigest != manifest.Tree.Digest {
		t.Fatalf("recomputed tree digest %q does not match manifest.Tree.Digest %q", recomputedDigest, manifest.Tree.Digest)
	}
	if len(recomputedEntries) != len(manifest.Entries) {
		t.Fatalf("recomputed %d staged file entries, manifest declares %d", len(recomputedEntries), len(manifest.Entries))
	}

	// Every staged contract file and the release artifact's own doc must be
	// present as a manifest entry.
	entryPaths := make(map[string]bool, len(manifest.Entries))
	for _, e := range manifest.Entries {
		entryPaths[e.Path] = true
	}
	for _, want := range []string{
		"contracts/review-integration/v1/schemas/capabilities.schema.json",
		"contracts/review-integration/v2/schemas/capabilities.schema.json",
		"contracts/release-artifact/v1/schemas/artifact-manifest.schema.json",
		"contracts/release-artifact/v1/fixtures/artifact-manifest.fixture.json",
		"docs/release-artifact.md",
		"LICENSE",
		"capabilities/review-integration-v2.semantic.json",
	} {
		if !entryPaths[want] {
			t.Errorf("manifest entries missing expected staged path %q", want)
		}
	}

	// references.contracts must name both staged contract namespaces/versions.
	wantContractRefs := map[string]string{
		"gentle-ai.review-integration/v1": "contracts/review-integration/v1",
		"gentle-ai.review-integration/v2": "contracts/review-integration/v2",
		"gentle-ai.release-artifact/v1":   "contracts/release-artifact/v1",
	}
	gotContractRefs := make(map[string]string, len(manifest.References.Contracts))
	for _, ref := range manifest.References.Contracts {
		gotContractRefs[ref.ID] = ref.Root
	}
	for id, root := range wantContractRefs {
		if gotContractRefs[id] != root {
			t.Errorf("references.contracts[%q] = %q, want %q", id, gotContractRefs[id], root)
		}
	}

	// references.semantic_snapshots must name the generated snapshot entry.
	if len(manifest.References.SemanticSnapshots) != 1 {
		t.Fatalf("references.semantic_snapshots has %d entries, want 1", len(manifest.References.SemanticSnapshots))
	}
	snapshotRef := manifest.References.SemanticSnapshots[0]
	if snapshotRef.Path != "capabilities/review-integration-v2.semantic.json" || snapshotRef.Schema != releaseartifact.SnapshotSchema {
		t.Fatalf("semantic snapshot reference = %+v, want path capabilities/review-integration-v2.semantic.json and schema %q", snapshotRef, releaseartifact.SnapshotSchema)
	}
}

func TestRunRejectsMissingReleaseIdentity(t *testing.T) {
	root := buildFixtureRoot(t)
	stagingDir := filepath.Join(t.TempDir(), "release-assets")
	cfg := testConfig(root, stagingDir)
	cfg.Version = ""

	var stdout, stderr bytes.Buffer
	if code := run(cfg, &stdout, &stderr); code == 0 {
		t.Fatal("run() = 0 with a missing --version, want a nonzero exit code")
	}
	if !strings.Contains(stderr.String(), "version") {
		t.Fatalf("stderr = %q, want it to name the missing --version flag", stderr.String())
	}
}
