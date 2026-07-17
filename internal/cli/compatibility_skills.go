package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/components/sdd"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/skills"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// compatibilitySkillsRefreshStep refreshes the registry-scanned shared skills
// path once after adapter-specific component injection has completed. The path
// remains opt-in: installs and syncs never create it when it is absent.
type compatibilitySkillsRefreshStep struct {
	id           string
	homeDir      string
	components   []model.ComponentID
	selection    model.Selection
	changedFiles *[]string
}

func (s compatibilitySkillsRefreshStep) ID() string {
	if s.id != "" {
		return s.id
	}
	return "compatibility-skills-refresh"
}

func needsCompatibilitySkillsRefresh(components []model.ComponentID) bool {
	return slices.Contains(components, model.ComponentSkills) || slices.Contains(components, model.ComponentSDD)
}

func existingCompatibilitySkillsDir(homeDir string) (string, bool) {
	agentsDir := filepath.Join(homeDir, ".agents")
	parent, err := os.Lstat(agentsDir)
	if err != nil || !parent.IsDir() {
		return filepath.Join(agentsDir, "skills"), false
	}
	skillDir := filepath.Join(agentsDir, "skills")
	info, err := os.Lstat(skillDir)
	return skillDir, err == nil && info.IsDir()
}

func compatibilitySkillsRefreshable(homeDir string, selection model.Selection) bool {
	if _, ok := existingCompatibilitySkillsDir(homeDir); !ok {
		return false
	}
	return slices.Contains(selection.Components, model.ComponentSDD) ||
		slices.Contains(selection.Components, model.ComponentSkills) && len(selectedSkillIDs(selection)) > 0
}

func compatibilitySkillFiles(homeDir string, components []model.ComponentID, selection model.Selection) ([]string, error) {
	skillDir, ok := existingCompatibilitySkillsDir(homeDir)
	if !ok {
		return nil, nil
	}
	paths := map[string]struct{}{}
	if err := filepath.Walk(skillDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			paths[path] = struct{}{}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk compatibility skills directory: %w", err)
	}
	if slices.Contains(components, model.ComponentSkills) {
		prospective, err := skills.DirectoryPaths(skillDir, selectedSkillIDs(selection), "")
		if err != nil {
			return nil, fmt.Errorf("enumerate compatibility skills: %w", err)
		}
		for _, path := range prospective {
			paths[path] = struct{}{}
		}
	}
	if slices.Contains(components, model.ComponentSDD) {
		prospective, err := sdd.SkillDirectoryPaths(skillDir, "")
		if err != nil {
			return nil, fmt.Errorf("enumerate compatibility SDD skills: %w", err)
		}
		for _, path := range prospective {
			paths[path] = struct{}{}
		}
	}
	files := make([]string, 0, len(paths))
	for path := range paths {
		files = append(files, path)
	}
	sort.Strings(files)
	if err := validateCompatibilityDestinations(skillDir, files); err != nil {
		return nil, err
	}
	return files, nil
}

func validateCompatibilityDestinations(root string, destinations []string) error {
	for _, destination := range destinations {
		relative, err := filepath.Rel(root, destination)
		if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("compatibility destination %q escapes %q", destination, root)
		}
		current := root
		for _, part := range strings.Split(filepath.Dir(relative), string(filepath.Separator)) {
			if part == "." || part == "" {
				continue
			}
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if os.IsNotExist(err) {
				break
			}
			if err != nil {
				return fmt.Errorf("stat compatibility destination ancestor %q: %w", current, err)
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("compatibility destination ancestor %q must be a physical directory", current)
			}
		}
		info, err := os.Lstat(destination)
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			return fmt.Errorf("compatibility destination %q must be a regular file", destination)
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("stat compatibility destination %q: %w", destination, err)
		}
	}
	return nil
}

func (s compatibilitySkillsRefreshStep) Run() error {
	agentsDir := filepath.Join(s.homeDir, ".agents")
	parent, err := os.Lstat(agentsDir)
	if os.IsNotExist(err) || err == nil && !parent.IsDir() {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat compatibility skills parent directory: %w", err)
	}
	skillDir := filepath.Join(agentsDir, "skills")
	info, err := os.Lstat(skillDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat compatibility skills directory: %w", err)
	}
	if !info.IsDir() {
		return nil
	}

	destinations, err := compatibilitySkillFiles(s.homeDir, s.components, s.selection)
	if err != nil {
		return err
	}
	if err := validateCompatibilityDestinations(skillDir, destinations); err != nil {
		return err
	}

	var changed []string
	if slices.Contains(s.components, model.ComponentSkills) {
		skillIDs := selectedSkillIDs(s.selection)
		if len(skillIDs) > 0 {
			result, injectErr := skills.InjectDirectory(skillDir, skillIDs)
			if injectErr != nil {
				return fmt.Errorf("refresh compatibility skills: %w", injectErr)
			}
			if result.Changed {
				changed = append(changed, result.Files...)
			}
		}
	}

	if slices.Contains(s.components, model.ComponentSDD) {
		result, injectErr := sdd.InjectSkillDirectory(skillDir, "")
		if injectErr != nil {
			return fmt.Errorf("refresh compatibility SDD skills: %w", injectErr)
		}
		if result.Changed {
			changed = append(changed, result.Files...)
		}
	}

	if s.changedFiles != nil {
		*s.changedFiles = append(*s.changedFiles, changed...)
	}
	return nil
}
