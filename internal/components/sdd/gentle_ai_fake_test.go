package sdd

// gentle_ai_fake_test.go implements the fake `gentle-ai` executable used by
// TestClaudeUserPromptSubmitHookExecutesPowerShellCommandWithSpecialChars.
//
// The fake is the test binary re-executed under the name `gentle-ai` (or
// `gentle-ai.exe` on Windows): TestMain detects GENTLE_AI_FAKE_LOG in the
// child process and runs the fake instead of the test suite. Re-executing the
// test binary is the only portable way to observe exact argv elements on
// Windows without a POSIX shell: a #!/bin/sh script cannot be launched there,
// and any wrapper shell would re-tokenize the arguments before the fake sees
// them. Nothing in this file assumes a POSIX shell.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestMain turns the test binary into the fake gentle-ai executable when it
// is re-invoked with GENTLE_AI_FAKE_LOG set. In the normal test process the
// variable is empty and the suite runs as usual.
func TestMain(m *testing.M) {
	if logPath := os.Getenv("GENTLE_AI_FAKE_LOG"); logPath != "" {
		os.Exit(runGentleAIFake(logPath))
	}
	os.Exit(m.Run())
}

// runGentleAIFake records its argv, stdin bytes and exit code into logPath,
// then exits with the integer in GENTLE_AI_FAKE_EXIT (default 0). It uses
// only the standard library and no test-framework helpers, because it runs
// inside a bare re-executed process, not under `go test`.
func runGentleAIFake(logPath string) int {
	var log bytes.Buffer
	for i, arg := range os.Args[1:] {
		fmt.Fprintf(&log, "argv[%d]=%s\n", i, arg)
	}
	if projectDir, ok := os.LookupEnv("CLAUDE_PROJECT_DIR"); ok {
		fmt.Fprintf(&log, "env-CLAUDE_PROJECT_DIR=%s\n", projectDir)
	} else {
		log.WriteString("env-CLAUDE_PROJECT_DIR=<unset>\n")
	}

	stdinBytes := readStdinBounded(os.Stdin, 10*time.Second)
	fmt.Fprintf(&log, "stdin-bytes=%d\n", len(stdinBytes))
	log.WriteString("stdin-begin\n")
	log.Write(stdinBytes)
	log.WriteString("\nstdin-end\n")

	exitCode := 0
	if raw := os.Getenv("GENTLE_AI_FAKE_EXIT"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			exitCode = parsed
		}
	}
	fmt.Fprintf(&log, "exit=%d\n", exitCode)

	if err := os.WriteFile(logPath, log.Bytes(), 0o644); err != nil {
		// No log means the parent test cannot verify anything; surface the
		// failure on stderr and exit non-zero so the hook wrapper is also
		// visibly affected.
		fmt.Fprintf(os.Stderr, "gentle-ai fake: write log %q: %v\n", logPath, err)
		return 70
	}
	return exitCode
}

// readStdinBounded reads r to EOF without being able to block forever: a
// read deadline is applied when the OS supports it, and otherwise a timer
// bounds the wait. The test process exits right after, so an abandoned
// goroutine on the fallback path is harmless.
func readStdinBounded(r io.Reader, limit time.Duration) []byte {
	if f, ok := r.(*os.File); ok {
		if err := f.SetReadDeadline(time.Now().Add(limit)); err == nil {
			data, _ := io.ReadAll(f)
			return data
		}
	}
	type result struct {
		data []byte
	}
	ch := make(chan result, 1)
	go func() {
		data, _ := io.ReadAll(r)
		ch <- result{data: data}
	}()
	select {
	case res := <-ch:
		return res.data
	case <-time.After(limit):
		return nil
	}
}
