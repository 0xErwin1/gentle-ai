//go:build windows

package deliveryadmission

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsUseDirectoryAccess = windows.FILE_GENERIC_READ |
		windows.FILE_GENERIC_WRITE |
		windows.READ_CONTROL |
		windows.WRITE_DAC |
		windows.SYNCHRONIZE
	windowsPrivateUseDirectoryAccess = windowsUseDirectoryAccess |
		windows.WRITE_OWNER
	windowsUseFileAccess = windows.FILE_GENERIC_READ |
		windows.FILE_GENERIC_WRITE |
		windows.DELETE |
		windows.READ_CONTROL |
		windows.WRITE_DAC |
		windows.WRITE_OWNER |
		windows.SYNCHRONIZE
	windowsFileCreated = 2
)

type windowsUseIdentity struct {
	volume uint32
	high   uint32
	low    uint32
}

type secureUseDirectory struct {
	commonFile     *os.File
	directoryFile  *os.File
	commonPath     string
	directoryPath  string
	commonIdentity windowsUseIdentity
	storeIdentity  windowsUseIdentity
}

type windowsFileRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

type windowsFileDispositionInformationEx struct {
	Flags uint32
}

func openSecureUseDirectory(
	commonPath string,
	directoryPath string,
	expectedCommon os.FileInfo,
) (*secureUseDirectory, error) {
	commonFile, err := openAbsoluteWindowsUseDirectory(commonPath, windowsUseDirectoryAccess)
	if err != nil {
		return nil, err
	}
	closeCommon := true
	defer func() {
		if closeCommon {
			_ = commonFile.Close()
		}
	}()
	openedCommon, err := commonFile.Stat()
	if err != nil {
		return nil, err
	}
	if expectedCommon == nil || !os.SameFile(expectedCommon, openedCommon) {
		return nil, fmt.Errorf("%w: Git common directory changed before secure open", ErrBindingMismatch)
	}
	commonIdentity, err := validateWindowsUseHandle(commonFile, true, false)
	if err != nil {
		return nil, err
	}

	components, err := secureUseDirectoryComponents(commonPath, directoryPath)
	if err != nil {
		return nil, err
	}
	current := commonFile
	for index, component := range components {
		next, created, openErr := openOrCreateWindowsUseDirectory(
			current, component, index > 0,
		)
		if openErr != nil {
			if current != commonFile {
				_ = current.Close()
			}
			return nil, openErr
		}
		if err := windows.FlushFileBuffers(windows.Handle(next.Fd())); err != nil {
			_ = next.Close()
			if current != commonFile {
				_ = current.Close()
			}
			return nil, fmt.Errorf("flush protected authorization-use directory: %w", err)
		}
		if created {
			if err := windows.FlushFileBuffers(windows.Handle(current.Fd())); err != nil {
				_ = next.Close()
				if current != commonFile {
					_ = current.Close()
				}
				return nil, fmt.Errorf("flush authorization-use parent directory: %w", err)
			}
		}
		if current != commonFile {
			_ = current.Close()
		}
		current = next
	}
	storeIdentity, err := validateWindowsUseHandle(current, true, true)
	if err != nil {
		_ = current.Close()
		return nil, err
	}
	closeCommon = false
	directory := &secureUseDirectory{
		commonFile: commonFile, directoryFile: current,
		commonPath: commonPath, directoryPath: directoryPath,
		commonIdentity: commonIdentity, storeIdentity: storeIdentity,
	}
	if err := directory.validate(); err != nil {
		_ = directory.close()
		return nil, err
	}
	if err := syncSecureUseDirectory(directory); err != nil {
		_ = directory.close()
		return nil, fmt.Errorf("flush secure authorization-use directory: %w", err)
	}
	return directory, nil
}

