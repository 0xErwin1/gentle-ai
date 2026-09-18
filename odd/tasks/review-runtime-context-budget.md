# Review runtime context budget — #4680

## Authority and tasks
User authorized ODD/TDD/RDD/TEAR/KISS, conservative200KiB runtime input policy, local review-unit commits and A/B/C split. Git/output ceilings4MiB unchanged. Scope frozen; no new features or size-only refactors. NO push/PR/merge/auto-merge or paid runtime experiments. Parent owns this log and full mirror odd/review-runtime-context-budget/tasks; .codegraph excluded.
- [x] T1 frozen interpretation/summaries/historical compatibility.
- [x] T1R review boundaries/test refactor, no artificial savings.
- [x] T2 complete prompt/retry200KiB policy plus regression fixes.
- [x] T3 DONE: full-suite verification closed. RDD is NOT a completion condition for this issue; see "RDD blocker, correct attribution".
- [x] T4 DONE: Cortex defect A fixed (legacy authority live revalidation), commit 35cb42ed.
- [x] T5 DONE: Cortex defect B fixed (generated-path evidence parity), commit 700facc2.
- [x] T6 DONE: Cortex second-pass defect C fixed (validator briefing promised content it is not handed), commit aab601c3.
- [x] T7 DONE: Cortex second-pass defect D fixed (START probe measured raw bytes, not the serialized role envelope), commit 73767522.

## Local commits and per-slice evidence
Baseline712ebdc78ebe57005c8f9364e21ed4b2d392ca5b. Feature branch barbatdev/bug-review-start-accepts-a-candidate-whose-4-mib, restored at tip b6b11687 after reviewing A attempt. No tracked local diff; untracked .codegraph/ and odd/ retained.
A6f502172: frozen snapshot representation/compatibility, +314/-15=329. Staged tree exported/tested before commit: transaction targeted25.744s and CLI lens19.712s PASS.
B0cfc423f: truthful generated metadata summaries, +467/-5=472. Explicit B size exception; larger than forecast414–434, no hidden savings. Isolated CLI lens/history/refusal-ratchet PASS16.019s.
C1 6c09bb1f: runtime admission/materialization policy, +290/-11=301. Isolated CLI runtime/lens/history/refusal PASS18.558s, provider0.007s.
C2 b6b11687: complete role prompts/retries, +311/-16=327. Isolated CLI provider/role/refuter/validator/runtime PASS26.255s, provider0.007s. C split naturally into two commits to avoid628-line unit. Total1429 authored changed lines; tests retained. Every staged tree verified in git-archive export, not added worktree. No push/PR/merge.

## Verification history
Observed behavioral RED for generated summaries, historical subject compatibility and oversized START acceptance; then GREEN. Parent disproved claimed preexisting transaction failures: same command baseline PASS5.440s, candidate FAIL5.528s. Missing interpretation carry-over fixed in transaction.pristineReviewing and corrected inspection using persisted state; strict equality unchanged. Parent fixed trio PASS5.468s; broader writer snapshot/invalidation PASS23.664s.
Full reviewtransaction PASS270.105s and reviewerprovider PASS0.078s. CLI full failed two environment-routed tests and timed out600.022s; NOT PASS. Log g4680-final-packages.6cCq1G in canonical temp root. Timeout cause unproven; no disk diagnosis from stack alone.
Inherited OPENCODE_CONFIG_DIR routed installation tests to real shared configuration. Two isolated CLI tests pass under command-local env -u OPENCODE_CONFIG_DIR plus canonical TMPDIR: clean base1.929s, candidate1.991s. All subsequent tests use that isolation. No global config edits. RuntimeBudget and refusal-ratchet parent PASS3.091s. Corrective writer failed final report; don't fabricate missing logs. No provider experiments.

## RDD blocker, correct attribution (NOT part of #4680)
For A, detached HEAD6f502172, gentle_review inspect(untrackedScope=exclude) returned ready, but offered START base-ref=f654764e231fd0e829b4b5568bbe287806513cdc (stale fork base), not A parent712ebdc7.
One START idempotencyKey issue4680-slice-a-6f502172 with input {mode:ordinary,baseRef:712ebdc78ebe57005c8f9364e21ed4b2d392ca5b,committedOnly:true} returned native-operation-failed, diagnostics.code=candidate-target-projection-drift, lineage_created:false, mutation_performed:false, mutation_outcome:none, reset_eligible:false.
Read-only inspect with explicit base input still offered stale f654764e. No START replay, no alternate lineage, raw commands, resets/recovery, receipt or acknowledgement. Returned feature branch tip b6b11687. No active review authority created. Do not substitute offered aggregate stale-base review for per-unit candidate.

