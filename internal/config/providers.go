package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// ProviderSelection is the contract form of one provider's configuration
// block, nested under selection.providers.<agentId>. Grouping every
// provider-specific choice under the provider that owns it, instead of one
// flat field per provider suffixed with that provider's name, keeps a
// provider's own vocabulary next to the fields that only make sense for it,
// and lets a provider gain a field (Pi's ActiveProfile) without every other
// provider's block growing an unused one.
type ProviderSelection struct {
	// Models carries the provider's own model vocabulary verbatim: a
	// per-phase ModelAssignment map for opencode, a per-phase model alias map
	// for claude-code/kiro-ide, a per-phase effort map for codex, or a
	// per-agent routing map for pi. Each provider decodes its own shape from
	// this, so the contract never forces every provider's vocabulary through
	// one common shape.
	Models json.RawMessage `json:"models,omitempty"`

	// ModelFamily is Pi-only: the provider whose model profile Pi borrows.
	ModelFamily model.AgentID `json:"modelFamily,omitempty"`

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

	// ActiveProfile is pi-only: the Profiles key gentle-pi should activate.
	// Declaring it also materialises the routing and orchestrator defaults
	// that profile implies, the way gentle-pi's own "apply" would.
	ActiveProfile string `json:"activeProfile,omitempty"`

	// Skills overrides the flat skill list for this provider. A provider
	// without this takes the flat list, so the simple form keeps meaning
	// "every provider" and this is only needed when one must differ.
	Skills []model.SkillID `json:"skills,omitempty"`

	// MCPServers overrides the flat MCP server set for this provider.
	MCPServers map[string]MCPServer `json:"mcpServers,omitempty"`
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
	model.AgentPi:         {},
}

// backgroundCapableProviders are the providers whose block may carry a
// backgroundIntent, because only they read a background sub-agent policy.
var backgroundCapableProviders = map[model.AgentID]struct{}{
	model.AgentOpenCode: {},
	model.AgentPi:       {},
}

// safePiProfileName mirrors the pattern gentle-pi validates a profile name
// against. It is duplicated rather than imported because it is gentle-pi's
// rule, not this contract's: what matters is refusing here what gentle-pi
// would refuse to load.
var safePiProfileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

var reservedPiProfileNames = map[string]struct{}{
	"__proto__":   {},
	"constructor": {},
	"prototype":   {},
}

// piReservedPhaseKey is the phase key gentle-pi reserves for the profile's
// orchestrator entry. A phase assignment cannot reuse it: doing so would
// silently collide with the orchestrator entry once the profile is written.
const piReservedPhaseKey = "orchestrator"

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
	declaredAgents := make(map[model.AgentID]struct{}, len(selection.Agents))
	for _, agent := range selection.Agents {
		declaredAgents[agent] = struct{}{}
	}

	for _, provider := range sortedProviderKeys(selection.Providers) {
		block := selection.Providers[provider]
		path := "$.selection.providers." + string(provider)

		validateProviderModels(provider, block, path, diagnostics)
		validateProviderModelFamily(provider, block, path, diagnostics)
		validateProviderModelPreset(provider, block, path, diagnostics)
		validateProviderBackgroundIntent(provider, block, path, diagnostics)
		validateProviderProfiles(provider, block, path, diagnostics)
		validateProviderSkills(provider, block, path, declaredAgents, diagnostics)
		validateProviderMCPServers(provider, block, path, declaredAgents, diagnostics)
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

	case model.AgentPi:
		var decoded map[string]model.PiAgentRouting
		if err := decodeStrict(block.Models, &decoded); err != nil {
			*diagnostics = append(*diagnostics, diagnostic("config.provider.models.malformed", modelsPath, "models must be an agent-keyed map of Pi routings"))
			return
		}
		validatePiRoutingValues(decoded, modelsPath, diagnostics)
	}
}

