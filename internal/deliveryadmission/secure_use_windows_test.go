//go:build windows

package deliveryadmission

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestOwnerOnlyWindowsUseDescriptorsAreProtectedAndCurrentUserOnly(t *testing.T) {
	for _, directory := range []bool{false, true} {
		descriptor, err := ownerOnlyUseSecurityDescriptor(directory)
		if err != nil {
			t.Fatal(err)
		}
		if !privateWindowsUseDescriptorSafe(descriptor, directory) {
			t.Fatalf("owner-only descriptor for directory=%v failed validation", directory)
		}
	}
}

func TestWindowsUseDirectoryKeepsSharedNamespaceInheritedAndProtectsOnlyPADSubtree(t *testing.T) {
	parent, err := openAbsoluteWindowsUseDirectory(t.TempDir(), windowsUseDirectoryAccess)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	current, err := currentWindowsUseSID()
	if err != nil {
		t.Fatal(err)
	}
	sharedParent, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;GA;;;" + current.String() + ")(A;OICI;GA;;;SY)",
	)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sharedParent.DACL()
	if err != nil || dacl == nil {
		t.Fatal("shared parent DACL is unavailable")
	}
	if err := windows.SetSecurityInfo(
		windows.Handle(parent.Fd()),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatal(err)
	}

	shared, created, err := openOrCreateWindowsUseDirectory(parent, "gentle-ai", false)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	if !created {
		t.Fatal("shared namespace fixture already existed")
	}
	sharedDescriptor, err := windows.GetSecurityInfo(
		windows.Handle(shared.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	if privateWindowsUseDescriptorSafe(sharedDescriptor, true) {
		t.Fatal("shared .git/gentle-ai namespace was rewritten as PAD-private")
	}
	if !currentWindowsUseOwner(sharedDescriptor) {
		t.Fatal("shared namespace is not owned by the current user")
	}

	private, created, err := openOrCreateWindowsUseDirectory(
		shared,
		"delivery-authorization-uses",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer private.Close()
	if !created {
		t.Fatal("PAD-private namespace fixture already existed")
	}
	privateDescriptor, err := windows.GetSecurityInfo(
		windows.Handle(private.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !privateWindowsUseDescriptorSafe(privateDescriptor, true) {
		t.Fatal("PAD-owned subtree is not protected for the current user only")
	}
}

func TestWindowsUseDirectoryRejectsReparsePoints(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("Windows symlinks are unavailable: %v", err)
	}
	file, err := openAbsoluteWindowsUseDirectory(link, windowsUseDirectoryAccess)
	if err == nil {
		_ = file.Close()
		t.Fatal("Windows reparse-point directory was accepted")
	}
}

func TestWindowsUseDirectoryRejectsWeakenedDACLBeforeUse(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t, current.repository, authorization.Binding.Destination.RepositoryRef,
	)
	permissive, err := windows.SecurityDescriptorFromString("D:(A;;GA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := permissive.DACL()
	if err != nil || dacl == nil {
		t.Fatal("permissive DACL unavailable")
	}
	if err := windows.SetSecurityInfo(
		windows.Handle(store.secureDirectory.directoryFile.Fd()),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	current.repository.now++
	_, err = ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if err == nil {
		t.Fatal("weakened Windows authorization-use DACL was accepted")
	}
}

func TestWindowsUseDirectoryFailsClosedOnFlushFailure(t *testing.T) {
	current, authorizationRef, authorization := normalAuthorization(t)
	store := openScenarioUseStore(
		t, current.repository, authorization.Binding.Destination.RepositoryRef,
	)
	original := syncSecureUseDirectory
	syncSecureUseDirectory = func(*secureUseDirectory) error {
		return errors.New("injected Windows directory flush failure")
	}
	t.Cleanup(func() { syncSecureUseDirectory = original })
	current.repository.now++
	_, err := ConsumeAuthorization(
		context.Background(), current.repository, current.repository,
		authorizationRef, useRequest(authorization), store,
	)
	if err == nil || !strings.Contains(err.Error(), "flush failure") {
		t.Fatalf("Windows durability failure = %v, want fail-closed error", err)
	}
}
