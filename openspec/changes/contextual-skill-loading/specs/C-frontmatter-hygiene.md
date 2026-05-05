# Delta C — Frontmatter Hygiene

**Domain**: skills/frontmatter
**Type**: MODIFIED (two specific SKILL.md files) + NEW test (linter)
**Status**: SHIPPED — fixes in commits 1b2b374 and 45dc833; linter in commit 540d32b
**Files**:
- `internal/assets/skills/chained-pr/SKILL.md` — name field fix (commit 1b2b374)
- `internal/assets/skills/skill-creator/SKILL.md` — allowed-tools removal (commit 45dc833)
- `internal/assets/skills_frontmatter_test.go` — regression guardrail (commit 540d32b)

## Context

Two SKILL.md files in the embedded skills asset tree had frontmatter anomalies. Both fixes have landed:

1. **`chained-pr/SKILL.md`** had `name: gentle-ai-chained-pr` but the directory slug is `chained-pr`. Fixed in commit 1b2b374 — `name:` now equals `chained-pr`. Agents that use `name:` to index or identify skills (e.g. the `<available_skills>` listing) now see the correct identifier.

2. **`skill-creator/SKILL.md`** had `allowed-tools: Read, Edit, Write, ...` as a top-level frontmatter key outside the documented schema. Fixed in commit 45dc833 — the key is removed. The linter (`TestSkillFrontmatterIsLintClean`) now enforces that no unknown top-level keys appear in any SKILL.md.

The ongoing scope of this spec is the **linter guardrail** (`TestSkillFrontmatterIsLintClean` in `internal/assets/skills_frontmatter_test.go`) that prevents any of these anomalies from reappearing in future contributions.

---

## Requirements (as shipped)

### Requirement: chained-pr Name Field Alignment

The `name:` field in `internal/assets/skills/chained-pr/SKILL.md` MUST equal the directory basename `chained-pr`.

(Fixed in commit 1b2b374. Previously: `name: gentle-ai-chained-pr` — mismatched with directory slug.)

#### Scenario: Name matches directory slug

- GIVEN the file `internal/assets/skills/chained-pr/SKILL.md`
- WHEN the frontmatter is parsed
- THEN `name` equals `"chained-pr"`
- AND the value does NOT contain the prefix `gentle-ai-`

#### Scenario: Linter catches name mismatches (regression guard)

- GIVEN `TestSkillFrontmatterIsLintClean` in `internal/assets/skills_frontmatter_test.go` (commit 540d32b)
- WHEN a future contributor changes a SKILL.md `name:` to something other than the directory basename
- THEN `go test ./internal/assets/...` fails with an error citing the mismatched file
- AND the test passes only when `name` is corrected to match the directory basename

---

### Requirement: skill-creator Non-Standard Field Absence

The `allowed-tools:` top-level key MUST NOT appear in the frontmatter of any SKILL.md in the embedded skill tree. If tool information is needed, it MUST be in the skill body or under the `metadata:` sub-key.

(Fixed in commit 45dc833. Previously: `allowed-tools: Read, Edit, Write, ...` appeared as a top-level key in `skill-creator/SKILL.md`.)

#### Scenario: Frontmatter linter rejects unknown top-level keys

- GIVEN `TestSkillFrontmatterIsLintClean` running against all 21 `skills/*/SKILL.md` files
- WHEN any SKILL.md contains a top-level key outside `{name, description, license, metadata, version}`
- THEN the test fails citing the unknown key and the file path
- AND the test passes only after the unknown key is removed or moved to `metadata:`

#### Scenario: skill-creator goldens do not contain allowed-tools in frontmatter

- GIVEN the 4 skill-creator goldens regenerated in commit 45dc833
- WHEN any of `skills-claude-skill-creator.golden`, `skills-opencode-skill-creator.golden`, `skills-windsurf-skill-creator.golden`, `skills-kiro-skill-creator.golden` is read
- THEN the frontmatter block (between `---` delimiters) does NOT contain `allowed-tools:`

---

## Linter Guardrail — Ongoing Regression Prevention

The linter `TestSkillFrontmatterIsLintClean` (commit 540d32b, `internal/assets/skills_frontmatter_test.go`) is the primary ongoing artifact of this spec. It runs as part of `go test ./...` and enforces:

| Assertion | Enforces |
|-----------|---------|
| `name` present, non-empty, equals directory basename | C-1 (chained-pr alignment) |
| `description` present, non-empty, plain scalar (NOT `>` or `|`) | D (block-scalar ban) |
| `description` contains substring `Trigger:` | Trigger phrasing preservation |
| No top-level keys outside `{name, description, license, metadata, version}` | C-2 (no allowed-tools) |

> **Note**: `license` and `metadata.author` are in the allowed-keys whitelist but are NOT actively asserted as required fields by `TestSkillFrontmatterIsLintClean`. The whitelist prevents those keys from being flagged as unknown, but their presence is not enforced. Requiring them is deferred to a future linter hardening change.

This test was written RED-first (commit 540d32b) and turned GREEN by C+D commits (1b2b374, 45dc833, 31ca188). Any future SKILL.md that violates these invariants will fail CI immediately.

---

## Test Surface (post-apply state)

| Test | Covers | Status |
|------|--------|--------|
| `TestSkillFrontmatterIsLintClean` (commit 540d32b) | All 21 `skills/*/SKILL.md` | green |
| `TestGoldenSkills_Claude` (skill-creator) | `skills-claude-skill-creator.golden` | green (no allowed-tools) |
| `TestGoldenSkills_Kiro` | `skills-kiro-skill-creator.golden` | green |
| `TestGoldenSkills_OpenCode` | `skills-opencode-skill-creator.golden` | green |
| `TestGoldenSkills_Windsurf` | `skills-windsurf-skill-creator.golden` | green |
