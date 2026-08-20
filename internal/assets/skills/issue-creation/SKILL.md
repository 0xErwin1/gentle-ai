---
name: issue-creation
description: "Trigger: issue creation, bug reports, feature requests, or issue approval. Create and triage GitHub issues from repository evidence."
license: Apache-2.0
metadata:
  author: gentleman-programming
  version: "1.3"
---

# Issue Creation

## Activation Contract

Use this skill when drafting, creating, commenting on, triaging, or approving a GitHub issue. Repository policy and its selected YAML Issue Form remain authoritative.

## Hard Rules

- Prefer the fast path: reuse verified current-session facts while they remain current; discover only missing or stale facts.
- Before any needed target policy read or write, resolve the exact target as `[HOST/]OWNER/REPO`. Never assume the current repository.
- YAML Issue Forms are the single format authority. Never use Markdown, a blank body, an alternate publisher, or a browser route as a fallback.
- Complete one open-and-closed duplicate search before a write. Reuse that result while it remains current.
- Never invent required facts, selections, first-person affirmations, labels, approval, or policy. Ask for the smallest missing fact.
- Use only labels declared by the selected form, discovered to exist, and permitted for the actor. Never add `status:approved`.
- Keep the final issue or comment body in a private temporary file outside repositories. Do not print its contents.
- Make one create or comment attempt with no blind retry. Classify it exactly `confirmed | no_write | unknown`; `unknown` stops every later mutation and retry.

## Decision Gates

| Path | Use when | Action |
| --- | --- | --- |
| Fast path | The current session has the exact target and form, reviewed answers and title, current labels and policy, and a completed classifiable duplicate search | Reuse them and enter the common publication flow. |
| Minimal discovery | Any required fact is missing, stale, ambiguous, or belongs to another target | Resolve the target first, then fetch only the missing facts and stop if any remain unknown. |
| Conforming equivalent | An existing issue covers the same behavior and follows the selected form | Comment there instead of creating a duplicate. |
| Nonconforming concrete issue | An existing issue covers the behavior but lacks required form information | Request that its author repair it in place; never auto-rewrite or approve it. |
| Question or triage | Policy routes questions to enabled Discussions, contact links, or review gates | Follow that route; otherwise request the smallest missing decision. |

## Execution Steps

1. Choose the fast path or minimal discovery. When discovery is needed, derive and verify `HOST`, `REPO=OWNER/REPO`, and `TARGET=$HOST/$REPO` from an explicit target or one unambiguous authenticated remote before target reads. Authenticate to `HOST`; discover only missing repository policy, default-branch Issue Forms/config, issue availability, Discussions routing, and labels. Failed or ambiguous discovery stops publication.
2. Complete one duplicate search covering open and closed issues, unless a matching current-session result is still valid:

   ```bash
   gh issue list --repo "$TARGET" --state all --search "$QUERY" --limit 1000
   ```

   Treat saturated or otherwise incomplete results as unknown discovery; narrow read-only search or stop without writing.
3. Select the one YAML form whose declared purpose matches. Process controls in declared order and omit `markdown` guidance:

   | Control | Materialized body |
   | --- | --- |
   | `input` / `textarea` | `### <visible label>` followed by the reviewed answer |
   | `dropdown` | `### <visible label>` followed by exact selected option text in declared order |
   | `checkboxes` | `### <visible label>` followed by each option as `- [x] <exact text>` or `- [ ] <exact text>` |

   Preserve labels, emojis, option text, and values. Enforce every `validations.required` field, required dropdown selection, and individually required checkbox option. Require explicit user affirmation for first-person checkbox text. For `textarea.attributes.render`, fence the answer with the declared language and a fence long enough for its content. Render an unanswered optional control as `_No response_`; stop on malformed, unsupported, missing, or ambiguous required input.
4. Review the exact target, title, selected form, materialized body or comment, and permitted discovered form labels. Pass each label as a separate repeated `--label <label>` option; omit the option when no label applies. For a comment, confirm the duplicate decision and intended in-place request.
5. Create an owner-only temporary directory and `BODY_FILE` (`0700`/`0600`, or strict Windows ACL equivalents), and install cleanup before writing content. Clean up on every stop, signal, failure, `confirmed`, `no_write`, and `unknown` path.
6. Immediately before mutation, perform one practical privacy scan of the title and body for actual local paths, usernames, hostnames, credentials or secrets, private project names, and private network addresses. Replace findings with `<project-name>`, `<user>`, `<hostname>`, or `<token>` as applicable while preserving intentionally public identifiers and useful reproduction structure. Then make only the applicable GitHub CLI attempt:

   ```bash
   gh issue create --repo "$TARGET" --title "$TITLE" --body-file "$BODY_FILE"
   gh issue comment "$NUMBER" --repo "$TARGET" --body-file "$BODY_FILE"
   ```

7. Capture the returned target-host issue or comment identity and read it back from that host. For issues, use `gh issue view "$NUMBER" --repo "$TARGET" --json number,url,title,body,labels`; for comments, use `gh api --hostname "$HOST" "repos/$REPO/issues/comments/$COMMENT_ID"`. Compare issue titles exactly and compare bodies after only CRLF-to-LF and trailing-final-newline normalization.
8. Report exactly one result:
   - `confirmed`: a stable identity was returned and target-host read-back matches; report only labels present in read-back.
   - `no_write`: an authoritative rejection proves no issue or comment could have been created.
   - `unknown`: timeout, lost response, network/5xx ambiguity, missing identity, unavailable read-back, or mismatch leaves the write uncertain. Clean up and stop all mutations and retries.

## Output Contract

Return the exact target, selected YAML form, duplicate decision, mutation kind, stable identity and read-back labels when confirmed, and exactly one of `confirmed | no_write | unknown`. When stopping before mutation, name the missing fact and state that no write occurred.
