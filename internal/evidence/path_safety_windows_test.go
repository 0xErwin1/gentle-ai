//go:build windows

package evidence

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivatePathACLRejectsAdditionalPrincipal(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "private")
	created, err := createEvidenceDirectory(directory, 0o700, true)
	if err != nil || !created {
		t.Fatalf("createEvidenceDirectory() = %t, %v", created, err)
	}
	assertPrivateWindowsPath(t, directory)

	file := filepath.Join(directory, "record.json")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := securePrivateEvidencePath(file); err != nil {
		t.Fatal(err)
	}
	assertPrivateWindowsPath(t, file)

	currentUser, err := currentWindowsUserSID()
	if err != nil {
		t.Fatal(err)
	}
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	entries := []windows.EXPLICIT_ACCESS{
		ownerWindowsAccessEntry(currentUser, false),
		ownerWindowsAccessEntry(world, false),
	}
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		file,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(dacl)
	info, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if privateEvidencePathSafe(file, info) {
		t.Fatal("owner-only ACL validation accepted an additional world ACE")
	}
}

func TestWindowsPrivatePathACLRejectsInheritedDACL(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	created, err := createEvidenceDirectory(directory, 0o700, true)
	if err != nil || !created {
		t.Fatalf("createEvidenceDirectory() = %t, %v", created, err)
	}
	descriptor, err := ownerOnlyWindowsSecurityDescriptor(true)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		directory,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(descriptor)
	info, err := os.Lstat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if privateEvidencePathSafe(directory, info) {
		t.Fatal("owner-only ACL validation accepted an unprotected DACL")
	}
}

func assertPrivateWindowsPath(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !privateEvidencePathSafe(path, info) {
		t.Fatalf("%q does not have a protected owner-only ACL", path)
	}
}

func ownerWindowsAccessEntry(sid *windows.SID, directory bool) windows.EXPLICIT_ACCESS {
	inheritance := uint32(windows.NO_INHERITANCE)
	if directory {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}
