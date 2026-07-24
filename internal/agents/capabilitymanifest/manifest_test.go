package capabilitymanifest

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/model"
)

func TestCanonicalImplementationRoutingBoundaries(t *testing.T) {
	t.Parallel()

	got := CanonicalImplementationRouting()
	want := ImplementationRoutingFacts{
		DirectInline: DirectInlineFacts{
			MinUnderstandingFiles:                    1,
			MaxUnderstandingFiles:                    3,
			MaxMechanicalWriteFiles:                  1,
			MechanicalWriteMustBeAlreadyUnderstood:   true,
			MechanicalWriteMustNotRequireResearch:    true,
			MechanicalWriteMustNotHaveOpenDesignWork: true,
		},
		DelegatedDirect: DelegatedDirectFacts{
			MappingMinUnderstandingFiles:  4,
			WriterMinNonTrivialFiles:      2,
			DelegateWhenReadPreparesWrite: true,
			DelegateWhenBroadResearch:     true,
		},
		SDD: SDDProposalFacts{
			ProposeWhenSubstantialOrAmbiguous:     true,
			DurableArtifactsMustReduceUncertainty: true,
			SelectionPolicy:                       SDDSelectionExplicitRequestOrAcceptedProposal,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalImplementationRouting() = %#v, want %#v", got, want)
	}
}

func TestManifestRejectsWeakenedRoutingFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		weaken func(*AgentCapabilityManifest)
	}{
		{
			name: "direct understanding starts below one file",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DirectInline.MinUnderstandingFiles = 0
			},
		},
		{
			name: "direct understanding exceeds three files",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DirectInline.MaxUnderstandingFiles = 4
			},
		},
		{
			name: "mapping starts after four files",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DelegatedDirect.MappingMinUnderstandingFiles = 5
			},
		},
		{
			name: "writer starts after two non-trivial files",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DelegatedDirect.WriterMinNonTrivialFiles = 3
			},
		},
		{
			name: "read preparing write no longer delegates",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DelegatedDirect.DelegateWhenReadPreparesWrite = false
			},
		},
		{
			name: "broad research no longer delegates",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.DelegatedDirect.DelegateWhenBroadResearch = false
			},
		},
		{
			name: "substantial ambiguity no longer proposes SDD",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.SDD.ProposeWhenSubstantialOrAmbiguous = false
			},
		},
		{
			name: "SDD proposal need not reduce durable uncertainty",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.SDD.DurableArtifactsMustReduceUncertainty = false
			},
		},
		{
			name: "SDD selection bypasses explicit consent",
			weaken: func(manifest *AgentCapabilityManifest) {
				manifest.ImplementationRouting.SDD.SelectionPolicy = "automatic"
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			manifest := MustForAgent(model.AgentClaudeCode)
			test.weaken(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatal("Validate() = nil, want non-canonical routing rejection")
			}
		})
	}
}

