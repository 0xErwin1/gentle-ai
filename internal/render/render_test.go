package render

import (
	"bytes"
	"encoding/json"
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

// An adapter that keeps agents inside one settings file still has to carry the
// role, not just its name. Rendering only the name leaves the operator with an
// entry the client reads as an agent with no description, no prompt, no tools
// and no model, which is indistinguishable from a role that was never declared.
func TestOpenCodeRendersTheWholeDeclaredRole(t *testing.T) {
	hidden := true
	state := config.DesiredState{Roles: []config.Role{
		{
			ID: "orchestrator", RenderedName: "my-orchestrator", References: []config.RoleRef{"worker"},
			Description: "coordinates", Prompt: "you coordinate", Tools: []string{"Read", "Edit"},
			Mode:  config.RolePrimary,
			Model: &config.ModelAssignment{Provider: "anthropic", Model: "claude-opus-5", Effort: "high"},
		},
		{ID: "worker", RenderedName: "my-worker", Mode: config.RoleSubagent, Hidden: &hidden},
	}}

	artifacts, err := OpenCodeProvider{}.Render(state, nil)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	var settings struct {
		Agent map[string]map[string]any `json:"agent"`
	}
	if err := json.Unmarshal(artifacts[0].Contents, &settings); err != nil {
		t.Fatalf("decode rendered settings: %v", err)
	}

	orchestrator := settings.Agent["my-orchestrator"]
	for field, want := range map[string]any{
		"description": "coordinates",
		"prompt":      "you coordinate",
		"mode":        "primary",
		"model":       "anthropic/claude-opus-5",
		"variant":     "high",
	} {
		if orchestrator[field] != want {
			t.Errorf("orchestrator %s = %v, want %v", field, orchestrator[field], want)
		}
	}

	tools, _ := orchestrator["tools"].(map[string]any)
	if tools["read"] != true || tools["edit"] != true || tools["*"] != false {
		t.Errorf("orchestrator tools = %v, want the declared tools enabled and the rest denied", tools)
	}

	permission, _ := orchestrator["permission"].(map[string]any)
	task, _ := permission["task"].(map[string]any)
	if task["my-worker"] != "allow" || task["*"] != "deny" {
		t.Errorf("orchestrator delegation = %v, want only the referenced role allowed", task)
	}

	if settings.Agent["my-worker"]["hidden"] != true {
		t.Errorf("worker hidden = %v, want true", settings.Agent["my-worker"]["hidden"])
	}
}

// A field the document left out must stay out: filling it would hand the client
// a description, a prompt or a toolset the operator never wrote.
func TestOpenCodeOmitsWhatTheRoleDidNotDeclare(t *testing.T) {
	state := config.DesiredState{Roles: []config.Role{{ID: "worker", RenderedName: "my-worker"}}}

	artifacts, err := OpenCodeProvider{}.Render(state, nil)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	var settings struct {
		Agent map[string]map[string]any `json:"agent"`
	}
	if err := json.Unmarshal(artifacts[0].Contents, &settings); err != nil {
		t.Fatalf("decode rendered settings: %v", err)
	}

	if len(settings.Agent["my-worker"]) != 0 {
		t.Errorf("rendered agent = %v, want nothing the document did not declare", settings.Agent["my-worker"])
	}
}
