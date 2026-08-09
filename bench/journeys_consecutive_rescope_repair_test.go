package main

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRC1ConsecutiveRescopeProvenanceMatchesFixture(t *testing.T) {
	records, err := rc1ConsecutiveRescopeRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("provenance record count = %d, want 4", len(records))
	}
}

func TestRC1ConsecutiveRescopeProvenanceRefusesSameLengthOperationMutation(t *testing.T) {
	fixture := mutatedRC1ConsecutiveRescopeProvenance(t, func(manifest *rc1ConsecutiveRescopeManifest) {
		manifest.OperationShape[3] = "objective/rescope B to D"
	})
	if _, err := rc1ConsecutiveRescopeRecordsFrom(fixture); err == nil || !strings.Contains(err.Error(), "refuses a different ordered operation sequence") {
		t.Fatalf("same-length operation mutation = %v, want observable ordered-sequence refusal", err)
	}
}

func TestRC1ConsecutiveRescopeProvenanceRefusesGeneratorCommandMutation(t *testing.T) {
	fixture := mutatedRC1ConsecutiveRescopeProvenance(t, func(manifest *rc1ConsecutiveRescopeManifest) {
		manifest.GeneratorCommands[1] = "go build -o gentle-ai ./cmd/gentle-ai"
	})
	if _, err := rc1ConsecutiveRescopeRecordsFrom(fixture); err == nil || !strings.Contains(err.Error(), "refuses different generator commands") {
		t.Fatalf("generator command mutation = %v, want observable generator-command refusal", err)
	}
}

func mutatedRC1ConsecutiveRescopeProvenance(t *testing.T, mutate func(*rc1ConsecutiveRescopeManifest)) fstest.MapFS {
	t.Helper()
	payload, err := consecutiveRescopeRC1.ReadFile("testdata/consecutive-rescope-rc1/provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rc1ConsecutiveRescopeManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	mutate(&manifest)
	payload, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{
		"testdata/consecutive-rescope-rc1/provenance.json": &fstest.MapFile{Data: payload},
	}
}
