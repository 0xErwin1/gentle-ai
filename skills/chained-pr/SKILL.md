---
name: gentle-ai-chained-pr
description: >
  Split large changes into chained or stacked pull requests that stay within
  Gentle AI's 400-line cognitive review budget. Trigger: when a PR would exceed
  400 changed lines, when planning chained PRs, stacked PRs, or reviewable slices.
license: Apache-2.0
metadata:
  author: gentleman-programming
  version: "1.0"
---

## When to Use

Use this skill when:

- A planned PR is likely to exceed **400 changed lines** (`additions + deletions`).
- A reviewer asks to split a PR for cognitive load or review fatigue.
- You need chained PRs, stacked PRs, or a feature branch with multiple reviewable slices.
- A change should be reviewed in roughly **60 minutes or less** per PR.

Do not use this skill for small fixes or single-purpose changes that fit comfortably under the review budget.

## Critical Rules

| Rule | Requirement |
|------|-------------|
| Review budget | Target **≤400 changed lines** per PR, measured as additions + deletions |
| Review time | Design each PR for an approximately **≤60 minute** human review |
| Scope | One implementation concern per PR; avoid mixing refactors, features, tests, and docs unless tightly coupled |
| Dependencies | State what each PR depends on and what follows next |
| Exceptions | Use `size:exception` only when a maintainer agrees the large diff is unavoidable |

The goal is not bureaucracy. The goal is protecting reviewer cognition so maintainers can review with care instead of skimming exhausted. Big PRs create fatigue, hide defects, and slow merge velocity.

## Choosing the Split Strategy

| Scenario | Recommended approach | Why |
|----------|----------------------|-----|
| Feature needs isolated integration before main | Feature branch chain | Keeps incomplete work away from `main` |
| Each slice can land independently | Stacked PRs to `main` | Reduces long-lived branch drift |
| API and UI are tightly coupled | Feature branch chain | Allows integration before final merge |
| Backend can ship before UI | Stacked PRs | Faster incremental value |
| Pure generated/vendor/migration diff | `size:exception` | Splitting may add noise without reducing review complexity |

## Feature Branch Chain

Use this when multiple PRs should integrate together before landing in `main`.

```text
main
 └── feat/my-feature              # integration branch
      ├── feat/my-feature-01-core # PR targets feat/my-feature
      ├── feat/my-feature-02-cli  # PR targets feat/my-feature
      └── feat/my-feature-03-docs # PR targets feat/my-feature
```

### Steps

1. Create the feature branch from `main`.
2. Open a main/tracker PR from the feature branch to `main` early and mark it as not ready to merge.
3. Create each implementation branch from the feature branch.
4. Target each chained PR back to the feature branch.
5. Merge the final feature branch to `main` only after all chained PRs are merged and tested together.

## Stacked PRs to Main

Use this when each PR can land in `main` in order.

```text
main <- PR 1: foundation
          └── PR 2: feature slice built on PR 1
                └── PR 3: docs/tests built on PR 2
```

### Steps

1. Create PR 1 from `main`.
2. Create PR 2 from PR 1's branch and target it to PR 1's branch.
3. After PR 1 merges, rebase PR 2 on `main` and retarget it to `main`.
4. Repeat until the stack is merged.

## PR Description Template

```markdown
## Chain Context

| Field | Value |
|-------|-------|
| Chain | <feature or stack name> |
| Position | <N of total> |
| Base | `<target branch>` |
| Depends on | <PR/issue/link or "None"> |
| Follow-up | <next PR or "None"> |
| Review budget | <changed lines> / 400 |

## Scope

- <What this PR includes>
- <What this PR intentionally excludes>

## Review Notes

- Review this PR in isolation.
- Do not review dependent PR changes here.
- If this exceeds 400 changed lines, explain why `size:exception` is justified.

## Test Plan

- <command or manual verification>
```

## Commands

```bash
# Check PR size before asking for review
gh pr view <PR_NUMBER> --json additions,deletions,changedFiles,title,url

# Create a chained PR targeting a feature branch
gh pr create --base feat/my-feature --title "feat(scope): focused slice" --body-file pr-body.md

# Create a stacked PR targeting the previous branch
gh pr create --base feat/my-feature-01-core --title "feat(scope): next focused slice" --body-file pr-body.md
```

## Reviewer Guidance

- If a PR exceeds 400 changed lines without `size:exception`, ask for a split.
- Recommend chained PRs when the work must integrate before `main`.
- Recommend stacked PRs when each slice can merge independently.
- Prefer clear dependency notes over clever branch gymnastics.
