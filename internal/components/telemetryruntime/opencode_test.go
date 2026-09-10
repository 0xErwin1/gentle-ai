package telemetryruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/telemetry"
)

func TestReadOpenCodeAssignment(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config string
		agent  string
		want   model.ModelAssignment
	}{
		{
			name:   "reads model and effort for the observed agent",
			config: `{"agent":{"sdd-apply":{"model":"openai/gpt-5.6","variant":"high"},"private-agent":{"model":"anthropic/claude-opus-5","variant":"xhigh"}}}`,
			agent:  "sdd-apply",
			want:   model.ModelAssignment{ProviderID: "openai", ModelID: "gpt-5.6", Effort: "high"},
		},
		{name: "missing agent", config: `{"agent":{}}`, agent: "sdd-apply"},
		{name: "invalid config", config: `{`, agent: "sdd-apply"},
		{name: "oversized config", config: strings.Repeat(" ", openCodeConfigMaxBytes+1), agent: "sdd-apply"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			path := filepath.Join(home, ".config", "opencode", "opencode.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := readOpenCodeAssignment(home, tt.agent); got != tt.want {
				t.Fatalf("readOpenCodeAssignment() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestApplyOpenCodeAssignment(t *testing.T) {
	for _, tt := range []struct {
		name       string
		row        telemetry.RuntimeRow
		assignment model.ModelAssignment
		wantModel  telemetry.RuntimeModel
		wantProof  string
		wantEffort string
	}{
		{
			name:       "response model wins and selected effort is retained",
			row:        telemetry.RuntimeRow{Model: telemetry.RuntimeModel{Provider: "openai", ID: "gpt-5.4"}, ModelEvidence: "response", SelectedEffort: "unavailable"},
			assignment: model.ModelAssignment{ProviderID: "openai", ModelID: "gpt-5.6", Effort: "high"},
			wantModel:  telemetry.RuntimeModel{Provider: "openai", ID: "gpt-5.4"}, wantProof: "response", wantEffort: "high",
		},
		{
			name:       "selected model fills absent response model",
			row:        telemetry.RuntimeRow{Model: telemetry.RuntimeModel{Provider: "unknown", ID: "unknown"}, ModelEvidence: "unknown", SelectedEffort: "unavailable"},
			assignment: model.ModelAssignment{ProviderID: "anthropic", ModelID: "claude-opus-5", Effort: "xhigh"},
			wantModel:  telemetry.RuntimeModel{Provider: "anthropic", ID: "claude-opus-5"}, wantProof: "selected", wantEffort: "xhigh",
		},
		{
			name:       "invalid effort is unavailable",
			row:        telemetry.RuntimeRow{Model: telemetry.RuntimeModel{Provider: "unknown", ID: "unknown"}, ModelEvidence: "unknown", SelectedEffort: "unavailable"},
			assignment: model.ModelAssignment{ProviderID: "private", ModelID: "private", Effort: "turbo"},
			wantModel:  telemetry.RuntimeModel{Provider: "custom", ID: "custom"}, wantProof: "selected", wantEffort: "unavailable",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			applyOpenCodeAssignment(&tt.row, tt.assignment)
			if tt.row.Model != tt.wantModel || tt.row.ModelEvidence != tt.wantProof || tt.row.SelectedEffort != tt.wantEffort {
				t.Fatalf("row = %+v", tt.row)
			}
		})
	}
}
