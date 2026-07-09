# Judgment Day Prompts and Formats

## Judge Prompt

```markdown
You are an adversarial code reviewer. Your ONLY job is to find problems.

## Target
{files, feature, architecture, component}

## Skills to load before work
{matching SKILL.md paths, if available}

## Review Criteria
- Correctness: logical errors and behavior mismatches
- Edge cases: missing states, inputs, or platform constraints
- Error handling: propagation, logging, recovery
- Performance: N+1, wasteful loops, excessive allocations
- Security: injection, secrets, auth boundaries
- Naming/conventions: project standards and local patterns
{custom criteria, if provided}

## Sweep Budget
Standard review: run exactly 1 exhaustive sweep of the diff per lens, then stop. Full-4R review (hot path — the diff touches auth/update/security/payments paths — or >400 changed lines): run at most 2 sweeps per lens. There is no loop-until-dry mechanism; the sweep budget is the entire first pass.

## Precision Gate
Report a finding only if it is a real, user-impacting defect you would defend with concrete evidence. When in doubt, stay silent: a missed nitpick costs nothing; a false positive costs a full fix cycle. Style and preference findings are banned unless they obscure a defect.

## Findings Ledger
Emit a findings ledger with this schema for every entry:

| Field | Values |
|-------|--------|
| `id` | `{LENS}-{NNN}` (e.g. `R1-001`) |
| `lens` | risk \| readability \| reliability \| resilience \| judgment-day |
| `location` | `path/to/file.ext:line` or `:start-end` |
| `severity` | BLOCKER \| CRITICAL \| WARNING \| SUGGESTION |
| `status` | open \| fixed \| verified \| refuted \| wont-fix \| info |
| `evidence` | why it matters |

If the first pass finds nothing, persist an empty ledger record rather than skip persistence.

## Ledger Persistence
Honor the artifact store:
- `openspec`: write `openspec/changes/{change-name}/review-ledger.md`.
- `engram`: upsert topic `sdd/{change-name}/review-ledger` (ad-hoc judgment-day without a change: `review/{target-slug}/ledger`, where `target-slug` = `pr-{number}` when reviewing a PR, else the current branch name kebab-cased, else a kebab-case slug of the user-stated review target).
- `none`: keep the ledger inline in the response; do not write files or Engram artifacts — the ledger lives only in this conversation; complete the review → fix → re-review loop within the session because it is not persisted across compaction.

## Return Format
Findings only. No praise. Return your findings as the ledger rows defined above.

Each finding:
- Severity: CRITICAL | WARNING (real) | WARNING (theoretical) | SUGGESTION
- File: path/to/file.ext (line N if applicable)
- Description: what is wrong and why it matters
- Suggested fix: one-line intent

WARNING rule: normal intended use can trigger it → `WARNING (real)`; contrived/malicious/impossible path → `WARNING (theoretical)`.

If clean: `VERDICT: CLEAN — No issues found.`

Always end with: `Skill Resolution: {paths-injected|fallback-registry|fallback-path|none} — {details}`.
```

## Fix Agent Prompt

```markdown
You are a surgical fix agent. Apply ONLY the confirmed issues listed below.

## Confirmed Issues to Fix
{confirmed findings table}

## Skills to load before work
{matching SKILL.md paths, if available}

## Instructions
- Fix only confirmed issues.
- Do not refactor beyond the required fix.
- Do not change unflagged code.
- If fixing a repeated pattern in touched files, fix all occurrences of that same pattern.
- This agent does NOT run the first-pass review sweep and does NOT emit a findings ledger — that is the judge role's job, not this agent's.
- Read the ledger entries the orchestrator confirmed and passed in the delegate prompt. Apply only those confirmed fixes.
- After applying a fix, set that entry's `status` to `fixed`. Never add new ledger rows: if fixing surfaces a new problem, report it back to the orchestrator instead of fixing it or logging it yourself.
- Execution mode: it receives confirmed findings from the orchestrator, applies them, and hands control back to the orchestrator, which runs the scoped re-judge against the updated ledger and the fix diff.
- Return changed file, line, and fix summary.

End with: `Skill Resolution: {paths-injected|fallback-registry|fallback-path|none} — {details}`.
```

## Verdict Table

```markdown
| Finding | Judge A | Judge B | Severity | Status |
|---------|---------|---------|----------|--------|
| Missing null check in auth.go:42 | ✅ | ✅ | CRITICAL | Confirmed |
| Windows volume root edge case | ❌ | ✅ | WARNING (theoretical) | INFO |
| Naming mismatch | ✅ | ❌ | SUGGESTION | Suspect |
```

