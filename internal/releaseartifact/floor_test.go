package releaseartifact_test

// This file uses the external releaseartifact_test package (not the
// internal releaseartifact test files used elsewhere in this directory)
// because it imports internal/cli to obtain the LIVE projection to check
// RequiredFloor against. internal/cli imports internal/releaseartifact
// (design D4, one-directional cli -> releaseartifact); a whitebox test file
// inside "package releaseartifact" importing internal/cli would be a real
// import cycle. The external test package breaks that cycle: it is a
// separate compilation unit linked only into this test binary.

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v2/internal/releaseartifact"
)

// TestRequiredFloorIsSubsetOfLiveProjection proves the frozen RequiredFloor
// is presently a subset of the real generated snapshot for both negotiated
// contracts (design D4, spec "Required-Floor Compatibility").
func TestRequiredFloorIsSubsetOfLiveProjection(t *testing.T) {
	for _, contract := range []string{cli.ReviewIntegrationContractV1, cli.ReviewIntegrationContractV2} {
		t.Run(contract, func(t *testing.T) {
			snapshot := cli.ReleaseSemanticSnapshot(contract)
			if err := releaseartifact.VerifyRequiredFloor(snapshot); err != nil {
				t.Fatalf("required floor is not a subset of the live %s projection: %v", contract, err)
			}
		})
	}
}

// TestRequiredFloorRemovalFails proves a removal from the live surface is
// NOT invisible: dropping one required-floor operation from an otherwise
// real, live snapshot must fail VerifyRequiredFloor.
func TestRequiredFloorRemovalFails(t *testing.T) {
	snapshot := cli.ReleaseSemanticSnapshot(cli.ReviewIntegrationContractV1)

	const removedOperation = "review.status"
	filtered := removeString(snapshot.Operations, removedOperation)
	if len(filtered) != len(snapshot.Operations)-1 {
		t.Fatalf("test fixture did not remove the expected floor operation %q from the live snapshot", removedOperation)
	}
	snapshot.Operations = filtered

	if err := releaseartifact.VerifyRequiredFloor(snapshot); err == nil {
		t.Fatalf("expected VerifyRequiredFloor to fail after removing required-floor operation %q, got nil", removedOperation)
	}
}

// TestRequiredFloorAdditionDoesNotFail proves adding a brand-new operation
// beyond the floor never breaks an existing consumer's floor check (spec
// "New optional operation does not break older consumer logic").
func TestRequiredFloorAdditionDoesNotFail(t *testing.T) {
	snapshot := cli.ReleaseSemanticSnapshot(cli.ReviewIntegrationContractV1)
	snapshot.Operations = append(append([]string{}, snapshot.Operations...), "review.a_future_operation")

	if err := releaseartifact.VerifyRequiredFloor(snapshot); err != nil {
		t.Fatalf("expected VerifyRequiredFloor to tolerate an addition beyond the floor, got: %v", err)
	}
}

func removeString(values []string, target string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == target {
			continue
		}
		out = append(out, v)
	}
	return out
}
