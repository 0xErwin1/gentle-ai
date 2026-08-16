package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/render"
)

// The read-only preview and the reconciliation must read the same installation.
// Planning against an empty manifest makes every managed resource the previous
// apply wrote report as a user-owned conflict, so the one operation whose job is
// to separate managed from user-owned content answers it backwards.
func TestConfigDiffReadsWhatApplyAlreadyWrote(t *testing.T) {
	home, destination := t.TempDir(), t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")
	document := `{"version":"v1","selection":{"agents":["opencode"]},"roles":[{"id":"writer","renderedName":"writer-v1"},{"id":"reviewer","references":["writer"]}]}`

	writeConfigDocument(t, configPath, document)
	runConfigMutation(t, "apply", configPath, home, destination)

	for _, operation := range []string{"plan", "diff"} {
		t.Run(operation, func(t *testing.T) {
			for _, planned := range planOperations(t, operation, configPath, home, destination) {
				// A provisioned component is installed rather than written, and
				// apply reports it pending instead of performing it, so it stays
				// outstanding by design.
				if planned.Selector == render.ProvisionSelector {
					continue
				}
				if planned.Kind != render.Skip {
					t.Errorf("%s of the applied document = %q on %s (%s), want skip",
						operation, planned.Kind, planned.Path, planned.Selector)
				}
			}
		})
	}
}

// A preview that reports no work for a changed document is the same defect
// inverted, so the rename must still be planned as a create and a removal.
func TestConfigDiffReportsAChangedDocument(t *testing.T) {
	home, destination := t.TempDir(), t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")

	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]},"roles":[{"id":"writer","renderedName":"writer-v1"}]}`)
	runConfigMutation(t, "apply", configPath, home, destination)

	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]},"roles":[{"id":"writer","renderedName":"writer-v2"}]}`)

	kinds := map[render.OperationKind]int{}
	for _, planned := range planOperations(t, "diff", configPath, home, destination) {
		kinds[planned.Kind]++
	}

	if kinds[render.Create] == 0 || kinds[render.Remove] == 0 {
		t.Errorf("plan kinds = %v, want the rename planned as a create and a removal", kinds)
	}
}

func planOperations(t *testing.T, operation, configPath, home, destination string) []render.Operation {
	t.Helper()

	var output bytes.Buffer
	if err := RunConfig([]string{operation, "--config", configPath, "--home", home, "--destination", destination, "--stage", t.TempDir()}, &output); err != nil {
		t.Fatalf("RunConfig(%s) error = %v", operation, err)
	}

	var result struct {
		Plan render.ReconcilePlan `json:"plan"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode %s result: %v", operation, err)
	}

	return result.Plan.Operations
}

// The staging directory stands in for the destination, so nothing that reaches
// the destination may name it. A staged absolute path that survives is both a
// render whose bytes depend on where it was staged and a live configuration
// pointing inside a directory the run already discarded.
func TestRenderedContentNamesTheDestinationRatherThanTheStage(t *testing.T) {
	home, destination := t.TempDir(), t.TempDir()
	configPath := filepath.Join(t.TempDir(), "desired.json")
	writeConfigDocument(t, configPath, `{"version":"v1","selection":{"agents":["opencode"]}}`)

	first, second := t.TempDir(), t.TempDir()
	var output bytes.Buffer
	for _, stage := range []string{first, second} {
		output.Reset()
		if err := RunConfig([]string{"render", "--config", configPath, "--home", home, "--destination", destination, "--stage", stage}, &output); err != nil {
			t.Fatalf("RunConfig(render) error = %v", err)
		}
		assertNoStagedPaths(t, stage, stage)
	}

	assertSameTree(t, first, second)
}

func assertNoStagedPaths(t *testing.T, root, stage string) {
	t.Helper()

	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(contents, []byte(stage)) {
			t.Errorf("%s names the staging directory", strings.TrimPrefix(path, root))
		}

		return nil
	}); err != nil {
		t.Fatalf("walk %q: %v", root, err)
	}
}

func assertSameTree(t *testing.T, first, second string) {
	t.Helper()

	digests := func(root string) map[string]string {
		found := map[string]string{}
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || !entry.Type().IsRegular() {
				return err
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found[strings.TrimPrefix(path, root)] = digest(contents)

			return nil
		}); err != nil {
			t.Fatalf("walk %q: %v", root, err)
		}

		return found
	}

	if !reflect.DeepEqual(digests(first), digests(second)) {
		t.Error("the same document rendered different bytes into two staging directories")
	}
}

// Permission rules only mean something to an adapter that reads them as
// allow/deny/ask lists. Accepting them for an adapter that keys permissions
// differently writes a block the client never reads, which is indistinguishable
// from a working configuration until someone checks whether the rule applies.
func TestDeclaredPermissionsAreRefusedForAnAdapterThatCannotExpressThem(t *testing.T) {
	for _, test := range []struct {
		name     string
		document string
		want     string
	}{
		{
			name:     "an adapter keying permissions differently is named",
			document: `{"version":"v1","selection":{"agents":["opencode"],"permissions":{"deny":["Bash(rm -rf:*)"]}}}`,
			want:     "config.permissions.unsupported-adapter",
		},
		{
			name:     "an adapter reading rule lists takes them",
			document: `{"version":"v1","selection":{"agents":["claude-code"],"permissions":{"deny":["Bash(rm -rf:*)"]}}}`,
			want:     `"diagnostics": []`,
		},
		{
			name:     "a document declaring no rules is unaffected",
			document: `{"version":"v1","selection":{"agents":["opencode"]}}`,
			want:     `"diagnostics": []`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "desired.json")
			writeConfigDocument(t, configPath, test.document)

			assertConfigOutput(t, []string{"validate", "--config", configPath}, test.want)
		})
	}
}
