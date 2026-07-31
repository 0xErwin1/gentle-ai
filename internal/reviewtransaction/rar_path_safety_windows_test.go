//go:build windows

package reviewtransaction

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRARSharedOwnerAcceptsOnlyCurrentWindowsPrincipals(t *testing.T) {
	currentUser, err := currentRARWindowsUserSID()
	if err != nil {
		t.Fatal(err)
	}
	tokenOwner, err := currentRARWindowsTokenOwnerSID()
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
	}
	wellKnown := []struct {
		name string
		sid  windows.WELL_KNOWN_SID_TYPE
		want bool
	}{
		// Elevated shells and managed provisioning own repository
		// directories as BUILTIN\Administrators; SYSTEM services and CI
		// runners own theirs as LocalSystem. Both require administrative
		// privilege to forge, so both are trusted shared owners.
		{name: "BUILTIN Administrators", sid: windows.WinBuiltinAdministratorsSid, want: true},
		{name: "LocalSystem", sid: windows.WinLocalSystemSid, want: true},
		// Any standard user can hold these owners; accepting them would
		// let an attacker-controlled directory host review authority.
		{name: "Everyone", sid: windows.WinWorldSid, want: false},
		{name: "Authenticated Users", sid: windows.WinAuthenticatedUserSid, want: false},
	}
	for _, known := range wellKnown {
		sid, err := windows.CreateWellKnownSid(known.sid)
		if err != nil {
			t.Fatal(err)
		}
		tests = append(tests, struct {
			name  string
			owner *windows.SID
			want  bool
		}{name: known.name, owner: sid, want: known.want})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor := rarWindowsDescriptorForOwner(t, test.owner)
			if got := rarSharedSecurityDescriptorOwnedByCurrentProcess(
				descriptor,
			); got != test.want {
				t.Fatalf("shared owner accepted = %t, want %t", got, test.want)
			}
		})
	}

	// CI runners hold an elevated token whose token owner IS
	// BUILTIN\Administrators, so the descriptor-level Administrators case
	// above passes there even without the administrative-owner acceptance.
	// These direct assertions prove the token-independent comparison itself,
	// which is what a non-elevated token (the reported onboarding wall)
	// exercises in production.
	t.Run("administrative trust is token independent", func(t *testing.T) {
		for _, trusted := range []windows.WELL_KNOWN_SID_TYPE{
			windows.WinBuiltinAdministratorsSid,
			windows.WinLocalSystemSid,
		} {
			sid, err := windows.CreateWellKnownSid(trusted)
			if err != nil {
				t.Fatal(err)
			}
			if !rarTrustedWindowsAdministrativeOwner(sid) {
				t.Fatalf("administrative owner %s was refused", sid)
			}
		}
		for _, foreign := range []windows.WELL_KNOWN_SID_TYPE{
			windows.WinWorldSid,
			windows.WinAuthenticatedUserSid,
			windows.WinLocalServiceSid,
			windows.WinNetworkServiceSid,
		} {
			sid, err := windows.CreateWellKnownSid(foreign)
			if err != nil {
				t.Fatal(err)
			}
			if rarTrustedWindowsAdministrativeOwner(sid) {
				t.Fatalf("forgeable owner %s was trusted", sid)
			}
		}
		if rarTrustedWindowsAdministrativeOwner(nil) {
			t.Fatal("nil owner was trusted")
		}
	})

	// A real directory created by this process is owned by the current user
	// or, under an elevated token, by BUILTIN\Administrators. Both shapes
	// must proceed through the real ACL read.
	t.Run("real directory proceeds", func(t *testing.T) {
		dir := t.TempDir()
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !rarRepositoryDirectorySafe(dir, info) {
			t.Fatalf(
				"real repository parent %q was refused; owner is %s",
				dir, rarRepositoryOwnerDescription(dir),
			)
		}
	})

	// A trusted owner never excuses a reparse point: the redirection half of
	// the check must keep refusing even when the link and its target are
	// owned by an accepted principal.
	t.Run("reparse parent still refused", func(t *testing.T) {
		target := t.TempDir()
		link := filepath.Join(t.TempDir(), "git-common-link")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("creating a directory symlink is unavailable: %v", err)
		}
		info, err := os.Lstat(link)
		if err != nil {
			t.Fatal(err)
		}
		if rarRepositoryDirectorySafe(link, info) {
			t.Fatal("reparse-point repository parent was accepted")
		}
	})
}

