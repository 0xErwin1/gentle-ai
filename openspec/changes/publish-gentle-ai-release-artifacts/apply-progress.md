# Apply Progress: Publish Gentle AI Release Artifacts

**Change**: publish-gentle-ai-release-artifacts
**Mode**: Strict TDD

This artifact accumulates evidence across work units. Each `## Phase` section below is its own self-contained record; do not overwrite a prior phase's section when appending a new one.

## Phase A1a — Contract Namespace, Canonical Encoder, Entry/Path/Mode/Tree, Manifest

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

## Remaining Tasks (out of scope for Phase A1a)

- [ ] Phase A1b: Snapshot Projection, Generator Command, Golden (A1b.1-A1b.11)
- [ ] Phase A2: Archive Assembly, Policy Amendment, Verify Script, Docs (A2.1-A2.10)
- [ ] Phase A3: Downstream Notification (A3.1-A3.5)

## Status (Phase A1a)

10/10 Phase A1a tasks complete. Ready for `sdd-verify` on this work unit, then PR 1 of the feature-branch chain (base: `feat/pi-parity-assets-artifact`).

---

## Phase A1b — Snapshot Projection, Generator Command, Golden

**Branch**: `feat/release-artifact-snapshot` (base: `feat/release-artifact-contract`)

### Completed Tasks

- [x] A1b.1 RED `internal/cli/review_capabilities_snapshot_test.go`
- [x] A1b.2 GREEN `internal/cli/review_capabilities_snapshot.go`
- [x] A1b.3 RED `internal/releaseartifact/snapshot_test.go`
- [x] A1b.4 GREEN `internal/releaseartifact/snapshot.go`
- [x] A1b.5 RED `internal/releaseartifact/floor_test.go`
- [x] A1b.6 GREEN `internal/releaseartifact/floor.go`
- [x] A1b.7 RED `internal/releaseassetscmd/main_test.go`
- [x] A1b.8 GREEN `internal/releaseassetscmd/main.go`
- [x] A1b.9 RED `internal/releaseartifact/snapshot_golden_test.go` (golden absent)
- [x] A1b.10 GREEN generated golden via `-update`, inspected diff, reran without `-update`, recorded sha256 constant
- [x] A1b.11 `docs/release-artifact.md` extended

All eleven A1b tasks complete. Phases A2, A3 are out of scope for this batch and remain `[ ]` in `tasks.md`.

### Sequencing note: real dependency order vs. tasks.md numbering

`tasks.md` lists A1b.1/A1b.2 (the `internal/cli` projection) before A1b.3/A1b.4 (the `internal/releaseartifact.SemanticSnapshot` type it returns). That numbering cannot compile in that literal order: A1b.2's `ReleaseSemanticSnapshot(contract string) releaseartifact.SemanticSnapshot` needs the `SemanticSnapshot` type to exist. I implemented in true compile-dependency order — A1b.3/A1b.4 first, then A1b.1/A1b.2 — while completing every task's described RED/GREEN content exactly as specified. This is a sequencing note, not a scope deviation: every task's file and test content matches its description in `tasks.md`.

### TDD Cycle Evidence

