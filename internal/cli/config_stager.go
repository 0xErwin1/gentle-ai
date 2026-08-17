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
	}
}

// provisionedComponents are performed rather than written: a download or a
// clone. They carry no staged bytes, so the manifest records them as present
// and the plan reconciles them by presence.
var provisionedComponents = map[model.ComponentID]bool{
	model.ComponentEngram: true,
	model.ComponentGGA:    true,
}

// Resources declares what the document provisions, so a plan reports it instead
// of a document silently asking for something no operation ever mentions.
func (stager configurationStager) ProvisionedResources(state configdomain.DesiredState) []render.Resource {
	selection := configdomain.Project(state)
	resources := make([]render.Resource, 0, len(selection.Components))

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
	if len(selection.MCPServers) == 0 {
		return nil
	}

	servers := make([]mcp.Server, 0, len(selection.MCPServers))
	for _, name := range sortedServerNames(selection.MCPServers) {
		declared := selection.MCPServers[name]
		servers = append(servers, mcp.Server{
			Name: name, Command: declared.Command, Args: declared.Args,
			Env: declared.Env, URL: declared.URL, Enabled: declared.Enabled,
		})
	}

	for _, adapter := range adapters {
		target := componentInjectionDirScoped(stageRoot, "", ScopeGlobal, adapter)
		if _, err := mcp.InjectDeclared(target, adapter, servers); err != nil {
			return fmt.Errorf("stage MCP servers for %q: %w", adapter.Agent(), err)
		}
	}

	return nil
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
