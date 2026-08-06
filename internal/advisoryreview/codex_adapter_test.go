package advisoryreview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCodexAdapterReturnsTypedUnavailableWhenBinaryMissing(t *testing.T) {
	adapter := &CodexAdapter{LookPath: func(string) (string, error) {
		return "", errors.New("no codex on PATH")
	}}
	raw, err := adapter.Review(context.Background(), "irrelevant prompt")
	if err == nil {
		t.Fatalf("Review() = %q, nil, want a typed unavailable transport error", raw)
	}
	if !strings.Contains(err.Error(), "codex advisory transport unavailable") {
		t.Fatalf("Review() error = %v, want a codex advisory transport unavailable message", err)
	}
	if raw != nil {
		t.Fatalf("Review() returned bytes alongside a transport error: %q", raw)
	}
}

// fakeCodexScript writes a POSIX shell script standing in for the real codex
// binary. It records the directory it was launched from, that directory's
// entire listing AT LAUNCH TIME (before the adapter's own deferred cleanup
// can remove it), and every argument it received, then, matching the real
// CLI's --output-last-message contract, writes fixedOutput to the path
// following that flag.
func fakeCodexScript(t *testing.T, fixedOutput string) (path string, invocationLog func() (dir string, entriesAtLaunch []string, args []string)) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake codex script targets POSIX shells")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "invocation.log")
	entriesPath := filepath.Join(dir, "entries.log")
	script := filepath.Join(dir, "codex")
	contents := "#!/bin/sh\n" +
		"pwd > " + shellQuote(logPath) + "\n" +
		"printf '%s\\n' \"$@\" >> " + shellQuote(logPath) + "\n" +
		"ls -A . > " + shellQuote(entriesPath) + "\n" +
		"output=\"\"\n" +
		"while [ \"$#\" -gt 0 ]; do\n" +
		"  if [ \"$1\" = \"--output-last-message\" ]; then\n" +
		"    output=\"$2\"\n" +
		"  fi\n" +
		"  shift\n" +
		"done\n" +
		"printf '%s' " + shellQuote(fixedOutput) + " > \"$output\"\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, func() (string, []string, []string) {
		payload, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read fake codex invocation log: %v", err)
		}
		lines := strings.Split(strings.TrimRight(string(payload), "\n"), "\n")
		var invocationDir string
		var args []string
		if len(lines) > 0 {
			invocationDir, args = lines[0], lines[1:]
		}
		entriesPayload, err := os.ReadFile(entriesPath)
		if err != nil {
			t.Fatalf("read fake codex directory listing: %v", err)
		}
		var entries []string
		for _, entry := range strings.Split(strings.TrimRight(string(entriesPayload), "\n"), "\n") {
			if entry != "" {
				entries = append(entries, entry)
			}
		}
		return invocationDir, entries, args
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// TestCodexAdapterInvokesNonInteractivelyInAnEmptyScratchDirectory proves the
// adapter's exact invocation shape without depending on network access or a
// real Codex account: it launches its own fake "codex" binary in place of the
// real one and asserts every argument the shared advisory contract requires
// (no-git-repo-check, ignore-user-config, read-only sandbox, a working
// directory the adapter itself created and never named by the caller) and
// that only that directory -- never the process's own working directory --
// is where codex was told to run.
func TestCodexAdapterInvokesNonInteractivelyInAnEmptyScratchDirectory(t *testing.T) {
	script, invocation := fakeCodexScript(t, `{"ok":true}`)
	adapter := &CodexAdapter{LookPath: func(name string) (string, error) {
		if name != "codex" {
			t.Fatalf("LookPath(%q), want LookPath(\"codex\")", name)
		}
		return script, nil
	}}

	raw, err := adapter.Review(context.Background(), "the canonical advisory prompt")
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("Review() = %q, want the fake codex's fixed raw output unmodified", raw)
	}

	dir, entriesAtLaunch, args := invocation()
	if dir == "" {
		t.Fatal("fake codex recorded no working directory")
	}
	// entriesAtLaunch is the scratch directory's listing captured by the fake
	// binary itself, at the moment codex actually ran and strictly before the
	// adapter's own deferred cleanup deletes it -- an empty directory has
	// nothing a reviewer could read beyond the supplied prompt.
	if len(entriesAtLaunch) != 0 {
		t.Fatalf("scratch directory %s was not empty at codex launch: %v", dir, entriesAtLaunch)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("Review() did not remove its scratch directory %s: err=%v", dir, err)
	}

	joined := strings.Join(args, "\n")
	for _, want := range []string{"exec", "--skip-git-repo-check", "--ignore-user-config", "--sandbox", "read-only", "--output-last-message", "the canonical advisory prompt"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("codex invocation args = %q, missing %q", args, want)
		}
	}
	if strings.Contains(joined, "-C") {
		index := -1
		for position, arg := range args {
			if arg == "-C" {
				index = position
			}
		}
		if index < 0 || index+1 >= len(args) || args[index+1] != dir {
			t.Fatalf("codex was not launched with -C pointed at its own scratch directory: %v (ran in %s)", args, dir)
		}
	} else {
		t.Fatalf("codex invocation args = %q, missing -C", args)
	}
}

func TestCodexAdapterReturnsTransportErrorOnNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake codex script targets POSIX shells")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho boom failure >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := &CodexAdapter{LookPath: func(string) (string, error) { return script, nil }}
	raw, err := adapter.Review(context.Background(), "prompt")
	if err == nil {
		t.Fatalf("Review() = %q, nil, want a transport failure", raw)
	}
	if !strings.Contains(err.Error(), "codex advisory transport failed") || !strings.Contains(err.Error(), "boom failure") {
		t.Fatalf("Review() error = %v, want the transport failure to carry the process's stderr", err)
	}
}

func TestCodexAdapterReturnsTransportErrorWhenContextExpires(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake codex script targets POSIX shells")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := &CodexAdapter{LookPath: func(string) (string, error) { return script, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw, err := adapter.Review(ctx, "prompt")
	if err == nil {
		t.Fatalf("Review() = %q, nil, want a transport failure for an already-canceled context", raw)
	}
}
