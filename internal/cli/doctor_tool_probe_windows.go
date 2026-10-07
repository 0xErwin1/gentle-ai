package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Batch launchers need cmd.exe; Go's CreateProcess cannot run them directly.
func doctorToolProbeCommand(ctx context.Context, path, arg string) (*exec.Cmd, error) {
	ext := filepath.Ext(path)
	if !strings.EqualFold(ext, ".cmd") && !strings.EqualFold(ext, ".bat") {
		return exec.CommandContext(ctx, path, arg), nil
	}
	// cmd expands percent variables even inside quotes. Refuse ambiguous paths
	// rather than executing a different command than the PATH-resolved launcher.
	if strings.ContainsAny(path, "%!\r\n\"") {
		return nil, fmt.Errorf("cannot safely probe batch launcher path %q; move the launcher to a path without expansion characters or quotes, then run 'gentle-ai doctor' again", path)
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		return nil, fmt.Errorf("SystemRoot is unset; restore it to your Windows directory, then run 'gentle-ai doctor' again")
	}
	shell := filepath.Join(root, "System32", "cmd.exe")
	cmd := exec.CommandContext(ctx, shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + shell + `" /d /v:off /s /c ""` + path + `" ` + arg + `"`}
	return cmd, nil
}
