package communitytool

import "github.com/gentleman-programming/gentle-ai/v2/internal/model"

const (
	rtkReleaseTag           = "v0.49.0"
	rtkReleaseCommit        = "b1c0dc00649c50fbe8930f849c800d4d6ca12091"
	rtkTelemetryDisabled    = "RTK_TELEMETRY_DISABLED=1"
	rtkDocumentedKillSwitch = "RTK_DISABLED=1"
)

type rtkUserMutationPathClass string

const rtkUserMutationPathInstruction rtkUserMutationPathClass = "instruction-file"

type rtkUserMutationPath struct {
	Class    rtkUserMutationPathClass
	Template string
}

// rtkSetupContracts records source-derived init metadata. It neither resolves a
// home directory nor constructs or executes a command.
type rtkSetupContract struct {
	Agent            model.AgentID
	Invocation       string
	UserMutationPath rtkUserMutationPath
}

var rtkSetupContracts = []rtkSetupContract{
	{model.AgentClaudeCode, "init -g", rtkUserMutationPath{rtkUserMutationPathInstruction, "~/.claude/CLAUDE.md"}},
	{model.AgentOpenCode, "init -g --opencode", rtkUserMutationPath{rtkUserMutationPathInstruction, "~/.config/opencode/AGENTS.md"}},
	{model.AgentCodex, "init -g --codex", rtkUserMutationPath{rtkUserMutationPathInstruction, "~/.codex/AGENTS.md"}},
	{model.AgentPi, "init -g --agent pi", rtkUserMutationPath{rtkUserMutationPathInstruction, "~/.pi/agent/AGENTS.md"}},
}

type rtkPlatform string

const rtkPlatformWindows rtkPlatform = "windows"

type rtkCandidateAsset struct {
	Platform         rtkPlatform
	Name             string
	URL              string
	ExecutableMember string
	SizeBytes        int64
	SHA256           string
	ChecksumSHA256   string
}

// rtkCandidateAssets preserves release evidence without admitting a production
// platform. A later release gate must populate rtkEnabledPlatforms explicitly.
var rtkCandidateAssets = []rtkCandidateAsset{
	{
		Platform:         rtkPlatformWindows,
		Name:             "rtk-x86_64-pc-windows-msvc.zip",
		URL:              "https://github.com/rtk-ai/rtk/releases/download/v0.49.0/rtk-x86_64-pc-windows-msvc.zip",
		ExecutableMember: "rtk.exe",
		SizeBytes:        4_448_627,
		SHA256:           "cb971046598f0e8bd51f6c27780fcdd2c39a4c459a811bd95b0d77ba8c0d7c9f",
		ChecksumSHA256:   "a5ff3570fe196a21e09a249c1777665d6ed887630d1c7c66de1154d4340d3ad0",
	},
}

// rtkEnabledPlatforms is deliberately empty: candidate release facts are not
// production support until a dedicated platform-admission slice approves them.
var rtkEnabledPlatforms = []rtkPlatform{}
