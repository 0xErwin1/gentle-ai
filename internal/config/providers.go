package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// ProviderSelection is the contract form of one provider's configuration
// block, nested under selection.providers.<agentId>. Grouping every
// provider-specific choice under the provider that owns it, instead of one
// flat field per provider suffixed with that provider's name, keeps a
// provider's own vocabulary next to the fields that only make sense for it,
// and lets a provider gain a field (OpenCode's ProfileStrategy) without every
// other provider's block growing an unused one.
type ProviderSelection struct {
	// Models carries the provider's own model vocabulary verbatim: a
	// per-phase ModelAssignment map for opencode, a per-phase model alias map
	// for claude-code/kiro-ide, a per-phase effort map for codex, or a
	// per-agent routing map for pi. Each provider decodes its own shape from
	// this, so the contract never forces every provider's vocabulary through
	// one common shape.
	Models json.RawMessage `json:"models,omitempty"`

	// ModelPreset names one of gentle-ai's own model profiles for this
	// provider, rather than restating the models and efforts it resolves to.
	// Spelling those out pins today's matrix into the document, so a profile
	// gentle-ai retunes silently stops being the profile the operator asked
	// for.
	ModelPreset string `json:"modelPreset,omitempty"`

	// BackgroundIntent is this provider's unresolved auto/on/off background
	// sub-agent preference. Only opencode and pi read it; silence stays
	// unresolved, because only an explicit choice is persisted.
	BackgroundIntent string `json:"backgroundIntent,omitempty"`

	// Profiles are named SDD orchestrator configurations, keyed by name
	// instead of carrying it as a field, so the same name cannot be declared
	// twice by accident. Accepted for opencode (the existing
	// generated-profiles path) and pi (gentle-pi agent profiles).
	Profiles map[string]ProviderProfile `json:"profiles,omitempty"`

	// ProfileStrategy is opencode-only: how sync handles named SDD profiles.
	ProfileStrategy model.SDDProfileStrategyID `json:"profileStrategy,omitempty"`
}

// ProviderProfile is the contract form of one named SDD profile nested under
// a provider block. The name lives in the enclosing map's key, not here.
type ProviderProfile struct {
	Orchestrator     *ModelAssignment           `json:"orchestrator,omitempty"`
	PhaseAssignments map[string]ModelAssignment `json:"phaseAssignments,omitempty"`
}

// modelCapableProviders are the providers whose block may carry a models
// vocabulary, and how to decode it. A provider absent from this table has no
// model vocabulary of its own, so a models block addressed to it can only be
// a document mistake.
var modelCapableProviders = map[model.AgentID]struct{}{
	model.AgentOpenCode:   {},
	model.AgentClaudeCode: {},
	model.AgentKiroIDE:    {},
	model.AgentCodex:      {},
}

// backgroundCapableProviders are the providers whose block may carry a
// backgroundIntent, because only they read a background sub-agent policy.
var backgroundCapableProviders = map[model.AgentID]struct{}{
	model.AgentOpenCode: {},
	model.AgentPi:       {},
}

// decodeStrict decodes raw into target the same way the top-level document
// decoder does: unknown fields are refused rather than silently dropped, so a
// typo inside a provider block is reported instead of vanishing.
func decodeStrict(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing content")
	}
	return nil
}

// validateProviders checks every provider block for a field only some
// providers accept, decodes each provider's models vocabulary, and validates
// the decoded values the same way the flat surfaces used to.
func validateProviders(selection Selection, diagnostics *[]Diagnostic) {
	for _, provider := range sortedProviderKeys(selection.Providers) {
		block := selection.Providers[provider]
		path := "$.selection.providers." + string(provider)

		validateProviderModels(provider, block, path, diagnostics)
		validateProviderModelPreset(provider, block, path, diagnostics)
		validateProviderBackgroundIntent(provider, block, path, diagnostics)
		validateProviderProfiles(provider, block, path, diagnostics)
	}
}

