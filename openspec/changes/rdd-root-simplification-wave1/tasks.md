# Tasks: RDD Root Simplification Wave 1 (Shadow Algebra)

## Review Workload Forecast

| Field | Value |
|---|---|
| Estimated changed lines | ~4100 total across PR0-PR6 (see per-slice table) |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR0 (docs) -> PR1 -> PR2 -> PR3 -> PR4 -> PR5 -> PR6 |
| Delivery strategy | auto-chain |
| Chain strategy | feature-branch-chain (tracker `feature/rdd-root-simplification`) |

Decision needed before apply: No
Chained PRs recommended: Yes
Chain strategy: feature-branch-chain
400-line budget risk: High

### Per-Slice Line Estimate vs Budgets

| PR | Content | Est. authored lines | vs 1000 (design cap) | vs 400 (CI gate) |
|---|---|---|---|---|
| PR0 | Land Wave 1 SDD artifacts + commit Wave 0 archive move | ~1300 | over — mostly doc/rename bulk | exceeds -> needs `size:exception` |
| PR1 | Characterization tests (`deriveBaseAdvanceCompatibility`) | ~450 | within | exceeds -> needs `size:exception` |
| PR2 | `CandidateIdentity` resolver + readonly guard | ~550 | within | exceeds -> needs `size:exception` |
| PR3 | Relation algebra (Amendment A/B) | ~600 | within | exceeds -> needs `size:exception` |
| PR4 | Authority graph classifier | ~300 | within | likely within 400 |
| PR5 | Observer + switch + call-site wiring | ~480 | within | exceeds -> needs `size:exception` |
| PR6 | Differential matrix + golden + doc fixes | ~420 (golden excluded) | within | exceeds -> needs `size:exception` |
| **Total** | | **~4100** | | 6 of 7 slices need `size:exception` |

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|---|---|---|---|---|---|
| 0 | Land SDD docs + archive Wave 0 | PR 0 | N/A (docs) | N/A — docs only, no runtime target | Revert docs commit |
| 1 | Characterize `deriveBaseAdvanceCompatibility` | PR 1 | `go test ./internal/reviewtransaction/... -run TestDeriveBaseAdvanceCompatibility -v` | N/A — test-only, no new runtime surface | Revert `prepr_base_advance_characterization_test.go`; zero prod impact |
| 2 | `CandidateIdentity` resolver | PR 2 | `go test ./internal/reviewtransaction/... -run 'TestShadowIdentity|TestShadowReadOnlyGuard' -v` | N/A — not wired to any live path until PR5 | Revert `shadow_identity.go` + tests |
| 3 | Relation algebra | PR 3 | `go test ./internal/reviewtransaction/... -run TestShadowRelation -v` | N/A — not wired until PR5 | Revert `shadow_relation.go` + tests |
| 4 | Authority graph classifier | PR 4 | `go test ./internal/reviewtransaction/... -run TestShadowAuthorityHealth -v` | N/A — not wired until PR5 | Revert `shadow_authority_health.go` + tests |
| 5 | Observer + switch + wiring | PR 5 | `go test ./internal/reviewtransaction/... -run TestShadowObserver -v` | `GENTLE_AI_RDD_SHADOW=1 go test ./internal/reviewtransaction/... -run TestGate -v` (all 5 gates ON/OFF, byte-identical) | Unset `GENTLE_AI_RDD_SHADOW` (behavioral) or revert observer + 4 call sites (structural) |
| 6 | Differential matrix + golden | PR 6 | `go test ./internal/reviewtransaction/... -run TestShadowMatrix -update` then rerun without `-update` | N/A — golden generation only | Revert `shadow_matrix_test.go` + golden + 2 doc edits |

## Phase 0 (PR 0): Land Wave 1 SDD Artifacts + Wave 0 Archive Move

