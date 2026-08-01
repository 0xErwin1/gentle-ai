package cli

import "github.com/gentleman-programming/gentle-ai/v2/internal/releaseartifact"

// ReleaseSemanticSnapshot projects the already-verified-pure
// reviewCapabilitiesStaticSurface into the platform-independent release
// artifact semantic snapshot (design D4). The import direction is
// one-directional — internal/cli imports internal/releaseartifact, never
// the reverse — so internal/releaseartifact stays stdlib-only.
//
// This function restates nothing: every returned field is copied verbatim
// (or converted field-by-field, for nested types releaseartifact cannot
// import) from reviewCapabilitiesStaticSurface(contract). It never touches
// Package, Build, or Executable — the platform-dependent identity fields
// buildReviewCapabilities alone adds — so the live `review capabilities`
// response is completely unaffected by this projection existing.
func ReleaseSemanticSnapshot(contract string) releaseartifact.SemanticSnapshot {
	surface := reviewCapabilitiesStaticSurface(contract)
	return releaseartifact.SemanticSnapshot{
		Schema:        surface.Schema,
		Contract:      surface.Contract,
		Protocol:      releaseartifact.SnapshotProtocol{Major: surface.Protocol.Major, Minor: surface.Protocol.Minor},
		Operations:    surface.Operations,
		Gates:         surface.Gates,
		Projections:   surface.Projections,
		Schemas:       surface.Schemas,
		Features:      releaseSnapshotFeatures(surface.Features),
		Bootstrap:     releaseSnapshotBootstrap(surface.Bootstrap),
		Compatibility: releaseSnapshotCompatibility(surface.Compatibility),
	}
}

func releaseSnapshotFeatures(features ReviewCapabilitiesFeatures) releaseartifact.SnapshotFeatures {
	return releaseartifact.SnapshotFeatures{
		Mandatory: releaseSnapshotFeatureList(features.Mandatory),
		Optional:  releaseSnapshotFeatureList(features.Optional),
	}
}

func releaseSnapshotFeatureList(features []ReviewCapabilityFeature) []releaseartifact.SnapshotFeature {
	out := make([]releaseartifact.SnapshotFeature, len(features))
	for i, feature := range features {
		out[i] = releaseartifact.SnapshotFeature{
			Name: feature.Name, Supported: feature.Supported, Requires: feature.Requires,
		}
	}
	return out
}

func releaseSnapshotBootstrap(bootstrap *ReviewCapabilitiesBootstrap) *releaseartifact.SnapshotBootstrap {
	if bootstrap == nil {
		return nil
	}
	variants := make([]releaseartifact.SnapshotTargetSelector, len(bootstrap.TargetSelectorVariants))
	for i, variant := range bootstrap.TargetSelectorVariants {
		variants[i] = releaseartifact.SnapshotTargetSelector{TargetType: variant.TargetType, Arguments: variant.Arguments}
	}
	return &releaseartifact.SnapshotBootstrap{
		Command:                bootstrap.Command,
		TargetSelectorVariants: variants,
		RequiredFeature:        bootstrap.RequiredFeature,
		UnsupportedOutcome:     bootstrap.UnsupportedOutcome,
		ParentOnly:             bootstrap.ParentOnly,
	}
}

func releaseSnapshotCompatibility(compat ReviewCapabilitiesCompatibility) releaseartifact.SnapshotCompatibility {
	return releaseartifact.SnapshotCompatibility{
		MinimumProtocolMajor: compat.MinimumProtocolMajor,
		MaximumProtocolMajor: compat.MaximumProtocolMajor,
		AdditiveMinorPolicy:  compat.AdditiveMinorPolicy,
		UnknownMandatory:     compat.UnknownMandatory,
		UnknownOptional:      compat.UnknownOptional,
		Modes:                compat.Modes,
		LegacyWindow: releaseartifact.SnapshotLegacyWindow{
			Mode: compat.LegacyWindow.Mode, State: compat.LegacyWindow.State,
			ReadOnly: compat.LegacyWindow.ReadOnly, DeprecationStarted: compat.LegacyWindow.DeprecationStarted,
			Removal: compat.LegacyWindow.Removal, MinimumCompatibilityReleases: compat.LegacyWindow.MinimumCompatibilityReleases,
		},
	}
}
