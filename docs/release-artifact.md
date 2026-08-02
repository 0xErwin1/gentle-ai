# Release Artifact Contract

← [Back to README](../README.md)

Gentle AI publishes a platform-independent assets archive with every release, described by a versioned, self-describing manifest. This is the synchronization artifact a consumer decodes to trust bundled contracts, schemas, and snapshots instead of hand-transcribing provider internals. The contract lives in its own namespace, independent of the runtime-negotiated `review-integration` contract, so a bootstrap-time archive trust decision and a live CLI negotiation can version on separate timelines.

> This document covers the manifest contract, the semantic capability snapshot it bundles, and the staging generator that assembles a payload and emits the manifest. The archive is not yet wired into the GoReleaser release build or published — that wiring, the release-policy amendment, and downstream notification are tracked separately and will extend this document as they land.

## Contract identity

| Item | Value |
| --- | --- |
| Namespace | `contracts/release-artifact/v1/` |
| Schema file | `schemas/artifact-manifest.schema.json` |
| Schema `$id` | `https://gentle-ai.dev/contracts/release-artifact/v1/schemas/artifact-manifest.schema.json` |
| Contract ID | `gentle-ai.release-artifact` (major `1`, minor `0`) |
| Manifest schema identity | `gentle-ai.release-artifact-manifest/v1` |
| Fixtures | `fixtures/artifact-manifest.fixture.json`, `fixtures/artifact-manifest-unsupported-major.fixture.json` |
| Manifest member | `artifact-manifest.json`, at the archive root |
| Semantic snapshot schema identity | `gentle-ai.release-semantic-capabilities/v1` |
| Tree canonicalization identity | `gentle-ai.release-artifact-tree/v1` |
| Go package | `internal/releaseartifact` (stdlib-only, no internal imports) |
| Assets archive name | `gentle-ai_{version}_assets.tar.gz`, GoReleaser `id: assets` |

A consumer that supports only contract major `1` MUST reject a manifest declaring any other major with an actionable error naming that major, before attempting any layout inference. The `unsupported-major` fixture is byte-identical to the valid fixture except for the fields that carry the major identity, so it proves rejection happens on the major alone.

## Mandatory manifest field groups

Every manifest carries, at minimum:

| Field group | Carries |
| --- | --- |
| `contract` | Contract id, major, minor, schema id, and schema path — a complete decode without any external fetch. |
| `release` | Repository, tag, semantic version, and immutable commit. |
| `layout` | A layout version, independent of the release version. |
| `archive` | The published asset name and its digest source (`signed-checksums.txt`; the manifest never carries an archive self-digest). |
| `references` | `semantic_snapshots` (an array, so a future addition never forces a breaking singular-to-plural reshape) and `contracts`, naming every applicable provider contract bundled in the archive. |
| `tree` | The non-recursive digest binding described below. |
| `compatibility` | `unknown_mandatory: reject`, carried into the archive so a consumer never silently widens its supported set. |
| `entries` | The ordered, canonical content list. |

A manifest missing the release identity or layout identity group MUST fail closed with an actionable error naming the missing group. Every path a manifest references — `contract.schema_path`, each `references.semantic_snapshots[].path` — MUST resolve into `entries`; an unresolved reference is a validation failure.

## Canonical entry records

Each entry in `entries` carries a confined relative path, a type restricted to regular files, a normalized non-executable mode, and a `sha256:` content digest over exact file bytes:

| Rule | Detail |
| --- | --- |
| Path | Relative, forward slashes only. Rejected: absolute paths, backslashes, `..`/`.`/empty segments, NUL or control bytes, paths over 1024 bytes, segments over 255 bytes. |
| Uniqueness / order | Unique after validation; ascending **raw UTF-8 byte** order. No case folding, no Unicode normalization. |
| Type | `"file"` only. Directories are implicit; the writer emits no directory members. Symlinks, hardlinks, devices, FIFOs, sockets, and any unknown type are rejected. |
| Mode | Exactly `"0644"`. Any executable, setuid, setgid, or sticky bit is rejected at write and at read. |
| Digest | `"sha256:"` followed by exactly 64 lowercase hex characters, over exact file bytes. |

A path traversal attempt or a disallowed entry type MUST be rejected before extraction proceeds further. A digest mismatch between an entry's declared value and the extracted file's actual sha256 MUST be rejected with an actionable mismatch error.

## The non-self-referential tree digest

The manifest's `tree.digest` is computed only over the sorted canonical entries and deliberately **excludes the manifest file itself** — `tree.manifest_included` is always the constant `false`, and a decoder MUST reject `true` as an unknown layout.

The preimage is the literal tag `"gentle-ai.release-artifact-tree/v1"`, a NUL byte, then per entry in ascending path order: `path "\x00" type "\x00" mode "\x00" size "\x00" digest "\n"`, using the exact manifest field strings so a consumer hashes what it actually read. `size` is decimal, unpadded. The digest is independent of the input slice's order — entries are always sorted before hashing.

The **complete-archive** digest never comes from a field inside the manifest. It comes only from the externally signed `checksums.txt`/minisign envelope, which the manifest recursion would otherwise undermine.

## Canonical JSON encoding (`EncodeCanonical`)

Every canonical document in this contract — the manifest and the semantic snapshot it references — is encoded with the same rules:

