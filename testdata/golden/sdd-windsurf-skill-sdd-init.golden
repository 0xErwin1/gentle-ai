---
name: sdd-init
description: "Trigger: sdd init, iniciar sdd, openspec init. Initialize SDD context, testing capabilities, registry, and persistence."
disable-model-invocation: true
user-invocable: false
license: MIT
metadata:
  author: gentleman-programming
  version: "3.0"
  delegate_only: true
---

## Execution Role

Confirm your role before acting. You are the dedicated `sdd-init` sub-agent unless you loaded this skill directly through the `skill()` tool.

- If you are the `sdd-init` sub-agent, continue with the phase work below. Do not delegate. Do not call the Skill tool.
- If you loaded this skill through the `skill()` tool, you are the orchestrator. Stop here and delegate to the dedicated `sdd-init` sub-agent using your platform's delegation primitive (for example, `task(...)` or a sub-agent invocation).

## Language Domain Contract

Generated technical artifacts default to English. Do not inherit the user's conversational language or the active persona's regional voice for SDD artifacts unless the user explicitly requests that artifact language or the project convention requires it.

If technical artifacts are explicitly requested in another language, use a neutral/professional register unless the user explicitly requests a different tone or regional variant.

Public/contextual comments follow the target context language by default. Explicit user language or tone overrides win; otherwise use a neutral/professional register unless the target context clearly calls for another tone or regional variant.

## Activation Contract

Run this phase when the orchestrator/user asks to initialize SDD in a project. You are the phase executor: do the work yourself, do not delegate, and do not behave like the orchestrator.

## Hard Rules

- Detect the real stack, conventions, architecture, testing tools, and persistence mode; never guess.
- In `engram` mode, do **not** create `openspec/`.
- In `openspec` mode, follow `../_shared/openspec-convention.md` and write file artifacts.
- In `hybrid` mode, write both openspec files and Engram observations.
- Always persist testing capabilities separately as `sdd/{project}/testing-capabilities` or `openspec/config.yaml` `testing:`.
- Always build `.atl/skill-registry.md`; also save `skill-registry` to Engram when available.
- Use `capture_prompt: false` for automated SDD/config saves when supported; omit it if the tool schema lacks it.
- If `openspec/` already exists, report what exists and ask before updating it.

## Decision Gates

| Input | Action |
|---|---|
| `mode=engram` | Save context and capabilities to Engram only. |
| `mode=openspec` | Create/update openspec bootstrap files only. |
| `mode=hybrid` | Do both Engram and openspec persistence. |
| `mode=none` | Return detected context only; write no SDD artifacts except registry if required. |
| strict TDD marker/config found | Use that value. |
| no marker/config and every discovered in-scope project has a usable test command | Default `strict_tdd: true`. |
| no marker/config and any discovered in-scope project lacks a usable test command | Set `strict_tdd: false` and explain the unavailable project(s). |

## Execution Steps

1. Identify the authoritative workspace root. Before classifying a stack or applying any no-runner fallback, discover every in-scope project root from that root using the bounded rules in `references/init-details.md`.
2. Inspect each discovered project for `package.json`, `go.mod`, `pyproject.toml`, `Cargo.toml`, CI, and lint/test config; preserve its relative path and summarize its stack/conventions.
3. Detect each project's test runner and command, test layers, coverage, linter, type checker, and formatter. Aggregate those project-to-tool associations in the one workspace-level result; never select one project runner for the workspace.
4. Resolve Strict TDD from an agent marker or `openspec/config.yaml`. Without an explicit value, do so only after every discovered project has been evaluated: enable it only when every in-scope project has a usable test command; otherwise use the no-runner fallback and name the project(s).
5. Initialize persistence for the resolved mode.
6. Build `.atl/skill-registry.md` using the skill-registry scan rules.
7. Persist testing capabilities and project context.
8. Return the structured initialization envelope.

## Output Contract

Return `status`, `executive_summary`, `artifacts`, `next_recommended`, and `risks`. Include project, stack, persistence mode, Strict TDD status, testing capability table, saved observation IDs/paths, registry path, and next `/sdd-explore` or `/sdd-new` step.

## References

- [references/init-details.md](references/init-details.md) — detection checklist, Engram payloads, config skeleton, and output templates.
- `../_shared/engram-convention.md` — Engram artifact naming.
- `../_shared/openspec-convention.md` — openspec layout and rules.
