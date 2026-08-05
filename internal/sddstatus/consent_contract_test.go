package sddstatus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// sddConsentGrantInvocationShape is the PROVISIONAL grant invocation pin
// (#2540 phase 1, S4a). S3 does not exist yet; this exact flag set, in this
// exact order, is what the future `sdd-attempt grant` verb must implement:
//
//	gentle-ai sdd-attempt grant --cwd <repo> --change <name> --root <path>... --actor <actor> --reason <reason>
//
// If S3 lands with a different flag set, that is a conscious contract change:
// update this pin, the schema, and the fixture together, and say so in the
// PR. It never drifts silently.
var sddConsentGrantInvocationShape = regexp.MustCompile(
	`^gentle-ai sdd-attempt grant --cwd \S+ --change \S+( --root \S+)+ --actor \S+ --reason .+$`)

// sddConsentDeclineInvocationShape pins the decline re-entry: declining
// persists nothing, so the runnable follow-up is native SDD status for the
// same change.
var sddConsentDeclineInvocationShape = regexp.MustCompile(
	`^gentle-ai sdd-status \S+ --cwd \S+$`)

func TestSDDIntegrationConsentContractsArePinned(t *testing.T) {
	root := filepath.Join("..", "..", "contracts", "sdd-integration", "v1")
	want := map[string]string{
		"fixtures/consent.fixture.json": "e594f43bad5c35cb70027ef2d36750bb8f0e66d6cf9d6e9e367042a412868596",
		"schemas/consent.schema.json":   "a71be755920825643e0e7697f370c8a477815be41472dde10eb1100925ae8553",
	}
	for name, expected := range want {
		payload, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(payload)
		if actual := hex.EncodeToString(digest[:]); actual != expected {
			t.Fatalf("%s digest = %s, want %s", name, actual, expected)
		}
	}
}

func TestSDDIntegrationConsentSchemaIsStrictAndBound(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "contracts", "sdd-integration", "v1", "schemas", "consent.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(payload, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" ||
		schema["$id"] != SDDIntegrationConsentSchemaID ||
		schema["additionalProperties"] != false {
		t.Fatalf("consent schema header = %#v", schema)
	}
	properties := schema["properties"].(map[string]any)
	if properties["schema"].(map[string]any)["const"] != SDDIntegrationConsentSchema ||
		properties["contract"].(map[string]any)["const"] != SDDIntegrationContractV1 ||
		properties["operation"].(map[string]any)["const"] != "sdd-attempt.grant" ||
		properties["action"].(map[string]any)["const"] != "consent_required" ||
		properties["blocking"].(map[string]any)["const"] != true {
		t.Fatalf("consent schema identity = %#v", properties)
	}
	choices := properties["choices"].(map[string]any)
	if choices["minItems"] != float64(2) || choices["maxItems"] != float64(2) || choices["items"] != false {
		t.Fatalf("consent schema does not require exactly two choices: %#v", choices)
	}
	defs := schema["$defs"].(map[string]any)
	grantedPattern := defs["choice_granted"].(map[string]any)["properties"].(map[string]any)["invocation"].(map[string]any)["pattern"].(string)
	if _, err := regexp.Compile(grantedPattern); err != nil {
		t.Fatalf("granted invocation pattern does not compile: %v", err)
	}
	declinedPattern := defs["choice_declined"].(map[string]any)["properties"].(map[string]any)["invocation"].(map[string]any)["pattern"].(string)
	if _, err := regexp.Compile(declinedPattern); err != nil {
		t.Fatalf("declined invocation pattern does not compile: %v", err)
	}
}

