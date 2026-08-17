// Package desiredstate persists the declarative desired state and the render
// manifest that records which destination bytes gentle-ai owns.
//
// It lives outside internal/state because it stores a render.Manifest, and
// internal/render depends on internal/agents, which depends on internal/state.
package desiredstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/gentleman-programming/gentle-ai/v2/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/render"
)

// stateDir mirrors the home-relative directory name internal/state keeps
// private. It is duplicated rather than exported so internal/state keeps the
// surface it has.
const stateDir = ".gentle-ai"

var writeAtomic = func(path string, data []byte) error {
	_, err := filemerge.WriteFileAtomic(path, data, 0o644)
	return err
}

func DesiredPath(home string) string { return filepath.Join(home, stateDir, "desired-state.json") }

func ManifestPath(home, destination string) string {
	sum := sha256.Sum256([]byte(destination))
	return filepath.Join(home, stateDir, "manifests", hex.EncodeToString(sum[:])+".json")
}

func ReadDesired(home string) (config.DesiredState, error) {
	var desired config.DesiredState
	err := readJSON(DesiredPath(home), &desired)
	return desired, err
}

func ReadManifest(home, destination string) (render.Manifest, error) {
	var manifest render.Manifest
	err := readJSON(ManifestPath(home, destination), &manifest)
	return manifest, err
}

// WriteDesired records the desired state alone, for a frontend that renders and
// owns the client files itself. No manifest is written: the manifest records
// which bytes gentle-ai owns, and such a frontend owns them instead.
func WriteDesired(home string, desired config.DesiredState) error {
	data, err := canonicalJSON(desired)
	if err != nil {
		return err
	}

	return writeAtomic(DesiredPath(home), data)
}

// WriteDesiredAndManifest atomically writes each store and restores both on a failed pair.
func WriteDesiredAndManifest(home, destination string, desired config.DesiredState, manifest render.Manifest) error {
	desiredPath, manifestPath := DesiredPath(home), ManifestPath(home, destination)
	desiredData, err := canonicalJSON(desired)
	if err != nil {
		return err
	}
	manifestData, err := canonicalJSON(manifest)
	if err != nil {
		return err
	}
	desiredBefore, desiredExists, err := stored(desiredPath)
	if err != nil {
		return err
	}
	manifestBefore, manifestExists, err := stored(manifestPath)
	if err != nil {
		return err
	}
	if err := writeAtomic(desiredPath, desiredData); err != nil {
		return err
	}
	if err := writeAtomic(manifestPath, manifestData); err != nil {
		_ = restore(desiredPath, desiredBefore, desiredExists)
		_ = restore(manifestPath, manifestBefore, manifestExists)
		return err
	}
	return nil
}

func canonicalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return append(data, '\n'), err
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func stored(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return data, err == nil, err
}

func restore(path string, data []byte, exists bool) error {
	if exists {
		return writeAtomic(path, data)
	}
	return os.Remove(path)
}
