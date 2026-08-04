package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/assets"
)

// reviewStopTransitionCallRegexp extracts every literal reason code passed to
// reviewStopTransition(...) in review_next_transition.go. A non-literal call
// (a variable or expression) would not match here and must be converted to a
// literal before this test can see it — that is deliberate: the docs table
// below can only ever cover reason codes it can read as plain text.
var reviewStopTransitionCallRegexp = regexp.MustCompile(`reviewStopTransition\("([a-z_]+)"\)`)

// reviewStopReasonDocsTableHeading marks the start of the docs table this
// test cross-checks. reviewStopReasonDocsTableRowRegexp then extracts the
// reason code named at the start of each row inside that section only —
// docs/review-integration.md contains several other tables whose first
// column is also a single backtick-quoted word (gates, applicability, ...),
// so matching the whole file would false-positive against those.
const reviewStopReasonDocsTableHeading = "### Continue after a stop reason code"

var reviewStopReasonDocsTableRowRegexp = regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|")

// TestEveryReviewStopReasonCodeHasADocsContinuation pins that every stop
// reason code newReviewNextTransition (and its helpers) can emit from
// internal/cli/review_next_transition.go has exactly one row in the
// "Continue after a stop reason code" table in docs/review-integration.md.
// It fails closed in both directions: a reason code added to the Go source
// without a matching docs row, and a docs row naming a reason code the
// source no longer emits — so the table can never silently drift from the
// wire contract it documents.
func TestEveryReviewStopReasonCodeHasADocsContinuation(t *testing.T) {
	source, err := os.ReadFile("review_next_transition.go")
	if err != nil {
		t.Fatal(err)
	}
	matches := reviewStopTransitionCallRegexp.FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		t.Fatal("found no reviewStopTransition(\"...\") call sites in review_next_transition.go; the extraction regexp is stale")
	}
	sourceCodes := map[string]bool{}
	for _, match := range matches {
		sourceCodes[match[1]] = true
	}

	docs, err := os.ReadFile("../../docs/review-integration.md")
	if err != nil {
		t.Fatal(err)
	}
	section := reviewStopReasonDocsSection(t, string(docs))
	rows := reviewStopReasonDocsTableRowRegexp.FindAllStringSubmatch(section, -1)
	if len(rows) == 0 {
		t.Fatal("found no rows in the \"Continue after a stop reason code\" table in docs/review-integration.md; the table heading or row shape moved")
	}
	docCodes := map[string]bool{}
	for _, row := range rows {
		docCodes[row[1]] = true
	}

	for code := range sourceCodes {
		if !docCodes[code] {
			t.Errorf("reason code %q is emitted by review_next_transition.go but has no row in the docs/review-integration.md stop-reason-code table", code)
		}
	}
	for code := range docCodes {
		if !sourceCodes[code] {
			t.Errorf("docs/review-integration.md documents reason code %q, which review_next_transition.go no longer emits", code)
		}
	}
}

// reviewLedgerContractAsset is the shipped, embedded orchestrator contract
// every runtime's rendered review protocol draws from (internal/assets is
// go:embed'd into the binary; docs/ is not — see internal/assets/assets.go).
// A consuming orchestrator can read this file; it cannot read docs/.
const reviewLedgerContractAsset = "skills/_shared/review-ledger-contract.md"

// TestEveryReviewStopReasonCodeHasAShippedContinuation is
// TestEveryReviewStopReasonCodeHasADocsContinuation's sibling for the
// channel an orchestrator can actually read. docs/review-integration.md's
// "Continue after a stop reason code" table is correct and complete, but
// internal/assets/assets.go's go:embed tree does not cover docs/, so nothing
// shipped ever carries it to a consumer that is contractually forbidden from
// routing off anything but the returned next_transition and the shipped
// contract. This test requires the SAME heading and row shape to exist,
// covering the SAME codes, inside the shipped
// internal/assets/skills/_shared/review-ledger-contract.md asset — the one
// file every runtime's rendered orchestrator prompt actually embeds.
func TestEveryReviewStopReasonCodeHasAShippedContinuation(t *testing.T) {
	source, err := os.ReadFile("review_next_transition.go")
	if err != nil {
		t.Fatal(err)
	}
	matches := reviewStopTransitionCallRegexp.FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		t.Fatal("found no reviewStopTransition(\"...\") call sites in review_next_transition.go; the extraction regexp is stale")
	}
	sourceCodes := map[string]bool{}
	for _, match := range matches {
		sourceCodes[match[1]] = true
	}

	contract := assets.MustRead(reviewLedgerContractAsset)
	section := reviewStopReasonDocsSection(t, contract)
	rows := reviewStopReasonDocsTableRowRegexp.FindAllStringSubmatch(section, -1)
	if len(rows) == 0 {
		t.Fatalf("found no rows in the %q table in the shipped %s asset; the contract never embeds the stop-reason continuation table an orchestrator can read", reviewStopReasonDocsTableHeading, reviewLedgerContractAsset)
	}
	assetCodes := map[string]bool{}
	for _, row := range rows {
		assetCodes[row[1]] = true
	}

	for code := range sourceCodes {
		if !assetCodes[code] {
			t.Errorf("reason code %q is emitted by review_next_transition.go but has no row in the shipped %s stop-reason table", code, reviewLedgerContractAsset)
		}
	}
	for code := range assetCodes {
		if !sourceCodes[code] {
			t.Errorf("shipped %s documents reason code %q, which review_next_transition.go no longer emits", reviewLedgerContractAsset, code)
		}
	}

	// The universal self-service exit (blocking-budget rule 2) must be
	// reachable from every row whose continuation names no other runnable
	// `gentle-ai` command, so a terminal stop the product cannot resolve
	// automatically never reads as "nothing more to do" on the one channel
	// the orchestrator is allowed to route from.
	namesOtherContinuation := regexp.MustCompile("`gentle-ai [a-z][a-z-]*|`--[a-z][a-z-]*")
	for _, line := range strings.Split(section, "\n") {
		row := reviewStopReasonDocsTableRowRegexp.FindStringSubmatch(line)
		if row == nil {
			continue
		}
		code := row[1]
		if strings.Contains(line, "gentle-ai review mode disable") {
			continue
		}
		if !namesOtherContinuation.MatchString(line) {
			t.Errorf("shipped %s row for %q names no runnable `gentle-ai` command, no `--flag` to pass on the same invocation, and no `gentle-ai review mode disable` fallback, so this stop reads as a dead end", reviewLedgerContractAsset, code)
		}
	}
}

// reviewStopReasonDocsSection returns the text of the given document
// strictly between the reviewStopReasonDocsTableHeading heading and the next
// heading of the same or a higher level, so the row regexp only ever sees
// this one table. Shared by the docs/review-integration.md check and the
// shipped-asset check; both name their own source in the failure message.
func reviewStopReasonDocsSection(t *testing.T, docs string) string {
	t.Helper()
	start := strings.Index(docs, reviewStopReasonDocsTableHeading)
	if start < 0 {
		t.Fatalf("document is missing the %q heading", reviewStopReasonDocsTableHeading)
	}
	rest := docs[start+len(reviewStopReasonDocsTableHeading):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	if end := strings.Index(rest, "\n### "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}