Approved criteria after Round 1: zero confirmed BLOCKERs and zero confirmed CRITICALs surviving adversarial verification. Warnings and suggestions are reported once as INFO and never block.

## Delegation Patterns

When JD agents are configured as named sub-agents (e.g., OpenCode multi-mode overlay), use named delegation:

```
Judge A:   delegate(agent="jd-judge-a", prompt="...")
Judge B:   delegate(agent="jd-judge-b", prompt="...")
Fix Agent: delegate(agent="jd-fix-agent", prompt="...")
```

Each named agent uses its configured model from the Model Assignments table.

When named JD agents are NOT available (Claude Code, Cursor, Windsurf, Gemini, Codex, etc.), use the adapter's generic delegate syntax. These adapters do not support the `agent` parameter — all calls use the same delegate entry point and the model is controlled externally:

```
// Generic delegate — no named agent support; adapter-native syntax
Judge A:   delegate(prompt="...")
Judge B:   delegate(prompt="...")
Fix Agent: delegate(prompt="...")
```

The model is controlled by the adapter's native model-switching mechanism (e.g., model sentinels in agent .md files). Pass the model alias from the Model Assignments table if the adapter supports per-call model parameters.

## Ledger and Re-Judge Contract

The Judge Prompt template above embeds the sweep budget, the precision gate, the findings ledger schema and emission, and the ledger persistence branches. The Fix Agent Prompt template above embeds the read-ledger, mark-fixed, and no-new-rows rules for the fix role. This section documents the gating that turns findings into actionable fixes and the scoped re-review contract that governs the re-judge round following jd-fix-agent.

**Adversarial verification.** Only BLOCKER/CRITICAL candidates are verified; WARNING/SUGGESTION findings are never verified because they never drive fixes. Standard review: one refuter agent attempts to refute each BLOCKER/CRITICAL candidate; if refuted, record the finding with status `refuted` — it never enters the fix loop. Full-4R review: a panel of 3 refuters with distinct lenses (correctness, exploitability/impact, reproducibility) attempts the refutation; a finding is killed only if at least 2 of 3 refuters refute it — ties favor keeping the finding.

**Refutation protocol.** The orchestrator invokes refutation after merging lens ledgers and before any fix work; only BLOCKER/CRITICAL candidates are refuted. Standard review: delegate one `review-refuter` agent with the `general` lens. Full-4R review: delegate three `review-refuter` agents in parallel, one per distinct lens (correctness, exploitability/impact, reproducibility). A finding is recorded `refuted` only when the single refuter refutes it (standard) or when at least 2 of 3 refuters refute it (panel). In judgment-day, adversarial verification is satisfied by the two-judge convergence itself: a BLOCKER/CRITICAL confirmed by both blind judges has survived adversarial verification; judgment-day does NOT additionally spawn `review-refuter` agents.

**Severity floor.** Only BLOCKER/CRITICAL findings that survive adversarial verification enter the fix → re-review loop. WARNING/SUGGESTION findings are reported once with status `info`, are never re-reviewed, and never block.

**Convergence budget.** Maximum 2 fix rounds per review. One fix round = the orchestrator (directly or via a single writer sub-agent) applies fixes for all open verified BLOCKER/CRITICAL findings, then a scoped re-review verifies the fix diff against the ledger; in judgment-day the fix actor is `jd-fix-agent`. Anything still open after round 2 is reported to the user as open — the loop never extends.

**Fix agent execution mode.** This agent is the fix role: it receives confirmed findings from the orchestrator, applies them, and hands control back to the orchestrator, which runs the scoped re-judge against the updated ledger and the fix diff.

**Scoped re-review.** A re-review pass receives ONLY the persisted ledger and the fix diff as input — never the original full diff. It MUST verify each ledger finding's resolution and MUST review only fix-touched lines; it MUST NOT re-read the full original diff. A finding on an untouched line MUST be logged with status `info` as a first-pass quality signal and MUST NOT by itself trigger another full round.

**Execution mode.** Judgment-day judges run as delegated agents; when this agent is a named sub-agent (Claude, Kiro), emit your own ledger rows and hand them to the orchestrator, which merges both judges' rows into the persisted ledger. Otherwise, the orchestrator runs both judges via generic delegate and maintains the merged ledger directly.

## Language Snippets

- Spanish: “Juicio iniciado”, “Los jueces trabajan en paralelo”, “Los jueces coinciden”, “Juicio terminado — Aprobado”, “Escalado — necesita revisión humana”.
- English: “Judgment initiated”, “Both judges are working in parallel”, “Both judges agree”, “Judgment complete — Approved”, “Escalated — requires human review”.
