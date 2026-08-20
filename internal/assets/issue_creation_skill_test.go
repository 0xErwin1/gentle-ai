package assets

import (
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
		{"private body lifecycle", []string{"private temporary file outside repositories", "owner-only temporary directory", "Clean up on every"}},
		{"file-backed CLI publication", []string{"gh issue create", "--body-file \"$BODY_FILE\"", "gh issue comment"}},
		{"bounded outcomes", []string{"confirmed | no_write | unknown", "one create or comment attempt with no blind retry", "stop all mutations and retries"}},
		{"target-host verification", []string{"target-host read-back", "CRLF-to-LF", "trailing-final-newline normalization"}},
		{"label policy", []string{"labels declared by the selected form", "permitted for the actor", "Never add `status:approved`"}},
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
	if strings.Count(normalized, "\n   "+createCommand+"\n") != 1 {
		t.Errorf("issue-creation skill must contain one common create command")
	}
	if strings.Count(content, commentCommand) != 1 {
		t.Errorf("issue-creation skill must contain one common comment command")
	}

	targetIndex := strings.Index(content, "[HOST/]OWNER/REPO")
	discoveryIndex := strings.Index(content, "When discovery is needed")
	createIndex := strings.Index(content, createCommand)
	if targetIndex == -1 || discoveryIndex == -1 || createIndex == -1 || targetIndex > discoveryIndex || discoveryIndex > createIndex {
		t.Errorf("issue-creation skill must resolve the exact target before discovery and publication")
	}
}
