package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Wave 6 closure disposition journeys (Slice S5, ds09-ds12)
// ---------------------------------------------------------------------------
//
// The ds06/ds08 journeys above (Wave 2) prove the cardinality-one leaf
// disposition plan black-box. Wave 6 relaxes admission to N>=1 closed-class
// closures with descendant-first ordering, an ordered N-node transaction,
// and forward-only resume (internal/reviewtransaction Slices S1-S3) and a
// negotiated `review status --next-transition` route (Slice S4). These four
// journeys are that relaxation's own exit evidence: ds09 proves a real
// multi-hop (N=3) closure derives and disposes end-to-end; ds10 proves the
// over-collection guard holds for everything NOT in the closure; ds11 proves
// a closure interrupted mid-transaction resumes forward-only through the
// real binary; ds12 proves the negotiated route this axis's ds06/ds08 never
// exercised (they always drove --plan-digest/--inventory-revision straight
// from `review repair --preflight`, never from
// `review status --next-transition`).
//
// The closure shape here is a LINEAR chain (seed -> child -> grandchild),
// not a branching tree: `review recover` refuses a second successor from
// the same predecessor ("recovery predecessor already has successor"), a
// real product constraint discovered while building this fixture — a
// lineage can have at most one direct successor through the CLI, so a
// closure with more than one descendant can only ever be a chain, never a
// fork. This still exercises the multi-hop BFS/DFS walk
// authorityDispositionClosure performs (internal/reviewtransaction Slice
// S1's TestAuthorityDispositionClosureMultiChainAssumption already proves
// that walk correct for exactly this report-edge shape) and the N=3 ordered
// transaction (Slice S2) — only the "branching" framing in the original
// task naming does not exist as a constructible product state.
const (
	closureSeedLineage       = "review-damaged-closure-seed"
	closureChildLineage      = "review-damaged-closure-child"
	closureGrandchildLineage = "review-damaged-closure-grandchild"

	scratchClosureSeedLineage        = "damaged-store/closure-seed-lineage"
	scratchClosureSeedRevision       = "damaged-store/closure-seed-revision"
	scratchClosureChildLineage       = "damaged-store/closure-child-lineage"
	scratchClosureChildRevision      = "damaged-store/closure-child-revision"
	scratchClosureGrandchildRevision = "damaged-store/closure-grandchild-revision"
)

