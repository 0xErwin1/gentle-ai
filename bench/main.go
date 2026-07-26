// Command gentle-ai-bench measures the FRICTION of driving gentle-ai's review
// lifecycle, so a "before" binary and an "after" binary can be compared.
//
// It is a black box: it drives a gentle-ai binary as a subprocess and never
// instruments the product, so it works against any build including old
// releases. It is deterministic and offline: no real model call is ever made.
//
// It deliberately does NOT measure wall-clock time or real model tokens, and
// it deliberately does NOT emit a single composite score. See README.md.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		os.Exit(commandRun(os.Args[2:]))
	case "compare":
		os.Exit(commandCompare(os.Args[2:]))
	case "record":
		os.Exit(commandRecord(os.Args[2:]))
	case "analyze":
		os.Exit(commandAnalyze(os.Args[2:]))
	case "__shim":
		os.Exit(commandShim(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `gentle-ai-bench — friction benchmark for the gentle-ai review lifecycle

  run      gentle-ai-bench run --binary <path> --out results.json
           Drive the built-in journey corpus against a binary (driven mode).

  record   gentle-ai-bench record --binary <path> --out session.jsonl
           Print the PATH line that records a real agent session (observed mode).

  analyze  gentle-ai-bench analyze --session session.jsonl --out results.json
           Compute the same dimensions from a recorded session.

  compare  gentle-ai-bench compare --before a.json --after b.json
           Per-dimension table. Refuses to compare driven against observed.

It does not measure wall-clock time, real model tokens, or a composite score.
`)
}

func commandRun(args []string) int {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	binary := flags.String("binary", "", "path to the gentle-ai binary to drive")
	out := flags.String("out", "results.json", "where to write the machine-readable results")
	only := flags.String("only", "", "comma-separated journey ids to run (default: all)")
	_ = flags.Parse(args)

	if strings.TrimSpace(*binary) == "" {
		fmt.Fprintln(os.Stderr, "run requires --binary")
		return 2
	}
	resolved, err := filepath.Abs(*binary)
	if err != nil || !executable(resolved) {
		fmt.Fprintf(os.Stderr, "cannot execute binary %q\n", *binary)
		return 2
	}

	selected := map[string]bool{}
	for _, id := range strings.Split(*only, ",") {
		if id = strings.TrimSpace(id); id != "" {
			selected[id] = true
		}
	}

	results := Results{
		Schema:        ResultsSchema,
		Mode:          ModeDriven,
		Binary:        resolved,
		BinaryVersion: binaryVersion(resolved),
	}
	for _, journey := range Journeys() {
		if len(selected) > 0 && !selected[journey.ID] {
			continue
		}
		fmt.Fprintf(os.Stderr, "running %s ...\n", journey.ID)
		results.Journeys = append(results.Journeys, runJourney(resolved, journey))
	}
	sortJourneys(results.Journeys)
	results.Totals, results.JourneysCounted, results.JourneysUnsupported, results.JourneysFailed = aggregate(results.Journeys)
	results.Notes = []string{
		"Driven mode: every journey ran in a fresh temp dir with its own HOME, XDG_*, throwaway git repo and local bare remote.",
		"Reviewer results were synthesized from the binary's own collect envelope. No model was called.",
		"No wall-clock timing is measured or reported.",
	}

	if err := writeJSON(*out, results); err != nil {
		fmt.Fprintf(os.Stderr, "write results: %v\n", err)
		return 1
	}
	writeRunReport(os.Stdout, results)
	fmt.Fprintf(os.Stderr, "\nwrote %s\n", *out)
	return 0
}

func commandRecord(args []string) int {
	flags := flag.NewFlagSet("record", flag.ExitOnError)
	binary := flags.String("binary", "", "path to the real gentle-ai binary the shim delegates to")
	out := flags.String("out", "session.jsonl", "where the shim appends recorded invocations")
	_ = flags.Parse(args)

	if strings.TrimSpace(*binary) == "" {
		fmt.Fprintln(os.Stderr, "record requires --binary")
		return 2
	}
	if err := setupRecording(*binary, *out); err != nil {
		fmt.Fprintf(os.Stderr, "record: %v\n", err)
		return 1
	}
	return 0
}

