# Bounded Review Lifecycle Trigger Rules

## Purpose

Define plain instructional lifecycle routing that starts one explicit post-implementation review and reuses its receipt. Gentle AI installs the rules; it does not create Git hooks, daemons, event listeners, or agent processes.

## Requirements

### Requirement: Closed lifecycle event set

The catalog MUST expose `pre-commit`, `pre-push`, `pre-pr`, `release`, `post-sdd-phase`, `on-ci`, and `on-schedule`.

#### Scenario: Release is recognized

- GIVEN a release binding
- WHEN catalog validation runs
- THEN `release` is accepted as a supported event

### Requirement: Post-apply starts explicit ordinary review

The default `post-sdd-phase` binding MUST target only `apply` and route to `review-start`. It MUST start `review/start(target)` only when no valid receipt exists. Planning phases MUST NOT auto-launch ordinary 4R or Judgment Day.

#### Scenario: Design phase completes

- GIVEN SDD design completes
- WHEN default trigger rules are evaluated
- THEN no adversarial implementation review starts

#### Scenario: Apply completes without receipt

- GIVEN all apply tasks complete and no valid receipt exists
- WHEN native status is resolved
- THEN `nextRecommended` is `review`
- AND the dispatcher supplies the native `gentle-ai review-start` operation shape

### Requirement: Lifecycle gates validate one receipt

Default pre-commit, pre-push, pre-PR, and release bindings MUST route only to `review-receipt-validator`. They MUST NOT route to a lens, refuter, or Judgment Day.

#### Scenario: Pre-PR target is unchanged

- GIVEN an approved receipt matches candidate tree, paths, policy, ledger, evidence, and base relationship
- WHEN pre-PR validation runs
- THEN it returns `allow`
- AND no review operation starts

### Requirement: Deterministic validation outcomes

Rendered rules MUST define `allow`, `scope-changed`, `invalidated`, and `escalated` actions. Missing receipt after implementation/post-apply starts an explicit new review; unrelated scope change requires a new lineage; invalidation requires maintainer action; escalation stops delivery.

#### Scenario: Receipt is invalidated

- GIVEN policy or provenance changed
- WHEN validation runs
- THEN the result is `invalidated`
- AND no review budget is silently reset

### Requirement: Explicit review-start lens classification

Risk classification MUST occur only inside explicit `review/start(target)`: low risk selects zero lenses, medium selects one dominant lens, and high selects four initial 4R lenses. Generated goldens MUST be excluded from authored changed-line threshold but included in target identity.

#### Scenario: Large generated-only churn

- GIVEN authored code is below 400 changed lines and generated golden churn is large
- WHEN review-start risk is classified
- THEN generated lines do not independently trigger high risk
- AND generated files remain in the immutable target

### Requirement: One refuter operation maximum

Trigger rules MUST NOT bind `review-refuter` to lifecycle events. Full 4R MUST mean four initial lens sweeps, while inferential severe findings share one transaction-wide detached refuter operation.

#### Scenario: Full 4R findings require interpretation

- GIVEN all four lenses emit inferential severe claims
- WHEN evidence routing occurs
- THEN one refuter operation receives the complete merged list

### Requirement: Catalog-derived adapter parity

Every AgentID returned by `catalog.AllAgents()` MUST render the bounded lifecycle and review execution contracts. Generic mappings, including Pi, MUST receive the same semantics.

#### Scenario: Pi is rendered

- GIVEN `AgentPi`
- WHEN the SDD orchestrator asset is selected
- THEN the generic adapter asset is rendered with the bounded contract

### Requirement: No runtime execution infrastructure

Trigger rendering MUST remain deterministic text generation. It MUST NOT generate Git hooks, evaluate events in a daemon, or invoke agents.

#### Scenario: Rules are installed

- GIVEN an adapter sync
- WHEN trigger rules are rendered and injected
- THEN only managed configuration files change
- AND no review process launches

### Requirement: User-owned model configuration

Rendered trigger and review rules MUST state that model, provider, profile, and effort are not risk-classifier inputs and remain user-owned choices.

#### Scenario: Risk classification renders

- GIVEN any supported adapter
- WHEN its trigger rules are inspected
- THEN no model/provider/profile/effort choice is enforced
