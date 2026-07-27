# gentle-ai-bench

Measures the **friction** of driving `gentle-ai`'s review lifecycle, so a
"before" binary and an "after" binary can be compared and the change can be
shown rather than asserted.

It is a **black box**. It drives a `gentle-ai` binary given by `--binary` as a
subprocess and never instruments the product, so it works against any build
including old releases. It is **deterministic and offline**: no model is ever
called. Every journey runs in a fresh temp directory with its own `HOME`,
`XDG_*`, a throwaway git repository and, where the flow needs one, a local bare
remote. It never touches your real config or repositories.

## Its own module, on purpose

`bench/` declares its own `go.mod`, so the root module's `go build ./...`,
`go vet ./...` and `go test ./...` do not see it. That is deliberate: the tool
must never be able to break, slow, or enter a release build of the product it
measures. The cost is that nothing verifies it automatically — build it from
inside this directory:

```
cd bench
go build ./...
go vet ./...
go test ./...
```

The measured binary is passed in with `--binary`, so the tool never depends on
the sources next to it. That is what lets it measure an old release and the
current build with identical code.

## Two modes, two questions

| Mode | Question it answers |
|---|---|
| `run` (driven) | "What does this binary cost to drive through a fixed corpus?" Reproducible, comparable between binaries. |
| `record` + `analyze` (observed) | "What did a real agent actually experience this session?" Honest about one agent, one session, whatever it happened to do. |

Both compute the dimensions with the **same** classifier function. `compare`
refuses to compare a driven run against an observed run: they measure
different populations and the table would be meaningless.

### Driven

```
gentle-ai-bench run --binary /path/to/gentle-ai --out results-after.json
gentle-ai-bench run --binary /path/to/old-gentle-ai --out results-before.json
gentle-ai-bench compare --before results-before.json --after results-after.json
```

`run --only j05-gate-without-any-review,j10-invalid-flag-combination` runs a
subset.

### Observed

```
gentle-ai-bench record --binary $(which gentle-ai) --out session.jsonl
# follow the printed PATH line, then run your agent through the testing guide
gentle-ai-bench analyze --session session.jsonl --out results-observed.json
```

A ready-to-paste prompt for the agent — which starts and closes the recording
itself — lives in [`AGENT-PROMPT.md`](AGENT-PROMPT.md). It carries one rule
worth repeating here: **the agent must not read gentle-ai's source.** An agent
that has read the implementation recovers using knowledge a real user does not
have, so the run comes out clean for the wrong reason. The whole point is
measuring whether the tool explains itself.

`record` writes a directory containing an executable named `gentle-ai` and
prints the one line that puts it first on `PATH`. The shim logs every
invocation and delegates to the real binary, preserving argv, stdin, stdout,
stderr and the exit code. Because it intercepts at the process boundary, it
works with any agent or harness.

**Shim fidelity rule.** A stream that is a character device (a terminal, and
also `/dev/null`) is passed through untouched instead of being teed. Replacing
it with a pipe would flip `gentle-ai`'s own interactivity check — it decides
whether to ask the consent question by testing whether stdin *and* stderr are
character devices — and a benchmark that changes the thing it measures is
worthless. The cost is that such invocations are recorded with
`stdout_captured: false` / `stderr_captured: false`, and the dimensions that
depend on them become `null` rather than a guess.

## The seven dimensions

| # | Dimension | What it counts | How |
|---|---|---|---|
| 1 | `human_prompts` | Times the flow would stop to ask a human | Runs non-TTY. `gentle-ai` prints a consent-skipped notice on **stderr** when it would have asked; the benchmark counts occurrences of that exact string. |
| 2 | `manual_tokens` | Steps needing a hand-assembled authorization | Invocations whose argv carries a non-empty `--maintainer-authorization`. Both `--flag value` and `--flag=value`. |
| 3 | `commands_to_completion` | Binary invocations from start to terminal state | Every product invocation the journey issues. Benchmark instrumentation (capability probes) is **not** counted. |
| 4 | `blocks` | Every non-zero exit or denial, in five buckets | See the classifier below. |
| 5 | `recovery_round_trips` | Commands spent between a block and the flow resuming | From the blocking command up to and including the first subsequent command that is not itself a block. |
| 6 | `model_runs` | Reviewer/lens invocations the flow required, re-runs included | Driven mode: measured — the benchmark issues them. Observed mode: **proxy**, see below. |
| 7 | `human_surface_bytes` | Human-facing narration volume | Total stderr bytes. |

