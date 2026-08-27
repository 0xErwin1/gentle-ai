package tui

import (
	"context"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/v2/internal/system"
	"github.com/gentleman-programming/gentle-ai/v2/internal/tui/screens"
	"strings"
	"testing"
)

func settleReviewMode(t *testing.T, model Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected an asynchronous command")
	}
	message := cmd()
	if batch, ok := message.(tea.BatchMsg); ok {
		for _, command := range batch {
			model = settleReviewMode(t, model, command)
		}
		return model
	}
	updated, _ := model.Update(message)
	return updated.(Model)
}
func TestReviewModeTUI(t *testing.T) {
	status := func(global, local, effective reviewtransaction.RDDMode, source reviewtransaction.RDDModeSource) reviewtransaction.RDDModeStatus {
		return reviewtransaction.RDDModeStatus{Schema: reviewtransaction.RDDModeStatusSchema, Global: global, CloneLocal: local, Effective: effective, Source: source}
	}
	m := NewModel(system.DetectionResult{}, "dev")
	m.Cursor = len(screens.WelcomeOptions(m.UpdateResults, m.UpdateCheckDone, false, 0, true)) - 4
	loads, mutations := 0, []bool{}
	m.ReviewModeCwdFn = func() (string, error) { return "/repo", nil }
	m.ReviewModeStatusFn = func(context.Context, string) (reviewtransaction.RDDModeStatus, error) {
		loads++
		return status(reviewtransaction.RDDModeOff, reviewtransaction.RDDModeUnset, reviewtransaction.RDDModeOff, reviewtransaction.RDDModeSourceDefault), nil
	}
	m.ReviewModeSetGlobalFn = func(_ context.Context, cwd string, enabled bool) (reviewtransaction.RDDModeStatus, error) {
		if cwd != "/repo" {
			t.Fatalf("mutation cwd = %q", cwd)
		}
		mutations = append(mutations, enabled)
		if enabled {
			return status(reviewtransaction.RDDModeOn, reviewtransaction.RDDModeOff, reviewtransaction.RDDModeOff, reviewtransaction.RDDModeSourceCloneLocal), nil
		}
		return status(reviewtransaction.RDDModeOff, reviewtransaction.RDDModeUnset, reviewtransaction.RDDModeOff, reviewtransaction.RDDModeSourceGlobal), nil
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.Screen != ScreenReviewMode || !m.OperationRunning || loads != 0 || len(mutations) != 0 {
		t.Fatalf("entry = screen %v running %t loads %d mutations %v", m.Screen, m.OperationRunning, loads, mutations)
	}
	m = settleReviewMode(t, m, cmd)
	for _, want := range []string{"Global: disabled", "Clone-local: unset", "Effective: disabled", "Decided by: default"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("initial view missing %q:\n%s", want, m.View())
		}
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = settleReviewMode(t, updated.(Model), cmd)
	if len(mutations) != 1 || !mutations[0] || !strings.Contains(m.View(), "Decided by: clone-local") || !strings.Contains(m.View(), "Effective: disabled") {
		t.Fatalf("global enable was not truthfully refreshed: %v\n%s", mutations, m.View())
	}
	m.Cursor = 1
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = settleReviewMode(t, updated.(Model), cmd)
	if len(mutations) != 2 || mutations[1] || !strings.Contains(m.View(), "Decided by: global") {
		t.Fatalf("global disable = %v\n%s", mutations, m.View())
	}
	m.ReviewModeCwdFn = func() (string, error) { return "", errors.New("not a repository") }
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = settleReviewMode(t, updated.(Model), cmd)
	if !strings.Contains(m.View(), "not a repository") || strings.Contains(m.View(), "✓ Receipt-Driven Development") {
		t.Fatalf("cwd error was not truthful:\n%s", m.View())
	}
	m.ReviewModeCwdFn = func() (string, error) { return "/repo", nil }
	m.ReviewModeSetGlobalFn = func(context.Context, string, bool) (reviewtransaction.RDDModeStatus, error) {
		return reviewtransaction.RDDModeStatus{}, errors.New("revision conflict")
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = settleReviewMode(t, updated.(Model), cmd)
	if !strings.Contains(m.View(), "revision conflict") {
		t.Fatalf("mutation error was not truthful:\n%s", m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.(Model).Screen != ScreenWelcome {
		t.Fatalf("back = %v, want welcome", updated.(Model).Screen)
	}
}
