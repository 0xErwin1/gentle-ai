package cli

import (
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
)

func TestApplyDeclaredRDDMode(t *testing.T) {
	earlier := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)

	t.Run("records a new mode and stamps the cutoff", func(t *testing.T) {
		got, changed := applyDeclaredRDDMode(state.InstallState{}, model.RDDModeOff, now)

		if !changed {
			t.Fatal("expected the declared mode to be recorded")
		}
		if got.RDDMode != "off" {
			t.Errorf("RDDMode = %q, want %q", got.RDDMode, "off")
		}
		if got.RDDModeRecordedAt == nil || !got.RDDModeRecordedAt.Equal(now) {
			t.Errorf("RDDModeRecordedAt = %v, want %v", got.RDDModeRecordedAt, now)
		}
	})

	// The cutoff decides which candidates a re-enable affects, so refreshing it
	// on an unchanged value would move that boundary forward on every run.
	t.Run("leaves the cutoff alone when the mode is unchanged", func(t *testing.T) {
		persisted := state.InstallState{RDDMode: "on", RDDModeRecordedAt: &earlier}

		got, changed := applyDeclaredRDDMode(persisted, model.RDDModeOn, now)

		if changed {
			t.Fatal("expected no change for an identical mode")
		}
		if got.RDDModeRecordedAt == nil || !got.RDDModeRecordedAt.Equal(earlier) {
			t.Errorf("RDDModeRecordedAt = %v, want the original %v", got.RDDModeRecordedAt, earlier)
		}
	})

	t.Run("stamps a fresh cutoff when the mode flips", func(t *testing.T) {
		persisted := state.InstallState{RDDMode: "off", RDDModeRecordedAt: &earlier}

		got, changed := applyDeclaredRDDMode(persisted, model.RDDModeOn, now)

		if !changed {
			t.Fatal("expected a flip to be recorded")
		}
		if got.RDDModeRecordedAt == nil || !got.RDDModeRecordedAt.Equal(now) {
			t.Errorf("RDDModeRecordedAt = %v, want %v", got.RDDModeRecordedAt, now)
		}
	})

	// Silence is not a policy decision. A document that says nothing about
	// reviews must not overwrite a mode the user set with the kill switch.
	t.Run("an omitted mode leaves persisted state untouched", func(t *testing.T) {
		persisted := state.InstallState{RDDMode: "off", RDDModeRecordedAt: &earlier}

		got, changed := applyDeclaredRDDMode(persisted, "", now)

		if changed {
			t.Fatal("expected no change for an omitted mode")
		}
		if got.RDDMode != "off" {
			t.Errorf("RDDMode = %q, want the persisted %q", got.RDDMode, "off")
		}
		if got.RDDModeRecordedAt == nil || !got.RDDModeRecordedAt.Equal(earlier) {
			t.Errorf("RDDModeRecordedAt = %v, want the original %v", got.RDDModeRecordedAt, earlier)
		}
	})
}
