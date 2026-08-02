package releaseartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// TreeCanonicalization identifies the exact preimage format TreeDigest
// hashes over (design D3, D1).
const TreeCanonicalization = "gentle-ai.release-artifact-tree/v1"

// Tree is the manifest's `tree` field group: the non-recursive digest
// binding over the sorted canonical entries, deliberately excluding the
// manifest file itself (design D3, spec "Non-Self-Referential Digest
// Binding").
type Tree struct {
	Algorithm        string `json:"algorithm"`
	Canonicalization string `json:"canonicalization"`
	ManifestIncluded bool   `json:"manifest_included"`
	Digest           string `json:"digest"`
}

// Validate rejects manifest_included: true as an unknown layout, rejects
// an unrecognized algorithm or canonicalization identity, and recomputes
// the tree digest from entries to confirm it matches t.Digest.
func (t Tree) Validate(entries []Entry) error {
	if t.ManifestIncluded {
		return errors.New("release artifact tree.manifest_included must be false; true is an unknown layout")
	}
	if t.Algorithm != "sha256" {
		return fmt.Errorf("release artifact tree algorithm %q is not supported; want %q", t.Algorithm, "sha256")
	}
	if t.Canonicalization != TreeCanonicalization {
		return fmt.Errorf("release artifact tree canonicalization %q is not supported; want %q", t.Canonicalization, TreeCanonicalization)
	}
	recomputed, err := TreeDigest(entries)
	if err != nil {
		return err
	}
	if recomputed != t.Digest {
		return fmt.Errorf("release artifact tree digest mismatch: declared %q, recomputed %q", t.Digest, recomputed)
	}
	return nil
}

// TreeDigest computes the gentle-ai.release-artifact-tree/v1 digest over
// entries. It sorts a copy internally (never mutating the caller's slice),
// so the result is independent of input order, and validates each entry
// before hashing so the manifest never signs an inadmissible member.
//
// Preimage (design D3): the literal tag "gentle-ai.release-artifact-tree/v1"
// followed by a NUL byte, then per entry in ascending path order:
// "path\x00type\x00mode\x00size\x00digest\n", using the exact manifest
// field strings and a decimal, unpadded size.
func TreeDigest(entries []Entry) (string, error) {
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	SortEntries(sorted)

	h := sha256.New()
	h.Write([]byte(TreeCanonicalization))
	h.Write([]byte{0})
	for _, e := range sorted {
		if err := validateEntry(e); err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00%s\n", e.Path, e.Type, e.Mode, e.Size, e.Digest)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