| Task | RED (test written first, run, failed for the right reason) | GREEN (implementation, run, passed) | REFACTOR |
|---|---|---|---|
| A1b.3/4 `SemanticSnapshot` type | `go test ./internal/releaseartifact/... -run Snapshot` → `undefined: SemanticSnapshot / SnapshotProtocol / SnapshotFeatures / ...` (compile failure) | `go test ./internal/releaseartifact/... -run Snapshot -v` → 2/2 PASS (encode/decode round-trip with `DisallowUnknownFields`; empty slices encode as `[]` not `null`) | None. |
| A1b.1/2 `ReleaseSemanticSnapshot` projection | `go test ./internal/cli/... -run ReleaseSemanticSnapshot` → `undefined: ReleaseSemanticSnapshot` (compile failure) | `go test ./internal/cli/... -run ReleaseSemanticSnapshot -v` → 4/4 PASS: field-set drift test (reflect over `ReviewCapabilitiesResult` minus `{Package,Build,Executable}`), parity test (JSON-map comparison vs `reviewCapabilitiesStaticSurface(contract)` for v1 and v2, surface minus `package/build/executable` keys), exclusion test (canonical bytes scanned for `package/build/executable/sha256/vcs/go_version/module_version/release_channel` — none found), live-`review capabilities`-untouched test | Renamed test functions from `TestReleaseSemanticSnapshot*` to `TestReviewCapabilitiesSnapshot*` so the tasks.md Suggested Work Units focused command (`-run ReviewCapabilitiesSnapshot`) matches verbatim. |
| A1b.5/6 `RequiredFloor` | `go test ./internal/releaseartifact/... -run RequiredFloor` → `undefined: releaseartifact.VerifyRequiredFloor` (compile failure) | `go test ./internal/releaseartifact/... -run RequiredFloor -v` → 3/3 PASS: floor is a subset of the real live v1 and v2 projections; removing a floor operation from a real live snapshot fails `VerifyRequiredFloor`; adding a novel operation beyond the floor does not fail | `floor_test.go` (and `snapshot_golden_test.go`) use the external `package releaseartifact_test` — the only way to import `internal/cli` (needed for the real live projection) from this directory without a real import cycle (`cli` imports `releaseartifact` in production). |
| A1b.7/8 `internal/releaseassetscmd` | `go vet ./internal/releaseassetscmd/...` → `undefined: runConfig` (no production file existed yet) | `go test ./internal/releaseassetscmd/... -v` → 2/2 PASS: staged payload + emitted manifest passes `Manifest.Validate()`, recomputed tree digest (independently walking the staging dir) matches `manifest.Tree.Digest`, every expected path present, `references.contracts`/`references.semantic_snapshots` correct; missing `--version` rejected with an actionable error | None. |
| A1b.9/10 golden | `go test ./internal/releaseartifact/... -run TestSemanticSnapshotGolden -v` → FAIL: `open testdata/review-integration-v2.semantic.json: no such file or directory` (golden file did not exist — RED for the right reason, not a type error) | Ran `-update` (wrote the golden), **inspected the full 312-line diff by hand** (see below), reran without `-update` → PASS | None. |

### What the golden diff actually showed (A1b.10 inspection)

Generated file: `internal/releaseartifact/testdata/review-integration-v2.semantic.json` (312 lines). Read end-to-end before recording the sha256. It contains exactly the pure `gentle-ai.review-integration/v2` static capability surface:

- `schema`/`contract`/`protocol` (major 2, minor 0) for v2.
- `operations`: the 8 registry operations in declaration order (`review.bind_sdd`, `review.capabilities`, `review.finalize`, `review.repair`, `review.retry_final_verification`, `review.start`, `review.status`, `review.validate`).
- `gates`: all 5 (`post-apply`, `pre-commit`, `pre-push`, `pre-pr`, `release`).
- `projections`: `staged`, `workspace`.
- `schemas`: 22 entries — the 21-entry v1 base list with each v2-swapped schema identity applied (e.g. `.../operation/v2`, `.../repair/v2`, `.../start/v3`, `.../status/v3`) plus the v2-only `gentle-ai.review-integration.consent/v2` appended.
- `features.mandatory`/`features.optional`: all entries from `reviewCapabilitiesStaticSurface`, including the v2-only `provider_bound_native_git_context` optional feature.
- `bootstrap.command`: the v2 next-transition refresh command string.
- `compatibility`: protocol major/minor `2`/`2`, `modes: [compact-v2, legacy-v1]`, full `legacy_window` block.
- **Confirmed absent everywhere**: `package`, `build`, `executable`, `sha256`, `vcs`, `go_version`, `module_version`, `release_channel` — grepped the full file, zero matches.
- Canonical formatting confirmed: 2-space indent, no HTML-escaped characters, single trailing newline (line 313, empty), no BOM.

sha256 of the golden: `da95ffdfba60acd234f482433b01a9dec7a85a7538a90df7a3d84e5236d39f80` — recorded in `goldenSemanticSnapshotSHA256` in `snapshot_golden_test.go`.

### Work Unit Evidence

