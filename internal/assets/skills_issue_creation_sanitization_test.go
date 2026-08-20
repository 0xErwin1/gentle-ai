package assets

import (
	"strings"
	"testing"
)

// TestIssueCreationSkillHasSanitizationRule walks the embedded
// issue-creation skill asset and asserts the privacy/sanitization
// requirements from issue #1906 are present. If any required element
// is removed in a future update, this test fails — preventing the
// safety rule from disappearing silently.
func TestIssueCreationSkillHasSanitizationRule(t *testing.T) {
	content := MustRead("skills/issue-creation/SKILL.md")

	requiredSubstrings := []string{
		// Critical Rule #6 title
		"Pre-submission privacy review",
		// Section header
		"Pre-submission Privacy Review",
		// Required placeholders from the issue's "Expected Behavior"
		"<project-name>",
		"<user>",
		"<hostname>",
		"<token>",
		// "Don't redact public identifiers" requirement
		"intentionally public",
	}

	for _, sub := range requiredSubstrings {
		if !strings.Contains(content, sub) {
			t.Errorf("issue-creation SKILL.md is missing required sanitization element %q (see issue #1906)", sub)
		}
	}

	// Version guard: 1.3 adds non-browser YAML-form publication while
	// preserving the sanitization contract introduced in 1.2.
	if !strings.Contains(content, `version: "1.3"`) {
		t.Errorf("issue-creation SKILL.md version must be at least 1.3 and preserve the sanitization rule; check the frontmatter metadata.version field")
	}
}
