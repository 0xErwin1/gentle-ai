package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
)

// ReviewModeSchema identifies the user-facing kill-switch projection.
const ReviewModeSchema = "gentle-ai.review-mode/v1"

const (
	reviewModeScopeGlobal = "global"
	reviewModeScopeClone  = "clone"
	reviewModeScopeBoth   = "both"
)

// ReviewModeResult reports what the command did and the resulting effective
// mode with both of its sources. It carries no review outcome.
type ReviewModeResult struct {
	Schema    string                          `json:"schema"`
	Operation string                          `json:"operation"`
	Scope     string                          `json:"scope"`
	Status    reviewtransaction.RDDModeStatus `json:"status"`
}

// RunReviewMode is the user-controlled review-driven-development kill switch.
// The global mode lives in uncommitted user state; the clone-local override
// lives under this clone's Git common directory and can only disable. Any off
// wins, status never mutates, and re-enabling applies to future candidates only.
func RunReviewMode(args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprintln(stdout, "Usage: gentle-ai review mode <enable|disable|status> [--cwd <repo>] [--scope <global|clone>] [--expected-revision <revision>] [--json]")
		_, _ = fmt.Fprintln(stdout, "User-owned kill switch. Any off wins: a repository may disable review-driven development for this clone but can never require it, and no other clone inherits the override. status is read-only and reports both sources plus the effective mode. Re-enabling applies to future candidates only.")
		return nil
	}
	operation := args[0]
	switch operation {
	case "enable", "disable", "status":
	default:
		return fmt.Errorf("unknown review mode command %q", operation)
	}

	flags := newReviewFlagSet("review mode "+operation, stdout, "Read or set the user-controlled review-driven-development kill switch.")
	cwd := flags.String("cwd", ".", "repository path")
	scope := flags.String("scope", reviewModeScopeGlobal, "mode source to write: global or clone")
	expectedRevision := flags.String("expected-revision", "", "exact clone-local revision this write replaces")
	emitJSON := flags.Bool("json", false, "emit the machine-readable review mode result")
	if err := parseReviewFlags(flags, args[1:]); err != nil {
		return err
	}
	if reviewHelpRequested(args[1:]) {
		return nil
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected review mode argument %q", flags.Arg(0))
	}
	selectedScope := strings.TrimSpace(*scope)
	switch selectedScope {
	case reviewModeScopeGlobal, reviewModeScopeClone:
	default:
		return fmt.Errorf("unknown review mode scope %q", *scope)
	}
	revisionProvided := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == "expected-revision" {
			revisionProvided = true
		}
	})

	ctx := context.Background()
	result := ReviewModeResult{Schema: ReviewModeSchema, Operation: operation, Scope: selectedScope}
	var err error
	if operation == "status" {
		result.Scope = reviewModeScopeBoth
		result.Status, err = reviewModeStatus(ctx, *cwd)
	} else {
		result.Status, err = applyReviewMode(ctx, *cwd, operation, selectedScope, *expectedRevision, revisionProvided)
	}
	if emitErr := emitReviewMode(stdout, result, *emitJSON); emitErr != nil && err == nil {
		return emitErr
	}
	return err
}

// reviewModeStatus is strictly read-only: it never creates user state and never
// creates repository state.
func reviewModeStatus(ctx context.Context, repo string) (reviewtransaction.RDDModeStatus, error) {
	global, err := readGlobalRDDMode()
	if err != nil {
		return reviewtransaction.RDDModeStatus{Schema: reviewtransaction.RDDModeStatusSchema, Effective: reviewtransaction.RDDModeOff}, err
	}
	return reviewtransaction.ResolveRDDMode(ctx, repo, global)
}

// reviewDeliveryDisposition reports what governs delivery for the candidate
// currently under a lifecycle gate. The kill switch alone never decides: an
// existing receipt keeps governing because disabling freezes authority
// read-only rather than unmaking an approval that is content-bound to exactly
// these bytes.
//
// An unreadable switch is not a disabled switch. When the mode cannot be
// resolved this fails closed to the managed disposition, so a broken or
// tampered mode file can never relax a gate into reporting work as unmanaged by
// choice.
func reviewDeliveryDisposition(ctx context.Context, repo string, receiptPresent bool) reviewtransaction.RDDDelivery {
	status, err := reviewModeStatus(ctx, repo)
	if err != nil {
		if receiptPresent {
			return reviewtransaction.RDDDeliveryReceiptGoverned
		}
		return reviewtransaction.RDDDeliveryUnmanaged
	}
	return reviewtransaction.RDDDeliveryDisposition(status, receiptPresent)
}

