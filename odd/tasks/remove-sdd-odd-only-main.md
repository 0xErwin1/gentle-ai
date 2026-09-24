# Reapply ODD-only retirement on current gentle-ai main

## Objective and problem
Deliver approved issue #4959: retire SDD/OpenSpec from the current tree so ODD is the only development workflow. The original feature branch was based on a divergent history, and a direct PR would carry unrelated mainline changes. Reapply only the feature onto current main without rewriting or modifying the original branch.

## Scope and constraints
- Authorized branch: `feat/remove-sdd-odd-only-main`, based on `f182ea2018a6399f5d1b6557cf36d71a3df0f723`. Original `feat/remove-sdd-odd-only` and commits `a20da5a0c`, `b07776996`, `688ebca26` remain untouched. Do not read or stage the unknown untracked `internal/cli/AGENTS.md` in any existing checkout.
- Retire SDD/OpenSpec-only code, assets, tests, docs, bench journeys and current-tree historical artifacts; preserve ODD, independent RDD, Strict TDD and user-owned installation assets. Retain original AI-5 installed-byte ownership and rollback protections as part of migration safety.
- User authorized local implementation, work-unit commits, push, PR and merge in `Gentleman-Programming/gentle-ai` via authenticated `gh` session, subject to issue, label, size, CI and ordinary policy gates. The original user choice favored a single coherent PR with documented size exception; applying any protected label to a concrete PR requires fresh exact authorization.
- TDD: `strict_tdd: true` on current-main `openspec/config.yaml` (also active in the original feature); runner `go test ./...`. Reused pre-existing RED/GREEN evidence applies only to original candidate. New conflict fixes require observed RED/GREEN where feasible; do not claim old checks verify new bytes.
- Route: delegated direct for multi-file conflict resolution and broad mapping; parent owns Git worktree state, task tracking and remote operations. The ~400 authored-line work-unit heuristic is advisory. A large coherent removal must not be artificially split or minified.
- Recovery locator: this document and Engram topic `odd/remove-sdd-odd-only-main/tasks` in project `gentle-ai`. Mirror the full current document and its repository-relative path after every task transition.

## Tasks
- [ ] **GO-1 — reapply the coherent retirement work unit.** Bring the original retirement commit onto the new branch while preserving current-main changes and excluding unrelated branch history. Acceptance: no unresolved conflicts, no SDD active routes, original branch intact; record exact commit and any integration exceptions. Route: delegated for nontrivial conflict resolution after mechanical Git application; 23 paths overlap between old feature change and current-main tree.
- [ ] **GO-2 — verify current-main integration.** Run check-only format, focused Go tests for conflicts and ownership, full offline Go suite, vet, bench declarations and offline organic-runtime checks; audit packaged assets, current-tree historical removals, generated ledgers and residual references. Record failures/skips precisely and avoid network or unauthorized live-agent tests. Route: delegated fresh verifier per bounded action.
- [ ] **GO-3 — review and delivery.** Assess exact committed work-unit candidate if RDD is enabled; report unavailable authority honestly. Prepare a scoped PR linked to approved #4959 and apply exactly one `type:*` label and protected size exception only under exact target-host authorizations. Merge only after required checks pass. Route: parent for Git/authority/policy, bounded workers for verification.

## Evidence and current state
- Read-only inventory before new source changes: original implementation changed 815 paths vs its parent; current main differs from that parent in 94 paths, with 23 overlapping paths. There are 792 disjoint feature paths. These counts do not prove a clean application or categorize hunks. Original coherent commit deletes historical `openspec/` artifacts and includes installed-byte agent ownership needed for safe migration; preserve it, not an isolated AI-1–3 subset.
- New isolated worktree created from exact GitHub main `f182ea2018a6399f5d1b6557cf36d71a3df0f723`. No source changes or checks yet. Original old-feature tests and downstream Gentle Shell PR #1408 are historical evidence, not proof for this new candidate.

## Next step
Record this tracker, then attempt one bounded application of the original coherent commit without rewriting the source branch. Resolve actual conflicts against current main before testing or committing the new behavior; preserve all disjoint current-main files and the original feature worktree.
