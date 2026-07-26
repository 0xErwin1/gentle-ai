package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// dimensionRow describes one printable dimension. Blocks are printed as four
// separate rows because collapsing them would hide the one thing that must
// never be hidden: dead ends going up while everything else goes down.
type dimensionRow struct {
	Label string
	Value func(MetricSet) (int, bool, string) // value, present, derivation
}

func dimensionRows() []dimensionRow {
	pick := func(get func(MetricSet) Dimension) func(MetricSet) (int, bool, string) {
		return func(set MetricSet) (int, bool, string) {
			dimension := get(set)
			if dimension.Value == nil {
				return 0, false, dimension.Derivation
			}
			return *dimension.Value, true, dimension.Derivation
		}
	}
	blocks := func(get func(BlockCounts) int) func(MetricSet) (int, bool, string) {
		return func(set MetricSet) (int, bool, string) { return get(set.Blocks), true, set.BlocksDerivation }
	}
	return []dimensionRow{
		{"1 human_prompts", pick(func(s MetricSet) Dimension { return s.HumanPrompts })},
		{"2 manual_tokens", pick(func(s MetricSet) Dimension { return s.ManualTokens })},
		{"3 commands_to_completion", pick(func(s MetricSet) Dimension { return s.CommandsToCompletion })},
		{"4 blocks (total)", blocks(func(b BlockCounts) int { return b.Total() })},
		{"4a   self_recovered", blocks(func(b BlockCounts) int { return b.SelfRecovered })},
		{"4b   in_band", blocks(func(b BlockCounts) int { return b.InBand })},
		{"4c   out_of_band", blocks(func(b BlockCounts) int { return b.OutOfBand })},
		{"4d   dead_end", blocks(func(b BlockCounts) int { return b.DeadEnd })},
		{"5 recovery_round_trips", pick(func(s MetricSet) Dimension { return s.RecoveryRoundTrips })},
		{"6 model_runs", pick(func(s MetricSet) Dimension { return s.ModelRuns })},
		{"7 human_surface_bytes", pick(func(s MetricSet) Dimension { return s.HumanSurfaceBytes })},
		{"- git_subprocesses (info)", pick(func(s MetricSet) Dimension { return s.GitSubprocesses })},
	}
}

func writeRunReport(out io.Writer, results Results) {
	fmt.Fprintf(out, "gentle-ai-bench — %s mode\n", results.Mode)
	fmt.Fprintf(out, "binary : %s\n", results.Binary)
	if results.BinaryVersion != "" {
		fmt.Fprintf(out, "version: %s\n", results.BinaryVersion)
	}
	fmt.Fprintf(out, "journeys: %d completed, %d unsupported, %d failed\n\n",
		results.JourneysCounted, results.JourneysUnsupported, results.JourneysFailed)

	rows := dimensionRows()
	headers := []string{"journey", "status"}
	for _, row := range rows {
		headers = append(headers, shortLabel(row.Label))
	}
	table := [][]string{headers}
	for _, journey := range results.Journeys {
		line := []string{journey.ID, journey.Status}
		for _, row := range rows {
			line = append(line, cell(journey, row))
		}
		table = append(table, line)
	}
	total := []string{"TOTAL (completed only)", ""}
	for _, row := range rows {
		value, present, _ := row.Value(results.Totals)
		total = append(total, format(value, present))
	}
	table = append(table, total)
	renderTable(out, table)

	fmt.Fprintf(out, "\nDimension legend\n")
	for _, row := range rows {
		value, present, derivation := row.Value(results.Totals)
		_ = value
		_ = present
		fmt.Fprintf(out, "  %-26s %s\n", row.Label, derivation)
	}

	blocked := []JourneyResult{}
	for _, journey := range results.Journeys {
		if journey.Status != StatusCompleted || journey.Metrics.Blocks.Total() > 0 {
			blocked = append(blocked, journey)
		}
	}
	if len(blocked) > 0 {
		fmt.Fprintf(out, "\nBlocks and unsupported steps\n")
		for _, journey := range blocked {
			fmt.Fprintf(out, "  %s (%s)\n", journey.ID, journey.Status)
			for _, step := range journey.UnsupportedSteps {
				fmt.Fprintf(out, "      unsupported: %s\n", step)
			}
			if journey.FailureReason != "" {
				fmt.Fprintf(out, "      failed: %s\n", journey.FailureReason)
			}
			for _, command := range journey.Commands {
				if command.Block == NotABlock {
					continue
				}
				fmt.Fprintf(out, "      %-13s %s\n", command.Block, command.Step)
				fmt.Fprintf(out, "                    %s\n", command.Message)
			}
		}
	}
	for _, note := range results.Notes {
		fmt.Fprintf(out, "\nnote: %s\n", note)
	}
}

