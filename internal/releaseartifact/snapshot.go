package releaseartifact

// SemanticSnapshot is the deterministic, platform-independent projection of
// a review integration contract's declared capability surface (design D4).
// Its field set is exactly the internal/cli ReviewCapabilitiesResult field
// set minus Package, Build, and Executable — the identity fields that
// depend on which platform's binary generated the snapshot (spec "Excluded
// Fields"). internal/cli owns the one-directional projection that builds
// this value from its pure static capability surface; this package never
// imports internal/cli.
type SemanticSnapshot struct {
	Schema        string                `json:"schema"`
	Contract      string                `json:"contract"`
	Protocol      SnapshotProtocol      `json:"protocol"`
	Operations    []string              `json:"operations"`
	Gates         []string              `json:"gates"`
	Projections   []string              `json:"projections"`
	Schemas       []string              `json:"schemas"`
	Features      SnapshotFeatures      `json:"features"`
	Bootstrap     *SnapshotBootstrap    `json:"bootstrap,omitempty"`
	Compatibility SnapshotCompatibility `json:"compatibility"`
}

// SnapshotProtocol mirrors internal/cli's ReviewCapabilitiesProtocol.
type SnapshotProtocol struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

// SnapshotFeature mirrors internal/cli's ReviewCapabilityFeature.
type SnapshotFeature struct {
	Name      string   `json:"name"`
	Supported bool     `json:"supported"`
	Requires  []string `json:"requires"`
}

// SnapshotFeatures mirrors internal/cli's ReviewCapabilitiesFeatures.
type SnapshotFeatures struct {
	Mandatory []SnapshotFeature `json:"mandatory"`
	Optional  []SnapshotFeature `json:"optional"`
}

// SnapshotBootstrap mirrors internal/cli's ReviewCapabilitiesBootstrap.
type SnapshotBootstrap struct {
	Command                string                   `json:"command"`
	TargetSelectorVariants []SnapshotTargetSelector `json:"target_selector_variants"`
	RequiredFeature        string                   `json:"required_feature"`
	UnsupportedOutcome     string                   `json:"unsupported_outcome"`
	ParentOnly             bool                     `json:"parent_only"`
}

// SnapshotTargetSelector mirrors internal/cli's ReviewCapabilitiesTargetSelector.
type SnapshotTargetSelector struct {
	TargetType string   `json:"target_type"`
	Arguments  []string `json:"arguments"`
}

// SnapshotCompatibility mirrors internal/cli's ReviewCapabilitiesCompatibility.
type SnapshotCompatibility struct {
	MinimumProtocolMajor int                  `json:"minimum_protocol_major"`
	MaximumProtocolMajor int                  `json:"maximum_protocol_major"`
	AdditiveMinorPolicy  string               `json:"additive_minor_policy"`
	UnknownMandatory     string               `json:"unknown_mandatory"`
	UnknownOptional      string               `json:"unknown_optional"`
	Modes                []string             `json:"modes"`
	LegacyWindow         SnapshotLegacyWindow `json:"legacy_window"`
}

// SnapshotLegacyWindow mirrors internal/cli's ReviewCapabilitiesLegacyWindow.
type SnapshotLegacyWindow struct {
	Mode                         string `json:"mode"`
	State                        string `json:"state"`
	ReadOnly                     bool   `json:"read_only"`
	DeprecationStarted           bool   `json:"deprecation_started"`
	Removal                      string `json:"removal"`
	MinimumCompatibilityReleases int    `json:"minimum_compatibility_releases"`
}
