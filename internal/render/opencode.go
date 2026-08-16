package render

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/gentleman-programming/gentle-ai/v2/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
)

const openCodeSettingsPath = ".config/opencode/opencode.json"

type OpenCodeProvider struct{}

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
