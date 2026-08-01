# Design: Publish Gentle AI Release Artifacts (Change A, provider-only)

## Technical Approach

Add one platform-independent assets archive to the existing release, described by a versioned self-describing manifest in a new `contracts/release-artifact/` namespace. A new stdlib-only package `internal/releaseartifact` owns the contract types, the canonical encoder, path/type/mode admission, and the tree digest. `internal/cli` gains one exported projection over the already-pure `reviewCapabilitiesStaticSurface`. `internal/releaseassetscmd` (run as `go run ./internal/releaseassetscmd`, following the `internal/gofmtcheck` precedent) stages the payload and emits the manifest. `internal/releasepolicy/policy.go` gains an **exact new branch** keyed to the assets archive's name and ID — never a relaxed count or a loosened `compareYAML`.

**Verified in this phase, not assumed:**

| Claim | Evidence |
|---|---|
| `reviewCapabilitiesStaticSurface` is pure | `internal/cli/review_capabilities.go:200-325` calls only `reviewIntegrationOperationNames()` (`review_operation_contract.go:86`, iterates the package-level `reviewIntegrationOperationRegistry`) and `reviewtransaction` constants. No `os`, `runtime`, `debug`, `time`, or filesystem call. All host-dependent decoration happens in `buildReviewCapabilities` (`:180-193`). |
| `expectedCounts` is `reflect.DeepEqual`-exact | `policy.go:296,311` |
| The archive loop assumes a platform axis for every `Archive` | `policy.go:343-369` — indexes `expectedTargets[GOOS/GOARCH]`, requires `Extra.ID=="default"` and `Binaries==["gentle-ai"]` |
| `Validate()` byte-diffs the **live** YAML against embedded constants | `policy.go:32-46` reads `.goreleaser.yaml` / `.github/workflows/release.yml` from disk; `validateYAMLSemantics` → `compareYAML` (`:197`) against `expectedGoReleaserYAML` (`:496`) and `expectedReleaseWorkflowYAML` (`:566`) |
| `brews:` has no `ids:` and `archives[0]` has no `id:` today | `policy.go:516-519,553-563` |
| `policy.go` must stay dependency-free | `policy.go:123-130` — the verifier copies `policy.go` + `releasepolicycmd/main.go` into a bare module, so it may not import `internal/releaseartifact` |

## Architecture Decisions

### D1: Contract namespace, schema ID, and file names

| Item | Value |
|---|---|
| Namespace | `contracts/release-artifact/v1/` |
| Schema file | `schemas/artifact-manifest.schema.json` |
| Schema `$id` | `https://gentle-ai.dev/contracts/release-artifact/v1/schemas/artifact-manifest.schema.json` |
| Schema identity | `gentle-ai.release-artifact-manifest/v1` |
| Contract ID | `gentle-ai.release-artifact` (major 1, minor 0) |
| Fixtures | `fixtures/artifact-manifest.fixture.json`, `fixtures/artifact-manifest-unsupported-major.fixture.json` |
| Manifest member | `artifact-manifest.json` at archive root |
| Snapshot schema identity | `gentle-ai.release-semantic-capabilities/v1` |
| Tree canonicalization | `gentle-ai.release-artifact-tree/v1` |
| Go package | `internal/releaseartifact` |
| Assets archive name | `gentle-ai_{version}_assets.tar.gz`; GoReleaser `id: assets` |

**Alternatives considered**: mirroring `contracts/review-integration/`. **Rationale**: bootstrap-time archive trust and runtime CLI negotiation have different consumers and must version independently; naming follows the existing `<subject>.fixture.json` / `<subject>.schema.json` convention so reviewers recognise it.

### D2: Smallest defensible v1 surface

The shape freezes at first publish, so each field must earn its place.

