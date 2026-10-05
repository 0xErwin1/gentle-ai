package cli

import (
	"errors"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

func TestShellCandidateGateAlwaysRefusesFourDrafts(t *testing.T) {
	for _, channel := range []shellinstaller.Channel{shellinstaller.ChannelStable, shellinstaller.ChannelMain} {
		for _, entry := range []shellinstaller.TerminalEntryPoint{
			shellinstaller.TerminalEntryPointPi, shellinstaller.TerminalEntryPointGentleShell,
		} {
			t.Run(string(channel)+"/"+string(entry), func(t *testing.T) {
				draft, err := GateShellInstallCandidate(shellinstaller.Profile{Channel: channel, TerminalEntryPoint: entry})
				var refusal ShellCandidateNotAuthorized
				if !errors.As(err, &refusal) || refusal.Kind != ShellCandidateRefusalNotAuthorized {
					t.Fatalf("gate error = %v, want typed not-authorized refusal", err)
				}
				if draft.Intent.Channel != channel || draft.Intent.EntryPoint != entry ||
					draft.MissingProofs[0] != shellinstaller.ProofIndependentSource ||
					draft.MissingProofs[5] != shellinstaller.ProofIndependentReady {
					t.Fatalf("gate draft = %+v, want explicit missing proofs for %q/%q", draft, channel, entry)
				}
			})
		}
	}
}

func TestShellCandidateGatePiSyntaxIsNotTakeoverAuthority(t *testing.T) {
	profile := shellinstaller.Profile{Channel: shellinstaller.ChannelStable, TerminalEntryPoint: shellinstaller.TerminalEntryPointPi}
	if err := profile.Validate(); err != nil {
		t.Fatalf("Pi syntax must remain valid: %v", err)
	}
	draft, err := GateShellInstallCandidate(profile)
	var refusal ShellCandidateNotAuthorized
	if !errors.As(err, &refusal) || refusal.Kind != ShellCandidateRefusalNotAuthorized ||
		draft.MissingProofs[1] != shellinstaller.ProofPhysicalInstance ||
		draft.MissingProofs[3] != shellinstaller.ProofRollback {
		t.Fatalf("syntax-valid Pi takeover must be refused before install: draft=%+v error=%v", draft, err)
	}
}

func TestShellCandidateGateDoesNotAuthorizeLegacyOrForgedCommand(t *testing.T) {
	legacy, err := GateShellInstallCandidate(shellinstaller.Profile{Channel: shellinstaller.ChannelStable})
	var refusal ShellCandidateNotAuthorized
	if !errors.As(err, &refusal) || legacy.Intent.EntryPoint != shellinstaller.TerminalEntryPointPi {
		t.Fatalf("legacy zero target = %+v, error %v; want refused Pi draft", legacy, err)
	}
	for _, profile := range []shellinstaller.Profile{
		{Channel: "floating", TerminalEntryPoint: shellinstaller.TerminalEntryPointPi},
		{Channel: shellinstaller.ChannelMain, TerminalEntryPoint: "forged"},
	} {
		draft, err := GateShellInstallCandidate(profile)
		if err == nil || draft != (shellinstaller.DraftPlan{}) {
			t.Fatalf("invalid profile %+v returned draft %+v, error %v", profile, draft, err)
		}
	}
}
