# Tasks: Publish Gentle AI Release Artifacts (Change A, provider-only)

> Tracker note: this repository's tracker is independent — no child PR crosses into `gentle-pi`. The only bridge to the consumer is a published immutable release; A3's `notify` job is credential-gated cross-repo dispatch, not a shared PR.

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | A1a ~500-650, A1b ~350-450, A2 ~300-400, A3 ~120-180 (authored; excludes golden bytes) |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | A1a → A1b → A2 → A3 (tracker order, dependency-strict) |
| Delivery strategy | exception-ok |
| Chain strategy | feature-branch-chain |

Decision needed before apply: No
Chained PRs recommended: Yes
Chain strategy: feature-branch-chain
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|---|---|---|---|---|---|
| A1a | Contract namespace + canonical encoder + entry/path/mode/tree + manifest + fixtures | PR 1 (base: tracker) | `go test ./internal/releaseartifact/... -run 'Canonical\|Entry\|Tree\|Manifest'` | N/A — pure, unwired contract types; no command consumes them yet | Delete `contracts/release-artifact/v1/{schemas,fixtures}` + `internal/releaseartifact/{canonical,entry,tree,manifest}*.go`; nothing else references them |
| A1b | Snapshot projection + generator command + golden | PR 2 (base: PR 1 branch) | `go test ./internal/cli/... -run ReviewCapabilitiesSnapshot && go test ./internal/releaseartifact/... -run 'Snapshot\|Floor' && go test ./internal/releaseassetscmd/...` | `go run ./internal/releaseassetscmd` against a scratch staging dir; verify manifest emitted and tree digest recomputes | Delete `internal/cli/review_capabilities_snapshot*.go`, `internal/releaseartifact/{snapshot,floor}*.go`, `internal/releaseassetscmd/`, golden testdata; live `review capabilities` unaffected |
| A2 | Archive assembly (D5 empirical gate), `policy.go` exact-plus-one, verify script, docs | PR 3 (base: PR 2 branch) | `go test ./internal/releasepolicy/... && go test ./internal/update/... -run TestReleaseDistributionPolicyAssertionFailsClosed` | `goreleaser release --snapshot --clean --skip=sign,publish`; `tar -tf dist/gentle-ai_*_assets.tar.gz`; run `scripts/verify-release-assets.sh` against the snapshot `dist/` | Revert `.goreleaser.yaml`, `policy.go` (both YAML constants), `verify-release-assets.sh` together — restores exact four-archive release |
| A3 | `notify` job + replay workflow | PR 4 (base: PR 3 branch) | `go test ./internal/releasepolicy/... -run ReleaseWorkflowYAML` | N/A — job-graph gating (`needs: verify`) only exercisable in a live Actions run | Remove `notify` job from `release.yml`, revert `expectedReleaseWorkflowYAML`, delete `notify-release.yml`; publish/verify untouched |

## Phase A1a: Contract Namespace, Canonical Encoder, Entry/Path/Mode/Tree, Manifest

