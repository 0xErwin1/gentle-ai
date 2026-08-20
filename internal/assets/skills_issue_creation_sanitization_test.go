package assets

import (
	"strings"
	"testing"
)

func TestIssueCreationSkillHasSanitizationRule(t *testing.T) {
	content := MustRead("skills/issue-creation/SKILL.md")

	for _, term := range []string{
		"practical privacy scan",
		"Immediately before mutation",
		"<project-name>",
		"<user>",
		"<hostname>",
		"<token>",
		"intentionally public identifiers",
		"useful reproduction structure",
	} {
		if !strings.Contains(content, term) {
			t.Errorf("issue-creation skill is missing privacy contract marker %q (see issue #1906)", term)
		}
	}

	privacyIndex := strings.Index(content, "Immediately before mutation")
	createIndex := strings.Index(content, "gh issue create")
	if privacyIndex == -1 || createIndex == -1 || privacyIndex > createIndex {
		t.Errorf("issue-creation skill must place its privacy scan immediately before publication")
	}

	if !strings.Contains(content, `version: "1.3"`) {
		t.Errorf("issue-creation skill must preserve canonical version 1.3")
	}
}
