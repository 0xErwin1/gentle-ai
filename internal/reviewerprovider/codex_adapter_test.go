package reviewerprovider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const codexAdapterHelperEnvironment = "GENTLE_AI_REVIEWER_PROVIDER_CODEX_HELPER"
const codexAdapterPromptPathEnvironment = "GENTLE_AI_REVIEWER_PROVIDER_CODEX_PROMPT_PATH"
const codexAdapterArgumentsPathEnvironment = "GENTLE_AI_REVIEWER_PROVIDER_CODEX_ARGUMENTS_PATH"

func TestCodexAdapterReturnsNoBytesWhenUnavailable(t *testing.T) {
	adapter := &CodexAdapter{LookPath: func(string) (string, error) { return "", errors.New("not found") }}
	raw, err := adapter.Review(context.Background(), NewInvocation([]byte("provider prompt")))
	if err == nil || !strings.Contains(err.Error(), "codex reviewer transport unavailable") {
		t.Fatalf("Review() error = %v, want unavailable transport error", err)
	}
	if raw != nil {
		t.Fatalf("Review() raw = %q with transport error, want no result bytes", raw)
	}
}

func TestCodexAdapterUsesStdinAndReturnsUntouchedRawOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the helper process uses POSIX argument handling")
	}
	promptPath := filepath.Join(t.TempDir(), "prompt")
	argumentsPath := filepath.Join(t.TempDir(), "arguments")
	t.Setenv(codexAdapterHelperEnvironment, "1")
	t.Setenv(codexAdapterPromptPathEnvironment, promptPath)
	t.Setenv(codexAdapterArgumentsPathEnvironment, argumentsPath)

	adapter := &CodexAdapter{
		LookPath: func(string) (string, error) { return "codex", nil },
		commandContext: func(ctx context.Context, _ string, arguments ...string) *exec.Cmd {
			return exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestCodexAdapterHelperProcess$", "--"}, arguments...)...)
		},
	}
	prompt := []byte("provider prompt\nwith bytes")
	raw, err := adapter.Review(context.Background(), NewInvocation(prompt))
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte("raw\x00reviewer\xffoutput"); !bytes.Equal(raw, want) {
		t.Fatalf("Review() = %q, want untouched raw bytes %q", raw, want)
	}
	if got, err := os.ReadFile(promptPath); err != nil || !bytes.Equal(got, prompt) {
		t.Fatalf("reviewer stdin = %q, %v; want %q", got, err, prompt)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(arguments), string(prompt)) {
		t.Fatalf("codex arguments carried provider prompt: %q", arguments)
	}
	for _, flag := range []string{"exec", "--skip-git-repo-check", "--ignore-user-config", "--sandbox", "read-only", "--output-last-message"} {
		if !strings.Contains(string(arguments), flag) {
			t.Fatalf("codex arguments = %q, missing %q", arguments, flag)
		}
	}
}

func TestCodexAdapterHelperProcess(t *testing.T) {
	if os.Getenv(codexAdapterHelperEnvironment) != "1" {
		return
	}
	prompt, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(1)
	}
	if err := os.WriteFile(os.Getenv(codexAdapterPromptPathEnvironment), prompt, 0o600); err != nil {
		os.Exit(1)
	}
	if err := os.WriteFile(os.Getenv(codexAdapterArgumentsPathEnvironment), []byte(strings.Join(os.Args[1:], "\n")), 0o600); err != nil {
		os.Exit(1)
	}
	for index, argument := range os.Args {
		if argument == "--output-last-message" && index+1 < len(os.Args) {
			if err := os.WriteFile(os.Args[index+1], []byte("raw\x00reviewer\xffoutput"), 0o600); err != nil {
				os.Exit(1)
			}
			return
		}
	}
	os.Exit(1)
}
