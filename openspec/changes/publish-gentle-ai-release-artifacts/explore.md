# Exploration: publish-gentle-ai-release-artifacts (Change A, provider-only)

## Current State

Gentle AI's release pipeline (`.goreleaser.yaml`, `.github/workflows/release.yml`) builds Linux/macOS binaries into **exactly 4 platform archives**, packages `contracts/review-integration/v1/{schemas,fixtures}` (never v2, even though gentle-pi is v2-only), signs only `checksums.txt` with minisign, and publishes via Homebrew tap.

Four independent layers enforce this exact shape — the fourth was not previously documented:

| Layer | File | Mechanism |
|---|---|---|
| CI structural gate | `internal/releasepolicy/policy.go` `validateArtifacts` | `reflect.DeepEqual` on `expectedCounts{Metadata:1, Binary:4, Archive:4, Checksum:1, Homebrew Formula:1}` (~:296); per-archive `Extra.ID=="default"`, `Format=="tar.gz"`, `Binaries==["gentle-ai"]` (~:359); GOOS/GOARCH-derived name/target matrix (~:315-368) |
| **CI byte-exact gate (newly confirmed)** | same file, `expectedGoReleaserYAML` (:496) / `expectedReleaseWorkflowYAML` (:566) | `Validate()` reads the **live** `.goreleaser.yaml` (:36) and `release.yml` (:44) off disk and diffs their full YAML AST against these embedded Go string literals via `compareYAML` (:197). This is stricter than structural — it is byte-semantic equality against a frozen copy. **Any edit to either file must update the matching constant in the same commit**, or the "Verify release distribution policy" preflight fails before GoReleaser runs. |
| Remote gate | `scripts/verify-release-assets.sh` | Hardcoded `archives=(...)` list of exactly the 4 platform tarballs plus `checksums.txt` and `checksums.txt.minisig` (~:23-29), diffed against the live GitHub release API |
| Consumer gate | gentle-pi `scripts/gentle-ai-installer.mjs` | Per-platform pinned archive and binary digests |

`internal/cli/review_capabilities.go` confirms the platform-dependence problem: `buildReviewCapabilities` (~:171) always decorates the response with `Build` (VCS/VCSRevision/VCSTime/VCSModified plus a sha256 `ID` over those, ~:357) and `Executable.SHA256` (a hash of the running binary, ~:375).

**However**, `reviewCapabilitiesStaticSurface(contract)` (:200) — the function producing `Schema`/`Contract`/`Protocol`/`Operations`/`Gates`/`Projections`/`Schemas`/`Features`/`Compatibility`/`Bootstrap` — is already a **pure function with no OS, runtime or build calls**. It takes only a contract string and returns identical bytes on every platform today. This is the natural seam for the semantic snapshot.

`internal/update/upgrade/download.go` confirms the self-updater is single-asset and per-platform: `resolveArchiveName` (~:170) builds exactly one filename from `{repo}_{version}_{os}_{arch}.tar.gz`, and `expectedChecksumFor` (~:292) extracts one manifest line. It never enumerates archive count, so **adding a fifth archive is additive-safe for every existing install** — verified, not assumed.

No provider release-artifact contract, semantic-snapshot generator, or notification code exists yet. This is a greenfield build inside an already-strict release pipeline.

## Affected Areas

- `internal/cli/review_capabilities.go` — `reviewCapabilitiesStaticSurface` is the snapshot source; `ReviewCapabilitiesBuild` and `ReviewCapabilitiesExecutable` are precisely the fields that must NOT appear in the shipped snapshot.
- `.goreleaser.yaml` — needs a new archive entry (or `before.hooks` + `checksum.extra_files`) and the v2 contract files added to the packaged file list.
- `internal/releasepolicy/policy.go` — `expectedCounts`, the per-archive platform-matrix loop, and **both** embedded YAML constants must change together. This is the security-adjacent surface.
- `scripts/verify-release-assets.sh` — hardcoded expected-asset list must admit the new archive name.
- `.github/workflows/release.yml` — needs a post-verification notification job; `brews:` needs an explicit `ids:` decision.
- `docs/review-integration.md` — establishes the rolling "Protocol vX.Y adds…" changelog pattern to reuse for the new contract's doc.
- New package location follows the existing convention (`internal/releasepolicy`, `internal/reviewtransaction`).

