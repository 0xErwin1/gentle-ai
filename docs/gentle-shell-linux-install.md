# Gentle Shell on Linux

**This is an unqualified source candidate, not a released installation guide.**
The released Gentle AI 3.7.0 binary does not contain this new installer route.
Use only a separately authorized source-built candidate in a bounded Guest
until the functional checklist below passes. Source review is not readiness.

The approved target is ordinary Pi 1.0.0 with Gentle/native 4.0.0 on top.
The source now composes independently pinned modern roots through stock npm,
using the unchanged Node-only bootstrap rather than legacy composite authority.
This replacement is uncompiled and unexecuted; it is not qualification.
Gentle Shell does not fork Pi, replace its updater, or modify PATH or shell files.

## Select the installation

| Mode | Physical installation and configuration |
| --- | --- |
| Separate | Private global npm prefix, Node/npm, HOME, agent directory, state and installer staging. Your existing `pi` is untouched. |
| Shared | Explicitly selected existing owned global Pi prefix and agent directory. Both new bindings use those same objects. |

Commands are `TARGET/bin/gentle-shell` and `TARGET/bin/pi`, outside npm's bin.
Choosing that directory for your PATH is your action, not an installer effect.
Neither binding changes an unrelated existing `pi` command.

## Candidate commands

The dedicated installer TUI is separate from normal Pi launches:

```sh
gentle-ai shell install
```

Select a target, mode and, for shared mode, the existing prefix and agent.
Enter reviews the physical selection; `y` explicitly confirms it.
Escape cancels; cancellation during installation waits for stop and reap.

For noninteractive use, inspect before approving exactly that selection:

```sh
gentle-ai shell install --target /owned/private-parent/shell --mode separate --inspect
gentle-ai shell install --target /owned/private-parent/shell --mode separate --confirm PRINTED_SHA256
```

Shared mode additionally requires `--prefix /owned/selected-prefix` and
`--agent /owned/selected-agent` on both invocations. The parent and selected
roots must be physically owned and private; aliases and collisions refuse.
Shared confirmation now binds both selected file trees, not just directories.
Inspection bounds each tree to 250,000 entries, 32 MiB per regular file and
256 MiB total. Foreign owners, writable objects, special files, escaping links
and changed preimages refuse. This inventory is not a recovery backup.

Normal launches have no installer menu. `TARGET/bin/gentle-shell install`
reopens the dedicated installer; other arguments go to the selected stock Pi.
The source now preserves the caller's project directory in both direct and
manager routes; installer staging is not the ordinary coding directory.
Terminal handoff and restoration are implemented but still need real PTY tests.

## Execution boundary

Linux amd64 and a normal non-root UID are required. Installation and launch
recheck real cgroup2 membership and physical leaf limits: 3 GiB memory, zero
swap, one CPU and 64 tasks; capabilities must be zero and NoNewPrivs set.

An already-qualified process enters directly. Otherwise the source candidate
uses only an existing delegated systemd user manager, version 254 or later.
There is no sudo, system-manager fallback, new delegation or container fallback.
Missing prerequisites refuse before package JavaScript runs. Successful user-
manager entry, terminal behavior and cancellation remain separately unqualified.

The sealed Node environment adds the system CA store to Node's bundled roots.
It does not inherit caller certificate selectors, proxies or loader injections,
and does not disable TLS verification. Test CA/key material belongs only to
credentialless Guest fixtures, never your machine's trust store.

## Updates and interrupted installation

Pi's ordinary update command still invokes real stock npm in the selected
physical global prefix. `pi update --self --force` against authentic 1.0.0
would qualify only a same-version reinstall. The source Guest now prepares an
independently authenticated published 0.99.2 cohort, including its nested TUI,
and invokes that actual Pi's `update --self` to reach the known 1.0.0 graph.
Its TEST-only TLS release API selects 1.0.0; it does not establish public latest
or provide a production version override. Fixture preparation is not an updater.
Prelaunch and postlaunch readback accept only complete known prior/modern graphs;
unknown versions, bytes or placement refuse and retain recovery evidence.
These controls are authored, not executed. Package lifecycles remain disabled.

