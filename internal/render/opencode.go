package render

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/gentleman-programming/gentle-ai/v2/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
)

const openCodeSettingsPath = ".config/opencode/opencode.json"

type OpenCodeProvider struct{}

// Selectors names the agents this adapter owns inside the shared settings file,
// so the renderer never has to know which file that is.
func (OpenCodeProvider) Selectors(state config.DesiredState) map[string][]string {
	selectors := make([]string, 0, len(state.Roles))
	for _, role := range state.Roles {
		name := role.RenderedName
		if name == "" {
			name = string(role.ID)
		}
		selectors = append(selectors, "/agent/"+name)
	}

	return map[string][]string{openCodeSettingsPath: selectors}
}

// Resources splits the settings file into the agents this adapter owns, leaving
// every unrelated key in it untouched and unowned.
func (OpenCodeProvider) Resources(path string, contents []byte, selectors []string) ([]Resource, error) {
	var settings map[string]any
	if err := json.Unmarshal(contents, &settings); err != nil {
		return nil, fmt.Errorf("parse staged OpenCode settings: %w", err)
	}
	agents, _ := settings["agent"].(map[string]any)
	resources := make([]Resource, 0, len(selectors))
	for _, selector := range selectors {
		name, ok := openCodeAgentName(selector)
		if !ok {
			return nil, fmt.Errorf("invalid staged OpenCode selector %q", selector)
		}
		agent, ok := agents[name]
		if !ok {
			return nil, fmt.Errorf("staged OpenCode agent %q is missing", name)
		}
		encoded, err := json.Marshal(agent)
		if err != nil {
			return nil, fmt.Errorf("encode staged OpenCode agent %q: %w", name, err)
		}
		resources = append(resources, Resource{Path: path, Selector: selector, Digest: resourceDigest(encoded)})
	}
	return resources, nil
}

func (OpenCodeProvider) Render(state config.DesiredState, baseline map[string][]byte) ([]ArtifactContent, error) {
	roles := make(map[config.RoleID]string, len(state.Roles))
	for _, role := range state.Roles {
		name := role.RenderedName
		if name == "" {
			name = string(role.ID)
		}
		roles[role.ID] = name
	}

	agents := make(map[string]any, len(state.Roles))
	for _, role := range state.Roles {
		references := make([]string, 0, len(role.References))
		for _, reference := range role.References {
			name, ok := roles[config.RoleID(reference)]
			if !ok {
				return nil, fmt.Errorf("resolve OpenCode role %q", reference)
			}
			references = append(references, name)
		}
		sort.Strings(references)
		agents[roles[role.ID]] = map[string]any{"references": references}
	}

	overlay, err := json.Marshal(map[string]any{"agent": agents})
	if err != nil {
		return nil, fmt.Errorf("encode OpenCode overlay: %w", err)
	}
	merged, err := filemerge.MergeJSONObjects(baseline[openCodeSettingsPath], overlay)
	if err != nil {
		return nil, fmt.Errorf("compose OpenCode settings: %w", err)
	}
	return []ArtifactContent{{Path: openCodeSettingsPath, Contents: merged}}, nil
}

// Merge applies one owned agent into the live settings file, leaving every
// unrelated key in it untouched.
func (OpenCodeProvider) Merge(operation Operation, source string, target []byte) ([]byte, error) {
	name, ok := openCodeAgentName(operation.Selector)
	if !ok {
		return nil, fmt.Errorf("unsupported resource selector %q", operation.Selector)
	}
	settings := map[string]any{}
	if len(target) != 0 {
		if err := json.Unmarshal(target, &settings); err != nil {
			return nil, fmt.Errorf("parse target OpenCode settings: %w", err)
		}
	}
	agents, _ := settings["agent"].(map[string]any)
	if agents == nil {
		agents = map[string]any{}
		settings["agent"] = agents
	}
	if operation.Kind == Remove {
		delete(agents, name)
		return json.Marshal(settings)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}
	var staged map[string]any
	if err := json.Unmarshal(contents, &staged); err != nil {
		return nil, fmt.Errorf("parse staged OpenCode settings: %w", err)
	}
	stagedAgents, _ := staged["agent"].(map[string]any)
	agent, exists := stagedAgents[name]
	if !exists {
		return nil, fmt.Errorf("staged OpenCode agent %q is missing", name)
	}
	agents[name] = agent
	return json.Marshal(settings)
}

func openCodeAgentName(selector string) (string, bool) {
	const prefix = "/agent/"
	if len(selector) <= len(prefix) || selector[:len(prefix)] != prefix {
		return "", false
	}
	return selector[len(prefix):], true
}
