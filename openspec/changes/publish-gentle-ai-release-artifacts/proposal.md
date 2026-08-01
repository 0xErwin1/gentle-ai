# Proposal: Publish Gentle AI Release Artifacts (Change A, provider-only)

## Intent

Gentle AI's contracts, schemas, capability surface and docs reach Gentle Pi by **hand transcription**. Every provider change silently creates consumer drift that is discovered late, by a human, and repaired by copying bytes across repositories with no authenticity, no version identity and no failure mode. The consumer currently pays for this in duplicated format ownership, pinned digests that must be re-derived by hand, and parity bugs that only surface at runtime.

This change makes the provider **declare** what a release contains: a versioned, self-describing, platform-independent assets archive covered by the existing signed `checksums.txt` envelope, plus a deterministic semantic capability snapshot. The consumer then verifies a declaration instead of reproducing a transcription.

## Scope

### In Scope (provider repository only)

| Unit | Deliverable | Depends on |
|---|---|---|
| **A1** | New `contracts/release-artifact/` namespace (schema + fixtures incl. `unsupported-major`), the deterministic semantic-snapshot projection, canonicalization rules, and the mandatory-feature policy declaration (`unknown_mandatory: reject`) | — |
| **A2** | Assets-archive assembly in `.goreleaser.yaml`, additive-only `internal/releasepolicy/policy.go` amendment, `scripts/verify-release-assets.sh` update, Homebrew `ids:` decision, docs | A1 |
| **A3** | Post-verification `notify` job (`needs: verify`) with credential-gated cross-repo dispatch | A2 |

Delivery: three repository-local PRs under one Gentle AI feature-branch tracker (`feature-branch-chain`).

### Out of Scope (non-goals)

- Anything Pi-specific: no agents, chains, adapters, or `internal/assets/pi/`.
- Generated TypeScript/client decoders — Gentle Pi owns host behavior.
- Loosening the existing release-shape exactness into a generic "N archives" rule.
- A prerelease/RC path: `.github/workflows/release.yml:10` excludes `!v*-*`; this ships as a normal stable patch.
- Publishing release R itself (separate unit, after the tracker merges).

## Capabilities

### New Capabilities

- `release-artifact-contract`: versioned self-describing manifest, bundled schema, canonical entries/tree digest, unsupported-major fail-closed behavior.
- `semantic-capability-snapshot`: deterministic, platform-independent capability projection and its compatibility policy.
- `release-artifact-publication`: assets-archive assembly, release-shape policy exactness, checksum/signature coverage, remote asset verification.
- `downstream-release-notification`: post-publish/post-verification ordering, credential gating, visible non-fatal failure, replay.

### Modified Capabilities

None. No existing `openspec/specs/` capability changes at requirement level.

## Approach

1. **Contract in its own namespace**, versioned independently from `review-integration`, because bootstrap-time archive trust and runtime CLI negotiation are different negotiated surfaces with different consumers. Documented in the rolling "Contract vX.Y adds…" style of `docs/review-integration.md`.
2. **Snapshot as a hybrid projection.** `reviewCapabilitiesStaticSurface` (`internal/cli/review_capabilities.go:200`) is already pure — no OS, runtime, or build calls. Project it into an independently-versioned type; a golden diff test proves no drift. Exclude `Build`, `Executable`, and package release identity (release identity lives in the manifest).
3. **Digest without self-reference.** Signed `checksums.txt` binds the whole archive; the internal manifest binds a canonical tree digest over sorted entries **excluding the manifest itself**.
4. **Archive assembly decided empirically, not debated.** A2's first task inspects `dist/artifacts.json` from a local `goreleaser release --snapshot --clean --skip=sign,publish` run and chooses `archives[].meta: true` (distinct `id:`, explicit `ids: [default]` on `brews:`) versus `before.hooks` + `checksum.extra_files`. This is a **scheduled gate with a defined decision rule** — pick `meta: true` only if its artifact shape and Homebrew interaction permit a clean additive `policy.go` change; otherwise fall back. Snapshot and manifest logic are identical either way, so A1 is unblocked.
5. **Release-policy amendment stays exact-plus-one.** Add a strict new branch for the named metadata archive; never relax the four-platform matrix, the per-archive binary requirement, or the Windows/Scoop prohibition.

## Affected Areas

