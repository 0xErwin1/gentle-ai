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
	Stage     string
	Artifacts []Artifact
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

	artifacts := make([]Artifact, 0, len(request.Baseline)+len(contents))
	paths := make(map[string]struct{}, len(request.Baseline)+len(contents))
	for path := range request.Baseline {
		paths[path] = struct{}{}
	}
	for _, artifact := range contents {
		paths[artifact.Path] = struct{}{}
	}
	for path := range paths {
		artifacts = append(artifacts, Artifact{Path: path})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })

	return Snapshot{Stage: request.StageRoot, Artifacts: artifacts}, nil
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
