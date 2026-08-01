# Apply Progress: Publish Gentle AI Release Artifacts — Phase A1a

**Change**: publish-gentle-ai-release-artifacts
**Unit**: A1a — Contract Namespace, Canonical Encoder, Entry/Path/Mode/Tree, Manifest
**Mode**: Strict TDD
**Branch**: `feat/release-artifact-contract` (base: `feat/pi-parity-assets-artifact`)

## Completed Tasks

- [x] A1a.1 RED `internal/releaseartifact/canonical_test.go`
- [x] A1a.2 GREEN `internal/releaseartifact/canonical.go`
- [x] A1a.3 RED `internal/releaseartifact/entry_test.go`
- [x] A1a.4 GREEN `internal/releaseartifact/entry.go`
- [x] A1a.5 RED `internal/releaseartifact/tree_test.go`
- [x] A1a.6 GREEN `internal/releaseartifact/tree.go`
- [x] A1a.7 `contracts/release-artifact/v1/schemas/artifact-manifest.schema.json` + both fixtures
- [x] A1a.8 RED `internal/releaseartifact/manifest_test.go`
- [x] A1a.9 GREEN `internal/releaseartifact/manifest.go`
- [x] A1a.10 `docs/release-artifact.md`

All ten A1a tasks complete. Phases A1b, A2, A3 are out of scope for this batch and remain `[ ]` in `tasks.md`.

## TDD Cycle Evidence

| Task | RED (test written first, run, failed for the right reason) | GREEN (implementation, run, passed) | REFACTOR |
|---|---|---|---|
| A1a.1/2 canonical encoder | `go test ./internal/releaseartifact/...` → `undefined: EncodeCanonical` (compile failure, package had no source) | `go test ./internal/releaseartifact/... -run Canonical` → 3/3 PASS | None needed — `EncodeCanonical` is a thin `json.Encoder` wrapper; kept minimal per design D3. |
| A1a.3/4 entry admission | `go test ./internal/releaseartifact/...` → `undefined: ValidateEntryPath / Entry / EntryMode / ValidateEntries` (compile failure) | `go test ./internal/releaseartifact/... -run "Entry\|Sort"` → all subtests PASS (13 path-rejection rows, 7 type-rejection rows, 7 mode-rejection rows, duplicate rejection, accept, sort) | Extracted unexported `validateEntry` shared by `ValidateEntries` and `TreeDigest`, avoiding duplicated path/type/mode/digest-format checks. |
| A1a.5/6 tree digest | `go test ./internal/releaseartifact/...` → `undefined: TreeDigest / Tree / TreeCanonicalization` (compile failure) | `go test ./internal/releaseartifact/... -run Tree` → 6/6 PASS, including the checked-in known-vector hash `sha256:971ca2d598a9f6e517c00eee9581f40376f9fef61b8a905c310a2a8d71140844` computed independently (Python `hashlib.sha256` over the exact D3 preimage) before the Go implementation existed, and matched on the first GREEN run — confirms the preimage format was implemented correctly, not tuned to pass. | None. |
| A1a.7 schema + fixtures | N/A — not RED/GREEN; artifact creation task. Verified with `python3 -m json.load` for JSON well-formedness before use. | — | — |
| A1a.8/9 manifest | `go test ./internal/releaseartifact/...` → `undefined: Manifest / ManifestSchemaID / ManifestRelease / ManifestLayout` (compile failure) | `go test ./internal/releaseartifact/... -run Manifest` → 6/6 PASS (schema-header + strict-fixture test mirroring `TestReviewCapabilitiesSchemaAndFixtureAreStrict`; unsupported-major-names-the-major; 5 missing-mandatory-group subtests; unresolved-reference; tampered-digest) | None. |

## Work Unit Evidence