CORRECTED ATTRIBUTION (evidence, 2026-09-17): the string candidate-target-projection-drift does not exist anywhere in gentle-ai. It is raised by the Pi client at gentle-pi/extensions/gentle-ai.ts:3595 in assertNativeStartCandidateBinding, which requires exact equality between the frozen immutable CandidateView and target.projection (projection=="workspace", baseTree, initialReviewTree, currentCandidateTree, paths, intendedUntracked). The failure is a pre-authority client-side assertion: lineage_created:false, mutation_performed:false, nothing corrupted. Earlier notes wrongly directed the fix at native committed-range projection. The exact mismatching field is still unproven and requires read-only instrumentation on the Pi side. This is a separate tooling defect and does not block delivery of #4680; RDD is opt-in and user-owned, and delivery follows ordinary repository policy.

## Bounded follow-up verification
Parent read original timeout lines202–205: package deadline10m fired while TestTargetedValidatorCaptureRejectsOutcomeOnlyTerminalFailureBeforeAuthorityMutation was listed at0s. This is not proof of a deadlock or root cause.
Exact corrected command via verifier: env -u OPENCODE_CONFIG_DIR TMPDIR="$(python3 -c 'import os; print(os.path.realpath(os.environ.get("TMPDIR", "/tmp")))')" go test ./internal/cli -run '^TestTargetedValidatorCaptureRejectsOutcomeOnlyTerminalFailureBeforeAuthorityMutation$' -count=1 -timeout=120s. PASS1.117s, exit0. This closes only isolated reproduction; full-suite completion remains unverified.
Verifier initially deviated: used empty env rather than unset, reported checkout/baseline whole-suite run despite explicit prohibition, and unsupported root-cause claims. Those conclusions were rejected. Parent confirmed feature branch tipb6b11687 with no tracked changes afterwards. No more baseline/full-suite runs authorized in this follow-up. RDD not retried or disabled; source unchanged.

## Resumed bounded closure evidence
Parent directly read review_runtime_budget_test.go and ran only `env -u OPENCODE_CONFIG_DIR TMPDIR="$(python3 -c 'import os; print(os.path.realpath(os.environ.get("TMPDIR", "/tmp")))')" go test ./internal/cli -run '^TestNegotiatedStartRuntimeBudget(RefusesOver200KiBCandidateWithoutAuthority|AdmitsCandidateUnder200KiB)$' -count=1 -timeout=120s -v` in this feature worktree: both PASS, package1.262s, exit0. This proves the CLI function-level over-budget no-authority refusal and under-budget admission/materialization controls, not external provider execution or full CLI-suite completion.
Rejected scout claims that lack of an arbitrary end-ref explains RDD drift: original attempt already had HEAD=A and base=A parent. The scout-located Pi-side equality assertion is now confirmed as the actual source (see corrected attribution above); the mismatched field remains unproven. No new flags, harness fixes, START retries, or authority mutations authorized/executed.

## Full-suite closure evidence (2026-09-17)
Command, feature worktree, tip b6b11687: `env -u OPENCODE_CONFIG_DIR TMPDIR="$(python3 -c 'import os; print(os.path.realpath(os.environ.get("TMPDIR", "/tmp")))')" go test ./internal/cli -count=1 -timeout=20m`. Package COMPLETED in 660.126s with exactly one failure, TestEngramPathGuidanceDefault (run_path_guidance_test.go:32): engramPathGuidance(default) missing "go/bin", got "Add /Users/jbarbat/.local/bin to your shell PATH".
The earlier 600.022s "unresolved timeout" was NOT a deadlock: the package legitimately needs ~11 minutes and was being run with -timeout=10m. The deadline was too short.
That single failure is proven pre-existing and environment-dependent: the same test run on untouched main a6ae4bfa fails identically (0.014s, exit 1). It is unrelated to this change.
Package results for the candidate: reviewtransaction PASS 270.105s, reviewerprovider PASS 0.078s, cli complete with only the pre-existing environment failure.