// multiHopClosureFixture builds a real, closed-classified 3-node LINEAR
// closure — predecessor -(forged, invalid)-> seed -(valid)-> child
// -(valid)-> grandchild — plus one unrelated approved witness lineage. It
// mirrors damagedLeafEligibleForDisposition's single-edge shape (ds06),
// extended to Wave 6's N>=2 closure, and damagedEdgePair's own 3-node
// predecessor/middle/successor construction order above (mint everything
// first, damage last).
//
// Every mint happens FIRST, while the whole graph is still valid:
// validateCompactRecoveryEdge runs at write time on `review recover` (file
// doc comment above), so the CLI refuses to mint a new edge from ANY
// predecessor once the graph already holds even one invalid edge elsewhere
// — the seed cannot be damaged before its descendants are minted. Damaging
// the seed LAST necessarily changes the seed's own file bytes (and
// therefore its revision — see damageRecordedReason), which would otherwise
// strand the child's already-recorded predecessor_revision/authorization
// against a predecessor that has since moved; realignRecoveryAuthorization
// re-signs the child's own authorization (never its target_identity, actor,
// or reason — only what actually changed) against the seed's post-damage
// revision so the child's edge stays genuinely valid. Realigning the child
// itself changes the CHILD's own revision, so the grandchild needs the same
// treatment, cascading one hop further — exactly the same cascade
// damagedEdgePair's own comment describes, except kept CONSISTENT at each
// step instead of deliberately left stale. This achieves, through the CLI's
// own write-time validation, the same end state
// internal/reviewtransaction's Go-level fixture reaches directly on disk
// (forgedRecoveryPair + forgedRecoveryDescendant, seed damaged before its
// descendant is even constructed) — the CLI simply requires reaching it in
// the opposite construction order.
func multiHopClosureFixture(sandbox *Sandbox) error {
	if err := approvedPredecessor(sandbox); err != nil {
		return err
	}
	if err := approvedUnrelatedDispositionWitness(sandbox); err != nil {
		return err
	}
	// widenWithProse (LOW risk, no lens capture) rather than widenWithCode:
	// the seed and the child here must become approved authority so each
	// can itself be a recovery predecessor for its descendant (mirroring
	// damagedEdgePair's "middle" lineage, ds01/ds07's own multi-hop shape)
	// — a different need than ds06's non-pristine leaf, which stays
	// "reviewing" forever because it is never itself a predecessor.
	// Pristine state has no bearing on plan derivation/admission/execution
	// (only on SanctionedCompactRecoveryExits' abandon-vs-repair
	// exit-naming, which ds09-ds11 do not exercise), so there is nothing to
	// trade away here.
	if err := mintSuccessor(sandbox, widenWithProse, scratchPredecessor, scratchPredecessorRevision, closureSeedLineage); err != nil {
		return err
	}
	if _, err := fixtureCommand(sandbox, "review", "finalize", "--cwd", sandbox.Repo, "--lineage", closureSeedLineage); err != nil {
		return err
	}
	seedRevision, err := approvedEntryRevision(sandbox, closureSeedLineage)
	if err != nil {
		return err
	}
	sandbox.Scratch[scratchClosureSeedLineage] = closureSeedLineage
	sandbox.Scratch[scratchClosureSeedRevision] = seedRevision

	if err := mintSuccessor(sandbox, stageProse("", "closure-hop-child"), scratchClosureSeedLineage, scratchClosureSeedRevision, closureChildLineage); err != nil {
		return err
	}
	if _, err := fixtureCommand(sandbox, "review", "finalize", "--cwd", sandbox.Repo, "--lineage", closureChildLineage); err != nil {
		return err
	}
	childRevision, err := approvedEntryRevision(sandbox, closureChildLineage)
	if err != nil {
		return err
	}
	sandbox.Scratch[scratchClosureChildLineage] = closureChildLineage
	sandbox.Scratch[scratchClosureChildRevision] = childRevision

	if err := mintSuccessor(sandbox, stageProse("", "closure-hop-grandchild"), scratchClosureChildLineage, scratchClosureChildRevision, closureGrandchildLineage); err != nil {
		return err
	}
	sandbox.Scratch[scratchClosureGrandchildRevision] = sandbox.Scratch[scratchSuccessorRevision]

	// Now, and only now, damage the predecessor->seed edge — child and
	// grandchild already exist as genuinely valid edges.
	damagedSeedRevision, err := damageRecordedReason(sandbox, closureSeedLineage, " (edited after the fact)")
	if err != nil {
		return err
	}
	sandbox.Scratch[scratchClosureSeedRevision] = damagedSeedRevision

	newChildRevision, err := realignRecoveryAuthorization(sandbox, closureChildLineage, closureSeedLineage, damagedSeedRevision)
	if err != nil {
		return err
	}
	sandbox.Scratch[scratchClosureChildRevision] = newChildRevision

	newGrandchildRevision, err := realignRecoveryAuthorization(sandbox, closureGrandchildLineage, closureChildLineage, newChildRevision)
	if err != nil {
		return err
	}
	sandbox.Scratch[scratchClosureGrandchildRevision] = newGrandchildRevision

	return requireClosureShape(sandbox)
}

// approvedEntryRevision reads back one lineage's current revision through
// `review status`, and fails loudly if it is not approved — the same proof
// mintSuccessor itself runs for a freshly recovered successor, reused here
// after finalize.
func approvedEntryRevision(sandbox *Sandbox, lineage string) (string, error) {
	status, err := proveStoreStatus(sandbox)
	if err != nil {
		return "", err
	}
	for _, entry := range status.Entries {
		if entry.LineageID == lineage {
			if entry.State != "approved" {
				return "", fmt.Errorf("fixture claims an approved %q but the product reports %q", lineage, entry.State)
			}
			return entry.Revision, nil
		}
	}
	return "", fmt.Errorf("fixture claims an approved %q but review status does not list it", lineage)
}

