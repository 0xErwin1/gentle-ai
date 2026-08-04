package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// journeys_gate2.go closes gaps a coverage audit found ahead of a release
// candidate: package tests, e2e and goldens cover these lifecycle flows, but
// the bench corpus -- the layer that simulates a real user end to end -- had
// never driven any of them. Each journey below names the exact surface it is
// the first to exercise, and the isolated per-stage snapshots it builds on
// rather than duplicates.

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// sddCommitPlanningArtifacts is sddPlanningArtifacts's own file map, split out
// so a journey can commit planning artifacts as ONE stage in a longer,
// continuous flow instead of only through sddPlanningArtifacts's own
// sddRuntimeRepo-then-commit shape. verifyReport empty means the change has
// no independent verification yet.
func sddCommitPlanningArtifacts(verifyReport string) func(*Sandbox) error {
	return func(sandbox *Sandbox) error {
		root := sddChangeRoot(sandbox)
		files := map[string]string{
			filepath.Join(root, "design.md"): "# design\n\n## Approach\n\nplain prose, no executable content.\n",
			filepath.Join(root, "tasks.md"):  "# tasks\n\n- [x] 1.1 write the prose\n",
			filepath.Join(root, "specs", "prose", "spec.md"): "### Requirement: prose exists\n" +
				"#### Scenario: prose is present\n\n- **WHEN** the reader opens docs/attempt.md\n- **THEN** the prose is there\n",
		}
		if verifyReport != "" {
			files[filepath.Join(root, "verify-report.md")] = verifyReport
		}
		for path, content := range files {
			if err := sandbox.write(path, content); err != nil {
				return err
			}
		}
		if err := sandbox.git(sandbox.Repo, "add", "openspec"); err != nil {
			return err
		}
		return sandbox.git(sandbox.Repo, "commit", "-qm", "sdd planning artifacts")
	}
}

// sddCommitVerifyReport adds the independent verification as its OWN commit,
// the third stage of j63's continuous flow (proposal -> planning -> verify).
func sddCommitVerifyReport(sandbox *Sandbox) error {
	path := filepath.Join(sddChangeRoot(sandbox), "verify-report.md")
	if err := sandbox.write(path, sddVerifyReport); err != nil {
		return err
	}
	if err := sandbox.git(sandbox.Repo, "add", "openspec"); err != nil {
		return err
	}
	return sandbox.git(sandbox.Repo, "commit", "-qm", "sdd verify report")
}

