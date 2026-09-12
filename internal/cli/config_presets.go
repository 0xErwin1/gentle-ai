package cli

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
)

// modelPresetSchema versions the presets document so a consumer can detect a
// shape change instead of guessing from field presence.
const modelPresetSchema = "gentle-ai.model-presets/v1"

// presetProviderNames lists, in stable output order, every provider whose
// model preset tables gentle-ai curates in internal/model. Pi is not one of
// them: its model routing belongs to gentle-pi, not to Gentle AI's own preset
// tables.
var presetProviderNames = []string{"claude-code", "codex", "kiro-ide"}

// presetPhaseEntry is one phase's resolved model, and effort when the
// provider's own preset table carries one, inside a named preset.
type presetPhaseEntry struct {
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
}

type providerPresetsDocument struct {
	Presets map[string]map[string]presetPhaseEntry `json:"presets"`
}

type presetsDocument struct {
	Schema    string                             `json:"schema"`
	Providers map[string]providerPresetsDocument `json:"providers"`
}

func claudePresetNames() []string {
	return []string{
		string(model.ClaudePresetBalanced), string(model.ClaudePresetPerformance),
		string(model.ClaudePresetEconomy), string(model.ClaudePresetDiversity),
	}
}

func kiroPresetNames() []string {
	return []string{
		string(model.KiroPresetBalanced), string(model.KiroPresetPerformance),
		string(model.KiroPresetEconomy), string(model.KiroPresetOpenWeight),
	}
}

func codexPresetNames() []string {
	return []string{
		string(model.CodexPresetLowCost), string(model.CodexPresetRecommended),
		string(model.CodexPresetPowerful),
	}
}

func claudePresetTable(preset string) map[string]presetPhaseEntry {
	table := make(map[string]presetPhaseEntry)
	for phase, alias := range model.ClaudeModelPresetAssignments(preset) {
		table[phase] = presetPhaseEntry{Model: string(alias)}
	}
	return table
}

func kiroPresetTable(preset string) map[string]presetPhaseEntry {
	table := make(map[string]presetPhaseEntry)
	for phase, alias := range model.KiroModelPresetAssignments(preset) {
		table[phase] = presetPhaseEntry{Model: string(alias)}
	}
	return table
}

// codexEffortsForPreset mirrors the same unknown-name fallback the internal
// model package's own preset resolvers use, so a preset name reported here
// never disagrees with the effort gentle-ai would actually assign for it.
func codexEffortsForPreset(preset string) map[string]model.CodexEffort {
	switch model.CodexPresetKey(preset) {
	case model.CodexPresetLowCost:
		return model.CodexModelPresetLowCost()
	case model.CodexPresetPowerful:
		return model.CodexModelPresetPowerful()
	default:
		return model.CodexModelPresetRecommended()
	}
}

// codexPresetTable expands a Codex preset per phase, including the top-level
// "orchestrator" entry the main session runs on, the same way gentle-ai's own
// renderers derive a Codex profile from CodexTierGroups.
func codexPresetTable(preset string) map[string]presetPhaseEntry {
	efforts := codexEffortsForPreset(preset)
	carrilModels := model.CodexCarrilModelsForPreset(preset)

	table := make(map[string]presetPhaseEntry)
	for _, tier := range model.CodexTierGroups() {
		modelID := carrilModels[tier.Profile]
		for _, phase := range tier.Phases {
			table[phase] = presetPhaseEntry{Model: modelID, Effort: string(efforts[phase])}
		}
	}

	if orchestrator := model.CodexPresetOrchestratorAssignment(preset); orchestrator != nil {
		table["orchestrator"] = presetPhaseEntry{Model: orchestrator.Model, Effort: string(orchestrator.Effort)}
	}

	return table
}

// providerPresets resolves every named preset a provider offers into its
// phase-keyed table. An unrecognized provider returns an empty document
// rather than guessing; RunConfigPresets refuses it before reaching here.
func providerPresets(provider string) providerPresetsDocument {
	document := providerPresetsDocument{Presets: make(map[string]map[string]presetPhaseEntry)}

	switch provider {
	case "claude-code":
		for _, name := range claudePresetNames() {
			document.Presets[name] = claudePresetTable(name)
		}
	case "kiro-ide":
		for _, name := range kiroPresetNames() {
			document.Presets[name] = kiroPresetTable(name)
		}
	case "codex":
		for _, name := range codexPresetNames() {
			document.Presets[name] = codexPresetTable(name)
		}
	}

	return document
}

// RunConfigPresets prints gentle-ai's own model preset tables: what each
// named profile resolves to per phase, for every provider that offers one.
// It reads no configuration document and requires no --config; the tables it
// prints live in internal/model, not in anything a user writes.
func RunConfigPresets(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("config presets", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	provider := flags.String("provider", "", "limit output to one provider (claude-code, codex, or kiro-ide)")
	_ = flags.Bool("json", false, "emit machine-readable JSON (the only output format; the flag documents intent)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("config presets does not accept positional arguments; run gentle-ai config presets [--provider <id>] --json")
	}

	names := presetProviderNames
	if *provider != "" {
		if !slices.Contains(presetProviderNames, *provider) {
			return fmt.Errorf("unsupported provider %q; run gentle-ai config presets --provider <id> --json with one of %s", *provider, strings.Join(presetProviderNames, ", "))
		}
		names = []string{*provider}
	}

	document := presetsDocument{Schema: modelPresetSchema, Providers: make(map[string]providerPresetsDocument)}
	for _, name := range names {
		document.Providers[name] = providerPresets(name)
	}

	return writeConfigResult(stdout, document)
}
