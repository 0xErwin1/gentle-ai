package shellinstaller

import "fmt"

// Separate creates private tooling, HOME and agent state; personal Pi stays put.
type UserInstallRequest struct {
	Destination, Mode, SharedPrefix, SharedAgent, Confirmation string
	Channel                                                  string
}

func UserInstallChannel(channel string) (string, error) {
	if channel == "" {
		channel = "stable"
	}
	if channel != "stable" && channel != "main" {
		return "", fmt.Errorf("unknown Gentle Shell channel %q; use stable or main", channel)
	}
	return channel, nil
}

type UserInstallResult struct {
	Destination, Prefix, Agent, State string
}
