package engram

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	case "healthy", "rpc-error", "null-result", "scalar-result", "missing-protocol-version", "invalid-capabilities", "missing-server-info", "invalid-server-info", "missing-jsonrpc", "wrong-jsonrpc", "null-jsonrpc", "request-shaped-result", "missing-result", "malformed-result", "early-exit", "stray-stdout-then-healthy", "mismatched-id-then-healthy", "trailing-stdout-after-healthy":
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
		if mode == "early-exit" {
			return
		}
		healthyResponse := fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"protocolVersion\":\"2024-11-05\",\"capabilities\":{},\"serverInfo\":{\"name\":\"fake-engram\",\"version\":\"0\"}}}", req.ID.String())
		switch mode {
		case "missing-jsonrpc":
			fmt.Printf("{\"id\":%s,\"result\":{\"protocolVersion\":\"2024-11-05\",\"capabilities\":{},\"serverInfo\":{\"name\":\"fake-engram\",\"version\":\"0\"}}}\n", req.ID.String())
			return
		case "wrong-jsonrpc":
			fmt.Printf("{\"jsonrpc\":\"1.0\",\"id\":%s,\"result\":{\"protocolVersion\":\"2024-11-05\",\"capabilities\":{},\"serverInfo\":{\"name\":\"fake-engram\",\"version\":\"0\"}}}\n", req.ID.String())
			return
		case "null-jsonrpc":
			fmt.Printf("{\"jsonrpc\":null,\"id\":%s,\"result\":{\"protocolVersion\":\"2024-11-05\",\"capabilities\":{},\"serverInfo\":{\"name\":\"fake-engram\",\"version\":\"0\"}}}\n", req.ID.String())
			return
		case "request-shaped-result":
			fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":%s,\"method\":\"notifications/test\",\"result\":{\"protocolVersion\":\"2024-11-05\",\"capabilities\":{},\"serverInfo\":{\"name\":\"fake-engram\",\"version\":\"0\"}}}\n", req.ID.String())
			return
		case "missing-result":
			fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":%s}\n", req.ID.String())
			return
		case "malformed-result":
			fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":\n", req.ID.String())
			return
		case "stray-stdout-then-healthy":
			fmt.Println("engram mcp starting")
			fmt.Println(healthyResponse)
			return
		case "mismatched-id-then-healthy":
			fmt.Println(strings.Replace(healthyResponse, `"id":1`, `"id":2`, 1))
			fmt.Println(healthyResponse)
			return
		case "trailing-stdout-after-healthy":
			fmt.Println(healthyResponse)
			fmt.Println("engram mcp stopped")
			return
		}
		result := map[string]string{
			"healthy":                  `{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake-engram","version":"0"}}`,
			"null-result":              `null`,
			"scalar-result":            `"not-an-object"`,
			"missing-protocol-version": `{"capabilities":{},"serverInfo":{"name":"fake-engram","version":"0"}}`,
			"invalid-capabilities":     `{"protocolVersion":"2024-11-05","capabilities":[],"serverInfo":{"name":"fake-engram","version":"0"}}`,
			"missing-server-info":      `{"protocolVersion":"2024-11-05","capabilities":{}}`,
			"invalid-server-info":      `{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"","version":0}}`,
		}[mode]
		if mode == "healthy" {
			// MCP diagnostics are not protocol frames and therefore belong on stderr.
			fmt.Fprintln(os.Stderr, "engram mcp starting")
		}
		fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\n", req.ID.String(), result)
		if mode == "healthy" {
			// Stay alive until the probe tears the process down.
			time.Sleep(30 * time.Second)
		}
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

