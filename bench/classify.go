package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Observation is everything the benchmark can see about ONE gentle-ai
// invocation, from either the driven runner or a recorded agent session.
// It is deliberately the process boundary and nothing more: the classifier
// must work against any build of the product, including old releases.
type Observation struct {
	Args     []string `json:"args"`
	ExitCode int      `json:"exit_code"`
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`

	// StdoutCaptured / StderrCaptured are false when the stream was a
	// terminal and the recording shim passed it through untouched rather
	// than teeing it (teeing a TTY would change the product's own
	// interactivity detection and void every measurement).
	StdoutCaptured bool `json:"stdout_captured"`
	StderrCaptured bool `json:"stderr_captured"`

	// DeclaredDeadEnd is the ONE author-declared input to classification.
	// "No continuation exists at all" cannot be decided from outside the
	// binary, so the journey corpus states it explicitly and the README
	// says so. Everything else below is mechanical.
	DeclaredDeadEnd bool `json:"declared_dead_end,omitempty"`

	// SelfRecovered marks a block after which the flow continued with no
	// extra command. The driven runner sets it when a composite step
	// absorbed the failure internally without issuing a recovery command.
	SelfRecovered bool `json:"self_recovered,omitempty"`
}

// Block classes. Exactly one applies to any block.
const (
	BlockSelfRecovered = "self_recovered"
	BlockInBand        = "in_band"
	BlockOutOfBand     = "out_of_band"
	BlockDeadEnd       = "dead_end"
	NotABlock          = ""
)

// productName is the literal a suggested command must begin with.
const productName = "gentle-ai"

// commandStart matches the product name where a command could begin: at the
// start of a line, or after whitespace or an opening quote/bracket, and
// followed by a delimiter rather than more word characters.
var commandStart = regexp.MustCompile("(?:^|[\\s'\"`(\\[])gentle-ai(?:[\\s'\"`)\\]]|$)")

// placeholderRun matches an unfilled template argument such as <gate>.
// A command the user still has to fill in is not runnable, so it does not
// make a block in-band on its own.
var placeholderRun = regexp.MustCompile(`<[^>\s]*>`)

// continuationKeys are the JSON envelope keys that name a runnable follow-up
// operation. They are looked up recursively so the rule survives envelope
// nesting changes across versions.
var continuationKeys = map[string]bool{
	"next_action":        true,
	"recovery_operation": true,
	"capture_operation":  true,
}

// nonContinuationValues are values that explicitly mean "there is nothing to
// run next".
var nonContinuationValues = map[string]bool{
	"":     true,
	"stop": true,
	"none": true,
	"n/a":  true,
}

// HasRunnableCommand reports whether the emitted text names a literal,
// immediately runnable gentle-ai command: the product name, at least one
// argument after it, and no unfilled <placeholder>.
//
// Each occurrence of the product name opens a candidate command that ends at
// the end of the line, at a closing quote or bracket, or where the next
// occurrence begins. That keeps two suggestions on one line independent, so a
// line offering both a clean command and a templated one still counts as
// in-band on the strength of the clean one.
func HasRunnableCommand(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		positions := commandStart.FindAllStringIndex(line, -1)
		for index, position := range positions {
			end := len(line)
			if index+1 < len(positions) {
				end = positions[index+1][0]
			}
			// The match includes the surrounding delimiters; the argument
			// list starts immediately after the product name itself.
			offset := strings.Index(line[position[0]:position[1]], productName)
			tailStart := position[0] + offset + len(productName)
			if commandTailIsRunnable(line[tailStart:end]) {
				return true
			}
		}
	}
	return false
}

// commandTailIsRunnable reports whether what follows the product name is a
// usable argument list.
func commandTailIsRunnable(tail string) bool {
	arguments := 0
	for _, token := range strings.Fields(tail) {
		if cut := strings.IndexAny(token, "'\"`)]"); cut >= 0 {
			token = token[:cut]
			if token == "" {
				break
			}
			if placeholderRun.MatchString(token) {
				return false
			}
			arguments++
			break
		}
		if placeholderRun.MatchString(token) {
			return false
		}
		arguments++
	}
	return arguments > 0
}

// HasEnvelopeContinuation reports whether a JSON envelope on stdout carries a
// next_action / recovery_operation / collect.capture_operation naming a real
// operation. next_transition.execute.operation is treated the same way: it is
// the execute-shaped sibling of collect.capture_operation.
func HasEnvelopeContinuation(stdout string) bool {
	var envelope any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &envelope); err != nil {
		return false
	}
	return walkContinuation(envelope, "")
}

func walkContinuation(node any, parentKey string) bool {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if continuationKeys[key] || (parentKey == "execute" && key == "operation") {
				if text, ok := value.(string); ok && !nonContinuationValues[strings.ToLower(strings.TrimSpace(text))] {
					return true
				}
			}
			if walkContinuation(value, key) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if walkContinuation(item, parentKey) {
				return true
			}
		}
	}
	return false
}