func sortedProviderKeys(providers map[model.AgentID]ProviderSelection) []model.AgentID {
	keys := make([]model.AgentID, 0, len(providers))
	for key := range providers {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func validateProviderModels(provider model.AgentID, block ProviderSelection, path string, diagnostics *[]Diagnostic) {
	if len(block.Models) == 0 {
		return
	}
	modelsPath := path + ".models"

	if _, ok := modelCapableProviders[provider]; !ok {
		*diagnostics = append(*diagnostics, diagnostic("config.provider.models.unsupported-provider", modelsPath, fmt.Sprintf("provider %q has no model vocabulary of its own", provider)))
		return
	}

	switch provider {
	case model.AgentOpenCode:
		var decoded map[string]ModelAssignment
		if err := decodeStrict(block.Models, &decoded); err != nil {
			*diagnostics = append(*diagnostics, diagnostic("config.provider.models.malformed", modelsPath, "models must be a phase-keyed map of provider-qualified model assignments"))
			return
		}
		for _, phase := range sortedKeys(decoded) {
			assignment := decoded[phase]
			if assignment.Provider == "" || assignment.Model == "" {
				*diagnostics = append(*diagnostics, diagnostic("config.model-assignment.incomplete", modelsPath+"."+phase, "a model assignment requires both provider and model"))
			}
		}

	case model.AgentClaudeCode:
		var decoded map[string]model.ClaudeModelAlias
		if err := decodeStrict(block.Models, &decoded); err != nil {
			*diagnostics = append(*diagnostics, diagnostic("config.provider.models.malformed", modelsPath, "models must be a phase-keyed map of Claude model aliases"))
			return
		}
		for _, phase := range sortedKeys(decoded) {
			if alias := decoded[phase]; !alias.Valid() {
				*diagnostics = append(*diagnostics, diagnostic("config.claude-model.unsupported", modelsPath+"."+phase, fmt.Sprintf("unsupported Claude model %q", alias)))
			}
		}

	case model.AgentKiroIDE:
		var decoded map[string]model.KiroModelAlias
		if err := decodeStrict(block.Models, &decoded); err != nil {
			*diagnostics = append(*diagnostics, diagnostic("config.provider.models.malformed", modelsPath, "models must be a phase-keyed map of Kiro model aliases"))
			return
		}
		for _, phase := range sortedKeys(decoded) {
			if alias := decoded[phase]; !alias.Valid() {
				*diagnostics = append(*diagnostics, diagnostic("config.kiro-model.unsupported", modelsPath+"."+phase, fmt.Sprintf("unsupported Kiro model %q", alias)))
			}
		}

	case model.AgentCodex:
		var decoded map[string]model.CodexEffort
		if err := decodeStrict(block.Models, &decoded); err != nil {
			*diagnostics = append(*diagnostics, diagnostic("config.provider.models.malformed", modelsPath, "models must be a phase-keyed map of Codex efforts"))
			return
		}
		for _, phase := range sortedKeys(decoded) {
			if effort := decoded[phase]; !effort.Valid() {
				*diagnostics = append(*diagnostics, diagnostic("config.codex-effort.unsupported", modelsPath+"."+phase, fmt.Sprintf("unsupported Codex effort %q; use low, medium, high, or xhigh", effort)))
			}
		}
	}
}

func validateProviderModelPreset(provider model.AgentID, block ProviderSelection, path string, diagnostics *[]Diagnostic) {
	if block.ModelPreset == "" {
		return
	}
	presetPath := path + ".modelPreset"

	known, offered := modelPresetNames[provider]
	if !offered {
		*diagnostics = append(*diagnostics, diagnostic("config.model-preset.unsupported-provider", presetPath, fmt.Sprintf("provider %q offers no model profiles; assign its models directly", provider)))
		return
	}
	if !containsString(known, block.ModelPreset) {
		*diagnostics = append(*diagnostics, diagnostic("config.model-preset.unsupported", presetPath, fmt.Sprintf("unsupported %s model profile %q; use %s", provider, block.ModelPreset, joinStrings(known))))
	}
}

func validateProviderBackgroundIntent(provider model.AgentID, block ProviderSelection, path string, diagnostics *[]Diagnostic) {
	if block.BackgroundIntent == "" {
		return
	}
	intentPath := path + ".backgroundIntent"

	if _, ok := backgroundCapableProviders[provider]; !ok {
		*diagnostics = append(*diagnostics, diagnostic("config.provider.background-intent.unsupported-provider", intentPath, fmt.Sprintf("provider %q has no background sub-agent policy", provider)))
		return
	}

	switch provider {
	case model.AgentOpenCode:
		if !model.OpenCodeBackgroundIntent(block.BackgroundIntent).Valid() {
			*diagnostics = append(*diagnostics, diagnostic("config.background-intent.unsupported", intentPath, fmt.Sprintf("unsupported background intent %q; use auto, on, or off", block.BackgroundIntent)))
		}
	case model.AgentPi:
		if !model.PiBackgroundIntent(block.BackgroundIntent).Valid() {
			*diagnostics = append(*diagnostics, diagnostic("config.pi-background-intent.unsupported", intentPath, fmt.Sprintf("unsupported Pi background intent %q; use auto, on, or off", block.BackgroundIntent)))
		}
	}
}

func validateProviderProfiles(provider model.AgentID, block ProviderSelection, path string, diagnostics *[]Diagnostic) {
	if len(block.Profiles) > 0 && provider != model.AgentOpenCode {
		*diagnostics = append(*diagnostics, diagnostic("config.provider.profiles.unsupported-provider", path+".profiles", fmt.Sprintf("provider %q does not support named profiles", provider)))
	}
	if block.ProfileStrategy != "" && provider != model.AgentOpenCode {
		*diagnostics = append(*diagnostics, diagnostic("config.provider.profile-strategy.unsupported-provider", path+".profileStrategy", fmt.Sprintf("provider %q does not support a profile strategy; only opencode does", provider)))
	}
}

func sortedProfileNames(profiles map[string]ProviderProfile) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func joinStrings(values []string) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += ", "
		}
		result += value
	}
	return result
}

