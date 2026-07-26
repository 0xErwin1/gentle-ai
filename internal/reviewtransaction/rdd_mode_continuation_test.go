package reviewtransaction

import (
	"strings"
	"testing"
)

// The kill-switch refusal was the last block a black-box tester still hit with
// no runnable way out: it explains which source keeps reviews off and never
// named the command that turns them back on. Turning reviews off is a
// deliberate choice, so refusing here is correct; naming nothing is not.
func TestRDDDisabledErrorNamesTheCommandThatTurnsItBackOn(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		source RDDModeSource
		want   string
	}{
		{name: "global", source: RDDModeSourceGlobal, want: "gentle-ai review mode enable --scope=global"},
		{name: "clone local", source: RDDModeSourceCloneLocal, want: "gentle-ai review mode enable --scope=clone"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := &RDDDisabledError{Operation: RDDOperationStart, Source: testCase.source}
			if got := err.Error(); !strings.Contains(got, testCase.want) {
				t.Fatalf("refusal names no runnable continuation.\n got: %s\nwant it to contain: %s", got, testCase.want)
			}
		})
	}
}

// The default source expresses no opinion, so it can never be what keeps
// reviews off. Naming a scope there would invent a continuation for a state
// that cannot occur, which is the failure mode these guards exist to prevent.
func TestRDDDisabledErrorInventsNoContinuationForTheDefaultSource(t *testing.T) {
	err := &RDDDisabledError{Operation: RDDOperationStart, Source: RDDModeSourceDefault}
	if got := err.Error(); strings.Contains(got, "review mode enable") {
		t.Fatalf("default source named a continuation it cannot know: %s", got)
	}
}
