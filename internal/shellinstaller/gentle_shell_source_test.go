package shellinstaller

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

// Deliberately not a tarball: equality of synthetic bytes is not publisher proof.
var syntheticSourceBytes = []byte("synthetic source bytes; not a tar archive")

func sourceExpectations(data []byte) (string, string) {
	h512 := sha512.Sum512(data)
	h256 := sha256.Sum256(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(h512[:]), hex.EncodeToString(h256[:])
}

func sourceSelector(t *testing.T, data []byte, length int64) GentleShellSourceSelection {
	t.Helper()
	sri, digest := sourceExpectations(data)
	s, err := NewGentleShellStableSource("gentle-pi", "3.7.0", sri, digest, length)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type sourceReadStep struct {
	data []byte
	err  error
}

type sourceScriptReader struct {
	steps    []sourceReadStep
	requests []int
}

func (r *sourceScriptReader) Read(p []byte) (int, error) {
	r.requests = append(r.requests, len(p))
	if len(r.steps) == 0 {
		return 0, io.EOF
	}
	step := r.steps[0]
	r.steps = r.steps[1:]
	return copy(p, step.data), step.err
}

type sourceBadCountReader struct{}

func (sourceBadCountReader) Read(p []byte) (int, error) { return len(p) + 1, nil }

func TestGentleShellStableSelectionIsStrictData(t *testing.T) {
	sri, digest := sourceExpectations(syntheticSourceBytes)
	cases := []struct {
		name, pkg, version, sri, digest string
		length                          int64
		valid                           bool
	}{
		{"exact Stable", "gentle-pi", "3.7.0", sri, digest, 1, true},
		{"maximum declared bytes", "gentle-pi", "3.7.0", sri, digest, gentleShellMaxArchiveBytes, true},
		{"wrong package", "gentle-shell", "3.7.0", sri, digest, 1, false},
		{"version range", "gentle-pi", "^3.7.0", sri, digest, 1, false},
		{"version suffix", "gentle-pi", "3.7.0-beta", sri, digest, 1, false},
		{"leading zero", "gentle-pi", "03.7.0", sri, digest, 1, false},
		{"missing version", "gentle-pi", "3.7", sri, digest, 1, false},
		{"SRI prefix", "gentle-pi", "3.7.0", "SHA512-" + strings.TrimPrefix(sri, "sha512-"), digest, 1, false},
		{"extra SRI padding", "gentle-pi", "3.7.0", sri + "=", digest, 1, false},
		{"noncanonical base64 bits", "gentle-pi", "3.7.0", sri[:len(sri)-3] + "B==", digest, 1, false},
		{"uppercase digest", "gentle-pi", "3.7.0", sri, "A" + digest[1:], 1, false},
		{"short digest", "gentle-pi", "3.7.0", sri, digest[:63], 1, false},
		{"zero bytes", "gentle-pi", "3.7.0", sri, digest, 0, false},
		{"negative bytes", "gentle-pi", "3.7.0", sri, digest, -1, false},
		{"over maximum", "gentle-pi", "3.7.0", sri, digest, gentleShellMaxArchiveBytes + 1, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewGentleShellStableSource(tt.pkg, tt.version, tt.sri, tt.digest, tt.length)
			if (err == nil) != tt.valid {
				t.Fatalf("selector accepted=%v, want=%v, error=%v", err == nil, tt.valid, err)
			}
			if !tt.valid {
				return
			}
			d, err := s.Describe()
			if err != nil || d.Kind != "not-authorized" || d.Channel != ChannelStable ||
				d.PackageName != "gentle-pi" || d.Version != "3.7.0" || d.Repository != "" || d.Commit != "" ||
				d.Missing[0] != "human-approved-exact-byte-sri-and-registry-tls-and-dependency-graph-proof" ||
				d.Missing[1] == "" || d.Missing[2] == "" {
				t.Fatalf("valid selector described as authorization: %+v err=%v", d, err)
			}
		})
	}
}

func TestGentleShellStableClaimComparisonIsDataOnly(t *testing.T) {
	selector := sourceSelector(t, syntheticSourceBytes, int64(len(syntheticSourceBytes)))
	claim := GentleShellStableSourceClaim{
		PackageName: selector.packageName, Version: selector.version,
		IntegritySRI: selector.integritySRI, ByteLength: selector.byteLength,
		RegistryURL: gentleShellStableRegistryURL,
	}
	wrongSRI, _ := sourceExpectations([]byte("different synthetic non-tar bytes"))
	mainSelector, err := NewGentleShellMainSource(gentleShellMainRepository, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		selector GentleShellSourceSelection
		claim    GentleShellStableSourceClaim
		matches  bool
	}{
		{"matching claim remains DATA", selector, claim, true},
		{"missing claim", selector, GentleShellStableSourceClaim{}, false},
		{"wrong package", selector, func() GentleShellStableSourceClaim { c := claim; c.PackageName = "other"; return c }(), false},
		{"wrong version", selector, func() GentleShellStableSourceClaim { c := claim; c.Version = "3.7.1"; return c }(), false},
		{"wrong SRI", selector, func() GentleShellStableSourceClaim { c := claim; c.IntegritySRI = wrongSRI; return c }(), false},
		{"wrong length", selector, func() GentleShellStableSourceClaim { c := claim; c.ByteLength++; return c }(), false},
		{"http URL", selector, func() GentleShellStableSourceClaim { c := claim; c.RegistryURL = "http://registry.npmjs.org/"; return c }(), false},
		{"noncanonical host", selector, func() GentleShellStableSourceClaim { c := claim; c.RegistryURL = "https://REGISTRY.npmjs.org/"; return c }(), false},
		{"noncanonical host suffix", selector, func() GentleShellStableSourceClaim { c := claim; c.RegistryURL = "https://registry.npmjs.org.evil/"; return c }(), false},
		{"query", selector, func() GentleShellStableSourceClaim { c := claim; c.RegistryURL += "?x=1"; return c }(), false},
		{"fragment", selector, func() GentleShellStableSourceClaim { c := claim; c.RegistryURL += "#x"; return c }(), false},
		{"userinfo", selector, func() GentleShellStableSourceClaim { c := claim; c.RegistryURL = "https://user@registry.npmjs.org/"; return c }(), false},
		{"Main rejected", mainSelector, claim, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CompareGentleShellStableClaim(tt.selector, tt.claim)
			if got.Kind != "not-authorized" || got.MatchesSelector != tt.matches || (err == nil) != tt.matches {
				t.Fatalf("claim comparison must remain non-authorizing data: %+v err=%v", got, err)
			}
		})
	}
}