// projectProviders flattens the nested provider blocks into the fields
// model.Selection already carries, filling selection in place. It runs
// before withModelPresets so a named profile still fills whatever a provider
// left unassigned.
func projectProviders(providers map[model.AgentID]ProviderSelection, selection *model.Selection) {
	presets := map[string]string{}

	for provider, block := range providers {
		if block.ModelPreset != "" {
			presets[string(provider)] = block.ModelPreset
		}

		switch provider {
		case model.AgentOpenCode:
			var decoded map[string]ModelAssignment
			_ = decodeStrict(block.Models, &decoded)
			selection.ModelAssignments = assignmentsToModel(decoded)
			selection.BackgroundIntent = model.OpenCodeBackgroundIntent(block.BackgroundIntent)
			selection.Profiles = providerProfilesToModel(block.Profiles)
			selection.SDDProfileStrategy = block.ProfileStrategy

		case model.AgentClaudeCode:
			var decoded map[string]model.ClaudeModelAlias
			_ = decodeStrict(block.Models, &decoded)
			selection.ClaudeModelAssignments = decoded

		case model.AgentKiroIDE:
			var decoded map[string]model.KiroModelAlias
			_ = decodeStrict(block.Models, &decoded)
			selection.KiroModelAssignments = decoded

		case model.AgentCodex:
			var decoded map[string]model.CodexEffort
			_ = decodeStrict(block.Models, &decoded)
			selection.CodexModelAssignments = decoded

		case model.AgentPi:
			selection.PiBackgroundIntent = model.PiBackgroundIntent(block.BackgroundIntent)
		}
	}

	if len(presets) > 0 {
		selection.ModelPresets = presets
	}
}