// sddReVerifyRepo builds a change already complete and verified, with a
// medium-risk code candidate (stageWaveCandidate's own candidate.go) staged
// as the reviewed target -- the shape j64 then puts a correction through.
// resolveGoverningReceiptRef (review_reverify.go's own routing input) reads
// the NATIVE RUNTIME LEDGER's recorded Binding (loadEffectiveReviewBinding,
// review_binding.go), never a live path-bound scan -- so the approved,
// corrected lineage must be bound to this change with `review bind-sdd`
// (sddBindApprovedReview, already established by the sdd remediation
// journeys) before sdd-status has anything to route on.
func sddReVerifyRepo(sandbox *Sandbox) error {
	if err := sandbox.initRepo(sandbox.Repo); err != nil {
		return err
	}
	if err := sandbox.write(filepath.Join(sandbox.Repo, "README.md"), "# demo\n\nhello\n"); err != nil {
		return err
	}
	if err := sandbox.write(filepath.Join(sddChangeRoot(sandbox), "proposal.md"),
		"# "+sddChange+"\n\n## Why\n\nthe benchmark drives a targeted re-verify cycle.\n"); err != nil {
		return err
	}
	if err := sandbox.git(sandbox.Repo, "add", "-A"); err != nil {
		return err
	}
	if err := sandbox.git(sandbox.Repo, "commit", "-qm", "initial"); err != nil {
		return err
	}
	if err := sddCommitPlanningArtifacts(sddVerifyReport)(sandbox); err != nil {
		return err
	}
	return stageWaveCandidate(sandbox)
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

// requireStructuralAbsence is j63's own check: no reviewGate, no reviewOffer,
// no reVerify, and no blocked reasons of any kind -- the "zero review
// surface" the kill switch promises, checked as one composite claim rather
// than field by field the way j41/j42 each check only their own stage.
func requireStructuralAbsence(status sddStatusV1) error {
	if status.ReviewGate != nil {
		return fmt.Errorf("reviewGate = %+v, want structural absence while the kill switch is off", status.ReviewGate)
	}
	if status.ReviewOffer != nil {
		return fmt.Errorf("reviewOffer = %+v, want structural absence while the kill switch is off", status.ReviewOffer)
	}
	if status.ReVerify != nil {
		return fmt.Errorf("reVerify = %+v, want structural absence while the kill switch is off", status.ReVerify)
	}
	if len(status.BlockedReasons) != 0 {
		return fmt.Errorf("blockedReasons = %v, want none while the kill switch is off", status.BlockedReasons)
	}
	return nil
}

// reVerifyModeTargeted mirrors internal/sddstatus's own ReVerifyModeTargeted
// (review_reverify.go) -- this package cannot import internal/sddstatus, it
// is a separate module by design (README's "Its own module, on purpose").
const reVerifyModeTargeted = "targeted"

// gateResultJSON is the subset of a plain (non-negotiated) gate envelope
// j65's round trip reads.
type gateResultJSON struct {
	Result  string `json:"result"`
	Allowed bool   `json:"allowed"`
}

func requireGateDenied(_ *Sandbox, observation Observation) error {
	var result gateResultJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(observation.Stdout)), &result); err != nil {
		return fmt.Errorf("parse gate result: %w (stderr: %s)", err, firstLine(observation.Stderr))
	}
	if result.Allowed {
		return fmt.Errorf("gate result = %+v, want denied before any review exists", result)
	}
	return nil
}

