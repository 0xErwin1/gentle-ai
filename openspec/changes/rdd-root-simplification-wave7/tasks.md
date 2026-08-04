# Tasks: RDD Root Simplification — Wave 7 (Compatibility Retirement)

Grounded at post-W6 tip `40176a8f`, pending W6's fix cycle `bba17974` (touches `authority_disposition_execute.go`, `review_repair.go`, ds11). Task 0 re-validates every row before any deletion.

## Review Workload Forecast

| Field | Value |
|---|---|
| Estimated changed lines | ~-8000 net (design forecast) across 20 work units, each ≤1000L |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | Feature Branch Chain, 20 sequential WUs on tracker `feature/rdd-root-simplification` |
| Delivery strategy | auto-chain (proposal Session parameters) |
| Chain strategy | feature-branch-chain |
| Effective per-PR budget | 1000L (proposal override of the 400L default) |

Decision needed before apply: No
Chained PRs recommended: Yes
Chain strategy: feature-branch-chain
400-line budget risk: High

### Suggested Work Units (sdd-attempt ledger)

| Unit | Goal | PR base | Focused test | Runtime harness | Rollback boundary |
|---|---|---|---|---|---|
| WU1 S1 | W-9/10/11 closure (+250) | tracker | `go test ./internal/reviewtransaction/... -run 'CaptureLensResult\|AdmitCandidateCausalFindings\|newLineageCapturedFindings' -count=1` | N/A — pure add | revert 1.1-1.9 |
| WU2 S6 | Byte-equiv Commit A (+150) | WU1 | `go test ./internal/cli/... -run TestByteEquivalence -count=1` | `bench --axis all` w/ `GENTLE_AI_RDD_NEW_LINEAGE=1`, record | delete evidence dir |
| WU3 S9a | v1 freeze + backlog proofs (+150) | WU2 | N/A doc-only | N/A | revert freeze marker |
| WU4 S2a | Shadow observer+alias (-430) | WU3 | `go test ./internal/reviewtransaction/... -run Shadow -count=1` | N/A | git revert |
| WU5 S2b | Shadow tests pt1 (-779) | WU4 | same | N/A | git revert |
| WU6 S2c | Shadow tests pt2 + golden retained (-886) | WU5 | same | N/A | git revert |
| WU7 S3a | reconcile-authority dispatch+bench retarget (-250) | WU6 | `go test ./internal/cli/... -run ReviewReconcile -count=1` | `bench --axis ds01,ds02,ds04` | git revert |
| WU8 S3b | `ReconcileInvalidRecoveryEdge` + tests (≤1000, measure) | WU7 | same | `bench --axis ds01,ds02,ds04` | git revert |
| WU9 S4a | reconcile-authority-batch dispatch (-200) | WU8 | `go test ./internal/cli/... -run ReviewReconcileBatch -count=1` | N/A | git revert |
| WU10 S4b | `ReconcileInvalidRecoveryEdges` journal (-592) | WU9 | same | N/A | git revert |
| WU11 S4c | plan+guard, confirm row 12 (-673) | WU10 | same | N/A | git revert |
| WU12 S4d | batch tests pt1 (-670) | WU11 | same | N/A | git revert |
| WU13 S4e | batch tests pt2 (-446) | WU12 | same | N/A | git revert |
| WU14 S5a | quarantine/repair verbs dispatch (-200) | WU13 | `go test ./internal/cli/... -run ReviewLegacy -count=1` | N/A | git revert |
| WU15 S5b | `legacy_fix_scope_quarantine.go` (-606) | WU14 | same | N/A | git revert |
| WU16 S5c | `legacy_quarantine.go`+`legacy_alias_repair.go` (-582) | WU15 | same | N/A | git revert |
| WU17 S5d | legacy tests, 5 files (-875) | WU16 | same | N/A | git revert |
| WU18 S7 | switch + legacy start branch, Commit B (-600) | WU17 | `go test ./internal/cli/... -run NewLineageSwitch -count=1` | `bench --axis all` switch-free, diff vs WU2 | git revert |
| WU19 S8 | D4 verbs classify+delete (≤300) | WU18 | `go test ./internal/cli/... -run ReviewFacadeDispatch -count=1`, `legacy_readonly_guard_test.go` | N/A | git revert |
| WU20 S9b | Capability deltas + close-out (+50) | WU19 | N/A doc-only | N/A | git revert |

