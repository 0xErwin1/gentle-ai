package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

type InstallChannel = model.InstallChannel

const (
	ChannelStable = model.InstallChannelStable
	ChannelBeta   = model.InstallChannelBeta

	channelEnvVar = "GENTLE_AI_CHANNEL"
)

func ResolveInstallChannel(flagValue string) (InstallChannel, error) {
	raw := strings.TrimSpace(flagValue)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(channelEnvVar))
	}
	if raw == "" {
		return ChannelStable, nil
	}

	switch InstallChannel(strings.ToLower(raw)) {
	case ChannelStable:
		return ChannelStable, nil
	case ChannelBeta, "nightly":
		return ChannelBeta, nil
	default:
		// refusal:by-design operator-knowledge: only the operator knows which channel they meant; the message already states the complete next action (use stable, beta, or nightly), and no runnable command can pick it for them
		return "", fmt.Errorf("unsupported Gentle AI channel %q (use stable, beta, or nightly)", raw)
	}
}
