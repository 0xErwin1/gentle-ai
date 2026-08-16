package config

import "testing"

func TestRetiredFlatProfilesRefuseBeforeAdmission(t *testing.T) {
	cases := []struct{ input, code, path, message string }{
		{`{"version":"v1","selection":{"profiles":[{"name":"old"}]}}`, "config.provider.profiles.retired", "$.selection.profiles", "SDD profiles are retired upstream; remove profiles from the document"},
		{`{"version":"v1","selection":{"sddProfileStrategy":"generated-multi"}}`, "config.provider.profile-strategy.retired", "$.selection.sddProfileStrategy", "SDD profile strategy is retired upstream; remove sddProfileStrategy from the document"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			calls := 0
			diagnostics := Admit([]byte(tc.input), func(DesiredState) { calls++ })
			if calls != 0 {
				t.Fatalf("admitted retired input: calls=%d", calls)
			}
			if len(diagnostics) != 1 {
				t.Fatalf("diagnostics=%+v", diagnostics)
			}
			d := diagnostics[0]
			if d.Code != tc.code || d.Path != tc.path || d.Message != tc.message || d.Severity != Error {
				t.Fatalf("diagnostic=%+v", d)
			}
		})
	}
}

func TestDefaultFlatContractStillAdmits(t *testing.T) {
	calls := 0
	diagnostics := Admit([]byte(`{"version":"v1","selection":{"agents":["codex"]}}`), func(DesiredState) { calls++ })
	if len(diagnostics) != 0 || calls != 1 {
		t.Fatalf("diagnostics=%+v calls=%d", diagnostics, calls)
	}
}
