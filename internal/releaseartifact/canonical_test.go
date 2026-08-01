package releaseartifact

import (
	"bytes"
	"testing"
)

// canonicalFixture deliberately declares its fields out of alphabetical
// order relative to their JSON tags so the byte assertion below proves key
// order follows Go struct declaration order, not JSON tag name or map-key
// sorting.
type canonicalFixture struct {
	Zeta  string   `json:"zeta"`
	Alpha int      `json:"alpha"`
	Empty []string `json:"empty"`
	HTML  string   `json:"html"`
}

func TestEncodeCanonicalMatchesD3Rules(t *testing.T) {
	fixture := canonicalFixture{
		Zeta:  "z-value",
		Alpha: 1,
		Empty: []string{},
		HTML:  `<tag>&"'</tag>`,
	}

	got, err := EncodeCanonical(fixture)
	if err != nil {
		t.Fatalf("EncodeCanonical() error = %v", err)
	}

	want := "{\n" +
		"  \"zeta\": \"z-value\",\n" +
		"  \"alpha\": 1,\n" +
		"  \"empty\": [],\n" +
		"  \"html\": \"<tag>&\\\"'</tag>\"\n" +
		"}\n"
	if string(got) != want {
		t.Fatalf("EncodeCanonical() =\n%q\nwant\n%q", string(got), want)
	}
}

func TestEncodeCanonicalHasNoBOMOrCarriageReturn(t *testing.T) {
	fixture := canonicalFixture{Zeta: "v", Alpha: 1, Empty: []string{}, HTML: "plain"}

	got, err := EncodeCanonical(fixture)
	if err != nil {
		t.Fatalf("EncodeCanonical() error = %v", err)
	}

	if bytes.HasPrefix(got, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("EncodeCanonical() emitted a UTF-8 BOM")
	}
	if bytes.Contains(got, []byte("\r")) {
		t.Fatal("EncodeCanonical() emitted a carriage return")
	}
}

func TestEncodeCanonicalEndsWithExactlyOneTrailingLF(t *testing.T) {
	fixture := canonicalFixture{Zeta: "v", Alpha: 1, Empty: []string{}, HTML: "plain"}

	got, err := EncodeCanonical(fixture)
	if err != nil {
		t.Fatalf("EncodeCanonical() error = %v", err)
	}

	if len(got) == 0 || got[len(got)-1] != '\n' {
		t.Fatalf("EncodeCanonical() must end with a trailing LF, got tail %q", tail(got))
	}
	if len(got) >= 2 && got[len(got)-2] == '\n' {
		t.Fatalf("EncodeCanonical() must end with exactly one trailing LF, got tail %q", tail(got))
	}
}

func tail(b []byte) []byte {
	if len(b) <= 4 {
		return b
	}
	return b[len(b)-4:]
}
