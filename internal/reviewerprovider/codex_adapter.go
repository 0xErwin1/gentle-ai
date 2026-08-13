package reviewerprovider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// CodexAdapter invokes Codex with an opaque provider invocation and returns
// the CLI's raw final-message bytes without interpreting them.
type CodexAdapter struct {
	LookPath       func(string) (string, error)
	commandContext func(context.Context, string, ...string) *exec.Cmd
}

// NewCodexAdapter returns an adapter using the Codex binary resolved from PATH.
func NewCodexAdapter() *CodexAdapter {
	return &CodexAdapter{LookPath: exec.LookPath, commandContext: exec.CommandContext}
}

// Review runs Codex in an empty temporary directory. The prompt is delivered
// through stdin so command arguments never carry provider material.
func (adapter *CodexAdapter) Review(ctx context.Context, invocation Invocation) ([]byte, error) {
	lookPath := adapter.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	binary, err := lookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("codex reviewer transport unavailable: %w", err)
	}

	scratch, err := os.MkdirTemp("", "gentle-ai-codex-reviewer-*")
	if err != nil {
		return nil, fmt.Errorf("codex reviewer transport unavailable: create scratch directory: %w", err)
	}
	defer os.RemoveAll(scratch)

	outputPath := filepath.Join(scratch, "result")
	commandContext := adapter.commandContext
	if commandContext == nil {
		commandContext = exec.CommandContext
	}
	command := commandContext(ctx, binary,
		"exec", "--skip-git-repo-check", "--ignore-user-config",
		"--sandbox", "read-only", "-C", scratch,
		"--output-last-message", outputPath,
	)
	command.Dir = scratch
	command.Stdin = bytes.NewReader(invocation.Prompt())
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("codex reviewer transport failed: %w: %s", err, stderr.String())
	}

	raw, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("codex reviewer transport produced no final message: %w", err)
	}
	return raw, nil
}
