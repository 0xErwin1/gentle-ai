package main

import (
	"encoding/json"
	"os"
	"testing"
)

// fixture is one recorded gentle-ai invocation from testdata/observations.json.
// The file was produced by driving a real gentle-ai binary; nothing in it is
// hand-written prose, so the classifier is tested against bytes the product
// actually emits rather than against what a test author imagines it emits.
type fixture struct {
	Name string `json:"name"`
	Observation
}

func loadFixtures(t *testing.T) map[string]Observation {
	t.Helper()
	content, err := os.ReadFile("testdata/observations.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var records []fixture
	if err := json.Unmarshal(content, &records); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	index := map[string]Observation{}
	for _, record := range records {
		index[record.Name] = record.Observation
	}
	if len(index) == 0 {
		t.Fatal("fixture file is empty")
	}
	return index
}

// TestClassifyRecordedObservations pins the classifier to output the product
// really emitted, so the in_band / out_of_band split is decided by bytes rather
// than by what a test author imagines a refusal looks like.
//
// It guards the classifier, NOT the product. The fixtures are frozen, so this
// test cannot notice a gentle-ai that stops naming a runnable continuation; it
// would keep passing against the recording. Several fixtures below already
// describe blocks the product no longer has, and they are kept on purpose: the
// classifier still has to recognise that shape if it ever returns. What guards
// the product is the bench run itself, plus the CLI tests that take a command
// out of an emitted message, dispatch it, and require the block to clear.
//
// So when a refusal is fixed, do not update these fixtures to match. Add the
// fixed shape as a new case if it is a shape the classifier has not seen.
func TestClassifyRecordedObservations(t *testing.T) {
	fixtures := loadFixtures(t)

	cases := []struct {
		name string
		want string
		why  string
	}{
		{"low_risk_start", NotABlock, "exit 0 and no denial"},
		{"low_risk_finalize", NotABlock, "exit 0 and no denial"},
		{"gate_allow", NotABlock, "allowed: true"},
		{"status_collect", NotABlock, "read-only status, exit 0"},
		{"mode_disable", NotABlock, "the kill switch turning off is not a block"},
		{"high_risk_start", NotABlock, "the consent-skipped notice is a prompt, not a block"},
		// These two are one letter apart in the envelope and opposite in
		// meaning, which is exactly why both are pinned. The first hands
		// delivery to ordinary repository policy and stops nothing; counting
		// it would have reported the kill switch working as friction it
		// caused. The second is the switch ON with no receipt, where the
		// operator really is stopped.
		{"gate_disabled_unmanaged", NotABlock,
			"delivery is disabled/unmanaged and action is repository-policy, so the commit proceeds"},
		{"gate_unmanaged_switch_on", BlockInBand,
			"exit 1 with no delivery disposition, and stderr names `gentle-ai review start`"},

		{"finalize_without_evidence", BlockInBand,
			"stderr names `gentle-ai review capture-evidence` and the follow-up finalize"},
		{"invalid_flag_combination", BlockInBand,
			"stderr names both runnable escapes verbatim"},

		// The three marked [fixed] recorded a block the product has since
		// stopped producing. They stay because the classifier must still
		// recognise the shape, and because a recording is the only honest
		// evidence of what the shape looked like.
		{"gate_without_receipt", BlockOutOfBand,
			"[fixed] exit 1, action is explicit-maintainer-action, nothing runnable is named"},
		{"finalize_without_results", BlockOutOfBand,
			"names the requirement but no command that satisfies it"},
		{"rejected_capture", BlockOutOfBand,
			"[fixed] binding_mismatch names no recapture command"},
		{"start_while_disabled", BlockOutOfBand,
			"[fixed] does not name `gentle-ai review mode enable`"},
		{"abandon_without_token", BlockOutOfBand,
			"lists required flags but no runnable command that produces the token"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			observation, ok := fixtures[testCase.name]
			if !ok {
				t.Fatalf("fixture %q missing", testCase.name)
			}
			if got := Classify(observation); got != testCase.want {
				t.Fatalf("Classify(%s) = %q, want %q (%s)\nstderr: %s\nstdout: %s",
					testCase.name, got, testCase.want, testCase.why, observation.Stderr, observation.Stdout)
			}
		})
	}
}

