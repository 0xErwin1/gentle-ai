//go:build !windows

package deliveryadmission

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type secureUseDirectory struct {
	commonFD       int
	directoryFD    int
	commonPath     string
	directoryPath  string
	commonIdentity unix.Stat_t
	storeIdentity  unix.Stat_t
}

func openSecureUseDirectory(
	commonPath string,
	directoryPath string,
	expectedCommon fs.FileInfo,
) (*secureUseDirectory, error) {
	commonFD, commonIdentity, err := openAbsoluteUseDirectory(commonPath, false)
	if err != nil {
		return nil, err
	}
	if !sameExpectedUnixUseIdentity(expectedCommon, commonIdentity) {
		_ = unix.Close(commonFD)
		return nil, fmt.Errorf("%w: Git common directory changed before secure open", ErrBindingMismatch)
	}
	closeCommon := true
	defer func() {
		if closeCommon {
			_ = unix.Close(commonFD)
		}
	}()

	components, err := secureUseDirectoryComponents(commonPath, directoryPath)
	if err != nil {
		return nil, err
	}
	currentFD := commonFD
	for index, component := range components {
		nextFD, _, openErr := openOrCreateUseDirectoryAt(currentFD, component, index > 0)
		if openErr != nil {
			if currentFD != commonFD {
				_ = unix.Close(currentFD)
			}
			return nil, openErr
		}
		if currentFD != commonFD {
			_ = unix.Close(currentFD)
		}
		currentFD = nextFD
	}
	var storeIdentity unix.Stat_t
	if err := unix.Fstat(currentFD, &storeIdentity); err != nil {
		_ = unix.Close(currentFD)
		return nil, err
	}
	closeCommon = false
	directory := &secureUseDirectory{
		commonFD: commonFD, directoryFD: currentFD,
		commonPath: commonPath, directoryPath: directoryPath,
		commonIdentity: commonIdentity, storeIdentity: storeIdentity,
	}
	if err := directory.validate(); err != nil {
		_ = directory.close()
		return nil, err
	}
	if err := syncSecureUseDirectory(directory); err != nil {
		_ = directory.close()
		return nil, fmt.Errorf("sync secure authorization-use directory: %w", err)
	}
	return directory, nil
}

func openAbsoluteUseDirectory(path string, private bool) (int, unix.Stat_t, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return -1, unix.Stat_t{}, fmt.Errorf("%w: non-canonical authority path", ErrInvalid)
	}
	fd, err := unix.Open(
		string(filepath.Separator),
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return -1, unix.Stat_t{}, err
	}
	for _, component := range strings.Split(
		strings.TrimPrefix(path, string(filepath.Separator)),
		string(filepath.Separator),
	) {
		if component == "" {
			continue
		}
		nextFD, openErr := unix.Openat(
			fd, component,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
			0,
		)
		_ = unix.Close(fd)
		if openErr != nil {
			return -1, unix.Stat_t{}, openErr
		}
		fd = nextFD
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return -1, unix.Stat_t{}, err
	}
	if err := validateUnixUseDirectoryStat(stat, private); err != nil {
		_ = unix.Close(fd)
		return -1, unix.Stat_t{}, err
	}
	return fd, stat, nil
}