| Included | Why it cannot be deferred |
|---|---|
| `contract.schema_path` | Self-describing decode without any external fetch (spec: *Complete decode without external lookup*) |
| `entries[].size` | The consumer must bound extraction **before** writing bytes; a post-hoc digest check is too late |
| `references.semantic_snapshots` as an **array** | Singular→plural is a breaking reshape; a one-element array costs nothing and avoids a guaranteed future major bump |
| `compatibility` block | Carries `unknown_mandatory: reject` into the archive, per spec |
| `required` floor in the snapshot | Additions must be decodable; without a frozen floor there is nothing to verify against |

| Excluded | Why |
|---|---|
| `$schema` URL key | `contract.schema_id` + `contract.schema_path` already give identity and locator; a URL invites a network fetch |
| Any archive self-digest | Recursive; the signed `checksums.txt` supplies it |
| Shared-skill payload | Deferred open question; the **entry list is data, not shape**, so adding files later is a content change under layout v1 |
| Per-entry timestamps/uid/gid | Not reproducible across the two assembly branches (see D5) |

### D3: Canonicalization rules (`gentle-ai.release-artifact-tree/v1`)

| Area | Rule |
|---|---|
| Encoding | UTF-8, no BOM, LF only |
| JSON | `json.Encoder`, `SetIndent("", "  ")`, `SetEscapeHTML(false)`; key order = Go struct declaration order; empty slices encode `[]` never `null`; exactly one trailing LF |
| Unknown fields | Schema `additionalProperties: false` everywhere; Go decode uses `DisallowUnknownFields` |
| Paths | Relative, forward slashes only. Reject: absolute, backslash, `path.Clean(p) != p`, any `.`/`..`/empty segment, NUL or byte `< 0x20` or `0x7F`, path > 1024 bytes, segment > 255 bytes |
| Uniqueness / order | Unique after validation; ascending **raw UTF-8 byte** order (Go `string` `<`). No case folding, no Unicode normalization |
| Entry types | `"file"` only. Directories are implicit — the writer emits no directory members. Reject symlink, hardlink, char/block device, FIFO, socket, unknown |
| Modes | Exactly the string `"0644"`. Any other value — including any executable, setuid, setgid, or sticky bit — is rejected at write and at read |
| Digests | `"sha256:"` + exactly 64 lowercase hex over exact file bytes |
| Tree preimage | `"gentle-ai.release-artifact-tree/v1\x00"` then, per entry in sorted order: `path "\x00" type "\x00" mode "\x00" size "\x00" digest "\n"`, using the **exact manifest field strings** (so a consumer hashes what it read). `size` is decimal, no padding |
| Manifest exclusion | `tree.manifest_included` is the constant `false`; a decoder MUST reject `true` as an unknown layout |
| Archive completeness | The archive contains exactly `artifact-manifest.json` plus exactly the listed entries — an unlisted extra member is a rejection |
| Minor policy | `optional-fields-only`. A new required field or changed meaning is a major bump |

### D4: Snapshot projection seam

`reviewCapabilitiesStaticSurface` is unexported in the large `internal/cli` package.

| Option | Tradeoff | Decision |
|---|---|---|
| Export the static surface and import `internal/cli` from `internal/releaseartifact` | Drags the whole CLI into the artifact package | Rejected |
| Put the projection in `internal/cli`, importing `internal/releaseartifact` | One-directional `cli → releaseartifact`; artifact package stays stdlib-only | **Chosen** |

New file `internal/cli/review_capabilities_snapshot.go` exports `ReleaseSemanticSnapshot(contract string) releaseartifact.SemanticSnapshot`. The live `review capabilities` response is untouched.

The `required` floor is a **hand-declared frozen constant** in `internal/releaseartifact`, not the generation-time set — a floor computed from the current surface moves every release and stops being a floor.

### D5: Archive assembly — scheduled empirical gate with both branches fully designed

A2 task 1 runs `goreleaser release --snapshot --clean --skip=sign,publish` and records, for the candidate configuration: every `dist/artifacts.json` entry (`name`, `path`, `type`, `goos`, `goarch`, `target`, `extra`), the exact tarball member paths from `tar -tf`, the `checksums.txt` lines, and the sha256 of `dist/homebrew/Formula/gentle-ai.rb` **before and after**.