// TestUnsupportedIsNotAPassAndNotABlock is the cross-version guard. An older
// binary that lacks a verb or a flag must record `unsupported`, which is
// neither a pass nor a block, so a comparison can never read "this version
// could not do it" as "this version needed zero".
func TestUnsupportedIsNotAPassAndNotABlock(t *testing.T) {
	fixtures := loadFixtures(t)
	for _, name := range []string{"undefined_flag", "unknown_verb"} {
		observation := fixtures[name]
		if !IsUnsupported(observation) {
			t.Fatalf("%s: IsUnsupported = false, want true\nstderr: %s", name, observation.Stderr)
		}
		if IsBlock(observation) {
			t.Fatalf("%s: IsBlock = true, want false (unsupported must not inflate blocks)", name)
		}
		if got := Classify(observation); got != NotABlock {
			t.Fatalf("%s: Classify = %q, want %q", name, got, NotABlock)
		}
	}
}

func TestHasRunnableCommand(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"backticked command", "capture it first with `gentle-ai review capture-evidence`, then", true},
		{"double quoted command", `rerun with "gentle-ai review start --projection staged"`, true},
		{"bare command", "run gentle-ai review mode disable to turn reviews off", true},
		{"placeholder is not runnable", "validate delivery with gentle-ai review validate --gate <gate>", false},
		{"bare product name", "gentle-ai could not read an answer", true},
		{"product name with no argument", "reported by gentle-ai", false},
		{"no mention", "review finalize requires all 4 original reviewer result(s)", false},
		{"placeholder plus a clean command", "run gentle-ai review status --json or gentle-ai review repair --lineage <id>", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := HasRunnableCommand(testCase.text); got != testCase.want {
				t.Fatalf("HasRunnableCommand(%q) = %v, want %v", testCase.text, got, testCase.want)
			}
		})
	}
}

func TestHasEnvelopeContinuation(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		want   bool
	}{
		{"collect capture operation", `{"next_transition":{"kind":"collect","collect":{"inputs":[{"capture_operation":"review.capture-result"}]}}}`, true},
		{"execute operation", `{"next_transition":{"kind":"execute","execute":{"operation":"review.finalize"}}}`, true},
		{"next_action", `{"next_action":"review.repair"}`, true},
		{"recovery_operation", `{"context":{"recovery_operation":"review.recover"}}`, true},
		{"stop is not a continuation", `{"next_action":"stop"}`, false},
		{"empty is not a continuation", `{"next_action":""}`, false},
		{"action is deliberately not a continuation key", `{"action":"explicit-maintainer-action"}`, false},
		{"not json", "Error: something went wrong", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := HasEnvelopeContinuation(testCase.stdout); got != testCase.want {
				t.Fatalf("HasEnvelopeContinuation(%q) = %v, want %v", testCase.stdout, got, testCase.want)
			}
		})
	}
}

// TestEnvelopeContinuationMakesABlockInBand covers rule 4 in isolation: an
// envelope that names an operation is in-band even when the prose names
// nothing.
func TestEnvelopeContinuationMakesABlockInBand(t *testing.T) {
	observation := Observation{
		ExitCode:       1,
		Stdout:         `{"result":"invalidated","allowed":false,"next_action":"review.repair"}`,
		StdoutCaptured: true,
		StderrCaptured: true,
	}
	if got := Classify(observation); got != BlockInBand {
		t.Fatalf("Classify = %q, want %q", got, BlockInBand)
	}
}

