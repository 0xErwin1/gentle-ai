package shellinstaller

import "testing"

func TestProfile(t *testing.T) {
	t.Run("new install defaults to stable Pi", func(t *testing.T) {
		profile := NewInstallDefaults()
		if profile.Channel != ChannelStable {
			t.Errorf("Channel = %q, want %q", profile.Channel, ChannelStable)
		}
		if profile.TerminalEntryPoint != TerminalEntryPointPi {
			t.Errorf("TerminalEntryPoint = %q, want %q", profile.TerminalEntryPoint, TerminalEntryPointPi)
		}
		if err := profile.Validate(); err != nil {
			t.Fatalf("default profile should validate: %v", err)
		}
	})

	t.Run("accepts legacy zero and explicit Pi targets", func(t *testing.T) {
		for _, target := range []TerminalEntryPoint{"", TerminalEntryPointPi} {
			profile := Profile{Channel: ChannelStable, TerminalEntryPoint: target}
			if err := profile.Validate(); err != nil {
				t.Errorf("target %q should validate: %v", target, err)
			}
		}
	})

	t.Run("rejects unauthorized or forged targets", func(t *testing.T) {
		for _, target := range []TerminalEntryPoint{TerminalEntryPointGentleShell, "other-terminal"} {
			profile := Profile{Channel: ChannelStable, TerminalEntryPoint: target}
			if err := profile.Validate(); err == nil {
				t.Errorf("target %q should be rejected", target)
			}
		}
	})

	t.Run("rejects invalid channels", func(t *testing.T) {
		for _, channel := range []Channel{"", "beta"} {
			profile := Profile{Channel: channel, TerminalEntryPoint: TerminalEntryPointPi}
			if err := profile.Validate(); err == nil {
				t.Errorf("channel %q should be rejected", channel)
			}
		}
	})
}

func TestProfileCommandModeIntentIsNotInstallAuthority(t *testing.T) {
	cases := []struct {
		name       string
		channel    Channel
		entryPoint TerminalEntryPoint
		wantEffect CommandModeEffect
		validateOK bool
	}{
		{"Stable Pi", ChannelStable, TerminalEntryPointPi,
			CommandModeEffect{ExistingPiReplaceAfterBoundConsent, PiInstallationReplaceExisting}, true},
		{"Main Pi", ChannelMain, TerminalEntryPointPi,
			CommandModeEffect{ExistingPiReplaceAfterBoundConsent, PiInstallationReplaceExisting}, true},
		{"Stable separate Gentle Shell", ChannelStable, TerminalEntryPointGentleShell,
			CommandModeEffect{ExistingPiPreserve, PiInstallationSeparate}, false},
		{"Main separate Gentle Shell", ChannelMain, TerminalEntryPointGentleShell,
			CommandModeEffect{ExistingPiPreserve, PiInstallationSeparate}, false},
		{"legacy zero is Pi intent", ChannelStable, "",
			CommandModeEffect{ExistingPiReplaceAfterBoundConsent, PiInstallationReplaceExisting}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			profile := Profile{Channel: tt.channel, TerminalEntryPoint: tt.entryPoint}
			intent, err := profile.DescribeCommandModeIntent()
			if err != nil {
				t.Fatalf("DescribeCommandModeIntent() error = %v", err)
			}
			entryPoint := tt.entryPoint
			if entryPoint == "" {
				entryPoint = TerminalEntryPointPi
			}
			if intent.Channel != tt.channel || intent.EntryPoint != entryPoint || intent.Effect != tt.wantEffect {
				t.Errorf("intent = %+v, want channel %q, entry point %q, effect %+v",
					intent, tt.channel, entryPoint, tt.wantEffect)
			}
			if valid := profile.Validate() == nil; valid != tt.validateOK {
				t.Errorf("Validate() success = %t, want %t", valid, tt.validateOK)
			}
		})
	}
}

func TestProfileCommandModeIntentRejectsInvalidSyntax(t *testing.T) {
	for _, profile := range []Profile{
		{Channel: "beta", TerminalEntryPoint: TerminalEntryPointPi},
		{Channel: ChannelStable, TerminalEntryPoint: "other-terminal"},
	} {
		if _, err := profile.DescribeCommandModeIntent(); err == nil {
			t.Errorf("profile %+v should not describe a command-mode intent", profile)
		}
	}
}