// realignRecoveryAuthorization re-signs successorLineage's recorded
// authorization against its predecessor's new (post-damage or
// post-realignment) revision — the successor's own target_identity, actor,
// and reason are read back unchanged from its own record, so this touches
// only the one field that actually changed (predecessor_revision) plus the
// authorization text that binds it, exactly mirroring
// compactRecoveryAuthorizationBinding's own six-field domain (schema,
// predecessor_lineage, predecessor_revision, target_identity, actor,
// reason). Unlike damageAuthorizationPredecessorRevision (which
// deliberately leaves the two inconsistent to simulate drift), this keeps
// them consistent, so the resulting edge is genuinely valid — the whole
// point being that only the seed's OWN incoming edge is invalid, not its
// descendants'.
func realignRecoveryAuthorization(sandbox *Sandbox, successorLineage, predecessorLineage, newPredecessorRevision string) (string, error) {
	path, err := storeStatePath(sandbox, successorLineage)
	if err != nil {
		return "", err
	}
	record, err := loadStoreRecord(path)
	if err != nil {
		return "", err
	}
	// target_identity is not its own field in CompactRecoveryProvenance —
	// it is bound only inside the rendered authorization text (mirrors
	// damageAuthorizationPredecessorRevision above, which treats the whole
	// authorization as a string too, never a target_identity JSON field).
	existingAuthorization, err := record.recoveryString("maintainer_authorization")
	if err != nil {
		return "", err
	}
	targetIdentity, err := authorizationFieldValue(existingAuthorization, "target_identity")
	if err != nil {
		return "", err
	}
	actor, err := record.recoveryString("actor")
	if err != nil {
		return "", err
	}
	reason, err := record.recoveryString("reason")
	if err != nil {
		return "", err
	}
	authorization := strings.Join([]string{
		damagedAuthorizationSchema,
		"predecessor_lineage=" + predecessorLineage,
		"predecessor_revision=" + newPredecessorRevision,
		"target_identity=" + targetIdentity,
		"actor=" + actor,
		"reason=" + reason,
	}, "\n")
	if err := record.setRecoveryString("predecessor_revision", newPredecessorRevision); err != nil {
		return "", err
	}
	if err := record.setRecoveryString("maintainer_authorization", authorization); err != nil {
		return "", err
	}
	return record.save()
}

// authorizationFieldValue extracts one "key=value" line's value from a
// rendered recovery authorization text (the domain-separated multi-line
// format compactRecoveryAuthorizationBinding renders — schema, then one
// "field=value" line per field).
func authorizationFieldValue(authorization, key string) (string, error) {
	prefix := key + "="
	for _, line := range strings.Split(authorization, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix), nil
		}
	}
	return "", fmt.Errorf("recorded authorization does not bind a %q field", key)
}

// requireClosureShape is multiHopClosureFixture's own proof: exactly three
// edges (predecessor->seed, seed->child, child->grandchild), exactly one
// invalid — the seed's own — the other two genuinely valid, and the store
// non-authoritative overall. It is the multi-hop analogue of
// requireInvalidEdges, which requires every edge invalid and cannot express
// a shape with valid descendant edges.
func requireClosureShape(sandbox *Sandbox) error {
	inspection, err := proveInspection(sandbox)
	if err != nil {
		return err
	}
	if inspection.Totals.Edges != 3 || inspection.Totals.InvalidEdges != 1 || inspection.Totals.ValidEdges != 2 {
		return fmt.Errorf("fixture claims a 1-invalid/2-valid three-edge closure shape but inspect-authority reports %+v", inspection.Totals)
	}
	invalidCount := 0
	for _, edge := range inspection.Edges {
		if !edge.Valid {
			invalidCount++
			if edge.SuccessorLineageID != closureSeedLineage {
				return fmt.Errorf("fixture claims the seed's own edge is the sole invalid one but %q is invalid instead", edge.SuccessorLineageID)
			}
			if len(edge.AnomalyClasses) != 0 {
				return fmt.Errorf("fixture's invalid edge now classifies as %v; the closure derivation this journey measures needs it outside every anomaly class", edge.AnomalyClasses)
			}
		}
	}
	if invalidCount != 1 {
		return fmt.Errorf("fixture claims exactly one invalid edge, inspect-authority reports %d", invalidCount)
	}
	return requireStoreNotAuthoritative(sandbox)
}

// closureMemberLineages is the fixture's whole closure, in construction
// (ancestor-first) order — the reverse of ordered_closure, which is
// descendant-first.
var closureMemberLineages = []string{closureSeedLineage, closureChildLineage, closureGrandchildLineage}

