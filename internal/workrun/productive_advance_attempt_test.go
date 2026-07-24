package workrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBeginProductiveAdvanceAnchorsExactSourceAndKeepsPublicCASStable(
	t *testing.T,
) {
	repo := initWorkRunRepository(t)
	store := openTestWorkRunStore(t, repo, "productive-attempt")
	started := startDirectWorkRun(t, store)
	request := BeginProductiveAdvanceRequest{
		ExpectedRevision: started.Revision,
		RequestID:        "begin-productive-attempt",
		SourceRevision:   started.Revision,
	}

	begun, err := store.BeginProductiveAdvance(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if begun.Revision == started.Revision ||
		begun.ProductiveAdvanceSourceRevision != started.Revision {
		t.Fatalf("begun state = %#v, want source %q", begun, started.Revision)
	}

	handoff := testHandoff(
		t,
		store,
		ImplementationRouteDirectInline,
		"",
		"productive-attempt-scope",
	)
	progressed, err := store.BindImplementationHandoff(
		context.Background(),
		BindImplementationHandoffRequest{
			ExpectedRevision: begun.Revision,
			RequestID:        "productive-attempt-handoff",
			Handoff:          handoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if progressed.Revision == begun.Revision ||
		progressed.ProductiveAdvanceSourceRevision != started.Revision {
		t.Fatalf("progressed state = %#v", progressed)
	}
	status, err := store.PublicStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Revision != started.Revision {
		t.Fatalf(
			"non-terminal public CAS = %q, want source %q",
			status.Revision,
			started.Revision,
		)
	}

	replayed, err := store.BeginProductiveAdvance(
		context.Background(),
		request,
	)
	if err != nil {
		t.Fatalf("exact begin replay failed: %v", err)
	}
	if replayed.Revision != begun.Revision ||
		replayed.ProductiveAdvanceSourceRevision != started.Revision {
		t.Fatalf("begin replay = %#v, want %#v", replayed, begun)
	}
	current, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != progressed.Revision ||
		current.Handoff == nil {
		t.Fatalf("historical begin replay changed live state: %#v", current)
	}
}

func TestBeginProductiveAdvanceRejectsStaleOrSecondSourceWithoutMutation(
	t *testing.T,
) {
	repo := initWorkRunRepository(t)
	store := openTestWorkRunStore(t, repo, "productive-attempt-cas")
	started := startDirectWorkRun(t, store)
	headPath := filepath.Join(store.Dir, "HEAD")
	headBefore, err := os.ReadFile(headPath)
	if err != nil {
		t.Fatal(err)
	}
	stale := testSHARef("forged-productive-attempt-source")
	if _, err := store.BeginProductiveAdvance(
		context.Background(),
		BeginProductiveAdvanceRequest{
			ExpectedRevision: stale,
			RequestID:        "stale-productive-attempt",
			SourceRevision:   stale,
		},
	); !errors.Is(err, ErrWorkRunRevisionConflict) {
		t.Fatalf("stale begin error = %v", err)
	}
	headAfter, err := os.ReadFile(headPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(headAfter) != string(headBefore) {
		t.Fatal("stale productive begin mutated WorkRun HEAD")
	}

	begun, err := store.BeginProductiveAdvance(
		context.Background(),
		BeginProductiveAdvanceRequest{
			ExpectedRevision: started.Revision,
			RequestID:        "valid-productive-attempt",
			SourceRevision:   started.Revision,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginProductiveAdvance(
		context.Background(),
		BeginProductiveAdvanceRequest{
			ExpectedRevision: begun.Revision,
			RequestID:        "second-productive-attempt",
			SourceRevision:   begun.Revision,
		},
	); !errors.Is(err, ErrWorkRunInvalidTransition) {
		t.Fatalf("second source error = %v", err)
	}
	current, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != begun.Revision ||
		current.ProductiveAdvanceSourceRevision != started.Revision {
		t.Fatalf("second begin changed live state: %#v", current)
	}
}

func TestBeginProductiveAdvanceRecordShapeAndSourceAreFailClosed(
	t *testing.T,
) {
	source := testSHARef("productive-attempt-record-source")
	record := workRunRecord{
		Schema:           workRunRecordSchemaV1,
		WorkRunID:        "productive-attempt-shape",
		PreviousRevision: source,
		RequestID:        "productive-attempt-shape",
		RequestDigest:    testSHARef("productive-attempt-request"),
		Operation:        workOperationBeginProductive,
		BeginProductive: &workBeginProductiveAdvanceEvent{
			SourceRevision: source,
		},
	}
	if err := validateWorkRunRecordShape(record); err != nil {
		t.Fatalf("valid begin record shape failed: %v", err)
	}
	mixed := record
	mixed.Blocker = &workProductiveBlockerEvent{
		DiagnosticRef: testSHARef("productive-attempt-mixed-blocker"),
	}
	if err := validateWorkRunRecordShape(mixed); err == nil {
		t.Fatal("begin record with a second typed event was accepted")
	}
	mismatched := record
	mismatched.BeginProductive = &workBeginProductiveAdvanceEvent{
		SourceRevision: testSHARef("different-productive-attempt-source"),
	}
	state := WorkRunState{
		Schema:              WorkRunStateSchemaV1,
		WorkRunID:           record.WorkRunID,
		Revision:            source,
		Started:             true,
		RouteDecision:       directRouteDecision(t),
		ImplementationRoute: ImplementationRouteDirectInline,
		Reservations:        []VerificationReservation{},
		LaunchClaims:        []VerificationLaunchClaim{},
	}
	if err := applyWorkRunRecord(&state, mismatched); !errors.Is(
		err,
		ErrWorkRunInvalidTransition,
	) {
		t.Fatalf("mismatched begin source error = %v", err)
	}
}
