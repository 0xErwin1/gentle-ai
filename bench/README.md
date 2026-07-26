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
| 4 | `blocks` | Every non-zero exit or denial, in four buckets | See the classifier below. |
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
4. The journey corpus declares that no continuation exists → `dead_end`.
5. Otherwise → `out_of_band`: blocked, and the output named no runnable
   continuation, so the user had to go and look it up.

Two precise sub-rules:

- **"Runnable" excludes templates.** `gentle-ai review validate --gate <gate>`
  is not runnable — the user still has to fill it in — so it does not make a
  block in-band on its own. A line offering both a templated command and a
  clean one counts as in-band on the strength of the clean one.
- **`action` is deliberately not a continuation key.** A gate denial carrying
  `action: "explicit-maintainer-action"` names a posture, not an operation you
  can run.

`dead_end` is the one class that is **author-declared** rather than derived:
"no continuation exists anywhere in the product" is not decidable from outside
the binary. It is set per step in the corpus (`Step.DeadEnd`) and a
mechanically detected continuation always overrides it. The current corpus
declares none, so `dead_end` is 0 everywhere — an honest 0, not a measured
absence of dead ends in the product as a whole.

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

6. **A correct "report, do not block" result still counts as a block.** The
   disabled-reviews gate returns `allowed: false` with exit 0 and
   `action: "repository-policy"` — intended behaviour, and the testing guide
   lists it under "what is not a bug". The classifier counts it as a denial
   because the rule is mechanical and a denial is a denial. Journey
   `j07-disabled-with-stale-receipts` exists to make that visible; read its
   block message before reading its `out_of_band` count as a defect.

7. **The corpus is small and honest, not exhaustive.** Fourteen journeys that
   pass end to end, weighted toward failure paths because that is where
   friction lives. Testing-guide flows 1 (install) and 8 (no phantom SDD
   artifacts) are inspection steps rather than review-lifecycle friction and
   are not modelled.

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

## Layout

```
main.go        run / record / analyze / compare / __shim dispatch
classify.go    Observation, IsBlock, IsUnsupported, Classify   <- the contract
metrics.go     Dimension, BlockCounts, accumulator, aggregate
runner.go      Sandbox, capability probe, journey engine
journeys.go    the corpus, as data
record.go      the recording shim and session log
analyze.go     observed-mode metrics, same classifier
report.go      plain-text tables and the comparison JSON
testdata/      recorded real gentle-ai output, used by the classifier tests
```
