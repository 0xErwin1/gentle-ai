package sddstatus

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// This file is Wave 4 S3's second increment (orchestrator amendment to
// design.md decision 3, 2026-08-03): the post-verify offer routes through
// internal/sddstatus's Resolve()/resolveEngramStatus(), not through
// internal/cli, carried in a new Status.ReviewOffer field emitted exactly
// when verify has passed and the kill switch is on. Kill switch off is
// structural absence: no field on the wire, zero calls into
// reviewEntryHook, not an Available:false placeholder.

// seedVerifiedReadyChangeForOffer seeds a change whose apply is complete,
// whose verify-report already passes, and whose workspace is a real Git
// repository — matching the "post-verify-passed, not-yet-archived" window
// the offer block is scoped to. A real Git repo is required here because
// the pre-existing (unrelated to this slice) post-verify archive gate
// (applyReviewGate/resolveReviewAuthority) needs one to classify "no review
// authority" as Absent/unmanaged rather than as a resolution error.
func seedVerifiedReadyChangeForOffer(t *testing.T, root string) {
	t.Helper()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	write(t, changeRoot+"/verify-report.md", boundedVerifyEnvelope(shaID("a"), "pass"))
	runSDDStatusGit(t, root, "init", "-q")
	runSDDStatusGit(t, root, "config", "user.email", "status@example.com")
	runSDDStatusGit(t, root, "config", "user.name", "Status Test")
	runSDDStatusGit(t, root, "add", ".")
	runSDDStatusGit(t, root, "commit", "-qm", "base")
}

// TestReviewOfferBlockPresentWhenVerifiedAndEnabled proves the offer block
// appears, carries an actionable invocation, and does not block Archive —
// Archive stays whatever resolveDependencies already computed for a passing
// verify report (Ready), never gated on the offer.
func TestReviewOfferBlockPresentWhenVerifiedAndEnabled(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("a"), "pass"))
	// writeApprovedReviewArtifacts also satisfies the pre-existing (unrelated
	// to this slice) post-verify archive gate, so this fixture proves the
	// offer coexists peacefully with an already-archive-ready state — the
	// offer never depends on, and never disturbs, that gate's own verdict.
	writeApprovedReviewArtifacts(t, changeRoot)

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Dependencies.Verify != DependencyAllDone {
		t.Fatalf("Dependencies.Verify = %q, want all_done (fixture must be past verify)", status.Dependencies.Verify)
	}
	if status.Dependencies.Archive != DependencyReady {
		t.Fatalf("Dependencies.Archive = %q, want ready", status.Dependencies.Archive)
	}
	if status.ReviewOffer == nil {
		t.Fatal("ReviewOffer = nil, want a present offer block once verify has passed with the switch on")
	}
	if status.ReviewOffer.Invocation == "" || !strings.Contains(status.ReviewOffer.Invocation, "review start") {
		t.Fatalf("ReviewOffer.Invocation = %q, want an actionable `gentle-ai review start` command", status.ReviewOffer.Invocation)
	}
}

// TestReviewOfferBlockAbsentStructurallyWhenDisabled proves the amendment's
// core requirement at BOTH the Go-value level and the serialized-JSON
// level: with the kill switch off, ReviewOffer is nil, the "reviewOffer"
// key never appears in the marshaled output, and reviewEntryHook fires
// zero times.
func TestReviewOfferBlockAbsentStructurallyWhenDisabled(t *testing.T) {
	root := t.TempDir()
	seedVerifiedReadyChangeForOffer(t, root)
	resetReviewEntryHookCallCountForTest()

	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin", ReviewDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if status.ReviewOffer != nil {
		t.Fatalf("ReviewOffer = %#v, want nil — structural absence when the switch is off", status.ReviewOffer)
	}
	if got := reviewEntryHookCallCountForTest(); got != 0 {
		t.Fatalf("reviewEntryHook fired %d times with the switch off, want 0", got)
	}

	payload, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "reviewOffer") {
		t.Fatalf("serialized status contains a \"reviewOffer\" key with the switch off:\n%s", payload)
	}
}