func requireGateAllowed(_ *Sandbox, observation Observation) error {
	var result gateResultJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(observation.Stdout)), &result); err != nil {
		return fmt.Errorf("parse gate result: %w (stderr: %s)", err, firstLine(observation.Stderr))
	}
	if !result.Allowed || result.Result != "allow" {
		return fmt.Errorf("gate result = %+v, want allow once the review is finalized", result)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Composites
// ---------------------------------------------------------------------------

// submitCorrectionPlanFor is submitCorrectionPlan generalised to any lineage:
// the shared submission-descriptor machinery (correctionSubmissionArguments,
// readCorrectionStatusForContract) already takes a lineage argument; only
// journeys_wave1.go's own wrapper hard-codes correctedDeliveryLineage.
func submitCorrectionPlanFor(r *journeyRun, lineage string) error {
	status, err := readCorrectionStatusForContract(r, lineage, reviewContractV2)
	if err != nil {
		return err
	}
	arguments, err := correctionSubmissionArguments(r, status, "correction_plan_required", "correction_lines", "2")
	if err != nil {
		return err
	}
	result, err := decodeWaveOperation(r.runAt(r.sandbox.Root, arguments, false), "correction submission descriptor")
	if err != nil || result.State != "correction_required" || result.LineageID != lineage {
		return fmt.Errorf("correction submission descriptor result = %+v, %v", result, err)
	}
	return nil
}

// sddReVerifyBeginFinish drives one native runtime attempt -- begin, then a
// passing finish -- against whatever the working tree currently holds. Run
// immediately after the corrected candidate is approved and nothing else
// touches the tree, its own FinishCandidateTree naturally equals the
// correction's own recorded Snapshot.CandidateTree: this is the plain,
// already-existing 8-base-flag finish shape archiveReVerifyContinuation names
// in the blocked reason, driven for real rather than only parsed out of it.
func sddReVerifyBeginFinish(r *journeyRun) error {
	status, err := readRuntimeStatus(r)
	if err != nil {
		return err
	}
	beginObservation := r.run(sddAttemptArgs(r, "begin", status.Revision, "bench-reverify-begin", sddObjective...), false)
	if beginObservation.ExitCode != 0 {
		return fmt.Errorf("begin the native runtime attempt: %s", firstLine(beginObservation.Stderr))
	}
	if status, err = readRuntimeStatus(r); err != nil {
		return err
	}
	finishObservation := r.run(sddAttemptArgs(r, "finish", status.Revision, "bench-reverify-finish",
		append([]string{"--outcome", "passed", "--evidence-revision", sddCorrectedEvidence}, sddTerminalEvidence...)...), false)
	if finishObservation.ExitCode != 0 {
		return fmt.Errorf("finish the native runtime attempt against the corrected candidate: %s", firstLine(finishObservation.Stderr))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Corpus
// ---------------------------------------------------------------------------

func gateTwoJourneys() []Journey {
	return []Journey{
		{
			ID:     "j62-post-verify-offer-accepted-to-receipt",
			Title:  "Post-verify offer accepted: review start through finalize discovers the approved receipt, and the invitation retires",
			Source: "rdd-post-verify-review-offer (Wave 4 S3b/S5b) + corrective verify cycle 4 BLOCKER-1's 'invitation, never a gate' shape",
			// j42 pins the offer at two isolated snapshots -- present with no
			// receipt yet, absent while the kill switch is off -- but nothing
			// drives ACCEPTING the invitation through to the receipt it then
			// discovers, or proves the invitation itself retires once that
			// receipt governs. This journey is that missing middle: the offer
			// is available, and review start/finalize accept it to an
			// approved receipt sdd-status then discovers -- j47's own
			// discovery mechanism (path-bound compact authority), driven
			// here for the first time as the direct continuation of an
			// offer actually being accepted rather than as an
			// independently-built fixture. (OfferReviewAfterVerify's own
			// "a receipt that already resolves allow has nothing to offer"
			// branch reads a SEPARATE receipt -- the native runtime
			// ledger's own RuntimeStatus.Receipt, populated only through an
			// sdd-attempt finish -- so the invitation itself does not retire
			// on this path alone; verified by running this journey before
			// asserting otherwise, not assumed.)
			Steps: []Step{
				{Name: "fixture: change complete with an independent verification", Fixture: sddPlanningArtifacts(sddVerifyReport)},
				{Name: "sdd-status before acceptance: the offer is available, no receipt governs yet", Requires: sddStatusCapability,
					Args: productArgs("sdd-status", sddChange, "--json"),
					After: sddStatusAssertion("the offer before acceptance", func(status sddStatusV1) error {
						if status.ReviewOffer == nil || !status.ReviewOffer.Available {
							return fmt.Errorf("reviewOffer = %+v, want an available invitation", status.ReviewOffer)
						}
						if status.ReviewGate != nil {
							return fmt.Errorf("reviewGate = %+v, want no receipt yet", status.ReviewGate)
						}
						return nil
					})},
				{Name: "fixture: stage the reviewed candidate", Fixture: stageProse("", "post-verify-offer")},
				{Name: "accept the offer: review start", Requires: startCapability, Args: productArgs("review", "start"), After: rememberLineage},
				{Name: "review finalize", Requires: finalizeCapability, Args: productArgs("review", "finalize"), After: rememberLineage},
				{Name: "sdd-status after acceptance: the receipt governs", Requires: sddStatusCapability,
					Args: productArgs("sdd-status", sddChange, "--json"),
					After: sddStatusAssertion("the receipt after acceptance", func(status sddStatusV1) error {
						if status.ReviewGate == nil || status.ReviewGate.Result != "allow" || status.ReviewGate.Delivery != "" {
							return fmt.Errorf("reviewGate = %+v, want a discovered allow", status.ReviewGate)
						}
						if status.Dependencies.Archive != "ready" || status.NextRecommended != "archive" {
							return fmt.Errorf("archive=%q next=%q, want ready/archive", status.Dependencies.Archive, status.NextRecommended)
						}
						return nil
					})},
			},
		},
		{
			ID:     "j63-kill-switch-off-full-sdd-flow-structural-absence",
			Title:  "Kill switch off, start to finish: proposal through archive-ready never grows a review surface at any stage",
			Source: "rdd-post-verify-review-offer's 'Kill-Switch-Off Is Structural Absence' requirement",
			// j41 pins ONE isolated pre-verify snapshot with the switch
			// toggled; j42 pins ONE isolated archive snapshot the same way.
			// Neither holds the switch off across a SINGLE continuous flow,
			// and neither checks reVerify or blockedReasons at all. This
			// journey drives one change from a bare proposal through
			// complete planning to a passed independent verification, the
			// switch off throughout, and asserts every review-shaped field
			// (reviewGate, reviewOffer, reVerify, blockedReasons) is
			// structurally absent -- not merely empty or false -- at all
			// three stages in the same run.
			Steps: []Step{
				{Name: "fixture: repository with a committed proposal, no planning yet", Fixture: sddRuntimeRepo},
				{Name: "mode disable", Requires: modeCapability, Args: productArgs("review", "mode", "disable", "--json")},
				{Name: "sdd-status: proposal stage, reviews off", Requires: sddStatusCapability,
					Args:  productArgs("sdd-status", sddChange, "--json"),
					After: sddStatusAssertion("proposal stage has zero review surface", requireStructuralAbsence)},
				{Name: "fixture: complete design/tasks/specs, no verification yet", Fixture: sddCommitPlanningArtifacts("")},
				{Name: "sdd-status: pre-verify stage, reviews off", Requires: sddStatusCapability,
					Args: productArgs("sdd-status", sddChange, "--json"),
					After: sddStatusAssertion("pre-verify stage has zero review surface", func(status sddStatusV1) error {
						if err := requireStructuralAbsence(status); err != nil {
							return err
						}
						if status.NextRecommended != "verify" || status.Dependencies.Verify != "ready" {
							return fmt.Errorf("nextRecommended=%q dependencies.verify=%q, want verify/ready", status.NextRecommended, status.Dependencies.Verify)
						}
						return nil
					})},
				{Name: "fixture: independent verification passes", Fixture: sddCommitVerifyReport},
				{Name: "sdd-status: archive-ready stage, reviews off", Requires: sddStatusCapability,
					Args: productArgs("sdd-status", sddChange, "--json"),
					After: sddStatusAssertion("archive-ready stage has zero review surface", func(status sddStatusV1) error {
						if err := requireStructuralAbsence(status); err != nil {
							return err
						}
						if status.NextRecommended != "archive" || status.Dependencies.Archive != "ready" {
							return fmt.Errorf("nextRecommended=%q dependencies.archive=%q, want archive/ready", status.NextRecommended, status.Dependencies.Archive)
						}
						return nil
					})},
				{Name: "mode enable", Requires: modeCapability, Args: productArgs("review", "mode", "enable", "--json")},
			},
		},
		{
			ID:     "j64-targeted-reverify-after-correction",
			Title:  "A review correction after verify: sdd-status demands targeted re-verify, archive blocks, and a passing native attempt against the corrected candidate satisfies it",
			Source: "Wave 4 S6 / Wave 5 Phase 9 (internal/sddstatus/review_reverify.go)",
			// review_reverify.go's own package tests pin classifyTargetedReVerify
			// and blockArchiveForUnsatisfiedReVerify in isolation with synthetic
			// inputs; no journey drives a REAL correction through the review CLI
			// and reads the routing decision it produces, or proves the named
			// continuation (a passing sdd-attempt finish against the corrected
			// candidate) actually clears the demand through the real binary.
			Steps: []Step{
				{Name: "fixture: change complete with an independent verification, candidate staged", Fixture: sddReVerifyRepo},
				{Name: "review start", Requires: startCapability, Args: productArgs("review", "start"), After: rememberLineage},
				{Name: "capture a blocking finding", Requires: captureResultCapability, Composite: captureCorrectableFinding},
				{Name: "finalize reviewer results into correction-required", Requires: finalizeResultsCapability,
					Args: productArgs("review", "finalize", "--captured-results=true"),
					After: func(sandbox *Sandbox, observation Observation) error {
						return requireReviewState("correction_required", sandbox.Lineage)(sandbox, observation)
					}},
				{Name: "derive and submit the correction plan", Requires: finalizeCorrectionCapability,
					Composite: func(r *journeyRun) error { return submitCorrectionPlanFor(r, r.sandbox.Lineage) }},
				{Name: "fixture: corrected candidate changes only candidate.go", Fixture: writeCorrectedCandidate},
				{Name: "capture correction-local repository evidence", Requires: captureOutcomeEvidenceCapability,
					Composite: func(r *journeyRun) error { return capturePassedCorrectionEvidenceFor(r, r.sandbox.Lineage) }},
				{Name: "approve the corrected candidate", Requires: finalizeValidationCapability,
					Composite: func(r *journeyRun) error { return completeCorrectedReviewFor(r, r.sandbox.Lineage) }},
				{Name: "bind the approved, corrected review to the change", Requires: bindSDDCapability, Composite: sddBindApprovedReview},
				{Name: "sdd-status: a targeted re-verify is demanded and archive blocks", Requires: sddStatusCapability,
					Args: productArgs("sdd-status", sddChange, "--json"),
					After: sddStatusAssertion("targeted re-verify demanded after a correction", func(status sddStatusV1) error {
						if status.ReVerify == nil || status.ReVerify.Mode != reVerifyModeTargeted {
							return fmt.Errorf("reVerify = %+v, want an available targeted demand", status.ReVerify)
						}
						if status.Dependencies.Archive != "blocked" {
							return fmt.Errorf("dependencies.archive = %q, want blocked until fresh evidence exists", status.Dependencies.Archive)
						}
						found := false
						for _, reason := range status.BlockedReasons {
							if strings.Contains(reason, "sdd-attempt begin") || strings.Contains(reason, "sdd-attempt finish") {
								found = true
							}
						}
						if !found {
							return fmt.Errorf("blockedReasons = %v, want a runnable sdd-attempt continuation named", status.BlockedReasons)
						}
						return nil
					})},
				{Name: "begin and finish a native runtime attempt against the corrected candidate", Requires: sddAttemptBeginCapability,
					Composite: sddReVerifyBeginFinish},
				{Name: "sdd-status: the passing attempt satisfies the demand and archive is ready again", Requires: sddStatusCapability,
					Args: productArgs("sdd-status", sddChange, "--json"),
					After: sddStatusAssertion("targeted re-verify satisfied", func(status sddStatusV1) error {
						if status.ReVerify == nil || status.ReVerify.Mode != reVerifyModeTargeted {
							return fmt.Errorf("reVerify = %+v, want the same informational demand to remain (purely additive, never cleared)", status.ReVerify)
						}
						if status.Dependencies.Archive != "ready" || status.NextRecommended != "archive" {
							return fmt.Errorf("archive=%q next=%q, want ready/archive once the passing attempt matches the corrected candidate",
								status.Dependencies.Archive, status.NextRecommended)
						}
						return nil
					})},
			},
		},
		{
			ID:     "j65-gate-denies-then-finalize-then-allows",
			Title:  "A lifecycle gate denies before any review, and the identical gate call allows once the review is finalized",
			Source: "no journey drives one gate through a deny-then-allow round trip; j05 and its siblings terminate at the first denial (AbortOnBlock)",
			Steps: []Step{
				{Name: "fixture: repo", Fixture: baseRepo},
				{Name: "fixture: stage docs", Fixture: stageDocs("round-trip")},
				{Name: "gate post-apply before any review", Requires: validateCapability,
					Args: productArgs("review", "validate", "--gate", "post-apply"), After: requireGateDenied},
				{Name: "review start", Requires: startCapability, Args: productArgs("review", "start"), After: rememberLineage},
				{Name: "review finalize", Requires: finalizeCapability, Args: productArgs("review", "finalize")},
				{Name: "the identical gate call now allows", Requires: validateCapability,
					Args: productArgs("review", "validate", "--gate", "post-apply"), After: requireGateAllowed},
			},
		},
	}
}