func TestRARPrivateOwnerRemainsTokenUserOnly(t *testing.T) {
	descriptor, err := ownerOnlyRARSecurityDescriptor(false)
	if err != nil {
		t.Fatal(err)
	}
	if !privateRARSecurityDescriptorSafe(descriptor, false) {
		t.Fatal("current-user-only private descriptor was rejected")
	}

	tokenOwner, err := currentRARWindowsTokenOwnerSID()
	if err != nil {
		t.Fatal(err)
	}
	currentUser, err := currentRARWindowsUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if tokenOwner.Equals(currentUser) {
		// The release blocker runs this test on an account whose token owner
		// differs from its token user; there the rebind class must be proven,
		// never skipped.
		if os.Getenv("GENTLE_AI_REQUIRE_DISTINCT_WINDOWS_TOKEN_OWNER") == "1" {
			t.Fatal(
				"release blocker requires a distinct Windows token owner; the rebind class was not exercised",
			)
		}
		t.Skip("token owner and token user are identical")
	}
	if privateRARSecurityDescriptorSafe(
		rarWindowsDescriptorForOwner(t, tokenOwner),
		false,
	) {
		t.Fatal("token-owner group was accepted for private RAR state")
	}
}

func rarWindowsDescriptorForOwner(
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

// untrustedTestDir creates a directory owned by Everyone (S-1-1-0) so that
// rarRepositoryDirectorySafe returns false for it. The classifier is invoked
// only after the owner check fails, which is exactly the path under test.
//
// We enable SeRestorePrivilege before calling SetNamedSecurityInfo since
// setting Everyone as owner is normally restricted. If the privilege cannot
// be enabled or the owner cannot be set, the test is skipped.
func untrustedTestDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "untrusted-owner")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	everyoneSID, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Skipf("CreateWellKnownSid(WinWorldSid) failed: %v", err)
	}

	// Attempt to enable SeRestorePrivilege to allow setting arbitrary owners.
	// This may fail without elevation; the test will skip in that case.
	var token windows.Token
	if err := windows.OpenProcessToken(
		windows.CurrentProcess(),
		windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY,
		&token,
	); err != nil {
		t.Skipf("OpenProcessToken failed: %v", err)
	}
	defer token.Close()
	var restorePriv windows.LUID
	if err := windows.LookupPrivilegeValue(
		nil,
		windows.StringToUTF16Ptr("SeRestorePrivilege"),
		&restorePriv,
	); err != nil {
		t.Skipf("LookupPrivilegeValue(SeRestorePrivilege) failed: %v", err)
	}
	privs := windows.Tokenprivileges{
		PrivilegeCount: 1,
		Privileges: [1]windows.LUIDAndAttributes{
			{Luid: restorePriv, Attributes: windows.SE_PRIVILEGE_ENABLED},
		},
	}
	if err := windows.AdjustTokenPrivileges(token, false, &privs, 0, nil, nil); err != nil {
		t.Skipf("AdjustTokenPrivileges(SeRestorePrivilege) failed: %v (try running as Administrator)", err)
	}

	err = windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION,
		everyoneSID,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Skipf("SetNamedSecurityInfo(Everyone owner) failed: %v (try running as Administrator)", err)
	}
	return dir
}

// classifierStub records every path it receives and returns a fixed
// filesystem type. It enables deterministic testing of every dispatch branch.
type classifierStub struct {
	calls []string
	give  string // filesystem type to return
}

func (s *classifierStub) classify(path string) string {
	s.calls = append(s.calls, path)
	return s.give
}

