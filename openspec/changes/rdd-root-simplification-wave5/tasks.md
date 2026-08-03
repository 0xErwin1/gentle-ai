# Tasks: RDD Root Simplification — Wave 5 (Gate Cutover)

## Gate

HARD-GATED: Wave 5 chains after BOTH Wave 3 AND Wave 4 land on the tracker
branch (`feature/rdd-root-simplification`). `resolveGoverningAuthority`,
`CandidateIdentity` promotion, `ReceiptRef`, and capability admission are
Wave 3/4 deliverables absent at `d591f4cf`; no Wave 5 slice may start before
both merge. Verify both waves are on the tracker (sdd-attempt ledger or
`git log feature/rdd-root-simplification` for wave3/wave4 slice merges)
before opening Wave 5 PR0.

## Review Workload Forecast

| Field | Value |
|---|---|
| Estimated changed lines | S1 ~650, S2 ~350, S3 ~700, S4 ~900, S5 ~800, S6 ~500, S7 ~600 (total ~4500) |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR0 → S1 → S2 → S3 → S4 → S5 → S6 → S7 |
| Delivery strategy | auto-chain |
| Chain strategy | feature-branch-chain |
| Per-slice PR budget (session override) | ≤1000 authored lines/slice |

Decision needed before apply: No
Chained PRs recommended: Yes
Chain strategy: feature-branch-chain
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | PR | Focused test | Harness | Rollback |
|---|---|---|---|---|---|
| PR0 | Land W5 SDD artifacts; confirm W3+W4 tracker gate | tracker base | N/A (docs) | N/A — SDD artifacts only | Revert `openspec/changes/rdd-root-simplification-wave5/**` |
| S1 | Characterization corpus (legacy funnel, invalidation verb, decline, pre-PR delta rows) + 35-cell matrix harness | PR0-base | `go test ./internal/reviewtransaction/... -run Characterization` | N/A — golden corpus, no runtime scenario yet | Revert characterization test files + harness generator |
| S2 | Kill switch consulted once before any authority read + per-gate disabled/double-eval goldens | S1-base | `go test ./internal/cli/... -run Disabled` | 5-gate disabled-fixture double-eval bench | Revert single-call ordering; restore two late reads |
| S3 | `NativeGateEvaluation` additive `Relation`/`Next`; `gateVerdict` totality; every denial names a next step | S2-base | `go test ./internal/reviewtransaction/... -run GateVerdict` | 5-gate deny-fixture bench | Revert additive fields + `gateVerdict`; composite literals stay keyed |
| S4 | `projectLegacyAuthority`; legacy evaluated through algebra; receipt precedence; byte-identity | S3-base | `go test ./internal/reviewtransaction/... -run ProjectLegacyAuthority` | 5-gate byte-hash-before/after bench | Revert `legacy_projection.go`; `resolveGoverningAuthority` legacy cell reverts to byte-identical branch |
| S5 | Pre-PR chain composition deletion; pinned explained divergences | S4-base | `go test ./internal/reviewtransaction/... ./internal/cli/... -run PrePRComposition` | black-box denial-names-next-step bench journey | Revert `compact_chain.go` deletion from git history |
| S6 | Decline downgrade to ordinary unmanaged; read-only parser retained | S5-base | `go test ./internal/reviewtransaction/... -run CandidateDecline` | declined-candidate bench journey | Revert `candidate_decline.go` resolver/writer deletion |
| S7 | Invalidation verb deletion, `StateInvalidated` parse-only (LANDS LAST — only destructive step) | S6-base | `go test ./internal/reviewtransaction/... -run Invalidation` | full 35-cell matrix golden re-run | Restore `compact_approved_invalidation.go` from git history |

## Gate Regression Test Index (#2222/#2239 supersession evidence)

One named test per gate × {disabled, deny, allow} branch (15 tests, S2–S4),
plus switch-off double-eval byte-equivalence (5 tests, S2) and pre-PR
composition-specific corroboration (S5):