- [x] 0.1 BLOCKED: `diff -r` found `openspec/changes/archive/2026-08-02-rdd-root-simplification-wave0/verify-report.md` (17 lines, Engram-pointer stub) is NOT byte-identical to `openspec/changes/rdd-root-simplification-wave0/verify-report.md` (263 lines, full report), even though the archive dir's own `archive-report.md` claims "verify-report.md | ✓ | 263 lines". All other files (proposal.md, design.md, tasks.md, 4 spec.md files) ARE byte-identical. Tracked source dir independently confirmed byte-identical to the scratchpad backup. Gate does not pass — do not remove the source until this discrepancy is resolved by the orchestrator/maintainer.
- [x] 0.2 DEFERRED (blocked by 0.1): `git add` the archive dir + merged specs not performed this batch.
- [x] 0.3 DEFERRED (blocked by 0.1): `git rm` of the tracked wave0 change dir not performed this batch.
- [x] 0.4 `git add openspec/changes/rdd-root-simplification-wave1/{proposal.md,specs/*,design.md,tasks.md}` — done on branch `docs/rdd-wave1-sdd-artifacts` (off fast-forwarded tracker `feature/rdd-root-simplification`).
- [x] 0.5 (partial) Commit `docs(sdd): land Wave 1 SDD artifacts` — Wave 1 artifacts only; the Wave 0 archive-move half of this task's original scope is deferred to a follow-up commit pending 0.1 resolution.

## Phase 1 (Slice 1 / PR 1): Characterization Tests for `deriveBaseAdvanceCompatibility`

Satisfies `rdd-candidate-relation-algebra` — "Characterization Tests Precede Delegation-Seam Changes" (Wave 0 SUGGESTION-5: 4 callers, 0 covering tests). Test-only, no production code.

- [x] 1.1 RED: `internal/reviewtransaction/prepr_base_advance_characterization_test.go` — table-driven, `t.TempDir()` + real `git`, success case covering all 7 conditions (merge-base preservation, path digest identity, patch identity, path disjointness, conflict-free merge-tree, issuer-bound CI attestation, base/HEAD non-advance revalidation).
- [x] 1.2 RED: one failure sub-test per of the 7 conditions — each returns a distinct error.
- [x] 1.3 GREEN: `go test ./internal/reviewtransaction/... -run TestDeriveBaseAdvanceCompatibility`; prod code (`prepr.go`) does NOT change — this characterizes existing behavior. All 8 sub-tests pass on the first run against unmodified `prepr.go` — no surprising behavior found.
- [x] 1.4 Ratchet: no new unwired functions; confirm `scripts/deadcode-ratchet.sh` reports no diff. `scripts/deadcode-ratchet.sh` exits 0 with "no new unreachable functions" (a pre-existing, unrelated note about 4 baselined entries becoming reachable/gone was already present before this slice and is not addressed here).

## Phase 2 (Slice 2 / PR 2): `CandidateIdentity` Resolver

Satisfies `rdd-candidate-identity`.

