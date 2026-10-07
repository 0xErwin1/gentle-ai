//go:build !windows

package cli

import (
	"context"
	"os/exec"
)

func doctorToolProbeCommand(ctx context.Context, path, arg string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, path, arg), nil
}
