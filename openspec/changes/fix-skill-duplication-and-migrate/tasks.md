# Tasks: fix-skill-duplication-and-migrate

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | 50–90 |
| 400-line budget risk | Low |
| Chained PRs recommended | No |
| Suggested split | Single PR |
| Delivery strategy | ask-on-risk |
| Chain strategy | pending |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: pending
400-line budget risk: Low

**Estimate breakdown:**
- 11 SKILL.md edits × 2 lines = 22 lines
- 1 linter file widen = 2 lines
- 8 golden files × ~3 lines each (2 added + separator) = ~24 lines
- Total: ~48 lines. Single PR; well under budget.

### Suggested Work Units

| Unit | Goal | Likely PR | Notes |
|------|------|-----------|-------|
| 1 | All 5 tasks below | PR 1 | Single atomic PR; tests + golden regen included |

---

## Phase 1: Frontmatter Edits (Foundation)

- [x] 1.1 Add `user-invocable: false` and `disable-model-invocation: true` as top-level YAML frontmatter keys to all 11 SDD SKILL.md files. Insert after `description:`, before `license:` (alphabetical order). Files: `internal/assets/skills/_shared/SKILL.md`, `internal/assets/skills/sdd-apply/SKILL.md`, `internal/assets/skills/sdd-archive/SKILL.md`, `internal/assets/skills/sdd-design/SKILL.md`, `internal/assets/skills/sdd-explore/SKILL.md`, `internal/assets/skills/sdd-init/SKILL.md`, `internal/assets/skills/sdd-onboard/SKILL.md`, `internal/assets/skills/sdd-propose/SKILL.md`, `internal/assets/skills/sdd-spec/SKILL.md`, `internal/assets/skills/sdd-tasks/SKILL.md`, `internal/assets/skills/sdd-verify/SKILL.md`. Done criteria: each file's frontmatter block contains both new keys; `name`, `description`, `license`, `metadata`, `version` values unchanged byte-for-byte.

**Suggested commit:** `feat(sdd): hide SDD SKILL.md files from / picker via frontmatter flags`

---

## Phase 2: Linter Widening (Unblock CI)

- [x] 2.1 Edit `internal/assets/skills_frontmatter_test.go` — extend `allowedKeys` map at lines 30–36 to add `"user-invocable": true` and `"disable-model-invocation": true`. Done criteria: `go test ./internal/assets/...` passes with zero `non-standard top-level frontmatter key` failures.

**Suggested commit:** `test(assets): widen frontmatter allowlist for user-invocable and disable-model-invocation`

---

## Phase 3: Golden Regeneration (Mechanical)

- [x] 3.1 Run `go test ./internal/components/ -update` to regenerate the 8 affected golden files: `testdata/golden/sdd-{antigravity,codex,cursor,gemini,kiro,opencode,vscode,windsurf}-skill-sdd-init.golden`. Done criteria: diff on each affected file shows exactly +2 lines (`user-invocable: false` and `disable-model-invocation: true` inside the frontmatter block) and no other changes. If any unexpected golden changes, stop and investigate.

**Suggested commit:** `test(golden): regenerate sdd-init SKILL.md goldens after frontmatter flags`

---

## Phase 4: Full Test Suite (Verification)

- [x] 4.1 Run `go test ./...` (no `-update` flag). Done criteria: all tests pass. This catches any snapshot or embedding test not covered by Phase 3.

**Suggested commit:** *(no separate commit — if tests pass, Phase 3 commit stands; if a new golden needs updating, iterate Phase 3)*

---

## Phase 5: Manual Verification (Human-in-the-loop)

- [ ] 5.1 Build the binary: `go build -o /tmp/gentle-ai .` from repo root.
- [ ] 5.2 Run `gentle-ai install` (or `gentle-ai sync`) against your `~/.claude/` directory.
- [ ] 5.3 Confirm `~/.claude/skills/sdd-apply/SKILL.md` and each of the other 10 files contain both `user-invocable: false` and `disable-model-invocation: true` at the top level of their YAML frontmatter.
- [ ] 5.4 Open Claude Code v2.1.131+ and open the `/` picker. Each of `sdd-apply`, `sdd-archive`, `sdd-design`, `sdd-explore`, `sdd-init`, `sdd-onboard`, `sdd-propose`, `sdd-spec`, `sdd-tasks`, `sdd-verify` MUST appear AT MOST ONCE. All visible entries originate from `~/.claude/commands/sdd-*.md`, not `~/.claude/skills/sdd-*/SKILL.md`.
- [ ] 5.5 Trigger an orchestrator delegation to `sdd-explore`. Confirm the sub-agent successfully reads `~/.claude/skills/sdd-explore/SKILL.md` via the `Read` tool and completes normally.

*Steps 5.1–5.5 are a human reviewer checklist, not automated CI.*

---

## Recommended PR

**Title:** `fix: hide SDD SKILL.md files from Claude Code / picker to eliminate duplicates`

**Body summary:**
Closes the duplicate `/sdd-*` picker entries in Claude Code v2.x. Adds `user-invocable: false` and `disable-model-invocation: true` to the YAML frontmatter of the 11 SDD SKILL.md embedded assets. Widens the embedded-asset frontmatter linter allowlist by exactly two keys to keep CI green. Regenerates 8 golden files (mechanical +2 line each). No path, routing, or runtime changes — Read-based access by sub-agents is unaffected.

**Checklist:**
- [ ] All 11 SKILL.md files carry both new frontmatter flags
- [ ] `go test ./internal/assets/...` passes (linter)
- [ ] `go test ./...` passes (full suite)
- [ ] 8 golden files regenerated with only the 2 expected lines per diff
- [ ] Manual picker verification done (Scenario F)
- [ ] Manual delegation smoke test done (Scenario G)

---

## Spec Scenarios Covered

| Task | Spec Scenario |
|------|---------------|
| 1.1  | Scenario B (both flags present), Scenario C (existing fields preserved), Scenario E (install writes flags) |
| 2.1  | Scenario A (linter accepts new keys) |
| 3.1  | Scenario D (goldens regenerated and passing) |
| 4.1  | Scenario D (full suite passes) |
| 5.*  | Scenario F (picker dedup), Scenario G (delegation still works) |
