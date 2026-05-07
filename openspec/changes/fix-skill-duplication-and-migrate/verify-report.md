# Verify Report: fix-skill-duplication-and-migrate

## Verdict

**PASS** — all in-scope CI-verifiable scenarios (A–E) pass; full test suite green; behavioral scenarios F + G remain manual post-merge. The original WARNING-1 (unrelated `gentle-logo.ts` plugin work in working tree) was **RESOLVED** before commit — those files were removed from the working tree by the user. Working tree now contains only the in-scope SDD diff plus an unrelated `internal/tui/styles/logo.go` change (separate concern, will be excluded from this commit by selective `git add`).

---

## Executive Summary

11/11 SDD `SKILL.md` files carry `user-invocable: false` and `disable-model-invocation: true` at the top level between `description:` and `license:`. The frontmatter linter allowlist was widened by exactly the two required keys. Eight `sdd-init` goldens were regenerated with +2 lines each. `go test -count=1 ./...` passes across all 40+ packages (zero failures, zero panics). In-scope diff is 45 insertions / 5 deletions across 20 files — within the forecast (48). However, the working tree carries an unrelated OpenCode `gentle-logo` plugin diff that must NOT ride along in this PR.

---

## Test Results

### Targeted linter
```
go test -run TestSkillFrontmatterIsLintClean ./internal/assets/...
ok  github.com/gentleman-programming/gentle-ai/internal/assets  0.006s
```
PASS.

### Full assets package
```
go test ./internal/assets/...
ok  github.com/gentleman-programming/gentle-ai/internal/assets  (cached)
```
PASS.

### Full suite (clean run, no caching)
```
go test -count=1 ./...
```
All 40+ packages PASS. Notable timings:
- `internal/components/sdd` — 69.088s
- `internal/cli` — 54.523s
- `internal/components` — 12.800s
- `internal/app` — 5.937s

Zero failures, zero panics, zero skipped suites.

---

## Per-Scenario Verification

### Scenario A — Frontmatter linter accepts new keys: **PASS**

Evidence — `internal/assets/skills_frontmatter_test.go` lines 30–38:
```go
allowedKeys := map[string]bool{
    "name":                     true,
    "description":              true,
    "license":                  true,
    "metadata":                 true,
    "version":                  true,
    "user-invocable":           true,
    "disable-model-invocation": true,
}
```
- 7 keys total: pre-existing 5 plus the 2 new keys.
- `TestSkillFrontmatterIsLintClean` passes with all 11 SDD SKILL.md files now using the new keys.

### Scenario B — All 11 files carry both flags: **PASS**

Inspected first 15 lines of each of the 11 files. Every file contains both keys at the top level, in the order:
```
description: "..."
disable-model-invocation: true
user-invocable: false
license: MIT
```
Files verified (all PASS):
- `internal/assets/skills/_shared/SKILL.md`
- `internal/assets/skills/sdd-apply/SKILL.md`
- `internal/assets/skills/sdd-archive/SKILL.md`
- `internal/assets/skills/sdd-design/SKILL.md`
- `internal/assets/skills/sdd-explore/SKILL.md`
- `internal/assets/skills/sdd-init/SKILL.md`
- `internal/assets/skills/sdd-onboard/SKILL.md`
- `internal/assets/skills/sdd-propose/SKILL.md`
- `internal/assets/skills/sdd-spec/SKILL.md`
- `internal/assets/skills/sdd-tasks/SKILL.md`
- `internal/assets/skills/sdd-verify/SKILL.md`

### Scenario C — Existing fields preserved byte-for-byte: **PASS**

`git diff -U0` on each SKILL.md shows ONLY the two new lines added — no changes to `name`, `description`, `license`, `metadata`, `version`, or body content. Representative tight diff for `_shared/SKILL.md`:
```
@@ -3,0 +4,2 @@ description: "Shared SDD references for installed skills. Not invokable."
+disable-model-invocation: true
+user-invocable: false
```
Each phase file also keeps `name:` equal to its directory basename (verified in Scenario B).

