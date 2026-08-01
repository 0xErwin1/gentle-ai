package releasepolicy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// snapshotVersionUnderTest matches the pattern validSnapshotVersion requires:
// it must contain "-SNAPSHOT" and use only the characters that pattern
// admits, and it is shared by every platform archive and the assets archive
// so the "one snapshot version" identity check has something real to bind.
const snapshotVersionUnderTest = "0.0.0-SNAPSHOT"

// baselineArtifacts returns one exact-plus-one release shape (design D6):
// four platform archives (each id: default) plus one platform-independent
// assets archive (id: assets), the four platform binaries, and the fixed
// Metadata/Checksum/Homebrew Formula singletons. Each test row starts from a
// fresh copy and mutates exactly the field under test.
func baselineArtifacts() []artifact {
	return []artifact{
		{Name: "metadata.json", Path: "dist/metadata.json", Type: "Metadata"},
		{
			Name: "gentle-ai", Path: "dist/gentle-ai_linux_amd64_v1/gentle-ai",
			GOOS: "linux", GOARCH: "amd64", Target: "linux_amd64_v1", Type: "Binary",
			Extra: map[string]any{"Binary": "gentle-ai", "ID": "gentle-ai"},
		},
		{
			Name: "gentle-ai", Path: "dist/gentle-ai_linux_arm64_v8.0/gentle-ai",
			GOOS: "linux", GOARCH: "arm64", Target: "linux_arm64_v8.0", Type: "Binary",
			Extra: map[string]any{"Binary": "gentle-ai", "ID": "gentle-ai"},
		},
		{
			Name: "gentle-ai", Path: "dist/gentle-ai_darwin_amd64_v1/gentle-ai",
			GOOS: "darwin", GOARCH: "amd64", Target: "darwin_amd64_v1", Type: "Binary",
			Extra: map[string]any{"Binary": "gentle-ai", "ID": "gentle-ai"},
		},
		{
			Name: "gentle-ai", Path: "dist/gentle-ai_darwin_arm64_v8.0/gentle-ai",
			GOOS: "darwin", GOARCH: "arm64", Target: "darwin_arm64_v8.0", Type: "Binary",
			Extra: map[string]any{"Binary": "gentle-ai", "ID": "gentle-ai"},
		},
		{
			Name: "gentle-ai_" + snapshotVersionUnderTest + "_linux_amd64.tar.gz", Path: "dist/gentle-ai_" + snapshotVersionUnderTest + "_linux_amd64.tar.gz",
			GOOS: "linux", GOARCH: "amd64", Target: "linux_amd64_v1", Type: "Archive",
			Extra: map[string]any{"Binaries": []any{"gentle-ai"}, "Format": "tar.gz", "ID": "default"},
		},
		{
			Name: "gentle-ai_" + snapshotVersionUnderTest + "_linux_arm64.tar.gz", Path: "dist/gentle-ai_" + snapshotVersionUnderTest + "_linux_arm64.tar.gz",
			GOOS: "linux", GOARCH: "arm64", Target: "linux_arm64_v8.0", Type: "Archive",
			Extra: map[string]any{"Binaries": []any{"gentle-ai"}, "Format": "tar.gz", "ID": "default"},
		},
		{
			Name: "gentle-ai_" + snapshotVersionUnderTest + "_darwin_amd64.tar.gz", Path: "dist/gentle-ai_" + snapshotVersionUnderTest + "_darwin_amd64.tar.gz",
			GOOS: "darwin", GOARCH: "amd64", Target: "darwin_amd64_v1", Type: "Archive",
			Extra: map[string]any{"Binaries": []any{"gentle-ai"}, "Format": "tar.gz", "ID": "default"},
		},
		{
			Name: "gentle-ai_" + snapshotVersionUnderTest + "_darwin_arm64.tar.gz", Path: "dist/gentle-ai_" + snapshotVersionUnderTest + "_darwin_arm64.tar.gz",
			GOOS: "darwin", GOARCH: "arm64", Target: "darwin_arm64_v8.0", Type: "Archive",
			Extra: map[string]any{"Binaries": []any{"gentle-ai"}, "Format": "tar.gz", "ID": "default"},
		},
		{
			Name: "gentle-ai_" + snapshotVersionUnderTest + "_assets.tar.gz", Path: "dist/gentle-ai_" + snapshotVersionUnderTest + "_assets.tar.gz",
			Type:  "Archive",
			Extra: map[string]any{"Binaries": []any{}, "Format": "tar.gz", "ID": "assetsArchiveIDPlaceholder"},
		},
		{Name: "checksums.txt", Path: "dist/checksums.txt", Type: "Checksum"},
		{
			Name: "gentle-ai.rb", Path: "dist/homebrew/Formula/gentle-ai.rb", Type: "Homebrew Formula",
			Extra: map[string]any{"BrewConfig": map[string]any{
				"name": "gentle-ai", "directory": "Formula",
				"repository": map[string]any{"owner": "Gentleman-Programming", "name": "homebrew-tap", "token": "{{ .Env.HOMEBREW_TAP_TOKEN }}"},
			}},
		},
	}
}