## Cortex tester findings (2026-09-17)
Six Cortex tester skills run through Pi on provider nan against 712ebdc7..b6b11687, read-only, in this worktree.
Launch shape: cd <worktree>, then: env -u OPENCODE_CONFIG_DIR pi --provider nan --model nan/<model> --skill ~/.pi/agent/skills/<tester> --no-session -p "<brief>"
Models: requirements-analyst and test-designer nan/qwen3.6; adversarial-tester and evidence-auditor nan/glm5.3; exploratory-tester nan/deepseek-v4-flash; test-runner nan/mimo-v2.5. All six exit 0.

DEFECT A (raised precisely by 2 of 6; related asymmetry by 5 of 6) - FIXED in 35cb42ed.
snapshotsEqual (store.go:778) compares GeneratedPathInterpretation and every fresh build stamps generated-summary/v1 (snapshot.go:257). verification_convergence.go:86 and transaction.go:1104 carried the stored value across; ValidateLiveSnapshot and rebuildCurrentSnapshotEvidence did not. Parent proved it by execution: a legacy snapshot against an UNCHANGED repository was rejected with identical expected and observed identities. Fix carries the stored interpretation across both comparisons, matching the existing precedent. RED observed on both new tests before the fix, GREEN after.
Verification for 35cb42ed: go vet clean; ./internal/reviewtransaction PASS 248.591s; ./internal/cli 651.758s with only the pre-existing environment failure TestEngramPathGuidanceDefault; ./internal/reviewerprovider PASS 0.077s.

DEFECT B - FIXED in 700facc2. Original statement: reviewProviderMaterializeEvidence (review_provider_roles.go:331+) has no entry.Generated branch, while the lens loop (review_lens_context.go:636-646) summarizes generated paths. A candidate with large generated files passes the lens-only START probe and then dead-ends at refuter/validator materialization with lens_context_budget_exceeded: the same unexecutable-lineage class this issue targets, moved after authority creation. The comment at review_provider_roles.go:323-327 claiming refuter and validator hold the same complete evidence as a lens is now false. Not covered by any test.

