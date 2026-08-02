package releaseartifact

import "testing"

// knownVectorEntries is a fixed, checked-in test vector for the
// gentle-ai.release-artifact-tree/v1 preimage: the canonicalization tag,
// then per entry in ascending path order, "path\x00type\x00mode\x00size\x00digest\n".
// The expected hex below was computed independently (sha256 over that exact
// byte sequence) and is checked in as a constant so an implementation
// change to the preimage format shows up as a diff, not a silently
// regenerated value.
func knownVectorEntries() []Entry {
	return []Entry{
		// Declared out of sorted order on purpose: "z/file.txt" sorts
		// after "a/file.txt", so TreeDigest must sort internally to
		// reach the expected hash regardless of input order.
		{Path: "z/file.txt", Type: "file", Mode: "0644", Size: 10, Digest: "sha256:" + repeat("a", 64)},
		{Path: "a/file.txt", Type: "file", Mode: "0644", Size: 5, Digest: "sha256:" + repeat("b", 64)},
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

const knownVectorTreeDigest = "sha256:971ca2d598a9f6e517c00eee9581f40376f9fef61b8a905c310a2a8d71140844"

func TestTreeDigestMatchesKnownVector(t *testing.T) {
	got, err := TreeDigest(knownVectorEntries())
	if err != nil {
		t.Fatalf("TreeDigest() error = %v", err)
	}
	if got != knownVectorTreeDigest {
		t.Fatalf("TreeDigest() = %q, want %q", got, knownVectorTreeDigest)
	}
}

func TestTreeDigestIsInputOrderIndependent(t *testing.T) {
	entries := knownVectorEntries()
	reversed := []Entry{entries[1], entries[0]}

	got, err := TreeDigest(reversed)
	if err != nil {
		t.Fatalf("TreeDigest() error = %v", err)
	}
	if got != knownVectorTreeDigest {
		t.Fatalf("TreeDigest() with reversed input = %q, want %q (order must not affect the digest)", got, knownVectorTreeDigest)
	}
}

func TestTreeDigestDoesNotMutateCallerSlice(t *testing.T) {
	entries := knownVectorEntries()
	original := append([]Entry(nil), entries...)

	if _, err := TreeDigest(entries); err != nil {
		t.Fatalf("TreeDigest() error = %v", err)
	}
	for i := range entries {
		if entries[i] != original[i] {
			t.Fatalf("TreeDigest() mutated caller slice at index %d: got %+v, want %+v", i, entries[i], original[i])
		}
	}
}

func TestTreeValidateRejectsManifestIncludedTrue(t *testing.T) {
	entries := knownVectorEntries()
	digest, err := TreeDigest(entries)
	if err != nil {
		t.Fatalf("TreeDigest() error = %v", err)
	}
	tree := Tree{
		Algorithm:        "sha256",
		Canonicalization: TreeCanonicalization,
		ManifestIncluded: true,
		Digest:           digest,
	}
	if err := tree.Validate(entries); err == nil {
		t.Fatal("Tree.Validate() with manifest_included=true = nil, want error")
	}
}

func TestTreeValidateAcceptsRecomputedDigest(t *testing.T) {
	entries := knownVectorEntries()
	digest, err := TreeDigest(entries)
	if err != nil {
		t.Fatalf("TreeDigest() error = %v", err)
	}
	tree := Tree{
		Algorithm:        "sha256",
		Canonicalization: TreeCanonicalization,
		ManifestIncluded: false,
		Digest:           digest,
	}
	if err := tree.Validate(entries); err != nil {
		t.Fatalf("Tree.Validate() = %v, want nil", err)
	}
}

func TestTreeValidateRejectsTamperedDigest(t *testing.T) {
	entries := knownVectorEntries()
	tree := Tree{
		Algorithm:        "sha256",
		Canonicalization: TreeCanonicalization,
		ManifestIncluded: false,
		Digest:           "sha256:" + repeat("0", 64),
	}
	if err := tree.Validate(entries); err == nil {
		t.Fatal("Tree.Validate() with tampered digest = nil, want error")
	}
}
