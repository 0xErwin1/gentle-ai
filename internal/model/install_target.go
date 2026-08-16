package model

// InstallScope controls where agent-scoped config files are written. The domain
// lives here rather than in the CLI because the declarative contract has to name
// the same values, and the contract package cannot import the CLI.
type InstallScope string

const (
	// InstallScopeGlobal writes to the global agent config dir.
	InstallScopeGlobal InstallScope = "global"
	// InstallScopeWorkspace writes to the current workspace config root.
	InstallScopeWorkspace InstallScope = "workspace"
)

func (s InstallScope) Valid() bool {
	return s == InstallScopeGlobal || s == InstallScopeWorkspace
}

// InstallChannel selects the release track an installation follows.
type InstallChannel string

const (
	InstallChannelStable InstallChannel = "stable"
	InstallChannelBeta   InstallChannel = "beta"
)

func (c InstallChannel) Valid() bool {
	return c == InstallChannelStable || c == InstallChannelBeta
}

func (c InstallChannel) IsBeta() bool {
	return c == InstallChannelBeta
}
