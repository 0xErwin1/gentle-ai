//go:build windows

package workprovider

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestCoordinationSharedOwnerAcceptsOnlyCurrentWindowsPrincipals(
	t *testing.T,
) {
	currentUser, err := currentCoordinationWindowsUserSID()
	if err != nil {
		t.Fatal(err)
	}
	tokenOwner, err := currentCoordinationWindowsTokenOwnerSID()
	if err != nil {
		t.Fatal(err)
	}

	for name, owner := range map[string]*windows.SID{
		"token user":  currentUser,
		"token owner": tokenOwner,
	} {
		t.Run(name, func(t *testing.T) {
			descriptor := coordinationWindowsDescriptorForOwner(t, owner)
			if !coordinationSharedSecurityDescriptorOwnedByCurrentProcess(
				descriptor,
			) {
				t.Fatalf("shared descriptor owned by %s was rejected", name)
			}
		})
	}

	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	if coordinationSharedSecurityDescriptorOwnedByCurrentProcess(
		coordinationWindowsDescriptorForOwner(t, world),
	) {
		t.Fatal("shared descriptor owned by Everyone was accepted")
	}
}

func TestCoordinationPrivateOwnerRemainsTokenUserOnly(t *testing.T) {
	descriptor, err := ownerOnlyCoordinationSecurityDescriptor(false)
	if err != nil {
		t.Fatal(err)
	}
	if !privateCoordinationSecurityDescriptorSafe(descriptor, false) {
		t.Fatal("current-user-only private descriptor was rejected")
	}

	tokenOwner, err := currentCoordinationWindowsTokenOwnerSID()
	if err != nil {
		t.Fatal(err)
	}
	currentUser, err := currentCoordinationWindowsUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if tokenOwner.Equals(currentUser) {
		t.Skip("token owner and token user are identical")
	}
	if privateCoordinationSecurityDescriptorSafe(
		coordinationWindowsDescriptorForOwner(t, tokenOwner),
		false,
	) {
		t.Fatal("token-owner group was accepted for private provider state")
	}
}

func coordinationWindowsDescriptorForOwner(
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
