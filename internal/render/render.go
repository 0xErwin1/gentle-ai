// Package render stages provider configuration without mutating live destinations.
package render

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
)

type Artifact struct {
	Path string
}

type Snapshot struct {
	Stage            string
	Artifacts        []Artifact
	ManagedSelectors map[string][]string

	// decompose and merge belong to the adapter that rendered the snapshot, so
	// neither manifest construction nor reconciliation has to recognise any
	// adapter's file format.
	decompose ResourceDecomposer
	merge     ResourceMerger
}

type Request struct {
	State       config.DesiredState
	Destination string
	StageRoot   string
	Baseline    map[string][]byte
}

type Provider interface {
	Render(config.DesiredState, map[string][]byte) ([]ArtifactContent, error)
}

// StagingProvider is implemented by an adapter that materialises a tree of
// files rather than a single composed document. It writes under the staging
// root and the renderer enumerates what appeared, so the engine never has to
// predict any adapter's directory layout.
type StagingProvider interface {
	Stage(state config.DesiredState, stageRoot string) error
}

// SelectorProvider is implemented by an adapter whose artifacts are composed
// files, where several independently owned resources share one path. The
// adapter names its own selectors because only it knows the shape of what it
// wrote; the renderer stays neutral about every adapter's schema.
type SelectorProvider interface {
	Selectors(config.DesiredState) map[string][]string
}

// ResourceMerger is implemented by an adapter that owns resources inside a
// composed file. Merging a selector-level resource into a live file requires
// knowing that file's shape, which only the adapter that wrote it knows.
type ResourceMerger interface {
	Merge(operation Operation, stagedPath string, target []byte) ([]byte, error)
}

// ResourceDecomposer is implemented by an adapter that splits a composed
// artifact into the resources it owns. An artifact whose adapter does not
// decompose it is owned whole.
type ResourceDecomposer interface {
	Resources(path string, contents []byte, selectors []string) ([]Resource, error)
}

type ArtifactContent struct {
	Path     string
	Contents []byte
}

type Renderer struct {
	provider Provider
}

func New(provider Provider) Renderer {
	return Renderer{provider: provider}
}

func (r Renderer) Render(request Request) (Snapshot, error) {
	if r.provider == nil {
		return Snapshot{}, fmt.Errorf("render provider is required")
	}
	if err := isolatedRoots(request.Destination, request.StageRoot); err != nil {
		return Snapshot{}, err
	}
	if err := writeBaseline(request.StageRoot, request.Baseline); err != nil {
		return Snapshot{}, err
	}

	contents, err := r.provider.Render(request.State, copyBaseline(request.Baseline))
	if err != nil {
		return Snapshot{}, err
	}
	for _, artifact := range contents {
		if err := writeArtifact(request.StageRoot, artifact); err != nil {
			return Snapshot{}, err
		}
	}

	staging, stages := r.provider.(StagingProvider)
	if stages {
		if err := staging.Stage(request.State, request.StageRoot); err != nil {
			return Snapshot{}, fmt.Errorf("stage adapter tree: %w", err)
		}
		if err := survivedStaging(r.provider, request, contents); err != nil {
			return Snapshot{}, err
		}
	}

	paths := make(map[string]struct{}, len(request.Baseline)+len(contents))
	for path := range request.Baseline {
		paths[path] = struct{}{}
	}
	for _, artifact := range contents {
		paths[artifact.Path] = struct{}{}
	}
	// Enumeration is confined to adapters that stage a tree. Walking the root
	// unconditionally would claim anything already sitting in a reused staging
	// directory as managed, which reconciliation would then apply.
	if stages {
		staged, err := enumerateStage(request.StageRoot)
		if err != nil {
			return Snapshot{}, err
		}
		for _, path := range staged {
			paths[path] = struct{}{}
		}
	}

	artifacts := make([]Artifact, 0, len(paths))
	for path := range paths {
		artifacts = append(artifacts, Artifact{Path: path})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })

	managed := map[string][]string{}
	if selector, ok := r.provider.(SelectorProvider); ok {
		for path, selectors := range selector.Selectors(request.State) {
			sorted := append([]string(nil), selectors...)
			sort.Strings(sorted)
			managed[path] = sorted
		}
	}

	decomposer, _ := r.provider.(ResourceDecomposer)
	merger, _ := r.provider.(ResourceMerger)

	return Snapshot{Stage: request.StageRoot, Artifacts: artifacts, ManagedSelectors: managed, decompose: decomposer, merge: merger}, nil
}

