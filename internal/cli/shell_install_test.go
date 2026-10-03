package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v3/internal/shellinstaller"
)

func TestShellInstallFlags(t *testing.T) {
	for _, args := range [][]string{{"--unknown"}, {"extra"}, {"--target"}, {"--confirm"}} {
		if _, _, err := parseShellInstall(args, io.Discard); err == nil {
			t.Fatalf("accepted invalid flags %q", args)
		}
	}
	req, inspect, err := parseShellInstall([]string{"--target", "/owned/shell", "--mode", "shared", "--prefix", "/owned/pi", "--agent", "/owned/agent", "--inspect"}, io.Discard)
	if err != nil || !inspect || req.Mode != "shared" || req.Confirmation != "" {
		t.Fatalf("selection = %+v inspect=%v error=%v", req, inspect, err)
	}
	if _, err := shellinstaller.UserInstallFromEntry(shellEntryValues(req)); err != nil {
		t.Fatal(err)
	}
}

func TestShellInstallHelpHasNoEffects(t *testing.T) {
	var output bytes.Buffer
	if err := RunShell([]string{"install", "--help"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "--inspect") || !strings.Contains(output.String(), "existing delegated") {
		t.Fatalf("missing consent or prerequisites: %q", output.String())
	}
}

func TestShellInstallTUIEditAndCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := shellInstallModel{ctx: ctx, cancel: cancel, stdout: io.Discard, req: shellinstaller.UserInstallRequest{Mode: "separate"}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/owned/shell")})
	m = next.(shellInstallModel)
	if cmd != nil || m.req.Destination != "/owned/shell" || m.review || m.busy {
		t.Fatal("typing performed effects or did not edit selection")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(shellInstallModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(shellInstallModel)
	if m.req.Mode != "shared" || !strings.Contains(m.View(), "/bin/pi") {
		t.Fatal("shared selection or explicit command review absent")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil || ctx.Err() == nil || next.(shellInstallModel).busy {
		t.Fatal("idle cancellation did not settle without installation")
	}
}

func TestShellInstallTUIBusyCancelWaitsForReap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := shellInstallModel{ctx: ctx, cancel: cancel, busy: true}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || ctx.Err() == nil || !next.(shellInstallModel).busy {
		t.Fatal("busy cancellation abandoned the worker")
	}
	next, cmd = next.(shellInstallModel).Update(shellInstallDone{context.Canceled})
	if cmd == nil || next.(shellInstallModel).busy || next.(shellInstallModel).err != context.Canceled {
		t.Fatal("completion did not preserve cancellation outcome")
	}
}
