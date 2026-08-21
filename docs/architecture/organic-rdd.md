# Organic RDD — atomic review architecture

← [Back to README](../../README.md)

Receipt-Driven Development (RDD) reviews a finished candidate without taking ownership of delivery. It is deliberately small: native code freezes one worktree candidate, coordinates bounded review, burns completed authority, and returns control to the human.

## The model

- **Review follows work.** A candidate exists before review begins; the parent asks native STATUS to preflight that current worktree only.
- **Native owns review mechanics.** Go derives risk, frozen trees, lenses, provider bindings, admission, refutation, one bounded correction, repository evidence, and targeted validation.
- **Humans own delivery.** Approval never commits, pushes, opens a PR, or overrides repository policy.

## Atomic transaction lifecycle

**The switch is a switch, and it starts off.** RDD is opt-in: until someone runs
`gentle-ai review mode enable --scope global`, it does not govern the candidate.
Nothing blocks or gates delivery; ordinary repository policy applies. `gentle-ai
review mode disable` returns to that state. Enabling RDD revalidates the current
candidate instead of resuming stale obligations.

```text
selectorless STATUS -> exact START -> bound collection/finalize -> approved + burn -> commit question
```

### Preflight and START

Selectorless STATUS does not scan or resume ambient authority. It preflights the current worktree candidate and returns one exact START invocation. START creates one compact transaction whose lineage, worktree, and target are explicit and immutable.

The parent retains the lineage, revision, and target returned by START. Every subsequent STATUS, capture, and FINALIZE call uses those exact tokens. An exact active START replay can report `replayed`; a genuinely new START is independent. A burned lineage is never reused.

This prevents a historical authority, a sibling worktree, or a stale lifecycle response from steering the current candidate.

### Cross-repository root continuity

A session rooted in repository A can review an explicitly user-authorized nested target in unrelated repository B. Go resolves the requested path to B's canonical worktree root; adapters remain opaque and never parse authorization or roots. Once B is selected, the host retains B through STATUS, consent, collection, correction, validation, FINALIZE, burn, and the commit question. Provider-issued tokens remain exact; an invocation without `--cwd` runs with process cwd B.

Opaque `repository_context` can materialize or capture from process cwd A, but remains bound to B. Identical lineage text in A and B names independent authority: approval burns B only and leaves A unchanged. The commit question names B, and any selected commit runs in B only without pushing.

Only Claude Code, Codex, OpenCode, and Pi receive this lifecycle. Unsupported runtimes fail before repository or authority mutation.

### Review and finalization

Reviewers receive provider-issued immutable context, not live workspace state. Adapters are opaque transport: they do not parse bindings, build prompts, admit findings, or decide workflow state. Only candidate-caused severe findings can block. Native review permits one bounded correction and only a validator that can inspect the frozen trees may return a verdict.

Successful FINALIZE reads terminal state back and burns the exact authority and its artifacts before it returns `approved`. No terminal receipt, tombstone, witness, mirror, or delivery authority remains. Unrelated transactions remain untouched.

An ambiguous FINALIZE or burn is not approval. The parent queries exact-lineage STATUS only if that authority still exists, then follows the returned action. It never falls back to ambient recovery or invents another lineage.

## Informational gates

`review validate` and its named gates are compatibility/informational commands. They do not inspect authority, choose a lineage, allow, approve, or block a delivery.

| RDD mode | Result |
| --- | --- |
| Enabled | `invalidated/unmanaged` |
| Disabled | `disabled/unmanaged` |

Ordinary repository policy remains the delivery mechanism.

## The post-approval boundary

Immediately after terminal approval and burn, before another edit or START, the parent presents one native single-select question and stops. When B was selected from A, the question names B and any selected commit runs in B only:

1. **Commit approved changes (Recommended)** — explicit permission to create a conventional commit for the approved work. It does not push. The next default START compares later changes against the new `HEAD`.
2. **Continue uncommitted** — retain the workspace. The next default START reviews the full outstanding delta again, including the already approved bytes.

Push and pull requests always require their own explicit human decision. Approval is evidence about the completed review transaction, not delivery authority.

## Runtime boundary

The atomic lifecycle is rendered only for Claude Code, OpenCode, Codex, and Pi. Generic and non-RDD runtime guidance keeps ordinary SDD behavior and makes no review-transport promise. Pi receives the lifecycle dynamically over the generic base only when its compiled capability advertises the provider contract.

## Historical compatibility

Older contracts and historical artifacts may be read through explicit manual compatibility operations. They do not participate in the ordinary atomic lifecycle, restore burned authority, or decide delivery.
