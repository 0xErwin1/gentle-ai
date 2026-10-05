//go:build windows

package shellinstaller

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const userWindowsJobFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_JOB_MEMORY | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS

// The Windows API defines a pair of DWORDs, not a pointer-sized CPU structure.
// ENABLE | HARD_CAP limits this job to one logical CPU's share of the machine.
// Job memory is committed memory; this does not claim to disable the pagefile.
type userWindowsCPU struct {
	Flags uint32
	Rate  uint32
}

var resumeUserWindowsProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
var assignUserWindowsProcessToJob = windows.AssignProcessToJobObject

func userWindowsJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	refuse := func(err error) (windows.Handle, error) {
		return 0, errors.Join(err, windows.CloseHandle(job))
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: userWindowsJobFlags, ActiveProcessLimit: 64,
		},
		JobMemoryLimit: 3221225472,
	}
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	runtime.KeepAlive(&limits)
	if err != nil {
		return refuse(err)
	}
	cpu := userWindowsCPU{Flags: 5, Rate: uint32(10000 / runtime.NumCPU())}
	if cpu.Rate == 0 {
		return refuse(errors.New("physical CPU count exceeds supported job cap"))
	}
	_, err = windows.SetInformationJobObject(job, windows.JobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu)))
	runtime.KeepAlive(&cpu)
	if err != nil {
		return refuse(err)
	}
	var observed windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&observed)), uint32(unsafe.Sizeof(observed)), nil); err != nil {
		return refuse(err)
	}
	if observed.BasicLimitInformation.LimitFlags != limits.BasicLimitInformation.LimitFlags || observed.BasicLimitInformation.ActiveProcessLimit != 64 || observed.JobMemoryLimit != limits.JobMemoryLimit {
		return refuse(errors.New("physical Windows job memory/process limits differ"))
	}
	var observedCPU userWindowsCPU
	if err := windows.QueryInformationJobObject(job, windows.JobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&observedCPU)), uint32(unsafe.Sizeof(observedCPU)), nil); err != nil {
		return refuse(err)
	}
	if observedCPU != cpu {
		return refuse(errors.New("physical Windows job CPU limit differs"))
	}
	return job, nil
}

// No CREATE_NO_WINDOW: the eventual interactive entry inherits its console.
// This primitive supplies process custody, not path ownership, artifact trust,
// a Windows 11 qualification, or a complete installer/terminal contract.
func userWindowsStart(command *exec.Cmd) (func() error, error) {
	job, err := userWindowsJob()
	if err != nil {
		return nil, err
	}
	release := func() error {
		return errors.Join(windows.TerminateJobObject(job, 1), windows.CloseHandle(job))
	}
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		return nil, errors.Join(err, release())
	}
	refuse := func(cause error) (func() error, error) {
		stop := release()
		kill := command.Process.Kill() // Covers failure before job assignment.
		wait := command.Wait()         // Never leave a suspended child unreaped.
		return nil, errors.Join(cause, stop, kill, wait)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(command.Process.Pid))
	if err != nil {
		return refuse(err)
	}
	if err := assignUserWindowsProcessToJob(job, process); err != nil {
		return refuse(errors.Join(err, windows.CloseHandle(process)))
	}
	if err := resumeUserWindowsProcess.Find(); err != nil {
		return refuse(errors.Join(err, windows.CloseHandle(process)))
	}
	status, _, _ := resumeUserWindowsProcess.Call(uintptr(process))
	closeErr := windows.CloseHandle(process)
	if status != 0 || closeErr != nil {
		return refuse(errors.Join(fmt.Errorf("resume bound Windows process: NTSTATUS %#x", status), closeErr))
	}
	return release, nil
}