## Contract Design Options

The plan fixes that Gentle AI owns the format. Open question is shape and location only; the illustrative `contracts/release-artifact/v1/...` path, schema ID and field names are explicitly NOT approved.

| Approach | Description | Pros | Cons | Effort |
|---|---|---|---|---|
| A. Mirror `review-integration` layout | Same directory and naming pattern as the existing negotiated contract | Reuses a pattern reviewers know; repo-consistent | Couples an install-time artifact contract to a runtime-negotiation contract's shape despite different consumers | Low |
| B. Separate top-level namespace | `contracts/release-artifact/` with its own major/minor lifecycle, doc and fixtures, independent of `review-integration` | The two surfaces version independently — a review-integration minor bump does not become a release-artifact compatibility event, or vice versa | Slightly more scaffolding | Low–Medium |

**Recommendation: B.** The release-artifact manifest and the review-integration protocol are different negotiated surfaces (bootstrap-time archive trust vs. runtime CLI negotiation) and must version independently. Follow the `docs/review-integration.md` precedent: one doc with an explicit "Contract vX.Y adds…" changelog, plus an `unsupported-major` fixture proving the decoder must reject an unknown major.

**Digest self-reference:** the external `checksums.txt` binds the whole archive; the internal manifest binds a canonical tree digest over sorted entries **excluding the manifest file itself**. This avoids two-phase placeholder-then-patch generation. No better alternative found; adopt as specified in plan section 5.

## Deterministic Snapshot Design

**Include** (all verified pure, from `reviewCapabilitiesStaticSurface`): `Schema`, `Contract`, `Protocol{Major,Minor}`, `Operations`, `Gates`, `Projections`, `Schemas`, `Features{Mandatory,Optional}`, `Compatibility`, `Bootstrap`.

