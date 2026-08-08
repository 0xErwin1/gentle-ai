package advisoryreview

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestClaudeAdapterReturnsTypedUnavailableWhenBinaryMissing(t *testing.T) {
	adapter := &ClaudeAdapter{LookPath: func(string) (string, error) {
		return "", errors.New("no claude on PATH")
	}}
	raw, err := adapter.Review(context.Background(), "irrelevant prompt")
	if err == nil {
		t.Fatalf("Review() = %q, nil, want a typed unavailable transport error", raw)
	}
	if !strings.Contains(err.Error(), "claude advisory transport unavailable") {
		t.Fatalf("Review() error = %v, want a claude advisory transport unavailable message", err)
	}
	if raw != nil {
		t.Fatalf("Review() returned bytes alongside a transport error: %q", raw)
	}
}

// fakeClaudeScript writes a POSIX shell script standing in for the real claude
// binary. It records the directory it was launched from, that directory's
// entire listing AT LAUNCH TIME (before the adapter's own deferred cleanup can
// remove it), every argument it received, and the complete prompt it received
// on stdin, then writes the fixed `--output-format json` result envelope the
// adapter decodes.
func fakeClaudeScript(t *testing.T, fixedOutput string) (path string, invocationLog func() (dir string, entriesAtLaunch []string, args []string, prompt string)) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake claude script targets POSIX shells")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "invocation.log")
	entriesPath := filepath.Join(dir, "entries.log")
	promptPath := filepath.Join(dir, "prompt.log")
	script := filepath.Join(dir, "claude")
	envelope := `{"is_error":false,"result":` + strconv.Quote(fixedOutput) + `}`
	contents := "#!/bin/sh\n" +
		"pwd > " + shellQuote(logPath) + "\n" +
		"printf '%s\\n' \"$@\" >> " + shellQuote(logPath) + "\n" +
		"ls -A . > " + shellQuote(entriesPath) + "\n" +
		"cat > " + shellQuote(promptPath) + "\n" +
		"printf '%s' " + shellQuote(envelope) + "\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, func() (string, []string, []string, string) {
		payload, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read fake claude invocation log: %v", err)
		}
		lines := strings.Split(strings.TrimRight(string(payload), "\n"), "\n")
		var invocationDir string
		var args []string
		if len(lines) > 0 {
			invocationDir, args = lines[0], lines[1:]
		}
		entriesPayload, err := os.ReadFile(entriesPath)
		if err != nil {
			t.Fatalf("read fake claude directory listing: %v", err)
		}
		var entries []string
		for _, entry := range strings.Split(strings.TrimRight(string(entriesPayload), "\n"), "\n") {
			if entry != "" {
				entries = append(entries, entry)
			}
		}
		promptPayload, err := os.ReadFile(promptPath)
		if err != nil {
			t.Fatalf("read fake claude prompt capture: %v", err)
		}
		return invocationDir, entries, args, string(promptPayload)
	}
}

