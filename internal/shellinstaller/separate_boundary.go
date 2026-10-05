package shellinstaller

// SeparateBoundarySelectors are caller-supplied DATA, never path, environment,
// launcher, instance, authorization or filesystem observations by this package.
// Presence is explicit so an empty observed value cannot bypass a rejection.
type SeparateBoundarySelectors struct {
	ResolverEnvGentleShellPi         bool // GENTLE_SHELL_PI from the 3.7.0 resolver.
	OptionalPeerWithoutPhysicalProof bool
	PathFallback                     bool
	LinkFlag                         bool
	SavedLink                        bool
	ExplicitPath                     bool
	HomeFlag                         bool
	GentleShellHomeEnv               bool
	InheritedPiCodingAgentDir        bool
	UnresolvedDefaultHome            bool
	LifecyclePostinstall             bool
	LauncherAutoProvision            bool
	ProposedExecutable               string
	ProposedHome                     string
	ProposedLoad                     string
	ClaimedVerifiedExecutable        string
	ClaimedVerifiedHome              string
	ClaimedVerifiedLoad              string
	ClaimedApproved                  bool
	ClaimedReady                     bool
	ClaimedExecutable                bool
}

// Every bit is an observed reason to refuse, not evidence when absent.
type SeparateBoundaryReject uint32

const (
	RejectSeparateCommand SeparateBoundaryReject = 1 << iota
	RejectSeparateChannel
	RejectSeparateResolverEnv
	RejectSeparateOptionalPeer
	RejectSeparatePathFallback
	RejectSeparateLinkFlag
	RejectSeparateSavedLink
	RejectSeparateExplicitPath
	RejectSeparateHomeFlag
	RejectSeparateHomeEnv
	RejectSeparateInheritedPiDir
	RejectSeparateDefaultHome
	RejectSeparatePostinstall
	RejectSeparateAutoProvision
	RejectSeparateClaimedAuthority
)

type SeparateRequirement string

const (
	RequireSeparateSource       SeparateRequirement = "independent-channel-source"
	RequireSeparatePhysicalPeer SeparateRequirement = "physical-instance-proof"
	RequireSeparateBoundConsent SeparateRequirement = "instance-bound-consent"
	RequireSeparateExecutable   SeparateRequirement = "independent-dedicated-executable"
	RequireSeparateHome         SeparateRequirement = "independent-dedicated-home"
	RequireSeparateLoad         SeparateRequirement = "independent-load-witness"
	RequireSeparateRollback     SeparateRequirement = "rollback-and-quarantine"
	RequireSeparateReady        SeparateRequirement = "independent-ready-witness"
)

type SeparateBoundaryRefusalKind string

const SeparateBoundaryNotAuthorized SeparateBoundaryRefusalKind = "not-authorized"

// SeparateBoundaryRefusal cannot express Approved, Ready or an executable action.
// Rejected may be zero; Requirements never is, and Kind always refuses.
type SeparateBoundaryRefusal struct {
	Kind         SeparateBoundaryRefusalKind
	Channel      Channel // Stable/Main intent only; no resolved source.
	Rejected     SeparateBoundaryReject
	Requirements [8]SeparateRequirement
}

// DescribeSeparateBoundary is pure value-only negative evidence. The npm 3.7.0
// launcher is unverified DATA, never a trusted executable or a reusable route.
func DescribeSeparateBoundary(profile Profile, observedSelectors SeparateBoundarySelectors) SeparateBoundaryRefusal {
	result := SeparateBoundaryRefusal{
		Kind: SeparateBoundaryNotAuthorized,
		Requirements: [8]SeparateRequirement{
			RequireSeparateSource, RequireSeparatePhysicalPeer,
			RequireSeparateBoundConsent, RequireSeparateExecutable,
			RequireSeparateHome, RequireSeparateLoad,
			RequireSeparateRollback, RequireSeparateReady,
		},
	}
	if profile.Channel.Valid() {
		result.Channel = profile.Channel
	} else {
		result.Rejected |= RejectSeparateChannel
	}
	if profile.TerminalEntryPoint != TerminalEntryPointGentleShell {
		result.Rejected |= RejectSeparateCommand
	}
	if observedSelectors.ResolverEnvGentleShellPi {
		result.Rejected |= RejectSeparateResolverEnv
	}
	if observedSelectors.OptionalPeerWithoutPhysicalProof {
		result.Rejected |= RejectSeparateOptionalPeer
	}
	if observedSelectors.PathFallback {
		result.Rejected |= RejectSeparatePathFallback
	}
	if observedSelectors.LinkFlag {
		result.Rejected |= RejectSeparateLinkFlag
	}
	if observedSelectors.SavedLink {
		result.Rejected |= RejectSeparateSavedLink
	}
	if observedSelectors.ExplicitPath {
		result.Rejected |= RejectSeparateExplicitPath
	}
	if observedSelectors.HomeFlag {
		result.Rejected |= RejectSeparateHomeFlag
	}
	if observedSelectors.GentleShellHomeEnv {
		result.Rejected |= RejectSeparateHomeEnv
	}
	if observedSelectors.InheritedPiCodingAgentDir {
		result.Rejected |= RejectSeparateInheritedPiDir
	}
	if observedSelectors.UnresolvedDefaultHome {
		result.Rejected |= RejectSeparateDefaultHome
	}
	if observedSelectors.LifecyclePostinstall {
		result.Rejected |= RejectSeparatePostinstall
	}
	if observedSelectors.LauncherAutoProvision {
		result.Rejected |= RejectSeparateAutoProvision
	}
	if observedSelectors.ClaimedVerifiedExecutable != "" || observedSelectors.ClaimedVerifiedHome != "" ||
		observedSelectors.ClaimedVerifiedLoad != "" || observedSelectors.ClaimedApproved ||
		observedSelectors.ClaimedReady || observedSelectors.ClaimedExecutable {
		result.Rejected |= RejectSeparateClaimedAuthority
	}
	// Even syntactically absolute dedicated proposals are data, not a load witness.
	return result
}
