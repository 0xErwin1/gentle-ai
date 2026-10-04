# docs-wave-e-guards

Issue: #5234 (tracker #4556). PR 1 branch: `docs/5234-retired-term-guard` from upstream/main 7af7eef8. PR 2 gets its own branch from main; the two PRs are independent, not stacked.
Goal: Go-test guards so retired terms and hardcoded inventories cannot drift back into living docs.

## Specs

- S1 Retired-term guard: "Extend the existing test to walk every living doc: `README.md`, `CONTRIBUTING.md` and `docs/**`. Exclude historical material: `docs/audits/`, `docs/releases/`, `docs/architecture/rdd-*`, and pages flagged as historical." Existing guard: `internal/app/documented_invocation_test.go:409-414` (`/sdd-`, `/gentle-sdd-`, `gentle-ai sdd-`, `SDD phases`, `SDD agents`, `OpenSpec`).
- S2 "Allow only explicit retirement or legacy notes, through a small, reviewed allowlist. Examples are the "retired in v4.0.0" notes and the legacy `sdd-*` telemetry schema values (`docs/telemetry.md`)."
- S3 "Add `--strict-tdd` to the retired list." Do not ban "Strict TDD" (current, `agentguidance/strict_tdd.go`) or "shadow" (ambiguous).
- S4 "Clean the leftovers listed above so the guard starts green." — `docs/engram.md:159`, `docs/testing-agents-deterministically.md:70,71,251`.
- S5 "Reword the template checkbox: "If behavior changed, docs in `docs/` are updated in the same PR (reference docs track `main`).""
- S6 "Add a test that fails when `docs/agents.md` and `allAgents` disagree. The failure lists missing and extra agents."
- S7 "Add a test that keeps the `README.md` agents badge equal to `len(allAgents)`."
- S8 "Add a test that fails when the component trees in `docs/architecture.md` and `docs/codebase/repository-map.md` miss or invent a package under `internal/components/`."
- S9 "Fix any drift these tests find."
- Decision: drift tests, not generated blocks (no `go generate`, no Markdown markers). Each PR under 400 lines.

## Tasks

- [x] T1 S1-S4 Retired-term guard (RED test, then clean leftovers) — route: delegated writer (mutr97me-2-8bk7) — commit: 9c8c0368
- [x] T2 S5 PR template checkbox — route: inline — commit: 40de7ebc
- [ ] T3 S1-S5 Verify PR 1, RDD review, PR "Refs #5234" — route: verifier + parent — commit: n/a
- [ ] T4 S6-S9 Inventory drift tests (branch `docs/5234-inventory-drift-guard`) — route: delegated writer — commit: —
- [ ] T5 S6-S9 Verify PR 2, RDD review, PR "Closes #5234" — route: verifier + parent — commit: n/a

## Log

- L1 (user, 2026-10-04): "Retomo la wave E de la meta-issue #4556 (docs accuracy re-audit). [...] Scope de la wave E, según la tabla de #4556: "Guards: PR template docs checkbox, CI check for retired terms in living docs, generated agent and package inventories""
- L2 (user): accepted drift tests over generation ("Adelante"); label changed to type:docs; #5234 created and approved ("YA la aprobe, crea la rama y adelante").
- L3 (evidence): explorer mutqjc3c-1-zswz; existing guard covers 4 files only; `allAgents` has 17 entries (`internal/catalog/agents.go:18`); 20 dirs under `internal/components/`.
- L4 (evidence, T1): RED listed 7 lines (engram.md:159, gga-powershell-shim.md:71, skill-registry.md:128, testing-agents-deterministically.md:70,71,251, trigger-rules.md:14); GREEN after fixes; `go test ./internal/app/` ok. Historical rule: `> **Historical` callout in first 12 lines (skips organic-rdd-testing-guide.md, release-v0.1.0-checklist.md). Allowlist: one line-level rule `\b(retired|legacy)\b`. Tradeoff accepted by parent: the 4 entry files lost the file-wide no-exception check; a line saying retired/legacy now passes there too. New wording at testing-agents-deterministically.md matches `assertNoSDDArtifacts` (e2e/organicruntime/organic_runtime_test.go:3478).
- L5 (decision, T2): `skills/branch-pr/SKILL.md:133` keeps its own template copy, which already diverges from the real template; left out of scope.
- L6 (evidence, T3): verifier mutrfwio-3-a1bp: S1-S3, S5 PASS; S4 FAIL (testing-agents-deterministically.md:70,71,251 overclaimed "no workflow ... state"; `assertNoSDDArtifacts` only rejects sdd-*/sdd/trace/evaluation under the common-dir gentle-ai store). `go test ./internal/app/ ./internal/assets/` ok; gofmtcheck and vet clean; 162 changed lines.
- L7 (evidence, T3): user granted RDD consent on the host. Lineage review-470e723a6fe6dccf (medium, one lens review-reliability, base 7af7eef8 committed-only, target sha256:604ea30d...) approved with one informational WARNING R3-broad-retirement-exemption (same tradeoff as L4); acknowledge-approved burned authority (consumed sha256:2f49e370...). The S4 wording was then narrowed to "retired-workflow, trace, or evaluation state" in a passive docs follow-up commit (not reviewed: trivial passive docs edit).