// denialResults are gate results that deny delivery. A denial is a block even
// when the process exits 0, because the flow cannot proceed.
var denialResults = map[string]bool{
	"invalidated":   true,
	"scope-changed": true,
	"deny":          true,
	"denied":        true,
	"stop":          true,
	"blocked":       true,
	"corrupted":     true,
}

// IsBlock reports whether an invocation stopped the flow: a non-zero exit, or
// an envelope that denies.
func IsBlock(o Observation) bool {
	if IsUnsupported(o) {
		// A CLI surface this build does not have is recorded as
		// `unsupported`, never as a block and never as a pass.
		return false
	}
	if o.ExitCode != 0 {
		return true
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(o.Stdout)), &envelope); err != nil {
		return false
	}
	if allowed, ok := envelope["allowed"].(bool); ok && !allowed {
		return true
	}
	if result, ok := envelope["result"].(string); ok && denialResults[strings.ToLower(strings.TrimSpace(result))] {
		return true
	}
	if action, ok := envelope["action"].(string); ok && strings.EqualFold(strings.TrimSpace(action), "stop") {
		return true
	}
	return false
}

// unsupportedPatterns are the shapes a gentle-ai build emits when the CLI
// surface a step needs does not exist in that build. Matching on output rather
// than on the exit code alone is deliberate: exit codes for "I do not have
// that flag" are not guaranteed to differ from ordinary state failures, and
// counting a missing surface as a state failure would be a flattering lie.
var unsupportedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)unknown [a-z-]+ command "`),
	regexp.MustCompile(`(?i)flag provided but not defined`),
	regexp.MustCompile(`(?i)unknown (flag|shorthand flag|option)`),
	regexp.MustCompile(`(?i)unrecognized (flag|option|argument)`),
	regexp.MustCompile(`(?i)unexpected [a-z ]+ argument "`),
	regexp.MustCompile(`(?i)unknown [a-z-]+ "--`),
	regexp.MustCompile(`(?i)^Error: unknown command`),
}

// IsUnsupported reports whether the binary rejected the SHAPE of the command
// (verb or flag it does not have) rather than the state of the repository.
func IsUnsupported(o Observation) bool {
	text := o.Stdout + "\n" + o.Stderr
	for _, pattern := range unsupportedPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// Classify assigns exactly one class to a block. It is the load-bearing
// function of this benchmark, and it is mechanical on purpose: given the same
// bytes it always returns the same class, so the in_band / out_of_band split
// cannot drift into opinion between two runs or two reviewers.
//
// Order:
//  1. not a block            -> NotABlock
//  2. flow continued anyway  -> self_recovered
//  3. text names a runnable `gentle-ai <verb> ...`  -> in_band
//  4. envelope names an operation                   -> in_band
//  5. corpus declares no continuation exists        -> dead_end
//  6. otherwise                                     -> out_of_band
func Classify(o Observation) string {
	if !IsBlock(o) {
		return NotABlock
	}
	if o.SelfRecovered {
		return BlockSelfRecovered
	}
	if HasRunnableCommand(o.Stdout + "\n" + o.Stderr) {
		return BlockInBand
	}
	if HasEnvelopeContinuation(o.Stdout) {
		return BlockInBand
	}
	if o.DeclaredDeadEnd {
		return BlockDeadEnd
	}
	return BlockOutOfBand
}

// hasManualAuthorization reports whether an argv carried a hand-assembled
// --maintainer-authorization value. Both `--flag value` and `--flag=value`
// spellings count; an empty value does not.
func hasManualAuthorization(args []string) bool {
	const flag = "--maintainer-authorization"
	for index, arg := range args {
		trimmed := strings.TrimPrefix(arg, "-")
		trimmed = "--" + strings.TrimPrefix(trimmed, "-")
		if strings.HasPrefix(trimmed, flag+"=") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, flag+"=")) != ""
		}
		if trimmed == flag && index+1 < len(args) {
			return strings.TrimSpace(args[index+1]) != ""
		}
	}
	return false
}

// consentNotices are the exact stderr strings gentle-ai emits when it would
// have asked the human but had no terminal to ask on. Counting them is how a
// non-TTY run measures "times the flow would stop to ask a human".
var consentNotices = []string{
	"without asking, because this session has no terminal to answer on",
	"could not read an answer, so it reviewed this change",
	"did not recognize that answer, so it reviewed this change",
}

func countConsentNotices(stderr string) int {
	total := 0
	for _, notice := range consentNotices {
		total += strings.Count(stderr, notice)
	}
	return total
}

// isCaptureResult reports whether an argv is a reviewer-result capture that
// actually consumes reviewer output. `--preflight` is excluded: it verifies a
// binding and reads no result, so it is not a model run.
func isCaptureResult(args []string) bool {
	if len(args) < 2 || args[0] != "review" || args[1] != "capture-result" {
		return false
	}
	for _, arg := range args {
		if arg == "--preflight" || arg == "--preflight=true" {
			return false
		}
	}
	return true
}
