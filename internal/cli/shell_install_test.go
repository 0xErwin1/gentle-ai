package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

func TestWindowsShellInstallFlagsAndNoEffectHelp(t *testing.T) {
	for _, args := range [][]string{{"--unknown"}, {"extra"}, {"--target"}, {"--confirm"}} {
		if _, _, err := parseShellInstall(args, io.Discard); err == nil {
			t.Fatalf("invalid flags admitted: %q", args)
		}
	}
	var output bytes.Buffer
	if err := RunShell([]string{"install", "--help"}, &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"--inspect", "--confirm", "Windows 11 x64", "Separate only", "personal PATH"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing safe help: %s", expected)
		}
	}
}

func TestWindowsShellInstallReusesEditReviewAndSettledCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := shellInstallModel{ctx: ctx, cancel: cancel, stdout: io.Discard, req: shellinstaller.UserInstallRequest{Mode: "separate"}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C:\\owned\\shell")})
	m = next.(shellInstallModel)
	if cmd != nil || m.review || m.busy {
		t.Fatal("typing performed installation effects")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next, _ = next.(shellInstallModel).Update(tea.KeyMsg{Type: tea.KeyRight})
	m = next.(shellInstallModel)
	if m.req.Mode != "separate" || !strings.Contains(m.View(), "gentle-shell.cmd") || !strings.Contains(m.View(), "pi.cmd") {
		t.Fatal("Windows offered unsupported Shared or hid owned commands")
	}
	m.busy = true
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || ctx.Err() == nil || !next.(shellInstallModel).busy {
		t.Fatal("cancel abandoned the installer worker")
	}
	next, cmd = next.(shellInstallModel).Update(shellInstallDone{context.Canceled})
	if cmd == nil || next.(shellInstallModel).busy || next.(shellInstallModel).err != context.Canceled {
		t.Fatal("cancellation did not await actual completion")
	}
}
