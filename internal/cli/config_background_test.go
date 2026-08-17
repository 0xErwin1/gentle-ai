package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// backgroundPolicyStaged reports whether the policy block reached anything the
// client reads. The prompt it belongs to is composed into OpenCode's settings
// rather than written to a path of its own, so the whole staged tree is the
// honest place to look for it.
func backgroundPolicyStaged(t *testing.T, intent string) bool {
	t.Helper()

	const marker = "gentle-ai:opencode-background-subagents"

	document := `{"version":"v1","selection":{"agents":["opencode"],"components":["sdd"],"sddMode":"multi","backgroundIntent":"` + intent + `"}}`
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	assertConfigOutput(t, []string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, `"operation": "render"`)

	staged := stagedFiles(t, stage)
	if len(staged) == 0 {
		t.Fatalf("intent %q staged nothing", intent)
	}

	for _, path := range staged {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %q: %v", path, err)
		}
		if strings.Contains(string(content), marker) {
			return true
		}
	}

	return false
}

// Turning OpenCode's background sub-agents on adds a policy block to its
// orchestrator prompt. Declaring the intent and rendering the same prompt as
// declaring nothing is the worst of both: the choice is recorded, reported as
// applied, and absent from what the client actually reads.
func TestRenderCarriesTheOpenCodeBackgroundPolicy(t *testing.T) {
	if !backgroundPolicyStaged(t, "on") {
		t.Errorf("an enabled background intent rendered no policy block")
	}
	if backgroundPolicyStaged(t, "off") {
		t.Errorf("a disabled background intent rendered the policy block")
	}
}

// Pi reads its background policy from a file Gentle AI owns, the same shape as
// its persona config. Declaring the intent and staging nothing leaves the file
// carrying whatever a previous run resolved, so the document and what gentle-pi
// reads disagree with nothing to say so.
func TestRenderWritesThePiBackgroundPolicy(t *testing.T) {
	for _, intent := range []string{"on", "off"} {
		document := `{"version":"v1","selection":{"agents":["pi"],"piBackgroundIntent":"` + intent + `"}}`
		configPath := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
			t.Fatal(err)
		}

		stage := t.TempDir()
		assertConfigOutput(t, []string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, `"operation": "render"`)

		content, err := os.ReadFile(filepath.Join(stage, ".pi", "gentle-ai", "background-subagents.json"))
		if err != nil {
			t.Fatalf("read staged Pi background policy for %q: %v (staged %v)", intent, err, stagedFiles(t, stage))
		}
		if !strings.Contains(string(content), `"policy": "`+intent+`"`) {
			t.Errorf("intent %q staged %s", intent, content)
		}
	}
}

// Auto never reaches projection in the installer either: it means the runtime
// decides, and writing a resolved policy for it would answer on its behalf.
func TestRenderLeavesAnUnresolvedPiBackgroundIntentAlone(t *testing.T) {
	document := `{"version":"v1","selection":{"agents":["pi"],"piBackgroundIntent":"auto"}}`
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}

	stage := t.TempDir()
	assertConfigOutput(t, []string{"render", "--config", configPath, "--destination", t.TempDir(), "--stage", stage}, `"operation": "render"`)

	for _, path := range stagedFiles(t, stage) {
		if strings.Contains(path, "background-subagents.json") {
			t.Errorf("an unresolved Pi background intent staged %q", path)
		}
	}
}

// Auto is the unresolved value: it defers to whatever the client's runtime
// supports, which a render cannot see without making the same document produce
// different bytes on two machines. Deferring means rendering the prompt that
// carries no policy, not guessing.
func TestRenderLeavesAnUnresolvedBackgroundIntentAlone(t *testing.T) {
	if backgroundPolicyStaged(t, "auto") {
		t.Errorf("an unresolved background intent rendered the policy block")
	}
}