// baselineArtifactsWithAssetsID binds the assets archive's placeholder ID to
// the real assetsArchiveID constant, so every row exercises the identifier
// policy.go actually checks rather than a copy that could drift from it.
func baselineArtifactsWithAssetsID() []artifact {
	items := baselineArtifacts()
	for i := range items {
		if extraString(items[i].Extra, "ID") == "assetsArchiveIDPlaceholder" {
			items[i].Extra["ID"] = assetsArchiveID
		}
	}
	return items
}

// findArtifact returns a pointer to the first artifact matching predicate,
// failing the test if none matches, so mutate helpers cannot silently no-op.
func findArtifact(t *testing.T, items []artifact, predicate func(artifact) bool) *artifact {
	t.Helper()
	for i := range items {
		if predicate(items[i]) {
			return &items[i]
		}
	}
	t.Fatal("no artifact matched the mutation predicate")
	return nil
}

func isAssetsArchive(a artifact) bool {
	return a.Type == "Archive" && extraString(a.Extra, "ID") == assetsArchiveID
}

func isPlatformArchive(goos, goarch string) func(artifact) bool {
	return func(a artifact) bool {
		return a.Type == "Archive" && a.GOOS == goos && a.GOARCH == goarch
	}
}

// runValidateArtifacts builds a temporary snapshot directory containing one
// regular file per artifact path (mtime after markerTime), marshals items to
// canonical JSON, and calls validateArtifacts directly -- the same function
// Validate() delegates to for dist/artifacts.json.
func runValidateArtifacts(t *testing.T, items []artifact) error {
	t.Helper()
	root := t.TempDir()
	markerTime := time.Now().Add(-time.Minute)
	seen := make(map[string]bool)
	for _, item := range items {
		if item.Path == "" || seen[item.Path] {
			continue
		}
		seen[item.Path] = true
		full := filepath.Join(root, filepath.FromSlash(item.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("synthetic snapshot output\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	return validateArtifacts(root, payload, markerTime)
}

func TestValidateArtifactsAdmitsTheExactPlusOneReleaseShape(t *testing.T) {
	if err := runValidateArtifacts(t, baselineArtifactsWithAssetsID()); err != nil {
		t.Fatalf("valid four-platform-plus-assets shape was rejected: %v", err)
	}
}

func TestValidateArtifactsNewlyRedCases(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(t *testing.T, items []artifact) []artifact
		wantErrSub string
	}{
		{
			// Removing the assets archive drops the total Archive count from
			// 5 to 4, so the type-level expectedCounts check (which now
			// requires Archive: 5) rejects it before the ID-keyed split ever
			// runs -- an even earlier fail-closed layer than the dedicated
			// "assets archive count" check below.
			name: "assets archive absent",
			mutate: func(t *testing.T, items []artifact) []artifact {
				out := make([]artifact, 0, len(items))
				for _, item := range items {
					if isAssetsArchive(item) {
						continue
					}
					out = append(out, item)
				}
				return out
			},
			wantErrSub: "artifact types changed",
		},
		{
			// Duplicating the assets archive raises the total Archive count
			// to 6, so it is also caught by expectedCounts before the
			// ID-keyed split runs, for the same reason as the absent case.
			name: "assets archive duplicated",
			mutate: func(t *testing.T, items []artifact) []artifact {
				assets := *findArtifact(t, items, isAssetsArchive)
				duplicate := assets
				duplicate.Path = "dist/gentle-ai_" + snapshotVersionUnderTest + "_assets-2.tar.gz"
				duplicate.Name = "gentle-ai_" + snapshotVersionUnderTest + "_assets-2.tar.gz"
				return append(items, duplicate)
			},
			wantErrSub: "artifact types changed",
		},
		{
			// This is the one case that reaches the dedicated
			// len(assetsArchives) != 1 check: total Archive count stays at
			// 5 (nothing added or removed), but ID reassignment moves a
			// platform archive into the assets bucket alongside the genuine
			// one, so expectedCounts alone cannot catch it.
			name: "assets archive count changes without changing the total Archive count",
			mutate: func(t *testing.T, items []artifact) []artifact {
				platform := findArtifact(t, items, isPlatformArchive("linux", "amd64"))
				platform.Extra["ID"] = assetsArchiveID
				return items
			},
			wantErrSub: "assets archive count",
		},
		{
			name: "sixth archive with an unrelated id",
			mutate: func(t *testing.T, items []artifact) []artifact {
				extra := artifact{
					Name: "gentle-ai_" + snapshotVersionUnderTest + "_extra.tar.gz",
					Path: "dist/gentle-ai_" + snapshotVersionUnderTest + "_extra.tar.gz",
					Type: "Archive",
					Extra: map[string]any{
						"Binaries": []any{}, "Format": "tar.gz", "ID": "unrelated",
					},
				}
				return append(items, extra)
			},
			wantErrSub: "artifact types changed",
		},
		{
			name: "assets archive carries a platform axis",
			mutate: func(t *testing.T, items []artifact) []artifact {
				assets := findArtifact(t, items, isAssetsArchive)
				assets.GOOS = "linux"
				assets.GOARCH = "amd64"
				assets.Target = "linux_amd64_v1"
				return items
			},
			wantErrSub: "platform axis",
		},
		{
			name: "assets archive name does not bind snapshotVersion",
			mutate: func(t *testing.T, items []artifact) []artifact {
				assets := findArtifact(t, items, isAssetsArchive)
				assets.Name = "gentle-ai_9.9.9-SNAPSHOT_assets.tar.gz"
				assets.Path = "dist/gentle-ai_9.9.9-SNAPSHOT_assets.tar.gz"
				return items
			},
			wantErrSub: "identity changed",
		},
		{
			name: "assets archive declares Binaries",
			mutate: func(t *testing.T, items []artifact) []artifact {
				assets := findArtifact(t, items, isAssetsArchive)
				assets.Extra["Binaries"] = []any{"gentle-ai"}
				return items
			},
			wantErrSub: "identity changed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := tc.mutate(t, baselineArtifactsWithAssetsID())
			err := runValidateArtifacts(t, items)
			if err == nil {
				t.Fatal("policy accepted a mutated release shape it must reject")
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

func TestValidateArtifactsRegressionLocks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, items []artifact) []artifact
	}{
		{
			name: "a platform archive is missing",
			mutate: func(t *testing.T, items []artifact) []artifact {
				out := make([]artifact, 0, len(items))
				removed := false
				for _, item := range items {
					if !removed && item.Type == "Archive" && !isAssetsArchive(item) {
						removed = true
						continue
					}
					out = append(out, item)
				}
				return out
			},
		},
		{
			name: "a platform archive has a wrong target",
			mutate: func(t *testing.T, items []artifact) []artifact {
				archive := findArtifact(t, items, isPlatformArchive("linux", "amd64"))
				archive.Target = "linux_arm64_v8.0"
				return items
			},
		},
		{
			name: "a platform archive has a non-gentle-ai binary",
			mutate: func(t *testing.T, items []artifact) []artifact {
				archive := findArtifact(t, items, isPlatformArchive("linux", "amd64"))
				archive.Extra["Binaries"] = []any{"not-gentle-ai"}
				return items
			},
		},
		{
			name: "a platform archive carries Extra.ID assets",
			mutate: func(t *testing.T, items []artifact) []artifact {
				archive := findArtifact(t, items, isPlatformArchive("linux", "amd64"))
				archive.Extra["ID"] = assetsArchiveID
				return items
			},
		},
		{
			name: "a binary is missing from the four-platform matrix",
			mutate: func(t *testing.T, items []artifact) []artifact {
				out := make([]artifact, 0, len(items))
				removed := false
				for _, item := range items {
					if !removed && item.Type == "Binary" {
						removed = true
						continue
					}
					out = append(out, item)
				}
				return out
			},
		},
		{
			name: "a windows binary is present",
			mutate: func(t *testing.T, items []artifact) []artifact {
				windows := artifact{
					Name: "gentle-ai", Path: "dist/gentle-ai_windows_amd64_v1/gentle-ai.exe",
					GOOS: "windows", GOARCH: "amd64", Target: "windows_amd64_v1", Type: "Binary",
					Extra: map[string]any{"Binary": "gentle-ai", "ID": "gentle-ai"},
				}
				return append(items, windows)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := tc.mutate(t, baselineArtifactsWithAssetsID())
			if err := runValidateArtifacts(t, items); err == nil {
				t.Fatal("policy accepted a release-shape bypass it must still reject")
			}
		})
	}
}