### Scenario D — Goldens regenerated and passing: **PASS**

Exactly 8 goldens changed, all matching the spec'd set:
```
testdata/golden/sdd-antigravity-skill-sdd-init.golden
testdata/golden/sdd-codex-skill-sdd-init.golden
testdata/golden/sdd-cursor-skill-sdd-init.golden
testdata/golden/sdd-gemini-skill-sdd-init.golden
testdata/golden/sdd-kiro-skill-sdd-init.golden
testdata/golden/sdd-opencode-skill-sdd-init.golden
testdata/golden/sdd-vscode-skill-sdd-init.golden
testdata/golden/sdd-windsurf-skill-sdd-init.golden
```
Spot-checked diff on `sdd-codex-skill-sdd-init.golden` and `sdd-cursor-skill-sdd-init.golden` — each shows exactly the same +2 lines (`+disable-model-invocation: true`, `+user-invocable: false`) inserted between `description` and `license`, no other changes.

No unexpected goldens (e.g. for non-`sdd-init` SDD skills, or non-SDD assets) changed. Full suite passes WITHOUT `-update`, confirming goldens match current embedded content.

### Scenario E — Install writes new frontmatter to disk: **PASS (static evidence)**

Static evidence: the embedded asset content is what `installSkill` writes to `~/.claude/skills/{phase}/SKILL.md`. Since the embedded content now contains both flags (Scenario B) and write-paths in `internal/components/sdd/inject.go` are unchanged (Out-of-Scope guarantees), a fresh `gentle-ai install` will produce the new frontmatter on disk. Phase 5 manual verification will confirm runtime.

### Scenario F — Picker shows no duplicate SDD entries: **MANUAL (post-merge)**

Cannot run live Claude Code v2.x in CI. See Manual Verification Checklist below.

### Scenario G — Sub-agent delegation still works: **PASS (static evidence) + MANUAL (post-merge)**