func openOrCreateUseDirectoryAt(parentFD int, name string, private bool) (int, bool, error) {
	if !validUseStoreComponent(name) {
		return -1, false, fmt.Errorf("%w: invalid secure directory component", ErrInvalid)
	}
	mode := uint32(0o755)
	if private {
		mode = 0o700
	}
	created := false
	if err := unix.Mkdirat(parentFD, name, mode); err != nil {
		if !errors.Is(err, unix.EEXIST) {
			return -1, false, err
		}
	} else {
		created = true
	}
	fd, err := unix.Openat(
		parentFD, name,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return -1, false, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return -1, false, err
	}
	if stat.Uid != uint32(os.Geteuid()) {
		_ = unix.Close(fd)
		return -1, false, fmt.Errorf("%w: authorization-use directory owner", ErrUntrustedPreimage)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = unix.Close(fd)
		return -1, false, fmt.Errorf("%w: authorization-use path is not a directory", ErrUntrustedPreimage)
	}
	if private && stat.Mode&0o777 != 0o700 {
		if err := unix.Fchmod(fd, 0o700); err != nil {
			_ = unix.Close(fd)
			return -1, false, fmt.Errorf("protect authorization-use directory: %w", err)
		}
		if err := unix.Fstat(fd, &stat); err != nil {
			_ = unix.Close(fd)
			return -1, false, err
		}
	}
	if err := validateUnixUseDirectoryStat(stat, private); err != nil {
		_ = unix.Close(fd)
		return -1, false, err
	}
	if err := unix.Fsync(fd); err != nil {
		_ = unix.Close(fd)
		return -1, false, fmt.Errorf("sync protected authorization-use directory: %w", err)
	}
	if created {
		if err := unix.Fsync(parentFD); err != nil {
			_ = unix.Close(fd)
			return -1, false, fmt.Errorf("sync authorization-use parent directory: %w", err)
		}
	}
	return fd, created, nil
}

func validateUnixUseDirectoryStat(stat unix.Stat_t, private bool) error {
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("%w: authority path is not a directory", ErrUntrustedPreimage)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: authority directory is not owned by the current user", ErrUntrustedPreimage)
	}
	if private {
		if stat.Mode&0o777 != 0o700 {
			return fmt.Errorf("%w: authorization-use directory is not owner-only", ErrUntrustedPreimage)
		}
	}
	return nil
}

func (directory *secureUseDirectory) validate() error {
	if directory == nil || directory.commonFD < 0 || directory.directoryFD < 0 ||
		directory.commonPath == "" || directory.directoryPath == "" {
		return fmt.Errorf("%w: unopened secure use directory", ErrInvalid)
	}
	var currentCommon, currentStore unix.Stat_t
	if err := unix.Fstat(directory.commonFD, &currentCommon); err != nil {
		return err
	}
	if err := unix.Fstat(directory.directoryFD, &currentStore); err != nil {
		return err
	}
	if !sameUnixUseIdentity(directory.commonIdentity, currentCommon) ||
		!sameUnixUseIdentity(directory.storeIdentity, currentStore) {
		return fmt.Errorf("%w: open authorization-use directory identity changed", ErrBindingMismatch)
	}
	if err := validateUnixUseDirectoryStat(currentCommon, false); err != nil {
		return err
	}
	if err := validateUnixUseDirectoryStat(currentStore, true); err != nil {
		return err
	}

	commonFD, commonPathIdentity, err := openAbsoluteUseDirectory(directory.commonPath, false)
	if err != nil {
		return err
	}
	_ = unix.Close(commonFD)
	storeFD, storePathIdentity, err := openAbsoluteUseDirectory(directory.directoryPath, true)
	if err != nil {
		return err
	}
	_ = unix.Close(storeFD)
	if !sameUnixUseIdentity(directory.commonIdentity, commonPathIdentity) ||
		!sameUnixUseIdentity(directory.storeIdentity, storePathIdentity) {
		return fmt.Errorf("%w: authorization-use directory path was replaced", ErrBindingMismatch)
	}
	return nil
}

func (directory *secureUseDirectory) publishNoReplace(target string, payload []byte) error {
	if !validSecureRecordName(target) || len(payload) == 0 ||
		len(payload) > maximumSecureRecordBytes {
		return fmt.Errorf("%w: invalid authorization-use publication", ErrInvalid)
	}
	if err := directory.validate(); err != nil {
		return err
	}
	temporary, err := randomUseStoreName()
	if err != nil {
		return err
	}
	fd, err := unix.Openat(
		directory.directoryFD, temporary,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), temporary)
	published := false
	defer func() {
		_ = file.Close()
		if !published {
			_ = unix.Unlinkat(directory.directoryFD, temporary, 0)
		}
	}()
	written, err := file.Write(payload)
	if err != nil {
		return err
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := validateUnixUseFileInfo(info, int64(len(payload))); err != nil {
		return err
	}
	if err := directory.validate(); err != nil {
		return err
	}
	if err := unix.Linkat(
		directory.directoryFD, temporary,
		directory.directoryFD, target,
		0,
	); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return errSecureUseTargetExists
		}
		return err
	}
	published = true
	if err := syncSecureUseDirectory(directory); err != nil {
		return fmt.Errorf("sync published authorization use: %w", err)
	}
	if err := unix.Unlinkat(directory.directoryFD, temporary, 0); err != nil {
		return fmt.Errorf("remove authorization-use reservation link: %w", err)
	}
	if err := syncSecureUseDirectory(directory); err != nil {
		return fmt.Errorf("sync authorization-use reservation cleanup: %w", err)
	}
	return directory.validate()
}