| Area | Impact | Description |
|---|---|---|
| `contracts/release-artifact/` | New | Schema, fixtures, contract doc |
| `internal/cli/review_capabilities.go` | Modified | Add projection reading the existing pure static surface; live response unchanged |
| `internal/releasepolicy/policy.go` | Modified | `expectedCounts`, platform-matrix split, **and both** embedded YAML constants |
| `.goreleaser.yaml` | Modified | Assets archive entry / hooks + `checksum.extra_files`; explicit archive `id:`; `brews: ids:` |
| `scripts/verify-release-assets.sh` | Modified | Hardcoded four-archive list (~:23-29) admits the new asset |
| `.github/workflows/release.yml` | Modified | New `notify` job with `needs: verify` |
| `docs/` | New | Contract doc with rolling changelog |

## The Immutability Consequence

Once the release publishes, the contract shape is **frozen** for Gentle Pi's bootstrap decoder. A major bump is a real compatibility event with a fail-closed consumer path, not a quick fix.

Therefore: **cut release R as late as possible, after all provider changes have settled — never as an early unblocking step.** Consumer development is unblocked by the local unsigned GoReleaser snapshot (S), not by publishing. Field lists and canonicalization rules must be right before the first publish.

## Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| **`policy.go` gets loosened rather than extended.** It fails closed and loudly, so breakage is not the danger — erosion is. It exists to make the release shape assertable, not guessable. | Med | Design mandates an exact new branch keyed to the metadata archive's name and ID; an explicit review criterion rejects any open-ended count or relaxed `compareYAML` |
| `Validate()` diffs the **live** `.goreleaser.yaml` (:36) and `release.yml` (:44) against embedded constants `expectedGoReleaserYAML` (:496) / `expectedReleaseWorkflowYAML` (:566) via `compareYAML` (:197). Any YAML edit without its constant update fails CI before GoReleaser runs. | High | Sequence it: every A2 task that edits a YAML file updates its constant in the same commit. Anticipated, not discovered mid-review |
| `meta: true` artifact shape unverified (may report `Type: Archive` without GOOS/GOARCH) | Med | Scheduled empirical gate at A2 task 1 with a predesigned fallback |
| `brews:` has no `ids:` filter; formula is user-facing and this surface has never seen >1 archive | Med | Explicit distinct archive `id:` + `ids: [default]`; verify `dist/homebrew/Formula/gentle-ai.rb` from the existing snapshot preflight |
| Contract shape frozen at first publish | High impact | Fixture-tested major/minor policy; publish R last |
| Cross-repo dispatch credential unavailable at A3 | Med | Ship credential-gated and inert; `repository_dispatch` only fires for a workflow already on the target default branch, so A3 waits on Pi's P4 receiver regardless |
| Existing installs broken by a fifth asset | Low | Verified additive-safe: `internal/update/upgrade/download.go:171` resolves exactly one filename by format and reads one checksum line; it never enumerates archives |

## Rollback Plan

Each unit is independently revertible in dependency-reverse order:

- **A3**: remove the `notify` job. Publish and verify are untouched.
- **A2**: revert `.goreleaser.yaml`, `policy.go` (including both YAML constants), and `verify-release-assets.sh` together. Restores the exact four-archive release.
- **A1**: delete the contract namespace, projection, and tests. Live `review-capabilities` output was never modified.

A published immutable release is never rewritten; an invalid R is quarantined and superseded by a corrective version.

## Dependencies

- **None blocking A1.** This is greenfield inside an already-strict pipeline.
- **Bootstrap path**: `goreleaser release --snapshot --clean --skip=sign,publish` (already run by the CI preflight) produces `dist/artifacts.json` plus real archives locally. Gentle Pi's P1–P3 consume this through an explicit bootstrap input. It is unsigned and carries no release identity — every record from it is **development/bootstrap evidence**, never live signed release provenance.
- **A3 live activation** waits on Gentle Pi's P4 default-branch receiver and a provisioned scoped credential. Neither blocks A1, A2, or R.

## Success Criteria

- [ ] A published release contains the four platform archives **plus exactly one** named assets archive, all covered by signed `checksums.txt`.
- [ ] The assets archive decodes against its own bundled schema; the `unsupported-major` fixture fails closed with an actionable error.
- [ ] The semantic snapshot is byte-identical across Linux and macOS and contains no `Build`, `Executable`, or package-version field.
- [ ] A golden diff test fails if `reviewCapabilitiesStaticSurface` and the projection drift apart.
- [ ] `internal/releasepolicy` still rejects a missing platform archive, a wrong GOOS/GOARCH, a non-`gentle-ai` binary, and any un-mirrored YAML edit.
- [ ] `dist/homebrew/Formula/gentle-ai.rb` from a snapshot run is unchanged versus today.
- [ ] `scripts/verify-release-assets.sh` passes against the live release with the new asset and fails if it is missing.
- [ ] The `notify` job runs only after `verify` succeeds, and its failure leaves the published, verified release valid.
