package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/render"
)

func TestConfigManifestBaselineUsesResourceDigests(t *testing.T) {
	destination := t.TempDir()
	path := filepath.Join(destination, ".config", "opencode", "opencode.json")
	contents := []byte(`{"theme":"user","agent":{"writer":{"references":["reviewer"]}}}`)
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
	var settings map[string]any
	if err := json.Unmarshal(contents, &settings); err != nil {
		t.Fatal(err)
	}
	writer, err := json.Marshal(settings["agent"].(map[string]any)["writer"])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(writer)
	key := render.ResourceKey{Path: ".config/opencode/opencode.json", Selector: "/agent/writer"}
	if got, want := live[key], hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("live digest = %q, want %q", got, want)
	}
	if _, ok := live[render.ResourceKey{Path: key.Path, Selector: "file"}]; ok {
		t.Fatal("baseline retained whole-file ownership")
	}
}
