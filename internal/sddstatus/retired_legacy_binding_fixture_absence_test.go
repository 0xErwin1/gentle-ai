package sddstatus

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRetiredLegacyBindingFixturesAreAbsent(t *testing.T) {
	repoRoot := retiredLegacyFixtureRepositoryRoot(t)

	for _, fixture := range []string{
		"internal/sddstatus/legacy_binding_read_test.go",
		"internal/sddstatus/runtime_ledger_interrupted_legacy_test.go",
	} {
		requireRetiredLegacyFixtureAbsent(t, repoRoot, fixture)
	}

	currentFixture := filepath.Join(repoRoot, "internal/sddstatus/runtime_ledger_interrupted_current_test.go")
	contents, err := os.ReadFile(currentFixture)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("current malformed interrupted-evidence coverage fixture %q is missing", currentFixture)
		}
		t.Fatalf("read current malformed interrupted-evidence coverage fixture %q: %v", currentFixture, err)
	}
	if hasGoBuildConstraintInRetiredFixture(string(contents)) {
		t.Fatalf("current malformed interrupted-evidence coverage fixture %q must remain untagged", currentFixture)
	}
}

func retiredLegacyFixtureRepositoryRoot(t *testing.T) string {
	t.Helper()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository root from this test source: runtime.Caller failed")
	}

	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	info, err := os.Stat(repoRoot)
	if err != nil {
		t.Fatalf("stat repository root resolved from %q: %v", source, err)
	}
	if !info.IsDir() {
		t.Fatalf("repository root resolved from %q is not a directory: %q", source, repoRoot)
	}
	return repoRoot
}

func requireRetiredLegacyFixtureAbsent(t *testing.T, repoRoot, fixture string) {
	t.Helper()

	path := filepath.Join(repoRoot, fixture)
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("retired legacy fixture %q is still present", fixture)
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lstat retired legacy fixture %q: %v", fixture, err)
	}
}

func hasGoBuildConstraintInRetiredFixture(source string) bool {
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case line == "//go:build" || strings.HasPrefix(line, "//go:build "):
			return true
		case line == "// +build" || strings.HasPrefix(line, "// +build "):
			return true
		case strings.HasPrefix(line, "//"):
			continue
		default:
			return false
		}
	}
	return false
}
