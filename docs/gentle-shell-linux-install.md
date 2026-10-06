# Gentle Shell on Linux

**This is a source candidate with limited MVP acceptance, not a released installation guide.**
The released Gentle AI 3.7.0 binary does not contain this new installer route.
Use only a separately authorized source-built candidate in a bounded Guest.
Installation and UI opening do not establish full qualification or release readiness.

The approved target is ordinary Pi 1.0.0 with Gentle/native 4.0.0 on top.
The source now composes independently pinned modern roots through stock npm,
using the unchanged Node-only bootstrap rather than legacy composite authority.
Installation and warm UI opening were observed; the full journey still failed.
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
Tab cycles only the fields available in that mode; arrows change mode.
Spaces are literal when editing paths. Enter reviews the physical selection;
`y` explicitly confirms it and closes the TUI before the installer takes the
terminal. Escape cancels before installation; Ctrl-C during installation
requests cancellation and waits for stop and reap.

`gentle-ai shell help` and `gentle-ai shell --help` print usage without starting
a supervisor. Unknown commands refuse before entering the execution boundary.

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
1 GiB total. Foreign owners, writable objects, special files, escaping links
and changed preimages refuse. This inventory is not a recovery backup.

Normal launches have no installer menu. `TARGET/bin/gentle-shell install`
reopens the dedicated installer; other arguments go to the selected stock Pi.
The source now preserves the caller's project directory in both direct and
manager routes; installer staging is not the ordinary coding directory.
The limited smoke asserts caller CWD and foreground restoration for its UI openings;
the full launch, signal and manager journey remains deferred.

## Execution boundary

Linux amd64 and a normal non-root UID are required. Installation and launch
recheck real cgroup2 membership and physical leaf limits: 3 GiB memory, zero
swap, one CPU and 64 tasks; capabilities must be zero and NoNewPrivs set.

An already-qualified process enters directly. Otherwise the source candidate
uses only an existing delegated systemd user manager, version 254 or later.
There is no sudo, system-manager fallback, new delegation or container fallback.
Missing prerequisites refuse before package JavaScript runs. Refusals include
the underlying cause. The supervisor executable and its ancestors must be owned
by the current user or root and must not be group- or other-writable (except
root-owned sticky `/tmp`); an unsafe ancestor is named in the error. A group-writable `~/go/bin` or Linuxbrew prefix
is not accepted. Build or place the supervisor in a qualifying location rather
than relaxing permissions on an unrelated shared prefix.
Successful user-manager entry, terminal behavior and cancellation remain
separately unqualified.

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
The production npm update/force/recovery journey remains deferred.
Package lifecycles remain disabled.

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

## Project-preserving startup

Owned launches seal `GENTLE_PI_NO_SKILL_REGISTRY=1`, a supported opt-out in
pinned Gentle 4.0.0. It skips automatic `.atl/` skill-registry refresh and
watchers, not Pi's normal skill loading. Explicit `/skill-registry:refresh`
can still intentionally write the registry in the caller's project.
The opt-out targets the startup writes observed at `3e07a1179`; the unchanged
blank-project smoke must still pass on the corrected revision before claiming
preservation. No caller arguments, personal settings or package sources change.

## Cold search helpers — runtime proof pending

Stock Pi 1.0.0 opens its UI by ensuring `fd` and `rg`; missing helpers trigger
release lookups and downloads. Cold installation now supplies fixed Linux amd64
musl archives to the owned agent's standard `bin/`, without changing stock Pi:

| Helper | Fixed release | Archive SHA-256 |
| --- | --- | --- |
| fd | 10.5.0 | `761c72dc8e120d85b22292063be8a796e2eeb20eb3e4f38b8fa2343ccf3514a7` |
| rg | 15.2.0 | `33e15bcf1624b25cdd2a55813a47a2f95dbe126268203e76aa6a585d1e7b149c` |

These are hashes of human-authorized, credentialless HTTPS acquisition, not
independently published checksums or signature claims. Before extraction, the
installer checks exact archive bytes/hash, TLS, ownership and bounds; every
launch rechecks the retained archive and the exact physical helper bytes/mode.
Existing helper collisions are refused, never overwritten. Earlier installations
without these archives fail closed; no silent migration is claimed. The startup
network guard remains unchanged, `PI_OFFLINE` is not sealed, and the stock updater
is retained. Final-head Guest evidence is still required before claiming MVP PASS.

