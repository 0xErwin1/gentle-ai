//go:build windows

package shellinstaller

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestUserWindowsJobPhysicalLimits(t *testing.T) {
	job, err := userWindowsJob()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(job) })
	var memory windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&memory)), uint32(unsafe.Sizeof(memory)), nil); err != nil {
		t.Fatal(err)
	}
	if memory.BasicLimitInformation.LimitFlags != userWindowsJobFlags || memory.BasicLimitInformation.ActiveProcessLimit != 64 || memory.JobMemoryLimit != 3221225472 {
		t.Fatalf("physical job limits differ: %#v", memory)
	}
	var cpu userWindowsCPU
	if err := windows.QueryInformationJobObject(job, windows.JobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu)), nil); err != nil {
		t.Fatal(err)
	}
	if cpu.Flags != 5 || cpu.Rate != uint32(10000/runtime.NumCPU()) {
		t.Fatalf("physical CPU hard cap differs: %#v", cpu)
	}
}

func TestUserWindowsProcessFixture(t *testing.T) {
	if os.Getenv("GENTLE_WINDOWS_PROCESS_FIXTURE") != "1" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv("GENTLE_WINDOWS_PROCESS_MARKER"), []byte(cwd), 0600); err != nil {
		os.Exit(3)
	}
	_, _ = os.Stdout.WriteString("fixture ran\n")
	os.Exit(0)
}

func userWindowsProcessFixture(t *testing.T) (*exec.Cmd, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	marker := filepath.Join(cwd, "executed.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, self, "-test.run=^TestUserWindowsProcessFixture$")
	command.Dir = cwd
	command.Env = []string{"GENTLE_WINDOWS_PROCESS_FIXTURE=1", "GENTLE_WINDOWS_PROCESS_MARKER=" + marker, "SystemRoot=" + os.Getenv("SystemRoot")}
	return command, marker
}

func TestUserWindowsProcessStartsAfterBindingAndPreservesCWD(t *testing.T) {
	command, marker := userWindowsProcessFixture(t)
	var output bytes.Buffer
	command.Stdout = &output
	release, err := userWindowsStart(command)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != command.Dir || output.String() != "fixture ran\n" {
		t.Fatalf("child CWD/output differ: %q, %q, %v", got, output.String(), err)
	}
}

func TestUserWindowsProcessCannotRunBeforeJobBinding(t *testing.T) {
	previous := assignUserWindowsProcessToJob
	failure := errors.New("forced physical job binding refusal")
	assignUserWindowsProcessToJob = func(windows.Handle, windows.Handle) error { return failure }
	t.Cleanup(func() { assignUserWindowsProcessToJob = previous })
	command, marker := userWindowsProcessFixture(t)
	release, err := userWindowsStart(command)
	if release != nil || !errors.Is(err, failure) {
		t.Fatalf("binding failure = (%v, %v)", release != nil, err)
	}
	if command.ProcessState == nil {
		t.Fatal("refused suspended child was not reaped")
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("child executed before binding: %v", err)
	}
}
