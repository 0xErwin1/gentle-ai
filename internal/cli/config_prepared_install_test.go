package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/system"
)

// PrepareInstall is the dispatch seam: it resolves the declared document target
// and runs plan validation without reading or writing install state, and
// RunPreparedInstall must reuse the exact parsed input it produced. A prepared
// plan that dropped the declared scope or channel would install machine-wide
// while the document validated, persisted and exported a workspace install.
func TestPrepareInstallResolvesDeclaredDocumentTarget(t *testing.T) {
	document := filepath.Join(t.TempDir(), "gentle-ai.json")
	contents := `{"version":"v1","selection":{"agents":["opencode"],"persona":"neutral","scope":"workspace","channel":"beta"}}`
	if err := os.WriteFile(document, []byte(contents), 0o644); err != nil {
		t.Fatalf("write document: %v", err)
	}
	args := []string{"--config", document, "--dry-run"}
	detection := system.DetectionResult{}

	prepared, err := PrepareInstall(args, detection)
	if err != nil {
		t.Fatalf("PrepareInstall() error = %v", err)
	}
	if prepared.input.Scope != ScopeWorkspace {
		t.Errorf("prepared Scope = %q, want %q: a declared workspace install would be written machine-wide", prepared.input.Scope, ScopeWorkspace)
	}
	if prepared.input.Channel != ChannelBeta {
		t.Errorf("prepared Channel = %q, want %q: a declared channel would not select its release", prepared.input.Channel, ChannelBeta)
	}
	if !prepared.input.DryRun {
		t.Errorf("prepared input lost the operational --dry-run flag")
	}
	if !prepared.flags.DryRun {
		t.Errorf("prepared flags lost --dry-run")
	}

	result, err := RunPreparedInstall(prepared, detection)
	if err != nil {
		t.Fatalf("RunPreparedInstall() error = %v", err)
	}
	if !result.DryRun {
		t.Errorf("RunPreparedInstall() DryRun = false, want the prepared dry run")
	}
	if result.Selection.Persona != "neutral" {
		t.Errorf("RunPreparedInstall() persona = %q, want the document selection", result.Selection.Persona)
	}

	direct, err := RunInstall(args, detection)
	if err != nil {
		t.Fatalf("RunInstall() error = %v", err)
	}
	if !reflect.DeepEqual(result.Selection, direct.Selection) {
		t.Errorf("RunPreparedInstall() selection = %+v, want the exact input RunInstall resolved: %+v", result.Selection, direct.Selection)
	}
}

// Document refusals and modifier preflight refusals surface from PrepareInstall
// with their exact diagnostics, before any install state is touched.
func TestPrepareInstallRefusalsKeepExactDiagnostics(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.json")
	malformed := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(malformed, []byte(`{"version":"v1",`), 0o644); err != nil {
		t.Fatalf("write document: %v", err)
	}

	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"missing document", []string{"--config", missing}, "read config: "},
		{"malformed document", []string{"--config", malformed}, "config validation failed: config.document.malformed"},
		{"skill without consumer", []string{"--agent", "opencode", "--preset", "minimal", "--skill", "go-testing"}, "--skill/--skills requires component skills in the resolved install plan"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := PrepareInstall(test.args, system.DetectionResult{})
			if err == nil {
				t.Fatalf("PrepareInstall(%v) error = nil, want %q", test.args, test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("PrepareInstall(%v) error = %v, want diagnostic %q", test.args, err, test.want)
			}
		})
	}
}
