package render

import (
	"path/filepath"
	"testing"
)

func TestRendererRejectsBaselinePathEscape(t *testing.T) {
	_, err := New(fakeProvider{}).Render(Request{
		Destination: t.TempDir(),
		StageRoot:   t.TempDir(),
		Baseline:    map[string][]byte{"../escape": []byte("no")},
	})
	if err == nil {
		t.Fatal("path escape was accepted")
	}
}

func TestRendererRejectsOverlappingStageAndDestination(t *testing.T) {
	destination := t.TempDir()
	_, err := New(fakeProvider{}).Render(Request{
		Destination: destination,
		StageRoot:   filepath.Dir(destination),
	})
	if err == nil {
		t.Fatal("overlapping stage root was accepted")
	}
}
