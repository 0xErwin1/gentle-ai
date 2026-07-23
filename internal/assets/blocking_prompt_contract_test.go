package assets

import (
	"strings"
	"testing"
)

// TestCoordinatorOrchestratorsCarryLosslessBlockingPromptRule pins the fix for
// issue #615: orchestrators must never summarize a blocking prompt returned by
// a sub-agent or tool. Variants with a native interactive question tool must
// re-issue the prompt through that tool; the rest must restate every option.
func TestCoordinatorOrchestratorsCarryLosslessBlockingPromptRule(t *testing.T) {
	const heading = "**Lossless Blocking Prompts**"

	// path -> required question-tool wording ("" = fallback restatement shape)
	variants := map[string]string{
		"claude/sdd-orchestrator.md":   "`AskUserQuestion` tool",
		"opencode/sdd-orchestrator.md": "`question` tool",
		"cursor/sdd-orchestrator.md":   "",
		"gemini/sdd-orchestrator.md":   "",
		"generic/sdd-orchestrator.md":  "",
		"hermes/sdd-orchestrator.md":   "",
		"kimi/sdd-orchestrator.md":     "",
		"qwen/sdd-orchestrator.md":     "",
	}

	for path, questionTool := range variants {
		t.Run(path, func(t *testing.T) {
			content := MustRead(path)
			if !strings.Contains(content, "You are a COORDINATOR") {
				t.Fatalf("%s no longer carries the COORDINATOR block; update this contract test deliberately", path)
			}
			idx := strings.Index(content, heading)
			if idx == -1 {
				t.Fatalf("%s missing the %s rule", path, heading)
			}
			rule := content[idx:]
			if end := strings.Index(rule, "\n"); end != -1 {
				rule = rule[:end]
			}
			if !strings.Contains(rule, "Never summarize or abbreviate the option list") {
				t.Fatalf("%s lossless rule must forbid summarizing the option list: %q", path, rule)
			}
			if questionTool != "" {
				if !strings.Contains(rule, "re-issue it through the "+questionTool) {
					t.Fatalf("%s lossless rule must re-issue blocking prompts through the %s: %q", path, questionTool, rule)
				}
				if !strings.Contains(rule, "never render it as plain chat text") {
					t.Fatalf("%s lossless rule must forbid plain-chat-text rendering: %q", path, rule)
				}
				return
			}
			if !strings.Contains(rule, "restate every single option fully") {
				t.Fatalf("%s lossless rule must require full restatement: %q", path, rule)
			}
		})
	}
}
