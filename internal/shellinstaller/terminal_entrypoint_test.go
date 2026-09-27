package shellinstaller

import "testing"

func TestTerminalEntryPoint(t *testing.T) {
	cases := []struct {
		name  string
		value TerminalEntryPoint
		valid bool
	}{
		{name: "Pi", value: TerminalEntryPointPi, valid: true},
		{name: "Gentle Shell syntax", value: TerminalEntryPointGentleShell, valid: true},
		{name: "empty", value: "", valid: false},
		{name: "forged", value: "other-terminal", valid: false},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.Valid(); got != tt.valid {
				t.Errorf("Valid() = %t, want %t", got, tt.valid)
			}
		})
	}
}

func TestTerminalEntryPointDescribeEffect(t *testing.T) {
	cases := []struct {
		name       string
		entryPoint TerminalEntryPoint
		want       CommandModeEffect
		ok         bool
	}{
		{"Pi replaces only after bound consent", TerminalEntryPointPi,
			CommandModeEffect{ExistingPiReplaceAfterBoundConsent, PiInstallationReplaceExisting}, true},
		{"Gentle Shell isolates Pi", TerminalEntryPointGentleShell,
			CommandModeEffect{ExistingPiPreserve, PiInstallationSeparate}, true},
		{"empty is not a command", "", CommandModeEffect{}, false},
		{"unknown is not a command", "other-terminal", CommandModeEffect{}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.entryPoint.DescribeEffect()
			if ok != tt.ok || got != tt.want {
				t.Errorf("DescribeEffect() = (%+v, %t), want (%+v, %t)", got, ok, tt.want, tt.ok)
			}
		})
	}
}
