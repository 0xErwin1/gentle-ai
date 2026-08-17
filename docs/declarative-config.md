# Declarative Configuration

A Gentle AI installation can be described in one versioned JSON document and reproduced from it. The document is the desired state: what should be configured, for which clients, with which roles, skills, permissions and MCP servers. Every choice the interactive flags express is expressible here, and the same document drives validation, a non-mutating render, a diff against the live installation, reconciliation, and a lossless export back out.

```console
gentle-ai config validate --config gentle-ai.json
gentle-ai install --config gentle-ai.json
```

## Quick path

1. Export what the current machine already has:

   ```console
   gentle-ai config export > gentle-ai.json
   ```

   `export` reports anything it cannot represent instead of dropping it. A document it round-trips reports `"lossless": true`.

2. Check the document without touching anything:

   ```console
   gentle-ai config validate --config gentle-ai.json
   ```

   An empty `diagnostics` array means the document is valid. Every diagnostic carries a stable `code`, the JSON `path` it applies to, and a message naming how to resolve it.

3. Apply it:

   ```console
   gentle-ai install --config gentle-ai.json
   ```

   Use `--dry-run` first to preview the pipeline. On a machine Gentle AI already configured, `gentle-ai sync --config gentle-ai.json` reconciles instead.

## Document shape

```json
{
  "version": "v1",
  "selection": {
    "agents": ["opencode", "claude-code"],
    "components": ["skills", "persona", "permissions", "sdd", "theme"],
    "skills": ["comment-writer", "cognitive-doc-design"],
    "skillAssignments": { "opencode": ["comment-writer"] },
    "persona": "neutral",
    "preset": "full-gentleman",
    "sddMode": "single",
    "strictTDD": true,
    "scope": "global",
    "channel": "stable",
    "permissions": {
      "allow": ["Bash(git status:*)"],
      "deny": ["Bash(rm -rf:*)"],
      "ask": ["Bash(git push:*)"]
    },
    "mcpServers": {
      "example": { "command": "example-mcp", "args": ["serve"] }
    }
  },
  "roles": [
    {
      "id": "orchestrator",
      "renderedName": "gentle-orchestrator",
      "references": ["apply"],
      "description": "Coordinates the change",
      "prompt": "You coordinate work and delegate.",
      "tools": ["Read", "Grep"],
      "mode": "primary",
      "model": { "provider": "anthropic", "model": "claude-opus-5" }
    },
    { "id": "apply", "renderedName": "gentle-apply", "mode": "subagent", "hidden": true }
  ],
  "extensions": {
    "opencode": { "share": "disabled" }
  }
}
```

Unknown fields are rejected rather than ignored, so a typo fails validation instead of silently doing nothing.

Every field, what it accepts and what omitting it means is in the [reference](declarative-config-reference.md).

### Logical identity and rendered names

A role's `id` is its logical identity and never appears in generated output. `renderedName` is the name a client sees. `references` always name logical ids.

Renaming is therefore a one-line edit: change `renderedName`, reconcile, and every generated reference follows. The old resource is removed and the new one created in the same plan.

`mode` says whether the operator addresses the role directly (`primary`) or another role delegates to it (`subagent`); `hidden` keeps it out of the client's agent list. Each adapter renders the role in its own form: a file with frontmatter where agents are files, an entry with `mode`, `tools` and a delegation permission where they live in one settings file. An adapter whose format has no equivalent for a field renders the rest.

```console
gentle-ai config diff --config gentle-ai.json --home ~ --destination ~ --stage /tmp/stage
```

```json
{ "kind": "create", "path": ".claude/agents/gentle-implementer.md", "selector": "file" }
{ "kind": "update", "path": ".config/opencode/opencode.json", "selector": "/agent/gentle-orchestrator" }
{ "kind": "remove", "path": ".claude/agents/gentle-apply.md", "selector": "file" }
```

### Per-adapter overrides

`skills` applies to every declared adapter. `skillAssignments` replaces that list for the adapters it names and leaves the rest on the flat list, so a document only names an adapter when it differs.

`extensions` is the escape hatch for client configuration the neutral contract does not model. Each block is merged verbatim into that adapter's settings and only that adapter's; an extension naming an adapter the document does not declare fails validation.

## Operations

| Command | Mutates | Purpose |
|---------|---------|---------|
| `config validate` | no | Schema, references, adapter support and option compatibility. |
| `config render` | writes only into `--stage` | Produce the provider-specific files for inspection, packaging or comparison. |
| `config plan` / `config diff` | no | Compare the document with the live installation and report what would change. |
| `config apply` / `config reconcile` | yes | Bring the managed configuration to the declared state, with rollback on failure. |
| `config adopt` | writes only to `--home` | Record a document as the installation without writing a client file, for a frontend that renders the tree itself. |
| `config export` | no | Emit the current installation as a document, reporting what it cannot carry. |
| `install --config` | yes | Full install pipeline driven by the document. |
| `sync --config` | yes | Reconcile an existing installation against the document. |