func openOrCreateWindowsUseDirectory(
	parent *os.File,
	name string,
	private bool,
) (*os.File, bool, error) {
	if parent == nil || !validUseStoreComponent(name) {
		return nil, false, fmt.Errorf("%w: invalid secure directory component", ErrInvalid)
	}
	var descriptor *windows.SECURITY_DESCRIPTOR
	var err error
	if private {
		descriptor, err = ownerOnlyUseSecurityDescriptor(true)
		if err != nil {
			return nil, false, err
		}
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, false, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: windows.Handle(parent.Fd()), ObjectName: objectName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
		SecurityDescriptor: descriptor,
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	access := uint32(windowsUseDirectoryAccess)
	if private {
		access = windowsPrivateUseDirectoryAccess
	}
	err = windows.NtCreateFile(
		&handle,
		access,
		attributes,
		&status,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN_IF,
		windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|
			windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	runtime.KeepAlive(descriptor)
	if err != nil {
		return nil, false, err
	}
	file := os.NewFile(uintptr(handle), name)
	created := status.Information == windowsFileCreated
	if private {
		if err := protectWindowsUseHandle(file, true); err != nil {
			_ = file.Close()
			return nil, false, err
		}
	}
	if _, err := validateWindowsUseHandle(file, true, private); err != nil {
		_ = file.Close()
		return nil, false, err
	}
	return file, created, nil
}

func (directory *secureUseDirectory) validate() error {
	if directory == nil || directory.commonFile == nil || directory.directoryFile == nil ||
		directory.commonPath == "" || directory.directoryPath == "" {
		return fmt.Errorf("%w: unopened secure use directory", ErrInvalid)
	}
	commonIdentity, err := validateWindowsUseHandle(directory.commonFile, true, false)
	if err != nil {
		return err
	}
	storeIdentity, err := validateWindowsUseHandle(directory.directoryFile, true, true)
	if err != nil {
		return err
	}
	if commonIdentity != directory.commonIdentity || storeIdentity != directory.storeIdentity {
		return fmt.Errorf("%w: open authorization-use directory identity changed", ErrBindingMismatch)
	}
	commonPathFile, err := openAbsoluteWindowsUseDirectory(
		directory.commonPath, windowsUseDirectoryAccess,
	)
	if err != nil {
		return err
	}
	commonPathIdentity, commonErr := validateWindowsUseHandle(commonPathFile, true, false)
	_ = commonPathFile.Close()
	if commonErr != nil {
		return commonErr
	}
	storePathFile, err := openAbsoluteWindowsUseDirectory(
		directory.directoryPath, windowsUseDirectoryAccess,
	)
	if err != nil {
		return err
	}
	storePathIdentity, storeErr := validateWindowsUseHandle(storePathFile, true, true)
	_ = storePathFile.Close()
	if storeErr != nil {
		return storeErr
	}
	if commonPathIdentity != directory.commonIdentity ||
		storePathIdentity != directory.storeIdentity {
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
	file, err := createPrivateWindowsUseFile(directory.directoryFile, temporary)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = deleteWindowsUseFile(file)
		}
		_ = file.Close()
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
	if _, err := validateWindowsUseHandle(file, false, true); err != nil {
		return err
	}
	if err := directory.validate(); err != nil {
		return err
	}
	if err := renameWindowsUseFile(
		file, windows.Handle(directory.directoryFile.Fd()), target, false,
	); err != nil {
		cleanupErr := deleteWindowsUseFile(file)
		if cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("remove authorization-use reservation: %w", cleanupErr))
		}
		renamed = true
		if windowsUseTargetExists(err) {
			return errSecureUseTargetExists
		}
		return err
	}
	renamed = true
	if err := syncSecureUseDirectory(directory); err != nil {
		return fmt.Errorf("flush published authorization use: %w", err)
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
	file, err := createPrivateWindowsUseFile(directory.directoryFile, temporary)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = deleteWindowsUseFile(file)
		}
		_ = file.Close()
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
	if _, err := validateWindowsUseHandle(file, false, true); err != nil {
		return err
	}
	if err := directory.validate(); err != nil {
		return err
	}
	if err := renameWindowsUseFile(
		file, windows.Handle(directory.directoryFile.Fd()), target, true,
	); err != nil {
		return err
	}
	renamed = true
	if err := syncSecureUseDirectory(directory); err != nil {
		return fmt.Errorf("flush replaced secure record: %w", err)
	}
	return directory.validate()
}

