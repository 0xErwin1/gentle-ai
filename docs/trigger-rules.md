# Organic Implementation Trigger Rules

<- [Back to README](../README.md)

Ask for the outcome. Gentle AI keeps already-understood work inline, delegates only
the actions that benefit from fresh context, and offers SDD only when durable
planning would materially reduce uncertainty. Verification, review, delivery, and
lifecycle authority remain native provider responsibilities behind that simple
interaction.

## Quick path

1. Describe the outcome in natural language.
2. Gentle AI uses the smallest useful implementation route: direct inline,
   delegated direct, or an optional SDD proposal.
3. The normal interaction reports only **Working**, **Checking**, **Ready**, or
   **Needs your decision**.

The user does not choose review internals, hashes, receipts, or lifecycle
transitions. A question is necessary only when the answer changes requested
scope, destructive or irreversible impact, permission or security exposure,
verification cost or external side effects, accepted residual risk, or delivery.

## Implementation routes

| Route | Use it when | What happens |
|---|---|---|
| **Direct inline** | Deciding or verifying requires **1–3 files**; or the change is **one mechanical, already-understood file** with no research or unresolved design decision. | Keep the bounded action inline. |
| **Delegated direct** | Understanding requires **4+ files**; reading prepares a write; broad research is needed; or a writer must change **2+ non-trivial files**. | Delegate the narrow exploration and/or one writer needed for that action. |
| **Optional SDD** | The work has substantial ambiguity, or durable proposal, spec, design, or task artifacts would materially reduce uncertainty. | Propose SDD. Select it only after an explicit request or an accepted proposal. |

The file counts describe the context needed for the current action, not a risk
score and not an SDD threshold. Risk may strengthen native verification or
review, but it never forces SDD.

Delegation also applies per action. Tests, builds, installs, and native review
actors may use fresh workers without changing the implementation route or
creating an SDD run. Direct and delegated work create no SDD artifacts, phase
attempts, or synthetic SDD lifecycle.

If apparently simple work reveals substantial ambiguity, Gentle AI may offer SDD
at the next safe boundary. Declining it leads to a safely reduced scope, a
justified direct or delegated route, or **Needs your decision**—never silent SDD
enrollment.

## Native progress and authority

| Public state | Meaning |
|---|---|
| **Working** | The implementation can still change. |
| **Checking** | Gentle AI is performing the applicable functional proof and bounded review. |
| **Ready** | The exact candidate has sufficient evidence for the selected delivery route. |
| **Needs your decision** | Safe automatic convergence is impossible; Gentle AI presents the cause, impact, and concrete choices. |

Managed adapters read common-work status with exactly:

```bash
gentle-ai work-status --cwd <repo> --work-run <id> --contract gentle-ai.work-status/v1 --json
```

Status returns zero or one provider-issued `authorizedTransition`. When one is
present, the adapter may apply only that exact authorization and revision:

```bash
gentle-ai work-transition apply --cwd <repo> --work-run <id> --contract gentle-ai.work-transition/v1 --authorization-ref <ref> --expected-revision <revision> --json
```

`work-transition apply` is the only common-work mutation surface. Adapters do not
invent alternate flags, select review lenses, reconstruct recovery policy, infer
success from prose, or retry stale, expired, mismatched, or replayed
authorizations. Existing SDD v1 runs continue through their SDD-specific status
contract; direct and delegated runs do not create or consume an SDD run.

## Fail-closed activation

`GENTLE_AI_WORK_ROUTING_MODE` is the single operator-owned kill switch:

| Value | Effect |
|---|---|
| Unset or `read_only` | Keep the native common-work capability dormant, return status without an authorized mutation, and reject apply. |
| `enabled` | Explicitly enable status/apply for already owner-provisioned common work. It does not create or admit a new work run. |
| `recovery_only` | Keep capability advertisement dormant and expose only recovery-safe continuation. |
| `disabled` | Keep capability advertisement dormant and reject common-work use. |
| Empty or unknown | Resolve to disabled with a typed invalid-mode error. |

Unavailable, disabled, unknown, or read-only authority never becomes local
adapter policy. The adapter remains read-only and surfaces the typed stop instead
of inventing a transition. The provider-owned outcome intake and start surface
exists, but the canonical capability remains dormant until a normal runtime
consumer composes it with authenticated intake, semantic-evaluation, live-policy,
and PAD delivery authorities.

## Installation and refresh

`gentle-ai install` and `gentle-ai sync` project the same canonical rules into
every supported adapter:

- Standard adapters receive the managed `trigger-rules` marker in their
  adapter-owned system-prompt file.
- OpenCode and Kilocode receive it inside
  `agent.gentle-orchestrator.prompt` in their adapter-owned `opencode.json`.
- Kimi receives `~/.kimi/trigger-rules.md`, included by `KIMI.md`.

```bash
gentle-ai install   # full install
gentle-ai sync      # refresh managed content
```

Refresh is idempotent: the managed projection is replaced without duplication.

## Source of truth

The rendered projection comes from
[`internal/components/sdd/triggerrules.go`](../internal/components/sdd/triggerrules.go).
Canonical route facts come from
[`internal/agents/capabilitymanifest/manifest.go`](../internal/agents/capabilitymanifest/manifest.go).
The complete authority and recovery rationale is documented in the
[Organic Recovery implementation plan](audits/2026-07-23-organic-recovery-implementation-plan.md).
