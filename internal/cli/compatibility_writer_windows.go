//go:build windows

package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"unsafe"

	"github.com/gentleman-programming/gentle-ai/v2/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"golang.org/x/sys/windows"
)

const maxCompatibilityAtomicFileSize = 16 << 20

type windowsCompatibilityDirectoryWriter struct {
	root   string
	rootFD windows.Handle
}

func newCompatibilityDirectoryWriter(homeDir, skillDir string) (compatibilityDirectoryWriter, error) {
	home, err := reviewtransaction.OpenPhysicalPath(homeDir, true)
	if err != nil {
		return nil, fmt.Errorf("open compatibility home directory %q: %w", homeDir, err)
	}
	defer home.Close()

	agentsFD, err := openWindowsCompatibilityDirectoryAt(windows.Handle(home.Fd()), ".agents", windows.FILE_OPEN)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(agentsFD)

	skillsFD, err := openWindowsCompatibilityDirectoryAt(agentsFD, "skills", windows.FILE_OPEN)
	if err != nil {
		return nil, err
	}
	return &windowsCompatibilityDirectoryWriter{root: skillDir, rootFD: skillsFD}, nil
}

func (w *windowsCompatibilityDirectoryWriter) Close() error {
	return windows.CloseHandle(w.rootFD)
}

func (w *windowsCompatibilityDirectoryWriter) Write(path string, content []byte, perm fs.FileMode) (filemerge.WriteResult, error) {
	parts, err := compatibilityPathParts(w.root, path)
	if err != nil {
		return filemerge.WriteResult{}, err
	}

	parentFD, err := w.openParent(parts[:len(parts)-1])
	if err != nil {
		return filemerge.WriteResult{}, err
	}
	if parentFD != w.rootFD {
		defer windows.CloseHandle(parentFD)
	}

	name := parts[len(parts)-1]
	existing, exists, err := readWindowsCompatibilityFile(parentFD, name)
	if err != nil {
		return filemerge.WriteResult{}, err
	}
	if exists && bytes.Equal(existing, content) {
		return filemerge.WriteResult{}, nil
	}

	_, tmp, err := createWindowsCompatibilityTempFile(parentFD, perm)
	if err != nil {
		return filemerge.WriteResult{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			removeWindowsCompatibilityTemp(tmp)
		}
		_ = tmp.Close()
	}()

	if _, err := tmp.Write(content); err != nil {
		return filemerge.WriteResult{}, fmt.Errorf("write compatibility temp file %q: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		return filemerge.WriteResult{}, fmt.Errorf("set compatibility temp file permissions for %q: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		return filemerge.WriteResult{}, fmt.Errorf("sync compatibility temp file %q: %w", path, err)
	}
	if err := renameWindowsCompatibilityFile(windows.Handle(tmp.Fd()), parentFD, name); err != nil {
		return filemerge.WriteResult{}, fmt.Errorf("replace compatibility destination %q: %w", path, err)
	}
	cleanup = false
	result := filemerge.WriteResult{Changed: true, Created: !exists}
	if err := tmp.Close(); err != nil {
		return result, fmt.Errorf("close compatibility temp file %q: %w", path, err)
	}

	readBack, readBackExists, err := readWindowsCompatibilityFile(parentFD, name)
	if err != nil {
		return result, err
	}
	if !readBackExists || !bytes.Equal(readBack, content) {
		// refusal:by-design world-action: publication did not produce the staged bytes at the anchored destination.
		return result, fmt.Errorf("replace compatibility destination %q: the replacement did not land", path)
	}
	return result, nil
}

func (w *windowsCompatibilityDirectoryWriter) openParent(parts []string) (windows.Handle, error) {
	currentFD := w.rootFD
	for _, part := range parts {
		nextFD, err := openWindowsCompatibilityDirectoryAt(currentFD, part, windows.FILE_OPEN)
		if windowsCompatibilityNotExist(err) {
			nextFD, err = openWindowsCompatibilityDirectoryAt(currentFD, part, windows.FILE_OPEN_IF)
		}
		if err != nil {
			if currentFD != w.rootFD {
				_ = windows.CloseHandle(currentFD)
			}
			return 0, fmt.Errorf("open physical compatibility directory %q: %w", part, err)
		}
		if currentFD != w.rootFD {
			_ = windows.CloseHandle(currentFD)
		}
		currentFD = nextFD
	}
	return currentFD, nil
}

func openWindowsCompatibilityDirectoryAt(parent windows.Handle, name string, disposition uint32) (windows.Handle, error) {
	return openWindowsCompatibilityObject(parent, name, true, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE, disposition)
}

func openWindowsCompatibilityFileAt(parent windows.Handle, name string, access uint32, disposition uint32) (windows.Handle, error) {
	return openWindowsCompatibilityObject(parent, name, false, access, disposition)
}

func openWindowsCompatibilityObject(parent windows.Handle, name string, directory bool, access uint32, disposition uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	options := uint32(windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_OPEN_REPARSE_POINT)
	fileAttributes := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
		fileAttributes = windows.FILE_ATTRIBUTE_DIRECTORY
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&handle,
		access,
		attributes,
		&status,
		nil,
		fileAttributes,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		disposition,
		options,
		0,
		0,
	)
	if err != nil {
		return 0, err
	}
	if err := validateWindowsCompatibilityHandle(handle, directory); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

func validateWindowsCompatibilityHandle(handle windows.Handle, directory bool) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	fileType, err := windows.GetFileType(handle)
	if err != nil {
		return err
	}
	if fileType != windows.FILE_TYPE_DISK || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		directory != (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) {
		// refusal:by-design world-action: physical-directory containment requires the opened handle not target a reparse point.
		return fmt.Errorf("compatibility target is not a physical %s", map[bool]string{true: "directory", false: "file"}[directory])
	}
	return nil
}

