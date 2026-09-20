# Novedades

← [Back to README](../../README.md)

---

"Novedades" is Gentle AI's changelog for `main`: a summary of what changed, written so someone who
doesn't write code can follow it, with a technical layer underneath for anyone who wants the exact
detail (PR numbers, field names, commit hashes). It is not a marketing document and it does not
replace the commit history — it explains it.

`main` moves fast — roughly 19 non-merge commits a day — so the publication follows it **near-daily**
rather than waiting for a release. That cadence shapes the two rules below: how often an edition
ships, and where its output lives.

## Daily Markdown, per-release PDF

| Cadence | Artifact | Where it lives |
|---|---|---|
| Daily | one Markdown edition | committed in `docs/novedades/` |
| Per release | one consolidated PDF | attached as a GitHub release asset, never committed |

A daily edition is a plain Markdown file, `docs/novedades/YYYY-MM-DD.md` — no per-edition folder,
no generated PDF committed alongside it. Two reasons:

- **The repo stays light.** At a release every ~1.4 days, committing a pair of ~76 KB PDFs per
  daily edition would add tens of megabytes a year to a repository that is currently under 100 MB.
  Markdown text does not carry that cost.
- **Daily editions stay reviewable in a diff.** A Markdown file reviews like any other doc change.
  A binary PDF does not.

The polished, branded PDF still exists — just not one per day, and not committed. Once per release,
the daily Markdown editions published since the previous release are consolidated into a single PDF
(dark + light themes) and attached to that release's GitHub release page as a release asset.

## Structure every edition follows

Every daily edition follows the same fixed sequence, in this order:

1. **Header block** — front matter recording the date, the commit range, and the non-merge commit
   count, followed by a one-line summary with the same figures plus files/lines touched.
2. **"En 30 segundos"** — a short lead paragraph and a handful of one-line highlights.
3. **"¿Te afecta?"** — a table mapping a reader profile ("you use X") to the section that concerns
   them, followed by "Cómo leer este documento" explaining the tag/technical-notes convention.
4. **Numbered sections**, one per notable change, each with: a number, a title in plain language,
   an audience tag (`Te afecta si...` or `Solo cambia por dentro`), a one-line lead, plain-text
   explanation, and optional technical notes, an analogy, or a callout.
5. **Glosario** — the handful of technical terms used, defined in one line each.
6. **Cierre** — a short closing statement.
7. **Anexo** — every commit in the range, grouped by section, with its real hash and subject line.

This structure is fixed on purpose: readers learn the shape once and can skim any future edition
without relearning how to read it.

## Naming and layout

```
docs/novedades/
├── README.md              this file
├── plantilla.md            template for a new daily edition
└── YYYY-MM-DD.md            one Markdown file per daily edition
```

Each `YYYY-MM-DD.md` is the source of truth for that day's edition — reader-facing content in
neutral Latin American Spanish, following `plantilla.md`.

## Reproducibility: the chained range rule

Release-tag anchoring does not survive daily cadence: anchoring every edition to the *latest
release tag* would make most editions repeat the previous day's commits, since releases ship only
every ~1.4 days while editions ship daily. Instead, ranges **chain** from edition to edition:

- **base** = the head commit hash of the *previous* daily edition.
- **If there is no previous edition** (the very first one), base = the latest release tag reachable
  from `main`, recorded together with the hash it resolves to.
- **head** = the `main` tip at publication time, always a fixed commit hash — never `main` or
  `upstream/main`, since those pointers keep moving after the edition ships.
- **Commit count excludes merges:** `git rev-list --count --no-merges <base>..<head>`.

The 2026-09-20 edition is the first one, so its base falls back to the latest release tag:
`v3.4.0 (82a6de96)..f0782af2`, 17 non-merge commits of 19 total in the range
(`git rev-list --count <base>..<head>` for the total, `--no-merges` for the count that appears in
the index and the annex). Every edition after it chains from the previous edition's head hash
instead of re-resolving a release tag.

## Producing a daily edition

1. **Resolve the range.**
   - Find the previous edition's head hash: the most recent `docs/novedades/YYYY-MM-DD.md`'s
     `range` front-matter field, taking the hash after `..`.
   - If there is no previous edition, resolve base = the latest release tag reachable from `main`
     (`git describe --tags --abbrev=0 main`, or the newest `v*` tag that is an ancestor of `main`),
     resolved to its hash with `git log -1 --format=%H <tag>`.
   - head = the current `main` tip, captured as a fixed commit hash (`git log -1 --format=%H main`)
     — never recorded as `upstream/main` or `main`.
2. **Verify subjects with git.** Don't trust remembered commit messages — read the real ones:
   `git log --no-merges --format='%h %s' <base>..<head>` for the list. Every hash and subject in
   the Markdown annex must come from that output, not from memory.
3. **Count commits.** `git rev-list --count --no-merges <base>..<head>` gives the non-merge count
   that belongs in the front matter, the summary line, and the annex heading;
   `git rev-list --count <base>..<head>` gives the total (including merges) for the front matter.
4. **Write the Markdown** from `plantilla.md`, following the fixed structure above. Reader-facing
   content stays in neutral Latin American Spanish; headings and structure mirror the template.
5. **Add the file and update the index below.** Commit `docs/novedades/YYYY-MM-DD.md` on its own.

## Producing the per-release consolidated PDF

The PDF generator, `~/.claude/skills/gentle-docs/assets/build.py`, takes a **JSON content model
only** — it cannot build a PDF from Markdown directly. So the per-release PDF is produced by
authoring a consolidation JSON from the window's daily Markdown editions, not by feeding Markdown
into the generator:

1. **Pick the window.** The daily editions published since the previous release's consolidated PDF,
   up to and including the release being cut.
2. **Author the consolidation JSON** from those editions' content, following
   `~/.claude/skills/gentle-docs/assets/examples/novedades-plantilla.json` in the `gentle-docs`
   skill as the fixed template — do not duplicate that template in this repository; it is the
   canonical source for the JSON shape (cover stats, intro highlights, audience cards, numbered
   sections, glossary, annex).
3. **Build both themes:**
   ```bash
   # one-time setup
   bash ~/.claude/skills/gentle-docs/scripts/setup.sh

   ~/.cache/gentle-docs/venv/bin/python ~/.claude/skills/gentle-docs/assets/build.py \
     <consolidation.json> --out /tmp/novedades-release --theme both
   ```
4. **Check contrast and preview:**
   ```bash
   ~/.cache/gentle-docs/venv/bin/python ~/.claude/skills/gentle-docs/scripts/contrast.py
   ~/.cache/gentle-docs/venv/bin/python ~/.claude/skills/gentle-docs/scripts/preview.py /tmp/novedades-release/<file>.pdf
   ```
5. **Attach both PDFs (dark + light) to the release's GitHub release page as release assets.** They
   are never committed to this repository — the visual design lives entirely in the global
   `gentle-docs` skill, not here, so any past window can be rebuilt from its daily Markdown editions
   plus a current copy of the skill. A rebuild reproduces the same document, not the same bytes: PDF
   output carries build-time metadata, so hashes differ while pages and text match.

## Index of editions

| Date | Commit range | Commits (no-merge) | Files | Lines |
|---|---|---|---|---|
| [2026-09-20](2026-09-20.md) | [`v3.4.0..f0782af2`](https://github.com/Gentleman-Programming/gentle-ai/compare/82a6de96...f0782af2) | 17 | 58 | +2,351 / -365 |