| Evidence | Value |
|---|---|
| Focused test command and exact result | `go test ./internal/cli/... -run ReviewCapabilitiesSnapshot && go test ./internal/releaseartifact/... -run 'Snapshot\|Floor' && go test ./internal/releaseassetscmd/...` → all `ok` (matches the Suggested Work Units table's A1b focused command exactly) |
| Runtime harness command/scenario and exact result | `go run ./internal/releaseassetscmd --root . --staging-dir <scratch-dir> --repository Gentleman-Programming/gentle-ai --tag v0.0.0-a1b-smoke --version 0.0.0-a1b-smoke --commit 0000000000000000000000000000000000000a` against the real repository tree → staged the real `contracts/review-integration/v1`, `v2`, `contracts/release-artifact/v1`, `docs/release-artifact.md`, `LICENSE`, and the generated snapshot; emitted a valid `artifact-manifest.json` with `references.contracts` naming all three staged contract namespaces/versions. Scratch directory removed after inspection. |
| Full package result | `go test ./internal/cli/... ./internal/releaseartifact/... ./internal/releaseassetscmd/... -v` → 100% PASS; `gofmt -l` clean on every new/changed file; `go vet ./...` clean |
| Whole-repository regression check | `go test ./...` (full repo, ~70 packages including `internal/cli` at 165.9s and `internal/reviewtransaction` at 120.1s) → all `ok`, zero failures, zero build errors |
| Rollback boundary | Delete `internal/cli/review_capabilities_snapshot{,_test}.go`, `internal/releaseartifact/{snapshot,floor,snapshot_golden}{,_test}.go`, `internal/releaseartifact/testdata/review-integration-v2.semantic.json`, and `internal/releaseassetscmd/`; revert the `docs/release-artifact.md` additions and the `tasks.md` A1b checkbox edits. Nothing outside these paths imports or references them — the live `review capabilities` command, `internal/releaseartifact`'s A1a surface, and the rest of the repository are unaffected with them absent. |

### Commits (work-unit-commits skill)

1. `feat(releaseartifact): add semantic capability snapshot type` — `snapshot.go`, `snapshot_test.go`
2. `feat(cli): add release semantic snapshot projection` — `review_capabilities_snapshot.go`, `review_capabilities_snapshot_test.go`
3. `feat(releaseartifact): add frozen required-floor compatibility check` — `floor.go`, `floor_test.go`
4. `feat(releaseassetscmd): add release assets staging generator` — `main.go`, `main_test.go`
5. `test(releaseartifact): add semantic snapshot golden` — `snapshot_golden_test.go`, `testdata/review-integration-v2.semantic.json`
6. `docs(release-artifact): document the semantic snapshot, required floor, and generator` — `docs/release-artifact.md`
7. `docs(sdd): record Phase A1b apply-progress evidence` — `tasks.md`, `apply-progress.md`

### Deviations from Design

None in shape or behavior. One process deviation, already covered above: implementation order followed real compile dependency (`SemanticSnapshot` type before the projection that returns it) rather than the tasks.md numbering, because the literal numbered order cannot compile.

Design's Interfaces/Contracts section lists only `ReleaseSemanticSnapshot(contract string) releaseartifact.SemanticSnapshot` for the `internal/cli` side; it does not spell out `SemanticSnapshot`'s nested types or `RequiredFloor`/`VerifyRequiredFloor`'s exact shape. Both are additive, matching the design's prose (D2 "required floor in the snapshot", D4 "hand-declared frozen constant") and the Testing Strategy table, not an alternative to any settled decision.

### One interpretation made, not a deviation

Design's Data Flow diagram and D2 describe staging "contracts/\*\*, docs, LICENSE, schema" without naming which `docs/` files. I staged exactly `docs/release-artifact.md` — this contract's own documentation — rather than the entire `docs/` directory (which holds ~30 unrelated docs, e.g. `docs/pi.md`, `docs/kiro.md`). This keeps the payload scoped to what the contract itself needs a consumer to read. `references.contracts` entries are derived generically from the staged `contracts/<name>/<version>/` directory structure (`id = "gentle-ai.<name>/<version>"`, `root = "contracts/<name>/<version>"`), not hardcoded — so this generalizes correctly if a third contract namespace is added later.

### Issues Found

None.

### Left for A2

- Wiring `internal/releaseassetscmd` into `.goreleaser.yaml` (`before.hooks` or equivalent) — this unit deliberately leaves the command standalone.
- The empirical D5 branch-selection gate, `policy.go` exact-plus-one amendment, `scripts/verify-release-assets.sh` update, and the cross-package `assetsArchiveID` literal-equality guard.
- No `.goreleaser.yaml`, `.github/workflows/release.yml`, `internal/releasepolicy/policy.go`, or `scripts/verify-release-assets.sh` were touched in this unit, per scope boundary.

## Remaining Tasks (out of scope for Phase A1b)

- [ ] Phase A2: Archive Assembly, Policy Amendment, Verify Script, Docs (A2.1-A2.10)
- [ ] Phase A3: Downstream Notification (A3.1-A3.5)

## Status (cumulative)

10/10 Phase A1a tasks complete. 11/11 Phase A1b tasks complete. 21/36 total tasks complete. Ready for `sdd-verify` on Phase A1b, then PR 2 of the feature-branch chain (base: `feat/release-artifact-contract`, PR 1's branch).