func TestStdioHandshake_RejectsInvalidInitializeResult(t *testing.T) {
	tests := []struct {
		name string
		mode string
	}{
		{name: "null", mode: "null-result"},
		{name: "non-object", mode: "scalar-result"},
		{name: "missing protocol version", mode: "missing-protocol-version"},
		{name: "invalid capabilities", mode: "invalid-capabilities"},
		{name: "missing server info", mode: "missing-server-info"},
		{name: "invalid server info", mode: "invalid-server-info"},
		{name: "missing result", mode: "missing-result"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setStdioHelperProcess(t, tt.mode)

			err := stdioHandshake(context.Background(), "engram", "mcp", "--tools=agent")
			if err == nil || !strings.Contains(err.Error(), "initialize returned invalid result") {
				t.Fatalf("stdioHandshake() error = %v, want invalid initialize result", err)
			}
		})
	}
}

func TestStdioHandshake_EarlyExit(t *testing.T) {
	setStdioHelperProcess(t, "early-exit")

	err := stdioHandshake(context.Background(), "engram", "mcp", "--tools=agent")
	if err == nil || !strings.Contains(err.Error(), "exited without answering initialize") {
		t.Fatalf("stdioHandshake() error = %v, want early-exit error", err)
	}
}

func TestStdioHandshake_GarbageOutput(t *testing.T) {
	setStdioHelperProcess(t, "garbage")

	err := stdioHandshake(context.Background(), "engram", "mcp", "--tools=agent")
	if err == nil || !strings.Contains(err.Error(), "invalid MCP stdout") {
		t.Fatalf("stdioHandshake() error = %v, want invalid stdout error", err)
	}
}

func TestStdioHandshake_RejectsInvalidResponseEnvelope(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want string
	}{
		{name: "missing jsonrpc", mode: "missing-jsonrpc", want: "jsonrpc must be \"2.0\""},
		{name: "wrong jsonrpc", mode: "wrong-jsonrpc", want: "jsonrpc must be \"2.0\""},
		{name: "null jsonrpc", mode: "null-jsonrpc", want: "jsonrpc must be \"2.0\""},
		{name: "request-shaped result", mode: "request-shaped-result", want: "must not include method"},
		{name: "malformed result", mode: "malformed-result", want: "invalid MCP stdout"},
		{name: "stray stdout before healthy response", mode: "stray-stdout-then-healthy", want: "invalid MCP stdout"},
		{name: "mismatched id before healthy response", mode: "mismatched-id-then-healthy", want: "unexpected response id"},
		{name: "stray stdout after healthy response", mode: "trailing-stdout-after-healthy", want: "invalid MCP stdout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setStdioHelperProcess(t, tt.mode)

			err := stdioHandshake(context.Background(), "engram", "mcp", "--tools=agent")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("stdioHandshake() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestStdioHandshake_PreservesEarlierDeadline(t *testing.T) {
	setStdioHelperProcess(t, "silent")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := stdioHandshake(ctx, "engram", "mcp", "--tools=agent")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stdioHandshake() error = %v, want earlier caller deadline", err)
	}
	if strings.Contains(err.Error(), "timed out after 5s") {
		t.Fatalf("stdioHandshake() error = %v, must not misreport the caller deadline as the internal timeout", err)
	}
}

func TestStdioHandshake_PreservesCallerCancellation(t *testing.T) {
	setStdioHelperProcess(t, "silent")

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	defer cancel()

	err := stdioHandshake(ctx, "engram", "mcp", "--tools=agent")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stdioHandshake() error = %v, want caller cancellation", err)
	}
	if strings.Contains(err.Error(), "timed out after 5s") {
		t.Fatalf("stdioHandshake() error = %v, must not misreport caller cancellation as the internal timeout", err)
	}
}

func TestStdioHandshake_MissingBinary(t *testing.T) {
	if err := stdioHandshake(context.Background(), "gentle-ai-test-no-such-binary"); err == nil {
		t.Fatal("stdioHandshake() expected error for a missing binary")
	}
}

func TestProbeStdio_NotInstalled(t *testing.T) {
	orig := stdioHandshakeFn
	stdioHandshakeFn = func(context.Context, string, ...string) error { return exec.ErrNotFound }
	t.Cleanup(func() { stdioHandshakeFn = orig })

	if err := ProbeStdio(context.Background(), "engram", "mcp", "--tools=agent"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("ProbeStdio() error = %v, want ErrNotInstalled", err)
	}
}