func validatePiRoutingValues(routings map[string]model.PiAgentRouting, path string, diagnostics *[]Diagnostic) {
	for _, agent := range sortedKeys(routings) {
		routing := routings[agent]
		entryPath := path + "." + agent

		if routing.Model == "" && routing.Thinking == "" {
			*diagnostics = append(*diagnostics, diagnostic("config.pi-model.empty", entryPath, "a Pi routing assigns a model, a reasoning level, or both"))
			continue
		}
		if routing.Model != "" && !safePiModelID.MatchString(routing.Model) {
			*diagnostics = append(*diagnostics, diagnostic("config.pi-model.unsupported", entryPath, fmt.Sprintf("unsupported Pi model id %q; gentle-pi accepts letters, digits and ._~:@/+%%-", routing.Model)))
		}
		if routing.Thinking != "" && !routing.Thinking.Valid() {
			*diagnostics = append(*diagnostics, diagnostic("config.pi-model.thinking-unsupported", entryPath, fmt.Sprintf("unsupported Pi reasoning level %q; use off, minimal, low, medium, high, xhigh, or max", routing.Thinking)))
		}
	}
}

func validateProviderModelFamily(provider model.AgentID, block ProviderSelection, path string, diagnostics *[]Diagnostic) {
	if block.ModelFamily == "" {
		return
	}
	if provider != model.AgentPi {
		*diagnostics = append(*diagnostics, diagnostic("config.provider.model-family.unsupported-provider", path+".modelFamily", fmt.Sprintf("provider %q does not borrow a model family; only pi does", provider)))
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
	acceptsProfiles := provider == model.AgentOpenCode || provider == model.AgentPi
	if len(block.Profiles) > 0 && !acceptsProfiles {
		*diagnostics = append(*diagnostics, diagnostic("config.provider.profiles.unsupported-provider", path+".profiles", fmt.Sprintf("provider %q does not support named profiles", provider)))
	}
	if block.ProfileStrategy != "" && provider != model.AgentOpenCode {
		*diagnostics = append(*diagnostics, diagnostic("config.provider.profile-strategy.unsupported-provider", path+".profileStrategy", fmt.Sprintf("provider %q does not support a profile strategy; only opencode does", provider)))
	}
	if block.ActiveProfile != "" && provider != model.AgentPi {
		*diagnostics = append(*diagnostics, diagnostic("config.provider.active-profile.unsupported-provider", path+".activeProfile", fmt.Sprintf("provider %q does not support an active profile; only pi does", provider)))
	}

	if provider == model.AgentPi {
		validatePiProfileNames(block, path, diagnostics)
		validatePiProfileValues(block, path, diagnostics)
	}

	if block.ActiveProfile != "" && provider == model.AgentPi {
		if _, exists := block.Profiles[block.ActiveProfile]; !exists {
			*diagnostics = append(*diagnostics, diagnostic("config.pi-active-profile.unresolved", path+".activeProfile", fmt.Sprintf("activeProfile %q does not name a declared profile", block.ActiveProfile)))
		}
	}
}

// validatePiProfileNames refuses a profile name or phase key gentle-pi would
// refuse to load. Reporting nothing here would leave a typo the same way a
// dropped Pi model routing does: a profile that silently never applies.
func validatePiProfileNames(block ProviderSelection, path string, diagnostics *[]Diagnostic) {
	for _, name := range sortedProfileNames(block.Profiles) {
		profilePath := path + ".profiles." + name

		if _, reserved := reservedPiProfileNames[name]; reserved {
			*diagnostics = append(*diagnostics, diagnostic("config.pi-profile.name-reserved", profilePath, fmt.Sprintf("profile name %q is reserved", name)))
		} else if !safePiProfileName.MatchString(name) {
			*diagnostics = append(*diagnostics, diagnostic("config.pi-profile.name-unsupported", profilePath, fmt.Sprintf("unsupported profile name %q; gentle-pi accepts letters, digits, and ._-, starting with a letter or digit", name)))
		}

		if _, hasOrchestratorPhase := block.Profiles[name].PhaseAssignments[piReservedPhaseKey]; hasOrchestratorPhase {
			*diagnostics = append(*diagnostics, diagnostic("config.pi-profile.phase-reserved", profilePath+".phaseAssignments."+piReservedPhaseKey, fmt.Sprintf("phase %q is reserved for the profile's orchestrator entry", piReservedPhaseKey)))
		}
	}
}

// validatePiProfileValues refuses a profile orchestrator or phase assignment
// gentle-pi would refuse to load, the same way validatePiRoutingValues already
// refuses one under providers.pi.models.
func validatePiProfileValues(block ProviderSelection, path string, diagnostics *[]Diagnostic) {
	for _, name := range sortedProfileNames(block.Profiles) {
		profile := block.Profiles[name]
		profilePath := path + ".profiles." + name

		if profile.Orchestrator != nil {
			if profile.Orchestrator.Provider == "" || profile.Orchestrator.Model == "" {
				*diagnostics = append(*diagnostics, diagnostic("config.pi-profile.orchestrator-incomplete", profilePath+".orchestrator", "a profile orchestrator requires both provider and model"))
			}
			orchestrator := map[string]model.PiAgentRouting{piReservedPhaseKey: piRoutingFromAssignment(*profile.Orchestrator)}
			validatePiRoutingValues(orchestrator, profilePath, diagnostics)
		}

		if len(profile.PhaseAssignments) > 0 {
			phasesPath := profilePath + ".phaseAssignments"
			routings := make(map[string]model.PiAgentRouting, len(profile.PhaseAssignments))
			for _, phase := range sortedKeys(profile.PhaseAssignments) {
				assignment := profile.PhaseAssignments[phase]
				if (assignment.Provider == "") != (assignment.Model == "") {
					*diagnostics = append(*diagnostics, diagnostic("config.pi-profile.phase-incomplete", phasesPath+"."+phase, "a phase assignment requires both provider and model"))
				}
				routings[phase] = piRoutingFromAssignment(assignment)
			}
			validatePiRoutingValues(routings, phasesPath, diagnostics)
		}
	}
}

func validateProviderSkills(provider model.AgentID, block ProviderSelection, path string, declared map[model.AgentID]struct{}, diagnostics *[]Diagnostic) {
	if len(block.Skills) == 0 {
		return
	}
	if _, ok := declared[provider]; !ok {
		*diagnostics = append(*diagnostics, diagnostic("config.skill-assignment.undeclared-adapter", path+".skills", fmt.Sprintf("adapter %q takes skill assignments but is not declared; add it to agents or remove the assignment", provider)))
	}
}

func validateProviderMCPServers(provider model.AgentID, block ProviderSelection, path string, declared map[model.AgentID]struct{}, diagnostics *[]Diagnostic) {
	if len(block.MCPServers) == 0 {
		return
	}
	if _, ok := declared[provider]; !ok {
		*diagnostics = append(*diagnostics, diagnostic("config.mcp-assignment.undeclared-adapter", path+".mcpServers", fmt.Sprintf("adapter %q takes MCP servers but is not declared; add it to agents or remove the assignment", provider)))
		return
	}
	validateMCPServerSet(block.MCPServers, path+".mcpServers", diagnostics)
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
// left unassigned, and it resolves Pi's active profile into
// PiModelAssignments before that happens too, so an explicit Pi model
// assignment keeps winning over both the profile and the preset.
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
			var decoded map[string]model.PiAgentRouting
			_ = decodeStrict(block.Models, &decoded)
			selection.PiModelAssignments = decoded
			selection.PiModelFamily = block.ModelFamily
			selection.PiBackgroundIntent = model.PiBackgroundIntent(block.BackgroundIntent)
			selection.PiAgentProfiles = piProfilesToModel(block.Profiles)
			selection.PiActiveProfile = block.ActiveProfile
		}

		if len(block.Skills) > 0 {
			if selection.SkillAssignments == nil {
				selection.SkillAssignments = map[model.AgentID][]model.SkillID{}
			}
			selection.SkillAssignments[provider] = append([]model.SkillID(nil), block.Skills...)
		}

		if len(block.MCPServers) > 0 {
			if selection.MCPServerAssignments == nil {
				selection.MCPServerAssignments = map[model.AgentID]map[string]model.MCPServer{}
			}
			selection.MCPServerAssignments[provider] = mcpServersToModel(block.MCPServers)
		}
	}

	if len(presets) > 0 {
		selection.ModelPresets = presets
	}

	applyPiActiveProfile(providers, selection)
}

