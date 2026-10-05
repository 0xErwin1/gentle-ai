//go:build windows

package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

func TestWindowsShellInstallUnconfirmedSelectionHasNoEffects(t *testing.T) {
	if err := shellinstaller.UserKernelCheck(); err != nil {
		t.Skip("requires actual Windows 11 x64 normal-account acceptance; not Server/ARM qualification")
	}
	target := filepath.Join(t.TempDir(), "Gentle Shell")
	if _, err := shellinstaller.InspectUserInstall(shellinstaller.UserInstallRequest{Destination: target, Mode: "separate"}); err != nil {
		t.Fatalf("physical Windows selection fixture refused: %v", err)
	}
	err := RunShell([]string{"install", "--target", target, "--confirm", "not-confirmed"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--inspect") || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("wrong confirmation did not name the safe continuation: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("unconfirmed request created a target: %v", err)
	}
}