## Limited MVP smoke — result pending

The named `MVP Separate smoke` job compiles the exact workflow `github.sha`
only after physical worker isolation. Its Guest `smoke` mode seeds an authenticated
published personal Pi prefix, agent settings and shell configuration, then checks:

- Cold Separate installation through the advertised inspect/confirm CLI route.
- Pi 1.0.0, Gentle 4.0.0 and the pinned native 4.0.0 ELF hash and execution.
- Physical review/confirmation through the existing installer TUI on that installed target.
- Actual Gentle editor opening through both owned bindings, without a model or API key.
- Unchanged personal prefix, HOME/configuration and blank caller project.
- Caller CWD, successful exit, terminal foreground restoration and source formatting.

A successful receipt has `mvpSmoke.outcome: PASS` and the exact `sourceSHA`,
while `functionalReady` stays **false**. Until that job actually succeeds,
no smoke PASS is claimed. This is not a replacement for required PR checks.
The UI-opening marker is Gentle's empty-editor hint plus Pi's completed
credentialless startup fallback, not the missing `/gentle:status` response.
Cold **first-install TUI**, command registration, full provider/startup closure,
Shared, production update/force, manager termination and recovery remain debt.
The `full` Guest mode and its assertions remain available but are not run by
this limited smoke job. No existing safety, resource or output bounds are relaxed.

## Retained preview APIs — explicit deadcode debt

The human-approved deadcode exception adds exactly 97 retained protected-preview
and prior private-installer symbols to `.deadcode-baseline.txt`. These APIs are
not reached by the production MVP; this is retained implementation debt, not a
call-graph false positive or evidence of production integration. Their code and
existing tests remain intact. The unused MVP helper `userEntryArgs` is removed,
not exempted. The ratchet script is unchanged and still rejects other new entries.
The exact sorted exception manifest SHA-256 is
`ef752d43ea3a468cfdf837b88d974dbb5a15f6d62938dce12b3422fba8da18b5`.

The opening-only smoke uses stock Pi 1.0.0's `Ctrl+D` exit from its empty editor,
not two `Ctrl+C` clear actions. Complete input delivery, exit zero, caller
foreground restoration and the unchanged 45-second deadline remain mandatory.
A fresh exact-head smoke must pass; this change alone is not runtime proof.

## Disposable VM full qualification — deferred

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

The historical first lab attempt failed before worker compilation; later runs
observed Separate installation and warm UI opening inside the bounded laboratory.
The full journey at `3c863f6` still failed at command-registration observation.
The baseline job pins `322de52a3739ebe4fb0c2b04546d3af642be51ea` and asserts
User installer sources are absent. Its results do not validate the final candidate.
Output is strict UTF-8, lossless base64 with newline/NUL accounting;
entire Guest output is withheld at 4,096 bytes or above. No skipped manager test
can produce full readiness. Functional checks and source formatting remain due.

The installer remains inside the Gentle AI TUI, not a browser wizard.
The full Guest mode retains Alan PR1703's pinned pnpm experiment; the MVP smoke does not run it.
Root-only backend evidence is not full startup/native/PTY/shared-recovery qualification; production authentication checks remain unchanged and there is no second updater.

## Full qualification checklist — deferred

- [ ] Source-built CLI, dedicated TUI and both physical installation modes.
- [ ] Actual UID1002 global installation and independently authenticated graph.
- [ ] Full startup source closure, real PTY Pi startup and Gentle extension load.
- [ ] Personal Pi preservation and explicit shared selection/consent.
- [ ] Real stock prior/next upgrade and forced reinstall; both bindings survive.
- [ ] Cancellation/reap, fresh retry, collisions and supplier stream failure.
- [ ] Publication/readback/cleanup ambiguity and usable preserved recovery evidence.
- [ ] Existing delegated manager: real controllers, PTY and signal propagation.
- [ ] All historical private-install controls, non-root cases and formatting pass.

**The limited MVP smoke cannot mark this full checklist passed or support a `Ready` claim.**
