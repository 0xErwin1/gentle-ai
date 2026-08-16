package cli

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

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
