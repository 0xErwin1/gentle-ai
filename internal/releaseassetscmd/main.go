// Command releaseassetscmd stages the release artifact payload (the
// contracts/ namespace, the release artifact's own documentation, LICENSE,
// and the generated semantic capability snapshot) and emits the
// self-describing artifact-manifest.json (design D1-D4, Data Flow).
//
// Run it with `go run ./internal/releaseassetscmd`, following the
// internal/gofmtcheck precedent: a thin main() parses flags and calls the
// testable run(cfg, stdout, stderr) function, which returns a process exit
// code instead of calling os.Exit itself.
//
// This command reads no git state and derives no repository identity on
// its own (threat matrix: "Git repository selection" is N/A) — release
// identity (repository, tag, version, commit) is always supplied by the
// caller as explicit flags. It is deliberately unwired from
// .goreleaser.yaml in this unit (A1b); wiring it into the release build is
// A2 scope.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v2/internal/releaseartifact"
)

// semanticSnapshotPath is the fixed staged path of the generated semantic
// capability snapshot (matches design's Worked Example and the golden test
// fixture name).
const semanticSnapshotPath = "capabilities/review-integration-v2.semantic.json"

// releaseArtifactSchemaPath is the staged path of this contract's own
// schema file, bound into manifest.Contract.SchemaPath.
const releaseArtifactSchemaPath = "contracts/release-artifact/v1/schemas/artifact-manifest.schema.json"

// releaseArtifactDocPath is the staged path of this contract's own
// documentation.
const releaseArtifactDocPath = "docs/release-artifact.md"

type runConfig struct {
	Root       string // repository root containing contracts/, docs/, LICENSE
	StagingDir string // destination directory that becomes the assets archive payload root
	Repository string // e.g. Gentleman-Programming/gentle-ai
	Tag        string // e.g. v2.3.0
	Version    string // e.g. 2.3.0
	Commit     string // release commit SHA
}

func main() {
	cfg := runConfig{}
	flag.StringVar(&cfg.Root, "root", ".", "repository root that contains contracts/, docs/, and LICENSE")
	flag.StringVar(&cfg.StagingDir, "staging-dir", filepath.Join("dist", "release-assets"), "destination directory that becomes the assets archive payload root")
	flag.StringVar(&cfg.Repository, "repository", "", "release repository, e.g. Gentleman-Programming/gentle-ai")
	flag.StringVar(&cfg.Tag, "tag", "", "release tag, e.g. v2.3.0")
	flag.StringVar(&cfg.Version, "version", "", "release semantic version, e.g. 2.3.0")
	flag.StringVar(&cfg.Commit, "commit", "", "release commit SHA")
	flag.Parse()
	os.Exit(run(cfg, os.Stdout, os.Stderr))
}

func run(cfg runConfig, stdout, stderr io.Writer) int {
	manifestPath, err := stageAndEmitManifest(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "release assets staged and manifest emitted:", manifestPath)
	return 0
}

