//go:build windows

package workrun

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func workRunPathUnsafe(path string, info fs.FileInfo) bool {
	if path == "" || info == nil || info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(name)
	return err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func createPrivateWorkRunDirectory(path string) (bool, error) {
	descriptor, err := ownerOnlyWorkRunSecurityDescriptor(true)
	if err != nil {
		return false, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	err = windows.CreateDirectory(name, &attributes)
	runtime.KeepAlive(descriptor)
	created := err == nil
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
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
	descriptor, err := ownerOnlyWorkRunSecurityDescriptor(false)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		&attributes,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	runtime.KeepAlive(descriptor)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	info, statErr := file.Stat()
	pathInfo, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || !info.Mode().IsRegular() ||
		!os.SameFile(info, pathInfo) || !privateOpenWorkRunPathSafe(file, info) ||
		workRunPathUnsafe(path, pathInfo) {
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
	if workRunPathUnsafe(path, before) ||
		(!before.IsDir() && !before.Mode().IsRegular()) {
		return errUnsafeWorkRunPath
	}
	file, err := openWindowsWorkRunPath(
		path,
		before.IsDir(),
		windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES,
	)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || openWindowsWorkRunFileUnsafe(file) {
		if err != nil {
			return err
		}
		return errWorkRunPathReplaced
	}
	descriptor, err := ownerOnlyWorkRunSecurityDescriptor(opened.IsDir())
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("owner-only work run DACL is unavailable")
	}
	if err := windows.SetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("apply owner-only work run DACL: %w", err)
	}
	runtime.KeepAlive(descriptor)

	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(opened, current) || workRunPathUnsafe(path, current) ||
		!privateOpenWorkRunPathSafe(file, opened) ||
		validatePrivateWorkRunInfo(path, current, opened.IsDir()) != nil {
		return errWorkRunPathReplaced
	}
	return nil
}

func privateWorkRunPathSafe(path string, info fs.FileInfo) bool {
	if path == "" || info == nil || workRunPathUnsafe(path, info) {
		return false
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	return err == nil && privateWorkRunSecurityDescriptorSafe(descriptor, info.IsDir())
}

func privateOpenWorkRunPathSafe(file *os.File, info fs.FileInfo) bool {
	if file == nil || info == nil || openWindowsWorkRunFileUnsafe(file) {
		return false
	}
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	return err == nil && privateWorkRunSecurityDescriptorSafe(descriptor, info.IsDir())
}

func openWorkRunPathNoFollow(path string, directory bool) (*os.File, error) {
	return openWindowsWorkRunPath(
		path,
		directory,
		windows.GENERIC_READ|windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
	)
}

func openWindowsWorkRunPath(path string, directory bool, access uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if directory {
		flags |= windows.FILE_FLAG_BACKUP_SEMANTICS
	}
	handle, err := windows.CreateFile(
		name,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		flags,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if openWindowsWorkRunFileUnsafe(file) {
		_ = file.Close()
		return nil, errUnsafeWorkRunPath
	}
	return file, nil
}

func openWindowsWorkRunFileUnsafe(file *os.File) bool {
	if file == nil {
		return true
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return true
	}
	return info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func privateWorkRunSecurityDescriptorSafe(
	descriptor *windows.SECURITY_DESCRIPTOR,
	directory bool,
) bool {
	if descriptor == nil || !descriptor.IsValid() {
		return false
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PRESENT == 0 ||
		control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		return false
	}
	currentUser, err := currentWorkRunWindowsUserSID()
	if err != nil || !owner.Equals(currentUser) {
		return false
	}
	dacl, defaulted, err := descriptor.DACL()
	if err != nil || dacl == nil || defaulted || dacl.AceCount != 1 {
		return false
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil || ace == nil ||
		ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return false
	}
	wantFlags := uint8(0)
	if directory {
		wantFlags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	if ace.Header.AceFlags != wantFlags || !ownerOnlyWorkRunWindowsAccessMask(ace.Mask) {
		return false
	}
	const sidOffset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
	if uintptr(ace.Header.AceSize) < sidOffset+unsafe.Sizeof(ace.SidStart) {
		return false
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	return aceSID.IsValid() &&
		uintptr(ace.Header.AceSize) >= sidOffset+uintptr(aceSID.Len()) &&
		aceSID.Equals(currentUser)
}

func ownerOnlyWorkRunSecurityDescriptor(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	currentUser, err := currentWorkRunWindowsUserSID()
	if err != nil {
		return nil, err
	}
	sid := currentUser.String()
	if sid == "" {
		return nil, errors.New("current Windows user SID is unavailable")
	}
	inheritance := ""
	if directory {
		inheritance = "OICI"
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"O:" + sid + "D:P(A;" + inheritance + ";GA;;;" + sid + ")",
	)
	if err != nil || descriptor == nil || !descriptor.IsValid() {
		if err != nil {
			return nil, fmt.Errorf("build owner-only work run DACL: %w", err)
		}
		return nil, errors.New("owner-only work run DACL is invalid")
	}
	return descriptor, nil
}

func currentWorkRunWindowsUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		if err != nil {
			return nil, fmt.Errorf("resolve current Windows user SID: %w", err)
		}
		return nil, errors.New("current Windows user SID is invalid")
	}
	sid, err := user.User.Sid.Copy()
	if err != nil {
		return nil, fmt.Errorf("copy current Windows user SID: %w", err)
	}
	return sid, nil
}

func ownerOnlyWorkRunWindowsAccessMask(mask windows.ACCESS_MASK) bool {
	const fileAllAccess windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED |
		windows.SYNCHRONIZE | windows.ACCESS_MASK(0x1ff)
	return mask == windows.GENERIC_ALL || mask == fileAllAccess
}
