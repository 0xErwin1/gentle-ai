package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/releaseartifact"
)

// excludedReviewCapabilitiesFields names the ReviewCapabilitiesResult fields
// the release semantic snapshot deliberately drops: platform/build/executable
// identity (spec "Excluded Fields"). This is the ONLY exclusion list in this
// test file — everything else in ReviewCapabilitiesResult must appear in
// releaseartifact.SemanticSnapshot, so an unnoticed new CLI field fails this
// test instead of silently widening or silently narrowing the snapshot.
var excludedReviewCapabilitiesFields = map[string]bool{
	"Package":    true,
	"Build":      true,
	"Executable": true,
}

// TestReviewCapabilitiesSnapshotFieldSetMatchesStaticSurfaceMinusExclusions is
// the anti-drift test (design D4, Testing Strategy "Unit — snapshot drift").
// It fails the moment a field is added to ReviewCapabilitiesResult and
// neither projected into SemanticSnapshot nor consciously added to the
// exclusion list above.
func TestReviewCapabilitiesSnapshotFieldSetMatchesStaticSurfaceMinusExclusions(t *testing.T) {
	want := make(map[string]bool)
	resultType := reflect.TypeOf(ReviewCapabilitiesResult{})
	for i := 0; i < resultType.NumField(); i++ {
		name := resultType.Field(i).Name
		if excludedReviewCapabilitiesFields[name] {
			continue
		}
		want[name] = true
	}

	got := make(map[string]bool)
	snapshotType := reflect.TypeOf(releaseartifact.SemanticSnapshot{})
	for i := 0; i < snapshotType.NumField(); i++ {
		got[snapshotType.Field(i).Name] = true
	}

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("projected snapshot field set drifted from ReviewCapabilitiesResult minus %v\nwant: %v\ngot:  %v", excludedReviewCapabilitiesFields, want, got)
	}
}

// TestReviewCapabilitiesSnapshotParity proves ReleaseSemanticSnapshot restates
// nothing: every projected field must equal exactly what
// reviewCapabilitiesStaticSurface(contract) already declares, for both
// negotiated contracts (design D4, Testing Strategy "Unit — snapshot
// parity"). Comparing the JSON projections (surface minus the excluded
// top-level keys) catches drift in nested Features/Bootstrap/Compatibility
// without duplicating the production conversion inside the test.
func TestReviewCapabilitiesSnapshotParity(t *testing.T) {
	for _, contract := range []string{ReviewIntegrationContractV1, ReviewIntegrationContractV2} {
		t.Run(contract, func(t *testing.T) {
			surface := reviewCapabilitiesStaticSurface(contract)
			snapshot := ReleaseSemanticSnapshot(contract)

			surfaceBytes, err := json.Marshal(surface)
			if err != nil {
				t.Fatalf("marshal surface: %v", err)
			}
			var surfaceMap map[string]any
			if err := json.Unmarshal(surfaceBytes, &surfaceMap); err != nil {
				t.Fatalf("unmarshal surface: %v", err)
			}
			delete(surfaceMap, "package")
			delete(surfaceMap, "build")
			delete(surfaceMap, "executable")

			snapshotBytes, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatalf("marshal snapshot: %v", err)
			}
			var snapshotMap map[string]any
			if err := json.Unmarshal(snapshotBytes, &snapshotMap); err != nil {
				t.Fatalf("unmarshal snapshot: %v", err)
			}

			if !reflect.DeepEqual(surfaceMap, snapshotMap) {
				t.Fatalf("snapshot for contract %s diverges from the static surface projection\nsurface:  %s\nsnapshot: %s", contract, surfaceBytes, snapshotBytes)
			}
		})
	}
}

// TestReviewCapabilitiesSnapshotExcludesHostIdentity is the exclusion test
// (design "Verified in this phase" D4, spec "No excluded field present").
// The canonical encoded bytes must contain none of these substrings for
// either negotiated contract — proving byte-identity is achievable
// regardless of which platform's binary produced the snapshot.
func TestReviewCapabilitiesSnapshotExcludesHostIdentity(t *testing.T) {
	excludedSubstrings := []string{
		"package", "build", "executable", "sha256",
		"vcs", "go_version", "module_version", "release_channel",
	}
	for _, contract := range []string{ReviewIntegrationContractV1, ReviewIntegrationContractV2} {
		snapshot := ReleaseSemanticSnapshot(contract)
		encoded, err := releaseartifact.EncodeCanonical(snapshot)
		if err != nil {
			t.Fatalf("EncodeCanonical(%s): %v", contract, err)
		}
		body := string(encoded)
		for _, excluded := range excludedSubstrings {
			if strings.Contains(body, excluded) {
				t.Errorf("contract %s: canonical snapshot bytes contain excluded substring %q:\n%s", contract, excluded, body)
			}
		}
	}
}

// TestReviewCapabilitiesSnapshotLeavesLiveReviewCapabilitiesUntouched confirms
// this projection is additive: the live `review capabilities` response
// (buildReviewCapabilities) is unaffected by ReleaseSemanticSnapshot's
// existence (design D4: "The live review capabilities response is
// untouched").
func TestReviewCapabilitiesSnapshotLeavesLiveReviewCapabilitiesUntouched(t *testing.T) {
	before, err := buildReviewCapabilities(ReviewIntegrationContractV1)
	if err != nil {
		t.Fatalf("buildReviewCapabilities: %v", err)
	}
	_ = ReleaseSemanticSnapshot(ReviewIntegrationContractV1)
	after, err := buildReviewCapabilities(ReviewIntegrationContractV1)
	if err != nil {
		t.Fatalf("buildReviewCapabilities: %v", err)
	}
	if before.Schema != after.Schema || before.Contract != after.Contract || !reflect.DeepEqual(before.Operations, after.Operations) {
		t.Fatal("calling ReleaseSemanticSnapshot mutated the live review capabilities surface")
	}
}