// requireDispositionClosureFullyQuarantined proves the WHOLE closure
// disposed, not only the seed: `review repair`'s committed response only
// ever names the seed's own LineageID (the returned record is the seed's),
// so the full-closure claim needs this direct filesystem check — the
// seed's, child's, and grandchild's own v2/ store directories must all be
// gone.
func requireDispositionClosureFullyQuarantined(r *journeyRun) error {
	for _, lineage := range closureMemberLineages {
		dir, err := storeLineageDir(r.sandbox, lineage)
		if err != nil {
			return err
		}
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			return fmt.Errorf("closure member %q was not quarantined: its v2/ directory still exists", lineage)
		}
	}
	return nil
}

// requireUnrelatedPredecessorByteIdentical is ds10's second half of the
// over-collection guard, alongside requireDispositionWitnessBytesUnchanged:
// the top-level predecessor is not itself a closure member (only its
// outgoing edge into the seed is), so its own store bytes must never move
// either.
func requireUnrelatedPredecessorByteIdentical(r *journeyRun) error {
	lineage, err := scratchValue(r.sandbox, scratchPredecessor)
	if err != nil {
		return err
	}
	path, err := storeStatePath(r.sandbox, lineage)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		return fmt.Errorf("the unrelated predecessor %q was itself moved during closure disposition: %w", lineage, statErr)
	}
	return nil
}

// reviewTransactionsBase resolves the same base directory
// internal/reviewtransaction's reviewAuthorityRoot derives — the git common
// directory's gentle-ai/review-transactions subtree — so ds11 can author a
// crash-position quarantine state directly, exactly like this axis's other
// fixtures author review-state.json directly (file doc comment above): this
// state (a real forward-only resume mid-transaction) is unreachable through
// the CLI, because a clean `review repair` run never stops halfway.
func reviewTransactionsBase(sandbox *Sandbox) (string, error) {
	common, err := gitCommonDir(sandbox, sandbox.Repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(common, "gentle-ai", "review-transactions"), nil
}

// authorCommittedClosureMemberQuarantine directly authors the on-disk state
// production's own quarantineCompactStoreEntry leaves behind for one
// COMMITTED closure member — a real crash-position fixture, matching this
// axis's established convention for states the CLI cannot reach (no live
// process can be interrupted from a separate, black-box test binary). It
// moves lineage's whole v2/ entry into quarantine/<lineage>-crash/residue/
// and writes a reclaim-record.json byte-structurally identical to
// reviewtransaction.CompactReclaimRecord/AuthorityDispositionProof's own
// JSON shape, so discoverAuthorityDispositionRecord (which only compares
// LineageID and AuthorityDisposition.PlanDigest) accepts it as already
// committed on the very next `review repair` invocation.
func authorCommittedClosureMemberQuarantine(sandbox *Sandbox, lineage, planDigest, inventoryRevision, authorization string, closure []string, expectedRevisions map[string]string) error {
	base, err := reviewTransactionsBase(sandbox)
	if err != nil {
		return err
	}
	sourceDir := filepath.Join(base, "v2", lineage)
	items, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("read closure member %q before authoring its crash-position quarantine: %w", lineage, err)
	}
	residue := make([]string, 0, len(items))
	for _, item := range items {
		residue = append(residue, item.Name())
	}

	quarantineDir := filepath.Join(base, "quarantine", lineage+"-crash")
	if err := os.MkdirAll(quarantineDir, 0o755); err != nil {
		return err
	}
	if err := os.Rename(sourceDir, filepath.Join(quarantineDir, "residue")); err != nil {
		return fmt.Errorf("author crash-position quarantine for %q: %w", lineage, err)
	}

	sum := sha256.Sum256([]byte(authorization))
	authorizationSHA256 := "sha256:" + hex.EncodeToString(sum[:])

	record := map[string]any{
		"schema":          "gentle-ai.review-reclaim-record/v1",
		"status":          "committed",
		"lineage_id":      lineage,
		"reason":          "bench-authored crash-position fixture (ds11)",
		"actor":           "bench",
		"reclaimed_at":    time.Now().UTC().Format(time.RFC3339Nano),
		"source_path":     sourceDir,
		"quarantine_path": quarantineDir,
		"residue":         residue,
		"authority_disposition": map[string]any{
			"schema":                       "gentle-ai.review-authority-disposition-proof/v1",
			"plan_digest":                  planDigest,
			"authority_inventory_revision": inventoryRevision,
			"anomaly_class":                contentMismatchedRecoveryAuthorizationClass,
			"ordered_seed_set":             []string{closureSeedLineage},
			"ordered_closure":              closure,
			"expected_revisions":           expectedRevisions,
			"authorization_sha256":         authorizationSHA256,
		},
	}
	payload, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(quarantineDir, "reclaim-record.json"), append(payload, '\n'), 0o644)
}

