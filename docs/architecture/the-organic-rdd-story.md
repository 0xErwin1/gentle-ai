# The story of fixing RDD

> How two sleepless days, an entire community and three monthly resets turned into a release. For the technical detail, see [organic-rdd.md](organic-rdd.md).

## What RDD is, in one sentence

When you change something important, someone reviews it before it ships. That is all.

The hard part is not the idea, it is that it **must not get in the way**. A system that forces ceremony to change a comma gets uninstalled in three days. One that says nothing when you touch authentication is worth nothing.

## How it works now

You change something. The tool looks at **what** you changed, not how much.

- **Edited a README** → it asks nothing. Zero ceremony.
- **Wrote a thousand lines of documentation** → still nothing. Size does not matter.
- **Touched two lines of login code** → four reviewers.

And if you want none of it:

```
gentle-ai review mode disable
```

Done. It is off. **Not "off but still in your way"** — off. Do whatever you want, and if you turn it back on it tells you it is going to re-validate whatever was never reviewed.

---

## The part nobody tells

### It started badly

The first version of this was not written by hand. It was built with Codex GPT 5.6 in ultra mode, and what came out was enormous.

There was an audit that mentioned enterprise-level requirements. The model **inferred** that HTTP support, remote execution and a whole infrastructure for large teams were needed. And it built all of it. Complete. Coherent. Well made.

And it was not what needed doing.

Nobody had asked for it. It came from deducing a need out of a document that was about something else. Then all of it had to come out, and removing something large and well-built is harder than removing something broken, because **it looks like it works**.

That consumed **three monthly Codex resets**.

### And we had to learn how to ask

What changed was not the model. It was how it was directed.

With gentle-ai and the practices we had been assembling: **phases with explicit contracts, one writer per lane, verify before asserting, and the rule that a failing existing test is never edited — you stop and report.**

That last rule alone caught **nine wrong premises**. Nine times an agent was about to fix something, an old test went red, and it turned out the test was right and the diagnosis was not.

After two days of working flat out, we are still at **66% of the weekly limit**. The difference was not the model. It was the method.

---

## What the community found

This is the part I like most.

We shipped a pre-release and people broke it. In the good way.

**@Wladimirfn, @Denver2828, @MarsSall and @Freedom2828** reported the same failure from four angles. It looked like a Windows bug. It was not: it happened when the reviewed commit had already been published. Denver2828 reached the same diagnosis independently, building the branch with print statements, and **his patch was identical to ours, line for line**.

**@ElCaaarnal** typed a flag by hand and hit something we had announced as fixed. He was right: we had fixed the tool so it stopped *printing* the broken form, not the parser so it would accept it. **The changelog overclaimed and he lost time to it.**

**@ardelperal** reported a command exiting successfully when it should have failed. We investigated: it was a measurement trap. In bash, `$?` gives the status of the *last command in the pipeline*, not the binary. His report was not a bug, but it documented a trap that would have cost the next person an afternoon.

**@Blue-XL** found that a deliberately forged authorization was accepted and stored in the audit record as though genuine. Worse than having no field: an absent authorization is honestly absent, **a wrong one lies**.

**@AlbertGC13** found two things on Windows with a rigour worth copying: he separated explicitly what he had tested from what he had only read in the code, and **stated what he was not claiming**. He found a Git permissions refusal being turned into advice that could not possibly be followed.

**@edwinsaavedran** showed that four macOS defects had escaped because CI never runs on Darwin, and built the case with a link to each one.

**@Matere413** found that a reviewer result our own agents produce is rejected by our own admission, because two of our documents disagree about the required shape.

**@MarcosArispe, @dnlrsls, @GinoL221, @orlo-dragomir, @lu149e, @salema97, @diegofercho21323, @blickcbot, @Deco** and several more kept testing refresh after refresh.

None of those findings came from an internal audit. **They came from people using the tool.**

---

## The audits: the ones that worked and the ones that did not

### The ones that worked

The mechanical ones. The ones derived from the code rather than from a list someone has to remember to update.

One walks the syntax tree looking for error messages that name a command, and checks that the command and its flags **actually exist**. It found messages pointing at things that were not there.

Another rejects new functions nobody calls. When we removed the Codex cleanup, it told us **fifteen functions** had gone dead — an entire parser that existed only for that. We deleted them following that evidence.

That guard was eight hours old when it found its first real defect.

### The ones that did not

The ones that verified something was **emitted**, never that it was usable.

The perfect case: there was a message telling you "to get out of this, run this command". There were tests. They verified the message was emitted, with its exact text. All green.

**Nobody had ever run the command the message named.**

When we ran it, it did not work. We had been sending people into a dead end, with green test coverage, for months.

That is where the rule governing everything else came from:

> **A message may name a command only if running that command resolves the block.**

Naming a dead end is worse than naming nothing.

---

## The benchmark

At some point we stopped arguing about whether it was better and measured it.

The tool counts how often you get stuck and, above all, **how** you get stuck:

- **In band** — it stops you and tells you what to run
- **Out of band** — it stops you and tells you nothing
- **Dead end** — it stops you and there is nothing you can do

It does not measure speed. Speed depends on the provider and the day; friction is yours.

First measurement: **six blocks, every one of them out of band.**

Latest: **zero dead ends, and the single remaining out-of-band block exits successfully** — meaning it is not even a block, it is a report the analyzer over-counts.

A tester said it better than our own tool: *"it communicates the state correctly, but proposes no continuation command"*.

---

## The mistakes I made

Because if this is going to be honest, it goes in whole.

**I wrote guide steps without running them.** Three times. A tester followed them, they did not work, and reported the failure. A new rule came out of that: before naming a continuation, execute it.

**I turned a finding into a documentation patch.** Three different testers could not complete a flow. Instead of taking that as the data it was, I wrote the recipe into the guide. The maintainer called it out: doing that **destroyed the measurement** and hid the defect. I reverted it. The real defect was that the tool had a command emitting exactly what was needed, and no path led to it.

**I staged a file without reading its diff** while an agent was writing in it. I swept up 154 lines of someone else's half-finished work and pushed a branch that did not compile. I have ratchets, guards, and tests that demand commands work. **None of that protects you from a hasty `git add`.**

**I chased a defect that was my own measurement error.** I wrote a command's output inside the repository I was measuring, which added a file, changed the state, and the system correctly refused. I lost an hour. But something good came out: that refusal explained nothing either, so we fixed it, and it is now documented as a trap in the guide.

---

## Where it landed

The four macOS defects: closed and verified on real hardware, not on a synthetic profile.

Windows updates itself for the first time.

Codex used to start up broken after syncing, and now we **do not touch its configuration file at all** — verified with the same inode number before and after, meaning we did not open it for writing, not merely that we wrote the same bytes.

The kill switch is a kill switch.

And things remain open, written down in the technical document, because an honest list of what is missing is worth more than a release claiming everything is done.

---

## What we learned

**A test that verifies something was emitted does not verify it is usable.** That distinction explains nearly every defect in this branch.

**Dead code that is still documented is a lie.** There was a function that installed dependencies. Nothing called it. The docs said the tool installed dependencies. A Linux user read that and expected it to work.

**Over-engineering is harder to remove than a bug.** A bug is visible. An entire architecture nobody asked for, well built and coherent, defends itself.

**The community finds what audits do not.** The four most valuable reports of these days came from people using the tool on their machine, with their repository, with their odd configuration. No internal audit would have found them, because an audit looks for what you already know to look for.

**And the rule that ran over everything else:** if you tell someone what to do, make sure it works.
