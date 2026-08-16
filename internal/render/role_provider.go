package render

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
)

// RoleProvider materialises declared roles for an adapter that keeps agents as
// files. It reads the layout from the adapter rather than knowing any adapter's
// directory, so one implementation covers every adapter that expresses roles
// this way and a new one costs nothing.
type RoleProvider struct {
	adapter agents.Adapter
}

func NewRoleProvider(adapter agents.Adapter) RoleProvider {
	return RoleProvider{adapter: adapter}
}

func (RoleProvider) Render(config.DesiredState, map[string][]byte) ([]ArtifactContent, error) {
	return nil, nil
}

func (provider RoleProvider) Stage(state config.DesiredState, stageRoot string) error {
	directory := provider.adapter.SubAgentsDir(stageRoot)
	if directory == "" {
		return nil
	}
	if len(state.Roles) == 0 {
		return nil
	}

	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create sub-agent directory: %w", err)
	}

	for _, role := range state.Roles {
		name := renderedRoleName(role)
		path := filepath.Join(directory, name+".md")
		if err := os.WriteFile(path, roleDocument(role, name), 0o644); err != nil {
			return fmt.Errorf("write sub-agent %q: %w", name, err)
		}
	}

	return nil
}

// roleDocument renders only what the document declared. A field the role left
// out is left out of the file: filling it with a default would put words in the
// operator's mouth and make the rendered agent disagree with the document.
func roleDocument(role config.Role, name string) []byte {
	frontmatter := []string{"---", "name: " + name}

	if role.Description != "" {
		frontmatter = append(frontmatter, "description: "+role.Description)
	}
	if role.Model != nil {
		frontmatter = append(frontmatter, "model: "+role.Model.Model)
		if role.Model.Effort != "" {
			frontmatter = append(frontmatter, "effort: "+role.Model.Effort)
		}
	}
	if len(role.Tools) > 0 {
		frontmatter = append(frontmatter, "tools: "+strings.Join(role.Tools, ", "))
	}
	if references := referencedNames(role); len(references) > 0 {
		frontmatter = append(frontmatter, "references: "+strings.Join(references, ", "))
	}
	frontmatter = append(frontmatter, "---", "")

	document := strings.Join(frontmatter, "\n")
	if role.Prompt != "" {
		document += role.Prompt + "\n"
	}

	return []byte(document)
}

func referencedNames(role config.Role) []string {
	names := make([]string, 0, len(role.References))
	for _, reference := range role.References {
		names = append(names, string(reference))
	}
	sort.Strings(names)

	return names
}

func renderedRoleName(role config.Role) string {
	if role.RenderedName != "" {
		return role.RenderedName
	}

	return string(role.ID)
}
