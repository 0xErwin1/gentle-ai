//go:build windows

package evidence

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func evidencePathUnsafe(path string, info fs.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(name)
	return err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func createEvidenceDirectory(path string, mode os.FileMode, private bool) (bool, error) {
	if !private {
		err := os.Mkdir(path, mode)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, fs.ErrExist):
			return false, nil
		default:
			return false, err
		}
	}

	descriptor, err := ownerOnlyWindowsSecurityDescriptor(true)
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
	switch {
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS):
		return false, nil
	case err != nil:
		return false, err
	}

	info, statErr := os.Lstat(path)
	if statErr != nil || !info.IsDir() || evidencePathUnsafe(path, info) ||
		!privateEvidencePathSafe(path, info) {
		_ = os.Remove(path)
		if statErr != nil {
			return false, statErr
		}
		return false, errors.New("created evidence directory ACL is not owner-only")
	}
	return true, nil
}

func securePrivateEvidencePath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if (!info.IsDir() && !info.Mode().IsRegular()) || evidencePathUnsafe(path, info) {
		return errors.New("evidence path is unavailable or unsafe")
	}
	descriptor, err := ownerOnlyWindowsSecurityDescriptor(info.IsDir())
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("owner-only evidence DACL is unavailable")
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("apply owner-only evidence DACL: %w", err)
	}
	runtime.KeepAlive(descriptor)

	info, err = os.Lstat(path)
	if err != nil {
		return err
	}
	if evidencePathUnsafe(path, info) || !privateEvidencePathSafe(path, info) {
		return errors.New("evidence path ACL is not owner-only")
	}
	return nil
}

func privateEvidencePathSafe(path string, info fs.FileInfo) bool {
	if path == "" || info == nil || evidencePathUnsafe(path, info) {
		return false
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil || descriptor == nil || !descriptor.IsValid() {
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
	currentUser, err := currentWindowsUserSID()
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
	if info.IsDir() {
		wantFlags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	if ace.Header.AceFlags != wantFlags || !ownerOnlyWindowsAccessMask(ace.Mask) {
		return false
	}
	const sidOffset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
	if uintptr(ace.Header.AceSize) < sidOffset+unsafe.Sizeof(ace.SidStart) {
		return false
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceSID.IsValid() ||
		uintptr(ace.Header.AceSize) < sidOffset+uintptr(aceSID.Len()) ||
		!aceSID.Equals(currentUser) {
		return false
	}
	return true
}

func ownerOnlyWindowsSecurityDescriptor(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	currentUser, err := currentWindowsUserSID()
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
			return nil, fmt.Errorf("build owner-only evidence DACL: %w", err)
		}
		return nil, errors.New("owner-only evidence DACL is invalid")
	}
	return descriptor, nil
}

func currentWindowsUserSID() (*windows.SID, error) {
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

func ownerOnlyWindowsAccessMask(mask windows.ACCESS_MASK) bool {
	const fileAllAccess windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED |
		windows.SYNCHRONIZE | windows.ACCESS_MASK(0x1ff)
	return mask == windows.GENERIC_ALL || mask == fileAllAccess
}
