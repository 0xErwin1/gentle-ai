package releaseartifact_test

// This file uses the external releaseartifact_test package for the same
// reason as floor_test.go: it must import internal/cli to generate the
// real snapshot, and internal/cli imports internal/releaseartifact, so a
// whitebox "package releaseartifact" test file cannot do this without a
// real import cycle.

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/cli"
	"github.com/gentleman-programming/gentle-ai/v2/internal/releaseartifact"
)

var updateGolden = flag.Bool("update", false, "update golden files (go-testing skill golden rule: inspect the diff, then rerun without -update)")

// goldenSemanticSnapshotSHA256 guards against an accidental regeneration.
// Task A1b.10 requires generating this golden via -update, INSPECTING the
// diff by hand, then rerunning without -update and recording this exact
// sha256 — so any future unexamined `-update` run changes this constant
// too and this test fails until a human deliberately re-records it after
// inspecting the new diff.
const goldenSemanticSnapshotSHA256 = "da95ffdfba60acd234f482433b01a9dec7a85a7538a90df7a3d84e5236d39f80"

// TestSemanticSnapshotGoldenMatchesReviewIntegrationV2 asserts the
// canonical encoding of the real, live gentle-ai.review-integration/v2
// semantic snapshot against the checked-in golden file (design D4, Testing
// Strategy "Golden — snapshot"; spec "Platform-Independent Byte Identity").
func TestSemanticSnapshotGoldenMatchesReviewIntegrationV2(t *testing.T) {
	snapshot := cli.ReleaseSemanticSnapshot(cli.ReviewIntegrationContractV2)
	got, err := releaseartifact.EncodeCanonical(snapshot)
	if err != nil {
		t.Fatalf("EncodeCanonical: %v", err)
	}

	goldenPath := filepath.Join("testdata", "review-integration-v2.semantic.json")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", goldenPath, err)
		}
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v (generate it first with -update)", goldenPath, err)
	}
	if string(got) != string(want) {
		t.Fatalf("semantic snapshot bytes differ from golden %s; inspect the diff, then rerun `go test -run TestSemanticSnapshotGolden ./internal/releaseartifact/... -update` and record the new sha256\n--- got ---\n%s\n--- want (golden) ---\n%s", goldenPath, got, want)
	}

	sum := sha256.Sum256(want)
	gotSum := hex.EncodeToString(sum[:])
	if gotSum != goldenSemanticSnapshotSHA256 {
		t.Fatalf("golden %s sha256 = %s, want checked-in constant %s; this is a real regeneration and must be inspected by hand before the constant is updated, never accepted silently", goldenPath, gotSum, goldenSemanticSnapshotSHA256)
	}
}
