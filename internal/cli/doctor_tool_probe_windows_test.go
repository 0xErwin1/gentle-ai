package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestDoctorBatchProbeRefusalsNameRecovery(t *testing.T) {
	for _, tt := range []struct {
		name, path, want string
		unsetRoot        bool
	}{
		{name: "expansion-sensitive path", path: filepath.Join(t.TempDir(), "unsafe%gga.cmd")},
		{name: "missing system root", path: filepath.Join(t.TempDir(), "gga.cmd"), unsetRoot: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.unsetRoot {
				t.Setenv("SystemRoot", "")
			}
			want := fmt.Sprintf("cannot safely probe batch launcher path %q; move the launcher to a path without expansion characters or quotes, then run 'gentle-ai doctor' again", tt.path)
			if tt.unsetRoot {
				want = "SystemRoot is unset; restore it to your Windows directory, then run 'gentle-ai doctor' again"
			}
			cmd, err := doctorToolProbeCommand(context.Background(), tt.path, "--version")
			if cmd != nil || err == nil || err.Error() != want {
				t.Fatalf("refusal = (%v, %v), want no command and %q", cmd, err, want)
			}
		})
	}
}