func readWindowsCompatibilityFile(parent windows.Handle, name string) ([]byte, bool, error) {
	handle, err := openWindowsCompatibilityFileAt(parent, name, windows.FILE_GENERIC_READ, windows.FILE_OPEN)
	if windowsCompatibilityNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open physical compatibility destination %q: %w", name, err)
	}
	file := os.NewFile(uintptr(handle), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("stat compatibility destination %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		// refusal:by-design world-action: a non-regular destination must be replaced before a safe atomic refresh can continue.
		return nil, false, fmt.Errorf("compatibility destination %q must be a regular file", name)
	}
	if info.Size() > maxCompatibilityAtomicFileSize {
		// refusal:by-design world-action: an oversized destination cannot be safely compared before replacement.
		return nil, false, fmt.Errorf("compatibility destination %q exceeds max atomic compare size %d bytes", name, maxCompatibilityAtomicFileSize)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCompatibilityAtomicFileSize+1))
	if err != nil {
		return nil, false, fmt.Errorf("read compatibility destination %q: %w", name, err)
	}
	if len(data) > maxCompatibilityAtomicFileSize {
		// refusal:by-design world-action: an oversized destination cannot be safely compared before replacement.
		return nil, false, fmt.Errorf("compatibility destination %q exceeds max atomic compare size %d bytes", name, maxCompatibilityAtomicFileSize)
	}
	return data, true, nil
}

func createWindowsCompatibilityTempFile(parent windows.Handle, perm fs.FileMode) (string, *os.File, error) {
	for range 10 {
		var token [12]byte
		if _, err := rand.Read(token[:]); err != nil {
			return "", nil, fmt.Errorf("generate compatibility temp file name: %w", err)
		}
		name := ".gentle-ai-" + hex.EncodeToString(token[:]) + ".tmp"
		handle, err := openWindowsCompatibilityFileAt(parent, name, windows.FILE_GENERIC_READ|windows.FILE_GENERIC_WRITE|windows.DELETE, windows.FILE_CREATE)
		if windowsCompatibilityAlreadyExists(err) {
			continue
		}
		if err != nil {
			return "", nil, fmt.Errorf("create compatibility temp file: %w", err)
		}
		file := os.NewFile(uintptr(handle), name)
		if err := file.Chmod(perm); err != nil {
			_ = file.Close()
			return "", nil, fmt.Errorf("set compatibility temp file permissions: %w", err)
		}
		return name, file, nil
	}
	// refusal:by-design world-action: repeated random-name collisions prevent a safe temporary file publication.
	return "", nil, fmt.Errorf("create compatibility temp file: exhausted unique names")
}

type windowsCompatibilityRenameInformation struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func renameWindowsCompatibilityFile(file, parent windows.Handle, name string) error {
	utf16Name, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	nameLength := (len(utf16Name) - 1) * 2
	var rename windowsCompatibilityRenameInformation
	bufferSize := int(unsafe.Offsetof(rename.FileName)) + nameLength
	buffer := make([]byte, bufferSize)
	info := (*windowsCompatibilityRenameInformation)(unsafe.Pointer(&buffer[0]))
	info.ReplaceIfExists = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.RootDirectory = parent
	info.FileNameLength = uint32(nameLength)
	copy(unsafe.Slice(&info.FileName[0], len(utf16Name)-1), utf16Name)
	var status windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(file, &status, &buffer[0], uint32(bufferSize), windows.FileRenameInformation)
}

func removeWindowsCompatibilityTemp(file *os.File) {
	if file == nil {
		return
	}
	deleteFile := byte(1)
	var status windows.IO_STATUS_BLOCK
	_ = windows.NtSetInformationFile(windows.Handle(file.Fd()), &status, &deleteFile, 1, windows.FileDispositionInformation)
}

func windowsCompatibilityNotExist(err error) bool {
	return errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) ||
		errors.Is(err, windows.STATUS_OBJECT_PATH_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}

func windowsCompatibilityAlreadyExists(err error) bool {
	return errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) ||
		errors.Is(err, windows.ERROR_FILE_EXISTS) ||
		errors.Is(err, windows.ERROR_ALREADY_EXISTS)
}

func compatibilityDestinationUnsafe(path string, info os.FileInfo) bool {
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