// TestReviewOfferJourneyFiresZeroTimesAcrossFullFlowWhenDisabled is the
// OFF-mode absence journey (task 4.7): drives the same apply -> verify ->
// (archive-pending) sequence a real status/continue loop would produce, and
// asserts reviewEntryHook never fires anywhere in it. Realized as an
// in-process Go test rather than a bench/ black-box journey because
// reviewEntryHook is a Go-internal instrumentation point a separate bench
// subprocess cannot observe — the same zero-cost-by-default proof shape as
// Wave 1's shadowObserverCallCountForTest, applied to this door.
func TestReviewOfferJourneyFiresZeroTimesAcrossFullFlowWhenDisabled(t *testing.T) {
	root := t.TempDir()
	resetReviewEntryHookCallCountForTest()

	// Apply phase: core artifacts done, tasks pending, then complete.
	changeRoot := seedReadyChange(t, root, "thin", "- [ ] 1.1 Work\n")
	if _, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin", ReviewDisabled: true}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(changeRoot, "tasks.md"), "- [x] 1.1 Work\n")
	if _, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin", ReviewDisabled: true}); err != nil {
		t.Fatal(err)
	}

	// Verify phase: report lands passing. A real Git repo is required for the
	// pre-existing (unrelated) post-verify archive gate to classify "no
	// review authority" as Absent/unmanaged rather than a resolution error.
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("a"), "pass"))
	runSDDStatusGit(t, root, "init", "-q")
	runSDDStatusGit(t, root, "config", "user.email", "status@example.com")
	runSDDStatusGit(t, root, "config", "user.name", "Status Test")
	runSDDStatusGit(t, root, "add", ".")
	runSDDStatusGit(t, root, "commit", "-qm", "base")
	status, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin", ReviewDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if status.Dependencies.Archive != DependencyReady {
		t.Fatalf("Dependencies.Archive = %q, want ready — ordinary archive-pending window", status.Dependencies.Archive)
	}
	if status.ReviewOffer != nil {
		t.Fatalf("ReviewOffer = %#v, want nil throughout the disabled journey", status.ReviewOffer)
	}

	if got := reviewEntryHookCallCountForTest(); got != 0 {
		t.Fatalf("reviewEntryHook fired %d times across the full apply->verify->archive-pending journey with the switch off, want 0", got)
	}
}

// TestReviewOfferDeclineLeavesNoStateAndDoesNotSuppressLaterOffer proves
// 4.5's decline semantics: a decline is scoped to one candidate, persists
// no decision, and does not suppress the offer for a later status read of
// the same still-unarchived candidate. Archive proceeds under ordinary
// repository policy regardless — there is no new state and no new
// persistence to represent "declined".
func TestReviewOfferDeclineLeavesNoStateAndDoesNotSuppressLaterOffer(t *testing.T) {
	root := t.TempDir()
	seedVerifiedReadyChangeForOffer(t, root)

	first, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ReviewOffer == nil {
		t.Fatal("first ReviewOffer = nil, want present")
	}

	// Decline: nothing is written, nothing is called — the orchestrator
	// simply does not act on the offer and asks status again later. Archive
	// eligibility is governed entirely by the pre-existing (unrelated to
	// this slice) post-verify archive gate, whatever it currently reports;
	// what this test proves is that a decline changes NOTHING about it
	// (no new state, no new persistence) and does not suppress the offer.
	second, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ReviewOffer == nil {
		t.Fatal("second ReviewOffer = nil, want the offer to reappear — a decline must not suppress it")
	}
	if second.Dependencies.Archive != first.Dependencies.Archive {
		t.Fatalf("second Dependencies.Archive = %q, want unchanged from first read %q — a decline records no state", second.Dependencies.Archive, first.Dependencies.Archive)
	}
}

// TestReviewOfferDeclineStatusByteIdenticalToOffOutsideOfferBlock is task
// 4.8's integration proof, realized as a same-fixture double-eval (the
// only valid shape for a byte-equivalence claim: evaluated twice, only the
// kill-switch toggle changed — internal/cli/review_new_lineage_switch_off_
// golden_test.go and the pre-existing archive-gate byte-equivalence tests
// use the same discipline). A decline — the switch on, the offer present,
// nothing ever acted on — must leave the rest of the status output
// byte-identical to the switch-off case for the identical underlying
// repository state: decline records nothing that OFF would not also
// produce.
func TestReviewOfferDeclineStatusByteIdenticalToOffOutsideOfferBlock(t *testing.T) {
	root := t.TempDir()
	changeRoot := seedReadyChange(t, root, "thin", "- [x] 1.1 Done\n")
	write(t, filepath.Join(changeRoot, "verify-report.md"), boundedVerifyEnvelope(shaID("a"), "pass"))
	writeApprovedReviewArtifacts(t, changeRoot)

	declined, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin"})
	if err != nil {
		t.Fatal(err)
	}
	if declined.ReviewOffer == nil {
		t.Fatal("declined.ReviewOffer = nil, want present before the decline is applied to this comparison")
	}
	off, err := Resolve(ResolveOptions{CWD: root, ChangeName: "thin", ReviewDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if off.ReviewOffer != nil {
		t.Fatalf("off.ReviewOffer = %#v, want nil", off.ReviewOffer)
	}

	// The offer block is the one and only expected divergence; strip it from
	// the declined side before the byte comparison.
	declined.ReviewOffer = nil
	declinedJSON, err := json.MarshalIndent(declined, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	offJSON, err := json.MarshalIndent(off, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(declinedJSON) != string(offJSON) {
		t.Fatalf("declined status (offer block stripped) diverges from the switch-off status for the same fixture:\ndeclined:\n%s\noff:\n%s", declinedJSON, offJSON)
	}
}
