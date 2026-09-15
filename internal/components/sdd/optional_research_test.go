package sdd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/catalog"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// These exercise rendered and installed instructions, not live model decisions.
func assertOptionalResearchGuidance(t *testing.T, content string) {
	t.Helper()
	for _, want := range []string{
		"### Optional Research and Product Discovery",
		"Research remains optional, including after selection.",
		"Missing, partial, unavailable or divergent research metadata does not block proposal work.",
		"Ask one focused product question at a time and wait for the answer",
		"Pause only work dependent on an unresolved product decision or unsafe missing evidence",
		"actually available and authorized",
		"primary sources", "contradictions", "freshness",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("missing optional research instruction %q", want)
		}
	}
	for _, stale := range []string{"Research and Pre-Proposal Gate (MANDATORY)", "proposal_ready", "gentle-ai.sdd-preproposal/v1", "selection makes completion mandatory", "selected research must be `done`", "matching hybrid state"} {
		if strings.Contains(content, stale) {
			t.Errorf("retired research admission remains: %q", stale)
		}
	}
}

func TestOptionalResearchRendersWithoutAdministrativeAdmission(t *testing.T) {
	for _, agent := range catalog.AllAgents() {
		if agent.ID == model.AgentPi {
			continue
		}
		t.Run(string(agent.ID), func(t *testing.T) { assertOptionalResearchGuidance(t, renderSDDOrchestratorAsset(agent.ID)) })
	}
}

func TestOptionalResearchMigratesInstalledOpenCodePrompt(t *testing.T) {
	const oldGate = "### Research and Pre-Proposal Gate (MANDATORY) — Offer `sdd-research` immediately after `sdd-explore`; selection makes completion mandatory."
	const userText = "Keep my proposal question round notes and custom release instructions."
	for _, mode := range []model.SDDModeID{model.SDDModeSingle, model.SDDModeMulti} {
		for _, marked := range []bool{false, true} {
			name := string(mode) + "/inline"
			if marked {
				name = string(mode) + "/managed"
			}
			t.Run(name, func(t *testing.T) {
				home := t.TempDir()
				mockNoPackageManager(t)
				settings := filepath.Join(home, ".config", "opencode", "opencode.json")
				gate := oldGate
				if marked {
					gate = "<!-- gentle-ai:sdd-research-lifecycle -->\n" + gate + "\n<!-- /gentle-ai:sdd-research-lifecycle -->"
				}
				seed := map[string]any{"agent": map[string]any{"gentle-orchestrator": map[string]any{"mode": "primary", "prompt": "# Custom prompt\n" + userText + "\n" + gate + "\n"}}}
				data, err := json.Marshal(seed)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(settings, data, 0o644); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					if _, err := Inject(home, opencodeAdapter(), mode, InjectOptions{PreserveOpenCodeOrchestratorPrompt: true}); err != nil {
						t.Fatal(err)
					}
					prompt := readGentleOrchestratorPrompt(t, settings)
					assertOptionalResearchGuidance(t, prompt)
					if strings.Count(prompt, userText) != 1 {
						t.Fatal("migration changed unrelated user text")
					}
					if strings.Count(prompt, "### Optional Research and Product Discovery") != 1 {
						t.Fatal("migration duplicated managed research instructions")
					}
				}
			})
		}
	}
}
