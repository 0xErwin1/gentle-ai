//go:build windows

package reviewtransaction

import (
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
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	tests = append(tests, struct {
		name  string
		owner *windows.SID
		want  bool
	}{name: "Everyone", owner: world, want: false})

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
