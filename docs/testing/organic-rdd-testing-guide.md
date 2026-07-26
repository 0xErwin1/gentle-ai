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
4. [ ] `gentle-ai review start --cwd $HOME/demo` → **Expected**: refused, naming that reviews are turned off. It does NOT hang, it does NOT review.
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
6. [ ] Turn reviews on and repeat the gate → **Expected**: `result: "scope-changed"` with a readable reason naming the delivery base. No corruption either.

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

2. [ ] Look at the `token` of each argument in the response → **Expected**: each one is a complete flag ready to run (`--target=sha256:...`), not a name and a value sitting apart.
3. [ ] **Copy and paste the command exactly as it came out**, without fixing anything → **Expected**: it runs. It used to print `--captured-results true` (with a space) and the parser rejected it.

### Flow 12: Finalize without evidence says what to do

1. [ ] With a review in `validating` state and no captured evidence, run `review finalize --lineage <id>` → **Expected**: an error that **names both commands** to get out (`review capture-evidence` and then the finalize with `--captured-evidence`). It used to say `continue the current review state` and nothing ever happened.

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

## How to measure properly (read this before reporting)

Three things that made earlier reports measure the wrong thing. They are not bugs, they are environment traps:

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