func providerProfilesToModel(profiles map[string]ProviderProfile) []model.Profile {
	if len(profiles) == 0 {
		return nil
	}

	names := sortedProfileNames(profiles)
	converted := make([]model.Profile, 0, len(names))
	for _, name := range names {
		profile := profiles[name]

		phases := make(map[string]model.ModelAssignment, len(profile.PhaseAssignments))
		for phase, assignment := range profile.PhaseAssignments {
			phases[phase] = assignmentToModel(assignment)
		}
		if len(phases) == 0 {
			phases = nil
		}

		orchestrator := model.ModelAssignment{}
		if profile.Orchestrator != nil {
			orchestrator = assignmentToModel(*profile.Orchestrator)
		}

		converted = append(converted, model.Profile{
			Name:              name,
			OrchestratorModel: orchestrator,
			PhaseAssignments:  phases,
		})
	}

	return converted
}

func providerProfilesFromModel(profiles []model.Profile) map[string]ProviderProfile {
	if len(profiles) == 0 {
		return nil
	}

	converted := make(map[string]ProviderProfile, len(profiles))
	for _, profile := range profiles {
		phases := make(map[string]ModelAssignment, len(profile.PhaseAssignments))
		for phase, assignment := range profile.PhaseAssignments {
			phases[phase] = assignmentFromModel(assignment)
		}
		if len(phases) == 0 {
			phases = nil
		}

		var orchestrator *ModelAssignment
		if profile.OrchestratorModel != (model.ModelAssignment{}) {
			assignment := assignmentFromModel(profile.OrchestratorModel)
			orchestrator = &assignment
		}

		converted[profile.Name] = ProviderProfile{Orchestrator: orchestrator, PhaseAssignments: phases}
	}

	return converted
}

// providersFromModel builds the nested provider blocks back out of the flat
// model.Selection fields, so FromSelection produces the same shape Decode
// accepts. A provider whose block would otherwise be entirely empty is left
// out of the map, matching the document a caller would have written for the
// same selection.
func providersFromModel(selection model.Selection) map[model.AgentID]ProviderSelection {
	providers := map[model.AgentID]ProviderSelection{}

	opencode := ProviderSelection{
		Models:           rawFromAssignments(selection.ModelAssignments),
		BackgroundIntent: string(selection.BackgroundIntent),
		Profiles:         providerProfilesFromModel(selection.Profiles),
		ProfileStrategy:  selection.SDDProfileStrategy,
	}
	if !isZeroProviderSelection(opencode) {
		providers[model.AgentOpenCode] = opencode
	}

	if raw := rawFromMap(selection.ClaudeModelAssignments); raw != nil {
		providers[model.AgentClaudeCode] = ProviderSelection{Models: raw}
	}
	if raw := rawFromMap(selection.KiroModelAssignments); raw != nil {
		providers[model.AgentKiroIDE] = ProviderSelection{Models: raw}
	}
	if raw := rawFromMap(selection.CodexModelAssignments); raw != nil {
		providers[model.AgentCodex] = ProviderSelection{Models: raw}
	}

	pi := ProviderSelection{
		BackgroundIntent: string(selection.PiBackgroundIntent),
	}
	if !isZeroProviderSelection(pi) {
		providers[model.AgentPi] = pi
	}

	for provider, preset := range selection.ModelPresets {
		entry := providers[model.AgentID(provider)]
		entry.ModelPreset = preset
		providers[model.AgentID(provider)] = entry
	}

	if len(providers) == 0 {
		return nil
	}
	return providers
}

func isZeroProviderSelection(block ProviderSelection) bool {
	return len(block.Models) == 0 && block.ModelPreset == "" &&
		block.BackgroundIntent == "" && len(block.Profiles) == 0 && block.ProfileStrategy == ""
}

func rawFromAssignments(assignments map[string]model.ModelAssignment) json.RawMessage {
	if len(assignments) == 0 {
		return nil
	}
	return rawFromMap(assignmentsFromModel(assignments))
}

func rawFromMap[K comparable, V any](values map[K]V) json.RawMessage {
	if len(values) == 0 {
		return nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil
	}
	return encoded
}
