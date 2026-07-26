# 🧪 How to test — Organic RDD (pre-release 2.2.0-rc.1)

> Community testing guide for the candidate built from PR [#1801](https://github.com/Gentleman-Programming/gentle-ai/pull/1801). Every **Expected** in this guide was validated against real output before it was published. The guide uses a throwaway HOME precisely so it does not touch your real config — do not skip the setup.

## How to get this binary

The binaries are on the pre-release page: **https://github.com/Gentleman-Programming/gentle-ai/releases/tag/v2.2.0-rc.1**

1. Download the asset for your platform from the Assets section of that page.
2. Verify the checksum against `SHA256SUMS.txt`:
   ```
   sha256sum -c SHA256SUMS.txt --ignore-missing
   ```
3. Save your current binary and replace it:
   ```
   cp $(which gentle-ai) ~/gentle-ai.backup
   chmod +x gentle-ai_2.2.0-rc.1_<os>_<arch>
   mv gentle-ai_2.2.0-rc.1_<os>_<arch> $(which gentle-ai)
   ```
4. Confirm: `gentle-ai --version` must say `2.2.0-rc.1`.
5. To roll back when you are done: `mv ~/gentle-ai.backup $(which gentle-ai)`.

## Setup (once)

1. Create a test HOME so you do not touch your real config:
   ```
   export TESTHOME=$(mktemp -d) && export HOME=$TESTHOME
   ```
2. Create a test repo (the `.gitignore` keeps the installed config out of the diffs):
   ```
   mkdir -p $HOME/demo && cd $HOME/demo && git init -b main && git config user.email t@t && git config user.name T && echo ".claude/" > .gitignore && echo hello > README.md && git add -A && git commit -m "init"
   ```

## Steps to test

### Flow 1: Routing without SDD (the main fix)

1. [ ] `gentle-ai install --scope workspace --agents claude-code --components permissions` → **Expected**: it installs and ends with "You're ready", without asking anything about SDD.
2. [ ] Open `$HOME/demo/.claude/CLAUDE.md` → **Expected**: a routing section with **direct inline**, **delegated direct** and **optional SDD**.
3. [ ] Search for `WorkRun` or `work-capabilities` → **Expected**: **zero results**. If it shows up, that is a bug.
4. [ ] Search for `review mode` → **Expected**: `gentle-ai review mode enable|disable|status` shows up.
5. [ ] Run the same install again → **Expected**: same output and the files do NOT change.

### Flow 2: Kill switch

1. [ ] `gentle-ai review mode status --cwd $HOME/demo --json` → **Expected**: effective `on`, with the source that decides it.
2. [ ] `gentle-ai review mode disable --cwd $HOME/demo` → **Expected**: it confirms reviews are off.
3. [ ] `status` again → **Expected**: effective `off`, source `global`.
4. [ ] `gentle-ai review start --cwd $HOME/demo` → **Expected**: refused, naming that reviews are turned off **and naming the command that turns them back on**, scoped to the source that actually decided:

```
review-driven development is disabled: start is rejected because the global mode source
keeps it off; turn it back on with gentle-ai review mode enable --scope=global
```

It does NOT hang, it does NOT review. A refusal that exits non-zero and names no command is the defect. If you turned it off at clone scope, the scope in the message must say `clone`, not `global`.
5. [ ] `enable` and `status` → **Expected**: `on` again.
6. [ ] `disable --scope clone`, clone (`git clone $HOME/demo $HOME/demo2`) and `status` in `demo2` → **Expected**: `demo2` gives **on** — turning a clone off is NOT inherited.
7. [ ] **Before moving on**: `enable --scope clone` in `demo` → **Expected**: `on`.

### Flow 3: Documentation-only change (zero ceremony)

1. [ ] Edit `README.md` (plain text) and stage **only that file**: `git add README.md`.
2. [ ] `gentle-ai review start --cwd $HOME/demo` → **Expected**: `risk_level: low`, `selected_lenses: []` — zero reviewers, no question.

### Flow 4: The review is chosen by evidence, not by size

1. [ ] `mkdir -p internal/auth && echo "func CheckToken() {}" > internal/auth/session.go`, `git add internal/auth`.
2. [ ] `review start` → **Expected**: `risk_level: high`, 4 lenses, and `risk_evidence` naming the reason (e.g. `"authentication in internal/auth/session.go"`).
3. [ ] Commit that (`git commit -am "auth"`). Generate 1000+ lines of text across several `.md` files, `git add *.md`, `review start` → **Expected**: `low`, 0 lenses. It does NOT escalate on size.

### Flow 5: The consent question (needs a real terminal)

1. [ ] With a tier 1/2 change ready, `review start` in an interactive terminal → **Expected**: **two** options — `1) Run the review now` / `2) Not now, just this once` — and a final line naming `gentle-ai review mode disable`. **There is no option 3.**
2. [ ] Answer `2` → **Expected**: it does not review this candidate.
3. [ ] ANOTHER change and `review start` → **Expected**: it asks again.
4. [ ] Answer `1` → **Expected**: it reviews, and the next change no longer asks.

**If you are driving this from a script or an agent**: the answer is read as one whole line, so it must end with a newline. Sending the bare character `2` over a pseudo-terminal is echoed but never completes the read, and the command waits until your harness kills it. Send `2\n`. There is no timeout on this prompt, so a missing newline looks exactly like a hang.

### Flow 6: Delivery with reviews turned off

**Watch out for the fixture**: this flow needs a configured upstream. With no remote, `pre-push` cannot derive what to compare against and fails closed with a typed error — that is correct, but it does not test what this flow wants to test. Set the remote up first:

```
git init --bare $HOME/demo-remote.git
cd $HOME/demo && git remote add origin $HOME/demo-remote.git
git push -u origin HEAD
```

1. [ ] Turn reviews off, make a change and commit → **Expected**: the commit works normally.
2. [ ] `gentle-ai review validate --gate pre-push --cwd $HOME/demo` → **Expected**: `"delivery": "disabled/unmanaged"`, `"allowed": false`, **exit 0**. It reports, it does not block.
3. [ ] Check that it does NOT say `allow` → **Expected**: never a false PASS.

### Flow 7: Turning it off mid-work and coming back

1. [ ] With reviews on, a change **staged but not committed**. Turn reviews off → **Expected**: everything flows.
2. [ ] Turn them on and `review start` → **Expected**: it works — it freezes and reviews from scratch. Nothing is lost. (If you already committed, the result carries a `hint` with `--base-ref`.)

### Flow 8: No phantom SDD artifacts

1. [ ] `git rev-parse --git-common-dir` and look inside → **Expected**: inside `gentle-ai/` only review state; nothing like `sdd*`, `trace`, `evaluation`.

---

## Flows 9 to 13: what we fixed with your feedback

These flows are new. Each one reproduces a bug someone in the community found in earlier rounds. They need a binary **later than Refresh 4**. Check which build you have with `gentle-ai doctor`: it names the binary you actually invoked and its version, and warns when that differs from the one on your `PATH`. If yours predates the current refresh, download it again from the release page or build from the PR branch.

### Flow 9: Pre-push after you already pushed (the bug that cost us the most)

Reported by @Wladimirfn, @Denver2828, @MarsSall and @Freedom2828. It looked like a Windows bug and it was not: it happened when the reviewed commit was **already published**.

You need the remote from Flow 6.

1. [ ] Docs change, `review start` + `review finalize` → **Expected**: approved receipt.
2. [ ] `review validate --gate pre-commit`, commit, and `review validate --gate pre-push` → **Expected**: `allow`, **exit 0**. (This is the regression: before the push it still has to work the same.)
3. [ ] **Push**: `git push origin HEAD`.
4. [ ] Turn reviews off and make ANOTHER docs commit.
5. [ ] `review validate --gate pre-push` → **Expected**: `"delivery": "disabled/unmanaged"`, **exit 0**. **NEVER** `authority_corrupted`.
6. [ ] Turn reviews on and repeat the gate → **Expected**: `result: "scope-changed"` naming a **runnable** recovery, not just a reason:

```
review lifecycle gate denied: scope-changed: recover via review.recover
  --base-ref <commit> --committed-only (requires: predecessor_lineage_id, ...)
```

No corruption either.

7. [ ] **Now run exactly what it named**, filling in the required inputs, then `review finalize` on the successor, then repeat the gate → **Expected**: `allow`, exit 0.

Step 7 is the one that matters most in this whole guide. Until this refresh the message named a recovery that, followed literally, landed you right back at the same denial. The tests proved the message was **emitted** and never that following it **worked**. If you follow it and stay blocked, that is the most valuable report you can send us.

8. [ ] One case still ends without a one-step recovery **on purpose**: when the committed content is byte-identical to what was approved and only the commit topology changed. There the message stays at "requires explicit maintainer action" rather than naming a command that would not resolve it. That is intended, not a defect.

### Flow 10: First commit in a repo with no history

Reported by @lu149e, with the root cause confirmed by @Denver2828.

1. [ ] `mkdir $HOME/unborn && cd $HOME/unborn && git init -b main`.
2. [ ] Create a code file, `gofmt` if it applies, and `git add -A`. **Do not commit yet.**
3. [ ] `git rev-parse --verify HEAD` → **Expected**: it fails, because there is no first commit yet. That is correct.
4. [ ] `gentle-ai review start --cwd "$PWD"` → **Expected**: the review **starts**. It used to blow up with `Needed a single revision`.

### Flow 11: Transitions run exactly as they are printed

This one is for people using agents. The product prints the next command; if it is not literally executable, an agent that follows instructions to the letter gets stuck.

1. [ ] With a review in progress, ask for the next transition. The command needs the explicit contract:

```
gentle-ai review status --next-transition --contract gentle-ai.review-integration/v1
```

**First read `next_transition.kind`. Steps 2 to 4 only apply when it is `execute`.**

If it is `collect`, the tool is waiting for reviewer results that do not exist yet, so there is no command it could print: a model has to run the lens first. That is correct behaviour, not a defect. Skip to step 5. If it is `stop`, there is no transition at all and the same applies.

The quickest way to land on `execute` is to ask before any review has started, or right after `review capture-result`.

2. [ ] Look at the `token` of each argument in the response → **Expected**: each one is a complete flag ready to run (`--target=sha256:...`), not a name and a value sitting apart.
3. [ ] Read the `next_transition.execute.command` field → **Expected**: one complete line, starting with `gentle-ai review <verb>`, carrying every argument from step 2 in the same order and in `--flag=value` form. You never assemble it yourself: `operation` is a logical name (`review.start`), `command` is the runnable line.
4. [ ] **Copy and paste that `command` exactly as it came out**, without fixing anything → **Expected**: it runs. It used to print `--captured-results true` (with a space) and the parser rejected it, and before that there was no `command` at all — only `operation`, which an agent had to translate into a verb by guessing.
5. [ ] If you landed on `collect`, report what its `inputs[].arguments` carry → **Expected today**: `name` and `value`, and **no `token`**. That is a known gap on this side of the payload: `execute` arguments carry their runnable token, `collect` arguments do not, so a caller still assembles those flags by hand. Say so in your report if you hit it, and do not mark the flow FAIL for the missing `command` alone: on `collect` there is genuinely nothing runnable to print yet.

### Flow 12: Finalize without evidence says what to do

**This flow needs a review in `validating`, and getting there is part of the test.** Do not look it up here and do not read the source: start a review that selects lenses (the Flow 4 auth change works) and find your way to `validating` using only what the tool tells you.

That is deliberate. Three testers in a row marked this N/A because they could not work out how to produce the reviewer-result payload `capture-result --input` demands. That was not their failure, it was the finding: the product has a command that emits the schema with a working example and nothing pointed at it. It should now point at it from both the flag help and the refusal.

1. [ ] Work your way to `validating` from the tool's own output → **Expected**: every refusal along the way names what to do next. Write down each one that does not, and what you had to guess. **If you cannot get there without reading source or asking someone, stop and report that** — it is worth more than a PASS on the step below.

**The actual test:**

2. [ ] With that review in `validating` and no captured evidence, run `gentle-ai review finalize --lineage <id> --cwd .` → **Expected**: an error that **names both commands** to get out:

```
finalize for lineage "<id>" had no verification evidence to consume and made no
transition; capture it first with `gentle-ai review capture-evidence`, then run
`gentle-ai review finalize --lineage <id> --captured-evidence`
```

It used to say `continue the current review state` and nothing ever happened.

### Flow 13: Flag combinations we do not support

1. [ ] `review start --projection staged --base-ref HEAD~1` → **Expected**: a typed rejection naming **both escapes**: `--projection staged` alone (to review the index) or `--base-ref <ref> --committed-only` (to review base-diff). It does not guess which one you meant.

---

## Flows 14 to 19: organic DX

These flows test the new work: that the tool recovers on its own, that blocks say how to continue, and that old history does not constrain current work. They need a binary **later than Refresh 4**.

### Flow 14: Old receipts do not block new work

The bug @decode2 and @fisidj found. You need the remote from Flow 6.

1. [ ] Make **three** docs changes, each one with `review start` + `review finalize` + commit. Push the first two.
2. [ ] Turn reviews off and make a fourth commit **without reviewing it**.
3. [ ] `review validate --gate pre-push` → **Expected**: `delivery: "disabled/unmanaged"`, exit 0. **Never** "multiple terminal review receipts require explicit target selection".
4. [ ] Turn reviews on and run `review status --contract gentle-ai.review-integration/v1` → **Expected**: it says nothing governs the candidate and that you should start a new one. The old lineages show up listed as a recovery option, **not** as a list you are forced to pick from.

### Flow 15: Recovery that drives itself

1. [ ] Get to a state where `review recover` is the continuation (for example: an approved review, then change the candidate's scope).
2. [ ] Run `review recover` **without** `--actor`, **without** `--reason` and **without** `--maintainer-authorization` → **Expected**: it works. The tool derives all three on its own.
3. [ ] Now run the same thing passing a deliberately **wrong** `--maintainer-authorization` → **Expected**: it refuses. The tool authorizes itself when you said nothing, but it **never corrects what you said wrong**.

### Flow 16: Blocks say which command comes next

1. [ ] With a review in progress waiting for results, run `review finalize --lineage <id>` → **Expected**: the error names `gentle-ai review capture-result`.
2. [ ] Run a gate in a repo **with no review at all**, asking for the negotiated envelope:

```
gentle-ai review validate --gate pre-commit --contract gentle-ai.review-integration/v1
```

→ **Expected**: `code: receipt_missing` and `next_action: review.start`. It used to say only "stop" and the agent had to guess.

### Flow 17: Visible numbers when it escalates

1. [ ] Push a correction past its line budget → **Expected**: the message says **spent, remaining and total** with distinct labels. It used to escalate with a number that was not printed anywhere.

### Flow 18: Defect report ready to paste

1. [ ] If you hit a terminal block **caused by us** (not by a decision of yours), → **Expected**: a single sentence naming the report file and the link to open the issue.
2. [ ] Open that file → **Expected**: it carries version, commit, OS, the operation and the error. It does **NOT** carry the contents of your files, or absolute paths with your username, or environment variables. It is meant to be pasted into a public issue.
3. [ ] A block that is **your decision** (abandon vs recover) → **Expected**: NO report is generated. There is no bug to report.

### Flow 19: First-run hygiene

1. [ ] `install --agents opencode` (OpenCode only) → **Expected**: the last line names only OpenCode, not "run claude".
2. [ ] `doctor` running the binary **by absolute path** → **Expected**: it reports the binary you ran with its version, and warns if it differs from the one on the PATH.
3. [ ] `review start --committed-only true` (with a space) → **Expected**: the error explains that a boolean flag is passed as `--flag` or `--flag=true`, never with a separate value.

---

## Flows 20 to 23: macOS only

**Why these exist.** CI runs unit tests on Ubuntu and has a native lane for Windows. It has none for Darwin, and @edwinsaavedran showed in #1853 that four macOS defects reached a release through that hole: `/var` path aliasing (#1773), `EPERM` under managed profiles (#1781), reviewer-result publication on ExFAT (#1804), and first-use store contention (#1850).

Cross-compiling with `GOOS=darwin` proves the code builds. It proves nothing about APFS, temp-directory aliases, Darwin advisory locks, or real `git` path output. Only a Mac can answer these, which is why they are here and not in CI.

**Run these on macOS only.** On Linux or Windows, mark them N/A and move on.

### Flow 20: The `/var` alias (#1773)

macOS puts `$TMPDIR` under `/var/folders/...`, and `/var` is a symlink to `/private/var`. The same repository therefore has two valid absolute paths, and authority bound to one must be found from the other.

1. [ ] `cd "$(mktemp -d)"` and set up a throwaway repo there → note the path `git rev-parse --show-toplevel` prints.
2. [ ] Run a full cycle in it: `review start`, then `review finalize`, then `review validate --gate pre-commit` → **Expected**: it completes. No "no discoverable review lineage", no path-shaped error.
3. [ ] Now `cd` into the **other** spelling of the same directory (add or remove the `/private` prefix) and run `review status --cwd "$PWD"` → **Expected**: the same lineage, same state. If it reports no authority, that is the defect: paste both paths.

### Flow 21: Reviewer results on ExFAT (#1804)

macOS lacks the exclusive-rename primitive on ExFAT, so publication falls back to an exclusive-create copy. That fallback only ever runs on a real ExFAT volume.

1. [ ] Make one and mount it:

```bash
hdiutil create -size 200m -fs ExFAT -volname RDDTEST /tmp/rddtest.dmg
hdiutil attach /tmp/rddtest.dmg
```

If `hdiutil` rejects the filesystem name, `hdiutil create -help` lists the ones your macOS version accepts. A real ExFAT USB stick works just as well, and any external volume you already have formatted that way is fine.

2. [ ] Create a throwaway repo **on that volume** (`/Volumes/RDDTEST`), make a change, and run `review start` → `review capture-result` → `review finalize`.
3. [ ] → **Expected**: the reviewer result publishes and finalize reaches its normal terminal state. A raw `ENOTSUP`, `EINVAL` or `operation not supported` reaching you is the defect.
4. [ ] Detach with `hdiutil detach /Volumes/RDDTEST` when done.

### Flow 22: First-use store contention (#1850) — **known open, we want the current state**

This one is **not fixed**. @edwinsaavedran reported that concurrent first use of a new runtime store leaks a raw `ENOENT` to the losing writers instead of a typed conflict. It is fail-closed, exactly one writer still commits, and no ledger was corrupted, but a controller cannot classify or retry an untyped errno.

This needs a source checkout rather than the released binary. From the branch under test:

```bash
TMPDIR=/private/tmp GIT_CONFIG_NOSYSTEM=1 \
  go test -p=1 ./internal/sddstatus \
  -run '^TestRuntimeLedgerCASAllowsOnlyOneConcurrentOrdinal$' -count=20
```

1. [ ] → **Expected today**: it may still fail. The reporter saw 16 of 20 and 9 of 10 iterations fail on two independent runs.
2. [ ] Report **how many iterations of 20 failed**, and whether the failure is still `review store lock could not be acquired: no such file or directory` or something else.

A clean 20 of 20 is just as valuable as a failure: it tells us whether the rate moved, and we have no Mac in CI to ask.

### Flow 23: Managed profiles (#1781)

Only reproducible on a Mac under an MDM or corporate configuration profile, which cannot be staged on a personal machine.

1. [ ] If your Mac is company-managed, run the ordinary `review start` → `finalize` → `validate` cycle → **Expected**: it completes, or fails with a typed permission error naming what to do.
2. [ ] A raw `EPERM` or `operation not permitted` with no continuation is the defect. Say which profile restrictions apply if you can.

---

## Flows 24 to 26: things nobody has tested yet

These cover behaviour that did not exist until this refresh. Flow 24 is verified and reproducible; **flows 25 and 26 have never run on a real machine of the platform they describe**, only against synthetic test profiles. That is exactly why they are here.

### Flow 24: A stale target names its own way out

The review target comes from the workspace snapshot, so anything that writes a file between asking for a transition and running it invalidates that transition. A linter, a build, a watcher, or your own shell redirect all do this. It used to produce an opaque refusal.

Run everything below with output going **outside** the repo.

1. [ ] Ask for the next transition and keep the `command` it prints:

```
gentle-ai review status --next-transition --contract gentle-ai.review-integration/v1 --cwd . > /tmp/rdd-out/nt.json
```

2. [ ] Now change the workspace, exactly as a linter would: `echo "lint output" > lint-report.txt` **inside the repo**.
3. [ ] Run the command you kept, unchanged → **Expected**: it refuses with

```
code:        stale_target_identity
next_action: review.status
cause:       review start target does not match the freshly built snapshot
```

The `cause` naming the real reason is the point. A bare `invalid_request` with an empty `required_inputs` is the defect.

4. [ ] **Follow the continuation it named**: ask for the transition again → **Expected**: a **different** target identity.
5. [ ] Run that new command → **Expected**: exit 0, review starts. If following the named continuation does not unblock you, that is the report.

### Flow 25: Windows updates itself (**never tested on real Windows**)

Windows never auto-updated: it detected a new version and handed you a command to run yourself. With Go on PATH it now upgrades through a pinned `go install`. All the evidence we have is synthetic — this flow is the first real execution.

1. [ ] On Windows with Go 1.25.10+ on PATH, run a command that triggers the update check with an older gentle-ai installed → **Expected**: it upgrades itself. It does **not** print "requires manual update", and it does **not** send you to a releases page.
2. [ ] `gentle-ai --version` afterwards → **Expected**: the new version.
3. [ ] **Report the full output even when it works.** This path has never run outside a test double.
4. [ ] On Windows **without** Go → **Expected**: it still refuses, and the refusal names the exact `go install github.com/...@vX.Y.Z` command plus the Go version needed. A releases URL as the only guidance is the defect.

### Flow 26: The upgrade tells you if it landed somewhere else

`go install` writes to `GOBIN`, or `GOPATH/bin` when that is unset, which is not necessarily the directory holding the binary you run. Previously an upgrade could report success while you kept executing the old one.

1. [ ] Arrange the mismatch on purpose: `export GOBIN=$HOME/go-elsewhere` (a directory that is **not** on your PATH), then trigger the upgrade.
2. [ ] → **Expected**: the upgrade still reports success, **and** warns naming **both absolute paths** — where it wrote and what your shell runs.
3. [ ] → **Expected**: it never silently reports a clean success. If it does, you would keep running the old binary believing you updated, which is the defect this replaced.
4. [ ] If your `gentle-ai` is a symlink into the go-install directory → **Expected**: treated as a match, no warning. A spurious warning there is also a defect.

---

## How to measure properly (read this before reporting)

Four things that made earlier reports measure the wrong thing. They are not bugs, they are environment traps:

**Never write command output inside the repository under test.** The review target is derived from the workspace snapshot, so `gentle-ai ... > out.txt` run from inside the repo adds an untracked file and changes the very thing being measured. A transition proposed before the redirect no longer matches after it, and you get a refusal that has nothing to do with what you were testing. Keep a separate directory:

```
mkdir -p /tmp/rdd-out
cd $HOME/demo
gentle-ai review start --cwd . > /tmp/rdd-out/o.txt 2> /tmp/rdd-out/e.txt
```

This one cost the maintainer an hour of chasing a defect that was his own redirect. Flow 24 turns it into a deliberate test instead.

**If an agent runs it, set `CI=1`.** The consent question only shows up when there is a real terminal. Many agent harnesses allocate a pseudo-terminal, so the tool asks… and nobody answers: the shell hangs until it is killed, and the flow ends up as PARTIAL for a reason that is not the product's.

```
CI=1 gentle-ai review start
```

With `CI=1` the tool reviews anyway and warns on stderr that it did not ask. It is the same path CI already uses. **Exception: Flow 5 is precisely the test for the question**, so that one needs a real terminal and does not take `CI=1`; if your environment does not have one, mark it N/A.

**Exit codes get lost through a pipe.** In bash, `$?` gives you the status of the **last command in the pipeline**, not the binary's. If you run `gentle-ai ... | tee log.txt`, `$?` is `tee`'s and it is always 0. In PowerShell, `$LASTEXITCODE` does give you the binary's, and that is why the same case "behaved differently" between Windows and Linux. To measure properly:

```
gentle-ai review start --projection staged --base-ref HEAD~1 > out.txt 2> err.txt
echo "exit=$?"
```

**`--next-transition` needs the explicit contract.** This is not a bug: passing `--contract` is the opt-in to the negotiated envelope, and leaving it out has its own meaning.

## What to report

Anything that does not match an **Expected** — and anything you find confusing even if it works. Open an issue with: what you tried, what you expected, what you saw, `gentle-ai --version`, OS, and terminal output.

👉 https://github.com/Gentleman-Programming/gentle-ai/issues/new/choose — mention that this is the **2.2.0-rc.1 pre-release**.

If everything worked, comment on PR [#1801](https://github.com/Gentleman-Programming/gentle-ai/pull/1801) with which flows passed and on which platform — that feedback decides the merge.

## What is NOT a bug

- **The gate exits 0 when reviews are off.** It reports `disabled/unmanaged` but does not veto — repository policy rules.
- **`requirements.txt`/`CMakeLists.txt` get one review (tier 1), not zero.** An unreviewed dependency bump would be a security downgrade.
- **With no terminal, the question does not appear and it reviews straight away** (it warns on stderr). Turning a safety net off silently is not an option.
- **"Not now" asks again on the next piece of work.** Per work unit, on purpose.
- **A `.md` with executable content escalates.** The content is read, not the extension.
- **The installed `.claude/CLAUDE.md` escalates if you put it in the diff.** That is what the `.gitignore` in the setup is for.