func commandShim(args []string) int {
	real, logPath := "", ""
	forwarded := []string{}
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--real":
			index++
			if index < len(args) {
				real = args[index]
			}
		case "--log":
			index++
			if index < len(args) {
				logPath = args[index]
			}
		case "--":
			forwarded = append(forwarded, args[index+1:]...)
			index = len(args)
		}
	}
	if real == "" || logPath == "" {
		fmt.Fprintln(os.Stderr, "gentle-ai-bench shim: missing --real or --log")
		return 126
	}
	return runShim(real, logPath, forwarded)
}

func commandAnalyze(args []string) int {
	flags := flag.NewFlagSet("analyze", flag.ExitOnError)
	session := flags.String("session", "", "recorded session JSONL")
	out := flags.String("out", "results-observed.json", "where to write the machine-readable results")
	_ = flags.Parse(args)

	if strings.TrimSpace(*session) == "" {
		fmt.Fprintln(os.Stderr, "analyze requires --session")
		return 2
	}
	records, err := readSession(*session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze: %v\n", err)
		return 1
	}

	journey := analyzeSession(records)
	results := Results{
		Schema:   ResultsSchema,
		Mode:     ModeObserved,
		Binary:   *session,
		Journeys: []JourneyResult{journey},
	}
	results.Totals, results.JourneysCounted, results.JourneysUnsupported, results.JourneysFailed = aggregate(results.Journeys)
	results.Notes = []string{
		"Observed mode: dimensions are computed from what a real agent session actually invoked.",
		"model_runs is a PROXY here, not a measurement: the agent's model calls never cross the process boundary.",
		"human_prompts counts the consent-skipped notice, i.e. times the tool WOULD have asked; an agent session has no TTY.",
		"blocks and recovery_round_trips are exact: the invocation sequence is what happened.",
	}

	if err := writeJSON(*out, results); err != nil {
		fmt.Fprintf(os.Stderr, "write results: %v\n", err)
		return 1
	}
	writeRunReport(os.Stdout, results)
	fmt.Fprintf(os.Stderr, "\nwrote %s\n", *out)
	return 0
}

func commandCompare(args []string) int {
	flags := flag.NewFlagSet("compare", flag.ExitOnError)
	beforePath := flags.String("before", "", "results JSON from the before binary")
	afterPath := flags.String("after", "", "results JSON from the after binary")
	out := flags.String("out", "", "optional machine-readable comparison JSON")
	_ = flags.Parse(args)

	if strings.TrimSpace(*beforePath) == "" || strings.TrimSpace(*afterPath) == "" {
		fmt.Fprintln(os.Stderr, "compare requires --before and --after")
		return 2
	}
	before, err := readResults(*beforePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "compare: %v\n", err)
		return 1
	}
	after, err := readResults(*afterPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "compare: %v\n", err)
		return 1
	}
	if before.Mode != after.Mode {
		fmt.Fprintf(os.Stderr,
			"refusing to compare %s mode against %s mode: they measure different populations.\n"+
				"A driven run executes a fixed corpus; an observed run captures whatever one agent happened to do.\n"+
				"Compare driven against driven, or observed against observed.\n",
			before.Mode, after.Mode)
		return 2
	}

	report := buildComparison(before, after)
	if strings.TrimSpace(*out) != "" {
		if err := writeJSON(*out, report); err != nil {
			fmt.Fprintf(os.Stderr, "write comparison: %v\n", err)
			return 1
		}
	}
	writeCompareReport(os.Stdout, report)
	return 0
}

func readResults(path string) (Results, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Results{}, err
	}
	var results Results
	if err := json.Unmarshal(content, &results); err != nil {
		return Results{}, fmt.Errorf("%s: %w", path, err)
	}
	if results.Mode == "" {
		return Results{}, fmt.Errorf("%s: results carry no mode", path)
	}
	return results, nil
}

func writeJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func binaryVersion(path string) string {
	output, err := exec.Command(path, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