// survivedStaging checks that every resource the adapter rendered still holds
// what the adapter rendered after the components staged their own files.
//
// Both write the same composed file, and a component writes last. Where the
// document names a role a component also generates, the component's version
// wins and the declared one disappears without a word — and only for adapters
// that compose, so the same document produces two different agents under one
// name. Refusing names the collision instead of picking a winner, because
// picking either one discards something the document asked for.
func survivedStaging(provider Provider, request Request, contents []ArtifactContent) error {
	decomposer, decomposes := provider.(ResourceDecomposer)
	selector, selects := provider.(SelectorProvider)
	if !decomposes || !selects {
		return nil
	}

	owned := selector.Selectors(request.State)

	for _, artifact := range contents {
		selectors, claimed := owned[artifact.Path]
		if !claimed || len(selectors) == 0 {
			continue
		}

		rendered, err := decomposer.Resources(artifact.Path, artifact.Contents, selectors)
		if err != nil {
			return fmt.Errorf("decompose rendered %q: %w", artifact.Path, err)
		}

		path, err := stagedPath(request.StageRoot, artifact.Path)
		if err != nil {
			return err
		}
		staged, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read staged %q: %w", artifact.Path, err)
		}
		after, err := decomposer.Resources(artifact.Path, staged, selectors)
		if err != nil {
			return fmt.Errorf("decompose staged %q: %w", artifact.Path, err)
		}

		digests := make(map[string]string, len(after))
		for _, resource := range after {
			digests[resource.Selector] = resource.Digest
		}
		for _, resource := range rendered {
			if digests[resource.Selector] != resource.Digest {
				return fmt.Errorf(
					"declared %s in %q is also generated by a selected component; rename the role or drop the component that generates it",
					resource.Selector, artifact.Path,
				)
			}
		}
	}

	return nil
}

func isolatedRoots(destination, stage string) error {
	if destination == "" || stage == "" {
		return fmt.Errorf("destination and stage root are required")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
	stage, err = filepath.Abs(stage)
	if err != nil {
		return fmt.Errorf("resolve stage root: %w", err)
	}
	if within(destination, stage) || within(stage, destination) {
		return fmt.Errorf("stage root must be isolated from destination")
	}
	return nil
}

func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func writeBaseline(stage string, baseline map[string][]byte) error {
	for path, contents := range baseline {
		if err := writeArtifact(stage, ArtifactContent{Path: path, Contents: contents}); err != nil {
			return fmt.Errorf("stage baseline: %w", err)
		}
	}
	return nil
}

func writeArtifact(stage string, artifact ArtifactContent) error {
	path, err := stagedPath(stage, artifact.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create stage directory: %w", err)
	}
	if err := os.WriteFile(path, artifact.Contents, 0o644); err != nil {
		return fmt.Errorf("write staged artifact: %w", err)
	}
	return nil
}

func stagedPath(stage, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("artifact path %q escapes stage", relative)
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path %q escapes stage", relative)
	}
	return filepath.Join(stage, clean), nil
}

func copyBaseline(baseline map[string][]byte) map[string][]byte {
	copy := make(map[string][]byte, len(baseline))
	for path, contents := range baseline {
		copy[path] = append([]byte(nil), contents...)
	}
	return copy
}

// enumerateStage lists every file the stage holds, in slash form relative to the
// staging root. Enumerating what is there rather than trusting what a provider
// said it wrote keeps the manifest honest about the tree that will be applied.
func enumerateStage(stageRoot string) ([]string, error) {
	paths := make([]string, 0)
	err := filepath.Walk(stageRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(stageRoot, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))

		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("enumerate stage: %w", err)
	}
	sort.Strings(paths)

	return paths, nil
}