## Gate

- [x] G.1 Confirm Waves 3-6 merged to `main` (exit evidence present; `GENTLE_AI_RDD_NEW_LINEAGE` present under `internal/`).
- [x] G.2 Confirm W6 fix cycle `bba17974` merged before deriving the Task 0 inventory.

## Task 0: Inventory Re-Validation (no code diff, blocking)

- [x] 0.1 Re-read all 24 design rows against current tracker tip; confirm file:line accuracy post-`bba17974`.
- [x] 0.2 Confirm `ShadowRelation*` constants (`candidate_relation.go:34-45`) are declared independent of row 4's alias (line 36) before any shadow deletion.
- [x] 0.3 Confirm row 9's ds01/ds02/ds04 verb hits unaffected/updated by `bba17974`'s ds11 journey change.
- [x] 0.4 Record any drift as amended row notes before WU1.

## Bracket Slices (add-only, land first)

### WU1 — S1: W-9/W-10/W-11 closure
- [x] 1.1 RED: `FindingEvidence.Severity` (omitempty) missing-field test (`transaction.go:146`).
- [x] 1.2 GREEN: add `Severity`; carry through `newLineageCapturedFindings` (`review_artifact.go:937-946`, today drops `facadeFinding.Severity`).
- [x] 1.3 RED: `new_lineage_capture_test.go` — `CaptureLensResult` refuses severe finding missing `evidence_class`/`causal_disposition` (v2 message, `artifact_admission.go:331`).
- [x] 1.4 GREEN: refuse in `AuthorityStore.CaptureLensResult` (`new_lineage_capture.go:99`) reusing `isSevereSeverity`/`isSupportedEvidenceClass`/`isSupportedCausalDisposition` (`transaction.go:1774/1829/1838`).
- [x] 1.5 RED: v3 finalize — WARNING-severity candidate-causal findings stay non-blocking (v2 parity).
- [x] 1.6 GREEN: v3 finalize filters `CapturedFindingEvidence()` to SEVERE before `AdmitCandidateCausalFindings` call site; function body stays byte-identical.
- [x] 1.7 RED: `candidate_causal_admission_test.go` — unknown `causal_disposition` on a severe finding escalates via new `unresolvedIDs` return.
- [x] 1.8 GREEN: add third `unresolvedIDs` return to `AdmitCandidateCausalFindings` (`candidate_causal_admission.go:31`); only v3 finalize consumes it; v2 callers byte-identical.
- [x] 1.9 RED (RG.1): add `internal/reviewtransaction/legacy_readonly_guard_test.go` asserting D5 retained-symbol list (parseLegacyBinding, parseBinding, bindingBytes/Digest/Path, `candidate_decline.go` parser, StateInvalidated arms, AuthoritativeStore/LoadChain, NewLegacyReadOnlyError) reachable/read-only. Intentionally RED until WU19.
- [x] 1.10 Exit Checklist.