`render`, `plan`, `diff`, `apply` and `reconcile` take `--destination` (the root being configured) and `--stage` (an isolated staging root). `apply` and `reconcile` also require `--home`, where the desired state and the ownership manifest are persisted.

Read-only operations always emit JSON on stdout.

### Determinism

Given the same Gentle AI version, document and source assets, `render` produces the same files, paths and content. The staging root is a stand-in for the destination and never appears in rendered content, so two renders into different staging directories are byte-identical.

## Ownership and reconciliation

Gentle AI records an ownership manifest of every resource it wrote. Reconciliation acts only on those:

- A managed resource missing from the document is removed.
- A managed resource whose content changed is updated.
- An unmanaged resource occupying a path the document wants is reported as `render.ownership.conflict` and left alone.
- Unrelated keys in a composed settings file are preserved. Ownership inside `opencode.json` is per agent (`/agent/<name>`), not per file, so hand-written agents survive a reconcile.

Components that are installed rather than written — the ones that download a binary or clone a repository — carry no bytes to reconcile. A plan reports them as `pendingProvisioning` with the command that performs them, instead of pretending they were written.

## Precedence

For one invocation, later wins:

1. Defaults shipped by Gentle AI.
2. The selected preset.
3. The document.
4. Provider extensions (`extensions.<adapter>`).
5. Existing user-owned client configuration, where the adapter composes rather than replaces.

Declared permission rules are unioned with the shipped guardrails rather than replacing them, so allowing something never silently removes a deny.

`--config` is mutually exclusive with the flags that carry semantic configuration — `--agent`, `--component`, `--skill`, `--persona`, `--preset`, `--sdd-mode` and the background-subagent flags. Combining them is rejected (`config.flags.exclusive`) rather than resolved by a hidden rule. Operational flags stay composable: `--dry-run`, `--destination`, `--stage`, `--home`.

A declared `scope` and `channel` stand in for the flags they cannot be combined with, so `"scope": "workspace"` installs into the workspace exactly as `--scope workspace` does.

## Desired state versus runtime state

The document describes what Gentle AI should configure. It deliberately excludes state the runtime owns: provenance, managed-asset digests, update-check timestamps, and the user-owned review-mode kill switch. `config export` names those as loss diagnostics rather than inventing document fields for them, which is why exporting a legacy installation reports `config.export.loss.legacy-operational`.

## Versioning

`version` is the schema version and is independent of the binary version. `v1` is current; `v0` is accepted and migrated. An unrecognised version is rejected with `config.version.unsupported` naming the versions this binary understands, so a consumer can decide whether a document is safe to interpret without inspecting the binary.

## Adapter support

Adapters consume the same normalized model and only implement output. Two capabilities vary and are reported rather than silently worked around:

- An adapter that expresses no agent roles refuses a document declaring `roles` (`config.role.unsupported-adapter`) instead of dropping them. Declare the roles for the adapters that hold them, or drop that adapter.
- An adapter that does not read permissions as allow/deny/ask rule lists refuses a document declaring `permissions` (`config.permissions.unsupported-adapter`). OpenCode, for instance, keys permissions per tool and glob under a different name, so a rule list written there would be a block it never reads.

## Diagnostics

| Code | Meaning |
|------|---------|
| `config.document.unknown-field` | The document contains a field the schema does not define. |
| `config.version.unsupported` | The schema version is not one this binary understands. |
| `config.agent.unsupported` | A declared adapter does not exist. |
| `config.role.reference.unresolved` | A `references` entry names a role the document does not declare. |
| `config.role.mode.unsupported` | A role declares a mode other than `primary` or `subagent`. |
| `config.role.unsupported-adapter` | A declared adapter expresses no agent roles. |
| `config.skill-assignment.undeclared-adapter` | A skill assignment names an adapter the document does not declare. |
| `config.extension.undeclared-provider` | An extension names an adapter the document does not declare. |
| `config.permissions.unsupported-adapter` | A declared adapter does not read permissions as rule lists. |
| `config.flags.exclusive` | `--config` was combined with a semantic selection flag. |
| `config.export.loss.*` | Export could not represent a value; the message names what to do instead. |
| `render.ownership.conflict` | An unmanaged resource occupies a path the document wants. |

## Checklist

- [ ] `config validate` returns an empty `diagnostics` array.
- [ ] `config diff` against the intended machine reports only the changes you expect.
- [ ] `config export` of the result round-trips to the same document with `"lossless": true`.
- [ ] Roles referenced by other roles are declared in the same document.
- [ ] Adapters named by `skillAssignments` and `extensions` also appear in `agents`.
