package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/filemerge"
	"os"
	"path/filepath"
	"sort"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/communitytool"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/engram"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/gga"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/mcp"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/opencodeplugin"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/permissions"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/persona"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/sdd"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/skills"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/theme"
	configdomain "github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/render"
	"github.com/gentleman-programming/gentle-ai/v2/internal/system"
)

// configurationStager materialises the configuration a document declares by
// running the same injectors the installer runs, with the staging root standing
// in for the directory they write to. Rendering must not grow its own copy of
// what a component writes: the injectors are the definition, and they already
// resolve every path through the adapter, so the root is the whole difference
// between installing and staging.
//
// readRoot stays the live home because a few injectors derive content from what
// is already installed there. Pointing those reads at an empty stage would make
// a render quietly disagree with the install it is supposed to preview.
//
// destination is where the staged bytes are headed. An injector that records an
// absolute path to a file it just wrote resolves it against the root it was
// given, so staging alone would bake the staging directory into the content and
// ship a live configuration pointing inside a directory that no longer exists.
type configurationStager struct {
	adapters    []model.AgentID
	readRoot    string
	destination string
}

// stageableComponents are the components that contribute files. Engram and GGA
// appear here as well as in provisionedComponents because they do both: they
// install a binary and they configure the clients to use it. Treating them as
// provisioning alone left a document declaring them producing no configuration
// at all, which read as an installation that simply did not want them.
var stageableComponents = map[model.ComponentID]bool{
	model.ComponentEngram:             true,
	model.ComponentGGA:                true,
	model.ComponentSkills:             true,
	model.ComponentPersona:            true,
	model.ComponentPermission:         true,
	model.ComponentContext7:           true,
	model.ComponentSDD:                true,
	model.ComponentTheme:              true,
	model.ComponentClaudeTheme:        true,
	model.ComponentOpenCodeGentleLogo: true,
}

func (stager configurationStager) Render(configdomain.DesiredState, map[string][]byte) ([]render.ArtifactContent, error) {
	return nil, nil
}

func (stager configurationStager) Stage(state configdomain.DesiredState, stageRoot string) error {
	selection := configdomain.Project(state)
	adapters := resolveAdapters(stager.adapters)

	for _, component := range selection.Components {
		if !stageableComponents[component] {
			continue
		}
		if err := stager.stageComponent(component, stageRoot, selection, adapters); err != nil {
			return err
		}
	}

	if err := stageDeclaredMCPServers(stageRoot, selection, adapters); err != nil {
		return err
	}

	if err := stageDeclaredPermissions(stageRoot, selection, adapters); err != nil {
		return err
	}

	if err := stageDeclaredExtensions(stageRoot, state, adapters); err != nil {
		return err
	}

	if err := stagePiBackgroundPolicy(stageRoot, selection, adapters); err != nil {
		return err
	}

	if err := stagePiModelRouting(stageRoot, selection, adapters); err != nil {
		return err
	}

	return stager.rebaseStagedPaths(stageRoot)
}

// rebaseStagedPaths retargets the staging root recorded inside staged content
// at the destination it stands in for. Without it the same document renders
// different bytes for every staging directory, which is the determinism the
// contract promises, and the applied file points at the staging directory.
func (stager configurationStager) rebaseStagedPaths(stageRoot string) error {
	if stager.destination == "" || stager.destination == stageRoot {
		return nil
	}

	staged, destination := []byte(stageRoot), []byte(stager.destination)

	return filepath.WalkDir(stageRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}

		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read staged file %q: %w", path, err)
		}
		if !bytes.Contains(contents, staged) {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect staged file %q: %w", path, err)
		}
		if _, err := filemerge.WriteFileAtomic(path, bytes.ReplaceAll(contents, staged, destination), info.Mode().Perm()); err != nil {
			return fmt.Errorf("rebase staged file %q: %w", path, err)
		}

		return nil
	})
}

func (stager configurationStager) stageComponent(component model.ComponentID, stageRoot string, selection model.Selection, adapters []agents.Adapter) error {
	// GGA configures every declared client at once rather than one at a time,
	// so it never enters the per-adapter loop.
	if component == model.ComponentGGA {
		if _, err := gga.Inject(stageRoot, agentIDs(adapters)); err != nil {
			return fmt.Errorf("stage GGA: %w", err)
		}

		return nil
	}

	if component == model.ComponentOpenCodeGentleLogo {
		if _, err := opencodeplugin.Install(stageRoot, model.OpenCodePluginGentleLogo); err != nil {
			return fmt.Errorf("stage OpenCode logo plugin: %w", err)
		}

		return nil
	}

	for _, adapter := range adapters {
		target := componentInjectionDirScoped(stageRoot, "", ScopeGlobal, adapter)

		if err := stager.stageComponentForAdapter(component, stageRoot, target, selection, adapter); err != nil {
			return fmt.Errorf("stage %s for %q: %w", component, adapter.Agent(), err)
		}
	}

	return nil
}