There is one extra, informational field, deliberately **not** one of the seven:

- `git_subprocesses` — git processes the product spawned, counted from
  `GIT_TRACE` lines. In driven mode `GIT_TRACE` points at a per-journey log;
  in observed mode the shim sets it only when the user has not. It is a lower
  bound if a build ever performs git operations in-process. It is reported
  because it is cheap and reliable to observe, not because it is a friction
  dimension.

## The block classifier

This is the load-bearing part and it is **mechanical on purpose**. Given the
same bytes it always returns the same class, so the `in_band` / `out_of_band`
split cannot drift into opinion between two runs or two reviewers. It lives in
one function, `Classify` in `classify.go`, and is unit-tested against recorded
real output in `testdata/observations.json`.

**Is it a block?** Non-zero exit, or a JSON envelope with `allowed: false`, or
a denying `result` (`invalidated`, `scope-changed`, `deny`, `denied`, `stop`,
`blocked`, `corrupted`), or `action: "stop"`. A denial that exits 0 still
counts: the flow cannot proceed.

**Which class?** In this order:

1. The flow continued with no extra command → `self_recovered`.
2. The emitted text (stdout or stderr) contains a runnable `gentle-ai <verb> …`
   command → `in_band`.
3. The stdout JSON envelope carries a `next_action`, `recovery_operation`, or
   `collect.capture_operation` (and its execute-shaped sibling
   `next_transition.execute.operation`) naming an operation that is not
   `stop`/`none`/empty → `in_band`.
4. The corpus declares the refusal correct **and** the exact next-action text
   it quotes is verified present in the emitted bytes → `by_design`.
5. The journey corpus declares that no continuation exists → `dead_end`.
6. Otherwise → `out_of_band`: blocked, and the output named no runnable
   continuation, so the user had to go and look it up.

Two precise sub-rules:

- **"Runnable" excludes templates.** `gentle-ai review validate --gate <gate>`
  is not runnable — the user still has to fill it in — so it does not make a
  block in-band on its own. A line offering both a templated command and a
  clean one counts as in-band on the strength of the clean one.
- **`action` is deliberately not a continuation key.** A gate denial carrying
  `action: "explicit-maintainer-action"` names a posture, not an operation you
  can run.

Two classes are **author-declared** rather than derived, because neither
"nothing exists to run next" nor "nothing *could* honestly exist to run next"
is decidable from outside the binary. Both are set per step in the corpus, and
a mechanically detected continuation always overrides either one.

- `dead_end` (`Step.DeadEnd`) says there is **no next action**. The flow is
  over. The current corpus declares none, so `dead_end` is 0 everywhere — an
  honest 0, not a measured absence of dead ends in the product as a whole.
- `by_design` (`Step.ByDesign`) says there **is** a next action, the product
  already stated it, and it is not expressible as a `gentle-ai` command.

They are opposite answers to "is there anything to do next?", so a step
declaring both is contradicting itself and the run refuses to start.

`by_design` is deliberately the most expensive thing to declare in this
benchmark, because it is the only annotation that can make `out_of_band`
smaller. It costs two things:

- **A shape, from a closed vocabulary.** `operator-knowledge` — the product
  cannot know a value only the operator has. `world-action` — the exit is an
  action, not a command: edit the code, free some disk space, plug the mount
  back in. `human-authority` — the block *is* a human decision, and if a
  command could produce the authorization the gate would be theatre. Not free
  text: an unrecognised shape is a corpus error and `run` exits before driving
  anything.
- **A quote of the product's own next-action text**, which the classifier
  verifies is really in the bytes the product emitted. This is the load-bearing
  half. "No command can exist" never excuses "the message says nothing", so
  `Error: no.` cannot be declared by-design: there is nothing to quote, and a
  quote that is not in the output is not a quote — the declaration does not
  apply and the block stays `out_of_band`.

An exemption is a **reclassification, never a subtraction**: the block is still
a block and still inside `4 blocks (total)`. Every declaration in the run is
printed under *By-design blocks* with its shape and its verified quote,
including the ones that did **not** apply — a declaration the classifier
refused is the first sign the product's message changed under the corpus, so it
is reported rather than dropped.

The corpus declares one, in `j17-bare-repository`
(`operator-knowledge`): `review start` in a bare repository can only offer
`--cwd <path-to-a-checkout>`, an unfillable template, because it cannot know
where the operator's checkout is. What it prints instead is the action —
*"run the same command again from a checkout"* — and that is the string the
classifier checks for.