- Disabled (S2, #2222): `TestPostApplyGate_Disabled_ReportsUnmanagedBeforeAuthorityRead`, `TestPreCommitGate_Disabled_ReportsUnmanagedBeforeAuthorityRead`, `TestPrePushGate_Disabled_ReportsUnmanagedBeforeAuthorityRead`, `TestPrePRGate_Disabled_ReportsUnmanagedBeforeAuthorityRead`, `TestReleaseGate_Disabled_ReportsUnmanagedBeforeAuthorityRead`
- Double-eval byte-equivalence (S2): `TestDisabledGateOutput_DoubleEval_ByteEquivalent_PostApply`, `TestDisabledGateOutput_DoubleEval_ByteEquivalent_PreCommit`, `TestDisabledGateOutput_DoubleEval_ByteEquivalent_PrePush`, `TestDisabledGateOutput_DoubleEval_ByteEquivalent_PrePR`, `TestDisabledGateOutput_DoubleEval_ByteEquivalent_Release`
- Deny (S3): `TestPostApplyGate_Deny_ChangedRelationCarriesNextStep`, `TestPreCommitGate_Deny_ChangedRelationCarriesNextStep`, `TestPrePushGate_Deny_ChangedRelationCarriesNextStep`, `TestPrePRGate_Deny_BaseMismatchDeniesWithoutComposition`, `TestReleaseGate_Deny_ChangedRelationCarriesNextStep`
- Allow (S4): `TestPostApplyGate_Allow_ExactReceiptGovernsDelivery`, `TestPreCommitGate_Allow_ExactReceiptGovernsDelivery`, `TestPrePushGate_Allow_ExactReceiptGovernsDelivery`, `TestPrePRGate_Allow_ExactReceiptGovernsDelivery`, `TestReleaseGate_Allow_ExactReceiptGovernsDelivery`
- #2239 (S5): `TestPrePRGate_KillSwitchBeforeComposition_TriviallyPreserved`

## Absorbed from Wave 3/4 Verification (PR0)

Five debts were formally deferred by Wave 3's and Wave 4's verify-reports and
are absorbed here rather than dropped. PR0 itself stays docs-only (per the
work-unit table below); this section documents each debt's closure criterion
and names the slice/phase whose scope it belongs to, with new checklist items
added at that phase so the debt is not silently lost.

1. **N1 (Wave 3 verify) — new-lineage `finalize` self-approval.**
   `ReviewCore.finalize` (`internal/reviewtransaction/review_core.go:131`)
   issues an `approved`/`escalated` `ReceiptRef` purely from
   `authority.State` / `request.AdvanceRequest.{Failed,AdmittedFindingIDs}` —
   it never inspects `LensResults`, so a frozen tier can reach `approved`
   with zero captured lens results at any tier (self-approval). Closure:
   `finalize` must require the frozen tier's lens results before returning
   `CoreTransitionApprove` — tier-0/low MAY legitimately require none, per
   the tier semantics already encoded by `CorrectionBudget`/tier freeze in
   `start`. Absorbed into **Phase 5 (S4)** as task 5.10 below (S4 is where
   receipt precedence/projection work already touches `ReviewCore` output).

2. **N2 (Wave 3 verify) — `newLineageGateEvaluation` has no per-gate
   preconditions.** `newLineageGateEvaluation`
   (`internal/cli/review_governing_authority.go:240-261`) maps
   `CoreTransitionContinue → GateAllow` identically for all five gates, with
   no release-evidence, `BaseRelationshipValid`, or `Generation`
   precondition for pre-pr/release. This is the exact gap `gateVerdict`
   (Phase 4/S3) is designed to close. Reference implementation: the legacy
   `validateDerivedGate` (`internal/reviewtransaction/receipt.go:279-321`)
   already differentiates by gate — `BaseRelationshipValid` gated to
   `GatePrePR`/`GateRelease` only (line 304), `Release` evidence gated to
   `GateRelease` only (lines 307-314). Closure: `gateVerdict(gate, relation)`
   must reproduce this per-gate precondition shape, not a uniform
   continue→allow. Absorbed into **Phase 4 (S3)** as task 4.7 below.

3. **N3 (Wave 3 suggestion, one-liner) — gate receipt cross-check omits
   `CandidateIdentity`.** The `approved`-state receipt cross-check at
   `internal/cli/review_governing_authority.go:104-105` compares only
   `LineageID`, `AuthorityRevision`, and `TerminalState` — it never compares
   `CandidateIdentity` (`BaseTree`/`CandidateTree`/`PolicyHash`), even though
   receipt issuance is expected to bind identity at write time. Closure: add
   the `CandidateIdentity` comparison to that cross-check. Absorbed into
   **Phase 5 (S4)** as task 5.11 below (same file/area as byte-identity
   proof work).

4. **7.4 archive-gating livelock (Wave 4 deferral, SDD-status domain — NOT
   part of the S1-S7 gate-cutover chain).** Wave 4's verify-report CRITICAL-A
   (cycle 3) proved `blockArchiveForUnsatisfiedReVerify` livelocks:
   `applyTargetedReVerifyRouting` re-stamps `status.ReVerify.EvidenceRevision`
   with the *current* verify-report revision on every `Resolve()`, so a
   compliant re-verify only relabels the demand instead of satisfying it
   (cycle-1 demands `sha256:R1`; after a compliant remediation, cycle-2
   demands `sha256:R2`). The named continuation was also unrunnable as
   printed: `gentle-ai sdd-attempt finish --remediates-evidence-revision
   <rev>` alone fails `internal/cli/sdd_attempt.go`'s flag validation — the
   `finish` operation requires the eight base flags
   (`validateSDDAttemptOperationFlags`/`missingSDDAttemptFlags`, `finish`
   case) AND, if any of `--expected-binding-revision`,
   `--successor-lineage`, `--remediates-evidence-revision` is given, all
   three must be given together (`sdd_attempt.go:94-96`). Closure: replace
   the live-revision-chasing demand with a **frozen** anchor — the
   correction's own `FixDeltaHash` (never a live re-derivable value, per the
   W4 livelock finding's own diagnosis of what not to do) — and name the
   full, runnable `sdd-attempt finish` invocation (all 8 base flags + the 3
   remediation flags together) in the blocked reason text. This is the
   SDD-status/CLI domain (`internal/sddstatus`, `internal/cli/sdd_attempt.go`),
   distinct from the five delivery gates' domain
   (`internal/reviewtransaction/{gate,compact_gate}.go`) that S1-S7 rewrite —
   do not conflate them. Absorbed into a new **Phase 9** below, sequenced
   independently of S1-S7 (no shared files), landing after S7 so the gate
   cutover's own destructive step is not entangled with this fix.

5. **8.5 (Wave 4 deferral) — OpenCode plugin relaunch-bound-loss
   replacement.** DECISION (this apply batch): re-deferred to **Wave 7**,
   explicitly, with rationale — the OpenCode plugin surface is adapter
   territory (`Out of scope: adapter changes (W4)` in
   `proposal.md`'s Scope section), and Wave 5's own File Changes list
   (`design.md`) touches no adapter/plugin file; the five gates' cutover
   (`compact_gate.go`, `gate.go`, `review_facade.go`,
   `compact_approved_invalidation.go`, `compact_chain.go`,
   `candidate_decline.go`, `transaction.go`, `legacy_projection.go`) has zero
   overlap with the OpenCode plugin's relaunch-bound-loss surface. Forcing it
   into W5 would mix gate-cutover evidence with unrelated adapter evidence in
   the same PR chain, which design.md decision 8's own rationale rejects
   ("every removal slice depends on evidence a prior slice produced"). Not
   dropped — tracked for Wave 7 planning.

## Phase 1 (PR0): SDD Artifacts

- [x] 1.1 Land `openspec/changes/rdd-root-simplification-wave5/{proposal,specs,design,tasks}.md` (already written).
- [x] 1.2 Confirm Gate: verify Wave 3 AND Wave 4 have landed on `feature/rdd-root-simplification` before opening any Wave 5 slice PR. **Confirmed with a documented exception**: Wave 3 is fully merged on `origin/feature/rdd-root-simplification` (tip `f188be85`, PRs #2309-#2314). Wave 4 has NOT yet merged onto that tracker branch at apply time — its 12-PR chain is queued/merging (per orchestrator's rebase contract). This worktree's base branch `feat/rdd-wave5-base` @ `7598eda4` sits directly on the verified Wave 4 chain tip (`feat/rdd-wave4-s7b-plugin-investigation-and-asset-prose`, confirmed identical SHA, ancestor check passed), which the orchestrator states already passed its own envelope (16/16, 31/31). Wave 5 slices therefore build on Wave 4's verified content even though the tracker-merge event itself is still in flight; the rebase contract requires re-checking the Wave 4 chain tip before each slice's final full-test run and rebasing if it moved.
- [x] 1.3 Fix stale SHA token (Wave-4 verify-report W-e): `openspec/changes/rdd-root-simplification-wave4/specs/rdd-transport-capability/spec.md` cited pre-rebase SHA `ead610f6`; corrected to the patch-id-equal delivered commit `acb3c7c1`.
- [ ] 1.4 Archive Wave 4 (`openspec/changes/rdd-root-simplification-wave4/**` → `openspec/specs/`) when its turn comes, mirroring prior wave pattern (deferred — Wave 4 has not yet landed on the tracker at apply time; do not archive prematurely).

## Phase 2 (S1): Characterization Corpus + Gate-Boundary Matrix Harness (zero behavior change)

- [ ] 2.1 RED: `TestLegacyFunnelCharacterization_RunFacadeLegacyValidateNegotiated` — Wave-1 golden covering-array pattern (`-update`) pinning `runFacadeLegacyValidateNegotiated`'s observable contract (currently zero test references).
- [ ] 2.2 RED: `TestInvalidationVerbCharacterization_InvalidateApprovedCompactAuthority` — pins `review_facade.go:1371`'s writer-lock + rewrite + `os.Remove` behavior before deletion.
- [ ] 2.3 RED: `TestPrePRChainCompositionRemovalDelta` — DELTA rows layered onto existing `compact_chain_test.go`'s 25 test funcs, isolating exactly the rows S5 will delete.
- [ ] 2.4 RED: `TestCandidateDeclineCharacterization_ResolveCandidateDeclineForGate` — characterizes `ResolveCandidateDeclineForGate` + `RecordCandidateDecline` writer before S6 removal (spec requires characterization to precede `candidate_decline.go` removal, not just `compact_chain.go`).
- [ ] 2.5 GREEN: 2.1–2.4 pass against current (pre-cutover) code — zero behavior change.
- [ ] 2.6 Build `testdata/gate-boundary-matrix.golden` generator: 5 gates × 7 relations = 35 rows `{gate, relation, verdict, next_step, explained, reason}`, generated from the algebra (harness only — not yet wired to production gate output; wiring lands incrementally in S2–S7, full run in S6/S7).
- [ ] 2.7 Full verification: `go test ./... -count=1`; bench module; `scripts/deadcode-ratchet.sh --update`; refusal-resolution notes (none pending this slice).

## Phase 3 (S2): Kill Switch Consulted Once + Per-Gate Disabled Goldens

- [ ] 3.1 RED: `TestKillSwitchOrdering_SingleCallBeforeAuthorityRead` — one `reviewDeliveryDisposition(ctx, root, false)` call immediately after flag/contract resolution, before `discoverCompactFacadeGateReview` or any authority read; fails against current two-late-reads shape (`review_facade.go:2905`, `:2967`).
- [ ] 3.2 RED per-gate disabled branch (5 named tests, #2222 evidence — see index above): kill switch off + ambiguous/corrupted authority-store fixture ⇒ ordinary unmanaged delivery, `reason_code: reviews_disabled`, no discovery kind, underlying authority error never surfaces.
- [ ] 3.3 RED switch-off byte-equivalence via same-fixture double-eval (5 named tests, see index above): evaluate the same fixture twice while switch is OFF, assert byte-identical serialized `NativeGateEvaluation` output across both evaluations (idempotence proof, zero mutation on repeat).
- [ ] 3.4 Implement single-call kill-switch ordering; remove the two late reads (`review_facade.go:2905`, `:2967`); wire `emitDisabledUnmanagedDelivery`.
- [ ] 3.5 GREEN: 3.1–3.3 pass (11 named tests this slice).
- [ ] 3.6 Full verification: `go test ./... -count=1`; bench module; `scripts/deadcode-ratchet.sh --update` for removed late-read call sites; refusal-resolution notes (none pending).

## Phase 4 (S3): NativeGateEvaluation Additive Relation/Next + Executable Next Step Per Denial

- [ ] 4.1 RED: `TestGateVerdict_TotalFunction_35Cells` — table-driven totality test over `gateVerdict(gate, relation)`; every one of the 5×7=35 pairings resolves, no unhandled case.
- [ ] 4.2 RED per-gate deny branch (5 named tests — see index above): `changed` relation ⇒ denial carries a typed transition; `unknown` relation ⇒ stop + reason_code (never a bare denial).
- [ ] 4.3 Add `Relation CandidateRelation` and `Next *GateNextStep{Transition, ReasonCode}` fields to `NativeGateEvaluation` (`gate.go:109`); verify all 47 non-test + test composite literals stay keyed (additive-only, compile-clean).
- [ ] 4.4 Implement `gateVerdict(gate GateKind, relation CandidateRelation) (GateResult, GateNextStep)` total function.
- [ ] 4.5 GREEN: 4.1, 4.2 pass (6 named tests this slice).
- [ ] 4.6 Full verification: `go test ./... -count=1`; bench module; `scripts/deadcode-ratchet.sh --update` for `gateVerdict`/`GateNextStep` exports; refusal-resolution notes (none pending).
- [ ] 4.7 ABSORBED N2 (W3 verify, see "Absorbed from Wave 3/4 Verification" above): RED `TestGateVerdict_PerGatePreconditions_MatchLegacyValidateDerivedGate` — table-driven, asserts `gateVerdict` denies `GatePrePR`/`GateRelease` when the boundary descriptor's `BaseRelationshipValid` is false and denies `GateRelease` when release evidence is absent/mismatched, mirroring `validateDerivedGate` (`receipt.go:279-321`); GREEN by having `gateVerdict` consult these preconditions per gate instead of a uniform `continue→allow`.

## Phase 5 (S4): projectLegacyAuthority + Legacy Evaluated Through Algebra + Byte-Identity

- [ ] 5.1 RED: `TestProjectLegacyAuthority_Purity` — `projectLegacyAuthority(chain, artifacts)` is a pure read-only function of on-disk bytes; asserts zero writes/locks.
- [ ] 5.2 RED per-gate allow branch (5 named tests — see index above): covers both v3-present and legacy-only-present-via-projection cases reaching the same `exact`-relation allow.
- [ ] 5.3 RED: `TestLegacyAuthorityAlone_DeniesNewLineageCandidate` — unconditional receipt precedence; legacy-only authority never authorizes a new-lineage candidate.
- [ ] 5.4 RED: `TestLegacyReceiptBytes_ByteIdenticalAcrossAllFiveGates` — hash `review-state.json` + `review-receipt.json` before/after a full validate at each of the 5 gates; zero diff.
- [ ] 5.5 RED (regression, Migration item 4): `TestInFlightCorrection_PreCutoverFinalizes_ReceiptValidatesViaNewPath` — correction opened pre-cutover finalizes under the prior lifecycle; its receipt then validates through the new read-only path.
- [ ] 5.6 Create `internal/reviewtransaction/legacy_projection.go`: `projectLegacyAuthority(chain ValidatedChain, artifacts facadeArtifacts) (CandidateIdentity, ReceiptRef, error)`; delete `runFacadeLegacyValidateNegotiated` re-entry from the funnel.
- [ ] 5.7 Wire `resolveGoverningAuthority`'s "new absent, legacy present" cell to `projectLegacyAuthority` + `relateCandidates` (replacing the byte-identical legacy path).
- [ ] 5.8 GREEN: 5.1–5.5 pass (8 named tests this slice).
- [ ] 5.9 Full verification: `go test ./... -count=1`; bench module; `scripts/deadcode-ratchet.sh --update` for `legacy_projection.go` exports (remove `runFacadeLegacyValidateNegotiated` baseline entry if now unreachable); refusal-resolution notes (none pending).
- [ ] 5.10 ABSORBED N1 (W3 verify, see "Absorbed from Wave 3/4 Verification" above): RED `TestReviewCoreFinalize_RequiresFrozenTierLensResults` — asserts `ReviewCore.finalize` refuses `CoreTransitionApprove` when the frozen tier requires lens results and none were captured (tier-medium/high with empty `LensResults`), and still allows tier-low with zero lens results per tier semantics; GREEN by adding the check to `finalize` (`review_core.go:131`).
- [ ] 5.11 ABSORBED N3 (W3 suggestion, see "Absorbed from Wave 3/4 Verification" above): RED `TestApprovedReceiptCrossCheck_IncludesCandidateIdentity` — asserts the `approved`-state receipt cross-check at `review_governing_authority.go:104-105` denies (`GateInvalidated`) when `CandidateIdentity` (`BaseTree`/`CandidateTree`/`PolicyHash`) mismatches even though `LineageID`/`AuthorityRevision`/`TerminalState` match; GREEN by adding the comparison (one-liner per the original suggestion).

## Phase 6 (S5): Pre-PR Chain Composition Deletion

- [ ] 6.1 RED: `TestPrePRComposition_ZeroCallers` — AST/call-graph guard: no gate calls `EvaluateCompactPrePRChain` to authorize delivery.
- [ ] 6.2 RED: `TestPrePRGate_KillSwitchBeforeComposition_TriviallyPreserved` — #2239 corroborating test: composition function no longer exists in the call graph, proven by call-absence.
- [ ] 6.3 RED: `TestPrePRDivergence_CompatibleBaseAdvanceExplained` and `TestPrePRDivergence_ChangedExplained` — pre-PR's `compatible_base_advance` and `changed` cells pinned as named, explained divergences (`compact_gate.go:91-102` boundary-proof reason), never silent differences.
- [ ] 6.4 Delete `compact_chain.go` (`EvaluateCompactPrePRChain`, `compactPrePRChainProof`, helpers); delete the DELTA-marked rows from 2.3's characterization corpus whose behavior is intentionally gone, keep surviving rows.
- [ ] 6.5 GREEN: 6.1–6.3 pass (4 named tests this slice; 4.2's `TestPrePRGate_Deny_BaseMismatchDeniesWithoutComposition` now exercises the real deleted-composition path).
- [ ] 6.6 Run the full 35-cell gate-boundary-matrix golden (2.6's harness, now wired through S2–S5): zero unexplained divergences, two explained divergence cells for pre-PR.
- [ ] 6.7 Bench journey: black-box "denial names a runnable next step" journey per `rdd-defect-workflow` (`bench/journeys_wave5.go`).
- [ ] 6.8 Full verification: `go test ./... -count=1`; bench module; `scripts/deadcode-ratchet.sh --update` for deleted `compact_chain.go` symbols; refusal-resolution notes (none pending).

## Phase 7 (S6): Decline Downgrade to Ordinary Unmanaged

- [ ] 7.1 RED: `TestCandidateDecline_ZeroCallers` — AST/call-graph guard: no code path constructs delivery authorization from a decline record.
- [ ] 7.2 RED: `TestCandidateDecline_UnmanagedDelivery_ByteIdenticalToDisabled` — declined candidate reaches ordinary unmanaged delivery, output byte-identical to the kill-switch-off golden (3.3's fixtures), no receipt-like record created or read as authority.
- [ ] 7.3 Delete `ResolveCandidateDeclineForGate`, funnel branch (`review_facade.go:2941-2945`), `emitCandidateDeclinedUnmanagedDelivery`, `RecordCandidateDecline` writer (only non-test caller: `review_facade.go:1606`); keep `parseCandidateDeclineAuthorization` read-only.
- [ ] 7.4 GREEN: 7.1, 7.2 pass (2 named tests this slice).
- [ ] 7.5 Bench journey: declined candidate reaches ordinary unmanaged delivery (`bench/journeys_wave5.go`, extends 6.7).
- [ ] 7.6 Full verification: `go test ./... -count=1`; bench module; `scripts/deadcode-ratchet.sh --update` for removed decline-resolver/writer symbols (parser stays baselined read-only); refusal-resolution notes (none pending).

## Phase 8 (S7): Invalidation Verb Deletion (lands LAST — only destructive authority step)

- [ ] 8.1 RED: `TestReceiptFilePersistsAfterDerivedInvalidation_AllFiveGates` — a receipt that would have been `os.Remove`'d under the pre-cutover writer stays present on disk post-cutover; the gate denies with a derived mismatch relation instead.
- [ ] 8.2 RED: `TestPreCutoverInvalidatedRecordsStayReadable` — `StateInvalidated`/`InvalidationEvidence` parse without rewrite.
- [ ] 8.3 RED: `TestNoGateWritesAuthority_CallAbsenceGuard` — AST/guard test: no gate code path calls `acquireStoreLock`, `writeAtomic`, or `os.Remove` (proven by call-absence, not a passing green path, per success criterion 1).
- [ ] 8.4 Delete `compact_approved_invalidation.go` (`InvalidateApprovedCompactAuthority`, `CompactApprovedInvalidationRequest`, `invalidateApproved`, `compactInvalidationTarget*`, `compactInvalidationDenialBound`) and the `review invalidate` compact branch; `invalidated` becomes derived: `relation ∈ {changed, unrelated} ⇒ GateInvalidated`. Legacy-v1 `review invalidate` operator branch retains its write (Wave 7 deletes it).
- [ ] 8.5 Update `internal/sddstatus/runtime_ledger_self_remediation_test.go`: drop the invalidation-verb caller (its only test caller).
- [ ] 8.6 GREEN: 8.1–8.3 pass (3 named tests this slice).
- [ ] 8.7 Re-run the full 35-cell gate-boundary-matrix golden (6.6) post-invalidation-derivation: zero unexplained divergences.
- [ ] 8.8 `ReceiptPath()` reader audit (audit-gated ratification): sweep in-repo + bundled Pi assets for readers depending on file-absence as the invalidation signal; migrate findings to `review validate`; add an rc release-notes line about receipt-file persistence under derived invalidation.
- [ ] 8.9 Close #2222/#2239 as superseded: cross-reference the 15 named per-gate tests (S2 disabled + S3 deny + S4 allow) plus S5's `TestPrePRGate_KillSwitchBeforeComposition_TriviallyPreserved` as supersession evidence in the PR description / issue comments.
- [ ] 8.10 Full verification: `go test ./... -count=1`; bench module; `scripts/deadcode-ratchet.sh --update` for deleted `compact_approved_invalidation.go` symbols; refusal-resolution notes (all four ratified assumptions + the audit-gated item confirmed resolved, none pending).

## Phase 9: SDD-Attempt Archive Gate Fix (Absorbed W4 Deferral — SDD-Status Domain, Independent of S1-S7)

Not part of the gate-cutover chain (`internal/reviewtransaction`/`internal/cli/review_facade.go`); this phase
touches `internal/sddstatus` and `internal/cli/sdd_attempt.go` only and shares no files with S1-S7, so it may
land on its own base after S7 without reopening the gate-cutover evidence. See absorbed-debt item 4 above for
the full W4 CRITICAL-A citation.

- [ ] 9.1 RED: `TestBlockArchiveForUnsatisfiedReVerify_FrozenAnchorDoesNotRelabel` — reproduces the W4
      livelock probe (cycle 1 blocked → compliant remediation → cycle 2 must NOT re-demand a new revision);
      fails against the current live-revision-chasing demand.
- [ ] 9.2 RED: `TestBlockArchiveForUnsatisfiedReVerify_NamedContinuationIsRunnable` — asserts the blocked-reason
      text names a complete, literally-runnable `gentle-ai sdd-attempt finish` invocation: all 8 base flags
      (`--expected-revision`, `--request-id`, `--outcome`, `--evidence-revision`, `--diagnosis`,
      `--harness-disposition`, `--cleanup-evidence`, `--process-evidence`) plus the 3 remediation flags
      together (`--expected-binding-revision`, `--successor-lineage`, `--remediates-evidence-revision`), per
      `sdd_attempt.go`'s `missingSDDAttemptFlags`/`validateSDDAttemptOperationFlags` "finish" case; fails
      against the current `--remediates-evidence-revision <rev>`-only text.
- [ ] 9.3 Implement: replace `blockArchiveForUnsatisfiedReVerify`'s demand anchor with the correction's own
      `FixDeltaHash` (frozen at correction open, never re-derived live) instead of
      `status.ReVerify.EvidenceRevision` (re-stamped on every `Resolve()`); update the blocked-reason
      template to print the complete runnable invocation from 9.2.
- [ ] 9.4 GREEN: 9.1-9.2 pass.
- [ ] 9.5 Full verification: `go test ./... -count=1`; bench module; `scripts/deadcode-ratchet.sh --update`;
      refusal-resolution notes (none pending).