// reviewDrivenDevelopmentDisabled reports whether the user's kill switch is off
// for this clone. It is the reach the switch needs outside the review gate:
// every enforcement point that can refuse work on review grounds must be able
// to ask this cheaply, because a switched-off system has no implications.
//
// An unreadable switch is not a disabled switch. It fails closed to "enabled"
// for the same reason reviewDeliveryDisposition does: a broken or tampered mode
// record must never be able to relax an enforcement point.
func reviewDrivenDevelopmentDisabled(ctx context.Context, repo string) bool {
	status, err := reviewModeStatus(ctx, repo)
	if err != nil {
		return false
	}
	return !status.Enabled()
}

func applyReviewMode(
	ctx context.Context,
	repo string,
	operation string,
	scope string,
	expectedRevision string,
	revisionProvided bool,
) (reviewtransaction.RDDModeStatus, error) {
	disabled := reviewtransaction.RDDModeStatus{Schema: reviewtransaction.RDDModeStatusSchema, Effective: reviewtransaction.RDDModeOff}
	if scope == reviewModeScopeGlobal {
		if err := writeGlobalRDDMode(operation); err != nil {
			return disabled, err
		}
		return reviewModeStatus(ctx, repo)
	}
	global, err := readGlobalRDDMode()
	if err != nil {
		return disabled, err
	}
	mode := reviewtransaction.RDDModeOff
	if operation == "enable" {
		// The override is off-only, so enabling clears this clone's opinion
		// instead of asserting on: a repository may never force review on.
		mode = reviewtransaction.RDDModeUnset
	}
	if !revisionProvided {
		current, resolveErr := reviewtransaction.ResolveRDDMode(ctx, repo, global)
		if resolveErr != nil {
			return disabled, resolveErr
		}
		expectedRevision = current.Revision
	}
	return reviewtransaction.SetCloneLocalRDDMode(ctx, repo, mode, expectedRevision, global)
}

func readGlobalRDDMode() (reviewtransaction.RDDGlobalMode, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return reviewtransaction.RDDGlobalMode{}, fmt.Errorf("resolve user home directory: %w", err)
	}
	persisted, err := state.Read(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return reviewtransaction.RDDGlobalMode{}, nil
		}
		return reviewtransaction.RDDGlobalMode{}, fmt.Errorf("read global review mode: %w", err)
	}
	global := reviewtransaction.RDDGlobalMode{Value: persisted.RDDMode}
	if persisted.RDDModeRecordedAt != nil {
		global.RecordedAt = persisted.RDDModeRecordedAt.UTC()
	}
	return global, nil
}

func writeGlobalRDDMode(operation string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home directory: %w", err)
	}
	persisted, err := state.Read(home)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read global review mode: %w", err)
	}
	mode := reviewtransaction.RDDModeOff
	if operation == "enable" {
		mode = reviewtransaction.RDDModeOn
	}
	recorded := time.Now().UTC()
	persisted.RDDMode = string(mode)
	persisted.RDDModeRecordedAt = &recorded
	if err := state.Write(home, persisted); err != nil {
		return fmt.Errorf("persist global review mode: %w", err)
	}
	return nil
}

func emitReviewMode(stdout io.Writer, result ReviewModeResult, emitJSON bool) error {
	if emitJSON {
		payload, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "%s\n", payload)
		return err
	}
	_, err := fmt.Fprintf(
		stdout,
		"review-driven development: %s (decided by %s)\n  global:      %s\n  clone-local: %s\n",
		reviewModeLabel(result.Status.Effective),
		result.Status.Source,
		reviewModeLabel(result.Status.Global),
		reviewModeLabel(result.Status.CloneLocal),
	)
	return err
}

func reviewModeLabel(mode reviewtransaction.RDDMode) string {
	if mode == reviewtransaction.RDDModeUnset {
		return "unset"
	}
	return string(mode)
}

// errReviewDeclinedForCandidate signals internally that the user declined the
// review for this candidate only. Nothing is persisted, so the next candidate
// asks again. It never surfaces as a command error: START reports the decline
// as a typed consent outcome, because a deliberate user choice is not a
// failure — the same principle that keeps disabled/unmanaged delivery a
// reported disposition rather than a veto.
var errReviewDeclinedForCandidate = errors.New("review declined for this candidate")

const (
	reviewConsentAnswerRun    = "1"
	reviewConsentAnswerNotNow = "2"
)

