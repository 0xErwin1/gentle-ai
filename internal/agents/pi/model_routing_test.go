package pi

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func must(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

func writeManifest(t *testing.T, root, manifest string) {
	t.Helper()
	must(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(manifest), 0o644))
}

func writeTarget(t *testing.T, root, relative string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(t, os.WriteFile(path, []byte("#!/bin/sh\n"), mode))
	return path
}

func assertKind(t *testing.T, err error, domain, kind string) {
	t.Helper()
	var gotDomain, got string
	switch e := err.(type) {
	case *PackageError:
		gotDomain, got = "package", e.Kind
	case *ManifestError:
		gotDomain, got = "manifest", e.Kind
	case *BinError:
		gotDomain, got = "bin", e.Kind
	}
	if gotDomain != domain || got != kind {
		t.Fatalf("error = %T %v; want %s error %q", err, err, domain, kind)
	}
}

func TestResolvePackageBinForms(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		root := t.TempDir()
		writeManifest(t, root, `{"name":"gentle-pi-models","bin":"bin/gentle-pi-models"}`)
		want := writeTarget(t, root, "bin/gentle-pi-models", 0o755)
		got, err := ResolvePackageBin(root)
		if err != nil || got != want {
			t.Fatalf("ResolvePackageBin() = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("object and canonical symlink", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink permissions vary on Windows")
		}
		root := t.TempDir()
		want := writeTarget(t, root, "real/gentle-pi-models", 0o755)
		bin := filepath.Join(root, "bin", "gentle-pi-models")
		must(t, os.MkdirAll(filepath.Dir(bin), 0o755))
		must(t, os.Symlink(filepath.Join("..", "real", "gentle-pi-models"), bin))
		writeManifest(t, root, `{"bin":{"gentle-pi-models":"bin/gentle-pi-models"}}`)
		got, err := ResolvePackageBin(root)
		if err != nil || got != want {
			t.Fatalf("ResolvePackageBin() = %q, %v; want canonical %q", got, err, want)
		}
	})
}

func TestResolvePackageBinErrors(t *testing.T) {
	cases := []struct{ name, manifest, domain, kind, setup string }{
		{"missing package", "", "package", "missing-package", "package-missing"},
		{"missing manifest", "", "manifest", "missing-manifest", ""},
		{"malformed manifest", `{"bin":`, "manifest", "malformed-manifest", ""},
		{"malformed bin", `{"bin":true}`, "bin", "malformed-bin", ""},
		{"absent bin", `{"name":"gentle-pi-models"}`, "bin", "absent-bin", ""},
		{"absent object bin", `{"bin":{"other":"bin/other"}}`, "bin", "absent-bin", ""},
		{"missing target", `{"bin":{"gentle-pi-models":"bin/missing"}}`, "bin", "missing-bin-target", ""},
		{"non-regular target", `{"bin":{"gentle-pi-models":"bin/gentle-pi-models"}}`, "bin", "non-regular-bin-target", "directory"},
		{"non-executable target", `{"bin":{"gentle-pi-models":"bin/gentle-pi-models"}}`, "bin", "non-executable-bin-target", "nonexec"},
		{"absolute target", `{"bin":{"gentle-pi-models":"/outside"}}`, "bin", "unsafe-bin", ""},
		{"lexical escape", `{"bin":{"gentle-pi-models":"../outside"}}`, "bin", "unsafe-bin", ""},
		{"duplicate top-level bin", `{"bin":"bin/x","bin":"bin/y"}`, "bin", "malformed-bin", ""},
		{"duplicate selected bin", `{"bin":{"gentle-pi-models":"bin/x","gentle-pi-models":"bin/y"}}`, "bin", "malformed-bin", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup == "nonexec" && runtime.GOOS == "windows" {
				t.Skip("Windows does not use executable permission bits")
			}
			root := t.TempDir()
			if tc.setup == "package-missing" {
				root = filepath.Join(root, "missing")
			} else if tc.manifest != "" {
				writeManifest(t, root, tc.manifest)
			}
			if tc.setup == "directory" {
				must(t, os.MkdirAll(filepath.Join(root, "bin", "gentle-pi-models"), 0o755))
			} else if tc.setup == "nonexec" {
				writeTarget(t, root, "bin/gentle-pi-models", 0o644)
			}
			_, err := ResolvePackageBin(root)
			if tc.kind == "unsafe-bin" {
				assertKind(t, err, "bin", tc.kind)
			} else {
				assertKind(t, err, tc.domain, tc.kind)
				if tc.kind == "missing-package" || tc.kind == "missing-manifest" || tc.kind == "missing-bin-target" {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("error = %v; want os.ErrNotExist cause", err)
					}
				}
			}
		})
	}
}

func TestPackageErrorAndSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root, outside := t.TempDir(), filepath.Join(t.TempDir(), "outside")
	writeTarget(t, filepath.Dir(outside), filepath.Base(outside), 0o755)
	bin := filepath.Join(root, "bin", "gentle-pi-models")
	must(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	must(t, os.Symlink(outside, bin))
	writeManifest(t, root, `{"bin":{"gentle-pi-models":"bin/gentle-pi-models"}}`)
	_, err := ResolvePackageBin(root)
	assertKind(t, err, "bin", "unsafe-bin")
	if !errors.As(err, new(*UnsafeBinError)) {
		t.Fatalf("error = %T %v; want UnsafeBinError cause", err, err)
	}
}