Foreign destinations and native locks, stages, backups or tombstones refuse.
Do not delete uncertain shared workspaces or published evidence to retry.
Shared source snapshots retain the entire affected prefix and agent preimages.
Upgrade fixtures retain distinct full preimages in `state/upgrade`, including in
separate mode; they do not overwrite the initial shared-install snapshots.
Snapshot file trees and selection writes are synchronized before fixture effects.
Recovery selects the upgrade snapshot when present and binds fresh confirmation
to the current selected trees. Roots and target parent require one filesystem:

```sh
gentle-ai shell recover /preserved-workspace/installed inspect
gentle-ai shell recover /preserved-workspace/installed PRINTED_CONFIRMATION
```

For published uncertainty use the actual published target as ROOT. Inspection
binds recovery to current selected trees; restoration preserves new evidence in
fresh quarantine directories. This is not hostile-same-UID custody or full DR.
Recovery, retry and cleanup-failure reporting still require actual fault tests.
The full Guest path now attempts these controls only with an existing real
manager and bus. Any failed/missing assertion refuses qualification; merely
writing the path, or a successful direct-only run, is not functional readiness.

## Disposable VM qualification — not qualified

The separate `user-vm-laboratory` CI job targets Ubuntu 24.04 amd64 on the fork's
existing feature-branch trigger, not privileged Docker or your machine.
Privileged setup is explicitly laboratory preparation before installer entry:
it rejects an active UID1002 manager or occupied laboratory paths. It requires
UID/GID1002 and the account/group name `gentle-lab` to be free, then creates only
that fresh Guest's no-login account; occupied UID1000 is not touched or qualified. It prepares a bounded read-only manager
and workers, and verifies physical worker isolation
before compiling candidate Go. No credentials are forwarded to those workers.
Go and Node archives are independently size/hash checked before use.

The authored suite includes real prior→next stock update, both bindings, native
v4 execution and fresh-Guest negotiated STATUS, PTY/TUI cancellation and launch,
controller/subtree termination, and personal published-prefix preservation.
It injects acquisition cancellation, cleanup denial, stale recovery consent,
unknown-cohort refusal/recovery, and a post-publication Node-mode readback fault.
Blank-project and fixture-origin observations are scoped checks, not whole-network
attestation or a zero-write promise for arbitrary existing Pi projects.

The first lab attempt failed before worker compilation: the assumed `ubuntu`
account was absent. Revised setup, manager, compiler and suite remain unqualified.
A parallel legacy job compiled the new package outside the approved laboratory;
that execution is not admitted as qualification. Legacy source is now pinned to
qualified commit `322de52a3739ebe4fb0c2b04546d3af642be51ea`, excluding new User code.
Output is strict UTF-8, lossless base64 with newline/NUL accounting;
entire Guest output is withheld at 4,096 bytes or above. No skipped manager test
can produce full readiness. Functional checks and source formatting remain due.

The installer remains inside the Gentle AI TUI, not a browser wizard.
The Guest probes Alan PR1703's pinned stock npm 11.19.0 in a new private, unpublished runtime, with all existing global authentication checks unchanged.
This experimental backend reuse is not functional qualification and introduces no second updater.

## Acceptance checklist — pending

- [ ] Source-built CLI, dedicated TUI and both physical installation modes.
- [ ] Actual UID1002 global installation and independently authenticated graph.
- [ ] Full startup source closure, real PTY Pi startup and Gentle extension load.
- [ ] Personal Pi preservation and explicit shared selection/consent.
- [ ] Real stock prior/next upgrade and forced reinstall; both bindings survive.
- [ ] Cancellation/reap, fresh retry, collisions and supplier stream failure.
- [ ] Publication/readback/cleanup ambiguity and usable preserved recovery evidence.
- [ ] Existing delegated manager: real controllers, PTY and signal propagation.
- [ ] All historical private-install controls, non-root cases and formatting pass.

**No `Ready` claim is supported while any applicable item is pending.**