func TestEveryManifestAdvertisesWorkRoutingAndHashesCanonically(t *testing.T) {
	t.Parallel()

	const wantRoutingDigest = "sha256:ed03b86f20c9449a6e4c018f51d1e05619e1070b1076287a0792a74c458762b2"
	wantManifestDigests := map[model.AgentID]string{
		model.AgentAntigravity:   "sha256:82c6d22012ce6baf73b5c11ed13e689f7c0cd3c121252eb790591ef91d59af6e",
		model.AgentClaudeCode:    "sha256:6387c2b1cad3be4983d17736614dfbe269062c7c010adf41d9162d38b02e1f2c",
		model.AgentCodex:         "sha256:3d2613841279bc09307bddf753193f9991f2b4f27435c27ab75d6726da8124b0",
		model.AgentCursor:        "sha256:7ecb005b9e05dcb1a12aae18e6863ca4ea4ad47dba6eeb04fd9792fac7aedf51",
		model.AgentGeminiCLI:     "sha256:0020354d31a8decbfb0f31d042fb367297fe0c84ba2816fe3e0ad6f5bd556091",
		model.AgentHermes:        "sha256:69aaaee5f0ed11a027229266a721c195337095672baa9e6ad1face8302475b18",
		model.AgentKilocode:      "sha256:4bd8cc582fef1a59ec0b89adf45662c5aaf003c423d52fd0a4c201908053838d",
		model.AgentKimi:          "sha256:24e3bbb3f0f47f253db0852646fb1238249829c29578acc2928a85412f60de00",
		model.AgentKiroIDE:       "sha256:ad2469d1bd938ebc29168fd181fca998bf1d1b11babcbb82f91121ddd961b318",
		model.AgentOpenClaw:      "sha256:803ccdbb06332a969b427c4cbeaca430a2635aee2d23ebb6f10422b05dec3182",
		model.AgentOpenCode:      "sha256:62bfa1e59c91e1a9a6caa0bbc0634793c1d59f6601bd95652cb814a5850c3fa6",
		model.AgentPi:            "sha256:5a9da3b75edf1cd85cb9722bd016dcdde1dcf813f781fc20509f90685e3d7a23",
		model.AgentQwenCode:      "sha256:1910b87bd14baf97d992b7d33172bce41c1b022f2eba3145fde8d7e97e8f560b",
		model.AgentTrae:          "sha256:162c1682be62ef2910c15de3acba7d2bf976932a73e7d8c8c5a64b02f28b2748",
		model.AgentVSCodeCopilot: "sha256:92d1d6e9feead113e1e70b747bee0626d26b4f0f68cb090bbfcb4750ec2329a0",
		model.AgentWindsurf:      "sha256:f9a83cc0fd07ddf7e91c524731a81ef6e04d690848273c5893c74807facce9e5",
	}

	for agent, wantDigest := range wantManifestDigests {
		agent := agent
		wantDigest := wantDigest
		t.Run(string(agent), func(t *testing.T) {
			t.Parallel()

			manifest := MustForAgent(agent)
			if err := manifest.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if manifest.Contracts.WorkRoutingV1.Exposure != ContractExposureAdvertised {
				t.Fatalf("work-routing exposure = %q, want %q", manifest.Contracts.WorkRoutingV1.Exposure, ContractExposureAdvertised)
			}
			if !manifest.Advertises(ContractWorkRoutingV1) {
				t.Fatal("final canonical manifest must advertise work routing")
			}

			payload, err := manifest.CanonicalJSON()
			if err != nil {
				t.Fatalf("CanonicalJSON() error = %v", err)
			}
			var roundTrip AgentCapabilityManifest
			if err := json.Unmarshal(payload, &roundTrip); err != nil {
				t.Fatalf("Unmarshal(CanonicalJSON()) error = %v", err)
			}
			if roundTrip != manifest {
				t.Fatalf("canonical JSON round trip = %#v, want %#v", roundTrip, manifest)
			}

			gotDigest, err := roundTrip.Digest()
			if err != nil {
				t.Fatalf("Digest() error = %v", err)
			}
			if gotDigest != wantDigest {
				t.Fatalf("Digest() = %q, want %q", gotDigest, wantDigest)
			}

			gotRoutingDigest, err := manifest.RoutingDigest()
			if err != nil {
				t.Fatalf("RoutingDigest() error = %v", err)
			}
			if gotRoutingDigest != wantRoutingDigest {
				t.Fatalf("RoutingDigest() = %q, want %q", gotRoutingDigest, wantRoutingDigest)
			}
		})
	}
}

func TestForAgentRejectsUnknownAgent(t *testing.T) {
	t.Parallel()

	_, err := ForAgent(model.AgentID("unknown"))
	if !errors.Is(err, ErrUnsupportedAgent) {
		t.Fatalf("ForAgent() error = %v, want ErrUnsupportedAgent", err)
	}
}