const (
	reviewConsentHeadline = "Gentle AI can review this change before you call it done."
	reviewConsentValue    = "Reviewing takes a bit longer, and it makes the result substantially safer."
	reviewConsentAnswers  = "  1) Run the review now\n  2) Not now, just this once\n"

	// reviewConsentOffPath keeps the permanent disable reachable but deliberate.
	// It is a trailing line rather than a numbered answer because turning a
	// safety net off for good must cost more than pressing a number in a hurry.
	reviewConsentOffPath  = "To turn reviews off for good, run 'gentle-ai review mode disable'.\n"
	reviewConsentQuestion = "Choose 1 or 2 [1]: "

	// reviewConsentSkippedNotice keeps the fail-safe default discoverable: an
	// unanswerable question must never look like a silent yes.
	reviewConsentSkippedNotice = "Gentle AI reviewed this change without asking, because this session has no terminal to answer on. " +
		"Run 'gentle-ai review mode disable' to turn reviews off, or 'gentle-ai review mode status' to see the current setting."
	reviewConsentUnreadableNotice = "Gentle AI could not read an answer, so it reviewed this change and will ask again next time."
	reviewConsentUnknownNotice    = "Gentle AI did not recognize that answer, so it reviewed this change and will ask again next time."

	// reviewConsentDeclinedNotice confirms a decline in the user's own terms.
	// It goes to the console stream, never stdout: stdout stays pure JSON.
	reviewConsentDeclinedNotice = "Review skipped for this candidate at your request. It will be offered again on the next change."
)

// reviewConsentNoticeDirName and reviewConsentNoticeMarkerFile locate the
// once-per-clone marker for reviewConsentSkippedNotice, mirroring the
// existing <GitCommonDir>/gentle-ai/<subdir> convention this package already
// uses for clone-local, uncommitted state (see writeReviewDefectReport).
const (
	reviewConsentNoticeDirName    = "review-mode"
	reviewConsentNoticeMarkerFile = "non-interactive-notice-shown"
)

// reviewConsentNoticeAlreadyShown reports whether this clone already saw
// reviewConsentSkippedNotice. An error resolving the marker is treated as
// "not shown" so a broken marker can never silently suppress the notice.
func reviewConsentNoticeAlreadyShown(ctx context.Context, repo string) (bool, error) {
	path, err := reviewConsentNoticeMarkerPath(ctx, repo)
	if err != nil {
		return false, err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return false, nil
		}
		return false, statErr
	}
	return true, nil
}

// recordReviewConsentNoticeShown persists that this clone saw the notice.
// It is best-effort: a write failure here must never fail review start, the
// same fail-open posture the rest of this consent flow already uses for the
// consent-asked latch and the other console notices.
func recordReviewConsentNoticeShown(ctx context.Context, repo string) error {
	path, err := reviewConsentNoticeMarkerPath(ctx, repo)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644)
}

func reviewConsentNoticeMarkerPath(ctx context.Context, repo string) (string, error) {
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(lease.Identity().GitCommonDir, "gentle-ai", reviewConsentNoticeDirName, reviewConsentNoticeMarkerFile), nil
}

// reviewConsentSession is the console the one-time question uses.
type reviewConsentSession struct {
	Interactive bool
	Input       io.Reader
	Output      io.Writer
}

// reviewConsole is a variable so the question can be driven in tests without a
// real terminal; production always resolves the process console.
var reviewConsole = defaultReviewConsole

// defaultReviewConsole never writes the question to the command's stdout: that
// stream carries the machine-readable review result, and a prompt mixed into it
// would corrupt every caller that parses it.
func defaultReviewConsole() reviewConsentSession {
	return reviewConsentSession{Interactive: reviewConsoleInteractive(), Input: os.Stdin, Output: os.Stderr}
}

// reviewConsoleInteractive requires a real terminal on both ends and no CI
// marker. A question nobody can answer must never become a blocking read.
func reviewConsoleInteractive() bool {
	if strings.TrimSpace(os.Getenv("CI")) != "" {
		return false
	}
	return reviewConsoleTerminal(os.Stdin) && reviewConsoleTerminal(os.Stderr)
}

func reviewConsoleTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// authorizeReviewStart is the single call site where a new review start meets
// the user. The kill switch decides whether a start may run at all, and the
// one-time question is asked here — after the candidate is frozen and the tier
// is classified — so it can state the real reason instead of a generic warning.
func authorizeReviewStart(ctx context.Context, repo string, assessment reviewtransaction.RiskAssessment) error {
	global, err := readGlobalRDDMode()
	if err != nil {
		return err
	}
	if _, err := reviewtransaction.AuthorizeRDDOperation(
		ctx, repo, global, reviewtransaction.RDDOperationStart); err != nil {
		return err
	}
	if assessment.Level == reviewtransaction.RiskLow {
		// Tier 0 is silent structural readback. Asking here would reintroduce
		// exactly the ceremony the silent readback exists to remove.
		return nil
	}
	console := reviewConsole()
	asked, err := reviewtransaction.RDDConsentAsked(ctx, repo)
	if err != nil {
		// A damaged latch must neither block the review nor silently disable it:
		// review the candidate, and say why the question was skipped.
		_, _ = fmt.Fprintf(console.Output, "Gentle AI reviewed this change without asking: %v.\n", err)
		return nil
	}
	if asked {
		return nil
	}
	if !console.Interactive {
		// The notice carries the same information every time (issue #1848), so
		// a clone that already saw it once does not need it again. This is
		// deliberately a separate marker from the RDDConsentAsked latch above:
		// that latch means the one-time consent question is resolved, but a
		// non-interactive run never actually asked anyone, so a later
		// interactive session in this same clone must still be free to ask.
		// An unreadable marker fails open to showing the notice — the first
		// occurrence must never be silently suppressed.
		if shown, shownErr := reviewConsentNoticeAlreadyShown(ctx, repo); shownErr != nil || !shown {
			_, _ = fmt.Fprintln(console.Output, reviewConsentSkippedNotice)
			_ = recordReviewConsentNoticeShown(ctx, repo)
		}
		return nil
	}
	_, _ = fmt.Fprint(console.Output, reviewConsentPrompt(assessment))
	answer, err := readReviewConsentAnswer(console.Input)
	if err != nil {
		// Leaving the latch unset is deliberate: the human never got to answer,
		// so the one-time question must survive for a session that can ask it.
		_, _ = fmt.Fprintln(console.Output, reviewConsentUnreadableNotice)
		return nil
	}
	// The two answers persist asymmetrically. Accepting the review is the safe
	// direction, so it is latched and never asked again. Declining is not: each
	// work unit carries different risk, and today's README says nothing about
	// tomorrow's migration, so the next candidate gets its own question.
	switch answer {
	case reviewConsentAnswerRun:
		return recordReviewConsentAsked(ctx, repo)
	case reviewConsentAnswerNotNow:
		_, _ = fmt.Fprintln(console.Output, reviewConsentDeclinedNotice)
		return fmt.Errorf("%w: the next candidate is asked again", errReviewDeclinedForCandidate)
	default:
		_, _ = fmt.Fprintln(console.Output, reviewConsentUnknownNotice)
		return nil
	}
}

func recordReviewConsentAsked(ctx context.Context, repo string) error {
	if err := reviewtransaction.RecordRDDConsentAsked(ctx, repo); err != nil {
		return fmt.Errorf("record the review question as asked: %w", err)
	}
	return nil
}

// readReviewConsentAnswer reads exactly one line. An empty line accepts the
// offered default, which is the safe direction: review the change.
func readReviewConsentAnswer(input io.Reader) (string, error) {
	if input == nil {
		return "", errors.New("review consent console has no input")
	}
	line, err := bufio.NewReader(input).ReadString('\n')
	answer := strings.TrimSpace(line)
	if err != nil && answer == "" {
		return "", err
	}
	if answer == "" {
		return reviewConsentAnswerRun, nil
	}
	return answer, nil
}

func reviewConsentPrompt(assessment reviewtransaction.RiskAssessment) string {
	return fmt.Sprintf("%s\nWhy: %s\n%s\n%s%s%s",
		reviewConsentHeadline, reviewConsentReason(assessment), reviewConsentValue,
		reviewConsentAnswers, reviewConsentOffPath, reviewConsentQuestion)
}

// reviewConsentMediumReason is the single wording source for the tier-1
// reason: the interactive consent prompt speaks it and the machine-readable
// START result relays it verbatim, so the two surfaces cannot drift.
const reviewConsentMediumReason = "this change is not purely passive documentation, so it gets one consolidated review."

