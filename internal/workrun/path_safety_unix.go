//go:build !windows

package workrun

import (
	"errors"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func workRunPathUnsafe(_ string, info fs.FileInfo) bool {
	return info == nil || info.Mode()&os.ModeSymlink != 0
}

func createPrivateWorkRunDirectory(path string) (bool, error) {
	err := os.Mkdir(path, 0o700)
	created := err == nil
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return false, err
	}
	if err := validatePrivateWorkRunDirectory(path); err != nil {
		if created {
			_ = os.Remove(path)
		}
		return false, err
	}
	return created, nil
}

func createPrivateWorkRunFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	pathInfo, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || !info.Mode().IsRegular() ||
		!os.SameFile(info, pathInfo) || !privateOpenWorkRunPathSafe(file, info) {
		_ = file.Close()
		_ = os.Remove(path)
		if statErr != nil {
			return nil, statErr
		}
		if pathErr != nil {
			return nil, pathErr
		}
		return nil, errUnsafeWorkRunPath
	}
	return file, nil
}

func securePrivateWorkRunPath(path string) error {
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 ||
		(!before.IsDir() && !before.Mode().IsRegular()) {
		return errUnsafeWorkRunPath
	}
	file, err := openWorkRunPathNoFollow(path, before.IsDir())
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		if err != nil {
			return err
		}
		return errWorkRunPathReplaced
	}
	mode := os.FileMode(0o600)
	if opened.IsDir() {
		mode = 0o700
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	secured, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(opened, secured) || !os.SameFile(secured, current) ||
		!privateOpenWorkRunPathSafe(file, secured) ||
		validatePrivateWorkRunInfo(path, current, secured.IsDir()) != nil {
		return errWorkRunPathReplaced
	}
	return nil
}

func privateWorkRunPathSafe(_ string, info fs.FileInfo) bool {
	if info == nil || info.Mode().Perm()&0o077 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func privateOpenWorkRunPathSafe(_ *os.File, info fs.FileInfo) bool {
	return privateWorkRunPathSafe("", info)
}

func openWorkRunPathNoFollow(path string, _ bool) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