func stageAndEmitManifest(cfg runConfig) (string, error) {
	if strings.TrimSpace(cfg.Repository) == "" || strings.TrimSpace(cfg.Tag) == "" ||
		strings.TrimSpace(cfg.Version) == "" || strings.TrimSpace(cfg.Commit) == "" {
		return "", errors.New("releaseassetscmd: --repository, --tag, --version, and --commit are all required")
	}
	if err := os.MkdirAll(cfg.StagingDir, 0o755); err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}

	var entries []releaseartifact.Entry

	contractEntries, contractRefs, err := stageContracts(cfg.Root, cfg.StagingDir)
	if err != nil {
		return "", err
	}
	entries = append(entries, contractEntries...)

	docEntry, err := stageSourceFile(cfg.Root, cfg.StagingDir, releaseArtifactDocPath)
	if err != nil {
		return "", err
	}
	entries = append(entries, docEntry)

	licenseEntry, err := stageSourceFile(cfg.Root, cfg.StagingDir, "LICENSE")
	if err != nil {
		return "", err
	}
	entries = append(entries, licenseEntry)

	snapshot := cli.ReleaseSemanticSnapshot(cli.ReviewIntegrationContractV2)
	snapshotBytes, err := releaseartifact.EncodeCanonical(snapshot)
	if err != nil {
		return "", fmt.Errorf("encode semantic snapshot: %w", err)
	}
	snapshotEntry, err := writeStagedFile(cfg.StagingDir, semanticSnapshotPath, snapshotBytes)
	if err != nil {
		return "", err
	}
	entries = append(entries, snapshotEntry)

	releaseartifact.SortEntries(entries)
	if err := releaseartifact.ValidateEntries(entries); err != nil {
		return "", fmt.Errorf("validate staged entries: %w", err)
	}

	treeDigest, err := releaseartifact.TreeDigest(entries)
	if err != nil {
		return "", fmt.Errorf("compute tree digest: %w", err)
	}

	manifest := releaseartifact.Manifest{
		Schema: releaseartifact.ManifestSchema,
		Contract: releaseartifact.ManifestContract{
			ID: releaseartifact.ContractID, Major: releaseartifact.ContractMajor, Minor: releaseartifact.ContractMinor,
			SchemaID:   releaseartifact.ManifestSchemaID,
			SchemaPath: releaseArtifactSchemaPath,
		},
		Release: releaseartifact.ManifestRelease{
			Repository: cfg.Repository, Tag: cfg.Tag, Version: cfg.Version, Commit: cfg.Commit,
		},
		Layout: releaseartifact.ManifestLayout{Version: 1},
		Archive: releaseartifact.ManifestArchive{
			Asset:        "gentle-ai_" + cfg.Version + "_assets.tar.gz",
			DigestSource: "signed-checksums.txt",
		},
		References: releaseartifact.ManifestReferences{
			SemanticSnapshots: []releaseartifact.ManifestSemanticSnapshotRef{
				{Contract: cli.ReviewIntegrationContractV2, Path: semanticSnapshotPath, Schema: releaseartifact.SnapshotSchema},
			},
			Contracts: contractRefs,
		},
		Tree: releaseartifact.Tree{
			Algorithm: "sha256", Canonicalization: releaseartifact.TreeCanonicalization,
			ManifestIncluded: false, Digest: treeDigest,
		},
		Compatibility: releaseartifact.ManifestCompatibility{
			MinimumContractMajor: releaseartifact.ContractMajor, MaximumContractMajor: releaseartifact.ContractMajor,
			AdditiveMinorPolicy: "optional-fields-only", UnknownMandatory: "reject", UnknownOptional: "ignore",
		},
		Entries: entries,
	}
	if err := manifest.Validate(); err != nil {
		return "", fmt.Errorf("validate generated manifest: %w", err)
	}

	manifestBytes, err := releaseartifact.EncodeCanonical(manifest)
	if err != nil {
		return "", fmt.Errorf("encode manifest: %w", err)
	}
	manifestPath := filepath.Join(cfg.StagingDir, releaseartifact.ManifestFileName)
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		return "", fmt.Errorf("write manifest: %w", err)
	}
	return manifestPath, nil
}

// stageContracts copies every regular file under root/contracts into
// stagingDir/contracts, preserving relative paths, and derives one
// ManifestContractRef per top-level contracts/<name>/<version> directory:
// id "gentle-ai.<name>/<version>", root "contracts/<name>/<version>" —
// exactly the convention the Worked Example's references.contracts uses.
func stageContracts(root, stagingDir string) ([]releaseartifact.Entry, []releaseartifact.ManifestContractRef, error) {
	contractsRoot := filepath.Join(root, "contracts")
	var entries []releaseartifact.Entry
	refsByRoot := make(map[string]releaseartifact.ManifestContractRef)

	err := filepath.WalkDir(contractsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)

		entry, err := stageSourceFile(root, stagingDir, relSlash)
		if err != nil {
			return err
		}
		entries = append(entries, entry)

		parts := strings.Split(relSlash, "/")
		if len(parts) >= 3 && parts[0] == "contracts" {
			name, version := parts[1], parts[2]
			contractRoot := strings.Join(parts[:3], "/")
			refsByRoot[contractRoot] = releaseartifact.ManifestContractRef{
				ID: "gentle-ai." + name + "/" + version, Root: contractRoot,
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("stage contracts: %w", err)
	}

	refs := make([]releaseartifact.ManifestContractRef, 0, len(refsByRoot))
	for _, ref := range refsByRoot {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Root < refs[j].Root })
	return entries, refs, nil
}

// stageSourceFile reads root/relSlash and stages it into stagingDir at the
// same relative path, returning its canonical Entry.
func stageSourceFile(root, stagingDir, relSlash string) (releaseartifact.Entry, error) {
	src := filepath.Join(root, filepath.FromSlash(relSlash))
	data, err := os.ReadFile(src)
	if err != nil {
		return releaseartifact.Entry{}, fmt.Errorf("read %s: %w", relSlash, err)
	}
	return writeStagedFile(stagingDir, relSlash, data)
}

// writeStagedFile writes data into stagingDir at relSlash and returns its
// canonical Entry (design D3: type "file", mode "0644", sha256 digest).
func writeStagedFile(stagingDir, relSlash string, data []byte) (releaseartifact.Entry, error) {
	dst := filepath.Join(stagingDir, filepath.FromSlash(relSlash))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return releaseartifact.Entry{}, fmt.Errorf("stage %s: %w", relSlash, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return releaseartifact.Entry{}, fmt.Errorf("stage %s: %w", relSlash, err)
	}
	sum := sha256.Sum256(data)
	return releaseartifact.Entry{
		Path: relSlash, Type: releaseartifact.EntryTypeFile, Mode: releaseartifact.EntryMode,
		Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}
