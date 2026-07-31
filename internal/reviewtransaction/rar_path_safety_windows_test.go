//go:build windows

package reviewtransaction

import (
	"errors"
	"io/fs"
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

// ownerStubAlwaysReject forces rarRepositoryDirectorySafe to return false,
// enabling the classifier branch without requiring Administrator privileges
// and SetNamedSecurityInfo. This mirrors how rarWindowsDescriptorForOwner
// is used in the existing owner-regression tests.
func ownerStubAlwaysReject(path string, info fs.FileInfo) bool {
	return false // always "unsafe" → rarRepositoryDirectorySafe returns false → classifier is called
}

func TestRARWindowsAuthorityFilesystemClassifier(t *testing.T) {
	// Inject owner predicate that always rejects so the classifier path is
	// reached deterministically without SetNamedSecurityInfo or Administrator.
	origOwner := rarWindowsAuthorityOwnerUnsafe
	t.Cleanup(func() {
		rarWindowsAuthorityOwnerUnsafe = origOwner
	})
	rarWindowsAuthorityOwnerUnsafe = ownerStubAlwaysReject

	stub := &classifierStub{give: "NTFS"}
	origClassifier := rarWindowsAuthorityFilesystemClassifier
	t.Cleanup(func() {
		rarWindowsAuthorityFilesystemClassifier = origClassifier
	})
	rarWindowsAuthorityFilesystemClassifier = stub.classify

	// Any valid directory works — the owner stub forces the classifier path.
	dir := t.TempDir()

	tests := []struct {
		name            string
		path            string // if non-empty, use this path instead of dir (for UNC)
		fsType          string
		wantBase        error  // errors.Is check against errUnsafeRARAuthorityPath
		wantUnknown     bool   // true → error must be errUnknownWindowsFilesystemType
		wantUnsupported bool   // true → error must be errUnsupportedWindowsFilesystemType
		wantSubStr      string // substring that MUST appear
		wantNilStr      string // substring that MUST NOT appear
	}{
		{
			name:       "NTFS wrong owner returns ACL-capable error with takeown/icacls guidance",
			fsType:     "NTFS",
			wantBase:   errUnsafeRARAuthorityPath,
			wantSubStr: "takeown",
			wantNilStr: "exFAT",
		},
		{
			name:       "ReFS wrong owner returns ACL-capable error with takeown/icacls guidance",
			fsType:     "ReFS",
			wantBase:   errUnsafeRARAuthorityPath,
			wantSubStr: "icacls",
			wantNilStr: "exFAT",
		},
		{
			name:            "exFAT returns typed errUnsupportedWindowsFilesystem, no ACL guidance",
			fsType:          "exFAT",
			wantBase:        errUnsafeRARAuthorityPath,
			wantUnsupported: true,
			wantSubStr:      "exFAT",
			wantNilStr:      "takeown",
		},
		{
			name:            "FAT32 returns typed errUnsupportedWindowsFilesystem, no ACL guidance",
			fsType:          "FAT32",
			wantBase:        errUnsafeRARAuthorityPath,
			wantUnsupported: true,
			wantSubStr:      "FAT32",
			wantNilStr:      "icacls",
		},
		{
			name:        "empty classifier result (query failure) returns errUnknownWindowsFilesystem",
			fsType:      "",
			wantBase:    errUnsafeRARAuthorityPath,
			wantUnknown: true,
			wantNilStr:  "takeown",
		},
		{
			name:        "non-empty unknown filesystem (e.g. FOOFS) returns errUnknownWindowsFilesystem",
			fsType:      "FOOFS",
			wantBase:    errUnsafeRARAuthorityPath,
			wantUnknown: true,
			wantNilStr:  "takeown",
		},
		{
			name:        "UNC path (\\\\server\\share) fails closed returning errUnknownWindowsFilesystem",
			path:        `\\server\share`,
			fsType:      "NTFS", // classifier would say NTFS but UNC check fires first
			wantBase:    errUnsafeRARAuthorityPath,
			wantUnknown: true,
			wantNilStr:  "takeown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testDir := dir
			if tc.path != "" {
				testDir = tc.path + `\temp` // e.g. \\server\share\temp
			}

			stub.give = tc.fsType
			stub.calls = nil

			err := validateRARRepositoryParent(testDir)

			// Base sentinel check.
			if !errors.Is(err, tc.wantBase) {
				t.Fatalf("validateRARRepositoryParent(%q) err = %v, want errors.Is(_, %v)", testDir, err, tc.wantBase)
			}

			// Typed error classification (FS-3: explicit classification).
			if tc.wantUnknown {
				if !errors.Is(err, errUnknownWindowsFilesystem) {
					t.Fatalf("validateRARRepositoryParent(%q) err = %v, want errors.Is(_, errUnknownWindowsFilesystem)", testDir, err)
				}
			}
			if tc.wantUnsupported {
				if !errors.Is(err, errUnsupportedWindowsFilesystem) {
					t.Fatalf("validateRARRepositoryParent(%q) err = %v, want errors.Is(_, errUnsupportedWindowsFilesystem)", testDir, err)
				}
			}

			// Content assertions.
			if tc.wantSubStr != "" && !strings.Contains(err.Error(), tc.wantSubStr) {
				t.Fatalf("validateRARRepositoryParent(%q) error %q does not contain %q", testDir, err.Error(), tc.wantSubStr)
			}
			if tc.wantNilStr != "" && strings.Contains(err.Error(), tc.wantNilStr) {
				t.Fatalf("validateRARRepositoryParent(%q) error %q unexpectedly contains %q", testDir, err.Error(), tc.wantNilStr)
			}

			// Classifier invocation check (skip for UNC case where isUNC fires before classifier).
			if tc.path == "" && (len(stub.calls) != 1 || stub.calls[0] != testDir) {
				t.Fatalf("classifier called with calls=%v, want [%q]", stub.calls, testDir)
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
	origClassifier := rarWindowsAuthorityFilesystemClassifier
	t.Cleanup(func() {
		rarWindowsAuthorityFilesystemClassifier = origClassifier
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
