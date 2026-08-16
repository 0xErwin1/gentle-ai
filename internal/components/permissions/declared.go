package permissions

import (
	"encoding/json"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// Declared are the permission rules a document adds on top of the guardrails
// gentle-ai ships. They are merged over the bundled overlay rather than
// replacing it, so declaring an allowance never quietly removes a deny the
// project considers a floor.
type Declared struct {
	Allow []string
	Deny  []string
	Ask   []string
}

// SupportsDeclaredRules reports whether an adapter expresses permissions as the
// allow/deny/ask rule lists a document declares. It reads the shipped overlay
// rather than naming adapters, so an adapter whose overlay changes shape stops
// or starts qualifying on its own.
//
// The distinction matters because the adapters that do not qualify still have
// an overlay: OpenCode keys its permissions per tool and glob under a different
// name entirely. Writing rule lists into those settings adds a key the client
// never reads, which looks configured and does nothing.
func SupportsDeclaredRules(id model.AgentID) bool {
	overlay := agentOverlay(id)
	if overlay == nil {
		return false
	}

	var shipped struct {
		Permissions map[string]json.RawMessage `json:"permissions"`
	}
	if json.Unmarshal(overlay, &shipped) != nil {
		return false
	}

	for _, rule := range []string{"allow", "deny", "ask"} {
		raw, present := shipped.Permissions[rule]
		if !present {
			continue
		}
		var values []string
		if json.Unmarshal(raw, &values) == nil {
			return true
		}
	}

	return false
}

// InjectDeclared merges declared permission rules into the adapter's settings.
// An adapter that does not express permissions as rule lists takes none, which
// the caller reports rather than dropping.
func InjectDeclared(homeDir string, adapter agents.Adapter, declared Declared) (InjectionResult, error) {
	if declared.empty() {
		return InjectionResult{}, nil
	}

	settingsPath := adapter.SettingsPath(homeDir)
	if settingsPath == "" || !SupportsDeclaredRules(adapter.Agent()) {
		return InjectionResult{}, nil
	}

	// JSON merge replaces an array wholesale, so merging a declared deny list
	// over the shipped one would delete every guardrail the document did not
	// repeat. The lists are unioned against what is already there instead.
	existing, err := osReadFile(settingsPath)
	if err != nil {
		return InjectionResult{}, fmt.Errorf("read settings %q: %w", settingsPath, err)
	}

	overlay, err := json.Marshal(map[string]any{"permissions": declared.unionedWith(existing)})
	if err != nil {
		return InjectionResult{}, fmt.Errorf("encode declared permissions: %w", err)
	}

	writeResult, err := mergeJSONFile(settingsPath, overlay)
	if err != nil {
		return InjectionResult{}, err
	}

	return InjectionResult{Changed: writeResult.Changed, Files: []string{settingsPath}}, nil
}

func (declared Declared) empty() bool {
	return len(declared.Allow) == 0 && len(declared.Deny) == 0 && len(declared.Ask) == 0
}

// unionedWith merges each declared list into the one already written, keeping
// order stable and dropping repeats, so a rule the document restates does not
// appear twice and a rule it never mentions survives.
func (declared Declared) unionedWith(existing []byte) map[string]any {
	// The shipped block mixes types: defaultMode is a string alongside the rule
	// arrays, so decoding the whole object into one array-valued map fails and
	// silently reads as empty, which is how a union quietly becomes a replace.
	current := map[string][]string{}
	if len(existing) > 0 {
		var settings struct {
			Permissions map[string]json.RawMessage `json:"permissions"`
		}
		if json.Unmarshal(existing, &settings) == nil {
			for key, raw := range settings.Permissions {
				var values []string
				if json.Unmarshal(raw, &values) == nil {
					current[key] = values
				}
			}
		}
	}

	rules := map[string]any{}
	for key, values := range map[string][]string{"allow": declared.Allow, "deny": declared.Deny, "ask": declared.Ask} {
		if union := unionStrings(current[key], values); len(union) > 0 {
			rules[key] = union
		}
	}

	return rules
}

func unionStrings(shipped, declared []string) []string {
	seen := make(map[string]struct{}, len(shipped)+len(declared))
	union := make([]string, 0, len(shipped)+len(declared))

	for _, group := range [][]string{shipped, declared} {
		for _, value := range group {
			if _, repeated := seen[value]; repeated {
				continue
			}
			seen[value] = struct{}{}
			union = append(union, value)
		}
	}

	return union
}
