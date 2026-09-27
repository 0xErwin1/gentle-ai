package shellinstaller

import "testing"

func TestDraftPlanDescribesFourUnexecutableIntents(t *testing.T) {
	wantMissing := [6]MissingProof{
		ProofIndependentSource, ProofPhysicalInstance, ProofBoundConsent,
		ProofRollback, ProofExecutedBytes, ProofIndependentReady,
	}
	for _, channel := range []Channel{ChannelStable, ChannelMain} {
		for _, tt := range []struct {
			entryPoint TerminalEntryPoint
			existing   ExistingPiIntent
			location   PiInstallationIntent
		}{
			{TerminalEntryPointPi, ExistingPiReplaceAfterBoundConsent, PiInstallationReplaceExisting},
			{TerminalEntryPointGentleShell, ExistingPiPreserve, PiInstallationSeparate},
		} {
			t.Run(string(channel)+"/"+string(tt.entryPoint), func(t *testing.T) {
				profile := Profile{Channel: channel, TerminalEntryPoint: tt.entryPoint}
				draft, err := BuildDraftPlan(profile)
				if err != nil {
					t.Fatal(err)
				}
				if draft.Intent.Channel != channel || draft.Intent.EntryPoint != tt.entryPoint ||
					draft.Intent.Effect.ExistingPi != tt.existing || draft.Intent.Effect.PiInstallation != tt.location {
					t.Fatalf("draft intent = %+v, want channel %q, command %q, effect %q/%q", draft.Intent, channel, tt.entryPoint, tt.existing, tt.location)
				}
				if draft.MissingProofs != wantMissing {
					t.Fatalf("missing proofs = %v, want %v", draft.MissingProofs, wantMissing)
				}
				// Describing gentle-shell syntax does not open its install route.
				if valid := profile.Validate() == nil; valid != (tt.entryPoint == TerminalEntryPointPi) {
					t.Fatalf("Validate() success = %t for %q", valid, tt.entryPoint)
				}
			})
		}
	}
}

func TestDraftPlanRejectsInvalidSyntaxAndKeepsLegacyPiIntent(t *testing.T) {
	for _, profile := range []Profile{
		{Channel: "beta", TerminalEntryPoint: TerminalEntryPointPi},
		{Channel: ChannelStable, TerminalEntryPoint: "forged"},
	} {
		if draft, err := BuildDraftPlan(profile); err == nil || draft != (DraftPlan{}) {
			t.Fatalf("invalid profile %+v yielded draft %+v, error %v", profile, draft, err)
		}
	}
	legacy, err := BuildDraftPlan(Profile{Channel: ChannelMain})
	if err != nil || legacy.Intent.EntryPoint != TerminalEntryPointPi || legacy.Intent.Channel != ChannelMain {
		t.Fatalf("legacy zero Pi intent = %+v, error %v", legacy, err)
	}
}
