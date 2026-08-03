package capabilitymanifest

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
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

func TestEveryManifestKeepsWorkRoutingDormantAndHashesCanonically(t *testing.T) {
	t.Parallel()

	const wantRoutingDigest = "sha256:ed03b86f20c9449a6e4c018f51d1e05619e1070b1076287a0792a74c458762b2"
	// Digests intentionally changed in Wave 4 S4: ContractClaims grew a new
	// ReviewTransportV1 field (design.md decision 5), which is content the
	// canonical JSON payload — and therefore the digest — legitimately
	// covers for every agent.
	wantManifestDigests := map[model.AgentID]string{
		model.AgentAntigravity:   "sha256:9039afe154cc0819ff8d133531081884a6d6f77ae75d26aa73a725c891095fbc",
		model.AgentClaudeCode:    "sha256:ab745fa46cdcb22f1c58d21c5125b9295d73c17710a3d87f52fb9efb674293dc",
		model.AgentCodex:         "sha256:083b5533ea666205657ef5cca8163e0d0e841d4d6c78831d9482dfe497bc21c3",
		model.AgentCursor:        "sha256:655eb90bd2ce82586be5e6cee056f94e0e331aacc301a130d4867dc60255f7e9",
		model.AgentGeminiCLI:     "sha256:9392a924baa63c4b7b94628454407157f4c8c7cef4f0357fbe568f9388d8f947",
		model.AgentHermes:        "sha256:06d947d3587bb53e6f0dadd51d7d21e39f51e8d90b326a012384690e6d3e5e3a",
		model.AgentKilocode:      "sha256:ad1042de6debafc218a8cd0a0c8e73f34617c26716fc99f4c58208746da2ae49",
		model.AgentKimi:          "sha256:0ac497c298af154de721047fef96c9c81f646202dbe2cefb074bebf2fbfd3ff6",
		model.AgentKiroIDE:       "sha256:341727decd4a78853dbddc29402c103eebdd7b5e9f1719e7e3f9b892f657345c",
		model.AgentOpenClaw:      "sha256:6b65856cf320aee8e0e51b70e06bb6dd455af5fe433db4addd5394744af5b67f",
		model.AgentOpenCode:      "sha256:70cfb9bcb82acfaf18a0fcc191259d6e72ad6ba440fe36393363aeafb1e40999",
		model.AgentPi:            "sha256:7efa5132ccfda4346d53c9e148f03ff731baebecbfbeb35ed42d03e6463330c5",
		model.AgentQwenCode:      "sha256:7a92759dd5d52cb09cd8743cf89b46683f657120f51e636f16e2d934fc297e90",
		model.AgentTrae:          "sha256:f71a112a213767666ab90c244eaaa570a8764faf4ea6c7d80fd3100f15cc5149",
		model.AgentVSCodeCopilot: "sha256:3b2b1d46c89ce7b8a8e0ff6f550bbfad1da3e677d773d84fa2bc58818b73ff8d",
		model.AgentWindsurf:      "sha256:276ba0ca496da63cc7699a980d70b7e54bcad6f351bb125eb0a261f59b1586a2",
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
			if manifest.Contracts.WorkRoutingV1.Exposure != ContractExposureDormant {
				t.Fatalf("work-routing exposure = %q, want %q", manifest.Contracts.WorkRoutingV1.Exposure, ContractExposureDormant)
			}
			if manifest.Advertises(ContractWorkRoutingV1) {
				t.Fatal("work-routing must remain unadvertised before final activation")
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