func cell(journey JourneyResult, row dimensionRow) string {
	if journey.Status == StatusUnsupported {
		return "unsup"
	}
	if journey.Status == StatusFailed {
		return "fail"
	}
	value, present, _ := row.Value(journey.Metrics)
	return format(value, present)
}

func format(value int, present bool) string {
	if !present {
		return "null"
	}
	return strconv.Itoa(value)
}

func shortLabel(label string) string {
	fields := strings.Fields(label)
	if len(fields) < 2 {
		return label
	}
	name := fields[len(fields)-1]
	if strings.HasPrefix(label, "4 ") {
		return "blk"
	}
	switch name {
	case "human_prompts":
		return "prompt"
	case "manual_tokens":
		return "token"
	case "commands_to_completion":
		return "cmds"
	case "self_recovered":
		return "self"
	case "in_band":
		return "in"
	case "out_of_band":
		return "out"
	case "dead_end":
		return "dead"
	case "recovery_round_trips":
		return "recov"
	case "model_runs":
		return "model"
	case "human_surface_bytes":
		return "stderrB"
	default:
		return "git"
	}
}

// CompareReport is the machine-readable comparison.
//
// The dimension totals are computed over the COMPARABLE SUBSET only: journeys
// that completed in both runs. Summing a run of 14 completed journeys against
// a run of 5 would produce a large, meaningless delta that reads as a
// regression when it is really just a wider corpus. The excluded journeys are
// listed rather than silently dropped.
type CompareReport struct {
	Schema             string           `json:"schema"`
	Mode               string           `json:"mode"`
	Before             string           `json:"before"`
	After              string           `json:"after"`
	ComparableJourneys []string         `json:"comparable_journeys"`
	ExcludedJourneys   []string         `json:"excluded_journeys,omitempty"`
	Dimensions         []CompareRow     `json:"dimensions"`
	Journeys           []CompareJourney `json:"journeys"`
	Notes              []string         `json:"notes,omitempty"`
}

type CompareRow struct {
	Dimension  string `json:"dimension"`
	Before     *int   `json:"before"`
	After      *int   `json:"after"`
	Delta      *int   `json:"delta"`
	Derivation string `json:"derivation"`
}

type CompareJourney struct {
	ID           string            `json:"id"`
	BeforeStatus string            `json:"before_status"`
	AfterStatus  string            `json:"after_status"`
	Cells        map[string]string `json:"cells"`
}

func buildComparison(before, after Results) CompareReport {
	report := CompareReport{
		Schema: "gentle-ai-bench.comparison/v1",
		Mode:   before.Mode,
		Before: before.Binary,
		After:  after.Binary,
	}
	beforeIndex, afterIndex := byID(before), byID(after)
	comparable := []JourneyResult{}
	comparableAfter := []JourneyResult{}
	for _, journey := range after.Journeys {
		counterpart, ok := beforeIndex[journey.ID]
		if !ok || counterpart.Status != StatusCompleted || journey.Status != StatusCompleted {
			report.ExcludedJourneys = append(report.ExcludedJourneys, journey.ID)
			continue
		}
		report.ComparableJourneys = append(report.ComparableJourneys, journey.ID)
		comparable = append(comparable, counterpart)
		comparableAfter = append(comparableAfter, journey)
	}
	beforeTotals, _, _, _ := aggregate(comparable)
	afterTotals, _, _, _ := aggregate(comparableAfter)

	for _, row := range dimensionRows() {
		beforeValue, beforePresent, derivation := row.Value(beforeTotals)
		afterValue, afterPresent, afterDerivation := row.Value(afterTotals)
		entry := CompareRow{Dimension: strings.TrimSpace(row.Label), Derivation: derivation}
		if afterDerivation == DerivedProxy || derivation == DerivedProxy {
			entry.Derivation = DerivedProxy
		}
		if beforePresent {
			value := beforeValue
			entry.Before = &value
		}
		if afterPresent {
			value := afterValue
			entry.After = &value
		}
		if beforePresent && afterPresent {
			delta := afterValue - beforeValue
			entry.Delta = &delta
		}
		report.Dimensions = append(report.Dimensions, entry)
	}

	statuses := map[string][2]string{}
	journeyIDs := []string{}
	index := func(results Results, slot int) {
		for _, journey := range results.Journeys {
			current, seen := statuses[journey.ID]
			if !seen {
				journeyIDs = append(journeyIDs, journey.ID)
				current = [2]string{"absent", "absent"}
			}
			current[slot] = journey.Status
			statuses[journey.ID] = current
		}
	}
	index(before, 0)
	index(after, 1)
	sort.Strings(journeyIDs)

	for _, id := range journeyIDs {
		entry := CompareJourney{ID: id, BeforeStatus: statuses[id][0], AfterStatus: statuses[id][1], Cells: map[string]string{}}
		for _, row := range dimensionRows() {
			label := strings.TrimSpace(row.Label)
			entry.Cells[label] = journeyCell(beforeIndex, id, row) + " -> " + journeyCell(afterIndex, id, row)
		}
		report.Journeys = append(report.Journeys, entry)
	}
	sort.Strings(report.ExcludedJourneys)

	report.Notes = append(report.Notes,
		"Dimension totals cover only the journeys that completed in BOTH runs; summing different populations would produce a meaningless delta.",
		"No composite friction score is emitted: a single number can improve while dead ends increase.",
		"`unsup` means the binary lacks that CLI surface. It is never counted as 0 and never included in totals.")
	return report
}