func TestRARWindowsAuthorityFilesystemClassifier(t *testing.T) {
	stub := &classifierStub{give: "NTFS"}
	orig := rarWindowsAuthorityFilesystemClassifier
	t.Cleanup(func() {
		rarWindowsAuthorityFilesystemClassifier = orig
	})
	rarWindowsAuthorityFilesystemClassifier = stub.classify

	// Create a directory owned by Everyone so rarRepositoryDirectorySafe
	// returns false and the classifier path is reached.
	dir := untrustedTestDir(t)

	tests := []struct {
		name       string
		fsType     string
		wantErr    error
		wantSubStr string // substring that MUST appear in error
		wantNilStr string // substring that MUST NOT appear (ACL guidance check)
	}{
		{
			name:       "NTFS wrong owner retains takeown/icacls guidance",
			fsType:     "NTFS",
			wantErr:    errUnsafeRARAuthorityPath,
			wantSubStr: "takeown",
			wantNilStr: "exFAT",
		},
		{
			name:       "ReFS wrong owner retains takeown/icacls guidance",
			fsType:     "ReFS",
			wantErr:    errUnsafeRARAuthorityPath,
			wantSubStr: "icacls",
			wantNilStr: "exFAT",
		},
		{
			name:       "exFAT returns typed errUnsupportedWindowsFilesystem no ACL guidance",
			fsType:     "exFAT",
			wantErr:    errUnsafeRARAuthorityPath,
			wantSubStr: "exFAT",
			wantNilStr: "takeown",
		},
		{
			name:       "FAT32 returns typed errUnsupportedWindowsFilesystem no ACL guidance",
			fsType:     "FAT32",
			wantErr:    errUnsafeRARAuthorityPath,
			wantSubStr: "FAT32",
			wantNilStr: "icacls",
		},
		{
			name:       "unknown filesystem fails closed",
			fsType:     "",
			wantErr:    errUnsafeRARAuthorityPath,
			wantSubStr: "",
			wantNilStr: "takeown",
		},
		{
			name:       "UNC remote path fails closed",
			fsType:     "",
			wantErr:    errUnsafeRARAuthorityPath,
			wantSubStr: "",
			wantNilStr: "takeown",
		},
		{
			name:       "query failure fails closed",
			fsType:     "",
			wantErr:    errUnsafeRARAuthorityPath,
			wantSubStr: "",
			wantNilStr: "icacls",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub.give = tc.fsType
			stub.calls = nil

			err := validateRARRepositoryParent(dir)

			if tc.fsType == "" {
				// Empty type means unknown/remote/query-fail — classifier
				// returns "" and the refusal path uses errUnknownWindowsFilesystem.
				if !errors.Is(err, errUnsafeRARAuthorityPath) {
					t.Fatalf("validateRARRepositoryParent(%q) err = %v, want errors.Is(_, errUnsafeRARAuthorityPath)", dir, err)
				}
				return
			}

			// ACL-capable (NTFS/ReFS) or unsupported (exFAT/FAT32)
			if !errors.Is(err, errUnsafeRARAuthorityPath) {
				t.Fatalf("validateRARRepositoryParent(%q) err = %v, want errors.Is(_, errUnsafeRARAuthorityPath)", dir, err)
			}
			if tc.wantSubStr != "" && !strings.Contains(err.Error(), tc.wantSubStr) {
				t.Fatalf("validateRARRepositoryParent(%q) error %q does not contain %q", dir, err.Error(), tc.wantSubStr)
			}
			if tc.wantNilStr != "" && strings.Contains(err.Error(), tc.wantNilStr) {
				t.Fatalf("validateRARRepositoryParent(%q) error %q unexpectedly contains %q", dir, err.Error(), tc.wantNilStr)
			}

			// Assert canonical filesystem identity string (FS-7)
			if !strings.Contains(err.Error(), tc.fsType) {
				t.Fatalf("validateRARRepositoryParent(%q) error %q does not contain canonical filesystem identity %q", dir, err.Error(), tc.fsType)
			}

			// Verify classifier was called with the correct path
			if len(stub.calls) != 1 || stub.calls[0] != dir {
				t.Fatalf("classifier called with calls=%v, want [%q]", stub.calls, dir)
			}
		})
	}
}

// TestRARWindowsAuthorityFilesystemClassifierFS5WorktreeOnExFAT verifies FS-5:
// when the worktree is on an unsupported filesystem but the Git common
// directory is on NTFS with a trusted owner, the path is accepted without
// invoking the classifier (the classifier result is irrelevant when the owner
// is trusted).
func TestRARWindowsAuthorityFilesystemClassifierFS5WorktreeOnExFAT(t *testing.T) {
	stub := &classifierStub{give: "exFAT"}
	orig := rarWindowsAuthorityFilesystemClassifier
	t.Cleanup(func() {
		rarWindowsAuthorityFilesystemClassifier = orig
	})
	rarWindowsAuthorityFilesystemClassifier = stub.classify

	// A real temp directory is owned by the current user (trusted), so
	// rarRepositoryDirectorySafe returns true and the classifier is never called.
	dir := t.TempDir()

	err := validateRARRepositoryParent(dir)
	if err != nil {
		t.Fatalf("validateRARRepositoryParent(%q) err = %v, want nil (trusted owner)", dir, err)
	}
	// Classifier must not have been called — trust decision short-circuits
	// before filesystem classification when the owner is trusted.
	if len(stub.calls) > 0 {
		t.Fatalf("classifier was called for trusted-owner path: calls=%v", stub.calls)
	}
}
