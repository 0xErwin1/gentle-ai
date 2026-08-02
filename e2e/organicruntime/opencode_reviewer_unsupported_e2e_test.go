package organicruntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents/opencode"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/sdd"
)

func TestRealOpenCodeRejectsUnsupportedImmutableReviewerBash(t *testing.T) {
	if os.Getenv(realAgentE2EEnvironment) != "1" {
		t.Skip("set GENTLE_AI_REAL_AGENT_E2E=1 to run the real OpenCode reviewer rejection proof")
	}
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only proof for OpenCode 1.18.10 Bash permission behavior")
	}
	requireOrganicExecutableVersion(t, "opencode", pinnedOpenCodeVersion)

	harness := newOrganicHarness(t)
	home := t.TempDir()
	configRoot := prepareOpenCodeConfig(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	if _, err := sdd.Inject(home, opencode.NewAdapter(), ""); err != nil {
		t.Fatalf("generate OpenCode reviewer: %v", err)
	}

	marker := filepath.Join(harness.repo.worktree, "reviewer-bash-ran")
	prompt := "Attempt this immutable Bash inspection exactly once: gentle-ai review inspect-candidate " +
		"--repository-context rctx1_" + strings.Repeat("a", 64) +
		" --expected-revision sha256:" + strings.Repeat("b", 64) +
		" --lineage reviewer-e2e --target sha256:" + strings.Repeat("d", 64) +
		" --lens review-risk --order 0 --operation name-status > " + marker
	fixture := newOpenCodeFixtureServer(t, []openCodeTurn{{tool: "task", arguments: map[string]any{
		"description":   "Attempt immutable reviewer inspection",
		"subagent_type": "review-risk",
		"prompt": "GENTLE_AI_REVIEW_BINDING {\"lineage\":\"reviewer-e2e\",\"target\":\"sha256:" + strings.Repeat("d", 64) +
			"\",\"lens\":\"review-risk\",\"order\":0,\"repository_context\":\"rctx1_" + strings.Repeat("a", 64) +
			"\",\"revision\":\"sha256:" + strings.Repeat("b", 64) + "\",\"subject_hash\":\"sha256:" + strings.Repeat("c", 64) + "\"}\n" + prompt,
	}}}, "")
	defer fixture.Close()

	config := generatedOpenCodeReviewConfig(t, filepath.Join(configRoot, "opencode", "opencode.json"), fixture.URL)
	environment := replaceOrganicEnvironment(organicEnvironment(home), map[string]string{
		"XDG_CONFIG_HOME":                           configRoot,
		"XDG_CACHE_HOME":                            t.TempDir(),
		"OPENCODE_CONFIG_DIR":                       filepath.Join(configRoot, "opencode"),
		"OPENCODE_TEST_HOME":                        filepath.Join(home, "opencode"),
		"OPENCODE_CONFIG_CONTENT":                   config,
		"OPENCODE_AUTH_CONTENT":                     "{}",
		"OPENCODE_DISABLE_PROJECT_CONFIG":           "1",
		"OPENCODE_DISABLE_AUTOUPDATE":               "1",
		"OPENCODE_DISABLE_AUTOCOMPACT":              "1",
		"OPENCODE_DISABLE_CLAUDE_CODE":              "1",
		"OPENCODE_DISABLE_DEFAULT_PLUGINS":          "1",
		"OPENCODE_DISABLE_EXTERNAL_SKILLS":          "1",
		"OPENCODE_DISABLE_LSP_DOWNLOAD":             "1",
		"OPENCODE_DISABLE_MODELS_FETCH":             "1",
		"OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER": "1",
		"OPENCODE_FAST_BOOT":                        "1",
		"OPENCODE_PURE":                             "1",
	})

	ctx, cancel := context.WithTimeout(context.Background(), organicAgentTimeout)
	defer cancel()
	command := organicCommandContext(ctx, "opencode", "run", "--pure", "--format", "json", "--agent", "review-driver", "--model", "fixture/fixture", "--dir", harness.repo.worktree, "Delegate the immutable review inspection.")
	command.Dir = harness.repo.worktree
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("opencode run: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	fixture.assertComplete(t, false)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected reviewer Bash changed the worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(harness.commonDir(), "gentle-ai", "review-transactions", "v2")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected reviewer Bash created review authority: %v", err)
	}
}

func generatedOpenCodeReviewConfig(t *testing.T, settingsPath, serverURL string) string {
	t.Helper()
	payload, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(payload, &config); err != nil {
		t.Fatal(err)
	}
	config["provider"] = map[string]any{"fixture": map[string]any{
		"npm": "@ai-sdk/openai-compatible", "name": "OpenCode Reviewer E2E Fixture",
		"options": map[string]any{"baseURL": serverURL + "/v1", "apiKey": "fixture"},
		"models":  map[string]any{"fixture": map[string]any{"name": "Fixture"}},
	}}
	agents := config["agent"].(map[string]any)
	agents["review-driver"] = map[string]any{
		"description": "Attempts the generated immutable reviewer", "mode": "primary", "model": "fixture/fixture",
		"permission": map[string]any{"bash": "deny", "task": "allow", "edit": "deny"},
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func replaceOrganicEnvironment(environment []string, values map[string]string) []string {
	for name, value := range values {
		prefix := name + "="
		replaced := false
		for index, entry := range environment {
			if strings.HasPrefix(entry, prefix) {
				environment[index], replaced = prefix+value, true
			}
		}
		if !replaced {
			environment = append(environment, prefix+value)
		}
	}
	return environment
}