- UTF-8, no BOM, LF-only line endings, exactly one trailing newline.
- Two-space indent, no HTML escaping.
- Key order follows Go struct declaration order — never map-key sorting.
- Empty collections encode as `[]`, never `null`.
- Unknown fields are rejected everywhere: the schema declares `additionalProperties: false` at every level, and the Go decoder uses `DisallowUnknownFields`.
- The minor-version policy is additive-fields-only; a new required field or a changed meaning is a major bump.

## Semantic capability snapshot

The archive bundles a deterministic, platform-independent snapshot of Gentle AI's declared review-integration capability surface — contract identity, protocol version, operations, gates, projections, schemas, features, bootstrap, and compatibility policy. It is **generated**, never hand-authored: `internal/cli.ReleaseSemanticSnapshot(contract)` projects the same pure static surface function (`reviewCapabilitiesStaticSurface`) that the live `review capabilities` command already uses, so the snapshot can never drift from what the provider actually declares.

| Excluded (never present) | Why |
| --- | --- |
| `package`, `build`, `executable` field groups | Package/release, Go build/VCS, and executable-hash identity depend on which platform's binary produced the snapshot. |
| `sha256`, `vcs`, `go_version`, `module_version`, `release_channel` | Sub-fields of the excluded groups above; a golden byte-comparison test and a substring-exclusion test both guard this. |

Because every excluded field is host-dependent, the snapshot built on Linux and the snapshot built on macOS for the same revision are **byte-for-byte identical** — the entire point of shipping it inside a platform-independent archive.

The live `review capabilities` CLI response is completely unaffected: `reviewCapabilitiesStaticSurface` is unexported and pure (no `os`, `runtime`, `time`, or filesystem call), and `ReleaseSemanticSnapshot` only reads from it — it never mutates or wraps it.

## Required floor (compatibility)

`internal/releaseartifact.RequiredFloor` is a **hand-declared, frozen** minimum subset of operations, gates, projections, and schemas — never recomputed from the live surface at generation time. A floor computed from "whatever exists right now" would move with every release and a removal would go silently unnoticed; a frozen floor catches that removal instead.

- `VerifyRequiredFloor(snapshot)` fails the moment any frozen floor element is missing from a live snapshot.
- Adding a new operation, gate, projection, or schema beyond the floor is never an error — only a **removal** is.
- Widening the floor to include a newly-important element is a deliberate, reviewed source edit, not something a generator run ever does automatically.

## Generating the release assets payload (`internal/releaseassetscmd`)

Run the generator against a scratch staging directory:

```bash
go run ./internal/releaseassetscmd \
  --root . \
  --staging-dir dist/release-assets \
  --repository Gentleman-Programming/gentle-ai \
  --tag v2.3.0 --version 2.3.0 --commit <commit-sha>
```

It stages the entire `contracts/` namespace, this document, `LICENSE`, and the generated semantic snapshot (`capabilities/review-integration-v2.semantic.json`), sorts every entry by ascending raw path bytes, computes the tree digest, and writes `artifact-manifest.json` at the staging directory root — validating the manifest it just built before exiting.

> **Not yet wired into `.goreleaser.yaml`.** This command is deliberately standalone in this contract revision so it can be tested independently of the release pipeline. Wiring it into the GoReleaser build (`before.hooks` or an equivalent) is tracked separately.

## Regenerating the semantic snapshot golden

`internal/releaseartifact/testdata/review-integration-v2.semantic.json` is a checked-in golden asserted byte-for-byte against the live generated snapshot, with its sha256 also pinned in a Go constant so an accidental regeneration cannot pass unnoticed:

1. Run `go test ./internal/releaseartifact/... -run TestSemanticSnapshotGolden -update`.
2. **Read the diff.** Confirm every change is an intentional capability-surface change (a new operation, schema, gate, feature, and so on) — never a stray formatting or host-identity change.
3. Rerun `go test ./internal/releaseartifact/... -run TestSemanticSnapshotGolden` **without** `-update` to confirm it now passes.
4. Update the `goldenSemanticSnapshotSHA256` constant in `snapshot_golden_test.go` to the value the failing run reported, in the same commit as the golden file.

## Checklist for a consumer

- [ ] Reject an unsupported contract major before inferring any layout.
- [ ] Validate the manifest against the bundled schema with unknown fields rejected.
- [ ] Confirm every mandatory field group is present and every reference resolves into `entries`.
- [ ] Recompute the tree digest from the archive's non-manifest entries and compare it to `tree.digest`.
- [ ] Establish archive authenticity only from the signed `checksums.txt` envelope, never from a manifest-internal field.
- [ ] Verify the frozen required floor is present in the semantic snapshot before treating additions beyond it as unsupported.

## Rolling changelog

### Contract v1.0 (unreleased)

Initial publish of the `gentle-ai.release-artifact` namespace: contract identity, the canonical JSON encoder, entry path/type/mode admission, the non-self-referential tree digest, and the manifest shape with its schema and fixtures.

Added the deterministic semantic capability snapshot (`internal/cli.ReleaseSemanticSnapshot`, `internal/releaseartifact.SemanticSnapshot`), the frozen required-floor compatibility check (`internal/releaseartifact.RequiredFloor` / `VerifyRequiredFloor`), the `internal/releaseassetscmd` staging generator, and its checked-in golden. The generator is not yet wired into the GoReleaser release build.
