package engram

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrNotInstalled reports that the stdio probe could not run because the
// engram binary is not resolvable on PATH. Callers (the doctor) map this to
// "not probed" instead of a transport failure, because binary presence is
// already covered by the tool:engram check.
var ErrNotInstalled = errors.New("engram binary not found on PATH")

// stdioProbeTimeout is the hard deadline for the whole stdio probe: spawning
// the engram MCP process plus the initialize handshake round-trip.
const stdioProbeTimeout = 5 * time.Second

// stdioHandshakeFn is a package-level seam over the real handshake (built on
// the execCommandContext precedent) so internal/cli doctor tests can pin the
// probe outcome without spawning a real process.
var stdioHandshakeFn = stdioHandshake

// ProbeStdio verifies the Engram transport gentle-ai actually configures.
// Inject only ever writes a stdio MCP server (`engram mcp --tools=agent`,
// see engramServerJSONWithCmd); there is no code path that configures an
// HTTP transport. So the health probe spawns that same command and performs
// a JSON-RPC initialize handshake over stdin/stdout (#2078). It returns
// ErrNotInstalled when the binary is absent, nil when the handshake
// succeeds, and a descriptive error otherwise.
func ProbeStdio(ctx context.Context) error {
	if _, err := EngramLookPath("engram"); err != nil {
		return ErrNotInstalled
	}
	cmd, _ := resolveEngramCommand()
	return stdioHandshakeFn(ctx, cmd, "mcp", "--tools=agent")
}

// stdioHandshake spawns name with args and performs a minimal MCP initialize
// handshake over the newline-delimited JSON-RPC stdio transport. It succeeds
// as soon as the server answers the initialize request with a result, then
// tears the process down; it never leaves the child running.
func stdioHandshake(ctx context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, stdioProbeTimeout)
	defer cancel()

	cmd := execCommandContext(ctx, name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open engram mcp stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open engram mcp stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	request := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"gentle-ai-doctor","version":"0"}}}` + "\n"
	if _, err := io.WriteString(stdin, request); err != nil {
		return fmt.Errorf("write engram mcp initialize request: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var response struct {
			ID     json.Number     `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(line, &response) != nil || response.ID.String() != "1" {
			continue
		}
		if len(response.Error) > 0 {
			return fmt.Errorf("engram mcp initialize returned error: %s", response.Error)
		}
		if len(response.Result) > 0 {
			return nil
		}
	}
	if ctx.Err() != nil {
		return fmt.Errorf("engram mcp initialize handshake timed out after %s", stdioProbeTimeout)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read engram mcp output: %w", err)
	}
	return errors.New("engram mcp exited without answering initialize")
}
