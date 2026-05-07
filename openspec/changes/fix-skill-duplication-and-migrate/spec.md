# Spec: fix-skill-duplication-and-migrate

## Goal

Add `user-invocable: false` and `disable-model-invocation: true` to the YAML frontmatter of the 11 SDD `SKILL.md` files so Claude Code v2.x stops showing duplicate entries in the `/` picker, while keeping the files readable by agents and commands via `Read`.

---

## Functional Requirements

- The 11 SDD `SKILL.md` files MUST contain `user-invocable: false` and `disable-model-invocation: true` as **top-level** YAML frontmatter keys (not nested under `metadata:`).
- The 11 files are: `internal/assets/skills/_shared/SKILL.md` plus `sdd-apply`, `sdd-archive`, `sdd-design`, `sdd-explore`, `sdd-init`, `sdd-onboard`, `sdd-propose`, `sdd-spec`, `sdd-tasks`, `sdd-verify`.
- All pre-existing top-level fields (`name`, `description`, `license`, `metadata`, `version`) MUST retain their values byte-for-byte after the change.
- Each of the 10 phase files MUST keep `name:` equal to its directory basename (e.g. `name: sdd-apply`).
- `_shared/SKILL.md` MUST keep `name: _shared`.
- The frontmatter linter at `internal/assets/skills_frontmatter_test.go` MUST accept `user-invocable` and `disable-model-invocation` as valid top-level keys — the existing allowlist `{name, description, license, metadata, version}` MUST be widened to include them.
- No other non-SDD `SKILL.md` files MUST carry the new flags (non-SDD skills are intentionally user-invocable).
- Golden test files under `testdata/golden/` that embed the affected SKILL.md content MUST be regenerated so that `go test ./...` passes without `-update`.

---

## Behavioral Requirements

- After `gentle-ai install` or `gentle-ai sync` on any `~/.claude/` directory, none of the 11 SDD SKILL.md entries MUST appear as user-invocable items in the Claude Code `/` picker.
- The `~/.claude/commands/sdd-*.md` entries MUST still appear in the `/` picker — they are the single canonical user-facing entry point for each phase.
- Each of `sdd-apply`, `sdd-archive`, `sdd-design`, `sdd-explore`, `sdd-init`, `sdd-onboard`, `sdd-propose`, `sdd-spec`, `sdd-tasks`, `sdd-verify` MUST appear AT MOST ONCE in the `/` picker after install.
- `~/.claude/agents/sdd-*.md` sub-agents MUST continue to function; orchestrator delegation MUST NOT be affected by the frontmatter flags.
- The `Read` tool MUST continue to load `~/.claude/skills/sdd-{phase}/SKILL.md` successfully — `Read` does not interpret `user-invocable` or `disable-model-invocation`.
- Claude Code MUST NOT auto-load the SDD SKILL.md files as contextual skills via description-based matching (suppressed by `disable-model-invocation: true`).

---

## Test Scenarios

### Scenario A — Frontmatter linter accepts new keys

- GIVEN the 11 embedded SDD SKILL.md files carry `user-invocable: false` and `disable-model-invocation: true` at the top level of their YAML frontmatter
- WHEN `go test ./internal/assets/...` runs
- THEN `TestSkillFrontmatterIsLintClean` passes with zero "non-standard top-level frontmatter key" failures
- AND the two new keys are present in the widened `allowedKeys` map in `skills_frontmatter_test.go`

---

### Scenario B — All 11 files carry both flags

- GIVEN the change has been applied to the embedded assets
- WHEN each of the 11 `SKILL.md` files is read and the YAML frontmatter block is parsed
- THEN every file contains `user-invocable: false` at the top level
- AND every file contains `disable-model-invocation: true` at the top level

---

### Scenario C — Existing fields preserved byte-for-byte

- GIVEN the 11 modified SKILL.md files
- WHEN parsed as YAML
- THEN `name`, `description`, `license`, `metadata`, and `version` retain values identical to their pre-change state
- AND no other lines in the file body (below the closing `---`) are altered

---

### Scenario D — Golden tests regenerated and passing

- GIVEN the embedded SKILL.md files now include the two new frontmatter flags
- WHEN `go test -run TestGolden ./... -update` is executed and the resulting golden files are committed
- THEN `go test ./...` passes without `-update`
- AND the diff on each affected golden file shows exactly the two added frontmatter lines and no other changes

---

### Scenario E — Install writes new frontmatter to disk

- GIVEN a fresh `~/.claude/skills/` directory with no prior gentle-ai install
- WHEN `gentle-ai install` runs with the Claude adapter selected
- THEN `~/.claude/skills/sdd-apply/SKILL.md` (and each of the other 10 skill files) contains `user-invocable: false` at the top level of its YAML frontmatter
- AND contains `disable-model-invocation: true` at the top level

---

### Scenario F — Picker shows no duplicate SDD entries (manual verification)

- GIVEN a Claude Code v2.1.131+ session with the new SKILL.md files installed via `gentle-ai sync`
- WHEN the user opens the `/` skill picker
- THEN each of `sdd-apply`, `sdd-archive`, `sdd-design`, `sdd-explore`, `sdd-init`, `sdd-onboard`, `sdd-propose`, `sdd-spec`, `sdd-tasks`, `sdd-verify` appears AT MOST ONCE
- AND every visible entry originates from `~/.claude/commands/sdd-*.md`, not from `~/.claude/skills/sdd-*/SKILL.md`
- NOTE: behavioral verification performed manually post-merge; not an automated Go test

---

### Scenario G — Sub-agent delegation still works (manual verification)

- GIVEN a Claude Code session with the new SKILL.md files installed
- WHEN the SDD orchestrator delegates to the `sdd-explore` sub-agent
- THEN the sub-agent executes `Read` on `~/.claude/skills/sdd-explore/SKILL.md` without error
- AND the sub-agent completes the exploration phase normally
- NOTE: behavioral verification performed manually post-merge; not an automated Go test

---

## Out of Scope

- Path relocation to `~/.claude/sdd-lib/` (Option C) — rejected in proposal.
- Migration logic — existing installs pick up new content automatically on next `gentle-ai sync`.
- Changes to `internal/components/sdd/inject.go` write paths — destination unchanged, only file content changes.
- Changes to `~/.claude/commands/sdd-*.md` or `~/.claude/agents/sdd-*.md` reference paths.
- Cross-adapter changes for Cursor, Kiro, OpenCode, Windsurf — no duplication in those adapters.
- Frontmatter changes to non-SDD skills (`judgment-day`, `branch-pr`, `chained-pr`, `cognitive-doc-design`, etc.).
- Changes to `internal/components/uninstall/service.go`.

---

## References

- Proposal (engram): `sdd/fix-skill-duplication-and-migrate/proposal`
- Proposal (file): `openspec/changes/fix-skill-duplication-and-migrate/proposal.md`
- Frontmatter linter: `internal/assets/skills_frontmatter_test.go` (lines 30–36 — current allowlist)
- Claude Code skill schema docs: https://code.claude.com/docs/en/skills
- Claude Code slash commands docs: https://code.claude.com/docs/en/slash-commands
