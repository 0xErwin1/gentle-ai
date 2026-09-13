package communitytool

import (
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

func TestRTKSourceContractPinsVerifiedFacts(t *testing.T) {
	if rtkReleaseTag != "v0.49.0" {
		t.Fatalf("release tag = %q", rtkReleaseTag)
	}
	if rtkReleaseCommit != "b1c0dc00649c50fbe8930f849c800d4d6ca12091" {
		t.Fatalf("release commit = %q", rtkReleaseCommit)
	}
	if rtkTelemetryDisabled != "RTK_TELEMETRY_DISABLED=1" {
		t.Fatalf("telemetry projection = %q", rtkTelemetryDisabled)
	}
	if rtkDocumentedKillSwitch != "RTK_DISABLED=1" {
		t.Fatalf("documented kill switch = %q", rtkDocumentedKillSwitch)
	}
	if len(rtkCandidateAssets) != 1 {
		t.Fatalf("candidate assets = %d, want 1", len(rtkCandidateAssets))
	}
	if len(rtkEnabledPlatforms) != 0 {
		t.Fatalf("enabled platforms = %v, want none", rtkEnabledPlatforms)
	}
}

func TestRTKSourceContractRejectsMutableOrIncompleteFacts(t *testing.T) {
	for _, tt := range []struct {
		name, value, want string
	}{
		{"release tag", rtkReleaseTag, "v0.49.0"},
		{"release commit", rtkReleaseCommit, "b1c0dc00649c50fbe8930f849c800d4d6ca12091"},
		{"telemetry environment", rtkTelemetryDisabled, "RTK_TELEMETRY_DISABLED=1"},
		{"kill-switch metadata", rtkDocumentedKillSwitch, "RTK_DISABLED=1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value != tt.want || strings.Contains(strings.ToLower(tt.value), "latest") {
				t.Fatalf("value = %q, want immutable %q", tt.value, tt.want)
			}
		})
	}
}

func TestRTKSetupContractsAreUniqueAndExact(t *testing.T) {
	tests := []struct {
		name       string
		agent      model.AgentID
		invocation string
		path       string
	}{
		{"Claude Code", model.AgentClaudeCode, "init -g", "~/.claude/CLAUDE.md"},
		{"OpenCode", model.AgentOpenCode, "init -g --opencode", "~/.config/opencode/AGENTS.md"},
		{"Codex CLI", model.AgentCodex, "init -g --codex", "~/.codex/AGENTS.md"},
		{"Pi", model.AgentPi, "init -g --agent pi", "~/.pi/agent/AGENTS.md"},
	}
	if len(rtkSetupContracts) != len(tests) {
		t.Fatalf("setup contracts = %d, want %d", len(rtkSetupContracts), len(tests))
	}

	byAgent := make(map[model.AgentID]rtkSetupContract, len(rtkSetupContracts))
	for _, contract := range rtkSetupContracts {
		if _, duplicate := byAgent[contract.Agent]; duplicate {
			t.Fatalf("duplicate setup contract for %q", contract.Agent)
		}
		byAgent[contract.Agent] = contract
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contract, ok := byAgent[tt.agent]
			if !ok {
				t.Fatalf("missing contract for %q", tt.agent)
			}
			if contract.Invocation != tt.invocation || strings.Contains(strings.ToLower(contract.Invocation), "latest") {
				t.Fatalf("invocation = %q, want %q", contract.Invocation, tt.invocation)
			}
			if contract.UserMutationPath.Class != rtkUserMutationPathInstruction || contract.UserMutationPath.Template != tt.path {
				t.Fatalf("user mutation path = %#v, want instruction file %q", contract.UserMutationPath, tt.path)
			}
		})
	}
}

func TestRTKCandidateAssetIsNotPlatformAdmission(t *testing.T) {
	tests := []struct {
		name, want string
	}{
		{"platform", string(rtkPlatformWindows)},
		{"name", "rtk-x86_64-pc-windows-msvc.zip"},
		{"immutable URL", "https://github.com/rtk-ai/rtk/releases/download/v0.49.0/rtk-x86_64-pc-windows-msvc.zip"},
		{"executable member", "rtk.exe"},
		{"SHA-256", "cb971046598f0e8bd51f6c27780fcdd2c39a4c459a811bd95b0d77ba8c0d7c9f"},
		{"checksum-file SHA-256", "a5ff3570fe196a21e09a249c1777665d6ed887630d1c7c66de1154d4340d3ad0"},
	}
	asset := rtkCandidateAssets[0]
	got := []string{string(asset.Platform), asset.Name, asset.URL, asset.ExecutableMember, asset.SHA256, asset.ChecksumSHA256}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got[i] != tt.want || strings.Contains(strings.ToLower(got[i]), "latest") {
				t.Fatalf("candidate %s = %q, want %q", tt.name, got[i], tt.want)
			}
		})
	}
	if asset.SizeBytes != 4_448_627 {
		t.Fatalf("candidate size = %d, want 4448627", asset.SizeBytes)
	}
	if len(rtkEnabledPlatforms) != 0 {
		t.Fatalf("enabled platforms = %v, want none", rtkEnabledPlatforms)
	}
}
