package cli

import (
	"reflect"
	"sort"
	"testing"

	configdomain "github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
)

// Every configuration choice a user can make imperatively must be expressible
// declaratively. These tables record where each imperative field stands against
// that rule, and the test below fails when a field escapes classification, when
// a recorded gap is silently closed, or when an entry outlives its field.
type parityDisposition struct {
	// declarativeField names the config.Selection field that carries this
	// intent. For a gap it names the field that must eventually carry it.
	declarativeField string

	// gap marks a user-settable surface with no declarative representation.
	gap bool

	// exemptBecause marks a field the parity rule does not reach, and why.
	exemptBecause string
}

func represented(field string) parityDisposition {
	return parityDisposition{declarativeField: field}
}

func gap(intendedField string) parityDisposition {
	return parityDisposition{declarativeField: intendedField, gap: true}
}

func exempt(reason string) parityDisposition {
	return parityDisposition{exemptBecause: reason}
}

const (
	observedState   = "observed runtime state, never set by a user"
	decodingDetail  = "decoding detail with no user-facing intent"
	operationalFlag = "operational invocation flag, not durable intent"
	explicitByFile  = "a declarative document is itself the explicit selection"
)

// selectionParity classifies the semantic selection the planner and installer consume.
var selectionParity = map[string]parityDisposition{
	"Agents":             represented("Agents"),
	"Components":         represented("Components"),
	"Skills":             represented("Skills"),
	"Persona":            represented("Persona"),
	"Preset":             represented("Preset"),
	"SDDMode":            represented("SDDMode"),
	"SDDProfileStrategy": represented("SDDProfileStrategy"),
	"StrictTDD":          represented("StrictTDD"),
	"Profiles":           represented("Profiles"),
	"BackgroundIntent":   represented("BackgroundIntent"),
	"Scope":              represented("Scope"),
	"Channel":            represented("Channel"),
	"RDDMode":            represented("RDDMode"),

	"ModelAssignments":            represented("ModelAssignments"),
	"ClaudeModelAssignments":      represented("ClaudeModelAssignments"),
	"ClaudePhaseAssignments":      represented("ClaudePhaseAssignments"),
	"KiroModelAssignments":        represented("KiroModelAssignments"),
	"CodexModelAssignments":       represented("CodexModelAssignments"),
	"CodexOrchestratorAssignment": represented("CodexOrchestrator"),
	"CodexCarrilModelAssignments": represented("CodexCarrilModelAssignments"),
	"CodexPhaseModelAssignments":  represented("CodexPhaseModelAssignments"),
	"OpenCodePlugins":             represented("OpenCodePlugins"),
	"CommunityTools":              represented("CommunityTools"),

	"CodexMultiAgent":                  exempt("deprecated; Codex always writes features.multi_agent and the field survives only for state back-compatibility"),
	"ClearCodexOrchestratorAssignment": exempt("imperative clear action; declarative state expresses the same intent by omitting the assignment"),
}

// installStateParity classifies persisted state, which mixes durable intent with observed outcomes.
var installStateParity = map[string]parityDisposition{
	"InstalledAgents":    represented("Agents"),
	"Components":         represented("Components"),
	"Skills":             represented("Skills"),
	"Preset":             represented("Preset"),
	"SDDMode":            represented("SDDMode"),
	"StrictTDD":          represented("StrictTDD"),
	"Persona":            represented("Persona"),
	"BackgroundIntent":   represented("BackgroundIntent"),
	"PiBackgroundIntent": gap("PiBackgroundIntent"),

	"CommunityTools":              represented("CommunityTools"),
	"ModelAssignments":            represented("ModelAssignments"),
	"ClaudeModelAssignments":      represented("ClaudeModelAssignments"),
	"ClaudePhaseAssignments":      represented("ClaudePhaseAssignments"),
	"KiroModelAssignments":        represented("KiroModelAssignments"),
	"CodexModelAssignments":       represented("CodexModelAssignments"),
	"CodexOrchestratorAssignment": represented("CodexOrchestrator"),
	"CodexCarrilModelAssignments": represented("CodexCarrilModelAssignments"),
	"CodexPhaseModelAssignments":  represented("CodexPhaseModelAssignments"),
	"RDDMode":                     represented("RDDMode"),

	"InstalledBinaryVersion":   exempt(observedState),
	"ManagedAssetDigest":       exempt(observedState),
	"LastUpdateCheck":          exempt(observedState),
	"PendingSync":              exempt(observedState),
	"RDDModeRecordedAt":        exempt(observedState),
	"SelectionConfigured":      exempt(explicitByFile),
	"CommunityToolsConfigured": exempt(explicitByFile),
	"PersonaPresent":           exempt(decodingDetail),
}

