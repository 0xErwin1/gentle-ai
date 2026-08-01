package releaseartifact

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const manifestFixtureRoot = "../../contracts/release-artifact/v1"

func readManifestFixture(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(manifestFixtureRoot, "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func compileManifestSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	schemaPayload, err := os.ReadFile(filepath.Join(manifestFixtureRoot, "schemas", "artifact-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(schemaPayload, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(ManifestSchemaID, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(ManifestSchemaID)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

// TestReleaseArtifactManifestSchemaAndFixtureAreStrict mirrors
// TestReviewCapabilitiesSchemaAndFixtureAreStrict (internal/cli): it
// asserts the schema header identity and additionalProperties:false, then
// decodes the valid fixture with DisallowUnknownFields, validates it
// against the schema, and confirms it passes Manifest.Validate().
func TestReleaseArtifactManifestSchemaAndFixtureAreStrict(t *testing.T) {
	schemaPayload, err := os.ReadFile(filepath.Join(manifestFixtureRoot, "schemas", "artifact-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaPayload, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["$id"] != ManifestSchemaID || schema["additionalProperties"] != false {
		t.Fatalf("artifact-manifest schema header = %#v", schema)
	}

	fixture := readManifestFixture(t, "artifact-manifest.fixture.json")
	compiled := compileManifestSchema(t)
	var instance any
	if err := json.Unmarshal(fixture, &instance); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(instance); err != nil {
		t.Fatalf("valid fixture failed schema validation: %v", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(fixture))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("decode valid fixture: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Manifest.Validate() on valid fixture = %v, want nil", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(fixture, &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	malformed, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	decoder = json.NewDecoder(bytes.NewReader(malformed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&Manifest{}); err == nil {
		t.Fatal("strict manifest decoder accepted an unknown top-level field")
	}
}

func TestReleaseArtifactManifestRejectsUnsupportedMajorBeforeLayoutInference(t *testing.T) {
	fixture := readManifestFixture(t, "artifact-manifest-unsupported-major.fixture.json")

	decoder := json.NewDecoder(bytes.NewReader(fixture))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("decode unsupported-major fixture: %v", err)
	}

	err := manifest.Validate()
	if err == nil {
		t.Fatal("Manifest.Validate() on unsupported-major fixture = nil, want error naming the major")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Fatalf("Manifest.Validate() error = %q, want it to name the unsupported major 2", err.Error())
	}
}

func validManifest(t *testing.T) Manifest {
	t.Helper()
	fixture := readManifestFixture(t, "artifact-manifest.fixture.json")
	var manifest Manifest
	if err := json.Unmarshal(fixture, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestReleaseArtifactManifestRejectsMissingMandatoryFieldGroup(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(m *Manifest)
	}{
		{name: "missing release identity", mutate: func(m *Manifest) { m.Release = ManifestRelease{} }},
		{name: "missing layout identity", mutate: func(m *Manifest) { m.Layout = ManifestLayout{} }},
		{name: "missing entries", mutate: func(m *Manifest) { m.Entries = nil }},
		{name: "missing semantic snapshot references", mutate: func(m *Manifest) { m.References.SemanticSnapshots = nil }},
		{name: "unknown_mandatory not reject", mutate: func(m *Manifest) { m.Compatibility.UnknownMandatory = "ignore" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validManifest(t)
			tt.mutate(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatalf("Manifest.Validate() with %s = nil, want error", tt.name)
			}
		})
	}
}

func TestReleaseArtifactManifestRejectsUnresolvedReference(t *testing.T) {
	manifest := validManifest(t)
	manifest.References.SemanticSnapshots[0].Path = "capabilities/does-not-exist.json"
	if err := manifest.Validate(); err == nil {
		t.Fatal("Manifest.Validate() with an unresolved semantic snapshot reference = nil, want error")
	}
}

func TestReleaseArtifactManifestRejectsTamperedEntryDigest(t *testing.T) {
	manifest := validManifest(t)
	manifest.Entries[0].Digest = "sha256:" + strings.Repeat("0", 64)
	if err := manifest.Validate(); err == nil {
		t.Fatal("Manifest.Validate() with a tampered entry digest = nil, want error (tree recompute must fail)")
	}
}
