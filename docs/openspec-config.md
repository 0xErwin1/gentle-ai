# OpenSpec Config for SDD

`openspec/config.yaml` is a supported project-level SDD convention in `gentle-ai` when working in `openspec` or `hybrid` persistence modes.

## What This File Can Customize

This file is used by the SDD skills as shared project context and as a place to declare phase-specific rules.

`openspec/config.yaml` can be used to customize SDD behavior by project conventions, specifically:

- project context reused across phases
- strict TDD enablement
- phase-specific writing rules for proposal, specs, design, tasks, and archive
- verification command overrides
- cached testing capabilities for apply/verify flows

## Which Phases Read It

The following SDD phases reference `openspec/config.yaml` today:

| Phase | How it uses the config |
|-------|-------------------------|
| `sdd-init` | Creates the file in OpenSpec mode, reads `strict_tdd`, writes detected `context`, `rules`, and `testing` sections. |
| `sdd-explore` | Reads it as part of project context discovery. |
| `sdd-propose` | Applies `rules.proposal`. |
| `sdd-design` | Applies `rules.design`. |
| `sdd-spec` | Applies `rules.specs`. |
| `sdd-tasks` | Applies `rules.tasks`. |
| `sdd-apply` | Applies `rules.apply`. |
| `sdd-verify` | Applies `rules.verify`. |
| `sdd-archive` | Applies `rules.archive`. |

## Schema Example

The repository currently shows this effective top-level structure:

```yaml
schema: spec-driven

context: |
  Tech stack: ...
  Architecture: ...
  Testing: ...
  Style: ...

strict_tdd: true

rules:
  proposal:
    - Include rollback plan for risky changes
  specs:
    - Use Given/When/Then for scenarios
  design:
    - Document architecture decisions with rationale
  tasks:
    - Keep tasks completable in one session
  apply:
    - Follow existing code patterns
  verify:
    test_command: ""
    build_command: ""
    coverage_threshold: 0
  archive:
    - Warn before merging destructive deltas

testing:
  strict_tdd: true
  detected: "YYYY-MM-DD"
  runner:
    command: "go test ./..."
    framework: "Go standard testing"
```

## Field Reference

### `schema`

- Expected value in examples: `spec-driven`
- Purpose: identifies the file as an SDD/OpenSpec config.

### `context`

- Type: multiline string
- Purpose: cached project context for later SDD phases.
- Typical contents: stack, architecture, testing, style, and other project conventions.

### `strict_tdd`

- Type: boolean
- Used by: `sdd-init`, orchestrator prompts, `sdd-apply`, `sdd-verify`
- Purpose: enables or disables strict TDD behavior when testing support exists.

### `rules`

- Type: phase-keyed map
- Purpose: attach project conventions to each SDD phase.
- Known phase keys:
  - `proposal`
  - `specs`
  - `design`
  - `tasks`
  - `apply`
  - `verify`
  - `archive`

### `testing`

- Type: structured object
- Written by: `sdd-init`
- Read by: `sdd-apply`, `sdd-verify`
- Purpose: cache detected testing capabilities so phases do not have to rediscover them every time.

## Current Type Caveat

There is an important inconsistency in the current examples and skill references:

- Several skills describe `rules.<phase>` as a list of textual instructions.
- `sdd-apply/strict-tdd.md` refers to `rules.apply.test_command`.
- `sdd-verify` refers to `rules.verify.test_command`, `rules.verify.build_command`, and `rules.verify.coverage_threshold`.

That means `rules.apply` and `rules.verify` are currently treated as if they may contain structured keys, while examples elsewhere also show phase rules as plain lists.