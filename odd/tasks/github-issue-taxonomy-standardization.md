# GitHub issue taxonomy standardization — T2

## Objective and authority

Standardize Gentle AI's reviewed catalog, forms and delegated skill guidance without workflow/runtime changes.
Parent readback: #5198 OPEN with `status:approved` + `type:feature`; egdev6 actor MAINTAIN verified.
#4701 OPEN with no labels; no claim about absence of a PR. Starting tree clean.
Branch: `feat/issue-taxonomy-standardization`; base HEAD `9dfe17d837dcd5c164c904164ae3888ff4f3ff54`.
Human implementation/T1 scope approval relayed by parent: “...aprovamos y la abarcamos...”.
Writer remote reads/writes, commits, staging, PRs, labels, native consent and memory writes remain unauthorized;
parent's separate grants do not extend this writer's scope.

## Allowed edit surfaces

.github/ISSUE_TEMPLATE/bug_report.yml
.github/ISSUE_TEMPLATE/feature_request.yml
CONTRIBUTING.md
internal/assets/skills/issue-creation/SKILL.md
internal/assets/skills/issue-creation/references/delegated-workflow-actions.md
skills/systemic-issue-triage/SKILL.md
internal/assets/skills/systemic-issue-triage/SKILL.md
skills/issue-root-resolution/SKILL.md
skills/gentle-ai-collab-perfect/SKILL.md
skills/branch-pr/SKILL.md
internal/assets/skills/branch-pr/SKILL.md
.github/PULL_REQUEST_TEMPLATE.md
internal/assets/issue_taxonomy_contract_test.go
internal/assets/issue_creation_skill_test.go
internal/assets/skills_issue_creation_sanitization_test.go
odd/tasks/github-issue-taxonomy-standardization.md

## Budget and tasks

Forecast: 212–386 additions + deletions; hard maximum 400 including untracked tests/docs/mirrors.
Stop before exceeding the budget or touching a new surface; no self-selected split or exception.
- [x] Add deterministic authored form/catalog/asset contracts before implementation.
- [x] Canonical six types, curated families, preserve existing labels and abstain/defer conflicts.
- [x] Keep form controls, publication commands, privacy, approval and protected authority intact.
- [x] Bump embedded issue-creation 1.4 → 1.5; mirror triage bytes; minimal consumer delegation.
- [x] Verified implementation done: writer self-verification and ordinary independent verifier PASS.
- [ ] Human delivery decisions and closing intent remain pending.

## Verification evidence

RED: `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test ./internal/assets/... ./internal/components/skills/... ./internal/catalog/...`
Observed intended failures in TestIssueTaxonomyForms/Catalog/ShippedContract; skills/catalog packages passed.
GREEN: same offline command passed all three packages after implementation; no downloads/bypasses used.
Intermediate run failed: restored required collab marker `discovered GitHub labels` and corrected fingerprint
extraction from baseline line 18 to line 17 (first control); final run passed, no old assertions weakened.
Alternate coverage: legacy output rejection, stale catalog rejection, form control fingerprints, triage byte identity,
PR six types/sections and existing exact CLI/protected-authority assertions. These do not prove runtime enforcement.
Runtime harness: N/A — docs/forms/embedded assets only, no runtime boundary changed.
Baseline: unknown beyond the observed pre-implementation run; no broad-suite claim.
Parent relay: verifier muqyf7du-6-cc0l PASS, no blockers, prior 254-line scope/control verification and cached tests.
Parent ASSESS was unassessable (required untracked declaration); plan used writerSelfVerification + independentVerifier,
high-equivalent ordinary verification, not native authority or native approval. No verifier respawn for this follow-up.
Fresh follow-up: `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go test -count=1 ./internal/assets/... ./internal/components/skills/... ./internal/catalog/...`
Observed RED for three missing exact legacy mappings, then GREEN for all three packages; no downloads/bypasses.
Parent relay: final focused verifier muqyf7du-6-cc0l PASS on pre-metadata 265-line snapshot: fresh command above (assets 4.831s, skills 0.097s, catalog 0.004s); exact mappings, base control hashes, triage parity, ODD authority, diff check and empty gofmt verified.
Actual all-file changed-line count: 266 = 249 additions + 17 deletions across exactly 16 allowed paths,
including 115 new test lines and 71 new ODD lines; remaining hard-budget headroom 134.

## Rollback and delivery gates

Rollback boundary: only the 16 paths above as one taxonomy/forms/asset/test/documentation unit; no workflow edits.
Delivery: NO. `Refs #5198` is provisional; human Closes versus Refs intent is not selected.
Prefer T2 first, then #4701 aligns its producer; no merge authority is granted.
If #4701 lands first, refresh main/forms/workflows and STOP for scope/budget reassessment.
Old 870-target migration plan is STALE: refetch and regenerate only in a future authorized task; no migration here.
T3 server-owned runtime version/digest enforcement belongs to the external control center, not these docs.
Engram locator: odd/github-issue-taxonomy-standardization/tasks; mirror pending: topic lookup timed out twice; read-only diagnostics OK, no save attempted.
