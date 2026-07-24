//go:build !windows

package hostruntime

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecuteDeadlineTerminatesDescendantProcessGroup(t *testing.T) {
	step := helperStep(t, t.TempDir(), "spawn-child")
	step.Deadline = 150 * time.Millisecond
	evidence, err := NewExecutor().execute(context.Background(), step)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if evidence.TerminalCause != TerminalDeadlineExceeded {
		t.Fatalf("TerminalCause = %q", evidence.TerminalCause)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(evidence.Stdout.Retained))
	if err != nil {
		t.Fatalf("parse descendant pid %q: %v", evidence.Stdout.Retained, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant process %d still exists after process-group cleanup", pid)
}
