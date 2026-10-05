package shellinstaller

import (
	"reflect"
	"testing"
)

// Source DATA only: neither Go execution nor product probing is authorized.
func assertSeparateRefusal(t *testing.T, profile Profile, selectors SeparateBoundarySelectors) SeparateBoundaryRefusal {
	t.Helper()
	result := DescribeSeparateBoundary(profile, selectors)
	if result.Kind != SeparateBoundaryNotAuthorized {
		t.Fatalf("positive/empty status from separate boundary: %#v", result)
	}
	for index, requirement := range result.Requirements {
		if requirement == "" {
			t.Fatalf("empty requirement %d: %#v", index, result)
		}
	}
	for _, name := range []string{"Approved", "Ready", "Executable", "Execute", "Install", "Home", "Path"} {
		if _, exists := reflect.TypeOf(result).FieldByName(name); exists {
			t.Fatalf("positive result field %s", name)
		}
	}
	return result
}

func TestSeparateBoundaryDefaultAndBothChannelsRemainRefused(t *testing.T) {
	for _, channel := range []Channel{ChannelStable, ChannelMain} {
		t.Run(string(channel), func(t *testing.T) {
			profile := Profile{Channel: channel, TerminalEntryPoint: TerminalEntryPointGentleShell}
			base := assertSeparateRefusal(t, profile, SeparateBoundarySelectors{})
			if base.Channel != channel || base.Rejected != 0 {
				t.Fatalf("valid channel/empty observations drifted: %#v", base)
			}
			proposed := assertSeparateRefusal(t, profile, SeparateBoundarySelectors{
				ProposedExecutable: "/opt/gentle-shell/bin/pi",
				ProposedHome:       "/opt/gentle-shell/home",
				ProposedLoad:       "/opt/gentle-shell/load",
			})
			if proposed.Requirements != base.Requirements || proposed.Rejected != 0 {
				t.Fatalf("absolute proposal manufactured evidence: %#v", proposed)
			}
		})
	}
}

func TestSeparateBoundaryRequiresExactGentleShellIntent(t *testing.T) {
	cases := []struct {
		profile Profile
		reason  SeparateBoundaryReject
	}{
		{Profile{Channel: ChannelStable}, RejectSeparateCommand},
		{Profile{Channel: ChannelMain, TerminalEntryPoint: TerminalEntryPointPi}, RejectSeparateCommand},
		{Profile{Channel: ChannelStable, TerminalEntryPoint: TerminalEntryPoint("unknown")}, RejectSeparateCommand},
		{Profile{Channel: Channel("unverified"), TerminalEntryPoint: TerminalEntryPointGentleShell}, RejectSeparateChannel},
		{Profile{TerminalEntryPoint: TerminalEntryPointGentleShell}, RejectSeparateChannel},
	}
	for _, tc := range cases {
		result := assertSeparateRefusal(t, tc.profile, SeparateBoundarySelectors{})
		if result.Rejected&tc.reason == 0 {
			t.Fatalf("command/channel accepted: %#v", result)
		}
	}
}

