# Pi Agent

← [Back to README](../README.md)

Pi support installs the Gentleman harness as Pi packages, then lets Pi own its own persona, models, SDD agents, chains, and memory wiring.

## Quick Start

1. Install Pi and make sure `pi` is available on `PATH`.
2. Install the Pi support stack from Gentle AI:

```bash
gentle-ai install --agent pi
```

3. Start Pi in your project:

```bash
pi
```

Gentle AI detects the `pi` binary first. If Pi is the only selected agent, the installer skips persona, ecosystem component selection, and Strict TDD prompts because `gentle-pi` owns those choices inside Pi.

## Installed Packages

Gentle AI runs exactly these Pi package installs:

```bash
pi install npm:gentle-pi
pi install npm:gentle-engram
pi install npm:pi-subagents
pi install npm:pi-intercom
```

| Package | What it adds |
|---------|--------------|
| [`gentle-pi`](https://www.npmjs.com/package/gentle-pi) | Gentleman persona, SDD/OpenSpec workflow, strict TDD support, safety policy, skills, prompts, SDD agents, and SDD chains. |
| [`gentle-engram`](https://pi.dev/packages/gentle-engram) | Engram session memory and MCP tools for Pi, with safe degradation when `engram` is missing. |
| `pi-subagents` | Runs SDD agents discovered from `.pi/agents/`. |
| `pi-intercom` | Lets child agents ask the parent Pi session for decisions while chains run. |

## Pi Commands

Run these inside Pi after installing the package stack.

| Command | What it does |
|---------|--------------|
| `/gentle-ai:status` | Shows package, SDD asset, OpenSpec, and model config status. |
| `/gentleman:persona` | Switches between `gentleman` and `neutral` personas. |
| `/gentle-ai:persona` | Compatibility alias for `/gentleman:persona`. |
| `/gentleman:models` | Opens the Pi-native model assignment modal. |
| `/gentle-ai:models` | Compatibility alias for `/gentleman:models`. |
| `/sdd-init` | Bootstraps or refreshes `openspec/config.yaml`. |
| `/gentle-ai:install-sdd` | Reinstalls SDD assets without overwriting local files. |
| `/gentle-ai:install-sdd --force` | Force-refreshes installed SDD assets. Use this when you explicitly want package assets to replace local copies. |

## Persona Selection

Pi persona selection belongs to `gentle-pi`, not the Gentle AI installer.

```text
/gentleman:persona
```

| Persona | Behavior |
|---------|----------|
| `gentleman` | Teaching-oriented senior architect persona with Rioplatense Spanish/voseo when the user writes Spanish. |
| `neutral` | Same senior architect discipline and teaching philosophy, but with warm professional language and no regional expressions. |

The selection is saved at:

```text
.pi/gentle-ai/persona.json
```

Run `/reload` or start a new Pi session after switching if the current session already injected the previous persona.

## Model Assignments

Pi model assignment belongs to `gentle-pi`, not the Gentle AI installer.

```text
/gentleman:models
```

The modal discovers project, user, and built-in agents. SDD agents are shown first so you can tune the phases that matter most.

| Agent kind | Recommended model shape |
|------------|-------------------------|
| Exploration, proposal, archive | Fast and cheap is usually enough. |
| Spec, design, tasks | Strong reasoning model, because these phases shape implementation. |
| Apply | Strong coding model with reliable tool use. |
| Verify / review agents | Strong fresh-context model. Verification benefits from independence. |
| Tiny utility agents | Inherit the active/default model unless they become a bottleneck. |

Saved config:

```text
.pi/gentle-ai/models.json
```

Applied configuration:

```text
.pi/agents/*.md
.pi/settings.json
```

Use `Inherit active/default model` to remove an agent override.

## Project Files

On Pi `session_start`, `gentle-pi` copies project-local assets without overwriting local edits:

```text
.pi/agents/sdd-*.md
.pi/chains/sdd-*.chain.md
.pi/gentle-ai/support/strict-tdd.md
.pi/gentle-ai/support/strict-tdd-verify.md
```

Use `/gentle-ai:install-sdd --force` only when you want to replace local SDD assets with the package version.

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| Gentle AI says Pi is missing | Install Pi first and make sure `pi` is on `PATH`. |
| SDD agents are missing in Pi | Start Pi in the project so `gentle-pi` can run `session_start`, or run `/gentle-ai:install-sdd`. |
| Persona did not change immediately | Run `/reload` or start a new Pi session. |
| Model override should be removed | Open `/gentleman:models` and choose `Inherit active/default model`. |
| Memory tools are missing | Confirm `gentle-engram` is installed, then check `/gentle-ai:status`. |

## Next Steps

- Read [Supported Agents](agents.md) for the full agent matrix.
- Read [Engram Commands](engram.md) if you want to inspect or sync persistent memory.
- Read [Usage](usage.md) for the general Gentle AI CLI and TUI flow.

← [Back to README](../README.md)
