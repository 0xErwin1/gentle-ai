package cli

import (
	"fmt"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents"
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
type configurationStager struct {
	adapters []model.AgentID
	readRoot string
}

// stageableComponents are the components whose entire contribution is files.
// Provisioning components are deliberately absent: downloading a binary or
// cloning a repository is an action, and an action has no staged bytes.
var stageableComponents = map[model.ComponentID]bool{
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

	return nil
}

func (stager configurationStager) stageComponent(component model.ComponentID, stageRoot string, selection model.Selection, adapters []agents.Adapter) error {
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
		_, err := persona.Inject(target, adapter, selection.Persona)

		return err

	case model.ComponentPermission:
		_, err := permissions.Inject(stageRoot, adapter)

		return err

	case model.ComponentContext7:
		_, err := mcp.Inject(stager.readRoot, target, adapter)

		return err

	case model.ComponentTheme:
		_, err := theme.Inject(stageRoot, adapter)

		return err

	case model.ComponentClaudeTheme:
		_, err := theme.InjectClaudeTheme(stageRoot, adapter)

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
