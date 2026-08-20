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

Load this skill when creating, drafting, triaging, commenting on, or approving a GitHub issue.

## Hard Rules

- Resolve the exact target as `[HOST/]OWNER/REPO` before reading target policy or mutating GitHub. Never assume the current repository.
- Repository YAML Issue Forms are the single semantic authority. Never use a browser publication route, Markdown template, blank body, hosted publisher, daemon, queue, database, or Go publisher as another authority or fallback.
- Materialize one reviewed form body, then use it unchanged through either the GitHub CLI or direct authenticated REST. Each transport must perform its own target-aware discovery, mutation, and read-back; do not mix transports within an attempt.
- Never invent evidence, required answers, attestations, labels, approval, or repository policy. Fail closed and name the exact missing fact.
- Never ask anyone to paste a token into chat, expose credentials in argv/logs, or put credentials in a body or payload file.
- Every create/comment attempt ends exactly `confirmed`, `no_write`, or `unknown`. An `unknown` result prohibits every later GitHub create, comment, edit, label, or retry, including through the other transport.

## Decision Gates

| Decision | Action |
| --- | --- |
| Explicit safe transport choice | Honor it when fully authenticated for the exact target. |
| No transport choice | Prefer a fully authenticated CLI; otherwise use fully authenticated REST. |
| REST requested or `gh` unavailable | Use REST if its independent credentials and complete path pass discovery. |
| Prior transport proved `no_write` | The other transport may be selected once if it is independently authenticated and complete. |
| Previous result is `unknown` | Stop all GitHub mutation; never retry or switch transport. |
| No unambiguous matching YAML form | Follow discovered contact/Discussions policy or stop with the exact missing fact. |
| Equivalent conforming issue exists | Comment there instead of creating a duplicate. |
| Concrete issue exists but does not conform | Ask its author to edit it in place; never rewrite or approve it automatically. |

## Execution Steps

1. Resolve `HOST`, `REPO=OWNER/REPO`, and `TARGET=$HOST/$REPO` from an explicit target or one unambiguous authenticated git remote. For `OWNER/REPO`, resolve the host from authenticated configuration or the remote; ask if ambiguous. Verify all three values before policy reads.
2. Select one complete transport. Run all discovery below through that transport before any mutation:

```bash
gh auth status --hostname "$HOST"
gh repo view "$TARGET" --json nameWithOwner,url,defaultBranchRef,hasDiscussionsEnabled,hasIssuesEnabled
gh api --hostname "$HOST" "repos/$REPO/git/trees/$DEFAULT_BRANCH?recursive=1"
gh api --hostname "$HOST" "repos/$REPO/contents/.github/ISSUE_TEMPLATE?ref=$DEFAULT_BRANCH"
gh api --hostname "$HOST" --paginate "repos/$REPO/labels?per_page=100" --jq '.[].name'
```

   The CLI path uses `gh api --hostname "$HOST"` to read default-branch `CONTRIBUTING*`, `README*`, `.github/ISSUE_TEMPLATE/config.yml`, and every YAML form found by the tree response. Immediately before a possible create, search open and closed issues exactly once:

   ```bash
   gh issue list --repo "$TARGET" --state all --search "$QUERY" --limit 1000
   ```

   Treat a saturated or incomplete result as failed discovery; narrow read-only search or stop, but never create speculatively.
3. The REST path must not invoke `gh`. Derive `API_BASE` from target-host configuration: use `https://api.github.com` only for `github.com`, and the discovered GHES API URL or `https://$HOST/api/v3` otherwise. Authenticate from a preconfigured credential helper, netrc, or approved secret environment through an HTTP client that keeps the secret out of argv and logs. Disable tracing. Use paginated target-host operations equivalent to the CLI path:

   - `GET /user` and `GET /repos/$REPO` for actor, authentication, issues availability, default branch, and Discussions;
   - `GET /repos/$REPO/git/trees/$DEFAULT_BRANCH?recursive=1` plus `GET /repos/$REPO/contents/{path}?ref=$DEFAULT_BRANCH` for contribution policy, forms, and config;
   - `GET /repos/$REPO/labels?per_page=100` for all existing labels;
   - one `GET /search/issues?q=repo:$REPO+is:issue+$QUERY` covering open and closed duplicates.

4. Stop before publication if authentication, exact target, metadata, default-branch policy/forms/config, duplicate completeness, or required permissions are unknown, or if issues are disabled.

## Form Materialization

Choose the YAML form whose declared purpose matches the report. Treat `markdown` blocks only as guidance; never emit them. Traverse supported controls in declared order:

- Emit each visible `label`, unchanged with emojis, as `### {label}`.
- Preserve supplied `input` and `textarea` values verbatim beneath the heading.
- Emit selected `dropdown` option text in declared option order; enforce required selection and allowed options.
- Emit every `checkboxes` option as `- [x] {text}` or `- [ ] {text}`, preserving text and selection. Require every mandatory option.
- For `textarea.render`, place the value in a correctly sized fenced code block using the declared language.

Enforce every required field and option. First-person attestations require explicit user affirmation; never infer affirmation from evidence. Unsupported control types, malformed schemas, unsafe fence ambiguity, or ambiguous answers fail closed with the exact missing fact. Do not parse a Markdown template or invent a no-template body.

