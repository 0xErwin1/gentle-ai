package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/gentleman-programming/gentle-ai/internal/components/sdd"
	"github.com/gentleman-programming/gentle-ai/internal/components/skills"
	"github.com/gentleman-programming/gentle-ai/internal/model"
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
	skillDir := filepath.Join(homeDir, ".agents", "skills")
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

func existingCompatibilitySkillFiles(homeDir string) []string {
	skillDir, ok := existingCompatibilitySkillsDir(homeDir)
	if !ok {
		return nil
	}
	var files []string
	_ = filepath.Walk(skillDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	return files
}

func (s compatibilitySkillsRefreshStep) Run() error {
	skillDir := filepath.Join(s.homeDir, ".agents", "skills")
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
