package cli

import (
	"context"
	"testing"
)

// Resolution/shadowing tests fake binaries, so explicitly fake execution too.
// The public execution regression leaves this seam real.
func stubDoctorToolProbe(t *testing.T) {
	t.Helper()
	original := doctorToolProbeFn
	t.Cleanup(func() { doctorToolProbeFn = original })
	doctorToolProbeFn = func(context.Context, string, string) error { return nil }
}