**Exclude** (platform, build or host dependent; added only in `buildReviewCapabilities`): all of `Build` (VCS, VCSRevision, VCSTime, VCSModified, GoVersion, ModuleVersion, ID digest), all of `Executable` (SHA256, Evidence, Verification), and `Package.Version`/`ReleaseChannel` (release identity already lives in the manifest's release block; duplicating it invites conflict).

| Approach | Description | Pros | Cons | Effort |
|---|---|---|---|---|
| A. Call `reviewCapabilitiesStaticSurface` directly | Wrap its output in a release-artifact envelope | Zero duplicated logic; future gates/features changes flow through automatically | Couples the archive schema 1:1 to the CLI response shape — a cosmetic capabilities change forces a release-artifact contract event | Low |
| B. Independent hand-written struct | Structurally similar but a separate type | Clean lifecycle decoupling | Duplicated field lists risk silent drift | Medium |
| C. Hybrid projection | Internally call `reviewCapabilitiesStaticSurface`, externally project into a distinct independently-versioned envelope | No duplication of the gates/features source of truth AND independent schema lifecycle; a golden diff test proves no drift | One extra projection function | Medium |

**Recommendation: C.** One small projection function taking the already-pure static surface and reshaping it into its own versioned type.

**Determinism proof:** because the source function makes zero host calls, byte-stability is nearly free. It needs a canonical encoder (fixed field order, stable indentation, single trailing LF, no BOM) plus a checked-in golden fixture asserting the encoded snapshot's SHA-256. Running that golden on both Linux and macOS is cheap corroborating evidence, not a correctness requirement.

## Archive Shape

**Verified:** GoReleaser `archives[].meta: true` (archive with files but no binaries) is documented and has existed since v2.6, well within the pinned v2.15.2. Its `name_template` must not reference `{{ .Os }}`/`{{ .Arch }}`.

**Unverified — must be the first empirical A2 task:** the exact shape a `meta: true` archive takes in `dist/artifacts.json`, specifically whether it reports `Type: "Archive"` (colliding with the current assumption that every Archive entry has GOOS/GOARCH and `Binaries == ["gentle-ai"]`) and whether its `Extra.ID` can differ from `"default"`.

| Approach | Description | Pros | Cons | Effort |
|---|---|---|---|---|
| A. Native `meta: true` | A fifth `archives:` entry with a distinct `id:`, content generated via `before.hooks` | Native feature; automatic checksum and signature coverage | Reported as `Archive` type, so it will break the per-archive platform-matrix loop, forcing a restructure that splits platform archives from the one named metadata archive by ID; `brews:` interaction unverified | Medium |
| B. `before.hooks` + `checksum.extra_files` | A generator assembles the tarball outside the archive pipe and registers it in the signed manifest | Does not appear as an Archive entry at all — `expectedCounts` and the matrix loop stay untouched; smaller blast radius on the security-adjacent file | Custom assembly code (path/mode/type confinement, sorted entries) | Medium |

**Recommendation:** attempt A first as the empirical A2 task, but treat B as the safer default if A's artifact shape or Homebrew interaction would force loosening `policy.go` beyond a clean additive change. The snapshot generator and canonical manifest logic are identical either way, so this does not block A1.

## Release-Policy Amendment (primary risk)

Three things change together, and none may loosen the four-platform matrix, the per-archive binary requirement, or the Windows/Scoop prohibition:

1. `expectedCounts` gains one Archive — or, under Approach B, no count change at all.
2. The per-archive loop must stop assuming every Archive entry has GOOS/GOARCH. It needs a branch validating the four platform archives exactly as today, plus a **separate and equally strict** validation of the metadata archive by its distinct name and ID — exact name template, exact file list, exact mode and type policy. Not a loosened generic check.
3. `expectedGoReleaserYAML` and, if `release.yml` changes, `expectedReleaseWorkflowYAML` must be updated byte-for-byte in the same PR.

The design must resist relaxing `compareYAML` or `expectedCounts` into something open-ended ("N archives allowed"). That would defeat the file's purpose, which is to make the release shape assertable rather than guessable. Keep it exact-plus-one.

## Homebrew Safety

GoReleaser's brew pipe requires exactly one archive per OS/Arch pair for a tap; an unspecified `ids:` means "consider all archive IDs." Today there is one archives entry (implicit `id: default`), so this has never mattered. Adding a fifth archive without a distinct `id:` risks an ID collision or an implicit-but-unaudited exclusion that works today and breaks on a future GoReleaser upgrade.

**Recommendation:** give the new archive an explicit non-default `id:` and add an explicit `ids: [default]` to `brews:`. Prove no regression by inspecting `dist/homebrew/Formula/gentle-ai.rb` — the existing CI preflight already produces it in snapshot mode with `--skip=sign,publish`, so the formula pipe runs locally without publishing.

## Notification Design (A3)

- **Ordering:** a new `notify` job with `needs: verify`, so it runs only after the archive is published **and** remote-asset verification passed.
- **Mechanism:** cross-repository dispatch with a scoped PAT or GitHub App token. The default `GITHUB_TOKEN` cannot dispatch across repositories — a real credential dependency.
- **Activation gating:** GitHub triggers `repository_dispatch` only for a workflow already present on the target's default branch, and gentle-pi's receiver (P4) does not exist yet. Posting with no receiver is harmless, but the plan forbids activating live dispatch before P4 lands. Build A3 now and gate the step on the presence of the credential, so it is inert by absence of a secret until the token is provisioned after P4 merges. This avoids a second code change later while respecting the ordering constraint.
- **Failure semantics:** the job is structurally unable to affect release validity — it runs strictly after `verify` passes, in its own job. A failure is visible (red job) and retryable (re-run that job, or a `workflow_dispatch` replay for a specific tag), while the published release and its green verification remain untouched.

## Bootstrap (Local Snapshot)

`goreleaser release --snapshot --clean --skip=sign,publish` already runs in the preflight job, and its output is already inspected structurally by `internal/releasepolicycmd`. This is exactly the bootstrap the plan calls for: it produces `dist/artifacts.json` plus real archive and formula files, unsigned, locally, with no network publish.

gentle-pi's P1–P3 development consumes this local tarball through an explicit bootstrap input, but per the plan's evidence boundary it must never substitute for release provenance: no signature exists, no GitHub release identity exists, and the checksum file is not minisign-covered under `--skip=sign`. Every record produced against it is labeled development/bootstrap evidence, never live signed release.

## Work-Unit Boundaries

| Unit | Scope | Independently revertible | Depends on |
|---|---|---|---|
| **A1** | New release-artifact contract namespace, the semantic-snapshot projection function, and the mandatory-feature policy declaration (`unknown_mandatory: reject`, already the live default, carried into the new contract's compatibility block) | Yes — purely additive new package and contract; no `.goreleaser.yaml` or `policy.go` change | Nothing |
| **A2** | `.goreleaser.yaml` archive addition, `policy.go` amendment (counts, matrix split, both embedded YAML constants), `verify-release-assets.sh` update, docs | Yes — reverting restores the exact four-archive behavior | A1 |
| **A3** | New `notify` job, credential-gated dispatch step | Yes — removing it leaves publish and verify untouched | A2 |

Each is a repository-local, reviewable PR under the `feature-branch-chain` decision.

## Recommendation

1. Contract in a separate `contracts/release-artifact/` namespace, documented in the existing rolling-changelog style.
2. Snapshot via hybrid projection reusing `reviewCapabilitiesStaticSurface`, with a golden diff test proving no drift.
3. Archive: try `meta: true` empirically first; fall back to `before.hooks` + `checksum.extra_files` if it forces loosening `policy.go`.
4. Release policy: additive-only amendment, byte-exact co-update of both embedded YAML constants, no relaxation of existing assertions.
5. Homebrew: explicit distinct archive `id:` plus explicit `ids: [default]` on `brews:`, proven via snapshot formula inspection.
6. Notification: `needs: verify` plus credential gating, so A3 ships inert until P4's receiver exists.

## Risks

- `policy.go` performs byte-exact AST comparison of both YAML files against embedded constants. Every A2 file edit needs its matching constant updated in the same commit or CI fails closed immediately. Fail-closed is correct, but it must be anticipated in task sequencing rather than discovered mid-review.
- The `meta: true` artifact shape is unverified; if it reports as Archive without GOOS/GOARCH, the per-archive loop needs restructuring regardless of assembly approach. Real work, not a formality.
- `brews:` has no `ids:` filter today. Adding a second archive is the first time this configuration surface is exercised, and the Homebrew formula is user-facing.
- `policy.go` exists to make the release shape non-guessable. The design must add an exact new branch, never loosen existing exactness into something generic.
- Once A1 merges and the release publishes, the contract shape is effectively frozen for gentle-pi's bootstrap decoder. A major bump is a real compatibility event, not a quick fix — get the field list and canonicalization rules right before publishing.
- Cross-repository notification credentials may be unavailable when A3 lands. Credential gating keeps this from blocking A2 or the release, but provisioning the token is separate follow-up work.

## Ready for Proposal

Yes. Contract shape, snapshot source, archive assembly and notification ordering resolve to specific evidence-based recommendations, with one genuinely open technical question (`meta: true` vs. hooks plus extra_files) explicitly deferred to an empirical first task in A2 rather than left ambiguous.
