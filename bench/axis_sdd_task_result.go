package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// This opt-in axis drives the installed OpenCode plugin rather than the native
// CLI. A task result is provider-owned transport, so the core offline corpus
// cannot manufacture this boundary without misrepresenting what it measures.
const sddTaskResultAxis = "sdd-task-result"

func init() {
	RegisterAxis(Axis{
		Name:     sddTaskResultAxis,
		Title:    "OpenCode SDD task-result transport failures",
		BlackBox: false,
		Properties: []string{
			"Runs the installed OpenCode plugin through Node with a provider-shaped empty task_result; it does not drive the gentle-ai CLI alone.",
			"Requires GENTLE_AI_BENCH_SDD_PLUGIN to name the installed review-result-artifacts.ts and skips honestly when Node or that plugin is unavailable.",
		},
		Journeys: sddTaskResultJourneys,
	})
}

func sddTaskResultJourneys() []Journey {
	return []Journey{{
		ID:     "tr01-sdd-empty-task-result",
		Title:  "Empty SDD task result: typed terminal failure without artifact mutation or downstream launch",
		Source: "issue #2117 provider transport report",
		Steps: []Step{{
			Name:      "provider-shaped empty task result reaches the installed OpenCode plugin",
			Skip:      sddTaskResultUnavailable,
			Composite: sddEmptyTaskResult,
		}},
	}}
}

func sddTaskResultPluginPath() string {
	return os.Getenv("GENTLE_AI_BENCH_SDD_PLUGIN")
}

func sddTaskResultUnavailable(*Sandbox) string {
	path := sddTaskResultPluginPath()
	if path == "" {
		return "GENTLE_AI_BENCH_SDD_PLUGIN is not set"
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return "GENTLE_AI_BENCH_SDD_PLUGIN does not name an installed plugin"
	}
	if _, err := exec.LookPath("node"); err != nil {
		return "node is unavailable"
	}
	return ""
}

const sddTaskResultHarness = `import { readFile, writeFile } from "node:fs/promises"
const source = process.argv[2]
const cwd = process.argv[3]
await writeFile("./plugin.mts", await readFile(source))
const { default: plugin } = await import("./plugin.mts")
const hooks = await plugin({ directory: cwd, worktree: cwd })
const artifact = cwd + "/proposal.md"
await writeFile(artifact, "existing artifact")
const input = { tool: "task", sessionID: "bench-sdd", callID: "empty", args: { subagent_type: "sdd-propose", prompt: "create a proposal" } }
let failure = "NO_ERROR"
try {
  await hooks["tool.execute.after"](input, { title: "", output: "<task id=\"phase\" state=\"completed\">\n<task_result>\n\n</task_result>\n</task>", metadata: {} })
} catch (cause) {
  failure = cause instanceof Error ? cause.message : String(cause)
}
let downstream = "NO_ERROR"
try {
  await hooks["tool.execute.before"]({ ...input, callID: "downstream", args: { subagent_type: "sdd-apply", prompt: "continue" } }, { args: { subagent_type: "sdd-apply", prompt: "continue" } })
} catch (cause) {
  downstream = cause instanceof Error ? cause.message : String(cause)
}
console.log([failure, downstream, await readFile(artifact, "utf8")].join("\n---\n"))
`

func sddEmptyTaskResult(r *journeyRun) error {
	root := filepath.Join(r.sandbox.Root, "sdd-task-result")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "harness.mts"), []byte(sddTaskResultHarness), 0o600); err != nil {
		return err
	}
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("node", "harness.mts", sddTaskResultPluginPath(), work)
	cmd.Dir = root
	cmd.Env = r.sandbox.env()
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	observation := Observation{Args: []string{"opencode", "task", "sdd-propose"}, ExitCode: 0, Stdout: output.String(), StdoutCaptured: true, StderrCaptured: true}
	if err != nil {
		observation.ExitCode = 1
		observation.Stderr = output.String()
	}
	record := r.accumulator.observe(r.step, observation, nil, true)
	r.accumulator.records = append(r.accumulator.records, record)
	if err != nil {
		return fmt.Errorf("run OpenCode task-result harness: %w: %s", err, output.String())
	}
	parts := strings.Split(strings.TrimSpace(output.String()), "\n---\n")
	if len(parts) != 3 {
		return fmt.Errorf("task-result harness output = %q", output.String())
	}
	if !strings.Contains(parts[0], "sdd_task_result_empty") || !strings.Contains(parts[1], "sdd_task_result_empty") {
		return fmt.Errorf("empty result was not routed as one typed terminal failure: %q", parts[:2])
	}
	if parts[2] != "existing artifact" {
		return fmt.Errorf("task-result failure mutated artifact state: %q", parts[2])
	}
	return nil
}
