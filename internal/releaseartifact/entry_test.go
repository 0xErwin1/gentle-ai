package releaseartifact

import (
	"strings"
	"testing"
)

func validDigest() string {
	return "sha256:" + strings.Repeat("a", 64)
}

func TestValidateEntryPathRejectsInvalidPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "absolute path", path: "/etc/passwd"},
		{name: "parent traversal segment", path: "../etc/passwd"},
		{name: "embedded parent traversal segment", path: "contracts/../secrets"},
		{name: "current directory segment", path: "contracts/./v1"},
		{name: "backslash separator", path: `contracts\v1\schema.json`},
		{name: "NUL byte", path: "contracts/v1\x00schema.json"},
		{name: "control byte", path: "contracts/v1\x01schema.json"},
		{name: "DEL byte", path: "contracts/v1\x7fschema.json"},
		{name: "empty segment", path: "contracts//v1"},
		{name: "empty path", path: ""},
		{name: "path exceeding 1024 bytes", path: "contracts/" + strings.Repeat("a", 1024)},
		{name: "segment exceeding 255 bytes", path: strings.Repeat("a", 256) + "/schema.json"},
		{name: "trailing slash is not clean", path: "contracts/v1/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateEntryPath(tt.path); err == nil {
				t.Fatalf("ValidateEntryPath(%q) = nil, want error", tt.path)
			}
		})
	}
}

func TestValidateEntryPathAcceptsWellFormedRelativePaths(t *testing.T) {
	tests := []string{
		"artifact-manifest.json",
		"contracts/release-artifact/v1/schemas/artifact-manifest.schema.json",
		"a",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			if err := ValidateEntryPath(path); err != nil {
				t.Fatalf("ValidateEntryPath(%q) = %v, want nil", path, err)
			}
		})
	}
}

func TestValidateEntriesRejectsDisallowedTypes(t *testing.T) {
	tests := []struct {
		name      string
		entryType string
	}{
		{name: "symlink", entryType: "symlink"},
		{name: "hardlink", entryType: "hardlink"},
		{name: "character device", entryType: "chardevice"},
		{name: "block device", entryType: "blockdevice"},
		{name: "FIFO", entryType: "fifo"},
		{name: "socket", entryType: "socket"},
		{name: "unknown type", entryType: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := []Entry{{Path: "a.txt", Type: tt.entryType, Mode: EntryMode, Size: 1, Digest: validDigest()}}
			if err := ValidateEntries(entries); err == nil {
				t.Fatalf("ValidateEntries() with type %q = nil, want error", tt.entryType)
			}
		})
	}
}

func TestValidateEntriesRejectsDisallowedModes(t *testing.T) {
	tests := []struct {
		name string
		mode string
	}{
		{name: "executable owner bit", mode: "0744"},
		{name: "executable group bit", mode: "0654"},
		{name: "executable other bit", mode: "0645"},
		{name: "setuid", mode: "4644"},
		{name: "setgid", mode: "2644"},
		{name: "sticky", mode: "1644"},
		{name: "world writable", mode: "0666"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := []Entry{{Path: "a.txt", Type: EntryTypeFile, Mode: tt.mode, Size: 1, Digest: validDigest()}}
			if err := ValidateEntries(entries); err == nil {
				t.Fatalf("ValidateEntries() with mode %q = nil, want error", tt.mode)
			}
		})
	}
}

func TestValidateEntriesRejectsDuplicatePaths(t *testing.T) {
	entries := []Entry{
		{Path: "a.txt", Type: EntryTypeFile, Mode: EntryMode, Size: 1, Digest: validDigest()},
		{Path: "a.txt", Type: EntryTypeFile, Mode: EntryMode, Size: 2, Digest: validDigest()},
	}
	if err := ValidateEntries(entries); err == nil {
		t.Fatal("ValidateEntries() with duplicate paths = nil, want error")
	}
}

func TestValidateEntriesAcceptsWellFormedEntries(t *testing.T) {
	entries := []Entry{
		{Path: "a.txt", Type: EntryTypeFile, Mode: EntryMode, Size: 1, Digest: validDigest()},
		{Path: "b.txt", Type: EntryTypeFile, Mode: EntryMode, Size: 2, Digest: validDigest()},
	}
	if err := ValidateEntries(entries); err != nil {
		t.Fatalf("ValidateEntries() = %v, want nil", err)
	}
}

func TestSortEntriesOrdersByAscendingRawUTF8Bytes(t *testing.T) {
	entries := []Entry{
		{Path: "z/file.txt"},
		{Path: "a/file.txt"},
		{Path: "M/file.txt"},
		{Path: "m/file.txt"},
	}
	SortEntries(entries)

	want := []string{"M/file.txt", "a/file.txt", "m/file.txt", "z/file.txt"}
	if len(entries) != len(want) {
		t.Fatalf("SortEntries() len = %d, want %d", len(entries), len(want))
	}
	for i, path := range want {
		if entries[i].Path != path {
			t.Fatalf("SortEntries()[%d].Path = %q, want %q", i, entries[i].Path, path)
		}
	}
}