**Choose branch A (`meta: true`) iff all five hold**, else branch B:

1. The meta archive appears with `Extra.ID == "assets"` and **empty** `GOOS`/`GOARCH`/`Target`.
2. Its tarball member paths equal the staged relative paths exactly (no injected prefix, no stripped directory).
3. `checksums.txt` has exactly one line for it.
4. The Homebrew formula sha256 is unchanged.
5. The needed `policy.go` change is a new ID-keyed branch plus `Archive: 5`, with the four-platform branch body textually unchanged.

**Correction to the explore assumption**: branch B is *not* free of `expectedCounts` change. GoReleaser reports extra files in `artifacts.json`; task 1 must measure the exact type key and count so the amendment adds an exact new entry, never a relaxed count. Branch B also needs `release.extra_files`, not only `checksum.extra_files` — `checksum.extra_files` alone signs the tarball but never uploads it. Because `--skip=publish` suppresses the release pipe, task 1 must record what the snapshot does and does not show and reason about that delta explicitly.

Manifest, snapshot, entry list, canonicalization, and every `internal/releaseartifact` test are identical under both branches. Only `.goreleaser.yaml`, the YAML constant, and the `policy.go` branch differ.

### D6: `policy.go` amendment — exact-plus-one, never generic

```go
const assetsArchiveID = "assets" // duplicated literal; see D6 note

platformArchives := make([]artifact, 0, len(expectedTargets))
var assetsArchives []artifact
for _, item := range byType["Archive"] {
    if extraString(item.Extra, "ID") == assetsArchiveID {
        assetsArchives = append(assetsArchives, item)
        continue
    }
    platformArchives = append(platformArchives, item)
}
if len(assetsArchives) != 1 {
    return fmt.Errorf("resolved assets archive count changed: %d", len(assetsArchives))
}
// existing loop body runs over platformArchives, UNCHANGED, including the
// Extra.ID == "default", Format, Binaries, name and target assertions.
// ... then, after snapshotVersion is known:
assets := assetsArchives[0]
expectedAssetsName := "gentle-ai_" + snapshotVersion + "_assets.tar.gz"
if assets.GOOS != "" || assets.GOARCH != "" || assets.Target != "" {
    return errors.New("resolved assets archive must not carry a platform axis")
}
if assets.Name != expectedAssetsName || assets.Path != "dist/"+expectedAssetsName ||
    extraString(assets.Extra, "Format") != "tar.gz" ||
    len(extraStrings(assets.Extra, "Binaries")) != 0 {
    return errors.New("resolved assets archive identity changed")
}
```

`expectedCounts` becomes `{"Metadata":1,"Binary":4,"Archive":5,"Checksum":1,"Homebrew Formula":1}` under branch A — still one exact map, still `reflect.DeepEqual`. Under branch B, `Archive` stays 4 and the map gains the measured extra-file key at count 1.

Every existing assertion is preserved verbatim. **What is added is strictly stronger**: the assets archive must have no platform axis, must carry no binaries, and its name must bind the *same* `snapshotVersion` the platform archives agreed on.

**D6 note (gotcha)**: `policy.go` is compiled in isolation, so `assetsArchiveID` is a duplicated string literal. A test in a package that may import both (`internal/releaseartifact`) asserts the two literals agree, and `TestReleaseDistributionPolicyAssertionFailsClosed` in `internal/update` must still pass after the copy.

### D7: Homebrew

