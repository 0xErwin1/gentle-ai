// Package releaseartifact implements the gentle-ai.release-artifact contract
// (see design D1-D3): the versioned, self-describing manifest bundled with
// the platform-independent assets archive published on every release. This
// package is stdlib-only and imports no other internal package, so it stays
// safe to import from both internal/cli and the release tooling that must
// remain dependency-free.
package releaseartifact

import (
	"bytes"
	"encoding/json"
)

// EncodeCanonical encodes v per the gentle-ai.release-artifact-tree/v1
// canonicalization rules (design D3): two-space indent, no HTML escaping,
// UTF-8 with no BOM, LF-only line endings, and exactly one trailing
// newline. Key order follows the Go struct declaration order of v — the
// only fields this contract encodes are struct fields, never map keys, so
// no additional ordering step is required.
//
// Callers that must render an empty collection as JSON's `[]` rather than
// `null` MUST initialize that field with a non-nil, zero-length slice (for
// example `make([]Entry, 0)` or `[]Entry{}`); encoding/json marshals a nil
// slice as `null`, and this function performs no such rewrite.
func EncodeCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
