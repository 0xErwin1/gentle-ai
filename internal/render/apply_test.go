package render

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
)

func TestApplyRejectsUnsafeAdmission(t *testing.T) {
	for _, test := range []struct {
		name    string
		request ApplyRequest
	}{
		{"diagnostics", ApplyRequest{Diagnostics: []config.Diagnostic{{Code: "config.invalid"}}}},
		{"unresolved references", ApplyRequest{Diagnostics: []config.Diagnostic{{Code: "config.role.reference.unresolved"}}}},
		{"conflict", ApplyRequest{Plan: ReconcilePlan{Operations: []Operation{{Kind: Conflict, Code: "render.ownership.conflict"}}}}},
		{"stale", ApplyRequest{Plan: ReconcilePlan{Operations: []Operation{{Kind: Conflict, Code: "render.precondition.stale"}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			test.request.Persist = func() error { called = true; return nil }
			if err := Apply(test.request); err == nil || called {
				t.Fatalf("Apply() error/called = %v/%t, want refusal before persistence", err, called)
			}
		})
	}
}

func TestApplyRollsBackFilesWhenPersistenceFails(t *testing.T) {
	stage, destination := t.TempDir(), t.TempDir()
	if err := writeArtifact(stage, ArtifactContent{Path: "config.json", Contents: []byte("new")}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(destination, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Apply(ApplyRequest{
		Snapshot:    Snapshot{Stage: stage},
		Destination: destination,
		Plan:        ReconcilePlan{Operations: []Operation{{Kind: Update, Path: "config.json", Selector: "file"}}},
		Persist:     func() error { return errors.New("store unavailable") },
	})
	if err == nil {
		t.Fatal("Apply() error = nil, want persistence failure")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "old" {
		t.Fatalf("destination after rollback = %q, %v; want old", got, readErr)
	}
}

func TestApplyWritesVerifiedOperationBeforePersistence(t *testing.T) {
	stage, destination := t.TempDir(), t.TempDir()
	if err := writeArtifact(stage, ArtifactContent{Path: "config.json", Contents: []byte("new")}); err != nil {
		t.Fatal(err)
	}
	persisted := false
	err := Apply(ApplyRequest{
		Snapshot: Snapshot{Stage: stage}, Destination: destination,
		Plan: ReconcilePlan{Operations: []Operation{{Kind: Create, Path: "config.json", Selector: "file"}}},
		Persist: func() error {
			contents, err := os.ReadFile(filepath.Join(destination, "config.json"))
			persisted = err == nil && string(contents) == "new"
			return err
		},
	})
	if err != nil || !persisted {
		t.Fatalf("Apply() error/persisted = %v/%t, want verified file then persistence", err, persisted)
	}
}