Branch A: give the existing archive an explicit `id: default` (semantically identical to today's implicit ID — `policy.go:359` already asserts `Extra.ID == "default"`), give the new one `id: assets`, and add `brews[0].ids: [default]`. Branch B adds neither: with no second archive an `ids:` filter is a no-op that still costs an edit to the security-adjacent YAML constant, and the smallest constant diff is the safer one.

Proof is evidence, not a Go test — only GoReleaser produces the formula: byte-compare `dist/homebrew/Formula/gentle-ai.rb` before and after, recorded in the A2 work-unit verification record.

### D8: Notification

New `notify` job in `release.yml` with `needs: verify` (so it is structurally unable to run before publish **and** verification), `permissions: contents: read`, and a single shell step that exits 0 with a printed skip line when `secrets.DOWNSTREAM_DISPATCH_TOKEN` is empty — inert by absence, not by failure. Payload carries repository, tag, version, commit, assets archive name, and contract major. It deliberately carries **no digest**: the consumer re-derives every digest from the signed `checksums.txt`, so a compromised dispatch cannot pin a false digest.

Replay uses a **separate** `.github/workflows/notify-release.yml` with only `workflow_dispatch` and a required `tag` input. Adding `workflow_dispatch` to `release.yml` itself would make the publishing workflow manually triggerable — rejected. `policy.go` pins only `.goreleaser.yaml` and `release.yml`, so the new file adds no constant; the `notify` job addition still requires mirroring into `expectedReleaseWorkflowYAML`.

## Data Flow

```
reviewCapabilitiesStaticSurface (pure, internal/cli:200)
        │  ReleaseSemanticSnapshot(contract)
        ▼
internal/releaseartifact ── canonical encode ──► capabilities/review-integration-v2.semantic.json
        │                                                      │
        │  stage payload (contracts/**, docs, LICENSE, schema) │
        ▼                                                      ▼
internal/releaseassetscmd ── entries + sort + tree digest ──► artifact-manifest.json
        ▼
dist/release-assets/  ──[branch A: archives meta:true | branch B: hooks + extra_files]──►
        gentle-ai_X.Y.Z_assets.tar.gz ──► checksums.txt ──minisign──► checksums.txt.minisig
                │                                │
                └── policy.go (exact-plus-one) ──┴── verify-release-assets.sh ──► notify
```

## Worked Example — `artifact-manifest.json`

```json
{
  "schema": "gentle-ai.release-artifact-manifest/v1",
  "contract": {
    "id": "gentle-ai.release-artifact",
    "major": 1,
    "minor": 0,
    "schema_id": "https://gentle-ai.dev/contracts/release-artifact/v1/schemas/artifact-manifest.schema.json",
    "schema_path": "contracts/release-artifact/v1/schemas/artifact-manifest.schema.json"
  },
  "release": {
    "repository": "Gentleman-Programming/gentle-ai",
    "tag": "v2.3.0",
    "version": "2.3.0",
    "commit": "5fe1beaa0000000000000000000000000000cafe"
  },
  "layout": { "version": 1 },
  "archive": {
    "asset": "gentle-ai_2.3.0_assets.tar.gz",
    "digest_source": "signed-checksums.txt"
  },
  "references": {
    "semantic_snapshots": [
      {
        "contract": "gentle-ai.review-integration/v2",
        "path": "capabilities/review-integration-v2.semantic.json",
        "schema": "gentle-ai.release-semantic-capabilities/v1"
      }
    ],
    "contracts": [
      { "id": "gentle-ai.review-integration/v1", "root": "contracts/review-integration/v1" },
      { "id": "gentle-ai.review-integration/v2", "root": "contracts/review-integration/v2" }
    ]
  },
  "tree": {
    "algorithm": "sha256",
    "canonicalization": "gentle-ai.release-artifact-tree/v1",
    "manifest_included": false,
    "digest": "sha256:9f2b1c7d4e8a05631f2c9d0e7a4b6c8d1e3f5a7b9c0d2e4f6a8b0c2d4e6f8a01"
  },
  "compatibility": {
    "minimum_contract_major": 1,
    "maximum_contract_major": 1,
    "additive_minor_policy": "optional-fields-only",
    "unknown_mandatory": "reject",
    "unknown_optional": "ignore"
  },
  "entries": [
    {
      "path": "capabilities/review-integration-v2.semantic.json",
      "type": "file",
      "mode": "0644",
      "size": 8123,
      "digest": "sha256:3a1f0b5c9d2e7480a6b3c5d7e9f1a2b4c6d8e0f2a4b6c8d0e2f4a6b8c0d2e4f6"
    },
    {
      "path": "contracts/release-artifact/v1/schemas/artifact-manifest.schema.json",
      "type": "file",
      "mode": "0644",
      "size": 6042,
      "digest": "sha256:7c5e3a1908f6d4b2c0a8e6d4b2f0a8c6e4d2b0f8a6c4e2d0b8f6a4c2e0d8b6f4"
    }
  ]
}
```

Entries are truncated for readability; the real manifest lists every non-manifest member, sorted by raw path bytes.

**`artifact-manifest-unsupported-major.fixture.json`** is byte-identical to the valid fixture except `"schema": "gentle-ai.release-artifact-manifest/v2"` and `"contract": { ..., "major": 2, "minor": 0, "schema_id": ".../v2/schemas/artifact-manifest.schema.json", "schema_path": "contracts/release-artifact/v2/schemas/artifact-manifest.schema.json" }`. Everything else stays structurally valid, so the fixture proves rejection happens on **major alone**, before any layout inference — not incidentally through a structural error.

## Interfaces / Contracts

```go
// internal/releaseartifact — stdlib only, no internal imports.
const (
    ContractID           = "gentle-ai.release-artifact"
    ContractMajor        = 1
    ContractMinor        = 0
    ManifestSchema       = "gentle-ai.release-artifact-manifest/v1"
    ManifestSchemaID     = "https://gentle-ai.dev/contracts/release-artifact/v1/schemas/artifact-manifest.schema.json"
    ManifestFileName     = "artifact-manifest.json"
    TreeCanonicalization = "gentle-ai.release-artifact-tree/v1"
    SnapshotSchema       = "gentle-ai.release-semantic-capabilities/v1"
    EntryTypeFile        = "file"
    EntryMode            = "0644"
    AssetsArchiveID      = "assets"
)

type Entry struct {
    Path   string `json:"path"`
    Type   string `json:"type"`
    Mode   string `json:"mode"`
    Size   int64  `json:"size"`
    Digest string `json:"digest"`
}

func ValidateEntryPath(p string) error       // D3 path rules
func SortEntries(entries []Entry)            // ascending raw byte order
func TreeDigest(entries []Entry) (string, error)
func EncodeCanonical(v any) ([]byte, error)  // D3 JSON rules
func (m Manifest) Validate() error           // groups, references resolve into entries, tree recompute

// internal/cli — projection over the verified-pure static surface.
func ReleaseSemanticSnapshot(contract string) releaseartifact.SemanticSnapshot
```

## Testing Strategy

All Go tests are table-driven with `t.Run(tt.name, ...)`; filesystem work uses `t.TempDir()`.

| Layer | What to test | Approach |
|---|---|---|
| Unit — `releaseartifact` | Path rejection (absolute, `..`, backslash, NUL/control, empty segment, over-length, duplicate); type rejection (symlink, hardlink, device, FIFO, socket, unknown); mode rejection (any non-`0644`, setuid/setgid/sticky, executable) | One table row per adversarial case |
| Unit — canonical encoder | Indent, no HTML escaping, single trailing LF, no BOM, `[]` not `null`, field order | Byte assertion against a small fixed struct |
| Unit — tree digest | Known-vector preimage; order independence of input slice; manifest exclusion; `manifest_included: true` rejected | Table-driven, checked-in expected hex |
| Unit — contract fixtures | Schema `$schema`/`$id`/`additionalProperties:false` header; valid fixture decodes with `DisallowUnknownFields` and validates; `unsupported-major` fixture rejected with an error naming the major | Mirrors `TestReviewCapabilitiesSchemaAndFixtureAreStrict` (`review_capabilities_test.go:195`) |
| Unit — snapshot drift | Reflect over `ReviewCapabilitiesResult` field names: projected set MUST equal all fields minus exactly `{Package, Build, Executable}`. Fails when a new CLI field is added and neither projected nor consciously excluded | The anti-drift test |
| Unit — snapshot parity | `reflect.DeepEqual` per projected field against `reviewCapabilitiesStaticSurface(contract)` for both v1 and v2 contracts | Table-driven over contracts |
| Unit — snapshot exclusion | Generated bytes contain none of `"package"`, `"build"`, `"executable"`, `"sha256"`, `"vcs"`, `"go_version"`, `"module_version"`, `"release_channel"` | Substring scan on canonical bytes |
| Golden — snapshot | Canonical bytes vs `internal/releaseartifact/testdata/review-integration-v2.semantic.json`, regenerated only via `-update`; the golden's sha256 is also asserted against a checked-in constant so an accidental `-update` surfaces as an authored change | go-testing skill golden rule |
| Unit — required floor | The frozen floor is a subset of the live projection for operations, gates, projections, schemas; a **removal** breaks it, an addition does not | Table-driven |
| Unit — `releasepolicy` | See the matrix below | Synthetic `artifacts.json` per row |
| Integration | `internal/releaseassetscmd` builds a staging tree and manifest in `t.TempDir()`; recompute the tree digest from the staged files and compare | `t.TempDir()` |
| Evidence (not a Go test) | Snapshot run: `artifacts.json` shape, `tar -tf` member paths, `checksums.txt` line, formula sha256 before/after | A2 work-unit record |

### `internal/releasepolicy` RED tests, both directions

**Newly RED** (must fail before the amendment, pass after):

| Case | Expected |
|---|---|
| Four platform archives + one `id: assets` archive | nil error |
| Assets archive absent | assets archive count error |
| Assets archive duplicated | assets archive count error |
| Sixth archive with an unrelated `id` | `expectedCounts` type error |
| Assets archive carries GOOS/GOARCH/Target | platform-axis error |
| Assets archive name does not bind `snapshotVersion` | identity error |
| Assets archive declares `Binaries` | identity error |

**Regression locks** (pass today, must keep passing after — proving nothing was loosened):

| Case | Expected |
|---|---|
| A platform archive is missing | archive matrix incomplete |
| A platform archive has a wrong GOOS/GOARCH/Target | archive matrix changed |
| A platform archive has a non-`gentle-ai` binary | archive identity changed |
| A platform archive carries `Extra.ID: assets` | matrix incomplete — the split cannot smuggle a platform archive out of the matrix |
| A binary is missing from the four-platform matrix | binary matrix incomplete |
| Windows/Scoop artifact present | `expectedCounts` type error |
| Live `.goreleaser.yaml` edited without its constant | YAML changed error |
| Constant edited without the live file | YAML changed error |
| `TestReleaseDistributionPolicyAssertionFailsClosed` (`internal/update`) | still passes with the copied `policy.go` |

## Threat Matrix

| Boundary | Applicability | Design response | Planned RED tests |
|---|---|---|---|
| Documentation-like paths / executable-file classification | **Applicable** — archive entries are classified by type and mode | Only `type: "file"` and mode exactly `"0644"` are admitted; every link, device, FIFO, socket, unknown type and every executable/setuid/setgid/sticky mode is rejected at write and at read | One table row per rejected type and per rejected mode class (D3, unit tests above) |
| Git repository selection | **N/A** — no `git -C`, no cwd-derived repository authority; the generator reads a staged directory and `policy.go` already resolves its own root | — | — |
| Commit state | **N/A** — no index or worktree mutation in this change | — | — |
| Push state | **N/A** — the release workflow's publish path is unchanged; no new ref resolution | — | — |
| PR commands | **N/A** — A3 dispatches a `repository_dispatch` event; it opens no PR and composes no `gh pr` command | — | — |
| Cross-repository dispatch (added row, shell/process boundary) | **Applicable** | Empty-credential guard exits 0 before any network call; payload carries no digest so a compromised dispatch cannot pin one; `needs: verify` makes pre-verification execution structurally impossible | Shell-level: credential-absent path prints the skip line and exits 0; job-level ordering is asserted by workflow structure mirrored into `expectedReleaseWorkflowYAML` |

## File Changes

| File | Action | Unit |
|---|---|---|
| `contracts/release-artifact/v1/schemas/artifact-manifest.schema.json` | Create | A1 |
| `contracts/release-artifact/v1/fixtures/artifact-manifest.fixture.json` | Create | A1 |
| `contracts/release-artifact/v1/fixtures/artifact-manifest-unsupported-major.fixture.json` | Create | A1 |
| `internal/releaseartifact/{manifest,entry,canonical,tree,snapshot,floor}.go` + tests | Create | A1 |
| `internal/releaseartifact/testdata/review-integration-v2.semantic.json` | Create (golden) | A1 |
| `internal/cli/review_capabilities_snapshot.go` + test | Create | A1 |
| `internal/releaseassetscmd/main.go` + test | Create | A1 |
| `docs/release-artifact.md` | Create (rolling "Contract vX.Y adds…" changelog, `docs/review-integration.md` style) | A1 |
| `.goreleaser.yaml` | Modify — assembly per D5, `id:` per D7 | A2 |
| `internal/releasepolicy/policy.go` | Modify — counts, ID-keyed split, `expectedGoReleaserYAML`; `expectedReleaseWorkflowYAML` only if `release.yml` changes in the same unit | A2 |
| `internal/releasepolicy/policy_test.go` | Create/extend | A2 |
| `scripts/verify-release-assets.sh` | Modify — admit `gentle-ai_${version}_assets.tar.gz` in `archives=(…)` (`:23-29`), so both the remote asset-set diff and the signed-manifest diff enforce it | A2 |
| `docs/release-artifact.md` | Modify — publication and verification sections | A2 |
| `.github/workflows/release.yml` | Modify — `notify` job | A3 |
| `internal/releasepolicy/policy.go` | Modify — `expectedReleaseWorkflowYAML` in the **same commit** | A3 |
| `.github/workflows/notify-release.yml` | Create — `workflow_dispatch` replay | A3 |

## Work-Unit Boundaries

| Unit | Boundary | Independently revertible | Forecast |
|---|---|---|---|
| **A1** | Everything additive: contract namespace, `internal/releaseartifact`, the projection, the generator command (unwired), goldens, doc. Touches no `.goreleaser.yaml`, no `policy.go`, no workflow. Live `review capabilities` output is unchanged | Delete the new files; nothing else moves | **High** vs the 400-line budget. Split into **A1a** (contract namespace + canonical encoder + entry/path/mode/tree + manifest, with its fixtures) and **A1b** (snapshot projection + generator command + golden) if the authored forecast exceeds 400 |
| **A2** | Ordered: (1) empirical gate — snapshot run, record `artifacts.json` / `tar -tf` / `checksums.txt` / formula sha256, apply the D5 decision rule; (2) `.goreleaser.yaml` **and** `expectedGoReleaserYAML` in one commit; (3) `policy.go` split + counts with its RED tests; (4) `verify-release-assets.sh`; (5) docs | Revert all four files together; restores the exact four-archive release | Medium |
| **A3** | `notify` job + `expectedReleaseWorkflowYAML` in one commit, plus `notify-release.yml` | Remove the job and the file; publish and verify untouched | Low |

**Hard sequencing rule for A2 and A3**: every commit that edits a YAML file pinned by `policy.go` updates its embedded constant in that same commit. Otherwise the "Verify release distribution policy" preflight fails before GoReleaser runs.

## Migration / Rollout

No data migration. Adding a fifth asset is additive-safe for every existing install: `internal/update/upgrade/download.go` resolves exactly one per-platform filename and reads one checksum line; it never enumerates archives. Rollout order is A1 → A2 → A3 under one feature-branch chain, with release R cut **last**, after all provider changes settle — the contract shape freezes at first publish.

## Open Questions

- [ ] Branch A vs branch B (D5) — resolved empirically by A2 task 1 against a predefined decision rule; both branches are fully designed, so a negative result needs no design rework.
- [ ] Under branch B, the exact `artifacts.json` type key and count for the extra file — measured in the same task; the amendment adds an exact new entry, never a relaxed count.
- [ ] Shared-skill payload inclusion — deferred; the entry list is data, not shape, so it is a content change under layout v1.
