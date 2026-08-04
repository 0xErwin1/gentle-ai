package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestReviewNarrationRegistryCoversEveryStopReasonCode is the RED-first
// structural proof for design.md Duty 4 / spec "Three-Tier Narration
// Contract": every reason code review_next_transition.go's
// reviewStopTransition(...) call sites can literally emit (the same
// extraction reviewStopInvariantClassification already proves is exhaustive,
// see review_stop_invariant_test.go) must have a Tier C entry in
// reviewNarrationRegistry, keyed "stop:<code>". A newly discovered stop
// reason code with no narration entry fails this test — the growth rule
// design.md Duty 4 requires.
func TestReviewNarrationRegistryCoversEveryStopReasonCode(t *testing.T) {
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

	for code := range sourceCodes {
		id := "stop:" + code
		emission, registered := reviewNarrationRegistry[id]
		if !registered {
			t.Errorf("stop reason code %q is emitted by review_next_transition.go but has no Tier C entry in reviewNarrationRegistry (key %q)", code, id)
			continue
		}
		if emission.Tier != reviewNarrationTierC {
			t.Errorf("stop reason code %q is registered as tier %q, want Tier C", code, emission.Tier)
		}
		if strings.TrimSpace(emission.Statement) == "" {
			t.Errorf("stop reason code %q has an empty Tier C statement", code)
		}
	}
	for id, emission := range reviewNarrationRegistry {
		if emission.Tier != reviewNarrationTierC || !strings.HasPrefix(id, "stop:") {
			continue
		}
		code := strings.TrimPrefix(id, "stop:")
		if !sourceCodes[code] {
			t.Errorf("reviewNarrationRegistry registers stop reason code %q, which review_next_transition.go no longer emits — remove its entry", code)
		}
	}
}

// TestReviewNarrationNeverClaimsNothingMoreToDo is the RED-first proof that
// the four stop reason codes review_narration.go used to tell a human "there
// is nothing more to do from here" are wrong: `gentle-ai review mode
// disable` is always available (dispatched ahead of the review facade
// specifically to stay reachable while review authority is broken --
// internal/app/app.go's `case "review":` arm; zero required flags; proven
// reachable by review_disabled_reach_test.go), so a human who only wants
// their work delivered always has a next step even when the review itself
// is a dead defect. Any Tier C stop statement that still claims otherwise is
// a false statement, not an acceptable terminal narration.
func TestReviewNarrationNeverClaimsNothingMoreToDo(t *testing.T) {
	const bannedClaim = "nothing more to do"
	for code, statement := range reviewStopReasonNarration {
		lowered := strings.ToLower(statement)
		if strings.Contains(lowered, bannedClaim) {
			t.Errorf("stop reason %q narration still claims %q, which is false: `gentle-ai review mode disable` is always reachable: %q", code, bannedClaim, statement)
		}
	}
}

// TestReviewNarrationNamesAUniversalOrBetterExit is the narration-surface
// sibling of the shipped asset's stop-reason table guard
// (TestEveryReviewStopReasonCodeHasAShippedContinuation in
// review_next_transition_docs_test.go) and of TestCompactBlockedNamesExitForEveryReachableReason
// in internal/sddstatus: every registered Tier C stop statement must either
// name a real `gentle-ai` command / `--flag` of its own, or fall back to the
// universal self-service exit `gentle-ai review mode disable`
// (blocking-budget rule 2: every blocking gate offers a documented
// self-service exit that requires no source-code archaeology). A statement
// naming neither is a dead end on the one human-facing surface stop reason
// codes render to.
func TestReviewNarrationNamesAUniversalOrBetterExit(t *testing.T) {
	namesOtherContinuation := regexp.MustCompile("`gentle-ai [a-z][a-z-]*|`--[a-z][a-z-]*")
	for code, statement := range reviewStopReasonNarration {
		if strings.Contains(statement, reviewConsentOffPathCommand) {
			continue
		}
		if !namesOtherContinuation.MatchString(statement) {
			t.Errorf("stop reason %q narration names no runnable `gentle-ai` command, no `--flag`, and no %q fallback, so this stop reads as a dead end: %q", code, reviewConsentOffPathCommand, statement)
		}
	}
}

// TestNarrationTierAAndCBanInternalVocabulary is the RED-first proof for
// spec "Three-Tier Narration Contract"'s ban on uncertainty phrasing and
// internal identifiers leaking onto the human surface. It runs over every
// registered Tier A and Tier C rendered statement, skipping backtick-quoted
// spans: those are literal, unavoidable CLI command/flag syntax (a public
// contract surface, e.g. `--lineage <id>`), not narrative jargon exposing
// internal architecture concepts — the ban targets prose leaking words like
// "lineage" as a concept, not the flag a caller must literally type. See
// reviewNarrationStripCodeSpans.
func TestNarrationTierAAndCBanInternalVocabulary(t *testing.T) {
	if len(reviewNarrationRegistry) == 0 {
		t.Fatal("reviewNarrationRegistry is empty; nothing to check")
	}
	for id, emission := range reviewNarrationRegistry {
		if emission.Tier != reviewNarrationTierA && emission.Tier != reviewNarrationTierC {
			continue
		}
		prose := reviewNarrationStripCodeSpans(emission.Statement)
		lowered := strings.ToLower(prose)
		for _, word := range reviewNarrationBannedVocabulary {
			if reviewNarrationContainsWord(lowered, word) {
				t.Errorf("narration %q (tier %s) leaks banned internal identifier %q outside a code span: %q", id, emission.Tier, word, emission.Statement)
			}
		}
		if strings.Contains(lowered, "sha256:") {
			t.Errorf("narration %q (tier %s) leaks a raw sha256: digest outside a code span: %q", id, emission.Tier, emission.Statement)
		}
		for _, phrase := range reviewNarrationBannedUncertaintyPhrases {
			if strings.Contains(lowered, phrase) {
				t.Errorf("narration %q (tier %s) uses banned uncertainty phrasing %q: %q", id, emission.Tier, phrase, emission.Statement)
			}
		}
	}
}
