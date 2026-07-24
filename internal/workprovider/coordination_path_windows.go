//go:build windows

package workprovider

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func coordinationPathUnsafe(path string, info fs.FileInfo) bool {
	if path == "" || info == nil || info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(name)
	return err != nil ||
		attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func createPrivateCoordinationDirectory(path string) (bool, error) {
	descriptor, err := ownerOnlyCoordinationSecurityDescriptor(true)
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
	if err := validatePrivateCoordinationDirectory(path); err != nil {
		if created {
			_ = os.Remove(path)
		}
		return false, err
	}
	return created, nil
}

func createPrivateCoordinationFile(path string) (*os.File, error) {
	descriptor, err := ownerOnlyCoordinationSecurityDescriptor(false)
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
	opened, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	if statErr != nil ||
		pathErr != nil ||
		!opened.Mode().IsRegular() ||
		!os.SameFile(opened, current) ||
		coordinationPathUnsafe(path, current) ||
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

func privateCoordinationPathSafe(path string, info fs.FileInfo) bool {
	if path == "" || info == nil || coordinationPathUnsafe(path, info) {
		return false
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	return err == nil &&
		privateCoordinationSecurityDescriptorSafe(
			descriptor,
			info.IsDir(),
		)
}

func privateOpenCoordinationPathSafe(
	file *os.File,
	info fs.FileInfo,
) bool {
	if file == nil || info == nil || openWindowsCoordinationFileUnsafe(file) {
		return false
	}
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	return err == nil &&
		privateCoordinationSecurityDescriptorSafe(
			descriptor,
			info.IsDir(),
		)
}

func coordinationSharedDirectorySafe(
	path string,
	info fs.FileInfo,
) bool {
	if path == "" ||
		info == nil ||
		!info.IsDir() ||
		coordinationPathUnsafe(path, info) {
		return false
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION,
	)
	return err == nil &&
		coordinationSharedSecurityDescriptorOwnedByCurrentProcess(descriptor)
}

func coordinationSharedOpenDirectorySafe(
	file *os.File,
	info fs.FileInfo,
) bool {
	if file == nil ||
		info == nil ||
		!info.IsDir() ||
		openWindowsCoordinationFileUnsafe(file) {
		return false
	}
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION,
	)
	return err == nil &&
		coordinationSharedSecurityDescriptorOwnedByCurrentProcess(descriptor)
}

func openCoordinationPathNoFollow(
	path string,
	directory bool,
) (*os.File, error) {
	handle, err := openWindowsCoordinationObject(
		coordinationNTPath(path),
		directory,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if openWindowsCoordinationFileUnsafe(file) {
		_ = file.Close()
		return nil, errUnsafeCoordinationPath
	}
	return file, nil
}

func openWindowsCoordinationObject(
	objectPath string,
	directory bool,
) (windows.Handle, error) {
	open := func(path string) (windows.Handle, error) {
		objectName, err := windows.NewNTUnicodeString(path)
		if err != nil {
			return 0, err
		}
		attributes := &windows.OBJECT_ATTRIBUTES{
			Length:     uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
			ObjectName: objectName,
			Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
		}
		options := uint32(
			windows.FILE_SYNCHRONOUS_IO_NONALERT |
				windows.FILE_OPEN_REPARSE_POINT,
		)
		if directory {
			options |= windows.FILE_DIRECTORY_FILE
		} else {
			options |= windows.FILE_NON_DIRECTORY_FILE
		}
		var handle windows.Handle
		var status windows.IO_STATUS_BLOCK
		err = windows.NtCreateFile(
			&handle,
			windows.FILE_GENERIC_READ|windows.READ_CONTROL,
			attributes,
			&status,
			nil,
			windows.FILE_ATTRIBUTE_NORMAL,
			windows.FILE_SHARE_READ|
				windows.FILE_SHARE_WRITE|
				windows.FILE_SHARE_DELETE,
			windows.FILE_OPEN,
			options,
			0,
			0,
		)
		return handle, err
	}
	handle, err := open(objectPath)
	if !errors.Is(err, windows.STATUS_REPARSE_POINT_ENCOUNTERED) {
		return handle, err
	}
	directPath, resolveErr := directCoordinationDriveObjectPath(objectPath)
	if resolveErr != nil {
		return 0, fmt.Errorf(
			"resolve secure Windows coordination path after %w: %v",
			err,
			resolveErr,
		)
	}
	return open(directPath)
}

func openWindowsCoordinationFileUnsafe(file *os.File) bool {
	if file == nil {
		return true
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(
		windows.Handle(file.Fd()),
		&info,
	); err != nil {
		return true
	}
	return info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func privateCoordinationSecurityDescriptorSafe(
	descriptor *windows.SECURITY_DESCRIPTOR,
	directory bool,
) bool {
	if descriptor == nil || !descriptor.IsValid() {
		return false
	}
	control, _, err := descriptor.Control()
	if err != nil ||
		control&windows.SE_DACL_PRESENT == 0 ||
		control&windows.SE_DACL_PROTECTED == 0 ||
		!coordinationSecurityDescriptorOwnedByCurrentUser(descriptor) {
		return false
	}
	currentUser, err := currentCoordinationWindowsUserSID()
	if err != nil {
		return false
	}
	dacl, defaulted, err := descriptor.DACL()
	if err != nil ||
		dacl == nil ||
		defaulted ||
		dacl.AceCount != 1 {
		return false
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil ||
		ace == nil ||
		ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return false
	}
	wantFlags := uint8(0)
	if directory {
		wantFlags = windows.OBJECT_INHERIT_ACE |
			windows.CONTAINER_INHERIT_ACE
	}
	if ace.Header.AceFlags != wantFlags ||
		!ownerOnlyCoordinationWindowsAccessMask(ace.Mask) {
		return false
	}
	const sidOffset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
	if uintptr(ace.Header.AceSize) <
		sidOffset+unsafe.Sizeof(ace.SidStart) {
		return false
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	return aceSID.IsValid() &&
		uintptr(ace.Header.AceSize) >=
			sidOffset+uintptr(aceSID.Len()) &&
		aceSID.Equals(currentUser)
}

func coordinationSecurityDescriptorOwnedByCurrentUser(
	descriptor *windows.SECURITY_DESCRIPTOR,
) bool {
	if descriptor == nil || !descriptor.IsValid() {
		return false
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		return false
	}
	currentUser, err := currentCoordinationWindowsUserSID()
	return err == nil && owner.Equals(currentUser)
}

func coordinationSharedSecurityDescriptorOwnedByCurrentProcess(
	descriptor *windows.SECURITY_DESCRIPTOR,
) bool {
	if descriptor == nil || !descriptor.IsValid() {
		return false
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		return false
	}
	currentUser, err := currentCoordinationWindowsUserSID()
	if err == nil && owner.Equals(currentUser) {
		return true
	}
	tokenOwner, err := currentCoordinationWindowsTokenOwnerSID()
	return err == nil && owner.Equals(tokenOwner)
}

func ownerOnlyCoordinationSecurityDescriptor(
	directory bool,
) (*windows.SECURITY_DESCRIPTOR, error) {
	currentUser, err := currentCoordinationWindowsUserSID()
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
			return nil, fmt.Errorf(
				"build owner-only coordination DACL: %w",
				err,
			)
		}
		return nil, errors.New("owner-only coordination DACL is invalid")
	}
	return descriptor, nil
}

func currentCoordinationWindowsUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil ||
		user == nil ||
		user.User.Sid == nil ||
		!user.User.Sid.IsValid() {
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

type coordinationWindowsTokenOwner struct {
	Owner *windows.SID
}

func currentCoordinationWindowsTokenOwnerSID() (*windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	var size uint32
	err := windows.GetTokenInformation(
		token,
		windows.TokenOwner,
		nil,
		0,
		&size,
	)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) ||
		size < uint32(unsafe.Sizeof(coordinationWindowsTokenOwner{})) {
		if err != nil {
			return nil, fmt.Errorf(
				"resolve current Windows token owner size: %w",
				err,
			)
		}
		return nil, errors.New(
			"current Windows token owner has an invalid size",
		)
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(
		token,
		windows.TokenOwner,
		&buffer[0],
		size,
		&size,
	); err != nil {
		return nil, fmt.Errorf(
			"resolve current Windows token owner: %w",
			err,
		)
	}
	value := (*coordinationWindowsTokenOwner)(unsafe.Pointer(&buffer[0]))
	if value.Owner == nil || !value.Owner.IsValid() {
		return nil, errors.New("current Windows token owner SID is invalid")
	}
	owner, err := value.Owner.Copy()
	runtime.KeepAlive(buffer)
	if err != nil {
		return nil, fmt.Errorf("copy current Windows token owner SID: %w", err)
	}
	return owner, nil
}

func ownerOnlyCoordinationWindowsAccessMask(
	mask windows.ACCESS_MASK,
) bool {
	const fileAllAccess windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED |
		windows.SYNCHRONIZE |
		windows.ACCESS_MASK(0x1ff)
	return mask == windows.GENERIC_ALL || mask == fileAllAccess
}

func directCoordinationDriveObjectPath(objectPath string) (string, error) {
	const dosDevicesPrefix = `\??\`
	if len(objectPath) < len(dosDevicesPrefix)+3 ||
		!strings.HasPrefix(objectPath, dosDevicesPrefix) ||
		!coordinationASCIILetter(objectPath[len(dosDevicesPrefix)]) ||
		objectPath[len(dosDevicesPrefix)+1] != ':' ||
		objectPath[len(dosDevicesPrefix)+2] != '\\' {
		return "", fmt.Errorf(
			"object path %q is not an absolute local drive path",
			objectPath,
		)
	}
	driveLetter := objectPath[len(dosDevicesPrefix)]
	if driveLetter >= 'a' && driveLetter <= 'z' {
		driveLetter -= 'a' - 'A'
	}
	device := string([]byte{driveLetter, ':'})
	targets, err := queryCoordinationDosDevice(device)
	if err != nil {
		return "", err
	}
	if len(targets) != 1 {
		return "", fmt.Errorf(
			"QueryDosDevice(%q) returned %d targets",
			device,
			len(targets),
		)
	}
	const devicePrefix = `\Device\`
	target := targets[0]
	if len(target) <= len(devicePrefix) ||
		!strings.EqualFold(target[:len(devicePrefix)], devicePrefix) {
		return "", fmt.Errorf(
			"QueryDosDevice(%q) returned non-device target %q",
			device,
			target,
		)
	}
	deviceName := target[len(devicePrefix):]
	if strings.ContainsAny(deviceName, `\/`) ||
		deviceName == "." ||
		deviceName == ".." {
		return "", fmt.Errorf(
			"QueryDosDevice(%q) returned non-direct target %q",
			device,
			target,
		)
	}
	return target + objectPath[len(dosDevicesPrefix)+2:], nil
}

func queryCoordinationDosDevice(device string) ([]string, error) {
	deviceName, err := windows.UTF16PtrFromString(device)
	if err != nil {
		return nil, err
	}
	buffer := make([]uint16, 32*1024)
	count, err := windows.QueryDosDevice(
		deviceName,
		&buffer[0],
		uint32(len(buffer)),
	)
	if err != nil {
		return nil, err
	}
	if count == 0 || count > uint32(len(buffer)) {
		return nil, errors.New("QueryDosDevice returned an invalid length")
	}
	targets := make([]string, 0, 1)
	start := 0
	for index, value := range buffer[:count] {
		if value != 0 {
			continue
		}
		if index == start {
			break
		}
		targets = append(
			targets,
			windows.UTF16ToString(buffer[start:index]),
		)
		start = index + 1
	}
	if start < int(count) && buffer[count-1] != 0 {
		targets = append(
			targets,
			windows.UTF16ToString(buffer[start:count]),
		)
	}
	return targets, nil
}

func coordinationASCIILetter(value byte) bool {
	return value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z'
}

func coordinationNTPath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\??\UNC\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	if strings.HasPrefix(path, `\\?\`) {
		return `\??\` + strings.TrimPrefix(path, `\\?\`)
	}
	if strings.HasPrefix(path, `\\`) {
		return `\??\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	return `\??\` + path
}
