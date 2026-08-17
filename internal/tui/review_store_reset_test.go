package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/v2/internal/system"
	"github.com/gentleman-programming/gentle-ai/v2/internal/tui/screens"
)

func reviewStoreResetModel(t *testing.T) Model {
	t.Helper()
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenReviewStoreResetConfirm
	m.Cursor = 0
	return m
}

func settledStoreResetReport() reviewtransaction.StoreResetReport {
	return reviewtransaction.StoreResetReport{
		Schema: reviewtransaction.StoreResetReportSchema, Operation: reviewtransaction.StoreResetPreview,
		Repository: "/repo", StoreRoot: "/repo/.git/gentle-ai",
		Removable: []reviewtransaction.StoreResetEntry{
			{Name: "review-transactions/v2", Reason: "compact authority", Present: true, Files: 12, Bytes: 4096},
		},
		Preserved: []reviewtransaction.StoreResetEntry{
			{Name: "review-mode", Reason: "the kill switch", Present: true, Files: 1, Bytes: 64},
		},
		Settled: 12, Complete: true,
	}
}

// TestWelcomeMenuOffersTheReviewStoreReset proves the action is reachable from
// the TUI at all, and that it sits in the maintenance cluster at the bottom
// rather than among the everyday entries.
func TestWelcomeMenuOffersTheReviewStoreReset(t *testing.T) {
	options := screens.WelcomeOptions(nil, true, false, 0, true)
	reset, backups, uninstall := -1, -1, -1
	for index, option := range options {
		switch option {
		case "Reset review store":
			reset = index
		case "Manage backups":
			backups = index
		case "Managed uninstall":
			uninstall = index
		}
	}
	if reset < 0 {
		t.Fatalf("the welcome menu does not offer the reset: %#v", options)
	}
	if reset != backups+1 || reset != uninstall-1 {
		t.Fatalf("reset at %d is not between backups (%d) and uninstall (%d)", reset, backups, uninstall)
	}
}

// TestWelcomeSelectionEntersTheSurvey proves the menu entry runs the read-only
// survey and lands on the confirmation screen, never straight into a removal.
func TestWelcomeSelectionEntersTheSurvey(t *testing.T) {
	m := NewModel(system.DetectionResult{}, "dev")
	m.Screen = ScreenWelcome
	surveyed := false
	m.ReviewStoreResetSurveyFn = func() (reviewtransaction.StoreResetReport, error) {
		surveyed = true
		return settledStoreResetReport(), nil
	}
	m.ReviewStoreResetFn = func() (reviewtransaction.StoreResetReport, error) {
		t.Fatal("selecting the menu entry applied a reset")
		return reviewtransaction.StoreResetReport{}, nil
	}
	options := screens.WelcomeOptions(m.UpdateResults, m.UpdateCheckDone, m.hasDetectedOpenCode(), len(m.ProfileList), m.hasAgentBuilderEngines())
	for index, option := range options {
		if option == "Reset review store" {
			m.Cursor = index
		}
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	state := updated.(Model)
	if state.Screen != ScreenReviewStoreResetConfirm {
		t.Fatalf("screen = %v, want the confirmation screen", state.Screen)
	}
	if !state.OperationRunning {
		t.Fatal("the survey did not start")
	}
	if state.optionCount() != 0 {
		t.Fatal("a running survey still exposes options")
	}
	if cmd == nil {
		t.Fatal("no survey command was returned")
	}
	state = drainReviewStoreResetCmd(t, state, cmd)
	if !surveyed {
		t.Fatal("the survey function was never called")
	}
	if state.OperationRunning {
		t.Fatal("the survey never finished")
	}
	if state.optionCount() != 2 {
		t.Fatalf("settled survey exposes %d options, want Delete + Cancel", state.optionCount())
	}
}

// drainReviewStoreResetCmd runs a returned command and feeds every message it
// produces back into the model, so a tea.Batch of a worker plus a spinner tick
// resolves the way the runtime would resolve it.
func drainReviewStoreResetCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	switch typed := msg.(type) {
	case tea.BatchMsg:
		for _, batched := range typed {
			m = drainReviewStoreResetCmd(t, m, batched)
		}
		return m
	case nil:
		return m
	default:
		updated, _ := m.Update(msg)
		return updated.(Model)
	}
}

// TestReviewStoreResetConfirmCancels proves the second option is inert.
func TestReviewStoreResetConfirmCancels(t *testing.T) {
	m := reviewStoreResetModel(t)
	m.ReviewStoreResetReport = settledStoreResetReport()
	m.ReviewStoreResetFn = func() (reviewtransaction.StoreResetReport, error) {
		t.Fatal("cancel applied the reset")
		return reviewtransaction.StoreResetReport{}, nil
	}
	m.Cursor = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	state := updated.(Model)
	if state.Screen != ScreenWelcome {
		t.Fatalf("cancel went to %v, want the welcome screen", state.Screen)
	}
	if state.ReviewStoreResetReport.Schema != "" {
		t.Fatal("cancel kept the previous survey around")
	}
}