### WU2 — S6: byte-equivalence evidence, Commit A
- [x] 2.1 Record goldens/envelopes/receipts with `GENTLE_AI_RDD_NEW_LINEAGE=1` across the full journey set, every entry surface: start (negotiated+unnegotiated), status `--next-transition`, capture-result, finalize, validate, all 5 gates. (Scoped at apply time to the unnegotiated form + status + finalize + all 5 gates; negotiated form and capture-result deferred to unit-level coverage + WU18's `bench --axis all` — see apply-progress.)
- [x] 2.2 Store recorded bytes as the Commit-B comparison baseline.
- [x] 2.3 Exit Checklist.

### WU3 — S9a: v1 freeze + backlog deletion proofs
- [x] 3.1 Add read-only freeze marker/doc for `contracts/review-integration/v1/**` (D3), byte-unchanged.
- [x] 3.2 Record deletion proof for backlog `#1455`, `#1462`, `#1570`, PRs `#1549`, `#1550` (superseded-by-design).
- [x] 3.3 Exit Checklist.

## Task 0 Re-Validation Findings (recorded before WU1, at `bba17974`)

All 24 design rows confirmed exact (file:line matches design.md byte-for-byte)
against `bba17974`, with one documented drift and no row requiring a design
amendment. W6's fix cycle (`bba17974`) touched `bench/axis_damaged_store_closure.go`,
`internal/cli/review_repair.go`(+test), `internal/reviewtransaction/authority_disposition_execute.go`(+test) —
none of these are Wave 7 inventory rows; row 9's `bench/axis_damaged_store.go`
(the file the design's row 9 actually names) received ZERO changes from `bba17974`.
Drift: row 9's `axis_damaged_store_closure.go` claim of "2 hits" is now 0 (that
file currently carries no reconcile-authority/-batch references at all) — the
retarget-needed references live exclusively in `axis_damaged_store.go` (confirmed
14 `reconcile` mentions there, unaffected by W6). Task 0.2 confirmed:
`ShadowRelation*` constants (lines 39-45) and `relateCandidates`/`shadowRelationHasNoLiveCounterpart`
(lines 81/228) are declared independent of the `ShadowRelation` alias (line 36) —
deleting only line 36 in WU4 leaves the live v3 governance vocabulary untouched.
Row 12 pre-confirmed early (S4/WU11 precondition): `PlanCompactBatchReconciliation`/
`PrepareCompactBatchReconciliation` have exactly one non-test consumer each,
both inside the S4 cluster itself (`compact_batch_reconcile_guard.go`,
`internal/cli/review_reconcile_batch.go:76`) — zero consumers outside the
journal/dispatch cluster being deleted in WU9-WU11.

## Consumer-First Deletion Slices

### S2 — Shadow observer retirement (rows 1-5)
- [ ] WU4 (-430): retire `ObserveShadowRelation` (`shadow_observer.go`, 201L) + `shadowObservationEnvVar` + `review_facade.go:856,:1566` consumers + `shadowClassifyAuthorityHealth`/`shadowAuthorityHealthAtRepo` (71L) + alias `ShadowRelation` (`candidate_relation.go:36`); gate on Task 0.2.
- [ ] WU5 (-779): delete `shadow_observer_test.go`(185)+`shadow_authority_health_test.go`(257)+`shadow_identity_test.go`(337); deletion proof per file.
- [ ] WU6 (-886): delete `shadow_readonly_guard_test.go`(286)+`shadow_matrix_test.go`(600); retain `shadow-differential-matrix.golden` byte-unchanged, no regenerating code.

### S3 — reconcile-authority (rows 6-9, 19)
- [ ] WU7 (-250): retire `RunReviewReconcileAuthority`+case (`review_facade.go:721-722`)+`review_reconcile.go`(60L); retarget ds01/ds02/ds04 (`bench/axis_damaged_store.go` 14 hits, `axis_damaged_store_closure.go` 2 hits) to shape-only; RED unknown-command refusal test for `reconcile-authority`.
- [ ] WU8 (≤1000, measure reconcile-only subset): delete `ReconcileInvalidRecoveryEdge` (`compact_reconcile.go:233`)+`review_reconcile.go:46`+reconcile-only subset of `compact_reconcile_test.go` (of 1192L total); split WU8/WU8b if subset pushes over budget.

### S4 — reconcile-authority-batch (rows 10-13, 19)
- [ ] WU9 (-200): retire `RunReviewReconcileAuthorityBatch`+cases (`:680-681,:723-724`)+`review_reconcile_batch.go`(110L); RED unknown-command refusal test.
- [ ] WU10 (-592): delete `ReconcileInvalidRecoveryEdges` (`compact_batch_reconcile_journal.go:71`).
- [ ] WU11 (-673): CONFIRM `compact_batch_reconcile_plan.go`(350)+`compact_batch_reconcile_guard.go`(323) have zero consumers outside the journal (row 12) — then delete.
- [ ] WU12 (-670): delete `..._journal_test.go`(340)+`..._plan_test.go`(330); deletion proof per file.
- [ ] WU13 (-446): delete `..._lock_test.go`(183)+`cli/review_reconcile_batch_test.go`(263).

### S5 — quarantine/repair legacy verbs (rows 14-19)
- [ ] WU14 (-200): retire 3 verbs+cases (`RunReviewLegacyQuarantine`, `RunReviewLegacyFixScopeQuarantine`, `RunReviewLegacyAliasRepair`; `review_facade.go:729-734`)+3 CLI handlers (57L each); RED unknown-command refusal test per verb.
- [ ] WU15 (-606): delete `legacy_fix_scope_quarantine.go`.
- [ ] WU16 (-582): delete `legacy_quarantine.go`(289)+`legacy_alias_repair.go`(293).
- [ ] WU17 (-875): delete `legacy_alias_repair_test.go`(311)+`legacy_fix_scope_quarantine_test.go`(387)+`cli/review_legacy_quarantine_test.go`(128)+`cli/review_legacy_alias_repair_test.go`(27)+`cli/review_legacy_fix_scope_quarantine_test.go`(22); deletion proof per file.

## Switch Removal (hard gate: after WU1-WU17)

### WU18 — S7: switch + legacy start branch, Commit B
- [ ] 18.1 Delete `newLineageActivationEnvVar` (`review_core.go:31`)+`NewLineageActivationEnabled` (`review_core.go:39-41`)+call site (`review_facade.go:1625`).
- [ ] 18.2 Delete legacy `review start` branch (`review_facade.go:1628` → end of `runReviewFacadeStart`).
- [ ] 18.3 Delete switch tests/harness: `new_lineage_switch_identity_test.go`, `review_new_lineage_switch{,_off_golden}_test.go`, `..._rollback_safety_test.go`, `..._kill_switch_test.go:81`, `bench/runner.go:86`, `bench/journeys_wave3.go:11`.
- [ ] 18.4 Re-run WU2's journey set switch-free; diff against Commit-A bytes — MUST be byte-identical (defect signal if not, never a golden-update task).
- [ ] 18.5 Confirm `legacy_readonly_guard_test.go` remains RED only for D4-verb scope (S8 still pending).
- [ ] 18.6 Exit Checklist.

## WU19 — S8: D4 verbs (row 24, classify at task time)
- [ ] 19.1 Classify `invalidate`/`abandon`/`recover`/`reclaim`/`dispose-result`/`reopen-results` (`review_facade.go:709-728`) against current tree: zero new-lineage role → delete; residual legacy-READ role → retain per D5, document in the guard.
- [ ] 19.2 Delete confirmed-dead verb cases+handlers+tests (est ≤300L).
- [ ] 19.3 GREEN: `legacy_readonly_guard_test.go` (RG.1/RG.2) fully GREEN — zero legacy mutation entry point reachable.
- [ ] 19.4 Exit Checklist.

## WU20 — S9b: capability deltas + wave close-out
- [ ] 20.1 Finalize `rdd-legacy-retirement`/`rdd-single-lifecycle`/`rdd-shadow-evaluation` deltas against actual landed state.
- [ ] 20.2 Verify every proposal success-criteria box checked.
- [ ] 20.3 Exit Checklist.

## Exit Checklist (every WU)
- [ ] `go test ./... -count=1` root module green.
- [ ] `go test ./... -count=1` bench module green.
- [ ] `bench --axis all` corpus vs fresh binary — byte-identical.
- [ ] Deadcode ratchet net-negative (WU1/2/3/20 exempt, add-only).
- [ ] Refusal ratchet shrinks or holds (`.refusal-ratchet-baseline.txt` rows 181-186, 222-227, 664-717, 955-1009).
- [ ] `gofmt -l .` empty; `go vet ./...` clean.

