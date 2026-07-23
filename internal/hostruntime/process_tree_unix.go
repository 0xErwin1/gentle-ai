//go:build !windows

package hostruntime

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

func startProcessTree(command *exec.Cmd) (func() error, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return nil, err
	}
	var (
		once       sync.Once
		releaseErr error
	)
	return func() error {
		once.Do(func() {
			releaseErr = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			if errors.Is(releaseErr, syscall.ESRCH) {
				releaseErr = nil
				return
			}
			if releaseErr != nil {
				return
			}
			deadline := time.Now().Add(500 * time.Millisecond)
			for time.Now().Before(deadline) {
				probeErr := syscall.Kill(-command.Process.Pid, 0)
				if errors.Is(probeErr, syscall.ESRCH) {
					return
				}
				if probeErr != nil {
					releaseErr = probeErr
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			releaseErr = fmt.Errorf("process-group cleanup could not be proven")
		})
		return releaseErr
	}, nil
}

func processTreeCleanupScope() string { return "process_group" }
