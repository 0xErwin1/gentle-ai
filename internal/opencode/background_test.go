package opencode

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCapabilityVersionTable(t *testing.T) {
	tests := []struct {
		name   string
		output string
		status CapabilityStatus
		ready  bool
	}{
		{name: "baseline", output: "1.15.11\n", status: CapabilityReady, ready: true},
		{name: "newer", output: "opencode 1.18.18", status: CapabilityReady, ready: true},
		{name: "older patch", output: "1.15.10", status: CapabilityUnsupported},
		{name: "older minor", output: "1.14.99", status: CapabilityUnsupported},
		{name: "pre release baseline", output: "1.15.11-beta.1", status: CapabilityUnsupported},
		{name: "unknown output", output: "development build", status: CapabilityUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveCapability("/real/opencode", func(string) (string, error) {
				return tt.output, nil
			})
			if got.Status != tt.status || got.Ready() != tt.ready {
				t.Fatalf("resolution = %#v, want status=%q ready=%t", got, tt.status, tt.ready)
			}
		})
	}

	unknown := ResolveCapability("/real/opencode", func(string) (string, error) {
		return "", errors.New("not runnable")
	})
	if unknown.Status != CapabilityUnknown || unknown.Ready() {
		t.Fatalf("command failure resolution = %#v, want unknown foreground", unknown)
	}
}

func TestResolveTargetSkipsManagedBinAndPreventsRecursion(t *testing.T) {
	home := t.TempDir()
	managed := BinDir(home)
	real := filepath.Join(t.TempDir(), "opencode")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, "opencode"), []byte("managed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("real"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveTarget(home, "linux", managed+string(os.PathListSeparator)+filepath.Dir(real))
	if err != nil {
		t.Fatal(err)
	}
	if got != real {
		t.Fatalf("ResolveTarget() = %q, want %q", got, real)
	}
}

func TestLauncherContentsPreserveExplicitFalse(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "real-opencode")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nprintf '%s|%s' \"${OPENCODE_EXPERIMENTAL_BACKGROUND_SUBAGENTS-unset}\" \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := PrepareActivation(home, ActivationOptions{
		OS:   "linux",
		Path: filepath.Dir(target),
		RunVersion: func(string) (string, error) {
			return "1.15.11", nil
		},
		AddToUserPath: func(string) error { return nil },
		ResolveTarget: func(string, string, string) (string, error) { return target, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}

	launcher := POSIXLauncherPath(home)
	run := func(value string) string {
		cmd := exec.Command(launcher, "arg")
		env := []string{}
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, BackgroundSubagentsEnv+"=") {
				env = append(env, entry)
			}
		}
		if value != "" {
			env = append(env, BackgroundSubagentsEnv+"="+value)
		}
		cmd.Env = env
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("launcher output: %v", err)
		}
		return string(output)
	}
	if got := run(""); got != "true|arg" {
		t.Fatalf("unset environment output = %q, want true|arg", got)
	}
	if got := run("false"); got != "false|arg" {
		t.Fatalf("explicit false output = %q, want false|arg", got)
	}
}

func TestWindowsLauncherContents(t *testing.T) {
	contents := launcherContent("windows", `C:\Program Files\OpenCode\opencode.exe`)
	for _, name := range []string{WindowsCMDPathPlaceholder, WindowsPS1PathPlaceholder} {
		content := contents[name]
		if !strings.Contains(content, OwnershipMarker) || !strings.Contains(content, BackgroundSubagentsEnv) {
			t.Fatalf("%s launcher = %q, missing ownership/env contract", name, content)
		}
		if !strings.Contains(content, "opencode.exe") {
			t.Fatalf("%s launcher = %q, missing real target", name, content)
		}
	}
	if strings.Contains(contents[WindowsCMDPathPlaceholder], "opencode.cmd") || strings.Contains(contents[WindowsPS1PathPlaceholder], "opencode.ps1") {
		t.Fatal("Windows launchers must execute the resolved real target, not themselves")
	}
}

func TestActivationIsIdempotentAndOffRemovesOnlyOwnedFiles(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(target, []byte("real"), 0o755); err != nil {
		t.Fatal(err)
	}
	options := ActivationOptions{
		OS:            "linux",
		Path:          filepath.Dir(target),
		RunVersion:    func(string) (string, error) { return "1.18.18", nil },
		AddToUserPath: func(string) error { return nil },
		ResolveTarget: func(string, string, string) (string, error) { return target, nil },
	}
	first, err := Activate(home, options)
	if err != nil {
		t.Fatal(err)
	}
	path := POSIXLauncherPath(home)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Activate(home, options)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || len(first.ChangedPaths()) == 0 || len(second.ChangedPaths()) != 0 {
		t.Fatalf("activation changed paths first=%v second=%v", first.ChangedPaths(), second.ChangedPaths())
	}
	if _, err := Deactivate(home, options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("owned launcher stat error = %v, want absent", err)
	}
	if _, err := Deactivate(home, options); err != nil {
		t.Fatal(err)
	}
}

func TestActivationRefusesUserOwnedCollision(t *testing.T) {
	home := t.TempDir()
	path := POSIXLauncherPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("user launcher"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareActivation(home, ActivationOptions{
		OS:            "linux",
		RunVersion:    func(string) (string, error) { return "1.18.18", nil },
		AddToUserPath: func(string) error { return nil },
		ResolveTarget: func(string, string, string) (string, error) { return "/real/opencode", nil },
	})
	if err == nil || plan != nil || !strings.Contains(err.Error(), "user-owned") {
		t.Fatalf("PrepareActivation() plan=%v error=%v, want collision refusal", plan, err)
	}
}

func TestActivationRollsBackLauncherWritesWhenPathUpdateFails(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(target, []byte("real"), 0o755); err != nil {
		t.Fatal(err)
	}
	pathErr := errors.New("path update failed")
	plan, err := PrepareActivation(home, ActivationOptions{
		OS:            "linux",
		RunVersion:    func(string) (string, error) { return "1.15.11", nil },
		AddToUserPath: func(string) error { return pathErr },
		ResolveTarget: func(string, string, string) (string, error) { return target, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err == nil || !strings.Contains(err.Error(), pathErr.Error()) {
		t.Fatalf("Apply() error = %v, want path update failure", err)
	}
	if _, err := os.Stat(POSIXLauncherPath(home)); !os.IsNotExist(err) {
		t.Fatalf("launcher after failed activation = %v, want absent", err)
	}
}