Observed mode has no corpus, so nothing there can declare an exemption and
`by_design` is 0 by construction, not by measurement. A recorded session's
correct refusals are counted as `out_of_band` exactly as before.

## `unsupported` is never `0`

The "before" binary will be an older release whose CLI surface differs. Before
a step runs, the benchmark probes the binary (`<verb> --help`, uncounted) for
the verb and every flag the step needs. A missing surface records
`unsupported`; the journey aborts cleanly and is **excluded from totals** and
counted separately. In every table an unsupported journey renders as `unsup`,
never as a number.

Runtime detection backs this up, matching on output rather than exit code
alone: `flag provided but not defined`, `unknown … command "…"`,
`unexpected … argument "…"` and friends. Matching on the message is deliberate
— exit codes for "I do not have that flag" are not guaranteed to differ from
ordinary state failures, and counting a missing surface as a state failure
would make an old binary look capable.

**When `--help` is not a help surface.** The `sdd-attempt` operations parse
their own flags and reject `--help` with `flag provided but not defined:
-help` — the same words a genuinely missing flag produces. The default probe
would therefore report a build that fully supports the verb as lacking it. Such
a capability declares `Probe` instead: a complete argv that carries the flag
under test and can only fail on *state*. A build with the flag answers
`sdd-attempt requires --cwd`; a build without it answers `flag provided but not
defined`. Probe invocations are uncounted like every other probe.

## Comparing two binaries

`compare` computes the dimension totals over the **comparable subset**:
journeys that completed in *both* runs. Summing a run of 14 completed journeys
against a run of 5 would produce a large delta that reads as a regression when
it is really just a wider corpus. Excluded journeys are named in the output and
in `excluded_journeys`, never silently dropped, and the per-journey breakdown
still shows every journey with `unsup -> n` where the older binary could not
run it.

## What this deliberately does NOT measure

- **Wall-clock time.** Excluded by design. Review duration is dominated by the
  model provider, which makes it non-comparable between two runs of the same
  binary, let alone between two binaries. The recorded session carries
  timestamps only to order records; no duration is computed or reported.
- **Real model tokens.** Excluded by design: provider-dependent, costly, and
  not reproducible. Where a journey needs reviewer output, it is synthesized
  from the binary's **own** preflight/collect envelope — the subject hash and
  the changed-path manifest come straight from the product, so the capture is
  admitted for the same reason a real reviewer's would be. That is what makes
  "model runs" countable without spending a token.
- **A single composite friction score.** Explicitly rejected. A weighted sum
  can improve while `dead_end` and `out_of_band` increase; collapsing the
  dimensions would hide exactly the regression that matters most. The tables
  print every dimension separately and `compare` emits no aggregate.

## Honesty contract — known gaps

Everything below is a real limitation, stated because a benchmark that quietly
invents a metric is worse than one that admits a gap.

1. **`model_runs` in observed mode is a proxy, not a measurement.** The agent's
   own model calls never cross the process boundary, so the shim cannot see
   them. It counts `review capture-result` invocations (one per lens run, plus
   recaptures; `--preflight` excluded because it reads no result). It is
   emitted with `"derivation": "proxy"` and a note, and `compare` propagates
   the proxy label so it can never be laundered into a measurement by summing.
   In driven mode it *is* measured, because the benchmark issues the runs
   itself.

2. **`human_prompts` never counts a real prompt.** Runs are non-TTY by
   construction, so what is counted is the consent-skipped stderr notice: the
   number of times the tool **would have asked**. The interactive question
   itself (testing-guide flow 5) needs a real terminal and a human answer, and
   is therefore outside what this benchmark can drive. It is not in the corpus.

3. **`human_surface_bytes` is `null` for terminal streams.** See the shim
   fidelity rule above. In driven mode it is always measured.

4. **`git_subprocesses` is a lower bound.** It counts `GIT_TRACE` lines, which
   only appear for real `git` subprocesses. A build performing git work
   in-process would undercount, silently. This is why it is informational and
   not one of the seven.

5. **`dead_end` is author-declared.** See above.

