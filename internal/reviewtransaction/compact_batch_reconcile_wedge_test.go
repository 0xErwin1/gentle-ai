package reviewtransaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file is the RED-first proof that a leftover batch-reconcile marker is
// a REPORTED historical artifact, not a permanent refusal.
//
// Wave 7 S4a/S4b retired the `review reconcile-authority-batch` verb and then
// its ReconcileInvalidRecoveryEdges provider. The on-disk
// "batch-reconcile-journal.json" marker guard was deliberately retained for
// forensic safety (D5): a repository interrupted mid-batch, possibly before
// the retirement, must never be silently proceeded past.
//
// Retaining DETECTION was right. Retaining the REFUSAL was not, and the
// difference is what this file pins. ErrCompactBatchReconcilePrepared says
// "exact replay is required", but the only thing that could replay the
// journal was deleted in the same wave, so the precondition is unsatisfiable
// by construction. Every call site that returned it therefore refused
// forever, and two of those call sites are the exact commands an operator
// reaches for to diagnose or escape: `review inspect-authority` (through
// InspectCompactRecoveryEdges) and `review abandon` (through the compact
// store openers). A dead end that also disables its own diagnosis is
// strictly worse than an ordinary dead end.
//
// The fix is a relaxation, not new machinery: keep finding the marker, keep
// naming its exact path in the authority inventory, and stop treating its
// presence as proof the whole authority is unreadable.

// batchReconcileWedgeRepo returns a real repository whose authority root
// holds a leftover prepared-batch marker, plus that marker's exact path.
func batchReconcileWedgeRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := initSnapshotRepo(t)
	base, _, err := reviewAuthorityRoot(context.Background(), repo)
	if err != nil {
		t.Fatalf("reviewAuthorityRoot(%q) = %v", repo, err)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := compactBatchReconcileMarkerPath(base)
	if err := os.WriteFile(marker, []byte("{\"prepared\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo, marker
}

// TestLeftoverBatchMarkerKeepsAuthorityReadable is the core wedge proof.
// Before the fix, InventoryAuthority set Complete=false and
// Authoritative=false on nothing but the marker's presence, and
// review_facade.go's `(!report.Complete || !report.Authoritative)` branch
// turned that into applicability "corrupted" -> next_transition
// {"kind":"stop","reason_code":"corrupted_or_unverifiable_authority"} for a
// repository whose records were never actually inspected. Because nothing on
// main removes the marker and nothing can replay it, that state was
// permanent.
func TestLeftoverBatchMarkerKeepsAuthorityReadable(t *testing.T) {
	repo, _ := batchReconcileWedgeRepo(t)

	report, err := InventoryAuthority(context.Background(), repo)
	if err != nil {
		t.Fatalf("InventoryAuthority() = %v, want nil", err)
	}
	if !report.Complete || !report.Authoritative {
		t.Errorf("InventoryAuthority() Complete=%v Authoritative=%v, want both true: a leftover historical marker is a diagnostic, not proof the authority is unreadable",
			report.Complete, report.Authoritative)
	}
	if report.Status == AuthorityStatusInvalid {
		t.Errorf("InventoryAuthority() Status = %q, want anything but %q for a marker-only repository",
			report.Status, AuthorityStatusInvalid)
	}
}

// TestLeftoverBatchMarkerIsStillReported is the other half, and it is what
// keeps this a relaxation rather than a deletion. Wave 7's D5 forensic-safety
// requirement is that the product is never BLIND to a historical on-disk
// artifact. Reporting the marker's exact path satisfies that; refusing every
// operation was never what made it visible. In fact the refusal actively hid
// it, because the negotiated v2 envelope carried only the stop reason code
// and inspect-authority could not run at all.
func TestLeftoverBatchMarkerIsStillReported(t *testing.T) {
	repo, marker := batchReconcileWedgeRepo(t)

	report, err := InventoryAuthority(context.Background(), repo)
	if err != nil {
		t.Fatalf("InventoryAuthority() = %v, want nil", err)
	}
	var found *AuthorityInventoryDiagnostic
	for index, diagnostic := range report.Diagnostics {
		if diagnostic.Path == marker {
			found = &report.Diagnostics[index]
			break
		}
	}
	if found == nil {
		paths := make([]string, 0, len(report.Diagnostics))
		for _, diagnostic := range report.Diagnostics {
			paths = append(paths, diagnostic.Path)
		}
		t.Fatalf("InventoryAuthority() diagnostics = %v, want one naming the marker path %q: relaxing the refusal must not make the product blind to the artifact (Wave 7 D5)",
			paths, marker)
	}
	if strings.TrimSpace(found.Problem) == "" {
		t.Errorf("marker diagnostic Problem is empty, want a description of the leftover artifact")
	}
}

// TestLeftoverBatchMarkerDoesNotBlockRecoveryInspection pins the sharpest
// edge of the wedge: InspectCompactRecoveryEdges backs `review
// inspect-authority`, which is the one command that would show an operator
// the marker's path and the state of every recovery edge. Refusing it on the
// marker meant the wedge disabled its own diagnosis.
func TestLeftoverBatchMarkerDoesNotBlockRecoveryInspection(t *testing.T) {
	repo, _ := batchReconcileWedgeRepo(t)

	if _, err := InspectCompactRecoveryEdges(context.Background(), repo); err != nil {
		if errors.Is(err, ErrCompactBatchReconcilePrepared) {
			t.Fatalf("InspectCompactRecoveryEdges() = %v, want nil: read-only inspection is how an operator diagnoses the marker, so it must never be gated on the marker", err)
		}
		t.Fatalf("InspectCompactRecoveryEdges() = %v, want nil", err)
	}
}

// TestLeftoverBatchMarkerDoesNotBlockCompactStoreAccess covers the opener
// path shared by `review abandon`, `review status` and every other verb that
// resolves a compact store. Blocking it is what made `review abandon` answer
// the prepared-batch error instead of its own argument-level guidance.
func TestLeftoverBatchMarkerDoesNotBlockCompactStoreAccess(t *testing.T) {
	repo, _ := batchReconcileWedgeRepo(t)

	if _, err := DiscoverCompactStores(context.Background(), repo); err != nil {
		if errors.Is(err, ErrCompactBatchReconcilePrepared) {
			t.Fatalf("DiscoverCompactStores() = %v, want nil: enumerating existing authority is read-only and must not be gated on a marker nothing can clear", err)
		}
		t.Fatalf("DiscoverCompactStores() = %v, want nil", err)
	}
}

// TestBatchReconcileMarkerPathIsUnchanged keeps the on-disk name pinned. The
// relaxation changes what the marker MEANS, never where it lives: an operator
// or a forensic script that already knows this filename must keep finding it.
func TestBatchReconcileMarkerPathIsUnchanged(t *testing.T) {
	base := t.TempDir()
	want := filepath.Join(base, "batch-reconcile-journal.json")
	if got := compactBatchReconcileMarkerPath(base); got != want {
		t.Fatalf("compactBatchReconcileMarkerPath(%q) = %q, want %q", base, got, want)
	}
}