// TestClaudeAdapterInvokesNonInteractivelyInAnEmptyScratchDirectory proves the
// adapter's exact invocation shape without depending on network access or a
// real Claude account: it launches its own fake "claude" binary in place of
// the real one and asserts every argument the shared advisory contract
// requires (print mode, json envelope, every built-in tool disabled, no
// setting sources, no MCP servers, no skills, no persisted session, a working
// directory the adapter itself created and never named by the caller) and
// that the prompt travels on stdin -- never as a positional argument the
// variadic --tools flag could swallow.
func TestClaudeAdapterInvokesNonInteractivelyInAnEmptyScratchDirectory(t *testing.T) {
	script, invocation := fakeClaudeScript(t, `{"ok":true}`)
	adapter := &ClaudeAdapter{LookPath: func(name string) (string, error) {
		if name != "claude" {
			t.Fatalf("LookPath(%q), want LookPath(\"claude\")", name)
		}
		return script, nil
	}}

	raw, err := adapter.Review(context.Background(), "the canonical advisory prompt")
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("Review() = %q, want the fake claude's fixed raw output unmodified", raw)
	}

	dir, entriesAtLaunch, args, prompt := invocation()
	if dir == "" {
		t.Fatal("fake claude recorded no working directory")
	}
	// entriesAtLaunch is the scratch directory's listing captured by the fake
	// binary itself, at the moment claude actually ran and strictly before the
	// adapter's own deferred cleanup deletes it -- an empty directory has
	// nothing a reviewer could read beyond the supplied prompt.
	if len(entriesAtLaunch) != 0 {
		t.Fatalf("scratch directory %s was not empty at claude launch: %v", dir, entriesAtLaunch)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("Review() did not remove its scratch directory %s: err=%v", dir, err)
	}

	if prompt != "the canonical advisory prompt" {
		t.Fatalf("claude received prompt %q on stdin, want the canonical advisory prompt unmodified", prompt)
	}
	joined := strings.Join(args, "\n")
	for _, want := range []string{"--bare", "--print", "--output-format", "json", "--tools", "--permission-mode", "dontAsk", "--setting-sources=", "--strict-mcp-config", "--disable-slash-commands", "--no-chrome", "--no-session-persistence", "--prompt-suggestions=false"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("claude invocation args = %q, missing %q", args, want)
		}
	}
	// --tools must carry exactly the empty string (disable every built-in
	// tool): any other value would re-grant the shell or read tools the
	// prompt-carried transport exists to remove.
	for position, arg := range args {
		if arg == "--tools" {
			if position+1 >= len(args) || args[position+1] != "" {
				t.Fatalf("claude invocation args = %q, --tools must be followed by the empty string", args)
			}
			return
		}
	}
	t.Fatalf("claude invocation args = %q, missing --tools", args)
}

func TestClaudeAdapterReturnsTransportErrorOnNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake claude script targets POSIX shells")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho boom failure >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := &ClaudeAdapter{LookPath: func(string) (string, error) { return script, nil }}
	raw, err := adapter.Review(context.Background(), "prompt")
	if err == nil {
		t.Fatalf("Review() = %q, nil, want a transport failure", raw)
	}
	if !strings.Contains(err.Error(), "claude advisory transport failed") || !strings.Contains(err.Error(), "boom failure") {
		t.Fatalf("Review() error = %v, want the transport failure to carry the process's stderr", err)
	}
}

// TestClaudeAdapterReturnsTransportErrorOnEnvelopeError proves the envelope's
// own error flag is a transport failure, never reviewer content: claude
// reports a failed run (for example missing credentials, "Not logged in")
// with exit code zero and is_error=true, and none of that text may reach
// native admission as if it were a result.
func TestClaudeAdapterReturnsTransportErrorOnEnvelopeError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake claude script targets POSIX shells")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	contents := "#!/bin/sh\ncat > /dev/null\nprintf '%s' '{\"is_error\":true,\"result\":\"Not logged in\"}'\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := &ClaudeAdapter{LookPath: func(string) (string, error) { return script, nil }}
	raw, err := adapter.Review(context.Background(), "prompt")
	if err == nil {
		t.Fatalf("Review() = %q, nil, want a transport failure for an is_error envelope", raw)
	}
	if !strings.Contains(err.Error(), "claude advisory transport failed") || !strings.Contains(err.Error(), "Not logged in") {
		t.Fatalf("Review() error = %v, want the transport failure to carry the envelope's error text", err)
	}
}

// buildEnvDumpingFakeClaude compiles a tiny real binary standing in for
// claude, deliberately NOT a POSIX shell script like fakeClaudeScript above:
// `sh` (dash on Debian/Ubuntu, this repo's CI base) unconditionally
// overwrites PWD with its own getcwd() result on every invocation, even when
// PWD is entirely absent from the environment it was launched with, which
// would silently defeat an assertion that PWD is absent. A plain Go binary
// just reports os.Environ() as received, with nothing rewritten in between.
// The dump path is baked into the generated source as a literal, exactly like
// fakeClaudeScript bakes its own log paths into the shell script text, so no
// runtime coordination channel (env var or extra argument) is needed.
func buildEnvDumpingFakeClaude(t *testing.T, dumpPath string) (path string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	program := "package main\n\n" +
		"import (\n\t\"fmt\"\n\t\"io\"\n\t\"os\"\n\t\"strings\"\n)\n\n" +
		"func main() {\n" +
		"\t_ = os.WriteFile(" + strconv.Quote(dumpPath) + ", []byte(strings.Join(os.Environ(), \"\\n\")), 0o644)\n" +
		"\t_, _ = io.Copy(io.Discard, os.Stdin)\n" +
		"\tfmt.Printf(`{\"is_error\":false,\"result\":\"{\\\"ok\\\":true}\"}`)\n" +
		"}\n"
	if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
		t.Fatalf("write fake claude helper source: %v", err)
	}
	binaryName := "claude"
	if runtime.GOOS == "windows" {
		binaryName = "claude.exe"
	}
	binary := filepath.Join(dir, binaryName)
	build := exec.Command("go", "build", "-o", binary, source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake claude helper: %v\n%s", err, output)
	}
	return binary
}