func TestGentleShellMainFullCommitStillStopsComparison(t *testing.T) {
	commit := strings.Repeat("a", 40)
	s, err := NewGentleShellMainSource("Gentleman-Programming/gentle-shell", commit)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Describe()
	if err != nil || d.Kind != "not-authorized" || d.Channel != ChannelMain ||
		d.Repository != "Gentleman-Programming/gentle-shell" || d.Commit != commit ||
		d.Missing[0] != "approved-full-commit-and-graph-proof" {
		t.Fatalf("Main commit incorrectly authorized: %+v err=%v", d, err)
	}
	reader := &sourceScriptReader{steps: []sourceReadStep{{syntheticSourceBytes, io.EOF}}}
	comparison, err := CompareGentleShellStableArchive(s, reader)
	if err == nil || comparison.Kind != "not-authorized" || comparison.Matched || len(reader.requests) != 0 {
		t.Fatalf("Main may not compare or read as Stable: %+v err=%v reads=%v", comparison, err, reader.requests)
	}
	cases := []struct {
		repository string
		commit     string
	}{
		{"gentleman-programming/gentle-shell", commit},
		{"Gentleman-Programming/gentle-pi", commit},
		{"Gentleman-Programming/gentle-shell", "main"},
		{"Gentleman-Programming/gentle-shell", commit[:39]},
		{"Gentleman-Programming/gentle-shell", "A" + commit[1:]},
		{"Gentleman-Programming/gentle-shell", "g" + commit[1:]},
	}
	for _, tt := range cases {
		if _, err := NewGentleShellMainSource(tt.repository, tt.commit); err == nil {
			t.Fatalf("noncanonical Main selector accepted: %+v", tt)
		}
	}
}