6. **The disabled-reviews gate is exempted by its delivery disposition, and
   that exemption is narrow on purpose.** It answers at exit 0 with
   `allowed: false`, `action: "repository-policy"` and
   `delivery: "disabled/unmanaged"`, and its reason says delivery follows
   ordinary repository policy. Nothing is stopped. Counting it reported the one
   guarantee the kill switch exists to provide as two blocks the product
   inflicted, so `IsBlock` returns false on that disposition alone. The sibling
   `unmanaged`, the switch ON with no receipt yet, stays a block, because there
   the operator really is stopped. Both are pinned from recordings of a real
   binary so they cannot be conflated again. This is the one place the
   classifier reads a field other than exit code and denial shape, and widening
   it would let the product talk its way out of a denial.

7. **The corpus is honest, not exhaustive.** Thirty-six journeys that run end
   to end, weighted toward failure paths because that is where friction lives.
   Testing-guide flows 1 (install) and 8 (no phantom SDD artifacts) are
   inspection steps rather than review-lifecycle friction and are not modelled.

8. **Some edge cases are unreachable from a temp directory and are guide flows
   instead.** A network mount where advisory locks fail in ways that are
   neither "busy" nor "missing", a read-only filesystem, a disk that fills
   during a receipt write, a case-insensitive or Unicode-normalizing volume,
   Windows antivirus holding a file mid-write, Windows long paths, and a system
   clock moving backwards all need a machine this harness cannot build. They
   are flows 27 to 33 of `docs/testing/organic-rdd-testing-guide.md`. A flaky
   journey inside a loop is worse than no journey, because it gets blamed on
   whatever changed last.

9. **`by_design` is an author-declared exemption, and it is the one number in
   here that can be gamed.** It exists because `out_of_band` was counting two
   different things: a defect — the product blocked the operator and gave them
   nothing runnable when a runnable continuation could exist — and a correct
   refusal for which naming a command would mean naming a dead end. Only the
   first is what the release criterion cares about. Splitting them makes the
   defect count mean something; it also opens a channel for laundering real
   defects into a clean bucket. What it costs to declare one is described
   above: a shape from a closed vocabulary, and a quote of the product's own
   next-action text that the classifier verifies against the emitted bytes.
   **The failure mode it introduces:** a declaration is a claim about a message
   the corpus does not own. The quote can keep matching while the sentence
   around it stops being useful, and the harness cannot tell the difference —
   it checks that the words are there, not that they still help. So the
   exemption is never a subtraction (the block stays in the total, in its own
   column, in its own section, quote included) and the honest way to read the
   section is to read the quote, not the count. A declaration that no longer
   applies is printed as stale rather than silently ignored, which catches the
   message disappearing but not the message rotting.

10. **The report is not byte-identical between two runs, though every count
    except one is.** Each journey runs under `os.MkdirTemp`, whose random
    suffix varies in length, and several journeys quote that path back in a
    block message — `j14`, `j17`, `j31`, `j33`, `j34`, `j37`, `j38` and `j39`
    observed so far, and any journey that drives a refusal naming a repository
    path can join them. Which of them actually moves between a given pair of
    runs is chance: the suffix is 9 or 10 digits. So the quoted messages differ
    run to run and
    `human_surface_bytes` wobbles by one byte per affected journey; every block
    classification, every count and `git_subprocesses` are stable. This is why the "byte-identical" claim under
    *Measured* below is scoped to the 14-journey corpus it describes — no
    journey in that corpus echoed a sandbox path. Making it hold again means
    choosing between a fixed-width random suffix (stabilises the numbers, not
    the quoted paths) and a deterministic per-journey path (stabilises both,
    and makes two concurrent runs collide). That is a maintainer's call and it
    has not been made.

## Measured: the current build

`results-after.json` in this directory is a real run of the whole corpus
against `gentle-ai 1.49.1-0.20260726001603-c2b91ac966ca+dirty`, built from
this repository at commit `c2b91ac9`. All 14 journeys
completed; nothing was unsupported. Re-running produces byte-identical numbers,
`git_subprocesses` included.

```
1 human_prompts             6      (one per medium/high-risk `review start`)
2 manual_tokens             1      (`review abandon`)
3 commands_to_completion   92
4 blocks (total)           10
4a   self_recovered         0
4b   in_band                3
4c   out_of_band            7
4d   dead_end               0      (author-declared; corpus declares none)
5 recovery_round_trips      4
6 model_runs               15
7 human_surface_bytes    2605
- git_subprocesses       2787      (informational)
```