func TestDeclaredDeadEndOnlyAppliesWhenNothingIsNamed(t *testing.T) {
	base := Observation{ExitCode: 1, Stderr: "Error: nothing to do", StdoutCaptured: true, StderrCaptured: true}
	base.DeclaredDeadEnd = true
	if got := Classify(base); got != BlockDeadEnd {
		t.Fatalf("Classify = %q, want %q", got, BlockDeadEnd)
	}

	// A named continuation always wins over the author's declaration: the
	// mechanical evidence outranks the corpus annotation.
	base.Stderr = "Error: rerun gentle-ai review start --projection staged"
	if got := Classify(base); got != BlockInBand {
		t.Fatalf("Classify = %q, want %q", got, BlockInBand)
	}
}

func TestSelfRecoveredWinsOverEverything(t *testing.T) {
	observation := Observation{
		ExitCode:      1,
		Stderr:        "Error: rerun gentle-ai review start",
		SelfRecovered: true,
	}
	if got := Classify(observation); got != BlockSelfRecovered {
		t.Fatalf("Classify = %q, want %q", got, BlockSelfRecovered)
	}
}

func TestIsBlockCoversZeroExitDenials(t *testing.T) {
	cases := []struct {
		name string
		o    Observation
		want bool
	}{
		{"allowed false with exit 0", Observation{Stdout: `{"result":"allow","allowed":false}`}, true},
		{"denial result with exit 0", Observation{Stdout: `{"result":"scope-changed"}`}, true},
		{"stop action with exit 0", Observation{Stdout: `{"action":"stop"}`}, true},
		{"allow with exit 0", Observation{Stdout: `{"result":"allow","allowed":true}`}, false},
		{"non json with exit 0", Observation{Stdout: "ok"}, false},
		{"non zero exit", Observation{ExitCode: 1, Stderr: "Error: nope"}, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := IsBlock(testCase.o); got != testCase.want {
				t.Fatalf("IsBlock = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestManualAuthorizationDetection(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"separate value", []string{"review", "abandon", "--maintainer-authorization", "schema\nlineage=x"}, true},
		{"equals value", []string{"review", "abandon", "--maintainer-authorization=schema\nlineage=x"}, true},
		{"single dash spelling", []string{"review", "abandon", "-maintainer-authorization", "token"}, true},
		{"empty value", []string{"review", "abandon", "--maintainer-authorization", "  "}, false},
		{"flag absent", []string{"review", "abandon", "--lineage", "x"}, false},
		{"trailing flag with no value", []string{"review", "abandon", "--maintainer-authorization"}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := hasManualAuthorization(testCase.args); got != testCase.want {
				t.Fatalf("hasManualAuthorization(%v) = %v, want %v", testCase.args, got, testCase.want)
			}
		})
	}
}

// TestConsentNoticeIsCountedFromRealStderr proves dimension 1 is grounded in
// the product's own string, not a paraphrase.
func TestConsentNoticeIsCountedFromRealStderr(t *testing.T) {
	fixtures := loadFixtures(t)
	if got := countConsentNotices(fixtures["high_risk_start"].Stderr); got != 1 {
		t.Fatalf("consent notices in a high-risk start = %d, want 1\nstderr: %q",
			got, fixtures["high_risk_start"].Stderr)
	}
	if got := countConsentNotices(fixtures["low_risk_start"].Stderr); got != 0 {
		t.Fatalf("consent notices in a low-risk start = %d, want 0", got)
	}
}

func TestCaptureResultDetectionExcludesPreflight(t *testing.T) {
	if !isCaptureResult([]string{"review", "capture-result", "--lens", "review-risk", "--input", "r.json"}) {
		t.Fatal("a real capture must count as a model run")
	}
	if isCaptureResult([]string{"review", "capture-result", "--preflight", "--lens", "review-risk"}) {
		t.Fatal("a preflight reads no reviewer result and must not count as a model run")
	}
	if isCaptureResult([]string{"review", "capture-evidence", "--input", "e.txt"}) {
		t.Fatal("evidence capture is not a reviewer run")
	}
}