Static evidence — both reference paths are intact in the Claude adapter (UNCHANGED in this batch):
- `internal/assets/claude/agents/sdd-explore.md`:
  > Read the skill file at \`~/.claude/skills/sdd-explore/SKILL.md\` and follow it exactly.
- `internal/assets/claude/commands/sdd-explore.md`:
  > Otherwise, read the skill file at \`~/.claude/skills/sdd-explore/SKILL.md\` FIRST, then follow its instructions exactly inline.

Since `Read` does not interpret `user-invocable` or `disable-model-invocation`, sub-agent delegation will continue to work. Phase 5 manual verification confirms runtime.

---

## Out-of-Scope Guarantee Status

| Guarantee | Status | Evidence |
|-----------|--------|----------|
| `internal/components/sdd/inject.go` UNCHANGED | **VIOLATED (warning, unrelated work)** | git diff shows ~93 line OpenCode gentle-logo plugin diff — see WARNING below |
| `internal/components/skills/inject.go` UNCHANGED | PASS | not in `git diff --name-only` |
| `internal/assets/claude/agents/*.md` UNCHANGED | PASS | not in diff |
| `internal/assets/claude/commands/*.md` UNCHANGED | PASS | not in diff |
| Other adapters (cursor, kiro, opencode, gemini, vscode, antigravity, codex, windsurf) UNCHANGED | PASS | only their `testdata/golden/sdd-*-skill-sdd-init.golden` golden files changed; no source asset changes |
| `internal/components/uninstall/service.go` UNCHANGED | PASS | not in diff |
| Non-SDD `SKILL.md` files UNCHANGED | PASS | only the 11 SDD SKILL.md files in `internal/assets/skills/{_shared, sdd-*}` were touched |

---

## Findings

### CRITICAL — none

The in-scope SDD work is complete and correct.

### WARNING-1 — RESOLVED (unrelated changes were removed from working tree)

Initial verify found unstaged changes to `internal/components/sdd/inject.go`, `internal/components/sdd/inject_test.go`, `internal/assets/assets_test.go`, and an untracked `internal/assets/opencode/plugins/gentle-logo.ts` that were unrelated to this SDD change. The user removed those before commit. Current working tree confirmed clean of that contamination.

A separate unrelated change to `internal/tui/styles/logo.go` (TUI logo refresh) remains in the working tree. It is also out of scope for this SDD change and will be excluded from the staged commit via selective `git add` of only the in-scope files:
- The 11 SKILL.md files under `internal/assets/skills/`
- `internal/assets/skills_frontmatter_test.go`
- The 8 goldens under `testdata/golden/sdd-*-skill-sdd-init.golden`
- The new SDD artifacts under `openspec/changes/fix-skill-duplication-and-migrate/`

The TUI logo work belongs in a separate commit/PR.

### SUGGESTION-1 — Consider documenting the alphabetical-within-pair convention

Tasks.md said "alphabetical order" without clarifying scope. Apply-progress correctly interpreted this as alphabetical order between the two new keys (`disable-model-invocation` precedes `user-invocable`). For future readers, design.md ADR 1 already covers TOP-LEVEL placement; a one-line note that the two new keys are inserted in alphabetical order between `description:` and `license:` would prevent ambiguity if more flags are added later. Optional, no rework needed.

---

## Manual Verification Checklist (post-merge / post-release)

Reviewer must run these against a real Claude Code v2.1.131+ environment after the PR ships:

### Scenario F — Picker dedup
- [ ] Build the binary: `go build -o /tmp/gentle-ai .`
- [ ] (Optional, for clean-room confidence) move `~/.claude/skills/` aside or use a fresh test home directory.
- [ ] Run `gentle-ai install` (or `gentle-ai sync`) against `~/.claude/`.
- [ ] Confirm `~/.claude/skills/sdd-apply/SKILL.md` (and the other 10) contain both `user-invocable: false` and `disable-model-invocation: true` at the top level.
- [ ] Open Claude Code v2.1.131+ and open the `/` picker.
- [ ] Confirm each of `sdd-apply`, `sdd-archive`, `sdd-design`, `sdd-explore`, `sdd-init`, `sdd-onboard`, `sdd-propose`, `sdd-spec`, `sdd-tasks`, `sdd-verify` appears AT MOST ONCE.
- [ ] Confirm every visible entry originates from `~/.claude/commands/sdd-*.md`, not `~/.claude/skills/sdd-*/SKILL.md`.

### Scenario G — Sub-agent delegation
- [ ] In a Claude Code session with the new install, trigger an orchestrator delegation to `sdd-explore` (e.g. start a small `/sdd-new` flow).
- [ ] Confirm the `sdd-explore` sub-agent reads `~/.claude/skills/sdd-explore/SKILL.md` via the `Read` tool successfully.
- [ ] Confirm the exploration completes normally and returns a structured analysis.

---

## Recommendation

**Ready to commit + open PR**, with one caveat: **stage only the in-scope files** for this SDD change. Do NOT include `internal/components/sdd/inject.go`, `internal/components/sdd/inject_test.go`, `internal/assets/assets_test.go`, or `internal/assets/opencode/plugins/gentle-logo.ts` in this commit — those belong in a separate PR for the OpenCode logo plugin work.

**Suggested commit (single, atomic) for this SDD change:**
```
fix(sdd): hide SDD SKILL.md files from Claude Code / picker

Add `user-invocable: false` and `disable-model-invocation: true` to the
frontmatter of all 11 SDD SKILL.md files (10 phases + _shared). This
removes the duplicate `/sdd-*` picker entries users were reporting in
Claude Code v2.x — each phase appeared twice, once from
~/.claude/skills/ and once from ~/.claude/commands/.

The skills remain readable by sub-agents and slash command fallbacks via
the Read tool — only the user-invocable and model-autoload paths are
disabled. Widens the frontmatter linter allowlist by the two new keys
and regenerates 8 affected sdd-init goldens.
```

After commit + PR is opened, the reviewer should walk through the manual checklist above before merging.

---

## Next Recommended

`sdd-archive` — once the PR is merged and Scenarios F + G are manually verified, archive this change.