func TestSeparateBoundaryRejectsObservedResolverAndLifecycleSelectors(t *testing.T) {
	cases := []struct {
		name      string
		selectors SeparateBoundarySelectors
		reason    SeparateBoundaryReject
	}{
		{"GENTLE_SHELL_PI", SeparateBoundarySelectors{ResolverEnvGentleShellPi: true}, RejectSeparateResolverEnv},
		{"optional peer", SeparateBoundarySelectors{OptionalPeerWithoutPhysicalProof: true}, RejectSeparateOptionalPeer},
		{"PATH fallback", SeparateBoundarySelectors{PathFallback: true}, RejectSeparatePathFallback},
		{"--link", SeparateBoundarySelectors{LinkFlag: true}, RejectSeparateLinkFlag},
		{"saved link", SeparateBoundarySelectors{SavedLink: true}, RejectSeparateSavedLink},
		{"path", SeparateBoundarySelectors{ExplicitPath: true}, RejectSeparateExplicitPath},
		{"--home", SeparateBoundarySelectors{HomeFlag: true}, RejectSeparateHomeFlag},
		{"GENTLE_SHELL_HOME", SeparateBoundarySelectors{GentleShellHomeEnv: true}, RejectSeparateHomeEnv},
		{"PI_CODING_AGENT_DIR", SeparateBoundarySelectors{InheritedPiCodingAgentDir: true}, RejectSeparateInheritedPiDir},
		{"default home", SeparateBoundarySelectors{UnresolvedDefaultHome: true}, RejectSeparateDefaultHome},
		{"postinstall", SeparateBoundarySelectors{LifecyclePostinstall: true}, RejectSeparatePostinstall},
		{"auto-provision", SeparateBoundarySelectors{LauncherAutoProvision: true}, RejectSeparateAutoProvision},
	}
	profile := Profile{Channel: ChannelStable, TerminalEntryPoint: TerminalEntryPointGentleShell}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.selectors
			result := assertSeparateRefusal(t, profile, tc.selectors)
			if result.Rejected != tc.reason || tc.selectors != before {
				t.Fatalf("selector escaped/was mutated: %#v %#v", result, tc.selectors)
			}
		})
	}
}

func TestSeparateBoundaryRefusesEachClaimedPositiveIndependently(t *testing.T) {
	profile := Profile{Channel: ChannelMain, TerminalEntryPoint: TerminalEntryPointGentleShell}
	baseline := assertSeparateRefusal(t, profile, SeparateBoundarySelectors{})
	cases := []struct {
		name      string
		selectors SeparateBoundarySelectors
	}{
		{"executable string", SeparateBoundarySelectors{ClaimedVerifiedExecutable: "verified"}},
		{"home string", SeparateBoundarySelectors{ClaimedVerifiedHome: "verified"}},
		{"load string", SeparateBoundarySelectors{ClaimedVerifiedLoad: "verified"}},
		{"approved bool", SeparateBoundarySelectors{ClaimedApproved: true}},
		{"ready bool", SeparateBoundarySelectors{ClaimedReady: true}},
		{"executable bool", SeparateBoundarySelectors{ClaimedExecutable: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := assertSeparateRefusal(t, profile, tc.selectors)
			if result.Rejected != RejectSeparateClaimedAuthority || result.Requirements != baseline.Requirements {
				t.Fatalf("individual claimed-positive field escaped: %#v", result)
			}
		})
	}
}

func TestSeparateBoundaryNeverTrustsClaimedVerificationOrApproval(t *testing.T) {
	profile := Profile{Channel: ChannelMain, TerminalEntryPoint: TerminalEntryPointGentleShell}
	selectors := SeparateBoundarySelectors{
		ProposedExecutable:        "/isolated/bin/gentle-shell",
		ProposedHome:              "/isolated/home",
		ProposedLoad:              "/isolated/modules",
		ClaimedVerifiedExecutable: "verified",
		ClaimedVerifiedHome:       "verified",
		ClaimedVerifiedLoad:       "verified",
		ClaimedApproved:           true,
		ClaimedReady:              true,
		ClaimedExecutable:         true,
		ResolverEnvGentleShellPi:   true,
		InheritedPiCodingAgentDir:  true,
	}
	result := assertSeparateRefusal(t, profile, selectors)
	want := RejectSeparateClaimedAuthority | RejectSeparateResolverEnv | RejectSeparateInheritedPiDir
	if result.Channel != ChannelMain || result.Rejected != want {
		t.Fatalf("forged positive field or alias slipped through: %#v", result)
	}
	if assertSeparateRefusal(t, profile, SeparateBoundarySelectors{}).Requirements != result.Requirements {
		t.Fatal("claimed verification removed an independent requirement")
	}
}
