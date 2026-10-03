# ODD spec-by-reference parity with gentle-pi (Gentleman-Programming/gentle-shell#1713)
Branch: `fix/odd-spec-by-reference` (base `origin/main` f4d3f3e8) · Delivery: single-pr · Runner: `go test ./internal/components/agentguidance/ ./internal/assets/`; full `go test ./...`
Engram mirror: `odd/odd-spec-by-reference/tasks` (project gentle-pi session) · Route: inline (user: no delegation today; RDD reviewers allowed)

## Specs
S1. Parity: "luego una vez que esto lo tengamos tenemos que hcer la paridad en gentle-ai" — the canonical ODD routing block carries the same contract gentle-pi merged in gentle-shell#1718.
S2. Feature document is the reference with a fixed order: header (2-3 lines) → `## Specs` (numbered `S#`, user's exact strings, error messages, and examples verbatim, never summarized, no unrequested requirements) → `## Tasks` (one line per task: ID, linked `S#`, route, commit) → `## Log` last (`L1` = original request verbatim; later user corrections verbatim; evidence and decisions).
S3. Change = only the affected spec and task: "A diferencia de SDD, un cambio no re hace todo, solo re hace esa spec y la tarea asociada".
S4. Handoffs pass a reference, never a paraphrase: `Spec: odd/tasks/<feature>.md (read until ## Log). Do T#; S#.`; without a document, the user's request verbatim. Workers report covered `S#`.
S5. Verify reads the whole document, runs the spec's examples (isolated state when they mutate), and returns a verdict per `S#`. User-reported failures are reproduced before deciding they already work.
S6. The shipped OpenCode `gentle-ai-worker`, `gentle-ai-verify`, and `gentle-ai-explore` agents match gentle-pi's merged wording.

## Tasks
- [x] T1 (S2-S5) inline · canonical routing.go + routing_test.go + docs/usage.md · RED→GREEN · see L4
- [ ] T2 (S6) inline · internal/assets/opencode/agents/gentle-ai-{worker,verify,explore}.md + assets test
- [ ] T3 (S1) pending merge · in gentle-pi, `npm run mirror:odd-routing` to regenerate `fixtures/odd-routing-canonical.md`

## Log
L1 2026-10-03 user (verbatim): > luego una vez que esto lo tengamos tenemos que hcer la paridad en gentle-ai
L2 2026-10-03 user (verbatim): > ahh si dale haz la paridad
L3 2026-10-03 context: gentle-pi PR gentle-shell#1718 merged (cd4ba5a7) with the always-on contract in extensions/gentle-ai.ts ODD steps 5-6, assets/orchestrator-memory.md, and the three agents. Canon still prescribed "objective, problem, why, scope, constraints..." and "passes the locator and relevant context".
L4 2026-10-03 T1 evidence (risk: medium, canonical prompt contract for every agent):
   RED: TestRenderRoutingOrganicTaskContinuity failed for all 17 catalog agents on the new spec-by-reference clauses.
   GREEN: `go test ./internal/components/agentguidance/ ./internal/assets/` ok; `go test ./...` 80 packages ok; `go vet` and `gofmt -l` clean.
   Decision: the handoff example is `Spec: odd/tasks/<feature>.md, T2, S3-S4` and "read until `## Log`" is stated outside the code span (backticks cannot nest; gentle-pi's always-on example nests them).