// TestClaudeAdapterScrubsChildEnvironmentToMinimalAllowlist proves the
// hardened boundary this adapter owns in addition to Dir: the child process
// receives an explicit allowlist (PATH, HOME, and the ANTHROPIC_* auth
// variables only when the caller's own environment carries them), never the
// full ambient environment Review()'s own process happens to carry. PWD is
// the motivating case -- Dir points claude at the empty scratch directory,
// but without an explicit Env, the OS-level PWD the child inherits still
// names wherever this test process itself started, which for a real reviewer
// invocation is the caller's live worktree, not the empty scratch directory
// the boundary comment above promises. A sentinel variable set on the test
// process itself proves the exclusion is general, not special-cased to PWD
// alone.
func TestClaudeAdapterScrubsChildEnvironmentToMinimalAllowlist(t *testing.T) {
	dumpDir := t.TempDir()
	dumpPath := filepath.Join(dumpDir, "env-dump.log")
	binary := buildEnvDumpingFakeClaude(t, dumpPath)

	t.Setenv("PWD", "/leaked/real/worktree/should-never-reach-claude")
	const sentinelName = "GENTLE_AI_CLAUDE_ADAPTER_TEST_SENTINEL"
	t.Setenv(sentinelName, "sentinel-value-must-not-leak")
	t.Setenv("ANTHROPIC_API_KEY", "gentle-ai-claude-adapter-test-key")

	adapter := &ClaudeAdapter{LookPath: func(string) (string, error) { return binary, nil }}
	raw, err := adapter.Review(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("Review() = %q, want the fake claude's fixed raw output unmodified", raw)
	}

	dumped, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read fake claude environment dump: %v", err)
	}
	entries := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(dumped), "\n"), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("malformed dumped environment entry: %q", line)
		}
		entries[key] = value
	}

	if _, present := entries["PWD"]; present {
		t.Fatalf("claude child environment carried PWD=%q, want it absent", entries["PWD"])
	}
	if _, present := entries[sentinelName]; present {
		t.Fatalf("claude child environment carried the test sentinel %s, want it absent", sentinelName)
	}
	if value, present := entries["PATH"]; !present || value == "" {
		t.Fatalf("claude child environment PATH = %q, present=%v, want the allowlisted PATH", value, present)
	}
	if value, present := entries["HOME"]; !present || value == "" {
		t.Fatalf("claude child environment HOME = %q, present=%v, want the allowlisted HOME", value, present)
	}
	if value, present := entries["ANTHROPIC_API_KEY"]; !present || value != "gentle-ai-claude-adapter-test-key" {
		t.Fatalf("claude child environment ANTHROPIC_API_KEY = %q, present=%v, want the caller's auth variable carried through", value, present)
	}
	// Windows processes cannot start without SYSTEMROOT, so os/exec appends it
	// to any Env that omits it. It is the platform's floor, not inherited
	// ambient state: every variable the scrub exists to keep out is still
	// asserted absent above (mirrors CodexAdapter, community report #2675).
	if runtime.GOOS == "windows" {
		for key := range entries {
			if strings.EqualFold(key, "SYSTEMROOT") {
				delete(entries, key)
			}
		}
	}
	if len(entries) != 3 {
		t.Fatalf("claude child environment = %v, want exactly PATH, HOME, and ANTHROPIC_API_KEY", entries)
	}
}

func TestClaudeAdapterReturnsTransportErrorWhenContextExpires(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake claude script targets POSIX shells")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat > /dev/null\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := &ClaudeAdapter{LookPath: func(string) (string, error) { return script, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw, err := adapter.Review(ctx, "prompt")
	if err == nil {
		t.Fatalf("Review() = %q, nil, want a transport failure for an already-canceled context", raw)
	}
}
