package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/filemerge"
	"os"
	"path/filepath"

	"github.com/gentleman-programming/gentle-ai/v3/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/agentguidance"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/communitytool"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/engram"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/gga"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/mcp"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/opencodeplugin"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/permissions"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/persona"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/skills"
	"github.com/gentleman-programming/gentle-ai/v3/internal/components/theme"
	configdomain "github.com/gentleman-programming/gentle-ai/v3/internal/config"
	"github.com/gentleman-programming/gentle-ai/v3/internal/model"
	"github.com/gentleman-programming/gentle-ai/v3/internal/render"
	"github.com/gentleman-programming/gentle-ai/v3/internal/system"
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

	// Guidance runs outside the component loop, exactly where the installer
	// schedules it: it is installed for every agent that can hold it, never
	// because a component was selected. An agent without it has no way to
	// choose between direct, delegated and proposed work, and skipping it was
	// how a declaratively rendered home lost the mandatory ODD protocol while
	// the installer kept writing it.
	if err := stageRoutingGuidance(stageRoot, adapters); err != nil {
		return err
	}

	if err := stagePiBackgroundPolicy(stageRoot, selection, adapters); err != nil {
		return err
	}

	return stager.rebaseStagedPaths(stageRoot)
}

// stageRoutingGuidance mirrors the installer's routing step against the staging
// root, so a rendered home carries the same always-on guidance an install would
// write. The injector owns every path decision -- the adapter resolves the file
// it writes, and the injector reserves a Jinja adapter's router template before
// its module -- so this only has to hand each agent the root that stands in for
// its installation directory.
//
// Pi is skipped, matching the installer: gentle-pi owns the Pi parent's
// guidance, and the installer only retires legacy sections from it, so staging
// one would describe an installation that does not exist.
//
// The OpenCode family is the one case where the installer and a render can
// legitimately disagree about the file: install resolves the effective layered
// project-over-global settings path from the working directory, and a render
// must not depend on where it happens to run. Leaving the option empty keeps the
// adapter's global fallback, avoiding dependence on the render's working directory.
func stageRoutingGuidance(stageRoot string, adapters []agents.Adapter) error {
	for _, adapter := range adapters {
		agent := adapter.Agent()
		if agent == model.AgentPi || !adapter.SupportsSystemPrompt() {
			continue
		}

		target := componentInjectionDirScoped(stageRoot, "", ScopeGlobal, adapter)
		if agentguidance.DeliversThroughOrchestratorPrompt(agent) {
			target = stageRoot
		}

		if _, err := agentguidance.InjectRoutingWithOptions(target, agent, agentguidance.RoutingOptions{}); err != nil {
			return fmt.Errorf("stage routing guidance for %q: %w", agent, err)
		}
	}

	return nil
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
		skillIDs := selectedSkillIDs(selection)
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

	}

	return nil
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
// of a document silently asking for something no operation ever mentions. The
// returned error surfaces a provisioning request the selection cannot honor
// (an unsupported Pi package source override, say) instead of the resource
// quietly dropping out of the manifest while the caller reports success.
func (stager configurationStager) ProvisionedResources(state configdomain.DesiredState) ([]render.Resource, error) {
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

	agentResources, err := agentProvisioning(selection)
	if err != nil {
		return nil, err
	}
	resources = append(resources, agentResources...)
	resources = append(resources, communityToolProvisioning(selection)...)

	return resources, nil
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
// name and a consumer never renders a stale copy of that list. An adapter
// that fails to build its install commands returns an error naming the
// agent, rather than the resource silently dropping out of the manifest: an
// install command an operator asked for but never got is a rendering
// failure, not an adapter that happens to install nothing.
func agentProvisioning(selection model.Selection) ([]render.Resource, error) {
	resources := make([]render.Resource, 0, len(selection.Agents))

	for _, agent := range selection.Agents {
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
		if err != nil {
			return nil, fmt.Errorf("provision %s: %w", agent, err)
		}
		if len(commands) == 0 {
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

	return resources, nil
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

func agentIDs(adapters []agents.Adapter) []model.AgentID {
	ids := make([]model.AgentID, 0, len(adapters))
	for _, adapter := range adapters {
		ids = append(ids, adapter.Agent())
	}

	return ids
}
