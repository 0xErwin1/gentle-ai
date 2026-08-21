package model

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

// The agents a profile routes. Naming them here rather than discovering them
// keeps a profile the same on a machine where an agent has not been installed
// yet: gentle-pi ignores an entry for an agent it does not have.
const (
	piAgentExplore  = "sdd-explore"
	piAgentPropose  = "sdd-propose"
	piAgentSpec     = "sdd-spec"
	piAgentDesign   = "sdd-design"
	piAgentTasks    = "sdd-tasks"
	piAgentApply    = "sdd-apply"
	piAgentVerify   = "sdd-verify"
	piAgentArchive  = "sdd-archive"
	piAgentWorker   = "gentle-ai-worker"
	piAgentReviewer = "review-risk"
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

	return map[string]PiAgentRouting{
		piAgentExplore:  {Thinking: cheap},
		piAgentPropose:  {Thinking: cheap},
		piAgentArchive:  {Thinking: cheap},
		piAgentSpec:     {Thinking: deep},
		piAgentDesign:   {Thinking: deep},
		piAgentTasks:    {Thinking: ordinary},
		piAgentApply:    {Thinking: deep},
		piAgentVerify:   {Thinking: deep},
		piAgentReviewer: {Thinking: deep},
		piAgentWorker:   {Thinking: ordinary},
	}
}
