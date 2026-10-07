//go:build windows

package shellinstaller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserWindowsMainDescriptorReadback(t *testing.T) {
	commit := strings.Repeat("a", 40)
	for _, mutation := range []string{"canonical", "wrong URL", "unknown field"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			artifact, _ := userWindowsMainArtifact(commit, strings.Repeat("ab", 32))
			if mutation == "wrong URL" {
				artifact.URL = "https://foreign.invalid/source.zip"
			}
			data, _ := json.Marshal(artifact)
			if mutation == "unknown field" {
				data = []byte(strings.TrimSuffix(string(data), "}") + `,"Injected":true}`)
			}
			if err := userWindowsWrite(filepath.Join(root, "main.json"), data); err != nil {
				t.Fatal(err)
			}
			_, err := userWindowsMainRead(root, userWindowsManifest{MainCommit: commit, MainArchiveSHA: artifact.SHA})
			if (err != nil) != (mutation != "canonical") {
				t.Fatalf("Main descriptor readback %s: %v", mutation, err)
			}
		})
	}
}

func TestUserWindowsMainArchiveAuthority(t *testing.T) {
	commit := strings.Repeat("a", 40)
	prefix := "gentle-shell-" + commit
	for _, tt := range []struct{ name, member string }{
		{"canonical", prefix + "/bin/entry.mjs"},
		{"wrong prefix", "foreign/entry.mjs"},
		{"traversal", prefix + "/../escape"},
		{"native replacement", prefix + "/.gentle-ai/entry"},
		{"dependency replacement", prefix + "/NODE_MODULES/entry"},
		{"duplicate", prefix + "/package.json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, _ := userWindowsZipFixture(t, prefix+"/package.json", tt.member)
			artifact, err := userWindowsMainArtifact(commit, userWindowsSHA(data))
			if err != nil {
				t.Fatal(err)
			}
			_, err = userWindowsMainFiles(context.Background(), data, artifact)
			if (err != nil) != (tt.name != "canonical") {
				t.Fatalf("Main archive %s: %v", tt.name, err)
			}
			artifact.SHA = strings.Repeat("0", 64)
			if _, err := userWindowsMainFiles(context.Background(), data, artifact); err == nil {
				t.Fatal("changed retained archive digest accepted")
			}
		})
	}
}

func TestUserWindowsMainExactSourceSet(t *testing.T) {
	ctx := context.Background()
	commit := strings.Repeat("a", 40)
	prefix := "gentle-shell-" + commit
	data, _ := userWindowsZipFixture(t, prefix+"/package.json", prefix+"/bin/entry.mjs")
	artifact, _ := userWindowsMainArtifact(commit, userWindowsSHA(data))
	for _, mutation := range []string{"unchanged", "extra file", "extra directory", "changed bytes", "missing source", "alias"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			if mutation != "missing source" && mutation != "alias" {
				if _, err := userWindowsZIP(ctx, data, artifact, root, ""); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			switch mutation {
			case "extra file":
				err = userWindowsWrite(filepath.Join(root, "extra.mjs"), nil)
			case "extra directory":
				err = os.Mkdir(filepath.Join(root, "extra"), 0700)
			case "changed bytes":
				err = os.WriteFile(filepath.Join(root, "package.json"), []byte("changed fixture"), 0600)
			case "alias":
				real := filepath.Join(t.TempDir(), "real.json")
				if err := os.WriteFile(real, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, filepath.Join(root, "package.json")); err != nil {
					t.Skipf("unelevated symlink unavailable: %v", err)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			err = userWindowsCompareMain(ctx, data, artifact, root)
			if (err != nil) != (mutation != "unchanged") {
				t.Fatalf("Main exact-set %s: %v", mutation, err)
			}
		})
	}
}
