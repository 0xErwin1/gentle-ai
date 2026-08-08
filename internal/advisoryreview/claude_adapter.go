package advisoryreview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ClaudeAdapter is the thin invocation wiring for the Claude Code runtime
// (rdd-advisory-transport SKILL.md: "Adapters only invoke the reviewer and
// return raw bytes plus a transport error"). It renders nothing, parses
// nothing, and validates nothing: Prompt and Validate above own every one of
// those responsibilities. Its only job is handing a provider-rendered prompt
// to a real `claude --print` process and returning that process's raw final
// message unmodified.
//
// Review runs claude in a brand-new, empty directory it creates and deletes
// itself, never the caller's repository or any path the caller names, with
// every built-in tool disabled and no settings sources, MCP servers, or
// skills loaded: the prompt text is the reviewer's only legitimate input,
// exactly as the shared advisory contract requires. This is the
// claude_prompt_carried immutable transport boundary
// (internal/cli/review_transport_capability.go) in its headless form -- the
// reviewer needs no shell and no `inspect-candidate` invocation because the
// provider already rendered the frozen candidate into the prompt
// (organic proof: TestRealClaudeReviewerOrdinarySessionAdmitsRawOutput,
// e2e/organicruntime; fake-model transport conformance:
// TestClaudeReviewerTransportInNetworkNone, internal/components/sdd).
type ClaudeAdapter struct {
	// LookPath resolves the claude binary. Overridable so a test can prove the
	// typed-unavailable path without requiring a real installation.
	LookPath func(string) (string, error)
	// Environment supplies the complete child environment. Tests use this seam
	// to isolate Claude configuration and bind the real runtime to a loopback
	// Anthropic-compatible endpoint without changing adapter semantics.
	Environment func() []string
}

// NewClaudeAdapter returns a ClaudeAdapter wired to the real claude binary on
// PATH.
func NewClaudeAdapter() *ClaudeAdapter {
	return &ClaudeAdapter{LookPath: exec.LookPath}
}

// claudeResultEnvelope is the subset of claude's `--output-format json`
// result envelope this transport reads: the raw final message plus the error
// flag. Every other envelope field is session bookkeeping the shared contract
// never consumes.
type claudeResultEnvelope struct {
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// Review invokes claude non-interactively with prompt and returns its raw
// final message. Every returned error is a transport failure -- unavailable
// binary, scratch-directory setup, non-zero exit, an error envelope (missing
// credentials included), or a canceled/expired context -- never a verdict
// about the reviewer's content. Only Validate may render that verdict, and
// only once this adapter has hung up.
func (adapter *ClaudeAdapter) Review(ctx context.Context, prompt string) ([]byte, error) {
	lookPath := adapter.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	binary, err := lookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude advisory transport unavailable: %w", err)
	}

	scratch, err := os.MkdirTemp("", "gentle-ai-claude-advisory-*")
	if err != nil {
		return nil, fmt.Errorf("claude advisory transport unavailable: create scratch directory: %w", err)
	}
	defer os.RemoveAll(scratch)

	command := exec.CommandContext(ctx, binary,
		"--bare",
		"--print",
		"--output-format", "json",
		// --tools "" disables every built-in tool: the prompt-carried
		// evidence is the reviewer's only input, so the reviewer must never
		// reach for a shell, a file read, or the network. The prompt itself
		// arrives on stdin below, never as a positional argument, because
		// --tools is variadic and would swallow a trailing prompt token.
		"--tools", "",
		// dontAsk is the fail-closed backstop: anything that still requests
		// permission is denied silently instead of hanging a headless run.
		"--permission-mode", "dontAsk",
		// Hermetic invocation: no user/project/local setting sources (and
		// with them no CLAUDE.md, hooks, or ambient permissions), no MCP
		// servers beyond the absent --mcp-config, no skills, no persisted
		// session. Auth credentials live under HOME, never in setting
		// sources, so this does not weaken authentication.
		"--setting-sources=",
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--no-chrome",
		"--no-session-persistence",
		"--prompt-suggestions=false",
	)
	// Dir is the empty scratch directory: the OS-level process working
	// directory is the actual boundary this adapter can independently
	// guarantee, so even a hypothetical tool grant could only ever observe an
	// empty directory rather than the caller's live worktree.
	command.Dir = scratch
	// Env is an explicit allowlist, not a security boundary: authority over
	// what a runtime may do lives in Go admission (candidate-causality
	// checks, evidence bounds, the terminal receipt), never in what
	// environment variables happen to reach a transport subprocess. This is
	// quality hardening of the scratch-directory boundary above -- command.Dir
	// already stops claude from reading the caller's repository, and
	// command.Env stops it from inheriting unrelated ambient state (most
	// notably PWD, which without this would still point at the real worktree
	// despite Dir naming the empty scratch directory). PATH is kept so claude
	// resolves its own runtime helpers; HOME so it can read its own ~/.claude
	// auth; ANTHROPIC_* and CLAUDE_CONFIG_DIR are carried only when the
	// caller's own environment already carries them.
	environment := adapter.Environment
	if environment == nil {
		environment = claudeAdvisoryEnvironment
	}
	command.Env = environment()
	command.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		// claude reports many failures on stdout as an is_error result
		// envelope (for example missing credentials, "Not logged in") and
		// exits non-zero, so decode that envelope first: its result text is
		// the readable diagnosis. Fall back to whichever stream is non-empty
		// when no envelope decodes.
		var envelope claudeResultEnvelope
		if jsonErr := json.Unmarshal(stdout.Bytes(), &envelope); jsonErr == nil && strings.TrimSpace(envelope.Result) != "" {
			return nil, fmt.Errorf("claude advisory transport failed: %w: %s", err, strings.TrimSpace(envelope.Result))
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("claude advisory transport failed: %w: %s", err, detail)
	}

	var envelope claudeResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		return nil, fmt.Errorf("claude advisory transport produced no decodable result envelope: %w: %s", err, stdout.String())
	}
	if envelope.IsError {
		return nil, fmt.Errorf("claude advisory transport failed: %s", envelope.Result)
	}
	return []byte(envelope.Result), nil
}

// claudeAdvisoryEnvironment returns the minimal explicit environment allowlist
// for the claude child process: PATH and HOME, plus the ANTHROPIC_* auth
// variables and CLAUDE_CONFIG_DIR when the caller's own environment carries
// them, nothing else. Everything the parent process happens to carry -- PWD
// in particular, which would otherwise still name the real worktree even
// though Dir points claude at an empty scratch directory -- is dropped by
// construction, since exec.Cmd.Env, once non-nil, replaces rather than
// extends the inherited environment.
func claudeAdvisoryEnvironment() []string {
	env := make([]string, 0, 6)
	if path, ok := os.LookupEnv("PATH"); ok {
		env = append(env, "PATH="+path)
	}
	if home, ok := os.LookupEnv("HOME"); ok {
		env = append(env, "HOME="+home)
	} else if home, err := os.UserHomeDir(); err == nil {
		env = append(env, "HOME="+home)
	}
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CONFIG_DIR"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}