// applyPiActiveProfile materialises what gentle-pi's own "apply" would do for
// the declared active profile: the profile's phase entries become the Pi
// routing, with any model the document assigned directly on top, since an
// explicit assignment wins over both the profile and the preset it might
// later expand into.
func applyPiActiveProfile(providers map[model.AgentID]ProviderSelection, selection *model.Selection) {
	pi, ok := providers[model.AgentPi]
	if !ok || pi.ActiveProfile == "" {
		return
	}
	profile, ok := pi.Profiles[pi.ActiveProfile]
	if !ok {
		return
	}

	routing := make(map[string]model.PiAgentRouting, len(profile.PhaseAssignments)+1)
	if profile.Orchestrator != nil {
		routing[piReservedPhaseKey] = piRoutingFromAssignment(*profile.Orchestrator)
	}
	for phase, assignment := range profile.PhaseAssignments {
		routing[phase] = piRoutingFromAssignment(assignment)
	}
	for agent, entry := range selection.PiModelAssignments {
		routing[agent] = entry
	}

	selection.PiModelAssignments = routing
}

// piRoutingFromAssignment mirrors the mapping gentle-pi's own profile format
// uses: a phase's provider-qualified model becomes "<provider>/<model>", and
// its effort becomes the reasoning level.
func piRoutingFromAssignment(assignment ModelAssignment) model.PiAgentRouting {
	routing := model.PiAgentRouting{Thinking: model.PiThinkingLevel(assignment.Effort)}
	if assignment.Provider != "" || assignment.Model != "" {
		routing.Model = assignment.Provider + "/" + assignment.Model
	}
	return routing
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

// piProfilesToModel and piProfilesFromModel carry Pi's named profiles through
// model.Selection the same way providerProfilesToModel/FromModel already do
// for OpenCode, keyed by name instead of the slice OpenCode uses.
func piProfilesToModel(profiles map[string]ProviderProfile) map[string]model.Profile {
	converted := providerProfilesToModel(profiles)
	if len(converted) == 0 {
		return nil
	}

	keyed := make(map[string]model.Profile, len(converted))
	for _, profile := range converted {
		keyed[profile.Name] = profile
	}
	return keyed
}

func piProfilesFromModel(profiles map[string]model.Profile) map[string]ProviderProfile {
	if len(profiles) == 0 {
		return nil
	}

	slice := make([]model.Profile, 0, len(profiles))
	for _, profile := range profiles {
		slice = append(slice, profile)
	}
	return providerProfilesFromModel(slice)
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
		Models:           rawFromMap(selection.PiModelAssignments),
		ModelFamily:      selection.PiModelFamily,
		BackgroundIntent: string(selection.PiBackgroundIntent),
		Profiles:         piProfilesFromModel(selection.PiAgentProfiles),
		ActiveProfile:    selection.PiActiveProfile,
	}
	if !isZeroProviderSelection(pi) {
		providers[model.AgentPi] = pi
	}

	for provider, preset := range selection.ModelPresets {
		entry := providers[model.AgentID(provider)]
		entry.ModelPreset = preset
		providers[model.AgentID(provider)] = entry
	}

	for agent, skills := range selection.SkillAssignments {
		entry := providers[agent]
		entry.Skills = append([]model.SkillID(nil), skills...)
		providers[agent] = entry
	}

	for agent, servers := range selection.MCPServerAssignments {
		entry := providers[agent]
		entry.MCPServers = mcpServersFromModel(servers)
		providers[agent] = entry
	}

	if len(providers) == 0 {
		return nil
	}
	return providers
}

func isZeroProviderSelection(block ProviderSelection) bool {
	return len(block.Models) == 0 && block.ModelFamily == "" && block.ModelPreset == "" &&
		block.BackgroundIntent == "" && len(block.Profiles) == 0 && block.ProfileStrategy == "" &&
		block.ActiveProfile == "" && len(block.Skills) == 0 && len(block.MCPServers) == 0
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
