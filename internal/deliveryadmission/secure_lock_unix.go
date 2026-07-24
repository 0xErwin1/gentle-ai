//go:build !windows

package deliveryadmission

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func (directory *secureUseDirectory) withExclusiveLock(
	ctx context.Context,
	action func() error,
) error {
	if directory == nil || action == nil {
		return fmt.Errorf("%w: invalid secure store lock", ErrInvalid)
	}
	if err := directory.validate(); err != nil {
		return err
	}
	fd, err := unix.Openat(
		directory.directoryFD,
		"repository.lock",
		unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "repository.lock")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := validateUnixUseFileInfo(info, -1); err != nil {
		return err
	}
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) &&
			!errors.Is(err, unix.EINTR) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer unix.Flock(fd, unix.LOCK_UN) //nolint:errcheck -- release is best effort after action
	if err := directory.validate(); err != nil {
		return err
	}
	return action()
}
