package engram

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v2/internal/agents/claude"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"gopkg.in/yaml.v3"
)

// ErrNotInstalled reports that a persisted relative "engram" command could
// not be resolved. Callers (the doctor) map this to "not probed" instead of a
// transport failure, because binary presence is already covered by the
// tool:engram check.
var ErrNotInstalled = errors.New("engram binary not found on PATH")

// stdioProbeTimeout is the hard deadline for the whole stdio probe: spawning
// the engram MCP process plus the initialize handshake round-trip.
const stdioProbeTimeout = 5 * time.Second

// stdioPostResponseWindow catches frames the server emitted with its
// initialize response before the probe tears the child down.
const stdioPostResponseWindow = 50 * time.Millisecond

// stdioHandshakeFn is a package-level seam over the real handshake (built on
// the execCommandContext precedent) so internal/cli doctor tests can pin the
// probe outcome without spawning a real process.
var stdioHandshakeFn = stdioHandshake

// StdioCommand is one stdio MCP command persisted in an installed agent's
// configuration. Source names the configuration file that supplied it.
type StdioCommand struct {
	Command string
	Args    []string
	Source  string
}

type stdioOutputFrame struct {
	line []byte
	err  error
	done bool
}

// ProbeStdio verifies the exact command and arguments persisted in an agent
// configuration. It deliberately does not resolve a replacement through the
// doctor's PATH: an absolute command in the config must be the process that is
// checked. A missing relative "engram" command maps to ErrNotInstalled; a
// missing absolute or custom command is a broken configured transport.
func ProbeStdio(ctx context.Context, command string, args ...string) error {
	err := stdioHandshakeFn(ctx, command, args...)
	if command == "engram" && errors.Is(err, exec.ErrNotFound) {
		return ErrNotInstalled
	}
	return err
}

// stdioHandshake spawns name with args and performs a minimal MCP initialize
// handshake over the newline-delimited JSON-RPC stdio transport. It succeeds
// only after a valid response and a brief window with no extra stdout frames,
// then tears the process down; it never leaves the child running.
func stdioHandshake(ctx context.Context, name string, args ...string) error {
	probeCtx, cancel := context.WithTimeout(ctx, stdioProbeTimeout)
	defer cancel()

	cmd := execCommandContext(probeCtx, name, args...)
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

	frames := make(chan stdioOutputFrame, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			frame := stdioOutputFrame{line: append([]byte(nil), scanner.Bytes()...)}
			select {
			case frames <- frame:
			case <-probeCtx.Done():
				return
			}
		}
		select {
		case frames <- stdioOutputFrame{err: scanner.Err(), done: true}:
		case <-probeCtx.Done():
		}
	}()

	for {
		select {
		case frame := <-frames:
			if frame.done {
				if frame.err != nil {
					return fmt.Errorf("read engram mcp output: %w", frame.err)
				}
				return errors.New("engram mcp exited without answering initialize")
			}
			if err := validateInitializeResponse(frame.line); err != nil {
				return err
			}

			// A successful response is final only after checking frames already
			// emitted alongside it. stdout is exclusively the MCP protocol stream.
			settle := time.NewTimer(stdioPostResponseWindow)
			defer settle.Stop()
			select {
			case trailing := <-frames:
				if trailing.done {
					if trailing.err != nil {
						return fmt.Errorf("read engram mcp output: %w", trailing.err)
					}
					return nil
				}
				if err := validateInitializeResponse(trailing.line); err != nil {
					return err
				}
				return errors.New("engram mcp wrote an unexpected stdout frame after initialize response")
			case <-settle.C:
				return nil
			case <-probeCtx.Done():
				return handshakeContextError(ctx, probeCtx)
			}
		case <-probeCtx.Done():
			return handshakeContextError(ctx, probeCtx)
		}
	}
}

func handshakeContextError(ctx, probeCtx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("engram mcp initialize handshake stopped by caller context: %w", err)
	}
	if probeCtx.Err() != nil {
		return fmt.Errorf("engram mcp initialize handshake timed out after %s", stdioProbeTimeout)
	}
	return errors.New("engram mcp initialize handshake stopped unexpectedly")
}

