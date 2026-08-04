package reviewtransaction

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// malformedLegacyFreezeFixture persists one shipped legacy-v1 lineage whose
// review/freeze-findings successor captured state the semantic replay
// considers unrelated to the findings freeze — the #1311 corruption class.
// The chain is structurally intact and content-addressed; only the semantic
// historical replay fails.
func malformedLegacyFreezeFixture(t *testing.T, repo, lineage string) (Store, string, string) {
	t.Helper()
	store, err := AuthoritativeStore(context.Background(), repo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := NewTransaction(Start{
		LineageID: lineage, Mode: ModeOrdinary4R, Generation: 1,
		Snapshot: Snapshot{
			Kind: TargetCurrentChanges, BaseTree: tree("a"), CandidateTree: tree("b"),
			PathsDigest: hash("a"), IntendedUntracked: []string{},
			IntendedUntrackedProof: hash("b"), Paths: []string{"internal/example.go"}, Identity: hash("c"),
		},
		PolicyHash: hash("d"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.StartReview(); err != nil {
		t.Fatal(err)
	}
	genesis := writeStoreEvent(t, store, Record{Operation: "review/start", Transaction: *tx})
	freeze := historicalFreezeTransition(t, *tx)
	// The persisted event also captured an evidence hash the freeze transition
	// never writes, so the semantic historical replay reports that the freeze
	// changed unrelated transaction state while the event stays structurally
	// valid, content-addressed, and decodable.
	freeze.EvidenceHash = hash("0")
	malformed := writeStoreEvent(t, store, Record{Operation: "review/freeze-findings", PreviousRevision: genesis, Transaction: freeze})
	if _, err := freeze.ClassifyEvidence([]FindingEvidence{}); err != nil {
		t.Fatal(err)
	}
	head := writeStoreEvent(t, store, Record{Operation: "review/classify-evidence", PreviousRevision: malformed, Transaction: freeze})
	return store, head, malformed
}

// TestMalformedLegacyFreezeEventBricksInventoryWithNoFamilyExit reproduces
// #1311: one semantically invalid legacy freeze event marks the lineage
// invalid, forces the whole inventory complete:false/authoritative:false, and
// every existing quarantine-family member refuses the entry.
func TestMalformedLegacyFreezeEventBricksInventoryWithNoFamilyExit(t *testing.T) {
	repo := initSnapshotRepo(t)
	approvedCompactFixture(t, repo, "legacy-quarantine-unrelated-approved")
	store, _, _ := malformedLegacyFreezeFixture(t, repo, "legacy-freeze-broken")

	_, loadErr := store.LoadChain()
	if loadErr == nil || !errors.Is(loadErr, ErrInvalidSuccessor) ||
		!strings.Contains(loadErr.Error(), "historical findings freeze changed unrelated transaction state") {
		t.Fatalf("malformed freeze load error = %v", loadErr)
	}
	t.Logf("verbatim load failure: %v", loadErr)

	report, err := InventoryAuthority(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if report.Complete || report.Authoritative || report.Status != AuthorityStatusInvalid ||
		!hasAuthorityInventoryStatus(report.Entries, "legacy-freeze-broken", AuthorityStatusInvalid) {
		t.Fatalf("bricked inventory = %#v", report)
	}
	for _, entry := range report.Entries {
		if entry.LineageID != "legacy-freeze-broken" {
			continue
		}
		if len(entry.Problems) != 1 || !strings.Contains(entry.Problems[0], "historical findings freeze changed unrelated transaction state") {
			t.Fatalf("bricked entry problems = %#v", entry.Problems)
		}
		t.Logf("verbatim inventory classification: status=%s problems=%q", entry.Status, entry.Problems)
	}

	// The audited quarantine family routes the anomaly nowhere: reclaim and
	// abandon both operate on compact-v2 entries only. (reconcile-authority
	// used to be checked here too, for the same reason; the verb and its
	// provider retired in Wave 7 S3a/S3b. quarantine-legacy -- the ONE verb
	// that actually did handle this exact malformed-freeze diagnostic --
	// retired in Wave 7 S5a/S5b; this anomaly class now has no sanctioned
	// exit at all, and this test's remaining assertions -- the bricked
	// inventory and the two refusals below -- are the whole proof of that.)
	if _, err := ReclaimIncompleteCompactStore(context.Background(), repo, CompactReclaimRequest{
		LineageID: "legacy-freeze-broken", Reason: "retire malformed legacy history", Actor: "maintainer@example.com",
	}); err == nil || !strings.Contains(err.Error(), "inspect reclaim target") {
		t.Fatalf("reclaim refusal = %v", err)
	} else {
		t.Logf("verbatim reclaim refusal: %v", err)
	}
	if _, err := AbandonPristineCompactStore(context.Background(), repo, CompactAbandonRequest{
		LineageID: "legacy-freeze-broken", ExpectedRevision: hash("3"),
		Reason: "retire malformed legacy history", Actor: "maintainer@example.com",
		MaintainerAuthorization: "irrelevant",
	}); err == nil || !strings.Contains(err.Error(), "no committed abandonment record matches") {
		t.Fatalf("abandon refusal = %v", err)
	} else {
		t.Logf("verbatim abandon refusal: %v", err)
	}
}
