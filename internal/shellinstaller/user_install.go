package shellinstaller

// Separate creates private tooling, HOME and agent state; personal Pi stays put.
type UserInstallRequest struct {
	Destination, Mode, SharedPrefix, SharedAgent, Confirmation string
}

type UserInstallResult struct {
	Destination, Prefix, Agent, State string
}