// TestReviewStoreResetConfirmAppliesOnTheFirstOption is the one path in the TUI
// that destroys anything, and it requires an explicit Enter on an explicitly
// labelled option.
func TestReviewStoreResetConfirmAppliesOnTheFirstOption(t *testing.T) {
	m := reviewStoreResetModel(t)
	m.ReviewStoreResetReport = settledStoreResetReport()
	applied := false
	m.ReviewStoreResetFn = func() (reviewtransaction.StoreResetReport, error) {
		applied = true
		report := settledStoreResetReport()
		report.Operation = reviewtransaction.StoreResetApplied
		report.RemovedFiles, report.RemovedBytes = 12, 4096
		report.Removable[0].Removed = true
		return report, nil
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	state := updated.(Model)
	if !state.OperationRunning {
		t.Fatal("the reset did not start")
	}
	state = drainReviewStoreResetCmd(t, state, cmd)
	if !applied {
		t.Fatal("the reset function was never called")
	}
	if state.Screen != ScreenReviewStoreResetResult {
		t.Fatalf("screen = %v, want the result screen", state.Screen)
	}
	if state.optionCount() != 0 {
		t.Fatal("the result screen exposes phantom options")
	}
	rendered := state.View()
	if !strings.Contains(rendered, "Review store reset") || !strings.Contains(rendered, "4.0 KB") {
		t.Fatalf("result view does not report what was freed:\n%s", rendered)
	}
}

// TestReviewStoreResetConfirmRefusesOpenReviews is the TUI half of the central
// safety rule. There is no cursor position that destroys an open review, and
// the screen names the CLI invocation that overrides it.
func TestReviewStoreResetConfirmRefusesOpenReviews(t *testing.T) {
	m := reviewStoreResetModel(t)
	report := settledStoreResetReport()
	report.InFlight = []reviewtransaction.StoreResetLineage{
		{LineageID: "review-open", State: reviewtransaction.StateReviewing},
	}
	m.ReviewStoreResetReport = report
	m.ReviewStoreResetFn = func() (reviewtransaction.StoreResetReport, error) {
		t.Fatal("the TUI destroyed an open review")
		return reviewtransaction.StoreResetReport{}, nil
	}

	if m.optionCount() != 1 {
		t.Fatalf("blocked survey exposes %d options, want only Back", m.optionCount())
	}
	rendered := m.View()
	if !strings.Contains(rendered, "review-open") {
		t.Fatalf("the screen does not name the open review:\n%s", rendered)
	}
	if !strings.Contains(rendered, "--include-in-flight") {
		t.Fatalf("the screen does not name the override:\n%s", rendered)
	}
	if strings.Contains(rendered, "Delete permanently") {
		t.Fatalf("the screen offered a destructive option:\n%s", rendered)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if state := updated.(Model); state.Screen != ScreenWelcome {
		t.Fatalf("Enter on the blocked screen went to %v", state.Screen)
	}
}

// TestReviewStoreResetSurveyFailureIsReportable proves a store this command
// cannot even read produces an explanation rather than a dead menu entry.
func TestReviewStoreResetSurveyFailureIsReportable(t *testing.T) {
	m := reviewStoreResetModel(t)
	m.ReviewStoreResetSurveyErr = errors.New("not a git repository")

	if m.optionCount() != 1 {
		t.Fatalf("failed survey exposes %d options, want only Back", m.optionCount())
	}
	rendered := m.View()
	if !strings.Contains(rendered, "not a git repository") {
		t.Fatalf("the failure is not shown:\n%s", rendered)
	}
	if strings.Contains(rendered, "Delete permanently") {
		t.Fatalf("a failed survey still offered to delete:\n%s", rendered)
	}
}

// TestReviewStoreResetResultReportsPartialFailure keeps the honesty rule
// visible in the interface a user actually reads.
func TestReviewStoreResetResultReportsPartialFailure(t *testing.T) {
	report := settledStoreResetReport()
	report.Operation = reviewtransaction.StoreResetApplied
	report.Complete = false
	report.RemovedFiles, report.RemovedBytes = 12, 4096
	report.Removable[0].Removed = true
	report.Removable = append(report.Removable, reviewtransaction.StoreResetEntry{
		Name: "reviews", Present: true, Files: 3, Bytes: 900, Skipped: "permission denied",
	})

	rendered := screens.RenderReviewStoreResetResult(report, errors.New("review store reset was incomplete"))
	if !strings.Contains(rendered, "Partially removed") {
		t.Fatalf("a partial run was not reported as partial:\n%s", rendered)
	}
	if strings.Contains(rendered, "✓") {
		t.Fatalf("a partial run claimed success:\n%s", rendered)
	}
	if !strings.Contains(rendered, "reviews: permission denied") {
		t.Fatalf("the skipped category and its reason are missing:\n%s", rendered)
	}
}

// TestReviewStoreResetConfirmNamesWhatItSpares keeps the exclusion list in
// front of the user, not only in the source.
func TestReviewStoreResetConfirmNamesWhatItSpares(t *testing.T) {
	rendered := screens.RenderReviewStoreResetConfirm(settledStoreResetReport(), nil, 0)
	if !strings.Contains(rendered, "Preserved:") || !strings.Contains(rendered, "review-mode") {
		t.Fatalf("the confirmation does not say what it spares:\n%s", rendered)
	}
	if !strings.Contains(rendered, "cannot be undone") {
		t.Fatalf("the confirmation does not say the removal is irreversible:\n%s", rendered)
	}
}
