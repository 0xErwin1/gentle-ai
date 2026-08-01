package releaseartifact

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	// EntryTypeFile is the only admitted release artifact entry type;
	// directories are implicit and the writer never emits a directory
	// member (design D3).
	EntryTypeFile = "file"

	// EntryMode is the only admitted release artifact entry mode. Any
	// executable, setuid, setgid, or sticky bit is rejected at write and
	// at read (design D3).
	EntryMode = "0644"

	// AssetsArchiveID is the GoReleaser `id:` bound to the release
	// artifact assets archive (design D1). Duplicated as a bare string
	// literal in internal/releasepolicy/policy.go, which must stay
	// stdlib-only; A2 adds a cross-package equality guard.
	AssetsArchiveID = "assets"

	maxEntryPathBytes   = 1024
	maxEntryPathSegment = 255
)

var entryDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Entry is one canonical release artifact content record (design D3,
// Interfaces/Contracts).
type Entry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Mode   string `json:"mode"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

// ValidateEntryPath enforces the design D3 path rules: relative, forward
// slashes only, no traversal, no control bytes, and bounded path/segment
// length.
func ValidateEntryPath(p string) error {
	if p == "" {
		return errors.New("release artifact entry path must not be empty")
	}
	if len(p) > maxEntryPathBytes {
		return fmt.Errorf("release artifact entry path exceeds %d bytes: %q", maxEntryPathBytes, p)
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("release artifact entry path must be relative: %q", p)
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("release artifact entry path must use forward slashes only: %q", p)
	}
	for _, r := range p {
		if r == 0 || r < 0x20 || r == 0x7f {
			return fmt.Errorf("release artifact entry path contains a control byte: %q", p)
		}
	}
	if path.Clean(p) != p {
		return fmt.Errorf("release artifact entry path is not clean (traversal, redundant, or trailing separator): %q", p)
	}
	for _, segment := range strings.Split(p, "/") {
		switch {
		case segment == "":
			return fmt.Errorf("release artifact entry path has an empty segment: %q", p)
		case segment == ".", segment == "..":
			return fmt.Errorf("release artifact entry path contains a traversal segment: %q", p)
		case len(segment) > maxEntryPathSegment:
			return fmt.Errorf("release artifact entry path segment exceeds %d bytes: %q", maxEntryPathSegment, p)
		}
	}
	return nil
}

// validateEntry enforces path, type, mode, and digest-format admission for
// one entry (design D3). It does not verify the digest against file bytes;
// that is the extraction-time responsibility of a consumer.
func validateEntry(e Entry) error {
	if err := ValidateEntryPath(e.Path); err != nil {
		return err
	}
	if e.Type != EntryTypeFile {
		return fmt.Errorf("release artifact entry %q has disallowed type %q; only %q is admitted", e.Path, e.Type, EntryTypeFile)
	}
	if e.Mode != EntryMode {
		return fmt.Errorf("release artifact entry %q has disallowed mode %q; only %q is admitted", e.Path, e.Mode, EntryMode)
	}
	if e.Size < 0 {
		return fmt.Errorf("release artifact entry %q has a negative size %d", e.Path, e.Size)
	}
	if !entryDigestPattern.MatchString(e.Digest) {
		return fmt.Errorf("release artifact entry %q has a malformed digest %q; want sha256:<64 lowercase hex>", e.Path, e.Digest)
	}
	return nil
}

// ValidateEntries validates every entry and rejects a duplicate path.
// Ascending sort order is enforced separately by callers that need it via
// SortEntries; ValidateEntries does not require entries to already be
// sorted.
func ValidateEntries(entries []Entry) error {
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if err := validateEntry(e); err != nil {
			return err
		}
		if seen[e.Path] {
			return fmt.Errorf("release artifact entries contain a duplicate path: %q", e.Path)
		}
		seen[e.Path] = true
	}
	return nil
}

// SortEntries orders entries ascending by raw UTF-8 byte order of Path,
// using Go's native string comparison — no case folding, no Unicode
// normalization (design D3).
func SortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
}
