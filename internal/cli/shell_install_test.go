package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
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

func TestShellInstallRefusalHelpIsRunnable(t *testing.T) {
	_, _, positionalErr := parseShellInstall([]string{"extra"}, io.Discard)
	_, gateErr := GateShellInstallCandidate(shellinstaller.Profile{Channel: shellinstaller.ChannelStable})
	for _, err := range []error{positionalErr, gateErr} {
		if err == nil || !strings.Contains(err.Error(), "gentle-ai shell install --help") {
			t.Fatalf("refusal lacks the help continuation: %v", err)
		}
	}
	if !strings.Contains(gateErr.Error(), "draft gate cannot execute") {
		t.Fatal("draft guidance implies installation authority")
	}
	var output bytes.Buffer
	if err := RunShell([]string{"install", "--help"}, &output); err != nil || !strings.Contains(output.String(), "--inspect") || !strings.Contains(output.String(), "--confirm") {
		t.Fatalf("named help is not runnable or lacks physical consent flags: %v %q", err, output.String())
	}
	if _, err := GateShellInstallCandidate(shellinstaller.Profile{Channel: shellinstaller.ChannelStable}); err == nil {
		t.Fatal("reading help authorized the draft gate")
	}
}

func TestShellInstallConfirmationRefusalHasNoEffects(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("physical user selection is Linux amd64 only")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "shell")
	if _, err := shellinstaller.InspectUserInstall(shellinstaller.UserInstallRequest{Destination: target, Mode: "separate"}); err != nil {
		t.Fatalf("invalid physical selection fixture: %v", err)
	}
	err := RunShell([]string{"install", "--target", target, "--confirm", "not-confirmed"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "gentle-ai shell install --help") || !strings.Contains(err.Error(), "--inspect") || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("unconfirmed selection lacks the safe continuation: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("unconfirmed selection created or published a target: %v", err)
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