func (stager configurationStager) stageComponentForAdapter(
	component model.ComponentID,
	stageRoot string,
	target string,
	selection model.Selection,
	adapter agents.Adapter,
) error {
	switch component {
	case model.ComponentSkills:
		skillIDs := skillsForAdapter(selection, adapter.Agent())
		if len(skillIDs) == 0 {
			return nil
		}
		_, err := skills.Inject(target, adapter, skillIDs)

		return err

	case model.ComponentPersona:
		// Pi reads its persona from a runtime config that gentle-pi owns, not
		// from the prompt file the shared injection appends to. Routing it
		// through that shared path writes nothing at all, which is how a
		// declared persona disappeared from a Pi render.
		if adapter.Agent() == model.AgentPi {
			_, err := persona.InjectPiPersona(stageRoot, selection.Persona)

			return err
		}

		_, err := persona.Inject(target, adapter, selection.Persona)

		return err

	case model.ComponentPermission:
		_, err := permissions.Inject(stageRoot, adapter)

		return err

	case model.ComponentEngram:
		// Version is deliberately absent. It selects between two renderings of
		// the Engram protocol from whichever binary happens to be installed
		// where the render runs, which would make the same document render
		// differently on two machines. The unversioned reading is the
		// documented safe default.
		_, err := engram.InjectWithOptions(target, adapter, engram.InjectOptions{
			CodexOrchestratorAssignment: selection.CodexOrchestratorAssignment,
			CodexCarrilModelAssignments: selection.CodexCarrilModelAssignments,
			CodexModelAssignments:       selection.CodexModelAssignments,
			SkipRuntimeProbe:            true,
		})

		return err

	case model.ComponentContext7:
		_, err := mcp.Inject(stager.readRoot, target, adapter)

		return err

	case model.ComponentTheme:
		_, err := theme.Inject(stageRoot, adapter)

		return err

	case model.ComponentClaudeTheme:
		_, err := theme.InjectVisualThemes(stageRoot, adapter)

		return err

	case model.ComponentSDD:
		_, err := injectSDD(target, adapter, selection.SDDMode, stager.sddOptions(selection, adapter))

		return err
	}

	return nil
}

// sddOptions mirrors the installer's options exactly, so a staged SDD tree is
// the same tree an install would write for the same document.
func (stager configurationStager) sddOptions(selection model.Selection, adapter agents.Adapter) sdd.InjectOptions {
	return sdd.InjectOptions{
		OpenCodeModelAssignments:    selection.ModelAssignments,
		ClaudeModelAssignments:      selection.ClaudeModelAssignments,
		ClaudePhaseAssignments:      selection.ClaudePhaseAssignments,
		KiroModelAssignments:        selection.KiroModelAssignments,
		CodexModelAssignments:       selection.CodexModelAssignments,
		CodexCarrilModelAssignments: selection.CodexCarrilModelAssignments,
		CodexPhaseModelAssignments:  selection.CodexPhaseModelAssignments,
		StrictTDD:                   selection.StrictTDD,
		Profiles:                    selection.Profiles,
		CodeGraphGuidanceMarkdown:   codeGraphGuidanceMarkdownForSDD(stager.readRoot, selection.CommunityTools),

		// The installer also asks the local OpenCode whether it can run
		// background sub-agents, and a render cannot: probing the machine
		// would make the same document produce different prompts on two of
		// them. A document that says "on" has already made that call, which
		// is why only the explicit value carries the policy and `auto` --
		// the value that means "decide for me" -- carries nothing.
		IncludeOpenCodeBackgroundPolicy: adapter.Agent() == model.AgentOpenCode &&
			selection.BackgroundIntent == model.OpenCodeBackgroundOn,
	}
}

