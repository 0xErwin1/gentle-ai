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
	// Digests pin the three providers with an enforceable fresh-reviewer
	// boundary: Claude Code's generated reviewer has no live tools, OpenCode
	// replaces the task prompt with provider-bound evidence from an ordinary
	// already-running session (no restart, child process, special session,
	// or OPENCODE_DISABLE_* variable), and Codex's CodexAdapter launches a
	// brand-new `codex exec` process in an empty scratch directory (organic
	// proof: TestRealCodexReviewerOrdinarySessionAdmitsRawOutput,
	// e2e/organicruntime).
	wantManifestDigests := map[model.AgentID]string{
		model.AgentAntigravity:   "sha256:962eb63dc7f59a0b4c9c011dbb890aca1b40ecbfd3800c3e69b08f8b1639332c",
		model.AgentClaudeCode:    "sha256:132b9219b222d35b0e4eafce3dae965c56eb8d79f07dff6d45c42c137e36fd9b",
		model.AgentCodex:         "sha256:dbf94a3b7815cf68ccd6299c634f3e17be9abc305b3849adee382c65055c5ed9",
		model.AgentCursor:        "sha256:2cf80b9bd4cdc9a9d3586e6d02dc2207f326841bba935c6f11f257a20756d821",
		model.AgentGeminiCLI:     "sha256:463fdc93ad387c9b107c5f031f806dece9da3e2d47300b88d7640174bcb22a1e",
		model.AgentHermes:        "sha256:ec03506bc4cb0d4850542412630ada103c882d61fa372075f5f8db209a301127",
		model.AgentKilocode:      "sha256:08dc6df101bb042e2da1673a213032b12ecf49ef15fc463d856fecb0a052951e",
		model.AgentKimi:          "sha256:565e369cacfdbc128166512040fe1b5a18eada11b333a9c153f85d6661762dc9",
		model.AgentKiroIDE:       "sha256:3be196483ed199894062892c9367b3772bb66e18d6ec7b64e477ea201851a44a",
		model.AgentOpenClaw:      "sha256:0fb9cb07a7be9174e93793678ad7cd4618c58c6a0d284be2fa1b6acd4d409014",
		model.AgentOpenCode:      "sha256:3df2c0ee0a61774b7b7f0d547abed55721cc37ecc332c131935ce72fb142103f",
		model.AgentPi:            "sha256:346579cb9f505086cb9e429a36e857ed7fa5880171af1ad55bf6d9a8a0d53fcd",
		model.AgentQwenCode:      "sha256:897dd9264f92356375d1255949e1170938222853524d4b65e62b642a04b41c52",
		model.AgentTrae:          "sha256:65234f37e1142edae7fff865613970e6ab0433b783df2e4c05b0023fb1c31ffe",
		model.AgentVSCodeCopilot: "sha256:d1ceedd93c41dc1c34cce4accef239ca4e1bf4a643f203d5b3f8d031c4b4117b",
		model.AgentWindsurf:      "sha256:660efc2939546f9e3517115aa1400af1d5e5f1d4d10bb4475f2569fba1267312",
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
			wantImmutableExecutor := agent == model.AgentClaudeCode || agent == model.AgentOpenCode || agent == model.AgentCodex
			if got := manifest.Advertises(ContractImmutableReviewExecutorV1); got != wantImmutableExecutor {
				t.Fatalf("immutable reviewer execution advertised = %t, want %t", got, wantImmutableExecutor)
			}
			wantExposure := ContractExposureDormant
			if wantImmutableExecutor {
				wantExposure = ContractExposureAdvertised
			}
			if got := manifest.Contracts.ImmutableReviewExecutorV1.Exposure; got != wantExposure {
				t.Fatalf("immutable reviewer execution exposure = %q, want %q", got, wantExposure)
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

// TestEveryManifestDigestStaysByteStable pins that exactly 15 non-Pi agent rows
// remain byte-stable when the Pi row is updated. The Pi row change is the only
// expected delta from the capability flip; any other row changing is a regression.
// This test passes against the current (pre-flip) manifest since it asserts only
// the 15 non-Pi digests — the Pi row still carries its original hash here.
// Commit 1 (this test) is intentionally RED-against-original-passing to establish
// the baseline; Commit 2 flips SystemPrompt and updates the Pi digest, after
// which both tests pass together.
func TestEveryManifestDigestStaysByteStable(t *testing.T) {
	t.Parallel()

	// Digests computed from the current (pre-flip) manifest. These 15 rows must
	// not change when the Pi row is updated — they are the stability contract.
	wantNonPiDigests := map[model.AgentID]string{
		model.AgentAntigravity: "sha256:2f72974f6abdce68ca28a585705227d37d1c64c965120281727455223d678394",
		model.AgentClaudeCode:  "sha256:1954836303597cd9efc3e9736f2eb7c72d2c3b5107f6f36e0ca63d82c561c005",
		model.AgentCodex:       "sha256:fd1ed4fc30881c9ce53550f4c57ad7bd007e1b76bba943510ef53840b2e43a16",
		model.AgentCursor:      "sha256:5ec0323fd33720a5a99ec2ff8b876312f52aa6a588871a71920516f748f23f80",
		model.AgentGeminiCLI:   "sha256:3d51601fb11f71e2cc22daba09fd19dfdf473fbcbf3b16d732d4100f0a09cbbb",
		model.AgentHermes:      "sha256:00d1f1d2db659d33a032a97dae373a0f5e4a676921ef65d0f1162923e4758aa1",
		model.AgentKilocode:    "sha256:5472e4fb098caa868c650cf0d065bf277e079ddb8d5b8996b0cd9e8faa72d381",
		model.AgentKimi:        "sha256:20da639dbb4c852aef56c81e417641bc0b817a7fa41fd6aa2eecfe42aad9fafa",
		model.AgentKiroIDE:     "sha256:00cec3beeaa3506476151a6aa19966a6b3bfe52c3648ecf2e9d1804adafb86c2",
		model.AgentOpenClaw:    "sha256:e3dadd12614a5d27daf1c3fbdde875df5cb52888b7987ded87e5abd7bca8d49f",
		model.AgentOpenCode:    "sha256:77b30ecfac3cd3a6d54db33328b9ddddd8db3f11fa19ddd5e3829c5a0a506b80",
		// Pi excluded intentionally — it changes in the next commit.
		model.AgentQwenCode:      "sha256:def191f9b6ec065eda9fdd490817f27bbc89634b393db4bbbe9e81dffe1d9fba",
		model.AgentTrae:          "sha256:aada07d8d187a2649cf18613c2ba4be6eefd1632c39a7e792c6ec23dcc8e803d",
		model.AgentVSCodeCopilot: "sha256:3be9f31260509c10c8f4b2866c76f950ab4d2fabd8bf8684fd3af7a9b2391657",
		model.AgentWindsurf:      "sha256:a8c46fa07497092005ce74cd3b71ef230704408ea035b08229ebd054f42794ae",
	}

	nonPiAgents := make([]model.AgentID, 0, len(wantNonPiDigests))
	for agent := range wantNonPiDigests {
		nonPiAgents = append(nonPiAgents, agent)
	}

	if got := len(nonPiAgents); got != 15 {
		t.Fatalf("want 15 non-Pi agents, got %d", got)
	}

	for _, agent := range nonPiAgents {
		agent := agent
		wantDigest := wantNonPiDigests[agent]
		t.Run(string(agent), func(t *testing.T) {
			t.Parallel()

			manifest := MustForAgent(agent)
			gotDigest, err := manifest.Digest()
			if err != nil {
				t.Fatalf("Digest() error = %v", err)
			}
			if gotDigest != wantDigest {
				t.Fatalf("Digest() = %q, want %q (byte-stable contract)", gotDigest, wantDigest)
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
