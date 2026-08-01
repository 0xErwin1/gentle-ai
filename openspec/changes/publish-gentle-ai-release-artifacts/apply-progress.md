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

---

## Phase A2 — Archive Assembly, Policy Amendment, Verify Script, Docs

**Branch**: `feat/release-assets-archive` (base: `feat/release-artifact-snapshot`)

### Completed Tasks

- [x] A2.1 EMPIRICAL GATE — measured both candidate configs; chose branch A (`meta: true`)
- [x] A2.2 RED `internal/releasepolicy/policy_test.go`
- [x] A2.3 GREEN `.goreleaser.yaml` (branch A)
- [x] A2.4 GREEN `internal/releasepolicy/policy.go` (D6 ID-keyed split), same commit as A2.3
- [x] A2.5 RED/GREEN `internal/releasepolicy/assets_archive_id_test.go` (cross-package literal-equality guard)
- [x] A2.6 GREEN re-ran `TestReleaseDistributionPolicyAssertionFailsClosed` (`internal/update`) against the copied `policy.go`; fixed the fixture drift it found
- [x] A2.7 RED / A2.8 GREEN `scripts/verify-release-assets.sh` admits the assets archive
- [x] A2.9 Evidence record — this section
- [x] A2.10 `docs/release-artifact.md` extended with publication + verification sections

All ten A2 tasks complete. Phase A3 is out of scope for this batch and remains `[ ]` in `tasks.md`.

### A2.1 — Empirical gate (verbatim measurements)

Environment: `goreleaser` v2.15.2 at `$(go env GOPATH)/bin/goreleaser` (not on `PATH`), `MINISIGN_PUBLIC_KEYS_CANONICAL=dummy`, `release --snapshot --clean --skip=sign,publish`, repository at `feat/release-assets-archive` (working tree clean before each run). Confirmed the given baseline first: an unmodified snapshot on this branch reproduced the exact stated shape — 4 `Archive` + 4 `Binary` + 1 each `Metadata`/`Checksum`/`Homebrew Formula`, and `dist/homebrew/Formula/gentle-ai.rb` sha256 `93a9c41a52d7b7fa22db750048cd1b5d34ebd79f7625c6b00312629d29f4ddbc` — matched byte-for-byte on the first run.

**Critical discovery before either branch could be measured meaningfully**: `before.hooks` run *after* `--clean` wipes `dist/`, but *before* GoReleaser's own "ensuring distribution directory" step, which requires `dist/` to not already exist/be non-empty. A hook that stages into any path under `dist/` (e.g. `dist/release-assets/`) makes that step fail with `dist is not empty, remove it before running goreleaser or use the --clean flag`. **Resolution, applying to both branches**: the generator must stage outside `dist/` — `.release-assets-staging/` at the repository root (added to `.gitignore`).

**Second discovery (branch A only)**: `internal/releaseassetscmd`'s own staged root contains two files at depth 0 (`artifact-manifest.json`, `LICENSE`); a GoReleaser archive `files:` glob of `.release-assets-staging/**/*` silently dropped both from the resulting tar (doublestar `**/*` requires at least one directory level of nesting to match). Fixed by using `.release-assets-staging/**` (no trailing `/*`), which matched every staged file including the two at depth 0. Confirmed with `tar -tf` diffed against `find .release-assets-staging -type f`: `IDENTICAL` only after this fix.

