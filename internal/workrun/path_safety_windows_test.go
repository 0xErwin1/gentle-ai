//go:build windows

package workrun

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateWorkRunWindowsACLAndReparseRejection(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "private")
	if _, err := createPrivateWorkRunDirectory(directory); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !privateWorkRunPathSafe(directory, info) {
		t.Fatal("created directory lacks a protected current-user-only DACL")
	}

	filePath := filepath.Join(directory, "record")
	file, err := createPrivateWorkRunFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("record\n"); err != nil {
		t.Fatal(err)
	}
	if !privateOpenWorkRunPathSafe(file, mustWorkRunFileInfo(t, file)) {
		t.Fatal("created file handle lacks a protected current-user-only DACL")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(directory, "reparse")
	if err := os.Symlink(filePath, link); err != nil {
		t.Skipf("Windows symlink privilege is unavailable: %v", err)
	}
	linkInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !workRunPathUnsafe(link, linkInfo) {
		t.Fatal("Windows reparse point was not classified as unsafe")
	}
	if _, err := openBoundedPrivateWorkRunFile(link, 32); err == nil {
		t.Fatal("Windows reparse point was opened as authority")
	}
}

func TestPrivateWorkRunWindowsRejectsBroadDACL(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "broad")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;GA;;;" + everyone.String() + ")",
	)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	runtime.KeepAlive(descriptor)
	if err := validatePrivateWorkRunDirectory(path); err == nil {
		t.Fatal("broad Windows DACL was accepted")
	}
	if err := securePrivateWorkRunPath(path); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateWorkRunDirectory(path); err != nil {
		t.Fatalf("secured Windows directory failed validation: %v", err)
	}
}

func mustWorkRunFileInfo(t *testing.T, file *os.File) os.FileInfo {
	t.Helper()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return info
}
