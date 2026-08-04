package engram

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// setStdioHelperProcess routes the execCommandContext seam through this test
// binary re-executing TestHelperEngramMCPProcess in the given mode, so the
// handshake runs against a real child process speaking real stdio (#2078
// matrix cell b).
func setStdioHelperProcess(t *testing.T, mode string) {
	t.Helper()
	orig := execCommandContext
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cs := append([]string{"-test.run=TestHelperEngramMCPProcess", "--", name}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = append(os.Environ(), "GO_ENGRAM_HELPER_MODE="+mode)
		return cmd
	}
	t.Cleanup(func() { execCommandContext = orig })
}

// TestHelperEngramMCPProcess is not a real test: it is the fake engram
// endpoint. When re-executed with GO_ENGRAM_HELPER_MODE set it emulates an
// engram MCP server over stdio in one of several modes, then exits without
// letting the test framework print to stdout (stdout is the MCP channel).
func TestHelperEngramMCPProcess(t *testing.T) {
	mode := os.Getenv("GO_ENGRAM_HELPER_MODE")
	if mode == "" {
		return
	}
	defer os.Exit(0)

	switch mode {
	case "healthy", "rpc-error":
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		var req struct {
			ID     json.Number `json:"id"`
			Method string      `json:"method"`
		}
		if json.Unmarshal([]byte(line), &req) != nil || req.Method != "initialize" {
			return
		}
		if mode == "rpc-error" {
			fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":%s,\"error\":{\"code\":-32603,\"message\":\"store locked\"}}\n", req.ID.String())
			return
		}
		// A stray log line first proves non-JSON output is tolerated.
		fmt.Println("engram mcp starting")
		fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"protocolVersion\":\"2024-11-05\",\"capabilities\":{},\"serverInfo\":{\"name\":\"fake-engram\",\"version\":\"0\"}}}\n", req.ID.String())
		// Stay alive until the probe tears the process down.
		time.Sleep(30 * time.Second)
	case "garbage":
		fmt.Println("this is not a JSON-RPC message")
	case "silent":
		time.Sleep(30 * time.Second)
	}
}

func TestStdioHandshake_Healthy(t *testing.T) {
	setStdioHelperProcess(t, "healthy")

	if err := stdioHandshake(context.Background(), "engram", "mcp", "--tools=agent"); err != nil {
		t.Fatalf("stdioHandshake() error = %v, want nil for a healthy MCP server", err)
	}
}

func TestStdioHandshake_RPCError(t *testing.T) {
	setStdioHelperProcess(t, "rpc-error")

	err := stdioHandshake(context.Background(), "engram", "mcp", "--tools=agent")
	if err == nil || !strings.Contains(err.Error(), "initialize returned error") {
		t.Fatalf("stdioHandshake() error = %v, want initialize error", err)
	}
}

func TestStdioHandshake_GarbageOutput(t *testing.T) {
	setStdioHelperProcess(t, "garbage")

	err := stdioHandshake(context.Background(), "engram", "mcp", "--tools=agent")
	if err == nil || !strings.Contains(err.Error(), "without answering initialize") {
		t.Fatalf("stdioHandshake() error = %v, want exited-without-answering error", err)
	}
}

func TestStdioHandshake_Timeout(t *testing.T) {
	setStdioHelperProcess(t, "silent")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := stdioHandshake(ctx, "engram", "mcp", "--tools=agent")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("stdioHandshake() error = %v, want timeout error", err)
	}
}

func TestStdioHandshake_MissingBinary(t *testing.T) {
	if err := stdioHandshake(context.Background(), "gentle-ai-test-no-such-binary"); err == nil {
		t.Fatal("stdioHandshake() expected error for a missing binary")
	}
}

func TestProbeStdio_NotInstalled(t *testing.T) {
	SetLookPathForTest(t, "", "engram not found")

	if err := ProbeStdio(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("ProbeStdio() error = %v, want ErrNotInstalled", err)
	}
}

// TestProbeStdio_UsesConfiguredCommand verifies the probe target derives from
// the same command resolution inject.go writes into agent configs: the
// resolved engram path with args ["mcp", "--tools=agent"].
func TestProbeStdio_UsesConfiguredCommand(t *testing.T) {
	SetLookPathForTest(t, "/opt/homebrew/bin/engram", "")

	var gotName string
	var gotArgs []string
	orig := stdioHandshakeFn
	stdioHandshakeFn = func(_ context.Context, name string, args ...string) error {
		gotName = name
		gotArgs = args
		return nil
	}
	t.Cleanup(func() { stdioHandshakeFn = orig })

	if err := ProbeStdio(context.Background()); err != nil {
		t.Fatalf("ProbeStdio() error = %v", err)
	}
	if gotName != "/opt/homebrew/bin/engram" {
		t.Errorf("probe command = %q, want resolved engram path", gotName)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "mcp" || gotArgs[1] != "--tools=agent" {
		t.Errorf("probe args = %v, want [mcp --tools=agent]", gotArgs)
	}
}
