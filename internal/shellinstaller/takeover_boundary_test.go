package shellinstaller

import "testing"

func TestPiTakeoverRequiresExplicitCommandAndIndependentEvidence(t *testing.T) {
	cases := []struct {
		name        string
		profile     Profile
		want        PiTakeoverReject
		wantChannel Channel
	}{
		{"stable Pi", Profile{ChannelStable, TerminalEntryPointPi}, 0, ChannelStable},
		{"main Pi has no source authority", Profile{ChannelMain, TerminalEntryPointPi}, RejectTakeoverMainSource, ChannelMain},
		{"legacy zero is not takeover consent", Profile{ChannelStable, ""}, RejectTakeoverCommand, ChannelStable},
		{"separate command is not takeover", Profile{ChannelStable, TerminalEntryPointGentleShell}, RejectTakeoverCommand, ChannelStable},
		{"invalid channel", Profile{"floating", TerminalEntryPointPi}, RejectTakeoverChannel, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DescribePiTakeoverBoundary(tc.profile, PiTakeoverClaims{})
			if got.Kind != PiTakeoverNotAuthorized || got.Rejected != tc.want || got.Channel != tc.wantChannel {
				t.Fatalf("refusal = %+v, want not-authorized, rejected %b and channel %q", got, tc.want, tc.wantChannel)
			}
			assertCompleteTakeoverRequirements(t, got)
		})
	}
}

func TestPiTakeoverCallerClaimsCannotSupplyAuthority(t *testing.T) {
	profile := Profile{Channel: ChannelStable, TerminalEntryPoint: TerminalEntryPointPi}
	cases := []struct {
		name   string
		claims PiTakeoverClaims
	}{
		{"executable path", PiTakeoverClaims{ExistingExecutable: "/claimed/pi"}},
		{"home path", PiTakeoverClaims{ExistingHome: "/claimed/home"}},
		{"physical instance", PiTakeoverClaims{PhysicalInstanceID: "claimed-id"}},
		{"source digest", PiTakeoverClaims{SourceDigest: "claimed-digest"}},
		{"exact actions", PiTakeoverClaims{ExactActions: "claimed-actions"}},
		{"consent", PiTakeoverClaims{ConsentBinding: "claimed-consent"}},
		{"rollback", PiTakeoverClaims{RollbackSnapshot: "claimed-snapshot"}},
		{"installed bytes", PiTakeoverClaims{InstalledBytes: "claimed-bytes"}},
		{"executed load", PiTakeoverClaims{ExecutedLoad: "claimed-load"}},
		{"empty missing proofs", PiTakeoverClaims{ClaimedMissingProofs: []string{}}},
		{"declared missing proofs", PiTakeoverClaims{ClaimedMissingProofs: []string{"ready"}}},
		{"approved", PiTakeoverClaims{ClaimedApproved: true}},
		{"ready", PiTakeoverClaims{ClaimedReady: true}},
		{"executable", PiTakeoverClaims{ClaimedExecutable: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DescribePiTakeoverBoundary(profile, tc.claims)
			if got.Kind != PiTakeoverNotAuthorized || got.Channel != ChannelStable ||
				got.Rejected&RejectTakeoverClaimedAuthority == 0 {
				t.Fatalf("caller assertion became authority: %+v", got)
			}
			assertCompleteTakeoverRequirements(t, got)
		})
	}
	all := PiTakeoverClaims{
		ExistingExecutable: "/claimed/pi", ExistingHome: "/claimed/home",
		PhysicalInstanceID: "claimed-id", SourceDigest: "claimed-digest",
		ExactActions: "claimed-actions", ConsentBinding: "claimed-consent",
		RollbackSnapshot: "claimed-snapshot", InstalledBytes: "claimed-bytes",
		ExecutedLoad: "claimed-load", ClaimedMissingProofs: []string{},
		ClaimedApproved: true, ClaimedReady: true, ClaimedExecutable: true,
	}
	got := DescribePiTakeoverBoundary(profile, all)
	if got.Kind != PiTakeoverNotAuthorized || got.Channel != ChannelStable ||
		got.Rejected&RejectTakeoverClaimedAuthority == 0 {
		t.Fatalf("apparently complete takeover must remain refused: %+v", got)
	}
	assertCompleteTakeoverRequirements(t, got)
}

func assertCompleteTakeoverRequirements(t *testing.T, got PiTakeoverRefusal) {
	t.Helper()
	want := [9]PiTakeoverRequirement{
		RequireTakeoverSourceAndToolchain,
		RequireTakeoverPhysicalInstance,
		RequireTakeoverExactActions,
		RequireTakeoverBoundConsent,
		RequireTakeoverRollback,
		RequireTakeoverDrift,
		RequireTakeoverInstalledBytes,
		RequireTakeoverExecutedLoad,
		RequireTakeoverReady,
	}
	if got.Requirements != want {
		t.Fatalf("takeover requirements = %+v, want complete fixed set %+v", got.Requirements, want)
	}
}
