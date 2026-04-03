package agentbuilder

import (
	"encoding/json"
	"errors"
	"os"
)

// knownBuiltinSkills is the set of built-in skill names shipped with Gentleman AI.
// A custom agent name must not collide with these.
var knownBuiltinSkills = map[string]struct{}{
	"sdd-init":       {},
	"sdd-apply":      {},
	"sdd-verify":     {},
	"sdd-explore":    {},
	"sdd-propose":    {},
	"sdd-spec":       {},
	"sdd-design":     {},
	"sdd-tasks":      {},
	"sdd-archive":    {},
	"sdd-onboard":    {},
	"go-testing":     {},
	"skill-creator":  {},
	"judgment-day":   {},
	"branch-pr":      {},
	"issue-creation": {},
}

// LoadRegistry reads the registry JSON from path.
// If the file does not exist an empty Registry with Version 1 is returned.
func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Registry{Version: 1, Agents: []RegistryEntry{}}, nil
		}
		return nil, err
	}

	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, err
	}
	return &reg, nil
}

// SaveRegistry writes reg to path as indented JSON.
// The parent directory must already exist.
func SaveRegistry(path string, reg *Registry) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Add appends entry to the registry.
func (r *Registry) Add(entry RegistryEntry) {
	r.Agents = append(r.Agents, entry)
}

// FindByName returns the first RegistryEntry whose Name matches, or nil.
func (r *Registry) FindByName(name string) *RegistryEntry {
	for i := range r.Agents {
		if r.Agents[i].Name == name {
			return &r.Agents[i]
		}
	}
	return nil
}

// RemoveByName removes the first entry matching name.
// Returns true when an entry was found and removed.
func (r *Registry) RemoveByName(name string) bool {
	for i, entry := range r.Agents {
		if entry.Name == name {
			r.Agents = append(r.Agents[:i], r.Agents[i+1:]...)
			return true
		}
	}
	return false
}

// HasConflictWithBuiltin reports whether name collides with a known built-in skill.
func HasConflictWithBuiltin(name string) bool {
	_, ok := knownBuiltinSkills[name]
	return ok
}