- [x] 2.1 RED: `shadow_readonly_guard_test.go` — AST guard: no `shadow_*.go` file references store mutation, `*CompactState` pointer receivers, or write paths. Guard scans production `shadow_*.go` files (excludes `_test.go`) for: (a) `*CompactState` pointer receivers, (b) any reference to the `Store`/`CompactStore` types at all (the shadow algebra never takes a mutable store handle), (c) filesystem write primitives (`os.WriteFile`, `os.Create`, `os.MkdirAll`, etc.). Deliberately allows Git read/`merge-tree --write-tree` calls per the design's threat matrix. Self-tested against 6 synthetic sources (3 clean, 3 violating) before running against real `shadow_identity.go`.
- [x] 2.2 RED: `shadow_identity_test.go` — Canonical Identity Structure (`repository_id`, `base_tree`, `candidate_tree`, `changed_paths_modes_digest`, `policy_hash` all populated).
- [x] 2.3 RED: Selector Normalization — 4 variants (workspace/staged/committed-range/workspace-overlay) converge to the same canonical tuple for equivalent Git state.
- [x] 2.4 RED: Threat — Git repository selection: linked worktree, separate git dir, absolute vs relative `repo` path yield identical `RepositoryID`. Implemented as: resolver's `repository_id` exactly equals `OpenRepositoryIdentityLease(...).Identity().RepositoryRef` for a linked worktree (whose `.git` is itself a separate-git-dir redirect) and for absolute-vs-relative path aliases of the same repo — proving delegation, not independent re-derivation. (Note: existing `TestRepositoryIdentityLeaseSeparatesLinkedWorktrees` already establishes that two *different* linked worktrees intentionally get *different* RepositoryRefs; this task's "yield identical RepositoryID" is about one worktree's resolution matching the lease's own value, not about collapsing distinct worktrees together.)
- [x] 2.5 RED: Read-Only Resolution — resolver leaves repo state/index untouched.
- [x] 2.6 RED: Deterministic Ambiguity/Failure Reporting — unresolvable selector returns the full ambiguity set or a typed failure, never a recency-inferred pick. Ambiguity modeled as: a `staged` selector supplying both `base_ref` and `ledger_ids` (two equally applicable live targets per Decision 4's mapping table) returns both candidates via `*shadowIdentityAmbiguity`; an unresolvable selector (bad revision, or no field populated) returns `*shadowIdentityFailure` carrying whatever `RepositoryID` evidence was already gathered.
- [x] 2.7 GREEN: `internal/reviewtransaction/shadow_identity.go` — `CandidateIdentity` struct + resolver; `policy_hash` from `CompactState`/`Receipt.PolicyHash`, absent -> `unknown`, never fabricated. Resolver (`shadowCandidateIdentity`, unexported — only `CandidateIdentity` is exported this slice per Decision 1) delegates `repository_id` to `OpenRepositoryIdentityLease` and base/candidate trees to `SnapshotBuilder.Build`; `changed_paths_modes_digest` computed via `git diff --raw` + `parseRawDiffModes` (paths+old/new modes), distinct from `Snapshot.PathsDigest` (paths-only) so mode-only drift is measurable. `policy_hash` supplied only via an explicit `shadowPolicyHashSource` callback, defaulting to `"unknown"`.
- [x] 2.8 Ratchet: this slice introduces functions unwired until PR5 — run `scripts/deadcode-ratchet.sh --update`. Baseline updated: 10 new unwired shadow_identity.go entries added, 4 stale entries from PR1's pre-existing note dropped in the same regeneration (236 total entries). `scripts/deadcode-ratchet.sh` exits 0 after update.

## Phase 3 (Slice 3 / PR 3): Relation Algebra

Satisfies `rdd-candidate-relation-algebra`.

- [x] 3.1 RED: `shadow_relation_test.go` — Seven-Value Relation Output, no eighth value. `TestShadowRelationSevenValuesNoEighth` pins all 7 literal string constants (fails if any typo'd or an eighth added); `TestShadowRelateExactAndUnrelatedScenarios` covers the two literal spec.md scenarios ("identical candidate and policy resolve to exact", "no governing lineage resolves to unrelated").
- [x] 3.2 RED: Ordered fail-closed evaluation — ambiguity -> unknown -> exact -> compatible_base_advance -> provable_contraction -> changed -> unrelated. `TestShadowRelateOrderedFailClosedPrecedence` — 6 sub-tests, each constructing an input satisfying BOTH an earlier- and later-priority condition simultaneously, asserting the earlier one wins.
- [x] 3.3 RED: Amendment A delegation — all 7 `deriveBaseAdvanceCompatibility` conditions hold => `compatible_base_advance`; any one fails => no override. Shadow does NOT reimplement the conditions: `shadowDeriveBaseAdvance` (shadow_relation.go) calls `deriveBaseAdvanceCompatibility` (prepr.go:73) directly, constructing its unexported `*resolvedPrePRRefs`/`gateArtifactPreimages` argument types in-package. `TestShadowRelateDelegatesCompatibleBaseAdvanceToDeriveBaseAdvanceCompatibility` (success, real git fixture) and `TestShadowRelateAmendmentANeverOverridesAFailedDelegatedCondition` (condition-1 break, real git fixture) both pass against real `deriveBaseAdvanceCompatibility` calls — precondition already satisfied by PR1's characterization tests (`prepr_base_advance_characterization_test.go`, all 8 sub-tests pass on unmodified `prepr.go`).
- [x] 3.4 RED: Amendment B degradation — `AdmittedPathsKnown=false` degrades a would-be `provable_contraction` to `changed` (never `unknown`, never `provable_contraction`). `TestShadowRelateAmendmentBDegradesContractionOnExcludedFinding` — 3 sub-cases (no excluded finding stays provable_contraction; excluded finding degrades; `AdmittedPathsKnown=false` no-input degradation).
- [x] 3.5 RED: Threat — commit state: staged/workspace/empty-index/unborn-HEAD at `pre-commit` and `post-apply` resolve to `unknown`, never an accidental `exact`. `TestShadowRelateCommitStateThreatNeverAccidentalExact` — 3 real-git sub-tests (unborn HEAD staged, unborn HEAD workspace, empty index at a real HEAD), each deliberately setting Live==Frozen to prove `unknown` wins over what would otherwise be an accidental `exact`. Design elaboration: design.md's own Threat Matrix response narrows this specifically to "unborn HEAD and empty index"; staged/workspace are the two projections under which those occur, not independently forced-unknown states.
- [x] 3.6 RED: Threat — push state: first push/tracking branch/explicit refspec; unresolvable boundary is `unknown`, fail-closed. `TestShadowRelatePushStateThreatUnresolvableBoundaryIsUnknown` — 3 real-git sub-tests via `shadowPushBoundaryUnresolvable` (a thin wrapper reusing `buildPushTarget`/`prePushTargetForRequest`, gate.go, never re-deriving push-boundary resolution): first-push empty-remote-bootstrap -> unresolvable=true -> `unknown`; a genuine resolution error (unadvertised remote branch) -> unresolvable=true; an established tracking boundary -> unresolvable=false (proves the check is not overbroad).
- [x] 3.7 RED: `ambiguous`/`unknown` rows have no live counterpart — fixture asserts "no live decision", never recorded as agreement. `TestShadowRelationHasNoLiveCounterpartOnlyForAmbiguousAndUnknown` — enumerates all 7 constants, asserts `shadowRelationHasNoLiveCounterpart` is true only for `ambiguous`/`unknown` (scoped exactly to spec.md's Requirement; design.md Decision 6's wider 3-way shadow/live collapse including `unrelated` is PR6's differential-matrix concern, not this slice's).
- [x] 3.8 GREEN: `internal/reviewtransaction/shadow_relation.go` — `ShadowRelation` type + `shadowRelationInput` + ordered relate function. Confirmed true RED first (production file moved aside, `go vet` failed with `undefined: ShadowRelation`), then restored; all 21 sub-tests pass on first run against the initial implementation — no fix cycle needed. `go build ./...` and `go vet ./internal/reviewtransaction/...` clean; AST readonly guard (`TestShadowReadOnlyGuardHoldsForProductionFiles`) automatically picked up `shadow_relation.go` and passes with zero additional guard-file changes.
- [x] 3.9 Ratchet: run `scripts/deadcode-ratchet.sh --update` (new unwired functions). 6 new unwired `shadow_relation.go` entries added (baseline: 242 total). `scripts/deadcode-ratchet.sh` exits 0 after update.

## Phase 4 (Slice 4 / PR 4): Authority Graph Classifier

Satisfies `rdd-authority-graph-classification`.

- [ ] 4.1 RED: `shadow_authority_health_test.go` — Three-Value Health Classification: consistent graph -> `healthy`; classified leaf anomaly -> `repairable`.
- [ ] 4.2 RED: No Mutation or Execution — zero side effects (no plan derivation, no quarantine, no lock).
- [ ] 4.3 RED: Fail-Closed on Unknown Shape — unclassifiable graph -> `blocked`, never `healthy`/`repairable`.
- [ ] 4.4 RED: Deterministic, Evidence-Backed Classification — same graph state -> same classification, idempotent re-inspection.
- [ ] 4.5 GREEN: `internal/reviewtransaction/shadow_authority_health.go` — read-only classifier.
- [ ] 4.6 Ratchet: run `scripts/deadcode-ratchet.sh --update`.

## Phase 5 (Slice 5 / PR 5): Observer Seam + Switch + Call-Site Wiring

Satisfies `rdd-shadow-evaluation`.

- [ ] 5.1 RED: `shadow_observer_test.go` — Advisory-Only, Never Blocking: an observation failure never blocks or alters the live gate outcome.
- [ ] 5.2 RED: Disable Switch Is the Rollback Boundary — `GENTLE_AI_RDD_SHADOW` unset/empty => zero shadow code executes; byte-identical to a shadow-off baseline.
- [ ] 5.3 RED: Off by Default in Live Paths — default config issues zero live Git cost from the shadow path.
- [ ] 5.4 RED: divergence line goes to **stderr** only when the switch is ON, never stdout (stdout is reserved for contract JSON).
- [ ] 5.5 RED: Zero Live-Lifecycle Behavior Change — run `GatePostApply`/`PreCommit`/`PrePush`/`PrePR`/`Release` with switch ON and OFF; assert byte-identical live results.
- [ ] 5.6 GREEN: `internal/reviewtransaction/shadow_observer.go` — `ObserveShadowRelation`, env switch, in-memory sink.
- [ ] 5.7 GREEN: one outcome-neutral observer call each in `gate.go`, `compact_gate.go`, `compact_recovery_binding.go`, `internal/cli/review_facade.go`.
- [ ] 5.8 Ratchet: this slice WIRES the previously-unwired PR2/PR3/PR4 functions — run `scripts/deadcode-ratchet.sh --update` to DROP their `.deadcode-baseline.txt` entries.

## Phase 6 (Slice 6 / PR 6): Differential Matrix + Golden + Exit Evidence

Satisfies `rdd-shadow-evaluation` exit bar.

- [ ] 6.1 RED: `shadow_matrix_test.go` — covering-array corpus (~40-60 rows) over 4 selectors x 7 relations, base movement, contraction, ambiguity, unknown.
- [ ] 6.2 RED: row verdict taxonomy — `agreement` | `divergence` | `no-live-decision` | `no-shadow-decision`, kept as 4 distinct classes (`no-shadow-decision` never collapsed into `no-live-decision`).
- [ ] 6.3 RED: **EXPLAINED DIVERGENCE** class — shadow=`unrelated`/`ambiguous`/`unknown` vs live=`unsafe` (and specifically shadow=`unrelated` vs live=`changed-scope`/`unsafe`) is recorded as its own divergence row with a stated reason, excluded from the exit bar, and never absorbed into `no-live-decision`.
- [ ] 6.4 RED: Exit bar — inject one unexplained divergence on `exact`/`compatible_base_advance`/`provable_contraction`; assert the matrix run reports it as blocking Wave 2.
- [ ] 6.5 GREEN: `internal/reviewtransaction/testdata/shadow-differential-matrix.golden` — generate with `-update`, inspect diff, rerun without `-update` (deterministic).
- [ ] 6.6 Docs: `docs/architecture/rdd-root-simplification-design.md` — add Wave 1 exit-evidence pointer only.
- [ ] 6.7 Docs: `openspec/specs/rdd-simplification-design/spec.md` — correct Amendment A paraphrase (trust root belongs inside condition 6; condition 7 is base/HEAD non-advance revalidation) — Wave 0 verify follow-up.
- [ ] 6.8 Ratchet: golden generation adds no unwired functions; verify `scripts/deadcode-ratchet.sh` reports no diff before commit.