| Evidence | Value |
|---|---|
| Focused test command and exact result | `go test ./internal/releaseartifact/... -run 'Canonical\|Entry\|Tree\|Manifest\|Sort'` → all PASS (matches the Suggested Work Units table's focused command for A1a) |
| Full package result | `go test ./internal/releaseartifact/... -v` → 100% PASS, `gofmt -l` clean, `go vet ./internal/releaseartifact/...` clean |
| Whole-repository regression check | `go test ./...` (full repo, ~90 packages) → all `ok`, zero failures, zero build errors |
| Runtime harness command/scenario and exact result | N/A — per the design's Suggested Work Units table, A1a ships pure, unwired contract types; no command consumes them yet. The generator command that would exercise them end-to-end is A1b scope. |
| Rollback boundary | Delete `contracts/release-artifact/v1/{schemas,fixtures}/` and `internal/releaseartifact/{canonical,entry,tree,manifest}{,_test}.go`; revert `docs/release-artifact.md` (new file) and the `tasks.md` checkbox edits. Nothing else in the repository imports or references these paths — `go build ./...` and `go test ./...` remain green with them absent. |

## Commits (work-unit-commits skill)

1. `feat(releaseartifact): add canonical JSON encoder for release artifact contract` — `canonical.go`, `canonical_test.go`
2. `feat(releaseartifact): add entry admission, ordering, and tree digest` — `entry.go`, `entry_test.go`, `tree.go`, `tree_test.go`
3. `feat(releaseartifact): add manifest contract schema, fixtures, and validation` — `manifest.go`, `manifest_test.go`, `contracts/release-artifact/v1/**`
4. `docs(release-artifact): document the release artifact contract` — `docs/release-artifact.md`, `tasks.md` checkbox update

Diff vs. tracker branch (`feat/pi-parity-assets-artifact..HEAD`): 13 files changed, 1231 insertions(+), 10 deletions(-). No `.goreleaser.yaml`, `.github/workflows/release.yml`, `internal/releasepolicy/policy.go`, or `scripts/verify-release-assets.sh` touched.

## Deviations from Design

None. Implementation follows D1-D3 and the Interfaces/Contracts section exactly, including:

- Array (not singular) `references.semantic_snapshots`.
- No `$schema` URL key inside the manifest's own top-level fields (the JSON Schema file itself carries `$schema`/`$id` as schema metadata, per the same convention as `capabilities.schema.json`; the manifest document has no `$schema` field).
- `entries[].size` retained.
- `RequiredFloor` is out of scope for A1a (A1b.5/A1b.6).
- Tree digest is non-self-referential: `TreeDigest` and `Tree.Validate` operate only on `entries`, never on the manifest's own bytes.

### One clarification made, not a deviation

The design's Interfaces/Contracts snippet lists only `ValidateEntryPath`, `SortEntries`, `TreeDigest`, `EncodeCanonical`, and `Manifest.Validate` as the cross-file API. Task A1a.3 explicitly requires table rows for entry **type** rejection, entry **mode** rejection, and **duplicate**-path rejection — none of which fit `ValidateEntryPath(p string) error`'s signature (a bare path string). To satisfy that task without inventing an undeclared shape, I added:

- unexported `validateEntry(e Entry) error` (path + type + mode + digest-format admission for one entry), and
- exported `ValidateEntries(entries []Entry) error` (validates every entry and rejects duplicate paths).

Both are additive, internally consistent with D3, and `Manifest.Validate()` (A1a.9, listed in the design) calls `ValidateEntries` to do the "groups present" entry-admission work the design assigns it. I did not invent an alternative to any decision the design actually settled.

## Issues Found

None.

## Design point I want the orchestrator to weigh in on

The design's `Tree` struct/`(t Tree) Validate(entries []Entry) error` is implied but not spelled out in the Interfaces/Contracts snippet (only `TreeCanonicalization` and the free function `TreeDigest` are listed there). Task A1a.5's RED test explicitly requires a `manifest_included: true` rejection case, which is a property of the `tree` field group as a whole, not of the entries list alone. I introduced `Tree` as a first-class type with its own `Validate` method so `Manifest.Validate()` has something concrete to call for "tree recompute" (per A1a.9's own description). This seems like the only coherent reading of the tasks, but flagging it since it is genuinely an addition to, not a restatement of, the design's explicit snippet.

## Remaining Tasks (out of scope for this batch)

- [ ] Phase A1b: Snapshot Projection, Generator Command, Golden (A1b.1-A1b.11)
- [ ] Phase A2: Archive Assembly, Policy Amendment, Verify Script, Docs (A2.1-A2.10)
- [ ] Phase A3: Downstream Notification (A3.1-A3.5)

## Status

10/10 Phase A1a tasks complete. Ready for `sdd-verify` on this work unit, then PR 1 of the feature-branch chain (base: `feat/pi-parity-assets-artifact`).
