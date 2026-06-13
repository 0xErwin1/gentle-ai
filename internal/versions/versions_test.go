package versions

import "testing"

func TestEngramPinsStaySeparate(t *testing.T) {
	if EngramCore != "1.16.3" {
		t.Fatalf("EngramCore = %q, want %q", EngramCore, "1.16.3")
	}
	if GentleEngram != "0.1.8" {
		t.Fatalf("GentleEngram = %q, want %q", GentleEngram, "0.1.8")
	}
	if EngramCore == GentleEngram {
		t.Fatalf("EngramCore and GentleEngram must remain separate pins; both are %q", EngramCore)
	}
}
