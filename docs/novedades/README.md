# Novedades

← [Back to README](../../README.md)

---

"Novedades" is Gentle AI's periodic changelog: a summary of what changed on `main` since the
previous edition, written so someone who doesn't write code can follow it, with a technical layer
underneath for anyone who wants the exact detail (PR numbers, field names, commit hashes). It is
not a marketing document and it does not replace the commit history — it explains it.

Each edition is a self-contained folder with its source data and both rendered PDFs, so any past
edition can be re-read, re-verified, or rebuilt byte-for-byte from what's committed here.

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

Every edition's `novedades.json` records its commit range in `annex.note` (`<base>..<head>`), so
the exact set of commits it describes is always recoverable from git, and the document itself can
always be rebuilt. The visual design (fonts, colors, layout, contrast rules) lives entirely in the
global `gentle-docs` skill, not in this repository — rebuilding from an archived JSON with a
current copy of the skill reproduces the same document.

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

1. **Resolve the range.** The new edition starts where the previous one's `annex.note` head commit
   ends. Find it in the latest folder under `docs/novedades/`.
2. **Verify subjects with git.** Don't trust remembered commit messages — read the real ones:
   `git log --oneline <previous-head>..main` for the list, `git log -1 --format=%s <hash>` per
   commit if you need the exact subject. `build.py` never shells out to git itself, so every hash
   and subject in `annex.groups[].rows` must already be correct in the JSON.
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
| 2026-09-20 | [`82a6de96..f0782af2`](https://github.com/Gentleman-Programming/gentle-ai/compare/82a6de96...f0782af2) | 17 | 58 | +2,351 / -365 | [dark](2026-09-20/gentle-ai-novedades-2026-09-20.pdf) · [light](2026-09-20/gentle-ai-novedades-2026-09-20-claro.pdf) |
