package assets

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestSkillRegistryPluginSkipsNonProjectDirectories runs the real plugin
// under node with a fake `gentle-ai` on PATH that records its argv. OpenCode
// resolves a brand-new non-project directory to "/" (or another markerless
// location); the plugin must skip those without spawning, and spawn exactly
// once for a real project root.
func TestSkillRegistryPluginSkipsNonProjectDirectories(t *testing.T) {
	source, err := Read("opencode/plugins/skill-registry.ts")
	if err != nil {
		t.Fatal(err)
	}
	const harness = `import { existsSync, mkdirSync, mkdtempSync, readFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import plugin from "./plugin.mts"

const projectDir = mkdtempSync(join(tmpdir(), "skill-registry-project-"))
mkdirSync(join(projectDir, ".git"))
const bareDir = mkdtempSync(join(tmpdir(), "skill-registry-bare-"))

await plugin({ worktree: "/", directory: "" })
await plugin({ worktree: "", directory: "" }) // falls back to process.cwd(): the markerless harness dir
await plugin({ worktree: bareDir, directory: "" })
await plugin({ worktree: projectDir, directory: "" })

const logPath = process.env.GENTLE_AI_RELAY_LOG
const readLines = () => (existsSync(logPath) ? readFileSync(logPath, "utf8").split("\n").filter(Boolean) : [])
const deadline = Date.now() + 5000
while (Date.now() < deadline && readLines().length < 1) {
  await new Promise((resolve) => setTimeout(resolve, 50))
}
// Grace period so a wrongly-spawned refresh for a skipped directory can land.
await new Promise((resolve) => setTimeout(resolve, 300))
console.log(JSON.stringify({ project: projectDir, lines: readLines() }))
`
	const relay = `#!/usr/bin/env node
import { appendFileSync } from "node:fs"
appendFileSync(process.env.GENTLE_AI_RELAY_LOG, JSON.stringify(process.argv.slice(2)) + "\n")
`
	output, _ := runOpenCodeTransportPluginHarness(t, map[string]string{"plugin.mts": string(source)}, harness, relay)
	// The plugin's console.info skip notices share stdout with the harness
	// result; the result is the final line.
	trimmed := strings.Split(strings.TrimSpace(output), "\n")
	last := trimmed[len(trimmed)-1]
	for _, line := range trimmed[:len(trimmed)-1] {
		if !strings.HasPrefix(line, "[skill-registry] skipping refresh:") {
			t.Fatalf("unexpected plugin output line %q (skips must be console.info notices, never errors)", line)
		}
	}
	var result struct {
		Project string   `json:"project"`
		Lines   []string `json:"lines"`
	}
	if err := json.Unmarshal([]byte(last), &result); err != nil {
		t.Fatalf("decode guard harness output %q: %v", output, err)
	}
	if len(result.Lines) != 1 {
		t.Fatalf("plugin must spawn exactly once (for the project root), got %d spawns: %v", len(result.Lines), result.Lines)
	}
	want := fmt.Sprintf(`["skill-registry","refresh","--quiet","--no-gitignore","--cwd",%q]`, result.Project)
	if result.Lines[0] != want {
		t.Fatalf("spawn argv = %s, want %s", result.Lines[0], want)
	}
}
