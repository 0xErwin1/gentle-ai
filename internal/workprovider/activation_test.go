package workprovider

import (
	"context"
	"errors"
	"testing"
)

func TestActivationParsingAndEnvironmentDefaultFailClosed(t *testing.T) {
	t.Parallel()

	for _, mode := range []ActivationMode{
		ActivationEnabled,
		ActivationRecoveryOnly,
		ActivationReadOnly,
		ActivationDisabled,
	} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			got, err := ParseActivationMode(string(mode))
			if err != nil || got != mode {
				t.Fatalf("ParseActivationMode(%q) = %q, %v", mode, got, err)
			}
		})
	}

	t.Run("unset defaults enabled", func(t *testing.T) {
		t.Parallel()
		resolver := EnvironmentActivationResolver{
			LookupEnv: func(string) (string, bool) { return "", false },
		}
		mode, err := resolver.ResolveActivation(context.Background(), "/repo")
		if err != nil || mode != ActivationEnabled {
			t.Fatalf("ResolveActivation() = %q, %v", mode, err)
		}
	})

	for _, value := range []string{"", "ENABLED", "unknown", " enabled"} {
		value := value
		t.Run("invalid_"+value, func(t *testing.T) {
			t.Parallel()
			resolver := EnvironmentActivationResolver{
				LookupEnv: func(string) (string, bool) { return value, true },
			}
			mode, err := resolver.ResolveActivation(context.Background(), "/repo")
			if mode != ActivationDisabled || !errors.Is(err, ErrInvalidActivationMode) {
				t.Fatalf("ResolveActivation(%q) = %q, %v", value, mode, err)
			}
		})
	}
}

func TestInvalidActivationStopsBeforeRepositoryOpen(t *testing.T) {
	t.Parallel()

	opener := &countingRepositoryOpener{}
	controller := NewController(
		opener,
		StaticActivationResolver{Mode: ActivationMode("unexpected")},
	)
	_, err := controller.Status(context.Background(), validStatusRequest())
	if !errors.Is(err, ErrInvalidActivationMode) {
		t.Fatalf("Status() error = %v", err)
	}
	if opener.calls != 0 {
		t.Fatalf("invalid activation opened repository %d times", opener.calls)
	}
}
