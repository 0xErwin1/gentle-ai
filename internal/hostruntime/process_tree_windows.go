//go:build windows

package hostruntime

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

func startProcessTree(command *exec.Cmd) (func() error, error) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := command.Start(); err != nil {
		return nil, err
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, err
	}
	var (
		once       sync.Once
		releaseErr error
	)
	release := func() error {
		once.Do(func() {
			_ = windows.TerminateJobObject(job, 1)
			releaseErr = windows.CloseHandle(job)
		})
		return releaseErr
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = release()
		_ = command.Process.Kill()
		_ = command.Wait()
		return release, err
	}

	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false,
		uint32(command.Process.Pid),
	)
	if err != nil {
		_ = release()
		_ = command.Process.Kill()
		_ = command.Wait()
		return release, err
	}
	defer windows.CloseHandle(process)

	if err = windows.AssignProcessToJobObject(job, process); err == nil {
		err = ntResumeProcess.Find()
	}
	if err == nil {
		if status, _, _ := ntResumeProcess.Call(uintptr(process)); status != 0 {
			err = windows.NTStatus(status)
		}
	}
	if err != nil {
		_ = release()
		_ = command.Process.Kill()
		_ = command.Wait()
		return release, err
	}
	return release, nil
}

func processTreeCleanupScope() string { return "job_object" }