// closureExpectedRevisions publishes the closure's ExpectedRevisions map —
// the current revision of every closure member, exactly what a real
// AuthorityDispositionPlan derivation would bind — which ds11 needs to
// author a structurally faithful crash-position quarantine record.
func closureExpectedRevisions(sandbox *Sandbox) (map[string]string, error) {
	seedRevision, err := scratchValue(sandbox, scratchClosureSeedRevision)
	if err != nil {
		return nil, err
	}
	childRevision, err := scratchValue(sandbox, scratchClosureChildRevision)
	if err != nil {
		return nil, err
	}
	grandchildRevision, err := scratchValue(sandbox, scratchClosureGrandchildRevision)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		closureSeedLineage:       seedRevision,
		closureChildLineage:      childRevision,
		closureGrandchildLineage: grandchildRevision,
	}, nil
}

// closureOrderedClosure is the fixture's real closure order: descendant
// first (deepest — the grandchild), seed last (rdd-authority-disposition-plan
// / "Deterministic Closure Derivation From the Graph Source of Record").
var closureOrderedClosure = []string{closureGrandchildLineage, closureChildLineage, closureSeedLineage}

// authorCrashAfterFirstDescendantCommitted is ds11's fixture-extension step:
// after `review repair --preflight` publishes the real plan digest and
// inventory revision, it authors the grandchild (the first, deepest closure
// member in descendant-first order) as already committed — a real
// interruption right after the first node of a 3-node closure, the exact
// position internal/reviewtransaction's
// TestAuthorityDispositionResumeCrashPositionMatrix proves converges at the
// unit level; this is that proof's journey-level twin, driven through the
// real binary.
func authorCrashAfterFirstDescendantCommitted(sandbox *Sandbox) error {
	planDigest, err := scratchValue(sandbox, scratchDispositionPlanDigest)
	if err != nil {
		return err
	}
	inventoryRevision, err := scratchValue(sandbox, scratchDispositionInventoryRevision)
	if err != nil {
		return err
	}
	binding, err := dispositionRepositoryBinding(sandbox)
	if err != nil {
		return err
	}
	const actor, reason = "bench", "quarantine the multi-hop closure"
	authorization := dispositionAuthorization(binding, planDigest, inventoryRevision, actor, reason)
	expectedRevisions, err := closureExpectedRevisions(sandbox)
	if err != nil {
		return err
	}
	return authorCommittedClosureMemberQuarantine(sandbox, closureGrandchildLineage, planDigest, inventoryRevision, authorization, closureOrderedClosure, expectedRevisions)
}

// requireClosureMemberAlreadyQuarantined proves the crash-position fixture
// really did author what it claims: exactly one quarantine directory for
// the named lineage exists before the resume step runs, so a subsequent
// double-move (not a skip) would be visible as two.
func requireClosureMemberAlreadyQuarantined(sandbox *Sandbox, lineage string) error {
	base, err := reviewTransactionsBase(sandbox)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(base, "quarantine"))
	if err != nil {
		return err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), lineage+"-") {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("expected exactly one quarantine directory for %q before resume, found %d", lineage, count)
	}
	return nil
}

// requireForgedResumeMovedNothingFurther is fix cycle 1's CRITICAL-1
// (security) journey-level mutation proof: a forged-authorization resume
// attempt against an in-progress closure must refuse through the real
// `review repair` binary before touching anything beyond the crash
// fixture's own pre-authored member — the grandchild's single quarantine
// directory must be unchanged, and child/seed must have none. This is the
// black-box, N=3, real-binary twin of
// TestAuthorityDispositionResumeRefusesForgedAuthorization.
func requireForgedResumeMovedNothingFurther(r *journeyRun) error {
	base, err := reviewTransactionsBase(r.sandbox)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(base, "quarantine"))
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, lineage := range closureMemberLineages {
			if strings.HasPrefix(entry.Name(), lineage+"-") {
				counts[lineage]++
			}
		}
	}
	if counts[closureGrandchildLineage] != 1 {
		return fmt.Errorf("grandchild quarantine directories after a refused forged-authorization resume = %d, want exactly 1 (unchanged from before the attempt)", counts[closureGrandchildLineage])
	}
	for _, lineage := range []string{closureChildLineage, closureSeedLineage} {
		if counts[lineage] != 0 {
			return fmt.Errorf("%q has %d quarantine directories after a refused forged-authorization resume, want 0 — the forged authorization moved something it should have refused", lineage, counts[lineage])
		}
	}
	return nil
}

