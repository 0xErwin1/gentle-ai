package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/render"
)

func TestConfigBaselineUsesManifestDigest(t *testing.T) {
	destination := t.TempDir()
	path := filepath.Join(destination, ".config", "opencode", "opencode.json")
	contents := []byte(`{"theme":"user"}`)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}

	_, live, err := configBaseline(destination)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	key := render.ResourceKey{Path: ".config/opencode/opencode.json", Selector: "file"}
	if got, want := live[key], hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("live digest = %q, want %q", got, want)
	}
}