- [x] A1a.1 RED: `internal/releaseartifact/canonical_test.go` — byte assertion on a small fixed struct: 2-space indent, `SetEscapeHTML(false)`, `[]` not `null`, single trailing LF, no BOM, struct-declaration field order.
- [x] A1a.2 GREEN: `internal/releaseartifact/canonical.go` — `EncodeCanonical(v any) ([]byte, error)` per D3.
- [x] A1a.3 RED: `internal/releaseartifact/entry_test.go` — table rows: path rejection (absolute, `..`, backslash, NUL/control byte, empty segment, >1024/255-byte, duplicate); type rejection (symlink, hardlink, device, FIFO, socket, unknown); mode rejection (non-`0644`, exec/setuid/setgid/sticky); `SortEntries` ascending raw-UTF8-byte order.
- [x] A1a.4 GREEN: `internal/releaseartifact/entry.go` — `Entry` struct; `EntryTypeFile`, `EntryMode`, `AssetsArchiveID` constants; `ValidateEntryPath`; `SortEntries`.
- [x] A1a.5 RED: `internal/releaseartifact/tree_test.go` — checked-in expected hex over the known-vector preimage `"gentle-ai.release-artifact-tree/v1\x00"` + sorted `path\x00type\x00mode\x00size\x00digest\n`; input-order independence; `manifest_included: true` rejected.
- [x] A1a.6 GREEN: `internal/releaseartifact/tree.go` — `TreeDigest(entries []Entry) (string, error)`, `TreeCanonicalization` constant.
- [x] A1a.7 Create `contracts/release-artifact/v1/schemas/artifact-manifest.schema.json` (`$id`, `additionalProperties:false` throughout, mandatory field groups) and both fixtures — `artifact-manifest.fixture.json` and `artifact-manifest-unsupported-major.fixture.json` (byte-identical except `schema`/`contract.major`/`schema_id`/`schema_path`, per Worked Example).
- [x] A1a.8 RED: `internal/releaseartifact/manifest_test.go` mirroring `TestReviewCapabilitiesSchemaAndFixtureAreStrict` — schema header assertions; valid fixture decodes with `DisallowUnknownFields` and validates; unsupported-major fixture rejected naming the major (before layout inference); missing mandatory field group rejected; tampered digest / unresolved reference rejected.
- [x] A1a.9 GREEN: `internal/releaseartifact/manifest.go` — `Manifest` type; `ContractID`, `ContractMajor`, `ContractMinor`, schema constants; `(m Manifest) Validate() error` (groups present, references resolve into entries, tree recompute, `unknown_mandatory: reject`).
- [x] A1a.10 `docs/release-artifact.md` — create, contract section only (namespace, schema, fixtures, rolling-changelog header), `docs/review-integration.md` style.

## Phase A1b: Snapshot Projection, Generator Command, Golden

- [x] A1b.1 RED: `internal/cli/review_capabilities_snapshot_test.go` — drift test (projected field set == `ReviewCapabilitiesResult` fields minus exactly `{Package, Build, Executable}`); parity test (`reflect.DeepEqual` per field vs `reviewCapabilitiesStaticSurface(contract)` for v1 and v2); exclusion test (canonical bytes contain none of `package/build/executable/sha256/vcs/go_version/module_version/release_channel`).
- [x] A1b.2 GREEN: `internal/cli/review_capabilities_snapshot.go` — `ReleaseSemanticSnapshot(contract string) releaseartifact.SemanticSnapshot`, one-directional `cli → releaseartifact` import; live `review capabilities` response untouched.
- [x] A1b.3 RED: `internal/releaseartifact/snapshot_test.go` — `SemanticSnapshot` encode/decode round-trip via `EncodeCanonical`.
- [x] A1b.4 GREEN: `internal/releaseartifact/snapshot.go` — `SemanticSnapshot` type (contract identity, protocol, operations, gates, projections, schemas, features, bootstrap, compatibility).
- [x] A1b.5 RED: `internal/releaseartifact/floor_test.go` — the frozen `RequiredFloor` is a subset of the live projection for operations/gates/projections/schemas; a removal fails, an addition does not.
- [x] A1b.6 GREEN: `internal/releaseartifact/floor.go` — hand-declared frozen `RequiredFloor` constant (never generation-time computed).
- [x] A1b.7 RED: `internal/releaseassetscmd/main_test.go` — builds a staging tree + manifest in `t.TempDir()`; recomputes the tree digest from staged files and compares.
- [x] A1b.8 GREEN: `internal/releaseassetscmd/main.go` (`go run ./internal/releaseassetscmd`, `internal/gofmtcheck` precedent) — stages `contracts/**`, docs, LICENSE, schema, generated snapshot; sorts entries; emits `artifact-manifest.json`. Unwired from `.goreleaser.yaml` in this unit.
- [x] A1b.9 RED: golden test asserting canonical snapshot bytes vs `internal/releaseartifact/testdata/review-integration-v2.semantic.json`; checked-in sha256 constant guards an accidental `-update`.
- [x] A1b.10 GREEN: generate the golden via `-update`, inspect diff, rerun without `-update`; record the sha256 constant.
- [x] A1b.11 `docs/release-artifact.md` — extend with generator command + golden-regeneration section.

## Phase A2: Archive Assembly, Policy Amendment, Verify Script, Docs