func createPrivateWindowsUseFile(parent *os.File, name string) (*os.File, error) {
	if parent == nil || !validUseStoreComponent(name) {
		return nil, fmt.Errorf("%w: invalid authorization-use reservation name", ErrInvalid)
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
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: windows.Handle(parent.Fd()), ObjectName: objectName,
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
		windows.FILE_CREATE,
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
		_ = deleteWindowsUseFile(file)
		_ = file.Close()
		return nil, err
	}
	if _, err := validateWindowsUseHandle(file, false, true); err != nil {
		_ = deleteWindowsUseFile(file)
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func renameWindowsUseFile(
	file *os.File,
	root windows.Handle,
	target string,
	replace bool,
) error {
	if file == nil || !validSecureRecordName(target) {
		return fmt.Errorf("%w: invalid authorization-use rename", ErrInvalid)
	}
	name, err := windows.UTF16FromString(target)
	if err != nil {
		return err
	}
	nameLength := (len(name) - 1) * 2
	var layout windowsFileRenameInformation
	size := int(unsafe.Offsetof(layout.FileName)) + nameLength
	buffer := make([]byte, size)
	information := (*windowsFileRenameInformation)(unsafe.Pointer(&buffer[0]))
	if replace {
		information.ReplaceIfExists = 1
	}
	information.RootDirectory = root
	information.FileNameLength = uint32(nameLength)
	copy(
		unsafe.Slice(&information.FileName[0], nameLength/2),
		name[:len(name)-1],
	)
	var status windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(
		windows.Handle(file.Fd()),
		&status,
		&buffer[0],
		uint32(len(buffer)),
		windows.FileRenameInformation,
	)
}

func deleteWindowsUseFile(file *os.File) error {
	if file == nil {
		return nil
	}
	information := windowsFileDispositionInformationEx{
		Flags: windows.FILE_DISPOSITION_DELETE |
			windows.FILE_DISPOSITION_POSIX_SEMANTICS |
			windows.FILE_DISPOSITION_IGNORE_READONLY_ATTRIBUTE,
	}
	var status windows.IO_STATUS_BLOCK
	err := windows.NtSetInformationFile(
		windows.Handle(file.Fd()),
		&status,
		(*byte)(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
		windows.FileDispositionInformationEx,
	)
	if err == nil {
		return nil
	}
	legacy := byte(1)
	var legacyStatus windows.IO_STATUS_BLOCK
	legacyErr := windows.NtSetInformationFile(
		windows.Handle(file.Fd()),
		&legacyStatus,
		&legacy,
		1,
		windows.FileDispositionInformation,
	)
	if legacyErr != nil {
		return errors.Join(err, legacyErr)
	}
	return nil
}

func windowsUseTargetExists(err error) bool {
	return errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) ||
		errors.Is(err, windows.STATUS_OBJECT_NAME_EXISTS) ||
		errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
		errors.Is(err, windows.ERROR_FILE_EXISTS)
}

func (directory *secureUseDirectory) readBounded(name string, maximum int) ([]byte, error) {
	if !validSecureRecordName(name) || maximum < 1 || maximum > maximumSecureRecordBytes {
		return nil, fmt.Errorf("%w: invalid authorization-use read", ErrInvalid)
	}
	if err := directory.validate(); err != nil {
		return nil, err
	}
	file, err := openRelativeWindowsUseFile(directory.directoryFile, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := validateWindowsUseHandle(file, false, true); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < 1 || info.Size() > int64(maximum) {
		return nil, fmt.Errorf("%w: authorization-use record byte bound", ErrUntrustedPreimage)
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

func openRelativeWindowsUseFile(parent *os.File, name string) (*os.File, error) {
	if parent == nil || !validSecureRecordName(name) {
		return nil, fmt.Errorf("%w: invalid authorization-use record name", ErrInvalid)
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: windows.Handle(parent.Fd()), ObjectName: objectName,
		Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&handle,
		windows.FILE_GENERIC_READ|windows.READ_CONTROL|windows.SYNCHRONIZE,
		attributes,
		&status,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|
			windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), name), nil
}

func openAbsoluteWindowsUseDirectory(path string, access uint32) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	handle, err := openWindowsUseDirectoryObject(windowsUseNTPath(absolute), access)
	if !errors.Is(err, windows.STATUS_REPARSE_POINT_ENCOUNTERED) {
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(handle), path), nil
	}
	direct, resolveErr := directWindowsUseDrivePath(windowsUseNTPath(absolute))
	if resolveErr != nil {
		return nil, fmt.Errorf("resolve secure Windows repository drive after %w: %v", err, resolveErr)
	}
	handle, err = openWindowsUseDirectoryObject(direct, access)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func openWindowsUseDirectoryObject(objectPath string, access uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(objectPath)
	if err != nil {
		return 0, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:     uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		ObjectName: objectName,
		Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&handle,
		access,
		attributes,
		&status,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|
			windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	return handle, err
}

func validateWindowsUseHandle(
	file *os.File,
	directory bool,
	private bool,
) (windowsUseIdentity, error) {
	if file == nil {
		return windowsUseIdentity{}, fmt.Errorf("%w: missing Windows authority handle", ErrInvalid)
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(
		windows.Handle(file.Fd()), &information,
	); err != nil {
		return windowsUseIdentity{}, err
	}
	isDirectory := information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if isDirectory != directory ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return windowsUseIdentity{}, fmt.Errorf("%w: Windows authority path type/reparse", ErrUntrustedPreimage)
	}
	fileType, err := windows.GetFileType(windows.Handle(file.Fd()))
	if err != nil || fileType != windows.FILE_TYPE_DISK {
		if err != nil {
			return windowsUseIdentity{}, err
		}
		return windowsUseIdentity{}, fmt.Errorf("%w: Windows authority path is not a disk file", ErrUntrustedPreimage)
	}
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return windowsUseIdentity{}, err
	}
	if private {
		if !privateWindowsUseDescriptorSafe(descriptor, directory) {
			return windowsUseIdentity{}, fmt.Errorf(
				"%w: Windows authority DACL (%s)",
				ErrUntrustedPreimage,
				privateWindowsUseDescriptorDiagnostic(descriptor, directory),
			)
		}
	} else if !currentWindowsSharedUseOwner(descriptor) {
		return windowsUseIdentity{}, fmt.Errorf("%w: Windows authority owner", ErrUntrustedPreimage)
	}
	return windowsUseIdentity{
		volume: information.VolumeSerialNumber,
		high:   information.FileIndexHigh,
		low:    information.FileIndexLow,
	}, nil
}

func protectWindowsUseHandle(file *os.File, directory bool) error {
	if file == nil {
		return fmt.Errorf("%w: missing Windows authority handle", ErrInvalid)
	}
	if _, err := validateWindowsUseHandle(file, directory, false); err != nil {
		return err
	}
	descriptor, err := ownerOnlyUseSecurityDescriptor(directory)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return errors.New("owner-only authorization-use DACL is unavailable")
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		return errors.New("owner-only authorization-use owner is unavailable")
	}
	// Harden the DACL before rebinding ownership. Directory ACL propagation and
	// ownership are separate Windows mutations; keeping them ordered also makes
	// an interrupted rebind fail closed with the current-user-only DACL applied.
	if err := windows.SetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|
			windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return err
	}
	if err := windows.SetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION,
		owner,
		nil,
		nil,
		nil,
	); err != nil {
		return err
	}
	runtime.KeepAlive(descriptor)
	_, err = validateWindowsUseHandle(file, directory, true)
	return err
}