// installFlagsParity classifies the non-interactive install surface.
var installFlagsParity = map[string]parityDisposition{
	"Agents":     represented("Agents"),
	"Components": represented("Components"),
	"Skills":     represented("Skills"),
	"Persona":    represented("Persona"),
	"Preset":     represented("Preset"),
	"SDDMode":    represented("SDDMode"),

	"Scope":                       represented("Scope"),
	"Channel":                     represented("Channel"),
	"OpenCodeBackgroundSubagents": represented("BackgroundIntent"),
	"PiBackgroundSubagents":       gap("PiBackgroundIntent"),

	"Config":                         exempt("selects the declarative document itself"),
	"DryRun":                         exempt(operationalFlag),
	"OpenCodeBackgroundSubagentsSet": exempt("flag-presence companion to OpenCodeBackgroundSubagents"),
	"PiBackgroundSubagentsSet":       exempt("flag-presence companion to PiBackgroundSubagents"),
}

func TestImperativeSurfacesAreClassifiedForParity(t *testing.T) {
	declarative := declarativeFields(t)

	for _, domain := range parityDomains() {
		fields := structFields(t, domain.name, domain.value)

		for _, field := range fields {
			disposition, classified := domain.parity[field]
			if !classified {
				t.Errorf("%s.%s has no parity classification; represent it in config.Selection, or record it as a gap or an exemption in %s", domain.name, field, domain.table)
				continue
			}

			if disposition.exemptBecause != "" {
				continue
			}

			_, isDeclared := declarative[disposition.declarativeField]

			if disposition.gap && isDeclared {
				t.Errorf("%s.%s is recorded as a parity gap but config.Selection.%s now exists; move the entry to represented in %s", domain.name, field, disposition.declarativeField, domain.table)
			}

			if !disposition.gap && !isDeclared {
				t.Errorf("%s.%s claims representation by config.Selection.%s, which does not exist; the declarative contract lost a field or it was renamed", domain.name, field, disposition.declarativeField)
			}
		}

		for _, stale := range staleEntries(domain.parity, fields) {
			t.Errorf("%s lists %q, which no longer exists on %s; remove the entry", domain.table, stale, domain.name)
		}
	}
}

// TestParityGapsAreReported keeps the remaining work visible in test output so a
// shrinking backlog is observable without reading the tables.
func TestParityGapsAreReported(t *testing.T) {
	remaining := map[string]struct{}{}

	for _, domain := range parityDomains() {
		for _, disposition := range domain.parity {
			if disposition.gap {
				remaining[disposition.declarativeField] = struct{}{}
			}
		}
	}

	surfaces := make([]string, 0, len(remaining))
	for surface := range remaining {
		surfaces = append(surfaces, surface)
	}
	sort.Strings(surfaces)

	t.Logf("declarative parity gaps remaining: %d", len(surfaces))
	for _, surface := range surfaces {
		t.Logf("  %s", surface)
	}
}

type parityDomain struct {
	name   string
	table  string
	value  any
	parity map[string]parityDisposition
}

func parityDomains() []parityDomain {
	return []parityDomain{
		{name: "model.Selection", table: "selectionParity", value: model.Selection{}, parity: selectionParity},
		{name: "state.InstallState", table: "installStateParity", value: state.InstallState{}, parity: installStateParity},
		{name: "cli.InstallFlags", table: "installFlagsParity", value: InstallFlags{}, parity: installFlagsParity},
	}
}

func declarativeFields(t *testing.T) map[string]struct{} {
	fields := map[string]struct{}{}
	for _, field := range structFields(t, "config.Selection", configdomain.Selection{}) {
		fields[field] = struct{}{}
	}
	return fields
}

func structFields(t *testing.T, name string, value any) []string {
	t.Helper()

	structType := reflect.TypeOf(value)
	if structType.Kind() != reflect.Struct {
		t.Fatalf("%s is a %s, not a struct; the parity guard can only classify struct fields", name, structType.Kind())
	}

	fields := make([]string, 0, structType.NumField())
	for index := 0; index < structType.NumField(); index++ {
		fields = append(fields, structType.Field(index).Name)
	}
	return fields
}

func staleEntries(parity map[string]parityDisposition, fields []string) []string {
	present := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		present[field] = struct{}{}
	}

	stale := make([]string, 0)
	for entry := range parity {
		if _, ok := present[entry]; !ok {
			stale = append(stale, entry)
		}
	}
	sort.Strings(stale)

	return stale
}

// A gap entry names the contract field that should eventually carry the intent,
// which is a prediction. When an implementation lands under a different name the
// gap entry keeps matching nothing and stays green forever, so the closure goes
// unreported. Checking the contract from the other side catches that: a field
// nobody claims is either an unrecorded closure or a field with no imperative
// counterpart at all.
func TestEveryContractFieldIsClaimed(t *testing.T) {
	claimed := map[string]struct{}{}

	for _, domain := range parityDomains() {
		for _, disposition := range domain.parity {
			if disposition.exemptBecause == "" && !disposition.gap {
				claimed[disposition.declarativeField] = struct{}{}
			}
		}
	}

	for _, field := range structFields(t, "config.Selection", configdomain.Selection{}) {
		if _, ok := claimed[field]; !ok {
			t.Errorf("config.Selection.%s is claimed by no parity entry; record which imperative surface it represents, or add it as an exemption", field)
		}
	}
}
