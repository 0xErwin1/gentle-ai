# Declarative Configuration Reference

Every field of the `config/v1` document, what it accepts, and what an omitted value means. For the guided introduction see [declarative-config.md](declarative-config.md).

Two rules run through the whole contract:

- **Omitted is not empty.** A field the document leaves out is unresolved — the flag, the environment or the machine's own setting keeps its turn. A field the document sets is a decision.
- **Unknown is rejected.** An unrecognised field name, value or reference fails validation with a diagnostic instead of being ignored.

## Document

| Field | Type | Meaning |
|-------|------|---------|
| `version` | string | Schema version. `v1` is current; `v0` is accepted and migrated. Anything else is rejected. |
| `selection` | object | What the installation should be. Every field below lives here. |
| `roles` | array of [role](#roles) | Logical agent roles. |
| `extensions` | object keyed by provider | Configuration the neutral contract does not model, merged verbatim into that provider's settings. |

## What to configure

| Field | Type | Omitted means |
|-------|------|---------------|
| `agents` | array of [provider id](#providers) | Nothing is configured. At least one is required in practice. |
| `components` | array of [component id](#components) | The preset decides. |
| `skills` | array of [skill id](#skills) | Every skill the preset carries, which for the default preset is all of them. |
| `skillExclusions` | array of skill id | Nothing is removed. Applies to whatever `skills` resolved to, so dropping one skill never means restating the rest. |
| `skillAssignments` | object: provider id → array of skill id | Every provider takes the flat `skills` list. A provider named here takes its own list instead; the others are unaffected. |
| `persona` | `gentleman` \| `neutral` | The default persona. |
| `preset` | `full-gentleman` \| `ecosystem-only` \| `minimal` \| `custom` | Treated as full. |
| `communityTools` | array of `codegraph` | None. |
| `openCodePlugins` | array of `sub-agent-statusline` \| `sdd-engram-plugin` \| `gentle-logo` | None. |

### Providers

`claude-code`, `opencode`, `codex`, `kilocode`, `gemini-cli`, `cursor`, `vscode-copilot`, `antigravity`, `windsurf`, `kimi`, `qwen-code`, `kiro-ide`, `openclaw`, `pi`, `trae-ide`, `hermes`.

Providers differ in what they can express. Two differences are reported rather than worked around:

| Capability | Providers | Otherwise |
|------------|-----------|-----------|
| Agent roles | any that keeps agents as files, plus `opencode` | `config.role.unsupported-adapter` rejects a document declaring `roles` |
| Permission rule lists | `claude-code`, `gemini-cli`, `qwen-code`, `vscode-copilot` | `config.permissions.unsupported-adapter` warns; the rules still reach every provider that reads them |

### Components

| Id | Writes | Also installs |
|----|--------|---------------|
| `skills` | Skill trees per provider | |
| `persona` | Persona guidance | |
| `permissions` | Shipped guardrails | |
| `sdd` | SDD agents, commands, skills and prompts | |
| `context7` | Context7 MCP configuration | |
| `theme`, `claude-theme` | Theme settings | |
| `opencode-gentle-logo` | The TUI logo plugin | |
| `engram` | MCP server, plugin and protocol section | the `engram` binary |
| `gga` | GGA configuration | the `gga` tool |

The last two both write configuration and install something. `config render` produces their configuration; the installation is reported as `pendingProvisioning`, because rendering writes bytes and installing is an action.

### Skills

`sdd-init`, `sdd-explore`, `sdd-propose`, `sdd-spec`, `sdd-design`, `sdd-tasks`, `sdd-apply`, `sdd-verify`, `sdd-archive`, `sdd-onboard`, `go-testing`, `gentle-ai-bench`, `skill-creator`, `skill-improver`, `judgment-day`, `branch-pr`, `issue-creation`, `skill-registry`, `chained-pr`, `cognitive-doc-design`, `comment-writer`, `work-unit-commits`, `rdd-defect-workflow`, `systemic-issue-triage`.

## Workflow

| Field | Type | Omitted means |
|-------|------|---------------|
| `sddMode` | `single` \| `multi` | The default mode. |
| `strictTDD` | boolean | Not enforced. |
| `sddProfileStrategy` | `generated-multi` \| `external-single-active` | Gentle AI detects it. OpenCode only. |
| `profiles` | array of [profile](#profiles) | No named profiles. OpenCode only. |
| `rddMode` | `on` \| `off` | The machine's own review setting stands. Declaring it is opting into managing a user-owned choice. |
| `backgroundIntent` | `auto` \| `on` \| `off` | Unresolved: the flag, the environment and the prior managed choice keep their turn. |
| `piBackgroundIntent` | `auto` \| `on` \| `off` | The same, for Pi. |

### Profiles

A named model configuration that generates its own orchestrator and phase agents beside the default set.

| Field | Type |
|-------|------|
| `name` | string, required |
| `orchestrator` | [model assignment](#model-assignment) |
| `phaseAssignments` | object: phase → model assignment |

## Models

| Field | Type | Applies to |
|-------|------|------------|
| `modelPresets` | object: provider id → profile name | A named tier per provider. Resolved through Gentle AI's own matrix, so a retuned profile stays the profile you asked for. An explicit assignment below still wins. |
| `modelAssignments` | object: phase → [model assignment](#model-assignment) | OpenCode |
| `claudeModelAssignments` | object: phase → `fable` \| `opus` \| `sonnet` \| `haiku` | Claude |
| `claudePhaseAssignments` | object: phase → `{model, effort}` | Claude, where a phase needs both |
| `kiroModelAssignments` | object: phase → `auto` \| `opus` \| `sonnet` \| `haiku` \| `minimax` \| `glm` \| `deepseek` \| `qwen` | Kiro |
| `codexModelAssignments` | object: phase → `low` \| `medium` \| `high` \| `xhigh` | Codex |
| `codexCarrilModelAssignments` | object: carril → model id | Codex |
| `codexPhaseModelAssignments` | object: phase → model id | Codex |
| `codexOrchestrator` | `{model, effort}` | Codex main session |

### Named profiles per provider

| Provider | Profiles |
|----------|----------|
| `codex` | `low-cost`, `recommended`, `powerful` |
| `claude-code` | `balanced`, `performance`, `economy`, `diversity` |
| `kiro-ide` | `balanced`, `performance`, `economy`, `open-weight` |

Any other provider is rejected with `config.model-preset.unsupported-provider`. OpenCode has none by design: it discovers which providers and models your subscription actually grants and you assign from those.

### Model assignment

| Field | Type |
|-------|------|
| `provider` | string, required |
| `model` | string, required |
| `effort` | string, where the provider expresses one |

## Roles

A role's `id` is its logical identity and never appears in generated output. Other roles reference the id, so renaming is a single edit.

| Field | Type | Omitted means |
|-------|------|---------------|
| `id` | string, required | — |
| `renderedName` | string | Rendered under its id. |
| `references` | array of role id | Delegates to nothing. |
| `description` | string | The client shows none. |
| `prompt` | string | The client uses its default. |
| `tools` | array of string | The client's default toolset. |
| `model` | [model assignment](#model-assignment) | The client's default model. |
| `mode` | `primary` \| `subagent` | The client decides. |
| `hidden` | boolean | The client decides. |

A role whose `renderedName` matches an agent a selected component generates is rejected: two different agents asking for one name.

## Other

| Field | Type | Omitted means |
|-------|------|---------------|
| `permissions` | `{allow, deny, ask}`, each an array of rule strings | No rules beyond the shipped guardrails. Declared rules are unioned with them, never replacing them. |
| `mcpServers` | object: name → [MCP server](#mcp-server) | Only what the components configure. |
| `scope` | `global` \| `workspace` | The flag and environment keep their turn. |
| `channel` | `stable` \| `beta` | The same. |

### MCP server

| Field | Type |
|-------|------|
| `command` | string — a local server. Mutually exclusive with `url`. |
| `args` | array of string |
| `env` | object: name → value |
| `url` | string — a remote server. Mutually exclusive with `command`. |
| `enabled` | boolean |

## Diagnostics

Every diagnostic carries a `code`, the JSON `path` it applies to, a `severity` and a message naming the resolution.

| Severity | Effect |
|----------|--------|
| `error` | The document is rejected and the command exits non-zero. |
| `warning` | The document is delivered; the message reports what one provider could not take. |

| Code | Cause |
|------|-------|
| `config.document.malformed` | The file is not valid JSON. |
| `config.document.unknown-field` | A field name the schema does not define. |
| `config.version.unsupported` | A schema version this binary cannot interpret. |
| `config.agent.unsupported` | A provider id that does not exist. |
| `config.component.unsupported` | A component id that does not exist. |
| `config.skill.unsupported` | A skill id that does not exist, in `skills` or `skillExclusions`. |
| `config.community-tool.unsupported`, `config.opencode-plugin.unsupported` | The same, for those lists. |
| `config.role.invalid`, `config.role.duplicate` | A role with no id, or two roles sharing one. |
| `config.role.reference.unresolved` | A `references` entry naming a role the document does not declare. |
| `config.role.model.incomplete` | A role model missing its provider or its model. |
| `config.role.mode.unsupported` | A mode other than `primary` or `subagent`. |
| `config.role.unsupported-adapter` | A declared provider expresses no agent roles. |
| `config.skill-assignment.undeclared-adapter` | A skill assignment naming a provider the document does not declare. |
| `config.extension.undeclared-provider` | An extension naming a provider the document does not declare. |
| `config.model-preset.unsupported-provider` | A provider that offers no named profiles. |
| `config.model-preset.unsupported` | A profile name that provider does not offer. |
| `config.permissions.unsupported-adapter` | *Warning.* A provider that does not read permissions as rule lists. |
| `config.flags.exclusive` | `--config` combined with a semantic selection flag. |
| `config.export.loss.*` | Export could not represent a value; the message names what to do instead. |
| `render.ownership.conflict` | An unmanaged resource occupies a path the document wants. |

## Next step

Validate a document against this reference without touching anything:

```console
gentle-ai config validate --config gentle-ai.json
```
