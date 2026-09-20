# Novedades

← [Back to README](../../README.md)

---

"Novedades" is Gentle AI's periodic changelog: a summary of what changed on `main` since the
previous edition, written so someone who doesn't write code can follow it, with a technical layer
underneath for anyone who wants the exact detail (PR numbers, field names, commit hashes). It is
not a marketing document and it does not replace the commit history — it explains it.

Each edition is a self-contained folder with its source data and both rendered PDFs, so any past
edition can be re-read, re-verified, or rebuilt from what's committed here. A rebuild reproduces
the same document, not the same bytes: PDF output carries build-time metadata, so hashes differ
while pages and text match.

## Structure every edition follows

Every edition renders the same fixed sequence, in this order:

1. **Cover** — title, subtitle, and four stats: commits, files touched, lines added, lines
   removed.
2. **"En 30 segundos"** — a short lead paragraph, a handful of one-line highlights, and the table
   of contents.
3. **"¿Te afecta?"** — cards that map a reader profile ("you use X") to the section that concerns
   them, followed by "Cómo leer este documento" explaining the tag/technical-notes convention.
4. **Numbered sections**, one per notable change, each with: a number, a title in plain language,
   an audience tag (`Te afecta si...` or `Solo cambia por dentro`), a one-line lead, plain-text
   explanation, and optional technical notes, an analogy, or a key box.
5. **Glosario** — the handful of technical terms used, defined in one line each.
6. **Cierre** — a short closing statement.
7. **Anexo** — every commit in the range, grouped by section, with its real hash and subject line.

This structure is fixed on purpose: readers learn the shape once and can skim any future edition
without relearning how to read it.

## Naming and layout

```
docs/novedades/
├── README.md              this file
├── plantilla.json          template for a new edition's content JSON
└── YYYY-MM-DD/              one folder per edition
    ├── novedades.json                                source of truth for that edition
    ├── gentle-ai-novedades-YYYY-MM-DD.pdf              dark theme
    └── gentle-ai-novedades-YYYY-MM-DD-claro.pdf         light theme
```

`novedades.json` is the source of truth. Both PDFs are generated from it and are never hand-edited
— if a PDF is wrong, fix the JSON and rebuild.

## Reproducibility

Every edition's `novedades.json` records its commit range in `annex.note`
(`<tag> (<base-hash>)..<head-hash>`). The range is anchored to a release, not to a moving ref:
the base is the latest release tag at the time of the edition, recorded together with the hash it
resolves to, and the head is a fixed commit hash — never `upstream/main`, `main`, or any other
branch pointer that keeps moving after the edition ships. Anchoring this way keeps the exact set
of commits it describes recoverable from git indefinitely, and the document itself can always be
rebuilt. The visual design (fonts, colors, layout, contrast rules) lives entirely in the global
`gentle-docs` skill, not in this repository — rebuilding from an archived JSON with a current copy
of the skill reproduces the same document. Compare a rebuild by page count and text, never by file
hash.

To rebuild an edition (replace the date below):

```bash
# one-time setup
bash ~/.claude/skills/gentle-docs/scripts/setup.sh

# build both themes
~/.cache/gentle-docs/venv/bin/python ~/.claude/skills/gentle-docs/assets/build.py \
  docs/novedades/2026-09-20/novedades.json --out /tmp/novedades-rebuild --theme both

# check every color pair still meets contrast
~/.cache/gentle-docs/venv/bin/python ~/.claude/skills/gentle-docs/scripts/contrast.py

# render a PNG preview per PDF to look at before calling it done
~/.cache/gentle-docs/venv/bin/python ~/.claude/skills/gentle-docs/scripts/preview.py /tmp/novedades-rebuild/<file>.pdf
```

## Producing a new edition

1. **Resolve the range.** The base is the latest release tag reachable from `main`
   (`git describe --tags --abbrev=0 main`, or the newest `v*` tag that is an ancestor of `main`),
   resolved to its hash with `git log -1 --format=%H <tag>`. The head is the current `main` tip,
   captured as a fixed commit hash (`git log -1 --format=%H main`) — never recorded as
   `upstream/main` or `main`, since that pointer keeps moving after the edition ships.
2. **Verify subjects with git.** Don't trust remembered commit messages — read the real ones:
   `git log --oneline <base-hash>..<head-hash>` for the list, `git log -1 --format=%s <hash>` per
   commit if you need the exact subject. `build.py` never shells out to git itself, so every hash
   and subject in `annex.groups[].rows` must already be correct in the JSON. Merge commits are
   excluded from the commit count: `git rev-list --count --no-merges <base-hash>..<head-hash>`
   gives the number that belongs in the index below (the 2026-09-20 edition has 19 commits in the
   range, 17 non-merge).
3. **Write the JSON** from `plantilla.json`, following the fixed structure above and the
   `gentle-docs` skill's `references/components.md` for the full block catalog (`p`, `bullets`,
   `tech`, `analogy`, `keybox`, `h2`, and the rest).
4. **Build both themes** with `build.py --theme both` and run `contrast.py`.
5. **Review the preview.** Render a PNG per PDF with `scripts/preview.py` and actually look at it
   before calling the edition done.
6. **Add the folder and update the index below.** Commit `novedades.json` and both PDFs together
   under `docs/novedades/YYYY-MM-DD/`.

## Index of editions

| Date | Commit range | Commits | Files | Lines | PDFs |
|---|---|---|---|---|---|
| 2026-09-20 | [`v3.4.0..f0782af2`](https://github.com/Gentleman-Programming/gentle-ai/compare/82a6de96...f0782af2) | 17 | 58 | +2,351 / -365 | [dark](2026-09-20/gentle-ai-novedades-2026-09-20.pdf) · [light](2026-09-20/gentle-ai-novedades-2026-09-20-claro.pdf) |
