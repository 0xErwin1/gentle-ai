package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
	"github.com/gentleman-programming/gentle-ai/v2/internal/system"
)

// A declared install target that only the helper honours is worse than one the
// contract never carried: it validates, persists and exports while the install
// runs against the flag defaults. Declaring a workspace scope and receiving a
// machine-wide install is the concrete cost, so this exercises the input the
// installer actually consumes rather than the helper in isolation.
func TestDeclaredInstallTargetReachesTheInstallInput(t *testing.T) {
	document := filepath.Join(t.TempDir(), "gentle-ai.json")
	contents := `{"version":"v1","selection":{"agents":["opencode"],"scope":"workspace","channel":"beta"}}`
	if err := os.WriteFile(document, []byte(contents), 0o644); err != nil {
		t.Fatalf("write document: %v", err)
	}

	input, err := ResolveInstallInput(InstallFlags{Config: document}, system.DetectionResult{})
	if err != nil {
		t.Fatalf("ResolveInstallInput() error = %v", err)
	}

	if input.Scope != ScopeWorkspace {
		t.Errorf("Scope = %q, want %q: a declared workspace install would be written machine-wide", input.Scope, ScopeWorkspace)
	}
	if input.Channel != ChannelBeta {
		t.Errorf("Channel = %q, want %q: a declared channel would not select its release", input.Channel, ChannelBeta)
	}
}

func TestApplyDeclaredInstallTarget(t *testing.T) {
	tests := []struct {
		name        string
		input       InstallInput
		selection   model.Selection
		wantScope   InstallScope
		wantChannel InstallChannel
	}{
		{
			name:        "a declared target overrides what the flag and environment resolved",
			input:       InstallInput{Scope: ScopeGlobal, Channel: ChannelStable},
			selection:   model.Selection{Scope: model.InstallScopeWorkspace, Channel: model.InstallChannelBeta},
			wantScope:   ScopeWorkspace,
			wantChannel: ChannelBeta,
		},
		{
			name:        "an omitted declaration leaves the resolved values alone",
			input:       InstallInput{Scope: ScopeWorkspace, Channel: ChannelBeta},
			selection:   model.Selection{},
			wantScope:   ScopeWorkspace,
			wantChannel: ChannelBeta,
		},
		{
			name:        "each field is independent",
			input:       InstallInput{Scope: ScopeGlobal, Channel: ChannelBeta},
			selection:   model.Selection{Scope: model.InstallScopeWorkspace},
			wantScope:   ScopeWorkspace,
			wantChannel: ChannelBeta,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := applyDeclaredInstallTarget(test.input, test.selection)

			if got.Scope != test.wantScope {
				t.Errorf("Scope = %q, want %q", got.Scope, test.wantScope)
			}
			if got.Channel != test.wantChannel {
				t.Errorf("Channel = %q, want %q", got.Channel, test.wantChannel)
			}
		})
	}
}

// A full install stamps the version of the binary that wrote the assets. Losing
// it leaves doctor's staleness check permanently skipped, so an upgrade never
// reports that the installed assets are older than the running binary.
func TestFullInstallStateKeepsTheVersionOfTheRunThatWroteIt(t *testing.T) {
	merged := mergeFullInstallState(
		state.InstallState{},
		state.InstallState{InstalledAgents: []string{"opencode"}, InstalledBinaryVersion: AppVersion},
	)

	if merged.InstalledBinaryVersion != AppVersion {
		t.Errorf("InstalledBinaryVersion = %q, want %q", merged.InstalledBinaryVersion, AppVersion)
	}
}
