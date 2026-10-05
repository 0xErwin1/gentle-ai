package shellinstaller

// PiTakeoverClaims are caller-authored assertions, never physical observations,
// consent, or verified rollback evidence. Even an empty proof list is a claim.
type PiTakeoverClaims struct {
	ExistingExecutable   string
	ExistingHome         string
	PhysicalInstanceID   string
	SourceDigest         string
	ExactActions         string
	ConsentBinding       string
	RollbackSnapshot     string
	InstalledBytes       string
	ExecutedLoad         string
	ClaimedMissingProofs []string
	ClaimedApproved      bool
	ClaimedReady         bool
	ClaimedExecutable    bool
}

type PiTakeoverReject uint8

const (
	RejectTakeoverCommand PiTakeoverReject = 1 << iota
	RejectTakeoverChannel
	RejectTakeoverMainSource
	RejectTakeoverClaimedAuthority
)

type PiTakeoverRequirement string

const (
	RequireTakeoverSourceAndToolchain PiTakeoverRequirement = "independent-source-and-toolchain"
	RequireTakeoverPhysicalInstance   PiTakeoverRequirement = "existing-physical-pi-instance"
	RequireTakeoverExactActions       PiTakeoverRequirement = "exact-action-scope"
	RequireTakeoverBoundConsent       PiTakeoverRequirement = "fresh-instance-source-actions-rollback-bound-consent"
	RequireTakeoverRollback           PiTakeoverRequirement = "audited-restorable-snapshot"
	RequireTakeoverDrift              PiTakeoverRequirement = "pre-apply-instance-and-source-drift-recheck"
	RequireTakeoverInstalledBytes     PiTakeoverRequirement = "installed-byte-identity"
	RequireTakeoverExecutedLoad       PiTakeoverRequirement = "executed-byte-and-load-witness"
	RequireTakeoverReady              PiTakeoverRequirement = "independent-ready-verification"
)

type PiTakeoverRefusalKind string

const PiTakeoverNotAuthorized PiTakeoverRefusalKind = "not-authorized"

// PiTakeoverRefusal is negative evidence only. A zero Rejected bitset is NOT
// approval: Kind refuses and Requirements remain nonempty for every input.
type PiTakeoverRefusal struct {
	Kind         PiTakeoverRefusalKind
	Channel      Channel // Syntax only; no source or executable was inspected.
	Rejected     PiTakeoverReject
	Requirements [9]PiTakeoverRequirement
}

// DescribePiTakeoverBoundary cannot inspect or authorize an existing Pi. In
// particular Profile.Validate accepting Pi syntax is not takeover permission.
// The legacy zero entry point cannot opt a user into replacing their Pi.
func DescribePiTakeoverBoundary(profile Profile, claims PiTakeoverClaims) PiTakeoverRefusal {
	result := PiTakeoverRefusal{
		Kind: PiTakeoverNotAuthorized,
		Requirements: [9]PiTakeoverRequirement{
			RequireTakeoverSourceAndToolchain,
			RequireTakeoverPhysicalInstance,
			RequireTakeoverExactActions,
			RequireTakeoverBoundConsent,
			RequireTakeoverRollback,
			RequireTakeoverDrift,
			RequireTakeoverInstalledBytes,
			RequireTakeoverExecutedLoad,
			RequireTakeoverReady,
		},
	}
	if profile.Channel.Valid() {
		result.Channel = profile.Channel
		if profile.Channel == ChannelMain {
			result.Rejected |= RejectTakeoverMainSource
		}
	} else {
		result.Rejected |= RejectTakeoverChannel
	}
	if profile.TerminalEntryPoint != TerminalEntryPointPi {
		result.Rejected |= RejectTakeoverCommand
	}
	if claims.ExistingExecutable != "" || claims.ExistingHome != "" ||
		claims.PhysicalInstanceID != "" || claims.SourceDigest != "" ||
		claims.ExactActions != "" || claims.ConsentBinding != "" ||
		claims.RollbackSnapshot != "" || claims.InstalledBytes != "" ||
		claims.ExecutedLoad != "" || claims.ClaimedMissingProofs != nil ||
		claims.ClaimedApproved || claims.ClaimedReady || claims.ClaimedExecutable {
		result.Rejected |= RejectTakeoverClaimedAuthority
	}
	return result
}