func TestSDDIntegrationConsentFixtureValidates(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "contracts", "sdd-integration", "v1", "fixtures", "consent.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var consent SDDIntegrationConsentResult
	if err := decoder.Decode(&consent); err != nil {
		t.Fatalf("consent fixture no longer decodes into the live type: %v", err)
	}
	if err := consent.Validate(); err != nil {
		t.Fatalf("consent fixture no longer validates: %v", err)
	}

	// The same byte discipline the review consent envelope carries: the live
	// type re-encodes to the exact fixture bytes, so a JSON tag or field-order
	// change cannot ship unnoticed.
	remarshaled, err := json.MarshalIndent(consent, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	remarshaled = append(remarshaled, '\n')
	if !bytes.Equal(remarshaled, payload) {
		t.Fatalf("consent envelope no longer serializes to the shipped fixture bytes\n--- fixture ---\n%s\n--- remarshaled ---\n%s", payload, remarshaled)
	}

	// Provisional S3 pin: the fixture's invocations carry the exact flag sets
	// the future verbs must honor.
	if !sddConsentGrantInvocationShape.MatchString(consent.Choices[0].Invocation) {
		t.Fatalf("granted invocation does not match the provisional S3 grant shape: %q", consent.Choices[0].Invocation)
	}
	if !sddConsentDeclineInvocationShape.MatchString(consent.Choices[1].Invocation) {
		t.Fatalf("declined invocation does not match the status re-entry shape: %q", consent.Choices[1].Invocation)
	}
}

func TestSDDIntegrationConsentValidateRejectsIncompleteEnvelopes(t *testing.T) {
	valid := validSDDConsentResult()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid envelope: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(result *SDDIntegrationConsentResult)
	}{
		{name: "wrong schema", mutate: func(result *SDDIntegrationConsentResult) { result.Schema = "gentle-ai.review-integration.consent/v1" }},
		{name: "not blocking", mutate: func(result *SDDIntegrationConsentResult) { result.Blocking = false }},
		{name: "missing change name", mutate: func(result *SDDIntegrationConsentResult) { result.Change = "" }},
		{name: "no missing roots", mutate: func(result *SDDIntegrationConsentResult) { result.MissingRoots = nil }},
		{name: "blank missing root", mutate: func(result *SDDIntegrationConsentResult) { result.MissingRoots = []string{" "} }},
		{name: "missing headline", mutate: func(result *SDDIntegrationConsentResult) { result.Headline = "" }},
		{name: "nil evidence", mutate: func(result *SDDIntegrationConsentResult) { result.Evidence = nil }},
		{name: "root absent from evidence", mutate: func(result *SDDIntegrationConsentResult) { result.Evidence = []string{"unrelated"} }},
		{name: "one choice", mutate: func(result *SDDIntegrationConsentResult) { result.Choices = result.Choices[:1] }},
		{name: "swapped answers", mutate: func(result *SDDIntegrationConsentResult) {
			result.Choices[0].Answer, result.Choices[1].Answer = result.Choices[1].Answer, result.Choices[0].Answer
		}},
		{name: "grant invocation missing a root", mutate: func(result *SDDIntegrationConsentResult) {
			result.Choices[0].Invocation = "gentle-ai sdd-attempt grant --cwd /workspace/planning --change multi-repo-rollout --root /workspace/service-a --actor maintainer --reason 'rollout'"
		}},
		{name: "grant invocation missing actor", mutate: func(result *SDDIntegrationConsentResult) {
			result.Choices[0].Invocation = "gentle-ai sdd-attempt grant --cwd /workspace/planning --change multi-repo-rollout --root /workspace/service-a --root /workspace/service-b --reason 'rollout'"
		}},
		{name: "decline invocation is not status re-entry", mutate: func(result *SDDIntegrationConsentResult) {
			result.Choices[1].Invocation = "gentle-ai review status"
		}},
		{name: "empty choice effect", mutate: func(result *SDDIntegrationConsentResult) { result.Choices[1].Effect = "" }},
		{name: "off path outside status", mutate: func(result *SDDIntegrationConsentResult) { result.OffPath.Command = "rm -rf tasks.md" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validSDDConsentResult()
			tt.mutate(&result)
			if err := result.Validate(); err == nil {
				t.Fatal("Validate() accepted an incomplete envelope")
			}
		})
	}
}

func validSDDConsentResult() SDDIntegrationConsentResult {
	payload, err := os.ReadFile(filepath.Join("..", "..", "contracts", "sdd-integration", "v1", "fixtures", "consent.fixture.json"))
	if err != nil {
		panic(err)
	}
	var consent SDDIntegrationConsentResult
	if err := json.Unmarshal(payload, &consent); err != nil {
		panic(err)
	}
	return consent
}
