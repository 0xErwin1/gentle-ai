package screens

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestWrapWelcomeBanner_UsesDisplayWidthForWideAdvisory(t *testing.T) {
	const width = 40
	text := "Advisory: 🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀 deployment notice must stay inside the welcome frame"

	wrapped := wrapWelcomeBanner(text, width)

	for i, line := range strings.Split(wrapped, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("wrapped line %d display width = %d, want <= %d\nline: %q\nwrapped:\n%s", i, got, width, line, wrapped)
		}
	}
}