// stagePiBackgroundPolicy writes the policy gentle-pi reads. It runs outside
// the component loop because Pi's background sub-agents are not one of Gentle
// AI's components: they are a choice about the client, and gentle-pi owns the
// components that would otherwise have carried it.
//
// `auto` stages nothing, matching the installer: it means the runtime decides,
// and a resolved file would answer that on the runtime's behalf.
func stagePiBackgroundPolicy(stageRoot string, selection model.Selection, adapters []agents.Adapter) error {
	intent := selection.PiBackgroundIntent
	if intent == "" || intent == model.PiBackgroundAuto {
		return nil
	}

	declared := false
	for _, adapter := range adapters {
		if adapter.Agent() == model.AgentPi {
			declared = true
		}
	}
	if !declared {
		return nil
	}

	content, err := json.MarshalIndent(map[string]string{
		"schema": piBackgroundPolicySchema,
		"policy": string(intent),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Pi background policy: %w", err)
	}

	path := piBackgroundPolicyPath(stageRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create Pi background policy directory: %w", err)
	}

	return os.WriteFile(path, append(content, '\n'), 0o644)
}

// stagePiModelRouting writes the routing gentle-pi reads. Like the persona and
// the background policy it is a small file Gentle AI owns inside Pi's own
// directory, which is why it sits outside the component loop: routing is not
// one of Gentle AI's components, and gentle-pi owns the ones that would
// otherwise have carried it.
func stagePiModelRouting(stageRoot string, selection model.Selection, adapters []agents.Adapter) error {
	if len(selection.PiModelAssignments) == 0 {
		return nil
	}

	declared := false
	for _, adapter := range adapters {
		if adapter.Agent() == model.AgentPi {
			declared = true
		}
	}
	if !declared {
		return nil
	}

	content, err := json.MarshalIndent(selection.PiModelAssignments, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Pi model routing: %w", err)
	}

	path := filepath.Join(stageRoot, ".pi", "gentle-ai", "models.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create Pi model routing directory: %w", err)
	}

	return os.WriteFile(path, append(content, '\n'), 0o644)
}

// provisionedComponents are performed rather than written: a download or a
// clone. They carry no staged bytes, so the manifest records them as present
// and the plan reconciles them by presence.
var provisionedComponents = map[model.ComponentID]bool{
	model.ComponentEngram: true,
	model.ComponentGGA:    true,
}

// provisionedAgents install harness content through their own tool rather than
// installing the client. Every other adapter's install command installs the
// client itself, which is the machine's business and never a document's, so
// only these are carried.
var provisionedAgents = map[model.AgentID]bool{
	model.AgentPi: true,
}

// Resources declares what the document provisions, so a plan reports it instead
// of a document silently asking for something no operation ever mentions.
func (stager configurationStager) ProvisionedResources(state configdomain.DesiredState) []render.Resource {
	selection := configdomain.Project(state)
	resources := make([]render.Resource, 0, len(selection.Components)+len(selection.Agents))

	for _, component := range selection.Components {
		if !provisionedComponents[component] {
			continue
		}
		resources = append(resources, render.Resource{
			Path:      string(component),
			Selector:  render.ProvisionSelector,
			Digest:    render.ProvisionPresent,
			Component: component,
		})
	}

	resources = append(resources, agentProvisioning(selection.Agents)...)
	resources = append(resources, communityToolProvisioning(selection)...)

	return resources
}

// communityToolProvisioning carries the commands that point a declared tool at
// the declared adapters. Installing the tool is left out on purpose: which
// package manager to reach for is a property of the machine, and a consumer
// that gets its binaries from elsewhere -- a Nix installation does -- would be
// told to fetch a second copy of something it already has.
func communityToolProvisioning(selection model.Selection) []render.Resource {
	resources := make([]render.Resource, 0, len(selection.CommunityTools))

	for _, tool := range selection.CommunityTools {
		commands := communitytool.WiringCommandsFor(tool, selection.Agents)
		if len(commands) == 0 {
			continue
		}

		resources = append(resources, render.Resource{
			Path:     string(tool),
			Selector: render.ProvisionSelector,
			Digest:   render.ProvisionPresent,
			Tool:     tool,
			Commands: commands,
		})
	}

	return resources
}

// agentProvisioning reads each adapter's own install commands rather than
// restating them, so the packages a harness is made of stay the adapter's to
// name and a consumer never renders a stale copy of that list.
func agentProvisioning(selected []model.AgentID) []render.Resource {
	resources := make([]render.Resource, 0, len(selected))

	for _, agent := range selected {
		if !provisionedAgents[agent] {
			continue
		}

		adapter, err := agents.NewAdapter(agent)
		if err != nil {
			continue
		}

		// The profile is deliberately empty. Reading the local machine here
		// would make the same document render different commands on two
		// machines, and the commands these adapters return do not vary by
		// platform: they run the adapter's own tool, which is a precondition
		// rather than something a platform provides.
		commands, err := adapter.InstallCommand(system.PlatformProfile{})
		if err != nil || len(commands) == 0 {
			continue
		}

		resources = append(resources, render.Resource{
			Path:     string(agent),
			Selector: render.ProvisionSelector,
			Digest:   render.ProvisionPresent,
			Agent:    agent,
			Commands: commands,
		})
	}

	return resources
}

// liveProvisioning reports which declared components are already installed,
// reusing the detectors the installer consults rather than probing again.
func liveProvisioning(resources []render.Resource, profile system.PlatformProfile) map[render.ResourceKey]string {
	live := make(map[render.ResourceKey]string, len(resources))

	for _, resource := range resources {
		present := false
		switch resource.Component {
		case model.ComponentEngram:
			present = engram.VerifyInstalled() == nil
		case model.ComponentGGA:
			present = ggaAvailable(profile)
		}
		if present {
			live[render.ResourceKey{Path: resource.Path, Selector: resource.Selector}] = render.ProvisionPresent
		}
	}

	return live
}

