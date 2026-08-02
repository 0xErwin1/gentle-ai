package releaseartifact

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// TestSemanticSnapshotEncodeDecodeRoundTrip proves SemanticSnapshot survives
// EncodeCanonical followed by a strict decode unchanged — the shape the
// released capabilities/review-integration-v2.semantic.json file must have
// (design D4, spec "Included Capability Surface").
func TestSemanticSnapshotEncodeDecodeRoundTrip(t *testing.T) {
	original := SemanticSnapshot{
		Schema:      "gentle-ai.review-integration.capabilities/v2",
		Contract:    "gentle-ai.review-integration/v2",
		Protocol:    SnapshotProtocol{Major: 2, Minor: 0},
		Operations:  []string{"review.capabilities", "review.start", "review.status"},
		Gates:       []string{"post-apply", "pre-commit", "pre-push", "pre-pr", "release"},
		Projections: []string{"staged", "workspace"},
		Schemas:     []string{"gentle-ai.review-receipt/v1", "gentle-ai.review-receipt/v2"},
		Features: SnapshotFeatures{
			Mandatory: []SnapshotFeature{
				{Name: "compact_v2_authority", Supported: true, Requires: []string{}},
			},
			Optional: []SnapshotFeature{
				{Name: "risk_reasons", Supported: true, Requires: []string{"repository_independent_capabilities"}},
			},
		},
		Bootstrap: &SnapshotBootstrap{
			Command: "gentle-ai review status --cwd <repo> --contract gentle-ai.review-integration/v2 --next-transition",
			TargetSelectorVariants: []SnapshotTargetSelector{
				{TargetType: "staged", Arguments: []string{"--projection", "staged"}},
			},
			RequiredFeature:    "native_next_transition",
			UnsupportedOutcome: "unsupported-capability",
			ParentOnly:         true,
		},
		Compatibility: SnapshotCompatibility{
			MinimumProtocolMajor: 2,
			MaximumProtocolMajor: 2,
			AdditiveMinorPolicy:  "optional-fields-only",
			UnknownMandatory:     "reject",
			UnknownOptional:      "ignore",
			Modes:                []string{"compact-v2"},
			LegacyWindow: SnapshotLegacyWindow{
				Mode: "legacy-v1", State: "active", ReadOnly: true, DeprecationStarted: true,
				Removal: "not-scheduled", MinimumCompatibilityReleases: 1,
			},
		},
	}

	encoded, err := EncodeCanonical(original)
	if err != nil {
		t.Fatalf("EncodeCanonical: %v", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var decoded SemanticSnapshot
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode canonical snapshot: %v", err)
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round trip mismatch:\noriginal: %+v\ndecoded:  %+v", original, decoded)
	}
}

// TestSemanticSnapshotEncodesEmptySlicesAsEmptyArray confirms the canonical
// encoder never turns an initialized empty slice into JSON null within a
// nested SemanticSnapshot field (design D3: "empty slices encode [] never
// null").
func TestSemanticSnapshotEncodesEmptySlicesAsEmptyArray(t *testing.T) {
	snapshot := SemanticSnapshot{
		Operations:  []string{},
		Gates:       []string{},
		Projections: []string{},
		Schemas:     []string{},
		Features: SnapshotFeatures{
			Mandatory: []SnapshotFeature{{Name: "x", Supported: true, Requires: []string{}}},
			Optional:  []SnapshotFeature{},
		},
		Compatibility: SnapshotCompatibility{Modes: []string{}},
	}

	encoded, err := EncodeCanonical(snapshot)
	if err != nil {
		t.Fatalf("EncodeCanonical: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"operations", "gates", "projections", "schemas"} {
		if raw[field] == nil {
			t.Errorf("field %q encoded as null, want []", field)
		}
	}
}
