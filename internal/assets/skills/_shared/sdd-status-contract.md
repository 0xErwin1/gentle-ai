# SDD Status and Instructions Contract

Shared OpenSpec-style contract for SDD commands and phase skills. Use this before acting on a change so orchestration does not guess state, paths, or edit scope.

## Purpose

Commands that select, continue, apply, verify, or archive an SDD change MUST first produce or consume structured status. The status is the handoff between orchestrator and phase executor.

## Change Selection

- If a change name is provided, use that exact change after confirming it exists in the selected artifact store.
- If no change name is provided, infer only when the active change is unambiguous from session state or there is exactly one active change.
- If multiple active changes match or the active change is unclear, ask the user to choose. Do not guess.
- If no active changes exist, report that no SDD change is active and suggest `/sdd-new <change>`.

## Status Schema

Return status as markdown with these fields, or as equivalent JSON when the host supports it:

```yaml
schemaName: spec-driven
changeName: <change-name>
artifactStore: engram | openspec | hybrid | none
planningHome:
  root: <project-or-openspec-root>
  changesDir: <openspec/changes or engram topic prefix>
changeRoot: <openspec/changes/<change> or engram topic prefix>
artifactPaths:
  proposal: [<path-or-topic>]
  specs: [<path-or-topic>]
  design: [<path-or-topic>]
  tasks: [<path-or-topic>]
  applyProgress: [<path-or-topic>]
  verifyReport: [<path-or-topic>]
contextFiles:
  proposal: [<concrete readable files/topics>]
  specs: [<concrete readable files/topics>]
  design: [<concrete readable files/topics>]
  tasks: [<concrete readable files/topics>]
  applyProgress: [<concrete readable files/topics>]
  verifyReport: [<concrete readable files/topics>]
artifacts:
  proposal: missing | done | partial
  specs: missing | done | partial
  design: missing | done | partial
  tasks: missing | done | partial
  applyProgress: missing | done | partial
  verifyReport: missing | done | partial
taskProgress:
  total: 0
  complete: 0
  remaining: 0
  unchecked: []
applyState: blocked | all_done | ready
dependencies:
  apply: blocked | ready | all_done
  verify: blocked | ready | all_done
  archive: blocked | ready | all_done
actionContext:
  mode: repo-local | workspace-planning
  workspaceRoot: <absolute path>
  allowedEditRoots: [<absolute paths>]
  warnings: []
nextRecommended: <command-or-action>
```

## Apply State

- `blocked`: Required apply artifacts are missing, task selection is ambiguous, or action context makes edits unsafe.
- `all_done`: Tasks artifact exists and every implementation task is checked `[x]`.
- `ready`: Tasks artifact exists, at least one implementation task remains unchecked, and edit scope is safe.

## Dependency States

- `apply` is `ready` only when specs, design, and tasks are available and task progress is not all done.
- `verify` is `ready` when tasks exist and either apply-progress exists or the tasks artifact shows all intended implementation work complete. Incomplete tasks remain blockers for full verification.
- `archive` is `ready` only when verify-report exists, has no CRITICAL issues, and tasks are complete. CRITICAL verification issues have no override. Explicit recorded exceptions are limited to non-critical partial archives or stale-checkbox reconciliation when apply-progress/verify-report prove completion.

## Action Context Guard

The orchestrator MUST carry `actionContext` into any phase launch.

- If `mode: workspace-planning` and `allowedEditRoots` is empty, stop before editing. Treat linked repos and folders as read-only planning context.
- If `allowedEditRoots` is present, only edit files within those roots.
- If a command cannot prove a file is inside the authoritative workspace or allowed edit roots, stop and ask for clarification.

## Status Output

Every command that acts on a change MUST show status before launching an executor or performing archive work:

- Active change selection and how it was resolved.
- Artifact statuses and paths/topics used as context.
- Task progress and unchecked task list when tasks exist.
- Next recommended action.
- Any actionContext or edit-root warnings.
