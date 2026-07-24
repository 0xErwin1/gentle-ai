//go:build windows

package deliveryadmission

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
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
	file, err := openOrCreatePrivateWindowsUseFile(
		directory.directoryFile,
		"repository.lock",
	)
	if err != nil {
		return err
	}
	defer file.Close()
	var overlapped windows.Overlapped
	for {
		err = windows.LockFileEx(
			windows.Handle(file.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0,
			1,
			0,
			&overlapped,
		)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer windows.UnlockFileEx( //nolint:errcheck -- release is best effort after action
		windows.Handle(file.Fd()),
		0,
		1,
		0,
		&overlapped,
	)
	if err := directory.validate(); err != nil {
		return err
	}
	return action()
}

func openOrCreatePrivateWindowsUseFile(parent *os.File, name string) (*os.File, error) {
	if parent == nil || !validSecureRecordName(name) {
		return nil, fmt.Errorf("%w: invalid secure lock name", ErrInvalid)
	}
	descriptor, err := ownerOnlyUseSecurityDescriptor(false)
	if err != nil {
		return nil, err
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:             uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory:      windows.Handle(parent.Fd()),
		ObjectName:         objectName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
		SecurityDescriptor: descriptor,
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&handle,
		windowsUseFileAccess,
		attributes,
		&status,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN_IF,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|
			windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	runtime.KeepAlive(descriptor)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), name)
	if err := protectWindowsUseHandle(file, false); err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := validateWindowsUseHandle(file, false, true); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
