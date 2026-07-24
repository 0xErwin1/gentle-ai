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

func TestPrivateWindowsUseDescriptorAcceptsOnlyCanonicalDirectoryACESplit(
	t *testing.T,
) {
	current, err := currentWindowsUseSID()
	if err != nil {
		t.Fatal(err)
	}
	sid := current.String()
	tests := []struct {
		name      string
		dacl      string
		directory bool
		want      bool
	}{
		{
			name: "effective then inherit-only",
			dacl: "(A;;FA;;;" + sid + ")" +
				"(A;OICIIO;GA;;;" + sid + ")",
			directory: true,
			want:      true,
		},
		{
			name: "inherit-only then effective",
			dacl: "(A;OICIIO;GA;;;" + sid + ")" +
				"(A;;FA;;;" + sid + ")",
			directory: true,
			want:      true,
		},
		{
			name: "directory split rejected for file",
			dacl: "(A;;FA;;;" + sid + ")" +
				"(A;OICIIO;GA;;;" + sid + ")",
			directory: false,
			want:      false,
		},
		{
			name: "inheritance ACE must be inherit-only",
			dacl: "(A;;FA;;;" + sid + ")" +
				"(A;OICI;GA;;;" + sid + ")",
			directory: true,
			want:      false,
		},
		{
			name: "inheritance ACE must remain current-user-only",
			dacl: "(A;;FA;;;" + sid + ")" +
				"(A;OICIIO;GA;;;SY)",
			directory: true,
			want:      false,
		},
		{
			name: "additional ACE rejected",
			dacl: "(A;;FA;;;" + sid + ")" +
				"(A;OICIIO;GA;;;" + sid + ")" +
				"(A;;FA;;;" + sid + ")",
			directory: true,
			want:      false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor, err := windows.SecurityDescriptorFromString(
				"O:" + sid + "D:P" + test.dacl,
			)
			if err != nil {
				t.Fatal(err)
			}
			if got := privateWindowsUseDescriptorSafe(
				descriptor,
				test.directory,
			); got != test.want {
				t.Fatalf(
					"private descriptor accepted = %t, want %t",
					got,
					test.want,
				)
			}
		})
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
	if !currentWindowsSharedUseOwner(sharedDescriptor) {
		t.Fatal("shared namespace is not owned by the current token")
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

func TestWindowsUseSharedOwnerAcceptsOnlyCurrentPrincipals(t *testing.T) {
	currentUser, err := currentWindowsUseSID()
	if err != nil {
		t.Fatal(err)
	}
	tokenOwner, err := currentWindowsUseTokenOwnerSID()
	if err != nil {
		t.Fatal(err)
	}
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		owner *windows.SID
		want  bool
	}{
		{name: "token user", owner: currentUser, want: true},
		{name: "token owner", owner: tokenOwner, want: true},
		{name: "Everyone", owner: world, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor := windowsUseDescriptorForOwner(t, test.owner)
			if got := currentWindowsSharedUseOwner(descriptor); got != test.want {
				t.Fatalf("shared owner accepted = %t, want %t", got, test.want)
			}
		})
	}
}

func TestWindowsUsePrivateOwnerRemainsTokenUserOnly(t *testing.T) {
	descriptor, err := ownerOnlyUseSecurityDescriptor(false)
	if err != nil {
		t.Fatal(err)
	}
	if !privateWindowsUseDescriptorSafe(descriptor, false) {
		t.Fatal("current-user-only private descriptor was rejected")
	}

	tokenOwner, err := currentWindowsUseTokenOwnerSID()
	if err != nil {
		t.Fatal(err)
	}
	currentUser, err := currentWindowsUseSID()
	if err != nil {
		t.Fatal(err)
	}
	if tokenOwner.Equals(currentUser) {
		t.Skip("token owner and token user are identical")
	}
	if privateWindowsUseDescriptorSafe(
		windowsUseDescriptorForOwner(t, tokenOwner),
		false,
	) {
		t.Fatal("token-owner group was accepted for private PAD state")
	}
}

