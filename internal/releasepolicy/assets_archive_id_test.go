package releasepolicy

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/releaseartifact"
)

// TestAssetsArchiveIDMatchesReleaseArtifactContract guards the duplicated
// literal design D6 requires: policy.go is compiled in isolation by the
// release-policy verifier and cannot import internal/releaseartifact, so
// assetsArchiveID stays a bare string literal here. This test file is
// exempt from that isolation -- the verifier's bare-module copy only
// includes policy.go and releasepolicycmd/main.go, never _test.go files --
// so it is the one place allowed to import both packages and assert their
// literals never drift apart.
func TestAssetsArchiveIDMatchesReleaseArtifactContract(t *testing.T) {
	if assetsArchiveID != releaseartifact.AssetsArchiveID {
		t.Fatalf("internal/releasepolicy assetsArchiveID = %q, internal/releaseartifact.AssetsArchiveID = %q; the duplicated literal has drifted",
			assetsArchiveID, releaseartifact.AssetsArchiveID)
	}
}
