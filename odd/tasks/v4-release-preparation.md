# v4 release preparation

## Objective and authority
Publish Gentle AI v4.0.0, then pin it in Gentle Shell and publish Gentle Shell v4.0.0 (npm: gentle-pi). The user explicitly approved both versions, Windows corrections, the Go /v4 migration, isolated worktrees, and two separate preparation PRs: Windows first, then the atomic migration with a size:exception for the latter only. Preparation issues and status:approved were requested; exact target-bound label actions must still follow repository policy. Use the existing gh session over HTTPS for authorized GitHub operations. No ambient SSH, local npm publish, tag movement, history rewriting, or live-runtime installation.

Workspace: /Users/alanbuscaglia/work/gentle-ai-v4-release
Branch: chore/v4-release-preparation
Initial HEAD: d623137c32bfa928cd062b226fd06ae24b7ba4ca
Preserve unrelated changes in every other worktree.

## Evidence and constraints
- Latest stable is v3.7.0 in both projects. Breaking SDD retirement requires the approved major release.
- AI CI run 36454083243 passed at the initial HEAD; Windows Full Suite 36454083300 failed in rest and cli-r-other. Those native failures are RED evidence, not proof of the proposed fix.
- Windows failure classes: path separators, POSIX mode assumptions, JSON-escaped diagnostic paths, host/simulated OS mismatch, and platform-specific hook commands. Do not weaken privacy, ownership, JSONC refusal, or idempotence protections merely to pass tests.
- The published AI tag and main are not ancestor-related. PR #4912 has patch-equivalent replayed merges. Reconcile release accounting without rewriting history; naive tag-range commit totals are misleading.
- Provider contract 1.2.0 is byte-identical at the compared trees. Preserve it unless a separate demonstrated change is authorized. Keep intentional historical and cross-major test fixtures.
- Notes must describe generic Bridge-plugin support, not name Claude Bridge. Do not claim unverified universal compatibility.
- Native Windows symlink and live Pi E2E gaps from Shell PR #1527 remain explicit.

## Delivery and forecast
Strategy: sequential PRs to main (stacked-to-main), Windows first; then a compile-coherent /v4 migration with installation guidance and tests. User accepted size:exception only for the migration. Forecast: Windows 80–220 authored diff lines; migration 3,200–3,800+ plus 100–250 installation/guidance lines. The 635-file import move must not be split into uncompilable intermediate states. Record actual commit/PR boundaries below; no tags before exact-main release gates pass.

## Tasks
- [ ] W1 (checking; native Windows and full CLI suite pending): Correct the nine mapped Windows test surfaces, preserving meaningful platform-specific assertions. Delegated writer: multi-file, security-sensitive tests. Risk high. Native RED is recorded in the failed Windows run; local checks cannot prove Windows GREEN. Require reviewed correction, native Windows Full Suite, and ordinary CI before merge.
- [ ] W2: Migrate module/imports, GoReleaser linkage, release-policy expectations, current installers and guidance to /v4 in one coherent PR. Delegated writer: 635+ files and public installation contract. Risk high. Do not mutate protocol schema versions or intentional cross-major fixtures.
- [ ] W3: Audit deduplicated release accounting and draft canonical notes with explicit non-ancestry qualification and generic Bridge-plugin wording. Delegated read-only research followed by a bounded documentation writer; no invented counts.
- [ ] W4: Publish Gentle AI from verified main; verify release jobs, signed assets, post-publication Go module resolution, notes/advisory, and Homebrew. Parent owns remote release mutations; delegated verification.
- [ ] W5: Update Shell's pin from the published assets through an approved issue and verified PR. Derive exact edit surfaces and contract identity from release evidence, never invent capabilities. Delegated writer and verifier, risk high.
- [ ] W6: Publish Shell through publish.yml on main; verify pinned-runtime packaging, notes, exact npm version and latest tag. Parent owns release mutations; delegated verification. No local npm publish.

## Checks and closure
W1: focused mapped tests and their package suites; go run ./internal/gofmtcheck; go vet for affected packages; cross-compilation where useful (not runtime proof); exact-head Windows Full Suite and CI. Preserve the Windows failing-run evidence; obtain real GREEN remotely before completion. Broader checks are delegated with exact commands. Production files are out of the initial writer scope; demonstrated production defects must return to the parent.
W2: install-module-path script, update/releasepolicy/providercontractbundle tests, gofmt checker, go vet ./..., go test ./..., release preflight and exact-head Windows/CI. Before publication validate the local /v4 declaration and release snapshot; do not claim a nonexistent v4.0.0 tag resolves. After publication require go list -m against the actual v4 tag.
Each source work unit receives native review under the enabled RDD switch and an authorized local Conventional Commit. Preserve independent verification and report unavailable checks. PRs require approved same-repository issues, exact type labels, and all required checks. No merge or release on unknown/failing gates.

## Progress
Mapping and scope approval complete. W1 writer changed only the nine authorized test files (63 insertions, 26 deletions), preserving production code and parent-owned tracker. Source-only diff SHA-256: deb96932e63b8fcc73bd7d8a15d0e195e11bdbbfc8584129ccfc0a89c717006f; HEAD unchanged. Native Windows RED was read from run 36454083300 before editing. Focused tests passed on Darwin before/after, as did gofmt checker, affected-package vet, nine Windows cross-compilations and diff check. Compilation is not Windows runtime proof.
The full affected-package command first exceeded a 300-second command limit, then reached the internal/cli 10-minute test timeout while TestInstallLeavesNoDanglingSharedReferences/cursor was running. Other eight packages passed. Base causality is unknown; do not label it environmental without evidence. W1 remains partial. Independent verifier mulkqodv-x-rj52 found no severe test weakening. The isolated cursor subtest passed on candidate (0.36s) and a comparable HEAD archive (0.41s), with Go 1.26.2 on Darwin arm64; this does not explain the whole-package timeout. Independent focused tests, formatting, vet and diff checks passed with unchanged source hash. No full-suite retry or production correction is authorized.
Native assessment was unavailable due to the untracked tracker; the required independent high-risk verification has now completed, with native Windows runtime evidence still pending. An initial native START consent expired before any lineage was created; renewal requires fresh inspect/START, not reuse of that binding.
Issue https://github.com/Gentleman-Programming/gentle-ai/issues/5074 was created from the canonical bug form after duplicate checks. Exact human authorization, fresh ADMIN capability and atomic label readback confirmed bug + status:approved. The PR will use the human-selected Closes #5074. No release tags, PRs, commits or publication performed. W2 waits for the Windows slice. Rollback W1 independently; rollback the compile-coherent W2 as a whole, never move published tags.
