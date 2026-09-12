package config

import "testing"

func TestRetiredOpenCodeProfilesRefuseBeforeAdmission(t *testing.T) {
	calls := 0
	diagnostics := Admit([]byte(`{"version":"v1","selection":{"providers":{"opencode":{"profiles":{"old":{}}}}}}`), func(DesiredState) { calls++ })
	if calls != 0 || len(diagnostics) != 1 {
		t.Fatalf("calls=%d diagnostics=%+v", calls, diagnostics)
	}
	d := diagnostics[0]
	if d.Code != "config.provider.profiles.retired" || d.Path != "$.selection.providers.opencode.profiles" || d.Message != "SDD profiles are retired upstream; remove profiles from the document" || d.Severity != Error {
		t.Fatalf("diagnostic=%+v", d)
	}
}
