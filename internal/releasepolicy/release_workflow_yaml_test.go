package releasepolicy

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// releaseWorkflowJob mirrors only the fields this test cares about; unknown
// fields are ignored by yaml.v3 unmarshal, so it stays valid as the workflow
// grows unrelated steps.
type releaseWorkflowJob struct {
	Needs       string            `yaml:"needs"`
	Permissions map[string]string `yaml:"permissions"`
}

type releaseWorkflowDoc struct {
	Jobs map[string]releaseWorkflowJob `yaml:"jobs"`
}

// TestReleaseWorkflowYAMLNotifyJobStructure proves the embedded release
// workflow policy declares a notify job that is structurally unable to run
// before the release has published AND remote verification has passed
// (needs: verify), scoped to only the read-only permission its dispatch step
// needs (design D8, spec "Notification Runs Only After Publish AND
// Verification").
func TestReleaseWorkflowYAMLNotifyJobStructure(t *testing.T) {
	var doc releaseWorkflowDoc
	if err := yaml.Unmarshal([]byte(expectedReleaseWorkflowYAML), &doc); err != nil {
		t.Fatalf("expectedReleaseWorkflowYAML is invalid YAML: %v", err)
	}
	notify, ok := doc.Jobs["notify"]
	if !ok {
		t.Fatal("expectedReleaseWorkflowYAML has no notify job")
	}
	if notify.Needs != "verify" {
		t.Fatalf("notify job needs = %q, want %q", notify.Needs, "verify")
	}
	if got := notify.Permissions["contents"]; got != "read" {
		t.Fatalf("notify job permissions.contents = %q, want %q", got, "read")
	}
	if len(notify.Permissions) != 1 {
		t.Fatalf("notify job permissions = %v, want exactly {contents: read}", notify.Permissions)
	}
}

// TestReleaseWorkflowYAMLMatchesLiveFile proves the live
// .github/workflows/release.yml is semantically identical (same rule
// compareYAML applies at the CI preflight gate) to the embedded
// expectedReleaseWorkflowYAML constant, so a change to one without the other
// is caught locally rather than only failing closed in CI.
func TestReleaseWorkflowYAMLMatchesLiveFile(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	live, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read live release workflow: %v", err)
	}
	if err := validateYAMLSemantics("release workflow", live, expectedReleaseWorkflowYAML); err != nil {
		t.Fatalf("live release.yml diverges from expectedReleaseWorkflowYAML: %v", err)
	}
}