func byID(results Results) map[string]JourneyResult {
	index := map[string]JourneyResult{}
	for _, journey := range results.Journeys {
		index[journey.ID] = journey
	}
	return index
}

func journeyCell(index map[string]JourneyResult, id string, row dimensionRow) string {
	journey, ok := index[id]
	if !ok {
		return "absent"
	}
	return cell(journey, row)
}

func writeCompareReport(out io.Writer, report CompareReport) {
	fmt.Fprintf(out, "gentle-ai-bench comparison — %s mode\n", report.Mode)
	fmt.Fprintf(out, "before: %s\nafter : %s\n", report.Before, report.After)
	fmt.Fprintf(out, "comparable journeys (completed in both): %d\n", len(report.ComparableJourneys))
	if len(report.ExcludedJourneys) > 0 {
		fmt.Fprintf(out, "excluded from the totals: %s\n", strings.Join(report.ExcludedJourneys, ", "))
	}
	fmt.Fprintln(out)

	table := [][]string{{"dimension", "before", "after", "delta", "derivation"}}
	for _, row := range report.Dimensions {
		table = append(table, []string{
			row.Dimension,
			optional(row.Before),
			optional(row.After),
			signed(row.Delta),
			row.Derivation,
		})
	}
	renderTable(out, table)

	fmt.Fprintf(out, "\nPer-journey breakdown (before -> after)\n")
	headers := []string{"journey", "status"}
	labels := []string{}
	for _, row := range dimensionRows() {
		labels = append(labels, strings.TrimSpace(row.Label))
		headers = append(headers, shortLabel(row.Label))
	}
	journeyTable := [][]string{headers}
	for _, journey := range report.Journeys {
		line := []string{journey.ID, journey.BeforeStatus + "->" + journey.AfterStatus}
		for _, label := range labels {
			line = append(line, journey.Cells[label])
		}
		journeyTable = append(journeyTable, line)
	}
	renderTable(out, journeyTable)

	for _, note := range report.Notes {
		fmt.Fprintf(out, "\nnote: %s\n", note)
	}
}

func optional(value *int) string {
	if value == nil {
		return "null"
	}
	return strconv.Itoa(*value)
}

func signed(value *int) string {
	if value == nil {
		return "n/a"
	}
	if *value > 0 {
		return "+" + strconv.Itoa(*value)
	}
	return strconv.Itoa(*value)
}

func renderTable(out io.Writer, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for index, cell := range row {
			if index < len(widths) && len(cell) > widths[index] {
				widths[index] = len(cell)
			}
		}
	}
	for rowIndex, row := range rows {
		parts := []string{}
		for index, cell := range row {
			width := 0
			if index < len(widths) {
				width = widths[index]
			}
			parts = append(parts, cell+strings.Repeat(" ", width-len(cell)))
		}
		fmt.Fprintf(out, "  %s\n", strings.TrimRight(strings.Join(parts, "  "), " "))
		if rowIndex == 0 {
			separators := []string{}
			for _, width := range widths {
				separators = append(separators, strings.Repeat("-", width))
			}
			fmt.Fprintf(out, "  %s\n", strings.Join(separators, "  "))
		}
	}
}
