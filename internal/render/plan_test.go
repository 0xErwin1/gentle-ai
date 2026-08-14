package render

import (
	"strings"
	"testing"
)

func TestPlanOrdersOwnershipOperations(t *testing.T) {
	previous := Manifest{Resources: []Resource{
		{Path: "managed", Selector: "file", Digest: "old"},
		{Path: "removed", Selector: "file", Digest: "remove"},
	}}
	current := Manifest{Resources: []Resource{
		{Path: "managed", Selector: "file", Digest: "new"},
		{Path: "created", Selector: "file", Digest: "create"},
	}}
	live := map[ResourceKey]string{
		{Path: "managed", Selector: "file"}: "old",
		{Path: "removed", Selector: "file"}: "remove",
		{Path: "user", Selector: "file"}:    "user",
	}

	plan := Plan(previous, current, live)
	got := make([]OperationKind, len(plan.Operations))
	for index, operation := range plan.Operations {
		got[index] = operation.Kind
	}
	if want := []OperationKind{Create, Update, Remove, Skip}; !equalOperationKinds(got, want) {
		t.Fatalf("operation kinds = %v, want %v", got, want)
	}
}

func TestPlanReportsStaleAndUserOwnedConflicts(t *testing.T) {
	previous := Manifest{Resources: []Resource{{Path: "managed", Selector: "file", Digest: "old"}}}
	current := Manifest{Resources: []Resource{
		{Path: "managed", Selector: "file", Digest: "new"},
		{Path: "user", Selector: "file", Digest: "desired"},
	}}
	live := map[ResourceKey]string{
		{Path: "managed", Selector: "file"}: "changed",
		{Path: "user", Selector: "file"}:    "user",
	}

	plan := Plan(previous, current, live)
	if len(plan.Operations) != 2 || plan.Operations[0].Kind != Conflict || plan.Operations[1].Kind != Conflict {
		t.Fatalf("operations = %#v, want stale and ownership conflicts", plan.Operations)
	}
	if !strings.Contains(plan.Operations[0].Reason, "stale") || !strings.Contains(plan.Operations[1].Reason, "user-owned") {
		t.Fatalf("conflict reasons = %#v", plan.Operations)
	}
}

func TestManifestForSnapshotIsDeterministic(t *testing.T) {
	stage := t.TempDir()
	if err := writeArtifact(stage, ArtifactContent{Path: "b", Contents: []byte("second")}); err != nil {
		t.Fatal(err)
	}
	if err := writeArtifact(stage, ArtifactContent{Path: "a", Contents: []byte("first")}); err != nil {
		t.Fatal(err)
	}

	manifest, err := ManifestFor(Snapshot{Stage: stage, Artifacts: []Artifact{{Path: "b"}, {Path: "a"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := manifest.Resources[0]; got.Path != "a" || got.Selector != "file" || got.Digest == "" {
		t.Fatalf("first resource = %#v", got)
	}
}

func equalOperationKinds(left, right []OperationKind) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
