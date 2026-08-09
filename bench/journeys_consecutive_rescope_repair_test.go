package main

import "testing"

func TestRC1ConsecutiveRescopeProvenanceMatchesFixture(t *testing.T) {
	records, err := rc1ConsecutiveRescopeRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("provenance record count = %d, want 4", len(records))
	}
}
