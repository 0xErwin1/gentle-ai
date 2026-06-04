---
description: Show structured SDD status for an active change
agent: gentle-orchestrator
---

You are the `gentle-orchestrator`. This command is read-only. Do not launch SDD executors and do not edit files.

HARD GATE:

SDD Session Preflight must already be complete for this session. It must include execution mode, artifact store, chained PR strategy, and review budget. If missing, ask the exact orchestrator preflight prompt and STOP. Do not inspect status in the same turn.

CONTEXT:

- Working directory: before doing anything else, run `git rev-parse --show-toplevel 2>/dev/null || pwd` with your bash tool and use the returned path as the authoritative workspace.
- Current project: the `basename` of the detected workspace above.
- Change name: $ARGUMENTS

TASK:

1. Read `skills/_shared/sdd-status-contract.md` and follow it.
2. Resolve the active change:
   - If `$ARGUMENTS` is provided, validate that exact change in the selected artifact store.
   - If omitted and exactly one active change exists, select it and say how it was selected.
   - If omitted or ambiguous with multiple active changes, ask the user to choose and STOP. Do not guess.
3. Inspect the selected artifact store from session preflight. Do not hardcode Engram.
4. Return structured status with:
   - Active change selection and schemaName.
   - planningHome, changeRoot, artifactPaths, and contextFiles.
   - Artifact statuses for proposal, specs, design, tasks, apply-progress, and verify-report.
   - Task progress: total, complete, remaining, unchecked task list.
   - Dependency states for apply, verify, and archive.
   - Next recommended action.
   - actionContext mode, allowed edit roots, and edit-root warnings.

READ-ONLY RULES:

- Do not create, update, or delete artifacts.
- Do not mark tasks complete.
- Do not launch apply, verify, archive, or continue.
- If status cannot be resolved safely, return `status: blocked` with the missing information.
