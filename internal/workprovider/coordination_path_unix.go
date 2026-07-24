//go:build !windows

package workprovider

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func coordinationPathUnsafe(_ string, info fs.FileInfo) bool {
	return info == nil || info.Mode()&os.ModeSymlink != 0
}

func createPrivateCoordinationDirectory(path string) (bool, error) {
	err := os.Mkdir(path, 0o700)
	created := err == nil
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return false, err
	}
	if err := validatePrivateCoordinationDirectory(path); err != nil {
		if created {
			_ = os.Remove(path)
		}
		return false, err
	}
	return created, nil
}

func createPrivateCoordinationFile(path string) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parentFD, err := openCoordinationDirectoryChain(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	defer unix.Close(parentFD)
	fd, err := unix.Openat(
		parentFD,
		filepath.Base(absolute),
		unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	opened, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if statErr != nil ||
		pathErr != nil ||
		!opened.Mode().IsRegular() ||
		!os.SameFile(opened, current) ||
		!privateOpenCoordinationPathSafe(file, opened) {
		_ = file.Close()
		_ = os.Remove(path)
		if statErr != nil {
			return nil, statErr
		}
		if pathErr != nil {
			return nil, pathErr
		}
		return nil, errUnsafeCoordinationPath
	}
	return file, nil
}

func privateCoordinationPathSafe(_ string, info fs.FileInfo) bool {
	if info == nil || info.Mode().Perm()&0o077 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func privateOpenCoordinationPathSafe(
	_ *os.File,
	info fs.FileInfo,
) bool {
	return privateCoordinationPathSafe("", info)
}

func coordinationSharedDirectorySafe(
	_ string,
	info fs.FileInfo,
) bool {
	if info == nil || !info.IsDir() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func coordinationSharedOpenDirectorySafe(
	_ *os.File,
	info fs.FileInfo,
) bool {
	return coordinationSharedDirectorySafe("", info)
}

func openCoordinationPathNoFollow(
	path string,
	directory bool,
) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parentFD, err := openCoordinationDirectoryChain(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	defer unix.Close(parentFD)
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(parentFD, filepath.Base(absolute), flags, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openCoordinationDirectoryChain(path string) (int, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return -1, err
	}
	fd, err := unix.Open(
		string(filepath.Separator),
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY,
		0,
	)
	if err != nil {
		return -1, err
	}
	components := strings.Split(
		strings.TrimPrefix(absolute, string(filepath.Separator)),
		string(filepath.Separator),
	)
	for _, component := range components {
		if component == "" {
			continue
		}
		nextFD, openErr := unix.Openat(
			fd,
			component,
			unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW,
			0,
		)
		if openErr != nil {
			_ = unix.Close(fd)
			return -1, openErr
		}
		_ = unix.Close(fd)
		fd = nextFD
	}
	return fd, nil
}
