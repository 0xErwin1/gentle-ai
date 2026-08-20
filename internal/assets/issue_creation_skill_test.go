package assets

import (
	"strings"
	"testing"
)

func TestIssueCreationSkillPublicationContract(t *testing.T) {
	content := MustRead("skills/issue-creation/SKILL.md")

	for _, forbidden := range []string{
		"Gentleman-Programming",
		"agent-teams-lite",
		"bug_report.yml",
		"feature_request.yml",
		"status:needs-review",
		"status:approved",
		"Blank issues are disabled",
		"Every issue gets",
		"A maintainer MUST add",
		"--web",
		"gh browse",
		"Open the web",
		"web issue chooser",
		"stop for human completion",
		"human browser completion",
		"browser fallback",
		"browser handoff",
		"complete in a browser",
		"finish in a browser",
		"open a browser",
		"launch a browser",
		"web form completion",
		`.md files are Markdown templates`,
		`--body "$BODY"`,
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("consumer issue-creation skill contains forbidden policy or publication route %q", forbidden)
		}
	}

	for _, required := range []string{
		"[HOST/]OWNER/REPO",
		"before reading target policy or mutating GitHub",
		"Never assume the current repository",
		"YAML Issue Forms are the single semantic authority",
		"Materialize one reviewed form body",
		"Each transport must perform its own target-aware discovery, mutation, and read-back",
		"Explicit safe transport choice",
		"Prefer a fully authenticated CLI; otherwise use fully authenticated REST",
		"REST requested or `gh` unavailable",
		"Prior transport proved `no_write`",
		"The other transport may be selected once",
		"Resolve `HOST`, `REPO=OWNER/REPO`, and `TARGET=$HOST/$REPO`",
		"gh auth status --hostname \"$HOST\"",
		"gh repo view \"$TARGET\" --json nameWithOwner,url,defaultBranchRef,hasDiscussionsEnabled,hasIssuesEnabled",
		"repos/$REPO/git/trees/$DEFAULT_BRANCH?recursive=1",
		"repos/$REPO/contents/.github/ISSUE_TEMPLATE?ref=$DEFAULT_BRANCH",
		"gh api --hostname \"$HOST\" --paginate \"repos/$REPO/labels?per_page=100\" --jq '.[].name'",
		"default-branch `CONTRIBUTING*`, `README*`, `.github/ISSUE_TEMPLATE/config.yml`, and every YAML form",
		"gh issue list --repo \"$TARGET\" --state all --search \"$QUERY\" --limit 1000",
		"REST path must not invoke `gh`",
		"discovered GHES API URL or `https://$HOST/api/v3` otherwise",
		"preconfigured credential helper, netrc, or approved secret environment",
		"keeps the secret out of argv and logs",
		"Never ask anyone to paste a token into chat",
		"put credentials in a body or payload file",
		"GET /user",
		"GET /repos/$REPO",
		"GET /repos/$REPO/contents/{path}?ref=$DEFAULT_BRANCH",
		"GET /repos/$REPO/labels?per_page=100",
		"one `GET /search/issues?q=repo:$REPO+is:issue+$QUERY` covering open and closed duplicates",
		"Treat `markdown` blocks only as guidance",
		"supported controls in declared order",
		"as `### {label}`",
		"unchanged with emojis",
		"`input` and `textarea` values",
		"selected `dropdown` option text",
		"`- [x] {text}` or `- [ ] {text}`",
		"`textarea.render`",
		"fenced code block using the declared language",
		"Enforce every required field and option",
		"First-person attestations require explicit user affirmation",
		"fail closed with the exact missing fact",
		"Comment on an equivalent conforming tracker",
		"Ask its author to edit it in place",
		"never rewrite or approve it automatically",
		"owner-only temporary directory",
		"cleanup on success, failure, signal, `confirmed`, `no_write`, and `unknown`",
		"immediately before the first mutation",
		"Never log, print, trace, or pass body/payload contents in argv",
		"gh issue create --repo \"$TARGET\" --title \"$TITLE\" --body-file \"$BODY_FILE\"",
		"gh issue comment \"$NUMBER\" --repo \"$TARGET\" --body-file \"$BODY_FILE\"",
		"POST /repos/$REPO/issues",
		"POST /repos/$REPO/issues/$NUMBER/comments",
		"GET /repos/$REPO/issues/$NUMBER",
		"GET /repos/$REPO/issues/comments/$COMMENT_ID",
		"stable target-host identity",
		"read-back matches the expected title/body or comment body",
		"Every create/comment attempt ends exactly",
		"`confirmed`",
		"`no_write`",
		"`unknown`",
		"After `unknown`",
		"Never create, comment, edit, label, blindly reconcile, retry, or switch transport",
		"Only authoritative `no_write` permits one attempt through the other fully authenticated transport",
		"LABEL_ARGS=()",
		"LABEL_ARGS+=(--label \"$LABEL\")",
		"only labels configured by the selected form that exist and the actor may apply",
		"Read-back is authoritative for applied labels",
		"no asynchronous external-label workflow",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("consumer issue-creation skill missing publication contract %q", required)
		}
	}

	failedDiscoveryGuard := "Run all discovery below through that transport before any mutation:"
	guardIndex := strings.Index(content, failedDiscoveryGuard)
	if guardIndex == -1 {
		t.Errorf("consumer issue-creation skill missing failed-discovery guard %q", failedDiscoveryGuard)
	}

	publicationCommands := []string{
		"gh issue create --repo \"$TARGET\" --title \"$TITLE\" --body-file \"$BODY_FILE\" \"${LABEL_ARGS[@]}\"",
		"POST /repos/$REPO/issues",
	}
	requiredDiscoverySteps := []string{
		"[HOST/]OWNER/REPO",
		"before reading target policy or mutating GitHub",
		"gh auth status --hostname \"$HOST\"",
		"gh repo view \"$TARGET\" --json nameWithOwner,url,defaultBranchRef,hasDiscussionsEnabled,hasIssuesEnabled",
		"repos/$REPO/git/trees/$DEFAULT_BRANCH?recursive=1",
		"repos/$REPO/contents/.github/ISSUE_TEMPLATE?ref=$DEFAULT_BRANCH",
		"gh api --hostname \"$HOST\" --paginate \"repos/$REPO/labels?per_page=100\" --jq '.[].name'",
		"gh issue list --repo \"$TARGET\" --state all --search \"$QUERY\" --limit 1000",
		"REST path must not invoke `gh`",
		"GET /user",
		"GET /repos/$REPO/contents/{path}?ref=$DEFAULT_BRANCH",
		"GET /search/issues?q=repo:$REPO+is:issue+$QUERY",
		"Treat `markdown` blocks only as guidance",
		"Enforce every required field and option",
		"immediately before the first mutation",
	}
	for _, issueCreationCommand := range publicationCommands {
		commandIndex := strings.Index(content, issueCreationCommand)
		if commandIndex == -1 {
			t.Errorf("consumer issue-creation skill missing publication command %q", issueCreationCommand)
			continue
		}
		if guardIndex >= commandIndex {
			t.Errorf("consumer issue-creation skill must place failed-discovery guard before %q", issueCreationCommand)
		}
		for _, discoveryStep := range requiredDiscoverySteps {
			discoveryStepIndex := strings.Index(content, discoveryStep)
			if discoveryStepIndex == -1 {
				t.Errorf("consumer issue-creation skill missing discovery step %q before %q", discoveryStep, issueCreationCommand)
			} else if discoveryStepIndex >= commandIndex {
				t.Errorf("consumer issue-creation skill must place discovery step %q before %q", discoveryStep, issueCreationCommand)
			}
		}
	}
}
