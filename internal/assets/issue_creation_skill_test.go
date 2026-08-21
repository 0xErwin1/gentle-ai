package assets

import (
	"regexp"
	"strings"
	"testing"
)

func TestIssueCreationSkillPublicationContract(t *testing.T) {
	content := MustRead("skills/issue-creation/SKILL.md")

	contracts := []struct {
		name  string
		terms []string
	}{
		{"proportional discovery", []string{"Fast path", "Minimal discovery", "missing or stale facts"}},
		{"exact target", []string{"[HOST/]OWNER/REPO", "Never assume the current repository", "TARGET=$HOST/$REPO"}},
		{"single format authority", []string{"YAML Issue Forms are the single format authority", "omit `markdown` guidance"}},
		{"current duplicate search", []string{"open-and-closed duplicate search", "Reuse that result while it remains current", "--state all"}},
		{"duplicate handling", []string{"Comment there instead", "repair it in place", "never auto-rewrite or approve"}},
		{"semantic form translation", []string{"declared order", "`input` / `textarea`", "`dropdown`", "`checkboxes`", "`validations.required`", "first-person", "textarea.attributes.render"}},
		{"private body and read-back lifecycle", []string{"private temporary files outside repositories", "Do not print either file's contents", "owner-only temporary directory", "BODY_FILE` plus `READBACK_FILE", "`0700`/`0600`, or strict Windows ACL equivalents", "Clean up both files on every"}},
		{"file-backed CLI publication", []string{"gh issue create", "--body-file \"$BODY_FILE\"", "gh issue comment"}},
		{"private body-bearing read-back", []string{"read it back from that host into `READBACK_FILE`", "Redirect stdout from both body-bearing read-back commands", "Validate and compare only from `READBACK_FILE`"}},
		{"bounded outcomes", []string{"confirmed | no_write | unknown", "one create or comment attempt with no blind retry", "stop all mutations and retries"}},
		{"target-host verification", []string{"target-host read-back", "CRLF-to-LF", "trailing-final-newline normalization"}},
		{"label policy", []string{"labels declared by the selected form", "permitted for the actor", "Never add `status:approved`"}},
		{"comment parent identity", []string{"returned comment's `issue_url`", "issue `$NUMBER` in `$REPO` on `$HOST`", "absent or mismatched parent identity is `unknown`", "Clean up and stop all mutations and retries"}},
		{"zero-label omission", []string{"omit the option when no label applies"}},
		{"multi-label repetition", []string{"each label as a separate repeated `--label <label>` option", "repeat the final `--label \"$PERMITTED_LABEL\"` segment once per permitted label"}},
	}

	for _, contract := range contracts {
		t.Run(contract.name, func(t *testing.T) {
			for _, term := range contract.terms {
				if !strings.Contains(content, term) {
					t.Errorf("issue-creation skill is missing %s contract marker %q", contract.name, term)
				}
			}
		})
	}

	forbidden := []string{
		"--web",
		"gh browse",
		"POST /repos/",
		"API_BASE",
		"PAYLOAD_FILE",
		"http.Client",
		"curl ",
		"hosted publisher",
		"Go publisher",
		"Markdown template",
		`--body "$BODY"`,
		`${LABEL_ARGS[@]}`,
	}
	for _, term := range forbidden {
		if strings.Contains(content, term) {
			t.Errorf("issue-creation skill contains forbidden alternate route %q", term)
		}
	}

	createCommand := `gh issue create --repo "$TARGET" --title "$TITLE" --body-file "$BODY_FILE" --label "$PERMITTED_LABEL"`
	commentCommand := `gh issue comment "$NUMBER" --repo "$TARGET" --body-file "$BODY_FILE"`
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	executionStart := strings.Index(normalized, "## Execution Steps\n")
	executionEnd := strings.Index(normalized, "## Output Contract\n")
	if executionStart == -1 || executionEnd == -1 || executionStart >= executionEnd {
		t.Fatal("issue-creation skill must contain a concrete Execution Steps section before its Output Contract")
	}
	executionSteps := normalized[executionStart:executionEnd]
	for _, publication := range []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{"create", regexp.MustCompile(`\bgh issue create\b`)},
		{"comment", regexp.MustCompile(`\bgh issue comment\b`)},
	} {
		if count := len(publication.pattern.FindAllStringIndex(executionSteps, -1)); count != 1 {
			t.Errorf("issue-creation skill Execution Steps must contain exactly one %s publication command, found %d", publication.name, count)
		}
	}
	if !strings.Contains(executionSteps, createCommand) || !strings.Contains(executionSteps, commentCommand) {
		t.Error("issue-creation skill Execution Steps must retain the common file-backed publication commands")
	}

	targetIndex := strings.Index(executionSteps, "derive and verify `HOST`, `REPO=OWNER/REPO`, and `TARGET=$HOST/$REPO`")
	discoveryIndex := strings.Index(executionSteps, "Authenticate to `HOST`; discover only missing")
	for _, publication := range []struct {
		name  string
		index int
	}{
		{"create", strings.Index(executionSteps, createCommand)},
		{"comment", strings.Index(executionSteps, commentCommand)},
	} {
		if targetIndex == -1 || discoveryIndex == -1 || publication.index == -1 || targetIndex > discoveryIndex || discoveryIndex > publication.index {
			t.Errorf("issue-creation skill Execution Steps must order target, discovery, then %s publication", publication.name)
		}
	}

	for _, readBackCommand := range []string{
		`gh issue view "$NUMBER" --repo "$TARGET" --json number,url,title,body,labels >"$READBACK_FILE"`,
		`gh api --hostname "$HOST" "repos/$REPO/issues/comments/$COMMENT_ID" >"$READBACK_FILE"`,
	} {
		if !strings.Contains(executionSteps, readBackCommand) {
			t.Errorf("issue-creation skill must redirect body-bearing read-back stdout to private READBACK_FILE: %q", readBackCommand)
		}
	}
}