// reviewConsentReason states why this candidate is reviewed, in the user's own
// terms. Both tiers name the evidence that triggered them through the same
// phrasing helper, so neither review's cost is ever unexplained: tier 1 keeps
// the consolidated-review sentence and appends what made the candidate
// non-passive (issue #1827); tier 2 names what triggered the deeper review.
func reviewConsentReason(assessment reviewtransaction.RiskAssessment) string {
	evidence := reviewConsentEvidence(assessment.Reasons)
	if assessment.Level != reviewtransaction.RiskHigh {
		if evidence == "" {
			return reviewConsentMediumReason
		}
		return reviewConsentMediumReason + " The review starts from " + evidence + "."
	}
	if evidence == "" {
		return "this change touches something sensitive, so it gets a deeper review."
	}
	return "this change gets a deeper review because it touches " + evidence + "."
}

func reviewConsentEvidence(reasons []reviewtransaction.RiskReason) string {
	phrases := reviewConsentEvidencePhrases(reasons)
	switch len(phrases) {
	case 0:
		return ""
	case 1:
		return phrases[0]
	case 2:
		return phrases[0] + " and " + phrases[1]
	default:
		// Naming every path would bury the decision the user has to make.
		return fmt.Sprintf("%s, %s, and %d more", phrases[0], phrases[1], len(phrases)-2)
	}
}

// reviewConsentRiskEvidence projects an already-classified assessment into
// the risk_evidence phrases a non-interactive START result carries. It is an
// output-only projection of facts the start already computed: tier 2 names
// the triggering evidence, tier 1 leads with the exact consolidated-review
// reason the consent prompt speaks and appends the same evidence phrases so
// the path that made the candidate non-passive is named (issue #1827), and
// tier 0 stays nil so the omitempty field is absent rather than empty-string
// noise.
func reviewConsentRiskEvidence(assessment reviewtransaction.RiskAssessment) []string {
	switch assessment.Level {
	case reviewtransaction.RiskHigh:
		return reviewConsentEvidencePhrases(assessment.Reasons)
	case reviewtransaction.RiskMedium:
		return append([]string{reviewConsentMediumReason}, reviewConsentEvidencePhrases(assessment.Reasons)...)
	default:
		return nil
	}
}

// reviewConsentEvidencePhrases is the single phrasing source for risk
// evidence: the interactive consent prompt and the machine-readable START
// result both project reasons through it, so the two surfaces cannot drift.
// It returns nil when no reason carries speakable evidence, which keeps the
// omitempty START field absent instead of empty-string noise.
func reviewConsentEvidencePhrases(reasons []reviewtransaction.RiskReason) []string {
	var phrases []string
	for _, reason := range reasons {
		if phrase := reviewConsentEvidencePhrase(reason); phrase != "" {
			phrases = append(phrases, phrase)
		}
	}
	return phrases
}

func reviewConsentEvidencePhrase(reason reviewtransaction.RiskReason) string {
	subject := reviewConsentEvidenceSubject(reason)
	if subject == "" || strings.TrimSpace(reason.Path) == "" {
		return subject
	}
	return fmt.Sprintf("%s in %s", subject, reason.Path)
}

func reviewConsentEvidenceSubject(reason reviewtransaction.RiskReason) string {
	switch reason.Code {
	case reviewtransaction.RiskReasonServiceToken:
		return "service credentials"
	case reviewtransaction.RiskReasonShellSource:
		return "shell scripting"
	case reviewtransaction.RiskReasonProcessBoundary, reviewtransaction.RiskReasonProcessScanLimit:
		return "code that starts other processes"
	case reviewtransaction.RiskReasonExecutableMode:
		return "an executable permission change"
	case reviewtransaction.RiskReasonExecutableChange:
		return "an executable change"
	case reviewtransaction.RiskReasonConfigurationChange:
		return "a configuration change"
	case reviewtransaction.RiskReasonHotPath:
		return reviewConsentSignalSubject(reason.Signal)
	default:
		return ""
	}
}

func reviewConsentSignalSubject(signal reviewtransaction.RiskSignal) string {
	switch signal {
	case reviewtransaction.SignalAuth:
		return "authentication"
	case reviewtransaction.SignalSecurity:
		return "security"
	case reviewtransaction.SignalPayments:
		return "payments"
	case reviewtransaction.SignalDataExposure:
		return "data exposure"
	case reviewtransaction.SignalDataLoss:
		return "data loss"
	case reviewtransaction.SignalPermissions:
		return "permissions"
	case reviewtransaction.SignalUpdate:
		return "the update path"
	case reviewtransaction.SignalShellProcess:
		return "shell or process execution"
	default:
		return "a sensitive area"
	}
}