// requireNoDoubleMoveAcrossClosure is ds11's resume-convergence proof:
// after the resumed `review repair` run, every closure member has exactly
// one quarantine directory — the crash-position fixture's pre-authored one
// for the grandchild was skipped, not re-processed, and child/seed were
// quarantined fresh exactly once each.
func requireNoDoubleMoveAcrossClosure(r *journeyRun) error {
	base, err := reviewTransactionsBase(r.sandbox)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(base, "quarantine"))
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, lineage := range closureMemberLineages {
			if strings.HasPrefix(entry.Name(), lineage+"-") {
				counts[lineage]++
			}
		}
	}
	for _, lineage := range closureMemberLineages {
		if counts[lineage] != 1 {
			return fmt.Errorf("closure member %q has %d quarantine directories after resume, want exactly 1 (a double-move or a skip that never happened)", lineage, counts[lineage])
		}
	}
	return nil
}

// closureDispositionJourneys returns Wave 6 Slice S5's exit-evidence
// journeys (ds09-ds12), appended to the Wave 2 damaged-store corpus above.
func closureDispositionJourneys() []Journey {
	return []Journey{
		{
			ID:     "ds09-multi-chain-closure",
			Title:  "A multi-hop closure — one damaged seed with a two-hop descendant chain — derives and disposes end-to-end",
			Source: "rdd-root-simplification-wave6 Slices S1/S2 (topological ordering, ordered N-node transaction)",
			// ds06 above proves the N=1 base case. This is Wave 6's own
			// answer to the question ds06 could not ask: does the SAME
			// `review repair` verb, unchanged from the operator's
			// perspective, actually dispose a real N=3 closure spanning
			// more than one descendant hop from the seed?
			Steps: []Step{
				{Name: "fixture: one damaged seed with a two-hop descendant chain, plus an unrelated witness lineage",
					Fixture: multiHopClosureFixture},
				{Name: "inspect the authority, which is what an operator does first",
					Requires: inspectAuthorityCapability,
					Args:     productArgs("review", "inspect-authority"),
					After: inspectionAssertion("one edge outside every anomaly class, two valid descendant edges", func(inspection storeInspection) error {
						if inspection.Totals.InvalidEdges != 1 || inspection.Totals.ValidEdges != 2 {
							return fmt.Errorf("invalid_edges=%d valid_edges=%d, want 1 and 2", inspection.Totals.InvalidEdges, inspection.Totals.ValidEdges)
						}
						return nil
					})},
				{Name: "ask what review repair would do", Requires: repairPreflightCapability,
					Args: productArgs("review", "repair", "--preflight=true"), After: requireDispositionPlanEligible},
				{Name: "repair the whole closure through its disposition plan", Requires: repairDispositionExecuteCapability,
					Args: dispositionRepairArgs("quarantine the multi-hop closure"), After: requireDispositionQuarantineCommitted},
				{Name: "the authority graph after repair", Requires: inspectAuthorityCapability,
					Args:  productArgs("review", "inspect-authority"),
					After: inspectionAssertion("the retained graph after a multi-hop closure repair", requireRetainedGraphValid)},
				{Name: "the store governs again", Composite: proveStoreRecovered},
				{Name: "every closure member was quarantined, not only the seed", Composite: requireDispositionClosureFullyQuarantined},
			},
		},
		{
			ID:     "ds10-cross-lineage-closure",
			Title:  "The over-collection guard: everything NOT in the closure — the predecessor and an unrelated lineage — stays byte-identical",
			Source: "rdd-root-simplification-wave6 design decision D6 (over-collection guard)",
			// ds09 proves the closure disposes. This journey isolates the
			// complementary claim design decision D6 makes: closure
			// derivation reaches only report-edge-reachable descendants —
			// the top-level predecessor (upstream of the seed, never a
			// closure member) and a wholly unrelated approved lineage both
			// keep byte-identical store bytes across the exact same
			// disposition.
			Steps: []Step{
				{Name: "fixture: one damaged seed with a two-hop descendant chain, plus an unrelated witness lineage",
					Fixture: multiHopClosureFixture},
				{Name: "ask what review repair would do", Requires: repairPreflightCapability,
					Args: productArgs("review", "repair", "--preflight=true"), After: requireDispositionPlanEligible},
				{Name: "repair the whole closure through its disposition plan", Requires: repairDispositionExecuteCapability,
					Args: dispositionRepairArgs("quarantine the multi-hop closure"), After: requireDispositionQuarantineCommitted},
				{Name: "every closure member was quarantined, not only the seed", Composite: requireDispositionClosureFullyQuarantined},
				{Name: "the unrelated witness lineage never moved", Composite: requireDispositionWitnessBytesUnchanged},
				{Name: "the closure's own predecessor never moved either", Composite: requireUnrelatedPredecessorByteIdentical},
			},
		},
		{
			ID:     "ds11-crash-recovery-mid-closure",
			Title:  "A closure interrupted after its first descendant commits resumes forward-only through the real binary — no double move",
			Source: "rdd-root-simplification-wave6 Slice S3 (forward-only resume) — the journey-level twin of TestAuthorityDispositionResumeCrashPositionMatrix",
			// internal/reviewtransaction's own crash-position matrix proves
			// convergence at EVERY ordered position through direct hook
			// injection (in-process, exhaustive: 6 positions on a 3-node
			// closure). This axis is black-box and out-of-process, so it
			// cannot inject a hook mid-transaction — but it CAN author the
			// exact on-disk state one real interruption leaves behind
			// (this axis's own established convention: "fixtures author
			// review-state.json directly" for states the CLI itself never
			// reaches), and then prove the real `review repair` binary
			// resumes correctly from it. This journey covers the first
			// (deepest-descendant) position; the unit-level matrix already
			// proves every other position converges by the identical
			// mechanism (the same discoverAuthorityDispositionRecord/
			// resumeAuthorityDispositionRecord seam, applied uniformly per
			// node regardless of which node is being resumed).
			Steps: []Step{
				{Name: "fixture: one damaged seed with a two-hop descendant chain, plus an unrelated witness lineage",
					Fixture: multiHopClosureFixture},
				{Name: "ask what review repair would do", Requires: repairPreflightCapability,
					Args: productArgs("review", "repair", "--preflight=true"), After: requireDispositionPlanEligible},
				{Name: "author the crash: the first descendant is already committed, as if a real execution stopped right there",
					Composite: func(r *journeyRun) error {
						if err := authorCrashAfterFirstDescendantCommitted(r.sandbox); err != nil {
							return err
						}
						return requireClosureMemberAlreadyQuarantined(r.sandbox, closureGrandchildLineage)
					}},
				// Fix cycle 1 (CRITICAL-1, security): before Wave 6's fix,
				// validateAuthorityDispositionAuthorization was gated inside
				// a fresh-execution-only branch, so this exact resume shape —
				// a real `review repair` call against an in-progress
				// closure — executed unauthorized regardless of what
				// --authorization it was given. This step submits the CORRECT
				// --plan-digest/--inventory-revision (so CAS and plan-match
				// both pass) but an authorization bound to a repository
				// identity that can never be the real one, mirroring
				// ds08's N=1 forged-authorization journey — the N=3,
				// mid-closure-resume twin.
				{Name: "attempt to resume with an authorization bound to the wrong repository — refused, nothing moves further", Requires: repairDispositionExecuteCapability,
					Args: forgedDispositionRepairArgs("quarantine the multi-hop closure")},
				{Name: "the forged-authorization resume attempt moved nothing beyond the pre-authored member",
					Composite: requireForgedResumeMovedNothingFurther},
				{Name: "resume the interrupted closure with the identical plan", Requires: repairDispositionExecuteCapability,
					Args: dispositionRepairArgs("quarantine the multi-hop closure"), After: requireDispositionQuarantineCommitted},
				{Name: "the authority graph after resume", Requires: inspectAuthorityCapability,
					Args:  productArgs("review", "inspect-authority"),
					After: inspectionAssertion("the retained graph after a resumed multi-hop closure repair", requireRetainedGraphValid)},
				{Name: "every closure member was quarantined exactly once — the pre-authored member was skipped, not re-processed",
					Composite: requireNoDoubleMoveAcrossClosure},
				{Name: "the unrelated witness lineage never moved", Composite: requireDispositionWitnessBytesUnchanged},
			},
		},
		{
			ID:     "ds12-negotiated-transition-route",
			Title:  "The negotiated route: `review status --next-transition` surfaces the closure disposition collect/execute, not a raw flag triad",
			Source: "rdd-root-simplification-wave6 Slice S4/D7 (negotiated transition route)",
			// ds06/ds08/ds09/ds10/ds11 above all drive --plan-digest and
			// --inventory-revision straight from `review repair
			// --preflight`'s dedicated JSON fields — a caller who already
			// knows the disposition surface exists. This journey drives the
			// SAME repair verb through the generic negotiated surface every
			// other lifecycle transition in this product already uses:
			// `review status --next-transition` offers collect{} naming the
			// same two values, then execute{review.repair, ...} once actor/
			// reason/authorization are supplied — proving Slice S4 actually
			// reaches a caller who never knew `review repair --preflight`
			// existed.
			Steps: []Step{
				{Name: "fixture: one damaged recovery edge, non-pristine successor, plus an unrelated witness lineage",
					Fixture: damagedLeafEligibleForDisposition},
				{Name: "ask the negotiated surface what happens next", Requires: statusCapability,
					Args: productArgs("review", "status", "--contract", reviewContract, "--next-transition"),
					After: func(sandbox *Sandbox, observation Observation) error {
						var envelope statusEnvelope
						if err := decodeWaveObservation(observation, &envelope, "review status --next-transition over a closed content-mismatched leaf"); err != nil {
							return err
						}
						if envelope.NextTransition.Kind != "collect" || len(envelope.NextTransition.Collect.Inputs) == 0 ||
							envelope.NextTransition.Collect.Inputs[0].Name != "disposition_authorization" {
							return fmt.Errorf("next_transition = %+v, want a disposition_authorization collect", envelope.NextTransition)
						}
						planDigest := envelope.argument("plan-digest")
						inventoryRevision := envelope.argument("inventory-revision")
						if !validDispositionSHA256(planDigest) || !validDispositionSHA256(inventoryRevision) {
							return fmt.Errorf("negotiated collect did not carry a valid plan_digest/inventory_revision preview: %+v", envelope.NextTransition.Collect.Inputs[0].Arguments)
						}
						sandbox.Scratch[scratchDispositionPlanDigest] = planDigest
						sandbox.Scratch[scratchDispositionInventoryRevision] = inventoryRevision
						return nil
					}},
				{Name: "preview the execute form once actor/reason/authorization are supplied", Requires: statusCapability,
					Args: func(sandbox *Sandbox) ([]string, error) {
						planDigest, inventoryRevision, binding, err := dispositionRepairExecutionInputs(sandbox)
						if err != nil {
							return nil, err
						}
						const actor, reason = "bench", "quarantine the content-mismatched leaf"
						authorization := dispositionAuthorization(binding, planDigest, inventoryRevision, actor, reason)
						return []string{
							"review", "status", "--cwd", sandbox.Repo, "--contract", reviewContract, "--next-transition",
							"--repair-actor", actor, "--repair-reason", reason, "--repair-authorization", authorization,
						}, nil
					},
					After: func(_ *Sandbox, observation Observation) error {
						var envelope statusEnvelope
						if err := decodeWaveObservation(observation, &envelope, "review status --next-transition with disposition authorization supplied"); err != nil {
							return err
						}
						if envelope.NextTransition.Kind != "execute" || envelope.NextTransition.Execute.Operation != "review.repair" {
							return fmt.Errorf("next_transition = %+v, want an execute of review.repair", envelope.NextTransition)
						}
						for _, want := range []string{"--plan-digest", "--inventory-revision", "--actor", "--reason", "--authorization"} {
							if !strings.Contains(envelope.NextTransition.Execute.Command, want) {
								return fmt.Errorf("negotiated execute command is missing %q: %q", want, envelope.NextTransition.Execute.Command)
							}
						}
						return nil
					}},
				{Name: "run the repair the negotiated route named", Requires: repairDispositionExecuteCapability,
					Args: dispositionRepairArgs("quarantine the content-mismatched leaf"), After: requireDispositionQuarantineCommitted},
				{Name: "the authority graph after the negotiated repair", Requires: inspectAuthorityCapability,
					Args:  productArgs("review", "inspect-authority"),
					After: inspectionAssertion("the retained graph after the negotiated route's repair", requireRetainedGraphValid)},
				{Name: "the store governs again", Composite: proveStoreRecovered},
				{Name: "the unrelated witness lineage never moved", Composite: requireDispositionWitnessBytesUnchanged},
			},
		},
	}
}
