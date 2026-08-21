package model

import "strings"

// PiThinkingLevel is the reasoning effort gentle-pi routes an agent at.
type PiThinkingLevel string

const (
	PiThinkingOff     PiThinkingLevel = "off"
	PiThinkingMinimal PiThinkingLevel = "minimal"
	PiThinkingLow     PiThinkingLevel = "low"
	PiThinkingMedium  PiThinkingLevel = "medium"
	PiThinkingHigh    PiThinkingLevel = "high"
	PiThinkingXHigh   PiThinkingLevel = "xhigh"
	PiThinkingMax     PiThinkingLevel = "max"
)

// Valid reports whether the level is one gentle-pi accepts. An unknown one is
// dropped on read rather than reported, so it has to be refused here.
func (l PiThinkingLevel) Valid() bool {
	switch l {
	case PiThinkingOff, PiThinkingMinimal, PiThinkingLow, PiThinkingMedium,
		PiThinkingHigh, PiThinkingXHigh, PiThinkingMax:
		return true
	default:
		return false
	}
}

// PiAgentRouting is how one Pi agent is routed. Either half stands alone: a
// model with no level takes the level Pi is running at, and a level with no
// model applies to whatever model the session is on.
type PiAgentRouting struct {
	Model    string          `json:"model,omitempty"`
	Thinking PiThinkingLevel `json:"thinking,omitempty"`
}

// PiModelPresetKey names a Pi routing profile.
type PiModelPresetKey string

const (
	PiPresetRecommended PiModelPresetKey = "recommended"
	PiPresetLowCost     PiModelPresetKey = "low-cost"
	PiPresetPowerful    PiModelPresetKey = "powerful"
)

// The agents a profile routes, as gentle-pi ships them. Naming them here rather
// than discovering them keeps a profile the same on a machine where an agent
// has not been installed yet: gentle-pi ignores an entry for an agent it does
// not have, and just as silently ignores one whose name is wrong.
var (
	// Deciding work: the phases that read a codebase or judge an outcome.
	piDeepAgents = []string{
		"sdd-explore", "sdd-proposal", "sdd-design", "sdd-verify",
		"gentle-ai-explore", "gentle-ai-verify",
		"jd-judge-a", "jd-judge-b",
		"review-risk", "review-readability", "review-reliability", "review-resilience",
	}

	// Producing work: writing the change and fixing what review found.
	piOrdinaryAgents = []string{"sdd-apply", "jd-fix-agent", "gentle-ai-worker"}

	// Bookkeeping: transcribing decisions already made.
	piCheapAgents = []string{
		"sdd-spec", "sdd-tasks", "sdd-archive",
		"sdd-init", "sdd-onboard", "sdd-status", "sdd-sync",
	}
)

// PiModelPresetAssignments returns the routing a profile stands for.
//
// A profile assigns reasoning effort and leaves the model alone, which is what
// makes it portable: which models a Pi installation can reach is a property of
// the operator's subscription, and a profile naming one would be wrong for
// everyone who does not have it. Assigning a model stays the operator's call,
// per agent, over the top of this.
//
// The shape follows the phases rather than the agents: exploring and archiving
// are cheap, the phases that decide the implementation are not, and verifying
// is worth its own budget because it is the one that reads the work fresh.
func PiModelPresetAssignments(preset string) map[string]PiAgentRouting {
	cheap, ordinary, deep := PiThinkingLow, PiThinkingMedium, PiThinkingHigh

	switch PiModelPresetKey(preset) {
	case PiPresetLowCost:
		cheap, ordinary, deep = PiThinkingMinimal, PiThinkingLow, PiThinkingMedium
	case PiPresetPowerful:
		cheap, ordinary, deep = PiThinkingMedium, PiThinkingHigh, PiThinkingMax
	}

	routing := make(map[string]PiAgentRouting, len(piDeepAgents)+len(piOrdinaryAgents)+len(piCheapAgents))
	for _, agent := range piDeepAgents {
		routing[agent] = PiAgentRouting{Thinking: deep}
	}
	for _, agent := range piOrdinaryAgents {
		routing[agent] = PiAgentRouting{Thinking: ordinary}
	}
	for _, agent := range piCheapAgents {
		routing[agent] = PiAgentRouting{Thinking: cheap}
	}

	return routing
}

// piAgentForPhase renames the phases whose agent gentle-pi ships under a
// different name. Everything absent from this table is the same on both sides.
var piAgentForPhase = map[string]string{
	"sdd-propose": "sdd-proposal",
}

// PiModelsFromCodexPreset maps a Codex profile onto Pi's agents.
//
// Pi has no model catalogue of its own: it runs on whatever provider the
// operator pointed it at, which is why a Pi profile assigns reasoning effort
// and stops there. Naming the provider Pi actually runs on is what lets the
// profile carry models too, and the table it borrows is the one Gentle AI
// already tunes for that provider — Sol reasons, Terra writes, Luna
// transcribes — rather than a copy of it somewhere else.
func PiModelsFromCodexPreset(preset string) map[string]PiAgentRouting {
	carriles := CodexCarrilModelsForPreset(preset)
	defaults := CodexPresetCarrilDefaults(preset)
	routing := make(map[string]PiAgentRouting)

	for _, tier := range CodexTierGroups() {
		model, ok := carriles[tier.Profile]
		if !ok || model == "" {
			continue
		}

		thinking := PiThinkingLevel(defaults[tier.Profile].Effort)
		if !thinking.Valid() {
			thinking = ""
		}

		for _, phase := range tier.Phases {
			agent, renamed := piAgentForPhase[phase]
			if !renamed {
				agent = phase
			}
			// Codex routes a main session under "default"; Pi has no agent by
			// that name, and inventing one would write an entry gentle-pi
			// discards.
			if agent == "default" {
				continue
			}

			routing[agent] = PiAgentRouting{Model: codexModelReference(model), Thinking: thinking}
		}
	}

	return routing
}

// codexModelReference qualifies a bare Codex model id with the provider Pi
// reaches it through. Pi resolves a model as provider/id, and the Codex tables
// carry the id alone because Codex needs no prefix.
func codexModelReference(model string) string {
	if strings.Contains(model, "/") {
		return model
	}

	return "openai-codex/" + model
}

// PiModelsForFamily returns the routing Pi inherits from another provider's
// profile. An empty or unknown family borrows nothing, which leaves the Pi
// profile as reasoning levels over whatever model the session is on.
func PiModelsForFamily(family AgentID, preset string) map[string]PiAgentRouting {
	switch family {
	case AgentCodex:
		return PiModelsFromCodexPreset(preset)
	default:
		return nil
	}
}
