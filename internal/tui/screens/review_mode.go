package screens

import (
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/v2/internal/tui/styles"
)

func ReviewModeOptions() []string { return []string{"Enable globally", "Disable globally", "Back"} }

func RenderReviewMode(status reviewtransaction.RDDModeStatus, err error, cursor int) string {
	var b strings.Builder
	b.WriteString(styles.TitleStyle.Render("Receipt-Driven Development"))
	b.WriteString("\n\n")
	b.WriteString(styles.SubtextStyle.Render("Global actions affect all clones."))
	b.WriteString("\n\n")
	if err != nil {
		b.WriteString(styles.ErrorStyle.Render("✗ Could not update review mode"))
		b.WriteString("\n\n")
		b.WriteString(styles.ErrorStyle.Render("  " + err.Error()))
		b.WriteString("\n\n")
	} else {
		for _, line := range []string{
			"Global: " + reviewModeLabel(status.Global),
			"Clone-local: " + reviewModeLabel(status.CloneLocal),
			"Effective: " + reviewModeLabel(status.Effective),
			"Decided by: " + reviewModeSourceLabel(status.Source),
		} {
			b.WriteString(styles.HeadingStyle.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(renderOptions(ReviewModeOptions(), cursor))
	b.WriteString("\n")
	b.WriteString(styles.HelpStyle.Render("j/k: navigate • enter: select • esc: back"))
	return b.String()
}

func reviewModeSourceLabel(source reviewtransaction.RDDModeSource) string {
	if source == reviewtransaction.RDDModeSourceCloneLocal {
		return "clone-local"
	}
	return string(source)
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
