package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
)

// The staging directory stands in for the destination, so nothing that reaches
// the destination may name it. A staged absolute path that survives is both a
// render whose bytes depend on where it was staged and a live configuration
// pointing inside a directory the run already discarded.
func TestRenderedContentNamesTheDestinationRatherThanTheStage(t *testing.T) {
	home, destination := t.TempDir(), t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]}}`)

	first, second := t.TempDir(), t.TempDir()
	var output bytes.Buffer
	for _, stage := range []string{first, second} {
		output.Reset()
		if err := RunConfig([]string{"render", "--config", configPath, "--home", home, "--destination", destination, "--stage", stage}, &output); err != nil {
			t.Fatalf("RunConfig(render) error = %v", err)
		}
		assertNoStagedPaths(t, stage, stage)
	}

	assertSameTree(t, first, second)
}

func assertNoStagedPaths(t *testing.T, root, stage string) {
	t.Helper()

	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(contents, []byte(stage)) {
			t.Errorf("%s names the staging directory", strings.TrimPrefix(path, root))
		}

		return nil
	}); err != nil {
		t.Fatalf("walk %q: %v", root, err)
	}
}

func assertSameTree(t *testing.T, first, second string) {
	t.Helper()

	digests := func(root string) map[string]string {
		found := map[string]string{}
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || !entry.Type().IsRegular() {
				return err
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found[strings.TrimPrefix(path, root)] = digest(contents)

			return nil
		}); err != nil {
			t.Fatalf("walk %q: %v", root, err)
		}

		return found
	}

	if !reflect.DeepEqual(digests(first), digests(second)) {
		t.Error("the same document rendered different bytes into two staging directories")
	}
}

// Diagnostics on stdout are the answer for a consumer that parses them. For a
// script, a shell pipeline or a build that only checks the exit status, a
// rejected document reported as success is indistinguishable from a valid one.
func TestARejectedDocumentFailsAndStillReportsWhy(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "desired.json")
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["not-an-agent"]}}`)

	var output bytes.Buffer
	err := RunConfig([]string{"validate", "--config", configPath}, &output)

	if err == nil {
		t.Error("RunConfig(validate) on a rejected document returned no error")
	}
	if !strings.Contains(output.String(), "config.agent.unsupported") {
		t.Errorf("output = %s, want the diagnostics reported as well", output.String())
	}
}

// A valid document must not be failed by the same path.
func TestAnAcceptedDocumentSucceeds(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "desired.json")
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]}}`)

	var output bytes.Buffer
	if err := RunConfig([]string{"validate", "--config", configPath}, &output); err != nil {
		t.Fatalf("RunConfig(validate) error = %v", err)
	}
}

// A frontend that renders the tree itself leaves gentle-ai unable to see its
// own installation: doctor reads state.json, which only install and sync ever
// wrote, so a plainly present installation reads as absent and doctor
// recommends installing it again.
func TestAdoptRecordsTheInstallationWithoutClaimingItsFiles(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode","claude-code"],"components":["skills"]}}`)

	var output bytes.Buffer
	if err := RunConfig([]string{"adopt", "--config", configPath, "--home", home}, &output); err != nil {
		t.Fatalf("RunConfig(adopt) error = %v", err)
	}

	recorded, err := state.Read(home)
	if err != nil {
		t.Fatalf("read recorded state: %v", err)
	}
	if got := recorded.InstalledAgents; !reflect.DeepEqual(got, []string{"opencode", "claude-code"}) {
		t.Errorf("InstalledAgents = %v, want the declared clients", got)
	}
	if recorded.InstalledBinaryVersion != AppVersion {
		t.Errorf("InstalledBinaryVersion = %q, want %q", recorded.InstalledBinaryVersion, AppVersion)
	}

	// The manifest records which bytes gentle-ai owns, and here it owns none:
	// writing one would let reconciliation remove files that belong to whoever
	// rendered them.
	if _, err := os.Stat(filepath.Join(home, ".gentle-ai", "manifest")); !os.IsNotExist(err) {
		t.Errorf("adopt wrote an ownership manifest for files it does not own")
	}
}

// Adopting twice must leave the same record, so a frontend can run it on every
// activation without the state drifting.
func TestAdoptIsRepeatable(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]}}`)

	var output bytes.Buffer
	if err := RunConfig([]string{"adopt", "--config", configPath, "--home", home}, &output); err != nil {
		t.Fatalf("first adopt: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(home, ".gentle-ai", "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}

	if err := RunConfig([]string{"adopt", "--config", configPath, "--home", home}, &output); err != nil {
		t.Fatalf("second adopt: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(home, ".gentle-ai", "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("adopting twice changed the record:\n%s\n%s", first, second)
	}
}

// A document adopt cannot deliver must not be recorded as installed.
func TestAdoptRefusesARejectedDocument(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["not-an-agent"]}}`)

	var output bytes.Buffer
	if err := RunConfig([]string{"adopt", "--config", configPath, "--home", home}, &output); err == nil {
		t.Fatal("adopt accepted a document the contract rejects")
	}
	if _, err := os.Stat(filepath.Join(home, ".gentle-ai", "state.json")); !os.IsNotExist(err) {
		t.Error("adopt recorded a rejected document")
	}
}