Those numbers are the **14-journey** corpus against the binary named above,
kept as-is because they belong to that named build. The corpus has since grown
to 43 journeys; re-run `run` against your own binary rather than reading the
block above as current totals. The row labels moved too: `by_design` did not
exist when this was recorded and is now printed as `4d`, next to the number it
carves out of, with `dead_end` at `4e`.

`results-before.json` is the same corpus against `v2.1.2`, kept as a worked
example of the cross-version path: 5 journeys completed and 9 recorded
`unsupported` (no `review capture-result`, no `review capture-evidence`, no
`review status`, no `review mode`). Nothing crashed and nothing was scored as
zero. On the 5 comparable journeys, blocks fell from 10 to 3 — all seven
removed blocks were `out_of_band` — and `human_surface_bytes` from 604 to 383,
with `commands_to_completion` unchanged at 16.

## The corpus

Journeys are data — a slice of `Step` in `journeys.go`. Adding one is
appending to that slice.

| ID | Flow | Source |
|---|---|---|
| `j01-docs-happy-path` | docs change: review, approve, commit, push gate | guide flow 3 + 9 |
| `j02-high-risk-four-lens` | four lenses, evidence, approval | guide flow 4 + review contract |
| `j03-kill-switch` | disable, start refused, re-enable, review | guide flow 2 |
| `j04-size-does-not-escalate` | 1200 lines of prose still reviews low | guide flow 4 |
| `j05-gate-without-any-review` | gate before any receipt exists | community failure path |
| `j06-pre-push-after-publication` | pre-push after the reviewed commit was pushed | guide flow 9 |
| `j07-disabled-with-stale-receipts` | reviews off with two stale receipts | guide flow 6 + 9 |
| `j08-finalize-without-reviewer-results` | finalize with no reviewer results | community failure path |
| `j09-finalize-without-evidence` | finalize with results but no evidence | guide flow 12 |
| `j10-invalid-flag-combination` | staged projection plus base ref | guide flow 13 |
| `j11-unborn-head` | first commit in a repo with no history | guide flow 10 |
| `j12-rejected-capture-then-recapture` | a rejected reviewer result, then a recapture | community failure path |
| `j13-next-transition-runs-verbatim` | the printed transition executes as printed | guide flow 11 |
| `j14-abandon-needs-a-hand-built-token` | abandoning a lineage needs an assembled authorization | `review abandon` contract |

### Edge cases (`journeys_edge.go`)

Journeys 1 to 14 came from the community testing guide and the failure paths it
collected. Journeys 15 to 36 are the edge cases those flows never reached. Each
one is tied to one of the five shapes a night of real defects clustered into,
and the shape is named in the journey's `Source`. Journeys 37 to 43 in
`journeys_sdd.go` reuse the same vocabulary:

| shape | what it is |
|---|---|
| 1 | **asymmetric comparison** — one operand canonicalized, the other not |
| 2 | **transient read as permanent** — a retryable condition surfacing as terminal or ambiguous |
| 3 | **a guard behind the wrong condition** — a check gated on something that is not its own precondition |
| 4 | **a message naming something that does not work** |
| 5 | **two sources of truth** — a document and the code disagreeing about the same fact |

A journey that stresses none of them is not added: it would only make the
number look covered.

| ID | Flow | Shape |
|---|---|---|
| `j15-linked-worktree` | one repository, two absolute paths, two HEADs, one shared review store | 1 + 5 |
| `j16-detached-head` | the whole cycle with no branch at all | 3 |
| `j17-bare-repository` | a repository with no working tree | 4 + 2 |
| `j18-space-and-non-ascii-path` | repository path with spaces and non-ASCII characters | 1 |
| `j19-submodule-gitlink` | a 160000 index entry with no blob behind it | 1 |
| `j20-symlink-candidate` | mode 120000 whose blob is a path | 1 |
| `j21-mode-only-change` | `100644` → `100755`, identical blob on both sides | 1 |
| `j22-pure-rename` | every byte identical, only the path moved | 1 |
| `j23-deletion-only` | a candidate whose new side is empty | 1 |
| `j24-empty-file` | zero bytes, zero changed lines | 4 |
| `j25-no-trailing-newline` | the last line has no terminator | 1 |
| `j26-crlf-content` | carriage returns survive into the staged blob | 1 |
| `j27-merge-in-progress` | review a conflict resolution before the merge commit exists | 3 |
| `j28-rebase-in-progress` | detached HEAD plus a rebase state directory | 3 |
| `j29-cherry-pick-in-progress` | `CHERRY_PICK_HEAD` present throughout | 3 |
| `j30-kill-switch-flipped-mid-review` | reviews turned off between START and FINALIZE | 3 + 5 |
| `j31-nonsense-mode-value` | a switch record that is readable and holds a value that is not `on`/`off` | 2 + 4 |
| `j32-recovery-of-a-recovery` | the named continuation has to work twice | 4 |
| `j33-escalate-then-recover` | escalate on a failed verification, then recover after fixing it | 2 + 4 |
| `j34-abandon-then-start-again` | an abandoned lineage must not poison the repository | 2 |
| `j35-correction-budget-exactly-zero` | forecasting a correction against a budget of 0 | 3 + 4 |
| `j36-contract-right-name-wrong-version` | `--contract` with the right name and a version this build lacks | 2 + 4 |