func validateInitializeResponse(line []byte) error {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return errors.New("invalid MCP stdout: empty frame")
	}

	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  json.RawMessage `json:"method"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("invalid MCP stdout: malformed JSON-RPC frame: %w", err)
	}
	if response.JSONRPC != "2.0" {
		return errors.New(`invalid MCP stdout: jsonrpc must be "2.0"`)
	}
	if !bytes.Equal(bytes.TrimSpace(response.ID), []byte("1")) {
		return errors.New("invalid MCP stdout: unexpected response id")
	}
	if len(response.Method) > 0 {
		return errors.New("invalid MCP stdout: initialize response must not include method")
	}
	if len(response.Error) > 0 {
		if bytes.Equal(bytes.TrimSpace(response.Error), []byte("null")) {
			return errors.New("invalid MCP stdout: error must be absent or a JSON-RPC error object")
		}
		return fmt.Errorf("engram mcp initialize returned error: %s", response.Error)
	}
	if len(response.Result) == 0 {
		return errors.New("engram mcp initialize returned invalid result: result is required")
	}
	if err := validateInitializeResult(response.Result); err != nil {
		return fmt.Errorf("engram mcp initialize returned invalid result: %w", err)
	}
	return nil
}

func validateInitializeResult(raw json.RawMessage) error {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil || result == nil {
		return errors.New("result must be an object")
	}

	if value, ok := result["protocolVersion"]; !ok || !isNonEmptyJSONString(value) {
		return errors.New("protocolVersion must be a non-empty string")
	}

	capabilities, ok := result["capabilities"]
	if !ok || !isJSONObject(capabilities) {
		return errors.New("capabilities must be an object")
	}

	serverInfo, ok := result["serverInfo"]
	if !ok {
		return errors.New("serverInfo is required")
	}
	var server map[string]json.RawMessage
	if err := json.Unmarshal(serverInfo, &server); err != nil || server == nil {
		return errors.New("serverInfo must be an object")
	}
	if !isNonEmptyJSONString(server["name"]) || !isNonEmptyJSONString(server["version"]) {
		return errors.New("serverInfo.name and serverInfo.version must be non-empty strings")
	}
	return nil
}

func isJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func isNonEmptyJSONString(raw json.RawMessage) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != ""
}

// ReadPersistedStdioCommands returns the unique stdio commands that installed
// agents currently persist for Engram. The doctor probes these values verbatim,
// rather than deriving a fresh command from its own environment.
func ReadPersistedStdioCommands(homeDir string, installedAgentIDs []string) ([]StdioCommand, error) {
	commands := make([]StdioCommand, 0, len(installedAgentIDs))
	seenPaths := make(map[string]struct{}, len(installedAgentIDs))
	seenCommands := make(map[string]struct{}, len(installedAgentIDs))

	for _, agentID := range installedAgentIDs {
		adapter, err := agents.NewAdapter(model.AgentID(agentID))
		if err != nil || !adapter.SupportsMCP() {
			continue
		}

		paths := []string{adapter.MCPConfigPath(homeDir, "engram")}
		if adapter.Agent() == model.AgentClaudeCode {
			// Claude Code reads user-scope MCP entries from ~/.claude.json.
			paths = []string{claude.UserConfigPath(homeDir)}
		}

		for _, path := range paths {
			if path == "" {
				continue
			}
			if _, seen := seenPaths[path]; seen {
				continue
			}
			seenPaths[path] = struct{}{}

			command, found, err := readPersistedStdioCommand(path, adapter.Agent())
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			command.Source = path
			key := command.Command + "\x00" + strings.Join(command.Args, "\x00")
			if _, seen := seenCommands[key]; seen {
				continue
			}
			seenCommands[key] = struct{}{}
			commands = append(commands, command)
		}
	}

	return commands, nil
}

func readPersistedStdioCommand(path string, agentID model.AgentID) (StdioCommand, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return StdioCommand{}, false, nil
		}
		return StdioCommand{}, false, fmt.Errorf("read persisted engram MCP configuration %q: %w", path, err)
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".toml":
		command, found, err := readTOMLEngramCommand(string(raw))
		if err != nil {
			return StdioCommand{}, false, fmt.Errorf("parse persisted engram MCP configuration %q: %w", path, err)
		}
		return command, found, nil
	case ".yaml", ".yml":
		command, found, err := readYAMLEngramCommand(raw)
		if err != nil {
			return StdioCommand{}, false, fmt.Errorf("parse persisted engram MCP configuration %q: %w", path, err)
		}
		return command, found, nil
	default:
		root, err := filemerge.UnmarshalJSONObject(raw)
		if err != nil {
			return StdioCommand{}, false, fmt.Errorf("parse persisted engram MCP configuration %q: %w", path, err)
		}
		server, found := engramServerFromJSON(root, agentID)
		if !found {
			return StdioCommand{}, false, nil
		}
		command, err := stdioCommandFromServer(server)
		if err != nil {
			return StdioCommand{}, false, fmt.Errorf("parse persisted engram MCP configuration %q: %w", path, err)
		}
		return command, true, nil
	}
}

func engramServerFromJSON(root map[string]any, agentID model.AgentID) (map[string]any, bool) {
	if _, directCommand := root["command"]; directCommand {
		return root, true
	}

	var server any
	switch agentID {
	case model.AgentOpenCode, model.AgentKilocode:
		server = nestedJSONObject(root["mcp"])["engram"]
	case model.AgentOpenClaw:
		server = nestedJSONObject(nestedJSONObject(root["mcp"])["servers"])["engram"]
	case model.AgentVSCodeCopilot:
		server = nestedJSONObject(root["servers"])["engram"]
	default:
		server = nestedJSONObject(root["mcpServers"])["engram"]
	}

	entry, ok := server.(map[string]any)
	return entry, ok
}

func nestedJSONObject(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func readTOMLEngramCommand(raw string) (StdioCommand, bool, error) {
	inEngramTable := false
	var commandValue, argsValue string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inEngramTable {
				break
			}
			inEngramTable = trimmed == "[mcp_servers.engram]"
			continue
		}
		if !inEngramTable {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "command":
			commandValue = strings.TrimSpace(value)
		case "args":
			argsValue = strings.TrimSpace(value)
		}
	}
	if !inEngramTable {
		return StdioCommand{}, false, nil
	}

	command, err := strconv.Unquote(commandValue)
	if err != nil || strings.TrimSpace(command) == "" {
		return StdioCommand{}, true, errors.New("command must be a non-empty quoted string")
	}
	args := []string(nil)
	if argsValue != "" && json.Unmarshal([]byte(argsValue), &args) != nil {
		return StdioCommand{}, true, errors.New("args must be a string array")
	}
	return StdioCommand{Command: command, Args: args}, true, nil
}

func readYAMLEngramCommand(raw []byte) (StdioCommand, bool, error) {
	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return StdioCommand{}, false, err
	}
	server := nestedJSONObject(nestedJSONObject(root["mcp_servers"])["engram"])
	if server == nil {
		return StdioCommand{}, false, nil
	}
	command, err := stdioCommandFromServer(server)
	if err != nil {
		return StdioCommand{}, false, err
	}
	return command, true, nil
}

func stdioCommandFromServer(server map[string]any) (StdioCommand, error) {
	commandValue, ok := server["command"]
	if !ok {
		return StdioCommand{}, errors.New("command is required")
	}

	command, commandArgs, commandIsArray, err := splitCommand(commandValue)
	if err != nil {
		return StdioCommand{}, err
	}
	argsValue, hasArgs := server["args"]
	if commandIsArray && hasArgs {
		return StdioCommand{}, errors.New("command array must not also define args")
	}
	if commandIsArray {
		return StdioCommand{Command: command, Args: commandArgs}, nil
	}
	if !hasArgs {
		return StdioCommand{Command: command}, nil
	}
	args, err := stringSlice(argsValue)
	if err != nil {
		return StdioCommand{}, fmt.Errorf("args: %w", err)
	}
	return StdioCommand{Command: command, Args: args}, nil
}

func splitCommand(value any) (string, []string, bool, error) {
	if command, ok := value.(string); ok {
		if strings.TrimSpace(command) == "" {
			return "", nil, false, errors.New("command must be a non-empty string")
		}
		return command, nil, false, nil
	}

	values, err := stringSlice(value)
	if err != nil || len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return "", nil, false, errors.New("command must be a non-empty string or string array")
	}
	return values[0], values[1:], true, nil
}

func stringSlice(value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, errors.New("must be a string array")
	}
	result := make([]string, len(values))
	for i, value := range values {
		item, ok := value.(string)
		if !ok {
			return nil, errors.New("must be a string array")
		}
		result[i] = item
	}
	return result, nil
}