- [ ] A2.1 EMPIRICAL GATE (no code change): run `goreleaser release --snapshot --clean --skip=sign,publish` for BOTH candidate configs (`meta:true` archive vs `before.hooks`+`extra_files`). Record, per branch: every `dist/artifacts.json` entry (name/path/type/goos/goarch/target/extra); exact tarball member paths via `tar -tf` (**hard gate**: must equal staged relative paths exactly — no injected/stripped prefix); whether `before.hooks` run after `--clean` wipes `dist/` (if not, staging is destroyed before archiving under both branches — must be resolved before A2.3); `checksums.txt` lines; sha256 of `dist/homebrew/Formula/gentle-ai.rb` before/after. Apply D5's 5-point decision rule and write the chosen branch + evidence into the A2 work-unit record.
- [ ] A2.2 RED: `internal/releasepolicy/policy_test.go` — newly-RED: 4 platform + 1 `id:assets` archive → nil; assets absent/duplicated → count error; sixth unrelated-id archive → `expectedCounts` type error; assets carries GOOS/GOARCH/Target → platform-axis error; assets name doesn't bind `snapshotVersion` → identity error; assets declares `Binaries` → identity error. Regression locks (must still fail): missing platform archive; wrong platform GOOS/GOARCH/Target; non-`gentle-ai` binary; platform archive with `Extra.ID:assets`; missing binary in the 4-platform matrix; Windows/Scoop artifact present; live YAML edited without its constant; constant edited without the live file.
- [ ] A2.3 GREEN: modify `.goreleaser.yaml` per the branch chosen in A2.1 (D5/D7) — explicit archive `id:` (`default`/`assets`) and either `brews[0].ids:[default]` (branch A) or `before.hooks` + `release.extra_files` + `checksum.extra_files` (branch B, not checksum-only). Same commit as A2.4.
- [ ] A2.4 GREEN: modify `internal/releasepolicy/policy.go` per D6 — `assetsArchiveID` constant, ID-keyed split (`platformArchives`/`assetsArchives`), exact-plus-one assertions (no platform axis, no binaries, name binds `snapshotVersion`), updated `expectedCounts` (branch A: `Archive:5`; branch B: `Archive:4` + the exact measured extra-file key/count from A2.1), `expectedGoReleaserYAML` byte-updated to A2.3's live YAML. Same commit as A2.3.
- [ ] A2.5 RED: cross-package test proving the `policy.go` `assetsArchiveID` literal equals `releaseartifact.AssetsArchiveID` — a duplicated-literal equality guard, not a shared import (`policy.go` stays stdlib-only).
- [ ] A2.6 GREEN: re-run `TestReleaseDistributionPolicyAssertionFailsClosed` (`internal/update`) against the copied `policy.go` + `releasepolicycmd/main.go`; fix any drift introduced by the copy.
- [ ] A2.7 RED: prove `scripts/verify-release-assets.sh` fails when `gentle-ai_${version}_assets.tar.gz` is absent from `archives=(…)`.
- [ ] A2.8 GREEN: modify `scripts/verify-release-assets.sh` (~:23-29) — admit the assets archive in `archives=(…)`.
- [ ] A2.9 Evidence record (not a Go test): attach the A2.1 before/after `dist/homebrew/Formula/gentle-ai.rb` sha256 pair to the A2 work-unit record.
- [ ] A2.10 `docs/release-artifact.md` — extend with publication + verification sections.

## Phase A3: Downstream Notification

- [ ] A3.1 RED: `internal/releasepolicy/policy_test.go` — workflow-structure assertion (mirrored into `expectedReleaseWorkflowYAML`) proving the `notify` job declares `needs: verify` and `permissions: contents: read`.
- [ ] A3.2 RED: shell-level test for the credential-absent path — prints the skip line, exits 0, attempts no dispatch.
- [ ] A3.3 GREEN: modify `.github/workflows/release.yml` — add `notify` job (`needs: verify`, `permissions: contents: read`, credential-gated dispatch step; payload = repository, tag, version, commit, assets archive name, contract major; no digest field).
- [ ] A3.4 GREEN: update `expectedReleaseWorkflowYAML` in `internal/releasepolicy/policy.go` to match A3.3's live `release.yml`. Same commit as A3.3.
- [ ] A3.5 Create `.github/workflows/notify-release.yml` — `workflow_dispatch` only, required `tag` input, replay path. `release.yml` itself gains no `workflow_dispatch` trigger; `policy.go` pins no third file.