DEFECT B design question, resolved read-only before implementing. Commit a01e35c7 (closes #3367) states the invariant: "START now proves the complete reviewer evidence for every selected lens can be assembled before it persists anything ... a candidate that starts is a candidate STATUS can answer." Probing the lens alone was sufficient then because refuter/validator evidence was identical to lens evidence. 0cfc423f added the generated-summary branch to the lens loop only, left reviewProviderMaterializeEvidence untouched and its parity comment stale, and carries a one-line message with no rationale. Decisive: a refuter answers only provider-issued lens findings (review_provider_roles.go:417,424) and lenses are told at review_lens_context.go:685 that "anything you cannot see here is not evidence", so no lens finding can cite generated content and the refuter can never need it. The refuter also materializes state.InitialSnapshot, the very snapshot the lenses saw. Conclusion: equalize rather than add a second START probe, which would entrench the divergence and make START permanently more expensive.
Fix 700facc2 reuses reviewLensContextGeneratedSummaryFor rather than duplicating it, charges the summary against the same budget, and rewrites the parity comment to record what depends on the equality. RED observed (go.sum arrived as a full diff), GREEN after; the test also pins that authored paths keep their complete patch.
Verification for 700facc2: go vet clean; ./internal/cli 648.552s with only the pre-existing environment failure TestEngramPathGuidanceDefault; ./internal/reviewtransaction PASS 252.124s; ./internal/reviewerprovider PASS 0.084s.

COSMETIC (all six) - RuntimeContextBudget (capture_runtimes.go:20-28) has two identical branches, so the RegisteredRuntime check is dead and the 4 MiB Git ceiling is vestigial on every reviewer-context surface. No behavior change proposed.

SEPARATE FLAKE, not caused by this change, OPEN - recurred in 2 of 5 full runs and escalated from three to five store tests. Every failure is "review store lock could not be acquired: not a directory" (ENOTDIR) in tests using Store{Dir: filepath.Join(t.TempDir(), "review-store")}. Deliberately NOT bundled into either fix; it is a third defect with its own cause and needs its own investigation. Original observation: one full ./internal/reviewtransaction run failed three store tests with "review store lock could not be acquired: not a directory" (TestStoreLoadsLegacyBoundedLineageAndCompletesFixWithoutNewBudgetSemantics, TestStoreRejectsFreshLegacyShapedBoundedGenesis, TestStoreLoadChainBindsGenesisHeadAndOrderedIdentity). Parent initially misattributed this to the fix on a single-run A/B, then repeated: the identical tree passed on a rerun, and the fix alone passed. Intermittent, pre-existing, unexplained.

## Cortex second pass (2026-09-18)
Second Cortex pass over the same six tester skills. Both first-pass fixes confirmed correct, 6 of 6: neither moves the defect. Open question 2 answered structurally: GeneratedPathInterpretation is a constant stamped on every rebuild and never derived from content, so carrying it across cannot mask a repository change; zero detection power lost. The RuntimeContextBudget "cosmetic" was re-examined and is NOT a defect: the doc comment states a future per-runtime cap must "specialize the registered case instead of widening it", and capture_runtimes_test.go pins every registered identity and the fail-closed answer. Documented, tested scaffolding; 6 of 6 agree.
Parent resolved a direct contradiction between two glm5.3 runs (evidence-auditor vs adversarial) on legacy successors: the recovery successor is built fresh via builder.Build / BuildReleaseScopeSnapshot / BuildStagedWorkspaceOverlayRecovery (review_facade.go:1702-1709) and all stamp v1, so no legacy authority reaches those comparisons on the CLI path. The evidence-auditor was right. Same model, opposite conclusions: nothing is signed without verification.

DEFECT C - introduced by 700facc2, FIXED in aab601c3. The targeted validator briefing (internal/reviewerprovider/contract.go) asserted the evidence array carries "the complete frozen tree-to-tree patch for every path" and is "not a summary of them, so a verdict reached from it is a verified verdict". After 700facc2 that is false for generated paths: the role receives a metadata summary with content_omitted and would sign a verified verdict over bytes it never saw. The lens instruction was updated in 0cfc423f; this one was not. Raised by exploratory-tester (deepseek); test-runner and test-designer called it safe. Fix distinguishes authored from generated paths, routes a check that turns on omitted content to inspect-candidate, and to a typed unavailable check when no command is available. New contract test pins both the removed promise and the new route; the forbidden strings were present before the fix.

DEFECT D - pre-existing on the branch, 3 of 6 converge, FIXED in 73767522. START's probe assembles the raw lens block; reviewProviderRolePrompt serializes refuter and validator requests with json.Marshal against the same 200 KiB ceiling, and escaping doubles every quote, backslash, newline and tab. A quote-dense candidate passes START and fails at role materialization with authority frozen. Compounding it: once one lens result is persisted, compactPristineReviewing is false, so review/invalidate is unavailable and no non-destructive exit remains. Same class as #4680 through a narrower window. TestReviewCaptureRefuterMaterializeRefusesCompletePromptOverRuntimeBudget (added in b6b11687) already demonstrated the sequence.
Fix adds reviewProviderRoleEnvelopeFloor: the probe charges the real materialized evidence serialized exactly as each role prompt serializes it, inside that role's real instruction and schema. Claims and the frozen policy body do not exist before a lens runs and are deliberately not estimated; guessing them would refuse candidates that fit.
RED observed: with the fix present but the probe wiring removed, TestNegotiatedStartRuntimeBudgetRefusesEscapedOverBudgetCandidate FAILED with START creating authority for the quote-dense candidate. GREEN after restoring the wiring. TestNegotiatedStartRuntimeBudgetAdmitsCandidateUnder200KiB still passes, so the floor does not over-refuse.

## Verification for aab601c3 and 73767522 (2026-09-18)
go vet ./internal/cli ./internal/reviewerprovider clean; gofmt clean.
`env -u OPENCODE_CONFIG_DIR TMPDIR=<realpath> go test ./internal/reviewerprovider ./internal/cli -count=1 -timeout=25m`: cli completed in 689.903s with exactly one failure, the proven pre-existing environment failure TestEngramPathGuidanceDefault (run_path_guidance_test.go:32), which reproduces identically on untouched main a6ae4bfa.
`... go test ./internal/reviewerprovider ./internal/reviewtransaction -count=1 -timeout=15m`: reviewerprovider PASS 0.077s, reviewtransaction PASS 262.141s.
Targeted: `-run '^TestNegotiatedStartRuntimeBudget'` PASS 3.205s, covering both the new escaped-envelope refusal and the unchanged under-budget admission.
The store-lock ENOTDIR flake did not recur in this run.

## Remaining/next
Feature branch tip is now 73767522 (A 6f502172, B 0cfc423f, C1 6c09bb1f, C2 b6b11687, D 35cb42ed, E 700facc2, F af5a4c8f, G aab601c3, H 73767522 over baseline 712ebdc7). All four Cortex defects across both passes are fixed and verified.
Rebased since that line was written, so the letter-keyed hashes above no longer
resolve; the branch tip is now 85cbeed3. Commits added after it:

- 812d152a fix(review): measure the role envelope START admits a candidate under
- bf969778 fix(review): charge the frozen policy in the role envelope floor
- 85cbeed3 test(review): prove recover keeps a non-destructive exit for
  over-budget lineages

Still open, tracked separately: the store-lock ENOTDIR failures above, the
cosmetic RuntimeContextBudget dead branch, and the empty RuntimeAgent on a
recovered lineage (inert today because the budget fails closed, but it means a
recovered lineage's cap is not selected by the frozen identity).

The store-lock ENOTDIR failures are not a flake and not an environment defect:
`store_test.go` hands raw `t.TempDir()` to the root-anchored O_NOFOLLOW lock
walk, which refuses a symlinked ancestor by design, and on Darwin $TMPDIR sits
under /var -> /private/var. Production is unaffected because every store
constructor resolves its directory through filepath.EvalSymlinks first. The
remedy already exists as `canonicalTempDir(t)` in canonical_temp_dir_test.go;
store_test.go has 20 raw call sites that never adopted it. Reproduces
identically on untouched main.
Delivery: the user explicitly authorized the push on 2026-09-18, and 85cbeed3
is pushed to fix/4680-start-runtime-context-budget (PR #4755, still draft). No
merge and no auto-merge were performed; marking the PR ready and merging remain
entirely the user's decision under ordinary repository policy.
## Cortex adversarial QA (2026-09-18, after 795fd972)

Two roles run against the branch tip. CI at the time: 17/17 green.

FALSIFIED — "a recovered over-budget lineage keeps `review invalidate` as its
exit" was written as an invariant and is only true on arrival. Two independent
breaks, both proven by execution:

- `RunReviewCaptureResult` is dispatched straight from runReviewCommand and
  never consults the budget guard, whose only production call site is STATUS
  (review_facade.go:1287). A hand-built `--input` result admits on an
  over-budget candidate; the admitted result makes compactPristineReviewing
  false and invalidate then refuses with "only a pristine reviewing compact
  authority may be invalidated". That is the #4680 dead-end, on a recovered
  lineage.
- `review invalidate` also runs rebuildCurrentSnapshotEvidence
  (compact_store.go:1691), so a pristine zero-result lineage whose worktree
  drifted cannot be invalidated at all. An over-budget candidate is a large
  change its author keeps editing, so drift is the ordinary case.

Fixed in this branch: the doc comment and the test name no longer promise an
invariant, and both now name the two conditions that lose the exit. The test is
TestRecoveredOverBudgetLineageStopsTypedAndArrivesWithItsExitIntact.

FALSIFIED, follow-up (NOT fixed here, separate surface): the START role-envelope
floor probes synthetic minimal shapes, while the real targeted validator
materializes evidence from the CORRECTED snapshot
(review_provider_roles.go:307, `correction` not `snapshot`) and adds
FixFindings/FixClassifications bounded only by ResultLimit 4<<20 against a
200 KiB budget. Executed: the floor shape is admitted at 110408 bytes while the
real shape is refused with lens_context_budget_exceeded. Since validator and
refuter run only after lens results are persisted, that path reaches a
non-pristine, un-invalidatable lineage — the #4680 dead-end relocated to the
correction path rather than closed there. The code deliberately declares the
refuter-Claims omission; the corrected-evidence and FixFindings gap is
undeclared.

SURVIVES: no additional unguarded authority creator beyond recover
(reopen-results reuses the frozen snapshot, lenses and target, so it cannot
introduce a larger candidate; repair/reclaim mint nothing). Recovery
inheritance does not break the pristine predicate. Generation 3 still
invalidates. snapshotsEqual holds right after recover. RuntimeContextBudget
cannot fail open — both branches return the same constant, which also makes its
"fails closed" comment vacuous until a per-runtime cap exists.

Open follow-up, tracked separately from this issue: instrument the Pi-side assertion at gentle-pi/extensions/gentle-ai.ts:3595 read-only to identify the exact drifting projection field. Do not repair the harness from inside this source change and do not disable RDD as a workaround.
Pre-existing environment failure TestEngramPathGuidanceDefault is a separate concern; it reproduces on untouched main.
