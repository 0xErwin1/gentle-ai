package shellinstaller

import (
	"errors"
	"fmt"
	"testing"
)

func TestPrivateRuntimeErrorClassification(t *testing.T) {
	cause := errors.New("source preimage differs")
	for _, tc := range []struct {
		name  string
		cause error
	}{
		{"with cause", cause},
		{"without cause", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := &PrivateRuntimeError{
				Kind: "source", Workspace: "/owned/workspace", Destination: "/owned/shell", Cause: tc.cause,
			}
			if got := failure.Error(); got != "private installation: source" {
				t.Fatalf("error = %q", got)
			}
			if got := errors.Unwrap(failure); got != tc.cause {
				t.Fatalf("cause = %v, want %v", got, tc.cause)
			}
			wrapped := fmt.Errorf("shell entry: %w", failure)
			var classified *PrivateRuntimeError
			if !errors.As(wrapped, &classified) || classified != failure {
				t.Fatalf("classification lost failure: %v", wrapped)
			}
			if tc.cause != nil && !errors.Is(wrapped, tc.cause) {
				t.Fatalf("classification lost cause: %v", wrapped)
			}
		})
	}
}
