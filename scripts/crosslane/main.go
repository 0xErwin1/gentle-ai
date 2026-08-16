// Command crosslane is the local cross-lane integration battery.
//
// It drives one real gentle-ai binary (--binary) end to end across the
// integration boundaries where host runtimes meet the Go facade:
//
//   - opencode lane: the REAL OpenCode transport plugin bytes
//     (internal/assets/opencode/plugins/opencode-review-transport.ts) driven
//     through an emulated Task hook surface with HOST-assembled binding
//     frames, against a live scratch-repo lineage. Covers the lens frame and
//     the validator role frame, in both host-shaped and Go-shaped variants.
//   - claude lane: one low-risk full lifecycle (start -> finalize -> validate
//     gate allow) and one medium-candidate consent/v3 round-trip (granted).
//     With --with-model it additionally runs the real compiled claude-code
//     reviewer runtime, which is why this battery lives outside CI.
//   - schema lane: every envelope captured along the way is validated
//     against the published schemas in contracts/review-integration/.
//     Any emitter/schema divergence fails the battery.
//
// The battery is intentionally honest: known-red checks (host binding frames
// pending fix/opencode-host-binding, schema gaps) FAIL and are annotated,
// because red at the exact seam where field defects escaped is the battery
// proving its worth.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	binary := flag.String("binary", "", "path to the gentle-ai binary under test (required)")
	withModel := flag.Bool("with-model", false, "include the real reviewer model run (uses the dev subscription)")
	keepWork := flag.Bool("keep-work", false, "keep the scratch working directory for inspection")
	flag.Parse()

	if *binary == "" {
		fmt.Fprintln(os.Stderr, "crosslane: --binary <path> is required")
		os.Exit(2)
	}
	resolved, err := filepath.Abs(*binary)
	if err == nil {
		_, err = os.Stat(resolved)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "crosslane: binary %q is not usable: %v\n", *binary, err)
		os.Exit(2)
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "crosslane: %v\n", err)
		os.Exit(2)
	}
	workRoot, err := os.MkdirTemp("", "gentle-ai-crosslane-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "crosslane: %v\n", err)
		os.Exit(2)
	}

	b := &battery{
		binary:    resolved,
		repoRoot:  repoRoot,
		workRoot:  workRoot,
		withModel: *withModel,
	}
	if !*keepWork {
		defer os.RemoveAll(workRoot)
	} else {
		defer fmt.Printf("\nwork directory kept: %s\n", workRoot)
	}

	b.captureCapabilities()
	b.runOpenCodeLane()
	b.runClaudeLane()
	b.runSchemaLane()

	failed := b.printTable()
	if failed > 0 {
		os.Exit(1)
	}
}

func (b *battery) printTable() int {
	fmt.Println()
	fmt.Println("cross-lane battery results")
	fmt.Printf("binary: %s\n", b.binary)
	fmt.Println()
	laneWidth, nameWidth := len("lane"), len("check")
	for _, c := range b.checks {
		laneWidth = max(laneWidth, len(c.Lane))
		nameWidth = max(nameWidth, len(c.Name))
	}
	fmt.Printf("%-*s  %-*s  %-6s  %s\n", laneWidth, "lane", nameWidth, "check", "status", "note")
	failed := 0
	for _, c := range b.checks {
		fmt.Printf("%-*s  %-*s  %-6s  %s\n", laneWidth, c.Lane, nameWidth, c.Name, c.Status, c.Note)
		if c.Status == statusFail {
			failed++
		}
	}
	fmt.Println()
	fmt.Printf("total: %d checks, %d failed\n", len(b.checks), failed)
	return failed
}
