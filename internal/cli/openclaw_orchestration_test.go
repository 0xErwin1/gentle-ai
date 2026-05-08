package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/system"
)

func TestComponentApplyStepOpenClawWorkspaceScopedInjections(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()

	restoreLookPath := cmdLookPath
	t.Cleanup(func() { cmdLookPath = restoreLookPath })
	cmdLookPath = func(name string) (string, error) {
		return filepath.Join(home, "bin", name), nil
	}

	tests := []struct {
		name          string
		component     model.ComponentID
		workspaceFile string
		homeFile      string
		marker        string
	}{
		{
			name:          "engram writes protocol to workspace AGENTS",
			component:     model.ComponentEngram,
			workspaceFile: filepath.Join(workspace, "AGENTS.md"),
			homeFile:      filepath.Join(home, "AGENTS.md"),
			marker:        "<!-- gentle-ai:engram-protocol -->",
		},
		{
			name:          "persona writes soul to workspace",
			component:     model.ComponentPersona,
			workspaceFile: filepath.Join(workspace, "SOUL.md"),
			homeFile:      filepath.Join(home, "SOUL.md"),
			marker:        "<!-- gentle-ai:persona -->",
		},
		{
			name:          "sdd writes protocol to workspace AGENTS",
			component:     model.ComponentSDD,
			workspaceFile: filepath.Join(workspace, "AGENTS.md"),
			homeFile:      filepath.Join(home, "AGENTS.md"),
			marker:        "<!-- gentle-ai:sdd-orchestrator -->",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := componentApplyStep{
				id:           "component:" + string(tt.component),
				component:    tt.component,
				homeDir:      home,
				workspaceDir: workspace,
				agents:       []model.AgentID{model.AgentOpenClaw},
				selection:    model.Selection{Persona: model.PersonaGentleman},
				profile:      system.PlatformProfile{PackageManager: "brew"},
			}

			if err := step.Run(); err != nil {
				t.Fatalf("componentApplyStep.Run() error = %v", err)
			}

			body, err := os.ReadFile(tt.workspaceFile)
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", tt.workspaceFile, err)
			}
			if !strings.Contains(string(body), tt.marker) {
				t.Fatalf("workspace file missing marker %q; got:\n%s", tt.marker, string(body))
			}
			if _, err := os.Stat(tt.homeFile); !os.IsNotExist(err) {
				t.Fatalf("OpenClaw orchestration must not write %q; stat err=%v", tt.homeFile, err)
			}
		})
	}
}

func TestComponentSyncStepOpenClawWorkspaceScopedInjections(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()

	tests := []struct {
		name          string
		component     model.ComponentID
		workspaceFile string
		homeFile      string
		marker        string
	}{
		{
			name:          "engram sync writes protocol to workspace AGENTS",
			component:     model.ComponentEngram,
			workspaceFile: filepath.Join(workspace, "AGENTS.md"),
			homeFile:      filepath.Join(home, "AGENTS.md"),
			marker:        "<!-- gentle-ai:engram-protocol -->",
		},
		{
			name:          "persona sync writes soul to workspace",
			component:     model.ComponentPersona,
			workspaceFile: filepath.Join(workspace, "SOUL.md"),
			homeFile:      filepath.Join(home, "SOUL.md"),
			marker:        "<!-- gentle-ai:persona -->",
		},
		{
			name:          "sdd sync writes protocol to workspace AGENTS",
			component:     model.ComponentSDD,
			workspaceFile: filepath.Join(workspace, "AGENTS.md"),
			homeFile:      filepath.Join(home, "AGENTS.md"),
			marker:        "<!-- gentle-ai:sdd-orchestrator -->",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := componentSyncStep{
				id:           "sync:component:" + string(tt.component),
				component:    tt.component,
				homeDir:      home,
				workspaceDir: workspace,
				agents:       []model.AgentID{model.AgentOpenClaw},
				selection:    model.Selection{Persona: model.PersonaGentleman},
			}

			if err := step.Run(); err != nil {
				t.Fatalf("componentSyncStep.Run() error = %v", err)
			}

			body, err := os.ReadFile(tt.workspaceFile)
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", tt.workspaceFile, err)
			}
			if !strings.Contains(string(body), tt.marker) {
				t.Fatalf("workspace file missing marker %q; got:\n%s", tt.marker, string(body))
			}
			if _, err := os.Stat(tt.homeFile); !os.IsNotExist(err) {
				t.Fatalf("OpenClaw sync must not write %q; stat err=%v", tt.homeFile, err)
			}
		})
	}
}
