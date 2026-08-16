package cli

import (
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
)

func TestBackgroundIntentSource(t *testing.T) {
	tests := []struct {
		name      string
		flagSet   bool
		flagValue string
		selection model.Selection
		wantSet   bool
		wantValue string
	}{
		{
			name:      "an explicit flag is the invocation choice",
			flagSet:   true,
			flagValue: "on",
			wantSet:   true,
			wantValue: "on",
		},
		{
			name:      "a declared intent stands in when no flag was passed",
			selection: model.Selection{BackgroundIntent: model.OpenCodeBackgroundOff},
			wantSet:   true,
			wantValue: "off",
		},
		{
			name:    "neither source leaves the choice unresolved",
			wantSet: false,
		},
		{
			name:      "an omitted declaration does not resolve the choice",
			selection: model.Selection{Persona: model.PersonaGentleman},
			wantSet:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set, value := backgroundIntentSource(test.flagSet, test.flagValue, test.selection)

			if set != test.wantSet {
				t.Fatalf("set = %v, want %v", set, test.wantSet)
			}
			if value != test.wantValue {
				t.Errorf("value = %q, want %q", value, test.wantValue)
			}
		})
	}
}

// A declared choice must reach the same persistence the flag reaches, otherwise
// the declarative document would validate a value that never takes effect.
func TestDeclaredBackgroundIntentResolvesAndPersists(t *testing.T) {
	set, value := backgroundIntentSource(false, "", model.Selection{BackgroundIntent: model.OpenCodeBackgroundOn})

	resolution, err := resolveOpenCodeBackgroundCLI(set, value, state.InstallState{})
	if err != nil {
		t.Fatalf("resolve declared background intent: %v", err)
	}

	if resolution.Effective != model.OpenCodeBackgroundOn {
		t.Errorf("Effective = %q, want %q", resolution.Effective, model.OpenCodeBackgroundOn)
	}
	if resolution.Persist != model.OpenCodeBackgroundOn {
		t.Errorf("Persist = %q, want %q", resolution.Persist, model.OpenCodeBackgroundOn)
	}
}

// A declared choice must not be overridden by prior managed state, which the
// resolver ranks below an explicit invocation choice.
func TestDeclaredBackgroundIntentOutranksPriorState(t *testing.T) {
	set, value := backgroundIntentSource(false, "", model.Selection{BackgroundIntent: model.OpenCodeBackgroundOff})

	resolution, err := resolveOpenCodeBackgroundCLI(set, value, state.InstallState{BackgroundIntent: model.OpenCodeBackgroundOn})
	if err != nil {
		t.Fatalf("resolve declared background intent: %v", err)
	}

	if resolution.Effective != model.OpenCodeBackgroundOff {
		t.Errorf("Effective = %q, want %q", resolution.Effective, model.OpenCodeBackgroundOff)
	}
}

// The resolver ranks an explicit invocation choice above the environment, and a
// declared intent enters that same tier. Without this the precedence between a
// document and GENTLE_AI_OPENCODE_BACKGROUND_SUBAGENTS is unproved.
func TestDeclaredBackgroundIntentOutranksEnvironment(t *testing.T) {
	t.Setenv(OpenCodeBackgroundSubagentsEnv, "on")

	set, value := backgroundIntentSource(false, "", model.Selection{BackgroundIntent: model.OpenCodeBackgroundOff})

	resolution, err := resolveOpenCodeBackgroundCLI(set, value, state.InstallState{})
	if err != nil {
		t.Fatalf("resolve declared background intent: %v", err)
	}

	if resolution.Effective != model.OpenCodeBackgroundOff {
		t.Errorf("Effective = %q, want %q", resolution.Effective, model.OpenCodeBackgroundOff)
	}
}

// With nothing declared and no flag, the environment is the next source and must
// still win over prior managed state.
func TestEnvironmentAppliesWhenNothingIsDeclared(t *testing.T) {
	t.Setenv(OpenCodeBackgroundSubagentsEnv, "on")

	set, value := backgroundIntentSource(false, "", model.Selection{})

	resolution, err := resolveOpenCodeBackgroundCLI(set, value, state.InstallState{BackgroundIntent: model.OpenCodeBackgroundOff})
	if err != nil {
		t.Fatalf("resolve background intent: %v", err)
	}

	if resolution.Effective != model.OpenCodeBackgroundOn {
		t.Errorf("Effective = %q, want %q", resolution.Effective, model.OpenCodeBackgroundOn)
	}
}