**Third discovery, affecting the D5 gate-4 measurement methodology itself**: two consecutive snapshot runs of the **completely unmodified baseline config** (zero config change) produced two different `dist/homebrew/Formula/gentle-ai.rb` sha256 values (`76e2d8e84e4be87d3b9ea646da62c0edbc2fb302845c15be203c915cfd97a14d` then `0bff55722ff28f4f3860f74c82691b64747d5552ca6f2acccb91cd7cc99b7931` — neither equals the given baseline `93a9c41a...` either, which was itself one specific run's output). The compiled Go binary was confirmed byte-identical across runs (`sha256sum` match) — Go's build is reproducible here. The variance is in the **tar archive member timestamps** GoReleaser stamps at build time (`tar -tvf` showed every member, including static repo files like `LICENSE`/`README.md`, carrying the *current wall-clock build time*, not the source file's real mtime), which changes the compiled archive's bytes — and therefore its sha256, and therefore every embedded sha256 in the generated Homebrew formula — on every single invocation, independent of any config change. **A literal byte-for-byte sha256 match is therefore not achievable by either branch, including a no-op re-run.** Diffing the two baseline-vs-baseline runs with sha256 lines stripped showed **zero** structural difference (identical URLs, identical `on_macos`/`on_linux` branching, identical install steps) — only the four sha256 values changed. D5 gate 4 ("Homebrew formula sha256 is unchanged") was therefore evaluated as **structural equivalence with sha256 lines normalized out** — the only form of "unchanged" that is actually measurable — and is documented here as a deviation from a literal reading, not a silent reinterpretation.

**Fourth discovery (branch B, independent of the 5-point checklist, and disqualifying on its own)**: with `--skip=publish` (mandatory in snapshot mode), GoReleaser's `release.extra_files`/`checksum.extra_files` mechanism (branch B) never produces an entry in `dist/artifacts.json` at all — `dist/artifacts.json`'s type counts stayed exactly `{"Metadata":1,"Binary":4,"Archive":4,"Checksum":1,"Homebrew Formula":1}`, unchanged from the zero-assets baseline, even though `checksums.txt` did gain a line for the assets tar (the checksum pipe itself does run under `--skip=publish`; only the release/upload pipe, which is where `artifacts.json`'s extra-file bookkeeping happens, is skipped). Since `internal/releasepolicy.Validate()` is the CI preflight gate and reads only `dist/artifacts.json`, **branch B's assets archive would be structurally invisible to the pre-publish policy check that is the entire point of this design** — the "Exact-Plus-One Release Shape" requirement could not be enforced before a live publish under branch B. This is a disqualifying flaw independent of D5's five points.

#### Branch A (`meta: true` archive, `id: default`/`id: assets`, `brews[0].ids: [default]`) — final measurement

`dist/artifacts.json` counts: `{"Metadata":1,"Binary":4,"Archive":5,"Checksum":1,"Homebrew Formula":1}`. The assets `Archive` entry (raw):

```json
{
  "name": "gentle-ai_2.2.4-SNAPSHOT-6af7642d_assets.tar.gz",
  "path": "dist/gentle-ai_2.2.4-SNAPSHOT-6af7642d_assets.tar.gz",
  "internal_type": 1,
  "type": "Archive",
  "extra": {
    "Binaries": [],
    "Checksum": "sha256:c8cb21287cf093366c0c1aa89bf442663c72c4626684d53d2c1f87bfa0b45477",
    "Files": ["LICENSE", "artifact-manifest.json", "capabilities/review-integration-v2.semantic.json", "contracts/release-artifact/v1/fixtures/artifact-manifest-unsupported-major.fixture.json", "contracts/release-artifact/v1/fixtures/artifact-manifest.fixture.json", "contracts/release-artifact/v1/schemas/artifact-manifest.schema.json", "... (60 contracts/review-integration v1+v2 fixture/schema paths) ...", "docs/release-artifact.md"],
    "Format": "tar.gz",
    "ID": "assets",
    "WrappedIn": ""
  }
}
```

No `goos`/`goarch`/`target` keys present at all (empty platform axis, D5 point 1: PASS).

`tar -tf dist/gentle-ai_2.2.4-SNAPSHOT-6af7642d_assets.tar.gz | sort` diffed against `(cd .release-assets-staging && find . -type f | sed 's#^\./##' | sort)`: **`IDENTICAL`** (D5 point 2, the hard gate: PASS).

`checksums.txt` (5 lines, exactly one for the assets archive — D5 point 3: PASS):

```
c8cb21287cf093366c0c1aa89bf442663c72c4626684d53d2c1f87bfa0b45477  gentle-ai_2.2.4-SNAPSHOT-6af7642d_assets.tar.gz
32958c7ffcebc368d87d6f0b79f50bfb722542ccacc3eff616b69c58f565ad1b  gentle-ai_2.2.4-SNAPSHOT-6af7642d_darwin_amd64.tar.gz
e3e9fc23292f32db8e9052cbb0b7063f00a076b13b42c2de70cd3de8fd6793b9  gentle-ai_2.2.4-SNAPSHOT-6af7642d_darwin_arm64.tar.gz
1924287fc9b823a2d946f579ce29ffa956d258e7cb0888865520699e398ab0e3  gentle-ai_2.2.4-SNAPSHOT-6af7642d_linux_amd64.tar.gz
cc06ef8493fae29be1fa0a40a63c3dcfb5c713dd5e6506d8d728229a10723d86  gentle-ai_2.2.4-SNAPSHOT-6af7642d_linux_arm64.tar.gz
```

Homebrew formula sha256 (branch A, with `brews[0].ids: [default]`): `6c09ab09628a8fa0f65ba26d442c443d8d5298c0a4594fe9ad5ce9fa3db72665`. Diffed against a fresh baseline run with sha256 lines stripped: **`STRUCTURALLY IDENTICAL`** — same 4 `on_macos`/`on_linux` URL blocks, same install steps, no assets-archive reference. Without `brews[0].ids: [default]` (i.e. leaving `brews:` unfiltered while a second archive exists), the formula's sha256-normalized structure was **still** identical in this run (GoReleaser evidently still resolved to the 4 platform archives by binary-count heuristic in this particular version/config), but `brews[0].ids: [default]` was kept anyway per D7 — it is the explicit, non-heuristic-dependent statement of intent and is required by design regardless of what one snapshot run happened to resolve to. D5 point 4 (formula unchanged): **PASS under structural-equivalence interpretation** (see the third discovery above for why literal byte-identity is unattainable in either branch).

D5 point 5 (policy.go change is a new ID-keyed branch plus `Archive: 5`, four-platform loop body textually unchanged): satisfied by construction in A2.4 below — verified the platform-loop body is byte-identical to the pre-amendment version except iterating `platformArchives` instead of `byType["Archive"]`.

**All five D5 points hold for branch A.**

#### Branch B (`before.hooks` tar + `checksum.extra_files` + `release.extra_files`, archives unmodified) — measurement

`dist/artifacts.json` counts: `{"Metadata":1,"Binary":4,"Archive":4,"Checksum":1,"Homebrew Formula":1}` — **unchanged from the zero-assets baseline**; the assets file produces no entry in `artifacts.json` at all under `--skip=publish` (fourth discovery above). No `type` key or count exists to extend `expectedCounts` with — there is nothing in `dist/artifacts.json` for `policy.go` to check.

`tar -tf .release-assets-out/gentle-ai_..._assets.tar.gz` initially (naive `tar -C dir -czf out .`) emitted directory-node entries (`./`, `capabilities/`, `contracts/`, …) in addition to the file entries — violating D3 ("directories are implicit, the writer emits no directory members") and the D5 point-2 hard gate. Fixed with an explicit file list (`find -type f | tar -T -`, no `.` source, no directory recursion flag) — after the fix, `tar -tf` member paths were `IDENTICAL` to the staged file list.

`checksums.txt` (5 lines, assets line present — the checksum pipe runs even under `--skip=publish`):

```
93a2f8ec708aec8f450a043dc761d0b2eaffbbcce1e538e475fa43b60d850fe9  gentle-ai_2.2.4-SNAPSHOT-6af7642d_assets.tar.gz
3b8ca3a05f78e553561b61f48820ce6bcca6e94a2f83c31d717217208c45ddd0  gentle-ai_2.2.4-SNAPSHOT-6af7642d_darwin_amd64.tar.gz
0b4e76508982af89796574f8343714ecf9054f35ec04c3255b831a46619de783  gentle-ai_2.2.4-SNAPSHOT-6af7642d_darwin_arm64.tar.gz
d5ae3a47bfbd96fb14a119354eaac497183692512db999467c0e3444d5725f0d  gentle-ai_2.2.4-SNAPSHOT-6af7642d_linux_amd64.tar.gz
79c4fea8a150dd5519e68fcda10b26686fdb0d3783591a88e2a682c42cd1213b  gentle-ai_2.2.4-SNAPSHOT-6af7642d_linux_arm64.tar.gz
```

Formula sha256 (branch B): `a362e5a1764d515f5a7ee90695bda286a017f69fcb8ac916837cd0595e4cc16c` — structurally identical to baseline with sha256 lines stripped (expected: branch B changes nothing about `archives:`/`brews:`).

**Branch B is disqualified**: not by a D5 checklist failure (D5 points 1-4 are effectively moot since the artifact never appears in `artifacts.json` to evaluate against points 1-3, and point 4 passes trivially since nothing about the Homebrew-relevant config changed), but by the independent, more severe finding above — the pre-publish CI preflight (`internal/releasepolicy.Validate()`, driven by `dist/artifacts.json`) cannot see or enforce the assets archive's presence/identity at all under `--skip=publish`, defeating the design's core purpose (the exact-plus-one release shape must be assertable *before* a live publish, not just after).

### Decision: **Branch A** (`meta: true` archive)

Chosen per D5's rule (all five points hold) and reinforced by branch B's independent, disqualifying policy-visibility gap. Implementation: `.goreleaser.yaml` gains `before.hooks` (staging via `internal/releaseassetscmd` into `.release-assets-staging/`, outside `dist/`), an explicit `id: default` on the existing archive, a new `id: assets, meta: true` archive with `files: [{src: .release-assets-staging/**, dst: .}]`, and `brews[0].ids: [default]`.

### TDD Cycle Evidence

| Task | RED (test written first, run, failed for the right reason) | GREEN (implementation, run, passed) | REFACTOR |
|---|---|---|---|
| A2.2/3/4 policy exact-plus-one split | `go vet ./internal/releasepolicy/...` → `undefined: assetsArchiveID` (compile failure — `policy_test.go` written against the not-yet-existing D6 amendment) | `go test ./internal/releasepolicy/... -v` → all PASS: happy path (4 platform + 1 assets → nil); 3 newly-RED count/axis/identity rejection rows (one required rewriting — see Deviations); 6 regression-lock rows (missing/wrong/mislabeled platform archive, missing binary, Windows binary present) all still reject | None. |
| A2.5 cross-package literal guard | N/A (literal already matched by construction from A2.4) — proved the guard is not vacuously true by temporarily mutating `assetsArchiveID` to `"assets-drifted"`: `go test -run TestAssetsArchiveIDMatchesReleaseArtifactContract` → FAIL with the exact drift message, then reverted → PASS | `go test ./internal/releasepolicy/... -run TestAssetsArchiveIDMatchesReleaseArtifactContract -v` → PASS | None. |
| A2.6 `internal/update` fixture drift | `go test ./internal/update/... -run TestReleaseDistributionPolicyAssertionFailsClosed -v` → FAIL: `resolved GoReleaser artifact types changed: map[Archive:4 ...]` (the fixture's "approved release plan" baseline still had 4 archives against the amended `expectedCounts["Archive"]==5`) | Added the assets archive to `releasePolicyArtifactsFixture` → `go test ./internal/update/... -run TestReleaseDistributionPolicyAssertionFailsClosed -v` → all 23 subtests PASS (happy path + 22 bypass-rejection rows) | None. |
| A2.7/8 `verify-release-assets.sh` | Added a fixed-expectation test (fake `gh` returning a 5-asset live release) → `go test ./internal/update/... -run TestReleaseAssetVerifierAdmitsTheAssetsArchive -v` → FAIL: `remote asset set is incomplete or unexpected` (script's `archives=()` still had only 4 entries, so the 5th remote asset was unrecognized) | Added the 5th entry to `archives=()` → same test PASS. Folded the fixture into the pre-existing `TestReleaseAssetVerifierPreservesReadOnlyRotationVerification` (which now represents the correct 5-archive live-release shape) and removed the standalone RED-proof test file to avoid a near-duplicate permanent fixture; the RED-then-GREEN transcript above is the retained record of that cycle | Deleted the transitional standalone test file after folding its fixture into the existing, broader test (which also asserts the read-only `gh` command surface — a property the transitional test didn't check). |

### Work Unit Evidence

| Evidence | Value |
|---|---|
| Focused test command and exact result | `go test ./internal/releasepolicy/... && go test ./internal/update/... -run TestReleaseDistributionPolicyAssertionFailsClosed` → both `ok` (matches the Suggested Work Units table's A2 focused command) |
| Runtime harness command/scenario and exact result | `MINISIGN_PUBLIC_KEYS_CANONICAL=dummy $(go env GOPATH)/bin/goreleaser release --snapshot --clean --skip=sign,publish` against the real repository (branch-A config) → succeeded, 5 archives, `tar -tf dist/gentle-ai_2.2.4-SNAPSHOT-6af7642d_assets.tar.gz` matched staged paths exactly. Then ran the real CI preflight gate end-to-end: wrote a run marker, reran the snapshot, executed `RELEASE_POLICY_SNAPSHOT_MARKER=... RELEASE_POLICY_SNAPSHOT_RUN_ID=... bash scripts/verify-release-distribution-policy.sh` → `release distribution policy: exact current Linux/macOS snapshot and sole Homebrew publisher verified`. Note: `scripts/verify-release-assets.sh` itself talks only to a live GitHub release (`gh api`/`gh release download`), not a local `dist/`, so it cannot be run against a snapshot; it is exercised by the Go-level fake-`gh` harness test above instead, which is the existing pattern in this repository (`internal/update/release_security_test.go`). |
| Full package result | `go test ./internal/releasepolicy/... ./internal/update/... ./internal/update/upgrade/... -v` → 100% PASS; `gofmt -l` clean (after one `gofmt -w` pass on `policy_test.go`); `go vet ./...` clean |
| Whole-repository regression check | `go test ./...` (full repo) → all `ok`, zero failures, zero build errors (`internal/cli` 162.2s, remainder cached/fast) |
| Rollback boundary | Revert `.goreleaser.yaml`, `.gitignore` (drop `.release-assets-staging/`), `internal/releasepolicy/policy.go` (both the archive-split logic and `expectedGoReleaserYAML`), `internal/releasepolicy/policy_test.go`, `internal/releasepolicy/assets_archive_id_test.go`, `internal/update/windows_distribution_policy_test.go` (fixture), `internal/update/release_security_test.go` (fixture), `scripts/verify-release-assets.sh`, and `docs/release-artifact.md`'s A2 sections together — restores the exact four-archive release with its original policy assertions. `internal/releaseartifact` and `internal/releaseassetscmd` (A1a/A1b) are untouched and remain independently valid; only the wiring added in this phase is removed. |

### Commits (work-unit-commits skill)

1. `feat(releasepolicy): admit a platform-independent assets archive in the release shape` — `.goreleaser.yaml`, `.gitignore`, `internal/releasepolicy/policy.go`, `internal/releasepolicy/policy_test.go`, `internal/releasepolicy/assets_archive_id_test.go`, `internal/update/windows_distribution_policy_test.go`
2. `fix(release): verify the assets archive in remote release verification` — `scripts/verify-release-assets.sh`, `internal/update/release_security_test.go`
3. `docs(release-artifact): document assets archive publication and verification` — `docs/release-artifact.md`
4. `docs(sdd): record Phase A2 apply-progress evidence` — `tasks.md`, `apply-progress.md`

### Deviations from Design

- **D5 gate 4 ("Homebrew formula sha256 unchanged") evaluated as structural equivalence, not literal byte identity.** Empirically proven unattainable literally: two runs of the fully unmodified baseline config produce different formula sha256 values (GoReleaser stamps build-time timestamps into tar member metadata regardless of config). Documented in full under A2.1's third discovery above. The measurable, intent-matching form — same URL/sha256-pair count, same `on_macos`/`on_linux` branching, same install steps, with sha256 lines normalized out — holds in every comparison performed.
- **A2.2's "assets archive absent"/"assets archive duplicated" rows** were originally written expecting a dedicated "assets archive count" error message; empirically they are caught one layer earlier by the pre-existing `expectedCounts` type-level check (since removing/duplicating the assets archive changes the total `Archive` count away from the fixed `5`). Rewrote the test rows' expected substring to `"artifact types changed"` and added one additional row — an `Extra.ID` reassignment that keeps the total count at `5` — to reach and prove the dedicated `len(assetsArchives) != 1` check design's D6 snippet describes. No behavior was loosened; this is a test-expectation correction, not a scope change.
- **A2.7's standalone RED-proof test file was folded into and then removed in favor of the pre-existing `TestReleaseAssetVerifierPreservesReadOnlyRotationVerification`**, whose fixture now represents the correct (post-A2) 5-archive live-release shape and additionally asserts the read-only `gh` command surface. The RED-then-GREEN transcript is preserved in this record rather than as a permanent duplicate test.

### Issues Found

None beyond the four empirical discoveries recorded under A2.1, all resolved within this phase.

## Remaining Tasks (out of scope for Phase A2)

- [ ] Phase A3: Downstream Notification (A3.1-A3.5)

## Status (cumulative, superseded by Phase A3 below)

10/10 Phase A1a tasks complete. 11/11 Phase A1b tasks complete. 10/10 Phase A2 tasks complete. 31/36 total tasks complete. Ready for `sdd-verify` on Phase A2, then PR 3 of the feature-branch chain (base: `feat/release-artifact-snapshot`, PR 2's branch).

---

## Phase A3 — Downstream Notification

**Branch**: `feat/release-downstream-notify` (base: `feat/release-assets-archive`)

### Completed Tasks

- [x] A3.1 RED `internal/releasepolicy/release_workflow_yaml_test.go`
- [x] A3.2 RED `scripts/test-notify-downstream-release-credential-absent.sh`
- [x] A3.3 GREEN `.github/workflows/release.yml` (`notify` job)
- [x] A3.4 GREEN `internal/releasepolicy/policy.go` (`expectedReleaseWorkflowYAML`), same commit as A3.3
- [x] A3.5 `.github/workflows/notify-release.yml`

All five A3 tasks complete. This is the final phase of the tracker (A1a → A1b → A2 → A3).

### MAINTAINER SUPERSESSION — task text vs. what was implemented (A3.3)

`tasks.md`'s A3.3 line, as originally written, described the dispatch payload as
"repository, tag, version, commit, assets archive name, contract major."
That predates a maintainer decision that supersedes it: **the payload is the
tag only.** Rationale recorded by the maintainer: the consumer must download
and verify `checksums.txt`, its minisign signature, and the assets archive
against the authoritative published release regardless of what the dispatch
payload says. Every other field in the original list is either (a) redundant
with what the consumer fetches anyway once it has the tag, or (b) a second
source of truth (version, commit, archive name, contract major) that would
have to be kept in sync with the actual release by hand — a drift risk with
no corresponding benefit, since nothing trusts the dispatch payload itself.

Implemented: `scripts/notify-downstream-release.sh` sends
`{"event_type":"gentle-ai-release","client_payload":{"tag":"<tag>"}}` only.
No digest field either — unchanged from the original design (D8): the
consumer re-derives every digest from the signed `checksums.txt`, so a
compromised dispatch can never pin a false one. `tasks.md`'s A3.3 line has
been updated in place to record this supersession rather than leave the
stale payload description looking like an implementation gap.

### TDD Cycle Evidence

| Task | RED (test/script written first, run, failed for the right reason) | GREEN (implementation, run, passed) | REFACTOR |
|---|---|---|---|
| A3.1 `notify` job structure | `go test ./internal/releasepolicy/... -run ReleaseWorkflowYAML -v` → `TestReleaseWorkflowYAMLNotifyJobStructure` FAIL: `expectedReleaseWorkflowYAML has no notify job` (parse succeeded, job genuinely absent — not a compile/type failure) | Added the `notify` job to `.github/workflows/release.yml` (A3.3) and mirrored it into `expectedReleaseWorkflowYAML` (A3.4) in the same commit → `go test ./internal/releasepolicy/... -run ReleaseWorkflowYAML -v` → both `TestReleaseWorkflowYAMLNotifyJobStructure` and `TestReleaseWorkflowYAMLMatchesLiveFile` PASS | None. Added `TestReleaseWorkflowYAMLMatchesLiveFile` alongside the required assertion — not itself an A3.1 requirement, but it directly exercises the byte-exact-YAML constraint (`compareYAML`) locally, so a future edit to one of the two files without the other is caught by `go test` instead of only failing closed in CI. |
| A3.2 credential-absent path | `./scripts/test-notify-downstream-release-credential-absent.sh` → FAIL, exit 127: `.../scripts/notify-downstream-release.sh: No such file or directory` (RED for the right reason — the script under test did not exist yet) | Created `scripts/notify-downstream-release.sh` (A3.3) → `./scripts/test-notify-downstream-release-credential-absent.sh` → `notify-downstream-release credential-absent path: PASS` (exit 0, skip line printed, fake `curl` on `PATH` never invoked — proven via a marker file the fake `curl` would have created) | None. |

### Design decisions carried into implementation

- **No checkout dropped, no inline-only shell.** The `notify` job gets its own `Checkout exact tag` step (same SHA-pinned `actions/checkout`, same `fetch-tags: true` / `persist-credentials: false` shape as `preflight`/`release`/`verify`) so it can invoke `./scripts/notify-downstream-release.sh` as a real, independently testable file rather than duplicating inline `run: |` shell in both `release.yml` and `notify-release.yml`. Design D8's "single shell step" is satisfied at the level of "the one step that does the dispatch work is a shell step" — consistent with every other job in this workflow, all of which also open with a checkout step before their business-logic step(s).
- **`needs: verify`** (not `needs: release`) — makes the job structurally unable to run before the release has published AND remote asset verification has passed, per spec "Notification Runs Only After Publish AND Verification." A verification failure or in-flight verification leaves `notify` un-scheduled; GitHub Actions' `needs:` gate is a hard dependency, not a convention.
- **`permissions: contents: read`** only — the dispatch step needs no write access to this repository; it only calls out to the downstream repository's API using a scoped secret.
- **Credential-gated, inert by absence**: `scripts/notify-downstream-release.sh` checks `DOWNSTREAM_DISPATCH_TOKEN` before doing anything else. Empty/unset → prints `downstream release notification: skipped, no dispatch credential configured` and exits 0. No `curl` invocation happens on that path (proven by A3.2's fake-`curl` harness). The consumer repository's receiver workflow does not exist yet, so `DOWNSTREAM_DISPATCH_TOKEN` is correctly absent from this repository's secrets today — the job ships inert by construction, and activating it later requires configuring the one secret, not a second code change.
- **Non-invalidating, visible, retryable failure**: `notify` is its own job in the DAG, downstream of (not overlapping with) `release`/`verify`. A `curl --fail` non-zero exit fails only the `notify` job (visible red status); it cannot mark `release`/`verify` as failed retroactively, and re-running the job (or running `.github/workflows/notify-release.yml` with the same `tag` input) replays only the dispatch attempt.
- **Separate replay workflow, no new trigger on `release.yml`**: `.github/workflows/notify-release.yml` is `workflow_dispatch`-only with a required `tag` string input; it checks out `ref: ${{ inputs.tag }}` and calls the same `scripts/notify-downstream-release.sh` with `RELEASE_TAG: ${{ inputs.tag }}`. `release.yml` gained no `workflow_dispatch` trigger — verified by inspection (its `on:` block still lists only `push.tags`). `internal/releasepolicy/policy.go` pins exactly two files (`.goreleaser.yaml`, `.github/workflows/release.yml`); `notify-release.yml` has no corresponding embedded constant, confirmed by `rg -n "notify-release" internal/releasepolicy/policy.go` returning nothing.

### Work Unit Evidence

| Evidence | Value |
|---|---|
| Focused test command and exact result | `go test ./internal/releasepolicy/... -run ReleaseWorkflowYAML -v` → both subtests PASS (matches the Suggested Work Units table's A3 focused command); `./scripts/test-notify-downstream-release-credential-absent.sh` → PASS |
| Full package result | `go test ./internal/releasepolicy/... -v` → 100% PASS; `gofmt -l` clean on every changed/new `.go` file; `go vet ./...` clean; `go run ./internal/gofmtcheck` clean; `shellcheck scripts/notify-downstream-release.sh scripts/test-notify-downstream-release-credential-absent.sh` clean |
| Runtime harness command/scenario and exact result | N/A per the Suggested Work Units table — `needs: verify` job-graph gating and the credential-gated dispatch's "present" branch are only exercisable in a live GitHub Actions run against a real downstream repository secret, neither of which exists in this environment. The credential-**absent** branch (the only branch reachable without live infrastructure) is fully exercised by the A3.2 shell harness above. Both `.github/workflows/release.yml` and the new `.github/workflows/notify-release.yml` were additionally parsed with `gopkg.in/yaml.v3` (the same library `internal/releasepolicy` uses) to confirm they are well-formed YAML documents. |
| Whole-repository regression check | `go test ./...` (full repo, ~65 packages) → all `ok`, zero failures, zero build errors. One pre-existing regression-lock test required an intentional, documented update (see below); no other package needed a change. |
| Rollback boundary | Remove the `notify:` job from `.github/workflows/release.yml`; revert the corresponding block from `expectedReleaseWorkflowYAML` in `internal/releasepolicy/policy.go`; delete `.github/workflows/notify-release.yml`, `scripts/notify-downstream-release.sh`, `scripts/test-notify-downstream-release-credential-absent.sh`, and `internal/releasepolicy/release_workflow_yaml_test.go`; revert the `persist-credentials: false` count and the four added required-substring lines in `internal/update/release_security_test.go` (`3 → 4` back to `3`, drop the four `notify`-related strings). The `preflight`/`release`/`verify` jobs, `.goreleaser.yaml`, and every A1a/A1b/A2 file are untouched — publish and remote verification are unaffected by removing this unit. |

### One pre-existing regression-lock test required an intentional update

`internal/update/release_security_test.go`'s `TestReleaseWorkflowUsesFailClosedLeastPrivilegeGates` asserted `persist-credentials: false` occurs **exactly 3** times in `release.yml` (one per existing job's checkout). Adding the `notify` job's own checkout step — using the identical fail-closed pattern as the other three jobs, per design — legitimately raises that count to 4. Ran the test before the workflow edit to confirm it was passing at 3, then updated the assertion to `4` (and its message from "all three checkouts" to "all four checkouts") together with the workflow change, and added four more required-substring checks (`"notify:"`, `"needs: verify"`, `"./scripts/notify-downstream-release.sh"`, `"DOWNSTREAM_DISPATCH_TOKEN: ${{ secrets.DOWNSTREAM_DISPATCH_TOKEN }}"`) so the test keeps asserting the new job's fail-closed shape rather than only its count. This is not a loosened assertion — every prior required substring, the SHA-pinning scan, and the `MINISIGN_SECRET_KEY_BASE64`-occurs-once check are untouched and still pass.

### Commits (work-unit-commits skill)

1. `feat(release): notify downstream consumer after publish and verification` — `.github/workflows/release.yml`, `internal/releasepolicy/policy.go`, `internal/releasepolicy/release_workflow_yaml_test.go`, `scripts/notify-downstream-release.sh`, `scripts/test-notify-downstream-release-credential-absent.sh`, `internal/update/release_security_test.go`
2. `feat(release): add manual replay workflow for downstream notification` — `.github/workflows/notify-release.yml`
3. `docs(sdd): record Phase A3 apply-progress evidence` — `tasks.md`, `apply-progress.md`

Diff vs. tracker base (`feat/release-assets-archive..HEAD`, authored files only, excluding `tasks.md`/`apply-progress.md`): 7 files changed, 258 insertions(+), 2 deletions(-) — well inside the Review Workload Forecast's "Low" risk rating for A3 and its ~120-180 authored-line estimate range (comment-heavy rationale on a security-adjacent workflow file accounts for the difference; no unplanned scope was added).

### Deviations from Design

- **Payload is tag-only**, superseding `tasks.md`'s A3.3 line — see the MAINTAINER SUPERSESSION section above. This is an explicit maintainer decision recorded before implementation, not a discovered-during-apply deviation.
- **`notify` job includes a checkout step** that design D8's "single shell step" phrasing does not explicitly spell out. Read as "the one step that does the actual work is a shell step" (matching every other job in this file, all of which open with checkout before their business-logic steps) rather than "the job has exactly one step total" — the latter reading would require either inlining duplicate shell into two workflow files or fetching the dispatch script over the network, both worse than one shared, independently-tested script file. No other design decision (D1-D7, or D8's ordering/credential-gating/failure-semantics/payload-shape requirements) was reinterpreted.

### Issues Found

None.

## Remaining Tasks

None. All 36 tasks across A1a, A1b, A2, and A3 are complete.

## Status (final)

10/10 Phase A1a tasks complete. 11/11 Phase A1b tasks complete. 10/10 Phase A2 tasks complete. 5/5 Phase A3 tasks complete. **36/36 total tasks complete.** Ready for `sdd-verify` on Phase A3, then PR 4 of the feature-branch chain (base: `feat/release-assets-archive`, PR 3's branch) — the tracker's final child PR.