## Duplicate And Approval Gate

Search once as specified above before creating. Comment on an equivalent conforming tracker. For a concrete nonconforming tracker, prepare a request for its author to edit in place; publishing that request is a comment attempt under the same privacy and outcome contract. Publish only after the target, title, selected form, materialized body, labels, and repository approval gate have been reviewed and authorized.

Apply only labels configured by the selected form that exist and the actor may apply:

```bash
LABEL_ARGS=()
LABEL_ARGS+=(--label "$LABEL")
```

An empty array applies no label. The REST payload may contain only the same validated labels.

## Pre-submission Privacy Review

Pre-submission privacy review is mandatory. Scan every title, issue/comment body, and final REST payload immediately before the first mutation. The scan replaces — never deletes — environment-specific data with explicit placeholders so the reproduction still teaches:

| Category | Replace with | Example (before → after) |
|----------|---------------|---------------------------|
| Private project names | `<project-name>` | `my-private-project-b` → `<project-name>` |
| Usernames | `<user>` | `C:\Users\my-real-username\go\bin` → `C:\Users\<user>\go\bin` |
| Hostnames | `<hostname>` | `devbox-macbook.local` → `<hostname>` |
| Home paths | `/home/<user>` or `C:\Users\<user>` | (covered above) |
| API keys, tokens, passwords | `<token>` / `<password>` | `ghp_abc123...` → `<token>` |
| Internal ports / hostnames | `<host>:<port>` | `10.0.0.42:5432` → `<host>:<port>` |

Do NOT redact intentionally public identifiers: tool names (`gentle-ai`, `engram`, `go`, `node`, `python`), package names, public documentation URLs, generic example domains (`example.com`, `localhost`). Keep reproduction structure with placeholders — never redact an example into nothingness.

**Rule of thumb:** if the reader can run the reproduction step after you replace every identifier with its placeholder, the sanitization is correct. If a step becomes impossible (because the placeholder consumed a needed value), that step needs the value — and you should mark it `<value-required>` and explain in the body what the user should fill in.

## Private Files And Publication

Before writing content, create an owner-only temporary directory and owner-only `BODY_FILE`, REST `PAYLOAD_FILE`, and any separate auth config (`0700`/`0600` or strict Windows ACL equivalents). Install trap/finally cleanup before populating them; cleanup on success, failure, signal, `confirmed`, `no_write`, and `unknown`. Never log, print, trace, or pass body/payload contents in argv. Re-run the privacy scan after the final content change and immediately before the first mutation.

CLI publication must use files:

```bash
gh issue create --repo "$TARGET" --title "$TITLE" --body-file "$BODY_FILE" "${LABEL_ARGS[@]}"
gh issue comment "$NUMBER" --repo "$TARGET" --body-file "$BODY_FILE"
```

REST publication sends the private JSON payload to `POST /repos/$REPO/issues` or `POST /repos/$REPO/issues/$NUMBER/comments`; never interpolate its body into argv. Capture the returned issue number/URL or comment ID/URL, then read it back from the target host. The CLI uses `gh issue view "$NUMBER" --repo "$TARGET" --json number,url,title,body,labels` or target-host `gh api` for a comment; REST uses `GET /repos/$REPO/issues/$NUMBER` or `GET /repos/$REPO/issues/comments/$COMMENT_ID`.

Classify each attempt exactly once:

- `confirmed`: a stable target-host identity was returned and read-back matches the expected title/body or comment body. Report only labels present in read-back; GitHub may drop unauthorized labels.
- `no_write`: an authoritative authenticated response proves rejection before acceptance and proves no issue/comment may have been created.
- `unknown`: timeout, network/5xx ambiguity, lost response, missing identity, failed/unavailable read-back, or content mismatch leaves mutation uncertain.

After `unknown`, clean up and stop all GitHub mutation. Never create, comment, edit, label, blindly reconcile, retry, or switch transport. Only authoritative `no_write` permits one attempt through the other fully authenticated transport.

## Labels And Approval

Treat labels and approval gates as conditional. Wait when repository policy requires maintainer approval, and do not invent status or priority taxonomy. Read-back is authoritative for applied labels; no asynchronous external-label workflow belongs in this flow.

## Questions And Discussions

Use Discussions only when target metadata enables it and target policy routes the question there. Otherwise report the documented support/contact route or ask where it belongs. Never open a publication page or use another repository's Discussions.

## Triage Decision

Before approving or closing an issue, verify:

- it describes a concrete bug or scoped improvement rather than an unsupported question;
- it is not a duplicate;
- the report contains enough evidence for an implementation decision;
- the requested behavior is in repository scope;
- labels and status changes follow the current repository's policy.

If any point is uncertain, keep the issue in the repository's review state and request the smallest missing evidence.

## Output Contract

Return the exact target and transport, selected YAML form, duplicate decision, stable issue/comment identity when confirmed, read-back labels, and one terminal result: `confirmed`, `no_write`, or `unknown`. Name missing facts and state that no mutation occurred when stopping before an attempt. Never claim publication or labels without authoritative target-host read-back.
