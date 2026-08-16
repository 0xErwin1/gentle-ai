package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents/claude"
	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
)

func TestRendererStagesOpenCodeDeterministically(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	destination := t.TempDir()
	livePath := filepath.Join(destination, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(livePath), 0o755); err != nil {
		t.Fatal(err)
	}
	live := []byte("{\"theme\":\"user\",\"agent\":{\"user-agent\":{\"description\":\"keep\"}}}\n")
	if err := os.WriteFile(livePath, live, 0o644); err != nil {
		t.Fatal(err)
	}

	request := Request{
		Destination: destination,
		StageRoot:   t.TempDir(),
		Baseline: map[string][]byte{
			".config/opencode/opencode.json": live,
		},
		State: config.DesiredState{Roles: []config.Role{
			{ID: "planner", RenderedName: "planner-v2", References: []config.RoleRef{"reviewer"}},
			{ID: "reviewer", RenderedName: "reviewer-v2"},
		}},
	}

	first, err := New(OpenCodeProvider{}).Render(request)
	if err != nil {
		t.Fatal(err)
	}
	request.StageRoot = t.TempDir()
	second, err := New(OpenCodeProvider{}).Render(request)
	if err != nil {
		t.Fatal(err)
	}

	firstBytes := readArtifact(t, first, ".config/opencode/opencode.json")
	secondBytes := readArtifact(t, second, ".config/opencode/opencode.json")
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("rendered bytes differ:\n%s\n%s", firstBytes, secondBytes)
	}
	if !bytes.Contains(firstBytes, []byte(`"theme": "user"`)) || !bytes.Contains(firstBytes, []byte(`"user-agent"`)) {
		t.Fatalf("user baseline was not composed: %s", firstBytes)
	}
	if !bytes.Contains(firstBytes, []byte(`"planner-v2"`)) || !bytes.Contains(firstBytes, []byte(`"reviewer-v2"`)) {
		t.Fatalf("logical role names were not propagated: %s", firstBytes)
	}
	if got, err := os.ReadFile(livePath); err != nil || !bytes.Equal(got, live) {
		t.Fatalf("live destination changed: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); !os.IsNotExist(err) {
		t.Fatalf("HOME changed: %v", err)
	}
}

func TestRendererRejectsBaselinePathEscape(t *testing.T) {
	_, err := New(OpenCodeProvider{}).Render(Request{
		Destination: t.TempDir(),
		StageRoot:   t.TempDir(),
		Baseline:    map[string][]byte{"../escape": []byte("no")},
	})
	if err == nil {
		t.Fatal("path escape was accepted")
	}
}

func TestRendererRejectsOverlappingStageAndDestination(t *testing.T) {
	destination := t.TempDir()
	_, err := New(OpenCodeProvider{}).Render(Request{
		Destination: destination,
		StageRoot:   filepath.Dir(destination),
	})
	if err == nil {
		t.Fatal("overlapping stage root was accepted")
	}
}

func readArtifact(t *testing.T, snapshot Snapshot, path string) []byte {
	t.Helper()

	for _, artifact := range snapshot.Artifacts {
		if artifact.Path == path {
			bytes, err := os.ReadFile(filepath.Join(snapshot.Stage, filepath.FromSlash(path)))
			if err != nil {
				t.Fatal(err)
			}
			return bytes
		}
	}

	t.Fatalf("artifact %q was not rendered", path)
	return nil
}

// A rename is one edit in the document only if every generated file follows it.
// Emitting the logical id leaves the orchestrator naming an agent no adapter
// renders, which is the textual replacement the contract exists to avoid.
func TestRenamingARoleUpdatesTheFilesThatReferenceIt(t *testing.T) {
	stage := t.TempDir()
	state := config.DesiredState{Roles: []config.Role{
		{ID: "orchestrator", RenderedName: "gentle-orchestrator", References: []config.RoleRef{"apply"}},
		{ID: "apply", RenderedName: "gentle-implementer"},
	}}

	if err := NewRoleProvider(claude.NewAdapter()).Stage(state, stage); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}

	document, err := os.ReadFile(filepath.Join(stage, ".claude", "agents", "gentle-orchestrator.md"))
	if err != nil {
		t.Fatalf("read rendered orchestrator: %v", err)
	}
	if !strings.Contains(string(document), "references: gentle-implementer") {
		t.Errorf("rendered orchestrator = %s, want the reference resolved to the rendered name", document)
	}
}