func TestProbeStdio_MissingAbsoluteConfiguredCommandFails(t *testing.T) {
	orig := stdioHandshakeFn
	stdioHandshakeFn = func(context.Context, string, ...string) error { return exec.ErrNotFound }
	t.Cleanup(func() { stdioHandshakeFn = orig })

	err := ProbeStdio(context.Background(), "/configured/engram", "mcp", "--tools=agent")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("ProbeStdio() error = %v, want missing configured command", err)
	}
	if errors.Is(err, ErrNotInstalled) {
		t.Fatalf("ProbeStdio() error = %v, must not hide a missing absolute configured command", err)
	}
}

func TestProbeStdio_UsesExactPersistedCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	orig := stdioHandshakeFn
	stdioHandshakeFn = func(_ context.Context, name string, args ...string) error {
		gotName = name
		gotArgs = args
		return nil
	}
	t.Cleanup(func() { stdioHandshakeFn = orig })

	if err := ProbeStdio(context.Background(), "/opt/gentle-engram/bin/engram", "mcp", "--tools=custom"); err != nil {
		t.Fatalf("ProbeStdio() error = %v", err)
	}
	if gotName != "/opt/gentle-engram/bin/engram" {
		t.Errorf("probe command = %q, want persisted command", gotName)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "mcp" || gotArgs[1] != "--tools=custom" {
		t.Errorf("probe args = %v, want persisted arguments", gotArgs)
	}
}

func TestReadPersistedStdioCommands_PreservesConfiguredCommandAndArguments(t *testing.T) {
	homeDir := t.TempDir()
	cases := []struct {
		name    string
		agentID string
		path    string
		content string
		command string
		args    []string
	}{
		{
			name:    "Claude user configuration",
			agentID: "claude-code",
			path:    ".claude.json",
			content: `{"mcpServers":{"engram":{"command":"/configured/claude-engram","args":["mcp","--tools=claude"]}}}`,
			command: "/configured/claude-engram",
			args:    []string{"mcp", "--tools=claude"},
		},
		{
			name:    "OpenCode command array",
			agentID: "opencode",
			path:    ".config/opencode/opencode.json",
			content: `{"mcp":{"engram":{"command":["/configured/opencode-engram","mcp","--tools=opencode"],"type":"local"}}}`,
			command: "/configured/opencode-engram",
			args:    []string{"mcp", "--tools=opencode"},
		},
		{
			name:    "Codex TOML configuration",
			agentID: "codex",
			path:    ".codex/config.toml",
			content: "[mcp_servers.engram]\ncommand = \"/configured/codex-engram\"\nargs = [\"mcp\", \"--tools=codex\"]\n",
			command: "/configured/codex-engram",
			args:    []string{"mcp", "--tools=codex"},
		},
		{
			name:    "Hermes YAML configuration",
			agentID: "hermes",
			path:    ".hermes/config.yaml",
			content: "mcp_servers:\n  engram:\n    command: /configured/hermes-engram\n    args:\n      - mcp\n      - --tools=hermes\n",
			command: "/configured/hermes-engram",
			args:    []string{"mcp", "--tools=hermes"},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(homeDir, tt.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			commands, err := ReadPersistedStdioCommands(homeDir, []string{tt.agentID})
			if err != nil {
				t.Fatalf("ReadPersistedStdioCommands() error = %v", err)
			}
			if len(commands) != 1 {
				t.Fatalf("commands = %v, want one configured command", commands)
			}
			if commands[0].Command != tt.command {
				t.Errorf("command = %q, want %q", commands[0].Command, tt.command)
			}
			if strings.Join(commands[0].Args, "\x00") != strings.Join(tt.args, "\x00") {
				t.Errorf("args = %v, want %v", commands[0].Args, tt.args)
			}
			if commands[0].Source != path {
				t.Errorf("source = %q, want %q", commands[0].Source, path)
			}
		})
	}
}
