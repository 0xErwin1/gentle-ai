package reviewtransaction

import "strings"

// rarWindowsTokenPrincipals is the token-derived evidence the Windows RAR
// directory repair decides ownership from.
//
// Every field is a string SID rather than a *windows.SID on purpose. The
// syscalls that gather these values cannot run anywhere but Windows, but the
// rule they feed is the part that has to be right, and keeping the rule a pure
// function over plain strings is what lets it be executed by tests on every
// platform instead of shipped on the strength of a successful compile.
type rarWindowsTokenPrincipals struct {
	// User is TOKEN_USER: the account this process runs as.
	User string

	// DefaultOwner is TOKEN_OWNER: the SID Windows stamps as the owner of
	// every object this token creates when the creator supplies no explicit
	// owner in the security descriptor. It is NOT always the token user --
	// see rarWindowsRepairOwnerControlled.
	DefaultOwner string

	// OwnerEligible is every group SID in the token carrying SE_GROUP_OWNER:
	// exactly the principals Windows itself will accept from this token as an
	// owner SID. Mere membership is not enough and must not be treated as
	// enough: a filtered, non-elevated administrator's token carries
	// BUILTIN\Administrators as SE_GROUP_USE_FOR_DENY_ONLY rather than as an
	// owner-eligible group, and such a process genuinely cannot take
	// ownership of an Administrators-owned directory.
	OwnerEligible []string
}

// rarWindowsRepairOwnerControlled reports whether an existing directory is
// owned by a principal this process controls, which is the property the
// bounded repair actually needs.
//
// The predicate #3376 shipped asked a narrower question -- is the owner SID
// equal to the token user, or to the token's default owner -- and answered the
// wrong one. Windows assigns the owner of a newly created object from the
// creating token's DEFAULT OWNER, never from the token user, unless the
// creator handed CreateDirectory an explicit owner. On a token that belongs to
// a member of the Administrators group and is running elevated, that default
// owner is BUILTIN\Administrators unless the "System objects: Default owner
// for objects created by members of the Administrators group" policy
// (HKLM\SYSTEM\CurrentControlSet\Control\Lsa\NoDefaultAdminOwner) has been
// switched to the object creator. So a directory this very process created
// moments ago is routinely owned by a SID that is not the token user, and a
// directory an earlier elevated run created is routinely owned by a SID that
// is neither the token user nor the current token's default owner. Refusing
// those is what makes the clone-scope kill switch -- the documented
// self-service delivery exit -- unable to self-heal on ordinary Windows
// machines, not just in CI.
//
// Accepting them is not a loosening, because the accepted set is defined by
// Windows rather than by this file: it is precisely the set of SIDs this token
// could itself stamp as an owner. A different user's SID is never in it. The
// squattable well-known groups this guard exists to refuse -- Everyone
// (S-1-1-0) and Authenticated Users (S-1-5-11) -- are never owner-eligible
// groups in any token. NT AUTHORITY\SYSTEM clears the bar only when this
// process actually runs as SYSTEM. And a genuinely foreign owner still refuses
// with the same message and the same operator repair (takeown / icacls
// /setowner) as before.
//
// Nothing downstream is widened either. Clearing this precondition only earns
// the directory a repair that rewrites its owner to the token user and
// reapplies the protected owner-only DACL; the caller then revalidates with
// the unchanged strict predicate, which still demands owner-only current-token
// user. A directory that clears this check and cannot be brought to that state
// is still refused.
func rarWindowsRepairOwnerControlled(owner string, token rarWindowsTokenPrincipals) bool {
	candidate := strings.TrimSpace(owner)
	if candidate == "" {
		return false
	}
	controlled := make([]string, 0, len(token.OwnerEligible)+2)
	controlled = append(controlled, token.User, token.DefaultOwner)
	controlled = append(controlled, token.OwnerEligible...)
	for _, principal := range controlled {
		principal = strings.TrimSpace(principal)
		if principal == "" {
			continue
		}
		if strings.EqualFold(candidate, principal) {
			return true
		}
	}
	return false
}