// stageDeclaredMCPServers materialises the servers a document declares through
// each adapter's own MCP strategy. It runs outside the component loop because a
// declared server is configuration in its own right, not something the Context7
// component happens to bring along.
func stageDeclaredMCPServers(stageRoot string, selection model.Selection, adapters []agents.Adapter) error {
	for _, adapter := range adapters {
		servers := mcpServersForAdapter(selection, adapter.Agent())
		if len(servers) == 0 {
			continue
		}

		target := componentInjectionDirScoped(stageRoot, "", ScopeGlobal, adapter)
		if _, err := mcp.InjectDeclared(target, adapter, servers); err != nil {
			return fmt.Errorf("stage MCP servers for %q: %w", adapter.Agent(), err)
		}
	}

	return nil
}

// mcpServersForAdapter resolves what one adapter receives. A per-adapter set
// replaces the flat one for that adapter only, so the simple form keeps meaning
// "every adapter" and an adapter is only named when it must differ -- a client
// that identifies itself to a server, or an installation that gives one client
// tools another has no use for.
func mcpServersForAdapter(selection model.Selection, agent model.AgentID) []mcp.Server {
	declared := selection.MCPServers
	if assigned, ok := selection.MCPServerAssignments[agent]; ok {
		declared = assigned
	}

	servers := make([]mcp.Server, 0, len(declared))
	for _, name := range sortedServerNames(declared) {
		server := declared[name]
		servers = append(servers, mcp.Server{
			Name: name, Command: server.Command, Args: server.Args,
			Env: server.Env, URL: server.URL, Headers: server.Headers, Enabled: server.Enabled,
		})
	}

	return servers
}

// stageDeclaredPermissions layers the rules a document declares over whatever
// the permissions component already wrote, so a declaration adds to the shipped
// guardrails instead of replacing them.
func stageDeclaredPermissions(stageRoot string, selection model.Selection, adapters []agents.Adapter) error {
	if selection.Permissions == nil {
		return nil
	}

	declared := permissions.Declared{
		Allow: selection.Permissions.Allow,
		Deny:  selection.Permissions.Deny,
		Ask:   selection.Permissions.Ask,
	}

	for _, adapter := range adapters {
		if _, err := permissions.InjectDeclared(stageRoot, adapter, declared); err != nil {
			return fmt.Errorf("stage permissions for %q: %w", adapter.Agent(), err)
		}
	}

	return nil
}

func agentIDs(adapters []agents.Adapter) []model.AgentID {
	ids := make([]model.AgentID, 0, len(adapters))
	for _, adapter := range adapters {
		ids = append(ids, adapter.Agent())
	}

	return ids
}

func sortedServerNames(servers map[string]model.MCPServer) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}

// skillsForAdapter resolves what one adapter receives. A per-adapter assignment
// replaces the flat list for that adapter only, so the simple form keeps
// meaning "every adapter" and a document only names an adapter when it differs.
func skillsForAdapter(selection model.Selection, agent model.AgentID) []model.SkillID {
	if assigned, ok := selection.SkillAssignments[agent]; ok {
		return assigned
	}

	return selectedSkillIDs(selection)
}

// stageDeclaredExtensions merges each provider's extension block into that
// adapter's settings. An extension is the escape hatch for configuration the
// neutral contract does not model, so it lands verbatim rather than being
// reinterpreted, and only for the adapter it names.
func stageDeclaredExtensions(stageRoot string, state configdomain.DesiredState, adapters []agents.Adapter) error {
	if len(state.Extensions) == 0 {
		return nil
	}

	for _, adapter := range adapters {
		block, declared := state.Extensions[string(adapter.Agent())]
		if !declared {
			continue
		}

		settingsPath := adapter.SettingsPath(stageRoot)
		if settingsPath == "" {
			continue
		}
		if err := mergeExtensionBlock(settingsPath, block); err != nil {
			return fmt.Errorf("stage extension for %q: %w", adapter.Agent(), err)
		}
	}

	return nil
}

func mergeExtensionBlock(settingsPath string, block json.RawMessage) error {
	existing, err := os.ReadFile(settingsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read settings %q: %w", settingsPath, err)
	}

	merged, err := filemerge.MergeJSONObjects(existing, block)
	if err != nil {
		return fmt.Errorf("merge extension into %q: %w", settingsPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	if _, err := filemerge.WriteFileAtomic(settingsPath, merged, 0o644); err != nil {
		return fmt.Errorf("write settings %q: %w", settingsPath, err)
	}

	return nil
}
