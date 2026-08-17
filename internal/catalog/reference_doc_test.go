package catalog_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/catalog"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

const referencePath = "../../docs/declarative-config-reference.md"

// Reference documentation that lists a vocabulary is only useful while the
// vocabulary is the one the binary enforces. A document naming a skill that was
// renamed a release ago is worse than no list: it reads as authoritative and
// sends the reader to write something the validator rejects.
func TestReferenceDocumentsTheVocabularyTheBinaryEnforces(t *testing.T) {
	reference, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}
	document := string(reference)

	for _, test := range []struct {
		name    string
		heading string
		want    []string
	}{
		{name: "providers", heading: "### Providers", want: agentIDs()},
		{name: "skills", heading: "### Skills", want: skillIDs()},
	} {
		t.Run(test.name, func(t *testing.T) {
			documented := backquoted(section(t, document, test.heading))

			if missing := difference(test.want, documented); len(missing) > 0 {
				t.Errorf("the reference omits %v; add them under %q", missing, test.heading)
			}
			if extra := difference(documented, test.want); len(extra) > 0 {
				t.Errorf("the reference lists %v, which the binary does not accept; remove them", extra)
			}
		})
	}

	t.Run("components", func(t *testing.T) {
		// Components are documented as a table rather than a list, so the whole
		// section is read and only the omissions are asserted.
		documented := backquoted(tableSection(document, "### Components"))
		want := componentIDs()

		if missing := difference(want, documented); len(missing) > 0 {
			t.Errorf("the reference omits component(s) %v", missing)
		}
	})
}

// section returns the first paragraph after a heading, which is where these
// sections carry their list. Reading further would sweep in the prose that
// follows, where a backquoted word is a field name rather than a vocabulary
// entry.
func section(t *testing.T, document, heading string) string {
	t.Helper()

	start := strings.Index(document, heading)
	if start < 0 {
		t.Fatalf("the reference has no %q section", heading)
	}
	rest := strings.TrimLeft(document[start+len(heading):], "\n")

	if end := strings.Index(rest, "\n\n"); end >= 0 {
		return rest[:end]
	}

	return rest
}

func tableSection(document, heading string) string {
	start := strings.Index(document, heading)
	if start < 0 {
		return ""
	}
	rest := document[start+len(heading):]

	if end := strings.Index(rest, "\n## "); end >= 0 {
		return rest[:end]
	}

	return rest
}

var backquotedPattern = regexp.MustCompile("`([a-z0-9][a-z0-9-]*)`")

func backquoted(text string) []string {
	seen := map[string]struct{}{}
	values := make([]string, 0)
	for _, match := range backquotedPattern.FindAllStringSubmatch(text, -1) {
		if _, repeated := seen[match[1]]; repeated {
			continue
		}
		seen[match[1]] = struct{}{}
		values = append(values, match[1])
	}
	sort.Strings(values)

	return values
}

func difference(from, without []string) []string {
	excluded := make(map[string]struct{}, len(without))
	for _, value := range without {
		excluded[value] = struct{}{}
	}

	missing := make([]string, 0)
	for _, value := range from {
		if _, ok := excluded[value]; !ok {
			missing = append(missing, value)
		}
	}

	return missing
}

func agentIDs() []string {
	values := make([]string, 0)
	for _, agent := range allSupportedAgents() {
		values = append(values, string(agent))
	}
	sort.Strings(values)

	return values
}

func allSupportedAgents() []model.AgentID {
	candidates := []model.AgentID{
		model.AgentClaudeCode, model.AgentOpenCode, model.AgentKilocode, model.AgentGeminiCLI,
		model.AgentCodex, model.AgentCursor, model.AgentVSCodeCopilot, model.AgentAntigravity,
		model.AgentWindsurf, model.AgentKimi, model.AgentQwenCode, model.AgentKiroIDE,
		model.AgentOpenClaw, model.AgentPi, model.AgentTrae, model.AgentHermes,
	}

	supported := make([]model.AgentID, 0, len(candidates))
	for _, agent := range candidates {
		if catalog.IsSupportedAgent(agent) {
			supported = append(supported, agent)
		}
	}

	return supported
}

func skillIDs() []string {
	values := make([]string, 0, len(catalog.MVPSkills()))
	for _, skill := range catalog.MVPSkills() {
		values = append(values, string(skill.ID))
	}
	sort.Strings(values)

	return values
}

func componentIDs() []string {
	values := make([]string, 0, len(catalog.MVPComponents()))
	for _, component := range catalog.MVPComponents() {
		values = append(values, string(component.ID))
	}
	sort.Strings(values)

	return values
}