func ownerOnlyUseSecurityDescriptor(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	currentUser, err := currentWindowsUseSID()
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
			return nil, err
		}
		return nil, errors.New("owner-only authorization-use DACL is invalid")
	}
	return descriptor, nil
}

func currentWindowsUseSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil || !user.User.Sid.IsValid() {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("current Windows user SID is invalid")
	}
	return user.User.Sid.Copy()
}

func currentWindowsUseOwner(descriptor *windows.SECURITY_DESCRIPTOR) bool {
	if descriptor == nil || !descriptor.IsValid() {
		return false
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		return false
	}
	current, err := currentWindowsUseSID()
	return err == nil && owner.Equals(current)
}

func currentWindowsSharedUseOwner(
	descriptor *windows.SECURITY_DESCRIPTOR,
) bool {
	if descriptor == nil || !descriptor.IsValid() {
		return false
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.IsValid() {
		return false
	}
	current, err := currentWindowsUseSID()
	if err == nil && owner.Equals(current) {
		return true
	}
	tokenOwner, err := currentWindowsUseTokenOwnerSID()
	return err == nil && owner.Equals(tokenOwner)
}

func privateWindowsUseDescriptorSafe(
	descriptor *windows.SECURITY_DESCRIPTOR,
	directory bool,
) bool {
	if !currentWindowsUseOwner(descriptor) {
		return false
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PRESENT == 0 ||
		control&windows.SE_DACL_PROTECTED == 0 {
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
	if ace.Header.AceFlags != wantFlags || !ownerOnlyWindowsUseAccessMask(ace.Mask) {
		return false
	}
	current, err := currentWindowsUseSID()
	if err != nil {
		return false
	}
	const sidOffset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
	if uintptr(ace.Header.AceSize) < sidOffset+unsafe.Sizeof(ace.SidStart) {
		return false
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	return aceSID.IsValid() &&
		uintptr(ace.Header.AceSize) >= sidOffset+uintptr(aceSID.Len()) &&
		aceSID.Equals(current)
}

func privateWindowsUseDescriptorDiagnostic(
	descriptor *windows.SECURITY_DESCRIPTOR,
	directory bool,
) string {
	if descriptor == nil || !descriptor.IsValid() {
		return "descriptor=invalid"
	}
	ownerCurrent := currentWindowsUseOwner(descriptor)
	control, _, controlErr := descriptor.Control()
	dacl, defaulted, daclErr := descriptor.DACL()
	if controlErr != nil || daclErr != nil || dacl == nil {
		return fmt.Sprintf(
			"owner-current=%t control-error=%t dacl-error=%t dacl-present=%t",
			ownerCurrent,
			controlErr != nil,
			daclErr != nil,
			dacl != nil,
		)
	}
	diagnostic := fmt.Sprintf(
		"owner-current=%t control=%#x dacl-defaulted=%t ace-count=%d",
		ownerCurrent,
		uint16(control),
		defaulted,
		dacl.AceCount,
	)
	if dacl.AceCount == 0 {
		return diagnostic
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil || ace == nil {
		return diagnostic + " first-ace=unavailable"
	}
	wantFlags := uint8(0)
	if directory {
		wantFlags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	current, currentErr := currentWindowsUseSID()
	const sidOffset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
	aceCurrent := false
	if currentErr == nil &&
		uintptr(ace.Header.AceSize) >= sidOffset+unsafe.Sizeof(ace.SidStart) {
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		aceCurrent = aceSID.IsValid() &&
			uintptr(ace.Header.AceSize) >= sidOffset+uintptr(aceSID.Len()) &&
			aceSID.Equals(current)
	}
	return fmt.Sprintf(
		"%s ace-type=%d ace-flags=%#x want-flags=%#x ace-mask=%#x ace-current=%t",
		diagnostic,
		ace.Header.AceType,
		ace.Header.AceFlags,
		wantFlags,
		uint32(ace.Mask),
		aceCurrent,
	)
}

type windowsUseTokenOwner struct {
	Owner *windows.SID
}

func currentWindowsUseTokenOwnerSID() (*windows.SID, error) {
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
		size < uint32(unsafe.Sizeof(windowsUseTokenOwner{})) {
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
	value := (*windowsUseTokenOwner)(unsafe.Pointer(&buffer[0]))
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

func ownerOnlyWindowsUseAccessMask(mask windows.ACCESS_MASK) bool {
	const fileAllAccess windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED |
		windows.SYNCHRONIZE | windows.ACCESS_MASK(0x1ff)
	return mask == windows.GENERIC_ALL || mask == fileAllAccess
}

func (directory *secureUseDirectory) sync() error {
	if directory == nil || directory.directoryFile == nil {
		return fmt.Errorf("%w: unopened secure use directory", ErrInvalid)
	}
	return windows.FlushFileBuffers(windows.Handle(directory.directoryFile.Fd()))
}

func (directory *secureUseDirectory) close() error {
	if directory == nil {
		return nil
	}
	var joined error
	if directory.directoryFile != nil {
		joined = errors.Join(joined, directory.directoryFile.Close())
		directory.directoryFile = nil
	}
	if directory.commonFile != nil {
		joined = errors.Join(joined, directory.commonFile.Close())
		directory.commonFile = nil
	}
	return joined
}

func windowsUseNTPath(path string) string {
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

func directWindowsUseDrivePath(objectPath string) (string, error) {
	const prefix = `\??\`
	if len(objectPath) < len(prefix)+3 ||
		!strings.HasPrefix(objectPath, prefix) ||
		!windowsUseASCIILetter(objectPath[len(prefix)]) ||
		objectPath[len(prefix)+1] != ':' ||
		objectPath[len(prefix)+2] != '\\' {
		return "", fmt.Errorf("object path %q is not an absolute local drive path", objectPath)
	}
	driveLetter := objectPath[len(prefix)]
	if driveLetter >= 'a' && driveLetter <= 'z' {
		driveLetter -= 'a' - 'A'
	}
	device := string([]byte{driveLetter, ':'})
	targets, err := queryWindowsUseDosDevice(device)
	if err != nil {
		return "", err
	}
	if len(targets) != 1 || !strings.HasPrefix(targets[0], `\Device\`) {
		return "", fmt.Errorf("drive %q has no unique direct device mapping", device)
	}
	deviceName := strings.TrimPrefix(targets[0], `\Device\`)
	if deviceName == "" || strings.ContainsAny(deviceName, `\/`) {
		return "", fmt.Errorf("drive %q has an unsafe device mapping", device)
	}
	return targets[0] + objectPath[len(prefix)+2:], nil
}

func queryWindowsUseDosDevice(device string) ([]string, error) {
	name, err := windows.UTF16PtrFromString(device)
	if err != nil {
		return nil, err
	}
	buffer := make([]uint16, 32*1024)
	n, err := windows.QueryDosDevice(name, &buffer[0], uint32(len(buffer)))
	if err != nil {
		return nil, err
	}
	if n == 0 || n > uint32(len(buffer)) {
		return nil, errors.New("QueryDosDevice returned an invalid length")
	}
	var targets []string
	start := 0
	for index, value := range buffer[:n] {
		if value != 0 {
			continue
		}
		if index == start {
			break
		}
		targets = append(targets, windows.UTF16ToString(buffer[start:index]))
		start = index + 1
	}
	return targets, nil
}

func windowsUseASCIILetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}
