# W15 — Main-only command-mode intent contract

This new task starts at clean local `main` commit `c9d6b0f7653fb1fbe2f56c280699d1399f0225df`. It ports only a pure shellinstaller data contract; there is **no installer, approval, Apply or Ready route** in this unit.

## Product effect matrix (intent, not execution)

| Command | Stable | Main |
| --- | --- | --- |
| `pi` | Describe replacement/upgrade of the **existing Pi** with pinned Stable Pi+Gentle-Shell, conditional on future physical InstanceID-bound consent and rollback. | Same existing-Pi replacement boundary, conditional on pinned Main source and the same future consent/rollback. |
| `gentle-shell` | Describe a **separate Pi, home and executable**, reachable only by `gentle-shell`; existing Pi untouched, pinned Stable. | Same isolated-Pi boundary, conditional on pinned Main source; existing Pi untouched. |

Command choice and channel choice are independent. A description does not select source bytes, read Pi settings, confer takeover authority, or authorize removal or installation.

## Scope and verification

- Seven NEW paths only: this task (≤65 lines) plus `internal/shellinstaller/{channel.go,channel_test.go,profile.go,profile_test.go,terminal_entrypoint.go,terminal_entrypoint_test.go}`. Total authored changed diff ≤400 lines.
- Six Go files must be byte-identical to the reviewed W14 core donor at `/home/devel/projects/gentle-ai-worktrees/shell-command-mode-contract-14`; compare full per-file SHA-256. W14 source is donor DATA, not a cherry-pick, branch merge or Main runtime proof.
- Keep `Profile.Validate` refusing `gentle-shell` until independently reviewed isolated source/instance/consent/install/Ready work exists. The legacy zero value remains Pi intent, not a takeover approval. Stable/Main source pinning is NOT implemented here.
- Main's existing retired-SDD run/sync NO-OP cases, legacy fields and guidance are untouched. Add no SDD offer/manage API; do not import core's live SDD behavior or claim complete SDD removal.
- Go tests are written as source DATA only. Independent adversarial STATIC review is the sole permitted verification now; no Go/Node/Pi tests/builds, config/Apply/root/network/CI, commit or delivery without fresh exact authority.
- Rollback boundary is deleting only these seven new paths. No Main, `main-current`, W14 donor, frozen W12, or U1 modifications. An operational Linux installer and protected WSL runner remain separately gated.
