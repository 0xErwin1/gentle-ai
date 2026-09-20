# Feature: novedades archive in docs/novedades

## Objective
Turn the generated "novedades" PDF into a periodic publication with a fixed identity, archived in the repository so the community can consult past editions and future ones stay reproducible.

## Problem / Why
The 2026-09-20 edition was generated ad hoc from a local content JSON. Nothing in the repository records the structure, the naming convention, or the source of an edition, so the next one could drift in style and old ones could not be regenerated.

## Scope
- `docs/novedades/README.md`: what this publication is, fixed structure, naming convention, how an edition is produced, and the index of editions.
- `docs/novedades/plantilla.json`: the fixed template, derived from the approved 2026-09-20 edition, with placeholder content and comments-by-example.
- `docs/novedades/2026-09-20/`: first archived edition — `novedades.json` (source), both PDFs (dark + light).
- Style/design lives in the global `gentle-docs` skill, not in this repo; the README points at it and records the commit range so an edition can be rebuilt.
- Out of scope: publishing a website, automating generation in CI, changing the skill itself (tracked separately below).

## Constraints
- Fixed structure for every edition, in order: cover + stats, "En 30 segundos", "¿Te afecta?", numbered sections, glosario, cierre, anexo de commits.
- Naming: `gentle-ai-novedades-YYYY-MM-DD[-claro].pdf`, folder `docs/novedades/YYYY-MM-DD/`.
- Every edition records its commit range (`<base>..<head>`) so it is reproducible.
- Reader-facing content in neutral Latin American Spanish; repository docs (README) in English per repo convention — verify against existing docs/ files before writing.
- Never show palette hex codes as visible document text.

## TDD
- Mode: strict where code is written. This feature is documentation + data only, so no unit tests apply.
- Verification instead: regenerate both PDFs from the archived `novedades.json` and confirm page counts match the archived files; `gentle-docs` contrast check passes.

## Delivery
- Route: delegated direct (writer trigger: 2+ non-trivial files). One writer.
- Worktree: `~/work/oss/gentle-ai-worktrees/novedades-archive`, branch `docs/novedades-archive`, base `upstream/main` (f0782af2).
- Commits: Conventional Commits on the branch. Push/PR remain the user's decision.

## Tasks
- [x] T1 `docs/novedades/` skeleton: README (structure, convention, index, how to produce), plantilla.json, first edition folder with source JSON and both PDFs; commit.
- [x] T2 Parent verification: rebuild the PDFs from the archived JSON, compare page counts, review a preview image, confirm the README index matches the files on disk.
- [x] T3 (outside this repo) `gentle-docs` skill: ship the novedades template as a preset so future editions start from it.

## Acceptance criteria
- A reader can open `docs/novedades/README.md` and understand what the publication is, how to read it, and which editions exist.
- Someone with the skill installed can rebuild any archived edition from its JSON and get the same document.
- The 2026-09-20 edition is archived with both themes and its source JSON.

## Progress / Evidence
- 2026-09-20: worktree created; feature document written.
- 2026-09-20: T1 done. Route: delegated direct (writer trigger, 2+ non-trivial files), one writer.
  - docs/ language convention verified: English (checked `docs/usage.md`, `docs/community-roadmap.md`, `docs/agents.md` — `## Section` headings, `← [Back to README](../README.md)` back-links, concise prose). README.md written in English; archived edition content stays in Spanish (per its own `lang: "es"`).
  - Built: `docs/novedades/README.md`, `docs/novedades/plantilla.json`, `docs/novedades/2026-09-20/{novedades.json, gentle-ai-novedades-2026-09-20.pdf, gentle-ai-novedades-2026-09-20-claro.pdf}`.
  - Verification (all commands run in the foreground from the worktree):
    - `build.py docs/novedades/2026-09-20/novedades.json --out <scratchpad>/build-edition --theme both`: exit 0, both PDFs produced.
    - `build.py docs/novedades/plantilla.json --out <scratchpad>/build-plantilla --theme both`: exit 0, both PDFs produced (proves the template is valid and buildable).
    - Page counts via pypdfium2: archived dark 12 / light 12; rebuilt dark 12 / light 12 (match); template dark 8 / light 8 (fewer sections by design, not compared to the archive).
    - `scripts/contrast.py`: "all 25 token pairs pass >= 4.5:1 in every theme", exit 0.
    - Every link/path in the README index (`README.md`, both PDFs, `plantilla.json`, `novedades.json`) confirmed to exist on disk.
  - Commit: see git log on `docs/novedades-archive` for the T1 commit hash and subject.
  - Deviation: none. Open question: none for T1 (T2 parent verification and T3 skill-side preset remain open, as scoped).
- 2026-09-20 T2 (parent, inline): rebuilt both PDFs from the archived `novedades.json` -> 12/12 pages, extracted text identical to the archived copies; `contrast.py` exit 0; preview rendered and reviewed; README index links resolve.
  - Two findings fixed in commit `7e2ba04f`: the README claimed a rebuild is byte-for-byte (false — PDF build metadata changes the hash; page count and text do match), and nothing linked to the archive, so the community could not find it. Added a `Novedades` entry to the README nav.
- 2026-09-20 T3: shipped `assets/examples/novedades-plantilla.json` in the global `gentle-docs` skill and referenced it from SKILL.md; `pytest` -> 26 passed (the new example is covered by the parametrized build test).
- Status: all tasks done. Branch `docs/novedades-archive` has 2 commits, not pushed. Push and PR remain the user's decision.