func (directory *secureUseDirectory) replace(target string, payload []byte) error {
	if !validSecureRecordName(target) || len(payload) == 0 ||
		len(payload) > maximumSecureRecordBytes {
		return fmt.Errorf("%w: invalid secure record replacement", ErrInvalid)
	}
	if err := directory.validate(); err != nil {
		return err
	}
	temporary, err := randomUseStoreName()
	if err != nil {
		return err
	}
	fd, err := unix.Openat(
		directory.directoryFD, temporary,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), temporary)
	renamed := false
	defer func() {
		_ = file.Close()
		if !renamed {
			_ = unix.Unlinkat(directory.directoryFD, temporary, 0)
		}
	}()
	written, err := file.Write(payload)
	if err != nil {
		return err
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := validateUnixUseFileInfo(info, int64(len(payload))); err != nil {
		return err
	}
	if err := directory.validate(); err != nil {
		return err
	}
	if err := unix.Renameat(
		directory.directoryFD, temporary,
		directory.directoryFD, target,
	); err != nil {
		return err
	}
	renamed = true
	if err := syncSecureUseDirectory(directory); err != nil {
		return fmt.Errorf("sync replaced secure record: %w", err)
	}
	return directory.validate()
}

func (directory *secureUseDirectory) readBounded(name string, maximum int) ([]byte, error) {
	if !validSecureRecordName(name) || maximum < 1 || maximum > maximumSecureRecordBytes {
		return nil, fmt.Errorf("%w: invalid authorization-use read", ErrInvalid)
	}
	if err := directory.validate(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(
		directory.directoryFD, name,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := validateUnixUseFileInfo(info, -1); err != nil {
		return nil, err
	}
	payload, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil {
		return nil, err
	}
	if len(payload) == 0 || len(payload) > maximum || int64(len(payload)) != info.Size() {
		return nil, fmt.Errorf("%w: authorization-use record byte bound", ErrUntrustedPreimage)
	}
	if err := directory.validate(); err != nil {
		return nil, err
	}
	return payload, nil
}

func validateUnixUseFileInfo(info fs.FileInfo, exactSize int64) error {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%w: authorization-use record is not an owner-only regular file", ErrUntrustedPreimage)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("%w: authorization-use record owner", ErrUntrustedPreimage)
	}
	if exactSize >= 0 && info.Size() != exactSize {
		return fmt.Errorf("%w: authorization-use record size", ErrUntrustedPreimage)
	}
	return nil
}

func (directory *secureUseDirectory) sync() error {
	if directory == nil || directory.directoryFD < 0 {
		return fmt.Errorf("%w: unopened secure use directory", ErrInvalid)
	}
	return unix.Fsync(directory.directoryFD)
}

func (directory *secureUseDirectory) close() error {
	if directory == nil {
		return nil
	}
	var joined error
	if directory.directoryFD >= 0 {
		joined = errors.Join(joined, unix.Close(directory.directoryFD))
		directory.directoryFD = -1
	}
	if directory.commonFD >= 0 {
		joined = errors.Join(joined, unix.Close(directory.commonFD))
		directory.commonFD = -1
	}
	return joined
}

func sameUnixUseIdentity(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino
}

func sameExpectedUnixUseIdentity(expected fs.FileInfo, current unix.Stat_t) bool {
	if expected == nil {
		return false
	}
	stat, ok := expected.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Dev) == uint64(current.Dev) &&
		uint64(stat.Ino) == uint64(current.Ino)
}