func TestProtectWindowsUseHandleRebindsTokenOwnerToTokenUser(t *testing.T) {
	currentUser, err := currentWindowsUseSID()
	if err != nil {
		t.Fatal(err)
	}
	tokenOwner, err := currentWindowsUseTokenOwnerSID()
	if err != nil {
		t.Fatal(err)
	}
	if tokenOwner.Equals(currentUser) {
		if os.Getenv("GENTLE_AI_REQUIRE_DISTINCT_WINDOWS_TOKEN_OWNER") == "1" {
			t.Fatal(
				"release-blocker environment must expose a TokenOwner distinct from TokenUser",
			)
		}
		t.Skip("token owner and token user are identical")
	}

	parent, err := openAbsoluteWindowsUseDirectory(
		t.TempDir(),
		windowsUseDirectoryAccess,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()

	for _, test := range []struct {
		name      string
		directory bool
	}{
		{name: "file", directory: false},
		{name: "directory", directory: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var file *os.File
			var err error
			if test.directory {
				var created bool
				file, created, err = openOrCreateWindowsUseDirectory(
					parent,
					"owner-rebinding-directory",
					true,
				)
				if err == nil && !created {
					t.Fatal("private directory fixture already existed")
				}
			} else {
				file, err = createPrivateWindowsUseFile(
					parent,
					"owner-rebinding-file",
				)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()

			if err := windows.SetSecurityInfo(
				windows.Handle(file.Fd()),
				windows.SE_FILE_OBJECT,
				windows.OWNER_SECURITY_INFORMATION,
				tokenOwner,
				nil,
				nil,
				nil,
			); err != nil {
				t.Fatal(err)
			}
			descriptor := windowsUseHandleDescriptor(t, file)
			owner, _, err := descriptor.Owner()
			if err != nil {
				t.Fatal(err)
			}
			if owner == nil || !owner.Equals(tokenOwner) {
				t.Fatal("private fixture owner was not changed to TokenOwner")
			}
			if privateWindowsUseDescriptorSafe(descriptor, test.directory) {
				t.Fatal("TokenOwner fixture was already accepted as PAD-private")
			}
			if _, err := validateWindowsUseHandle(
				file,
				test.directory,
				false,
			); err != nil {
				t.Fatalf("TokenOwner fixture is not a trusted shared preimage: %v", err)
			}

			if err := protectWindowsUseHandle(file, test.directory); err != nil {
				t.Fatal(err)
			}
			descriptor = windowsUseHandleDescriptor(t, file)
			owner, _, err = descriptor.Owner()
			if err != nil {
				t.Fatal(err)
			}
			if owner == nil || !owner.Equals(currentUser) {
				t.Fatal("PAD protection did not rebind owner to TokenUser")
			}
			if !privateWindowsUseDescriptorSafe(descriptor, test.directory) {
				t.Fatal("rebound fixture is not protected for TokenUser only")
			}
		})
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

func windowsUseDescriptorForOwner(
	t *testing.T,
	owner *windows.SID,
) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	if owner == nil || !owner.IsValid() || owner.String() == "" {
		t.Fatal("test owner SID is invalid")
	}
	sid := owner.String()
	descriptor, err := windows.SecurityDescriptorFromString(
		"O:" + sid + "D:P(A;;GA;;;" + sid + ")",
	)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor == nil || !descriptor.IsValid() {
		t.Fatal("test security descriptor is invalid")
	}
	return descriptor
}

func windowsUseHandleDescriptor(
	t *testing.T,
	file *os.File,
) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	if file == nil {
		t.Fatal("test Windows authority handle is nil")
	}
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor == nil || !descriptor.IsValid() {
		t.Fatal("test Windows authority security descriptor is invalid")
	}
	return descriptor
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
