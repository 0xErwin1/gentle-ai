package screens

import (
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/v2/internal/tui/styles"
)

func ReviewModeOptions(status reviewtransaction.RDDModeStatus, err error) []string {
	if err != nil && status.Schema == "" {
		return []string{"Continue"}
	}
	if status.Global == reviewtransaction.RDDModeOn {
		return []string{"Disable globally", "Continue"}
	}
	return []string{"Enable globally", "Continue"}
}

func RenderReviewMode(status reviewtransaction.RDDModeStatus, err error, cursor int) string {
	var b strings.Builder
	b.WriteString(styles.TitleStyle.Render("Receipt-Driven Development") + "\n\n")
	b.WriteString(styles.SubtextStyle.Render("Global changes affect all clones; clone-local overrides still win.") + "\n\n")
	if status.Schema != "" {
		for _, line := range []string{
			"Global: " + reviewModeLabel(status.Global),
			"Clone-local: " + reviewModeLabel(status.CloneLocal),
			"Effective: " + reviewModeLabel(status.Effective),
			"Decided by: " + strings.ReplaceAll(string(status.Source), "_", "-"),
		} {
			b.WriteString(styles.HeadingStyle.Render(line) + "\n")
		}
		question := "Do you want to enable RDD globally?"
		if status.Global == reviewtransaction.RDDModeOn {
			question = "Do you want to disable RDD globally?"
		}
		b.WriteString("\n" + styles.SubtextStyle.Render(question) + "\n\n")
	}
	if err != nil {
		b.WriteString(styles.ErrorStyle.Render("✗ Could not load or update review mode") + "\n\n")
		b.WriteString(styles.ErrorStyle.Render("  "+err.Error()) + "\n\n")
	}
	b.WriteString(renderOptions(ReviewModeOptions(status, err), cursor) + "\n")
	b.WriteString(styles.HelpStyle.Render("j/k: navigate • enter: select • esc: continue"))
	return b.String()
}

func reviewModeLabel(mode reviewtransaction.RDDMode) string {
	if mode == reviewtransaction.RDDModeOn {
		return "enabled"
	}
	if mode == reviewtransaction.RDDModeOff {
		return "disabled"
	}
	return "unset"
}
