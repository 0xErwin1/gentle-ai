# Delta B — Generic Skill-Invocation Directive (CENTRAL)

**Domain**: all 6 persona assets (claude, opencode, generic, kiro, kimi) — per-agent variant wording
**Type**: ADDED
**Status**: SHIPPED — commit 9bd58d9
**Files**:
- `internal/assets/claude/persona-gentleman.md` — Claude variant (names the built-in `Skill` tool)
- `internal/assets/opencode/persona-gentleman.md` — non-Claude variant
- `internal/assets/generic/persona-gentleman.md` — non-Claude variant
- `internal/assets/generic/persona-neutral.md` — non-Claude variant
- `internal/assets/kiro/persona-gentleman.md` — non-Claude variant
- `internal/assets/kimi/persona-gentleman.md` — non-Claude variant
**Depends on**: Delta A (replaces the block A removes)

## Context

After Delta A removes the hardcoded trigger table from all 6 personas, each persona MUST acquire a short behavioral directive that tells the model (or agent) to use `<available_skills>` for all skill discovery. This directive is the central behavioral fix. Without it, skills listed in `<available_skills>` remain available but the model has no explicit instruction to consult and act on that list.

**Per-agent variant policy** (design Decision 2, option β'):
- Claude variant names the built-in `Skill` tool explicitly: "invoke it via the built-in `Skill` tool"
- Non-Claude variants substitute: "read the matching SKILL.md (using your agent's read mechanism)"

All variants share the same mandatory-phrasing structure (`## Contextual Skill Loading (MANDATORY)`, `<available_skills>` authoritative block, `Self-check BEFORE every response` imperative).

---

## ADDED Requirements

### Requirement: Generic Skill-Invocation Directive

All 6 persona sources MUST contain a directive that instructs the model or agent to evaluate `<available_skills>` before responding and invoke (or read) the matching skill BEFORE generating its reply.

The directive in each persona MUST satisfy ALL of the following wording requirements:

| Requirement | Claude variant | Non-Claude variants |
|-------------|---------------|---------------------|
| References `<available_skills>` by that exact token | MUST | MUST |
| Names the invocation mechanism explicitly | `Skill` tool | "read the matching SKILL.md (using your agent's read mechanism)" |
| Uses mandatory phrasing (MUST, MANDATORY, or equivalent) | MUST | MUST |
| Instructs action BEFORE generating the reply | MUST | MUST |
| Does NOT enumerate specific skill names or file paths | MUST NOT | MUST NOT |
| Does NOT use a markdown table with trigger contexts | MUST NOT | MUST NOT |

The directive MUST be self-contained in no more than 6 lines (heading + body). The section heading MUST be `## Contextual Skill Loading (MANDATORY)`.

#### Scenario: Directive present in all 6 emitted personas

- GIVEN each of the 6 persona files after Delta B is applied (commit 9bd58d9)
- WHEN each file is read in full
- THEN it contains the token `<available_skills>`
- AND it contains the mandatory-phrasing keyword `BEFORE every response`
- AND it does NOT contain any markdown table with a `| Context |` or `| Read this file |` header
- AND for `claude/persona-gentleman.md` specifically: it contains the token `Skill` (referencing the built-in tool)
- AND for the 5 non-Claude personas: it contains the phrase "read the matching SKILL.md (using your agent's read mechanism)"

#### Scenario: No skill names hardcoded in directive

- GIVEN the directive paragraph added by Delta B
- WHEN the directive text is scanned for any known skill slug (e.g. `go-testing`, `skill-creator`, `chained-pr`, `sdd-apply`)
- THEN no matches are found
- AND the directive's behavior generalizes to all present and future skills

#### Scenario: Golden reflects directive addition

- GIVEN the test `TestGoldenPersona_Claude_Gentleman` is red after Delta A (golden mismatch)
- WHEN Delta B directive is written into the source and goldens are regenerated
- THEN `TestGoldenPersona_Claude_Gentleman` passes green
- AND the golden contains `<available_skills>` in the persona section
- AND the golden does NOT contain `Skills (Auto-load based on context)`

#### Scenario: Rendered directive contains all required structural tokens

- GIVEN each of the 6 persona files after Delta B is applied (commit 9bd58d9)
- WHEN the file content is asserted by `TestPersonasContainContextualSkillLoadingDirective` (in `internal/assets/assets_test.go`)
- THEN the section heading `## Contextual Skill Loading (MANDATORY)` is present
- AND the token `<available_skills>` is present
- AND the phrase `Self-check BEFORE every response` is present
- AND the phrase `blocking requirement` is present (mandatory phrasing)
- AND for `claude/persona-gentleman.md` specifically: the token `` `Skill` tool `` is present and the phrase `invoke it via the built-in \`Skill\` tool` is present
- AND for the 5 non-Claude personas: the phrase `read the matching SKILL.md` is present and `` `Skill` tool `` is NOT present

Verification method: `go test ./internal/assets/... -run TestPersonasContainContextualSkillLoadingDirective`

---

### Behavioral Verification (deferred)

Structural-only evidence cannot prove that a live model invokes the `Skill` tool proactively when the matching skill is listed in `<available_skills>`. The scenario above validates that the directive is correctly rendered into the persona asset; it cannot validate runtime model behavior.

Behavioral verification is deferred to a future change. That future change should verify:
- A Claude Code session with at least one skill installed (e.g. `go-testing`) triggers a `Skill` tool call — appearing in the session transcript BEFORE any Edit, Write, or code-modification call — when the user asks a task covered by that skill.
- The invocation occurs without any manual `~/.claude/CLAUDE.md` skill-trigger table added by the maintainer (i.e. the directive in the persona asset alone is sufficient).
- Non-Claude variants (opencode, kiro, kimi) read the matching SKILL.md via their native read mechanism before generating the response.
- The verification covers at least two skill slugs and two prompt phrasings to rule out keyword matching in the model rather than structural directive compliance.

Such verification requires either a manual transcript capture (option A) or a Claude API automated test (option B). Both were considered and deferred in this change in favor of speed (option C, structural-only).

---

## Test Surface

| Test | Files covered | Status |
|------|--------------|--------|
| `TestPersonasContainContextualSkillLoadingDirective` (commit 131707f) | All 6 persona paths | green |
| `TestGoldenPersona_Claude_Gentleman` | `persona-claude-gentleman.golden` | green |
| `TestGoldenPersona_Claude_Neutral` | `persona-claude-neutral.golden` | green |
| `TestGoldenPersona_Opencode_Gentleman` | `persona-opencode-gentleman.golden` | green |
| `TestGoldenPersona_Opencode_Neutral` | `persona-opencode-neutral.golden` | green |
| `TestGoldenPersona_Windsurf_Gentleman` | `persona-windsurf-gentleman.golden` | green |
| `TestGoldenPersona_Kiro_Gentleman` | `persona-kiro-gentleman.golden` | green |
| `TestGoldenPersona_Antigravity_Gentleman` | `persona-antigravity-gentleman.golden` | green |
| `TestGoldenCombined_Claude` | `combined-claude-claudemd.golden` | green |
| `TestGoldenCombined_Windsurf` | `combined-windsurf-global-rules.golden` | green |

---

## Manual Smoke Tests

### Manual smoke test (Claude Code variant)

- **Agent under test**: Claude Code v2.x (any version exposing the built-in `Skill` tool)
- **Setup**: fresh install of gentle-ai with at least the `go-testing` skill present in the resolved skill set; no manual `~/.claude/CLAUDE.md` skill-trigger table from the maintainer.
- **Prompt verbatim**: `Add a Go test for the function ParseURL in internal/url/parser.go.`
- **Observable signal**: the session transcript contains an invocation of the built-in `Skill` tool with `name: "go-testing"` BEFORE any Edit, Write, or Bash(go test) call.
- **Pass criterion**: the `Skill` tool invocation appears in the transcript before any code modification.
- **Fail criterion**: the model writes/edits the test file without first invoking the `Skill` tool.

### Manual smoke test (non-Claude variant)

- **Agents covered**: opencode, generic, kiro, kimi
- **Setup**: equivalent fresh install for each agent. Skill `pr-review` (or any other clearly-triggered skill) present in the agent's skills directory.
- **Prompt verbatim**: `Review PR #1 in this repo.`
- **Observable signal**: the agent reads `<agent-skills-dir>/pr-review/SKILL.md` (via its native read mechanism) BEFORE generating the review content.
- **Pass criterion**: the SKILL.md read appears in the transcript before review generation.
- **Fail criterion**: the agent generates the review without reading the SKILL.md.

---

## Out of Scope

Exact wording of the directive was a design phase decision (B2 chosen). This spec records the wording REQUIREMENTS and confirms B2 satisfies all of them.

Agents without SkillsDir (windsurf/qwen/codex) inherit the directive from generic personas but cannot act on `<available_skills>` if the host does not populate it — no worse off than before this change.