### The SDD remediation successor cycle (`journeys_sdd.go`)

Journeys 37 to 43 close the corpus's largest blind spot. A community tester
found a hard deadlock on this path that no internal audit had caught, and two
of its blocks were fixed by hand with nothing in a loop pinning either one. Up
to journey 36 the benchmark reported zero `out_of_band` blocks and zero dead
ends for a surface it had simply never driven.

| ID | Flow | Shape |
|---|---|---|
| `j37-sdd-remediation-self-successor` | a bound passing attempt over a corrected candidate is refused, and the refusal names the finish that IS accepted | 4 |
| `j38-sdd-remediation-distinct-successor` | with a real recovery successor in the way, the same refusal must route to review and must NOT name a finish | 3 + 4 |
| `j39-sdd-remediation-stranded-successor` | a successor that can never be finalized: the named route runs and changes nothing | 4 + 2 |
| `j40-sdd-attempt-reset-after-drift` | terminal attempt plus candidate drift: begin refuses, reset is the only way on | 2 + 4 |
| `j41-kill-switch-versus-sdd-pre-verify` | reviews off, and SDD still routes to a review the operator may not start | 5 |
| `j42-kill-switch-versus-sdd-archive` | reviews off at the archive decision: the product defers and never fabricates an approval | 5 |
| `j43-recovery-guard-rails-as-an-operator-meets-them` | three correct refusals around healthy approved authority, and the exit that is not a command | 4 |

Two of them measure something no test could: `j41` and `j42` each take one item
off the documented known-open list and let the number say whether it is still
open. `j42` is deliberately a journey with **no blocks at all** — the product
half of that limitation is closed, and the pin is an assertion on the envelope
rather than a block count, so a regression fails the journey loudly instead of
passing quietly.

The state these journeys need cannot be built with git alone: an attempt
ordinal, a populated review binding and the leaf/non-leaf topology of a lineage
all live inside the product. Every fixture and composite here therefore reads
them back out of the product (`Sandbox.readBack`, uncounted, `GIT_TRACE`
blanked so its git calls are never charged to the next counted invocation) and
fails the journey when the state is not what it claims — including the two
premises that matter most: that the plain passing finish really does block, and
that the topology really is the leaf or the non-leaf shape the journey says it
is.

**Every fixture proves its own edge case before the journey trusts the result.**
A fixture that sets its edge case up wrongly and then passes is the failure mode
these journeys exist to avoid, so each one reads the state back out of git and
fails the journey when it is not what it claims: the linked worktree asserts
that `.git` is a file and that its git dir differs from the common dir, the
mode-only change asserts the index really went `100644` → `100755` with a
`0\t0` numstat, the mid-operation fixtures assert `MERGE_HEAD` /
`rebase-merge` / `CHERRY_PICK_HEAD` exist *after* the conflict is resolved, and
the nonsense mode record asserts that it still parses as JSON so the journey
cannot silently decay into the already-covered corrupt-record case.

## Layout

```
main.go        run / record / analyze / compare / __shim dispatch
classify.go    Observation, IsBlock, IsUnsupported, Classify   <- the contract
metrics.go     Dimension, BlockCounts, accumulator, aggregate
runner.go      Sandbox, capability probe, journey engine
journeys.go    the corpus, as data — guide flows and their failure paths
journeys_edge.go  the edge-case part of the corpus, with self-proving fixtures
journeys_sdd.go   the SDD remediation successor cycle, the kill switch against
                  SDD, and the recovery guard rails
record.go      the recording shim and session log
analyze.go     observed-mode metrics, same classifier
report.go      plain-text tables and the comparison JSON
testdata/      recorded real gentle-ai output, used by the classifier tests
```