func TestGentleShellStableComparisonIsBoundedData(t *testing.T) {
	data := syntheticSourceBytes
	s := sourceSelector(t, data, int64(len(data)))
	sri, digest := sourceExpectations(data)
	for _, tt := range []struct {
		name         string
		reader       io.Reader
		wantRequests []int
	}{
		{"ordinary EOF probe", bytes.NewReader(data), nil},
		{"short reads then EOF probe", &sourceScriptReader{steps: []sourceReadStep{
			{data[:3], nil}, {data[3:], nil}, {nil, io.EOF}}}, []int{len(data), len(data) - 3, 1}},
		{"final bytes with EOF still probe", &sourceScriptReader{steps: []sourceReadStep{
			{data, io.EOF}, {nil, io.EOF}}}, []int{len(data), 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CompareGentleShellStableArchive(s, tt.reader)
			if err != nil || got.Kind != "not-authorized" || !got.Matched ||
				got.ByteLength != int64(len(data)) || got.MeasuredSRI != sri || got.MeasuredSHA256 != digest {
				t.Fatalf("exact synthetic bytes still only matched DATA: %+v err=%v", got, err)
			}
			if tt.wantRequests != nil {
				r := tt.reader.(*sourceScriptReader)
				if len(r.requests) != len(tt.wantRequests) {
					t.Fatalf("read count=%v want=%v", r.requests, tt.wantRequests)
				}
				for i, size := range tt.wantRequests {
					if r.requests[i] != size {
						t.Fatalf("read requests=%v want=%v", r.requests, tt.wantRequests)
					}
				}
			}
		})
	}
}

func TestGentleShellStableComparisonStopsOnUncertainBytes(t *testing.T) {
	data := syntheticSourceBytes
	s := sourceSelector(t, data, int64(len(data)))
	wrongSRI, digest := sourceExpectations([]byte("different synthetic bytes"))
	actualSRI, actualSHA := sourceExpectations(data)
	wrongSHA, err := NewGentleShellStableSource("gentle-pi", "3.7.0", actualSRI, digest, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	wrongLength := sourceSelector(t, data, int64(len(data)+1))
	shortLength := sourceSelector(t, data, int64(len(data)-1))
	wrongSRISelector, err := NewGentleShellStableSource("gentle-pi", "3.7.0", wrongSRI, actualSHA, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var typedNil *sourceScriptReader
	cases := []struct {
		name     string
		selector GentleShellSourceSelection
		reader   io.Reader
	}{
		{"nil interface", s, nil},
		{"typed nil reader", s, typedNil},
		{"short body", s, bytes.NewReader(data[:len(data)-1])},
		{"early bytes with EOF", s, &sourceScriptReader{steps: []sourceReadStep{{data[:3], io.EOF}}}},
		{"extra byte", s, bytes.NewReader(append(bytes.Clone(data), 'x'))},
		{"declared too long", wrongLength, bytes.NewReader(data)},
		{"declared too short", shortLength, bytes.NewReader(data)},
		{"wrong SHA256", wrongSHA, bytes.NewReader(data)},
		{"wrong SRI with correct SHA256", wrongSRISelector, bytes.NewReader(data)},
		{"zero progress in body", s, &sourceScriptReader{steps: []sourceReadStep{{nil, nil}}}},
		{"zero progress at probe", s, &sourceScriptReader{steps: []sourceReadStep{{data, nil}, {nil, nil}}}},
		{"false final EOF hides extra byte", s, &sourceScriptReader{steps: []sourceReadStep{{data, io.EOF}, {[]byte{'x'}, io.EOF}}}},
		{"final read with error", s, &sourceScriptReader{steps: []sourceReadStep{{data, errors.New("read failed")}}}},
		{"bounded Read contract violation", s, sourceBadCountReader{}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CompareGentleShellStableArchive(tt.selector, tt.reader)
			if err == nil || got.Kind != "not-authorized" || got.Matched {
				t.Fatalf("uncertain or mismatched bytes must STOP: %+v err=%v", got, err)
			}
		})
	}
}
