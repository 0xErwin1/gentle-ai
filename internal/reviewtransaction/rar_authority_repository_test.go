package reviewtransaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type rarVerificationFixture struct {
	repo        string
	repository  *RARAuthorityRepository
	publication RARAuthorityPublication
}

// newRARVerificationFixture seeds one exact published RAR verification
// authority: a real Git repository holding an approved historical v1 review
// authority, plus the owner contracts bound to that exact native receipt.
func newRARVerificationFixture(t *testing.T, name string) rarVerificationFixture {
	t.Helper()
	ctx := context.Background()
	repo := initSnapshotRepo(t)
	lineage := "authority-lineage"

	store, err := AuthoritativeStore(ctx, repo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	registry := verificationTestRegistry(t, []string{})
	writeSnapshotFile(t, repo, "tracked.txt", "verified candidate "+name+"\n")
	snapshot, err := (SnapshotBuilder{Repo: repo}).Build(ctx, Target{
		Kind: TargetCurrentChanges, IntendedUntracked: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := NewTransaction(Start{
		LineageID: lineage, Mode: ModeOrdinary4R, Generation: 1,
		Snapshot: snapshot, PolicyHash: registry.PolicyHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.StartReview(); err != nil {
		t.Fatal(err)
	}
	head, err := store.Append("", Record{Operation: "review/start", Transaction: *tx})
	if err != nil {
		t.Fatal(err)
	}
	if err := freezeTestFindings(tx, []Finding{}); err != nil {
		t.Fatal(err)
	}
	if head, err = store.Append(head, Record{Operation: "review/freeze-findings", Transaction: *tx}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ClassifyEvidence([]FindingEvidence{}); err != nil {
		t.Fatal(err)
	}
	if head, err = store.Append(head, Record{Operation: "review/classify-evidence", Transaction: *tx}); err != nil {
		t.Fatal(err)
	}
	if err := tx.BeginFinalVerification(); err != nil {
		t.Fatal(err)
	}
	if head, err = store.Append(head, Record{Operation: "review/begin-final-verification", Transaction: *tx}); err != nil {
		t.Fatal(err)
	}
	if err := tx.CompleteFinalVerification(hash("2"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(head, Record{Operation: "review/complete-final-verification", Transaction: *tx}); err != nil {
		t.Fatal(err)
	}
	receipt, err := tx.Receipt()
	if err != nil {
		t.Fatal(err)
	}
	receiptPayload, err := canonicalRARReceiptPayload(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store.Dir, "artifacts"), 0o700); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(store.Dir, "artifacts", "receipt.json")
	if err := os.WriteFile(receiptPath, receiptPayload, 0o600); err != nil {
		t.Fatal(err)
	}
	receiptRef, err := HashArtifact(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := VerificationSubjectFromSnapshot(tx.Snapshot)
	if err != nil {
		t.Fatal(err)
	}

	applicability := verificationTestApplicability(t, registry, subject.CandidateTree, subject.SnapshotIdentity)
	applicability.Subject = subject
	if applicability.Digest, err = verificationApplicabilityDigest(applicability); err != nil {
		t.Fatal(err)
	}
	if err := applicability.Validate(); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildVerificationPlan(applicability, registry)
	if err != nil {
		t.Fatal(err)
	}
	result := VerificationResultRef{
		Schema: VerificationResultRefSchema, ResultRef: verificationTestHash(name + "-result"),
		Subject: plan.Subject, PolicyHash: plan.PolicyHash,
		PlanDigest: plan.Digest, ApplicabilityDigest: plan.ApplicabilityDigest,
		Aggregate:            VerificationAggregateComplete,
		CompletedObligations: verificationObligationIDs(plan.Obligations),
		EvidenceRefs:         []string{},
	}
	if err := ValidateVerificationResultRef(applicability, registry, plan, result); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenRARAuthorityRepository(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	return rarVerificationFixture{
		repo:       repo,
		repository: repository,
		publication: RARAuthorityPublication{
			LineageID: lineage, ReceiptRef: receiptRef, Applicability: applicability,
			Registry: registry, Plan: plan, Result: result,
		},
	}
}

// TestRARVerificationAuthorityConvergesOnExhaustedRepositoryLock is the
// deterministic reproduction of the #3239 shape: an exact replay of an
// already-published RAR verification authority exhausts the bounded wait on
// the repository LOCK. The lock is the real advisory primitive held on the
// real LOCK path; only the timing is scripted. The honest outcome is
// convergence on the published authority, not a timeout for work that
// already succeeded.
func TestRARVerificationAuthorityConvergesOnExhaustedRepositoryLock(t *testing.T) {
	fixture := newRARVerificationFixture(t, "authority-lock-converge")
	published, err := fixture.repository.Publish(context.Background(), fixture.publication)
	if err != nil {
		t.Fatal(err)
	}
	held, err := acquireRARAuthorityLock(context.Background(), filepath.Join(fixture.repository.root, "LOCK"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.release() }()

	converged, err := fixture.repository.Publish(context.Background(), fixture.publication)
	if err != nil {
		t.Fatalf("Publish() behind a continuously-held repository LOCK with the exact published pair = %v, want convergence", err)
	}
	if !reflect.DeepEqual(published, converged) {
		t.Fatalf("converged verification authority diverged:\npublished=%#v\nconverged=%#v", published, converged)
	}
}

// TestRARVerificationAuthorityLockExhaustionWithoutConvergentPairStaysTyped is
// the guard on the convergence above: exhaustion with genuinely divergent
// state — no published pair, or different contracts addressed to the same
// pair — must keep failing with the typed *AuthorityLockTimeoutError and must
// not converge on a foreign authority. Do not relax this test to make
// contention disappear.
func TestRARVerificationAuthorityLockExhaustionWithoutConvergentPairStaysTyped(t *testing.T) {
	t.Run("no published pair", func(t *testing.T) {
		fixture := newRARVerificationFixture(t, "authority-lock-missing")
		if err := ensureRARRepositoryRoot(fixture.repository.identity.GitCommonDir, fixture.repository.root, true); err != nil {
			t.Fatal(err)
		}
		held, err := acquireRARAuthorityLock(context.Background(), filepath.Join(fixture.repository.root, "LOCK"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = held.release() }()

		_, err = fixture.repository.Publish(context.Background(), fixture.publication)
		if !errors.Is(err, ErrAuthorityLockTimeout) {
			t.Fatalf("Publish() behind a held lock without a published pair = %v, want %v", err, ErrAuthorityLockTimeout)
		}
	})

	t.Run("divergent contracts at the exact pair", func(t *testing.T) {
		fixture := newRARVerificationFixture(t, "authority-lock-divergent")
		if _, err := fixture.repository.Publish(context.Background(), fixture.publication); err != nil {
			t.Fatal(err)
		}
		held, err := acquireRARAuthorityLock(context.Background(), filepath.Join(fixture.repository.root, "LOCK"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = held.release() }()

		divergent := fixture.publication
		divergent.Result.Aggregate = VerificationAggregatePartial
		_, err = fixture.repository.Publish(context.Background(), divergent)
		if !errors.Is(err, ErrAuthorityLockTimeout) {
			t.Fatalf("Publish() behind a held lock with divergent contracts = %v, want %v", err, ErrAuthorityLockTimeout)
		}
	})
}
