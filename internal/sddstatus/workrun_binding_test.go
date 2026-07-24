package sddstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSDDWorkRunBindingRequiresRealAuthorityAndIsSingleAssignment(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, "workrun-binding")
	if err != nil {
		t.Fatal(err)
	}
	workRunID := "work-run-binding"
	acceptanceRef := sddBindingTestRef("route-acceptance")

	if _, err := store.BindWorkRun(
		context.Background(),
		workRunID,
		acceptanceRef,
	); !errors.Is(err, ErrSDDRunAuthorityNotFound) {
		t.Fatalf("BindWorkRun() absent change error = %v", err)
	}
	if _, err := os.Lstat(store.Dir); !os.IsNotExist(err) {
		t.Fatalf("absent change created runtime authority: %v", err)
	}

	changeRoot := seedSDDBindingChange(t, repo, store.Change)
	bound, err := store.BindWorkRun(context.Background(), workRunID, acceptanceRef)
	if err != nil {
		t.Fatal(err)
	}
	if bound.RunRef != store.Change ||
		bound.WorkRunID != workRunID ||
		bound.RouteAcceptanceRef != acceptanceRef ||
		bound.Schema != SDDWorkRunBindingSchema ||
		bound.BindingRef == "" {
		t.Fatalf("WorkRun binding = %#v", bound)
	}
	resolved, err := store.ResolveWorkRunBinding(context.Background())
	if err != nil || resolved != bound {
		t.Fatalf("ResolveWorkRunBinding() = %#v, %v", resolved, err)
	}
	replayed, err := store.BindWorkRun(context.Background(), workRunID, acceptanceRef)
	if err != nil || replayed != bound || countRuntimeRecords(t, store.Dir) != 1 {
		t.Fatalf("exact WorkRun replay = %#v, %v; records=%d", replayed, err, countRuntimeRecords(t, store.Dir))
	}

	before, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, mismatch := range []struct {
		workRunID  string
		acceptance string
	}{
		{workRunID: "different-work-run", acceptance: acceptanceRef},
		{workRunID: workRunID, acceptance: sddBindingTestRef("different-acceptance")},
	} {
		if _, err := store.BindWorkRun(
			context.Background(),
			mismatch.workRunID,
			mismatch.acceptance,
		); !errors.Is(err, ErrSDDWorkRunBindingConflict) {
			t.Fatalf("BindWorkRun() mismatch error = %v", err)
		}
	}
	after, err := store.Status()
	if err != nil || after.Revision != before.Revision || countRuntimeRecords(t, store.Dir) != 1 {
		t.Fatalf("rebind mutated authority: before=%#v after=%#v err=%v", before, after, err)
	}

	// Once imported, the immutable runtime chain remains the real SDD
	// authority even if the mutable OpenSpec projection later disappears.
	archived := filepath.Join(t.TempDir(), "archived-workrun-binding")
	if err := os.Rename(changeRoot, archived); err != nil {
		t.Fatal(err)
	}
	historicalReplay, err := store.BindWorkRun(context.Background(), workRunID, acceptanceRef)
	if err != nil || historicalReplay != bound {
		t.Fatalf("historical exact WorkRun replay = %#v, %v", historicalReplay, err)
	}
}

func TestSDDWorkRunBindingAcceptsExistingHistoricalRuntimeAuthority(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, "historical-runtime-binding")
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.Begin(context.Background(), BeginAttemptRequest{
		ExpectedRevision: "", RequestID: "bind-workrun", WorkUnit: "historical-work",
		EvidenceGoal: "preserve historical runtime authority", MaxAttempts: 2,
		MaxChangedLines: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Revision == "" {
		t.Fatal("historical runtime fixture has no native revision")
	}
	bound, err := store.BindWorkRun(
		context.Background(),
		"historical-work-run",
		sddBindingTestRef("historical-route"),
	)
	if err != nil {
		t.Fatalf("BindWorkRun() over historical runtime authority = %v", err)
	}
	if bound.RunRef != store.Change || countRuntimeRecords(t, store.Dir) != 2 {
		t.Fatalf("historical binding = %#v; records=%d", bound, countRuntimeRecords(t, store.Dir))
	}
}

func TestSDDWorkRunBindingRejectsForgedRepositoryIdentity(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	foreignRepo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, "forged-repository")
	if err != nil {
		t.Fatal(err)
	}
	seedSDDBindingChange(t, foreignRepo, store.Change)

	for _, mutate := range []func(*RuntimeStore){
		func(forged *RuntimeStore) {
			forged.Repo = foreignRepo
		},
		func(forged *RuntimeStore) {
			forged.Repo = foreignRepo
			forged.Workspace = foreignRepo
		},
	} {
		forged := store
		mutate(&forged)
		if _, err := forged.BindWorkRun(
			context.Background(),
			"forged-work-run",
			sddBindingTestRef("forged-route"),
		); err == nil || !strings.Contains(err.Error(), "canonical Git authority") {
			t.Fatalf("BindWorkRun() forged repository error = %v", err)
		}
	}
	if _, err := os.Lstat(store.Dir); !os.IsNotExist(err) {
		t.Fatalf("forged repository created runtime authority: %v", err)
	}
}

func TestSDDWorkRunBindingConcurrentPublicationConverges(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, "concurrent-workrun-binding")
	if err != nil {
		t.Fatal(err)
	}
	seedSDDBindingChange(t, repo, store.Change)
	const writers = 12
	start := make(chan struct{})
	var successes atomic.Int32
	var unexpectedMu sync.Mutex
	var unexpected []error
	var wait sync.WaitGroup
	for range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, bindErr := store.BindWorkRun(
				context.Background(),
				"concurrent-work-run",
				sddBindingTestRef("concurrent-route"),
			)
			if bindErr == nil {
				successes.Add(1)
				return
			}
			if !errors.Is(bindErr, ErrRuntimeConcurrentUpdate) {
				unexpectedMu.Lock()
				unexpected = append(unexpected, bindErr)
				unexpectedMu.Unlock()
			}
		}()
	}
	close(start)
	wait.Wait()
	if successes.Load() < 1 || len(unexpected) != 0 {
		t.Fatalf("concurrent WorkRun binding: successes=%d unexpected=%v", successes.Load(), unexpected)
	}
	bound, err := store.BindWorkRun(
		context.Background(),
		"concurrent-work-run",
		sddBindingTestRef("concurrent-route"),
	)
	if err != nil || bound.WorkRunID != "concurrent-work-run" ||
		countRuntimeRecords(t, store.Dir) != 1 {
		t.Fatalf("converged WorkRun binding = %#v, %v; records=%d", bound, err, countRuntimeRecords(t, store.Dir))
	}
}

func TestSDDWorkRunBindingUsesSharedGitCommonDirectory(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	mainStore, err := OpenRuntimeStore(
		context.Background(),
		repo,
		"linked-worktree-binding",
	)
	if err != nil {
		t.Fatal(err)
	}
	seedSDDBindingChange(t, repo, mainStore.Change)
	workRunID := "linked-worktree-work-run"
	bound, err := mainStore.BindWorkRun(
		context.Background(),
		workRunID,
		sddBindingTestRef("linked-worktree-route"),
	)
	if err != nil {
		t.Fatal(err)
	}

	linked := filepath.Join(t.TempDir(), "linked")
	runSDDStatusGit(t, repo, "worktree", "add", "-q", "--detach", linked)
	linkedStore, err := OpenRuntimeStore(
		context.Background(),
		linked,
		mainStore.Change,
	)
	if err != nil {
		t.Fatal(err)
	}
	if linkedStore.Dir != mainStore.Dir {
		t.Fatalf("linked RuntimeStore Dir = %q, want %q", linkedStore.Dir, mainStore.Dir)
	}
	resolved, err := linkedStore.ResolveWorkRunBinding(context.Background())
	if err != nil || resolved != bound {
		t.Fatalf("linked ResolveWorkRunBinding() = %#v, %v", resolved, err)
	}
	beginSDDBindingAttempt(t, linkedStore, "", "linked-worktree-begin")
	reservation, err := linkedStore.BindCommonVerificationReservation(
		context.Background(),
		workRunID,
		sddBindingTestRef("linked-worktree-workrun-revision"),
		sddBindingTestRef("linked-worktree-reservation"),
		sddBindingTestRef("linked-worktree-ticket"),
	)
	if err != nil {
		t.Fatal(err)
	}
	status, err := mainStore.Status()
	if err != nil || len(status.VerificationReservations) != 1 ||
		status.VerificationReservations[0] != reservation {
		t.Fatalf("main worktree status = %#v, %v", status, err)
	}
}

func TestSDDCommonVerificationReservationBindsExactActiveAttempt(t *testing.T) {
	fixture := newSDDReservationFixture(t, "reservation-exact")
	started := beginSDDBindingAttempt(t, fixture.store, "", "begin-exact")
	workRunRevision := sddBindingTestRef("workrun-revision-one")
	reservationRef := sddBindingTestRef("reservation-one")
	actionTicketRef := sddBindingTestRef("action-ticket-one")

	bound, err := fixture.store.BindCommonVerificationReservation(
		context.Background(),
		fixture.workRunID,
		workRunRevision,
		reservationRef,
		actionTicketRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bound.RunRef != fixture.store.Change ||
		bound.WorkRunID != fixture.workRunID ||
		bound.WorkRunRevision != workRunRevision ||
		bound.ReservationRef != reservationRef ||
		bound.ActionTicketRef != actionTicketRef ||
		bound.ObjectiveID != started.ActiveAttempt.ObjectiveID ||
		bound.ObjectiveGeneration != started.ActiveAttempt.ObjectiveGeneration ||
		bound.AttemptOrdinal != started.ActiveAttempt.Ordinal {
		t.Fatalf("reservation binding = %#v; active=%#v", bound, started.ActiveAttempt)
	}
	replayed, err := fixture.store.BindCommonVerificationReservation(
		context.Background(),
		fixture.workRunID,
		workRunRevision,
		reservationRef,
		actionTicketRef,
	)
	if err != nil || replayed != bound || countRuntimeRecords(t, fixture.store.Dir) != 3 {
		t.Fatalf("reservation replay = %#v, %v; records=%d", replayed, err, countRuntimeRecords(t, fixture.store.Dir))
	}

	finished := finishSDDBindingAttempt(t, fixture.store, started, "finish-exact")
	replayAfterFinish, err := fixture.store.BindCommonVerificationReservation(
		context.Background(),
		fixture.workRunID,
		workRunRevision,
		reservationRef,
		actionTicketRef,
	)
	if err != nil || replayAfterFinish != bound {
		t.Fatalf("terminal exact reservation replay = %#v, %v", replayAfterFinish, err)
	}
	if _, err := fixture.store.BindCommonVerificationReservation(
		context.Background(),
		fixture.workRunID,
		sddBindingTestRef("workrun-revision-no-attempt"),
		sddBindingTestRef("reservation-no-attempt"),
		sddBindingTestRef("ticket-no-attempt"),
	); !errors.Is(err, ErrRuntimeNoActiveAttempt) {
		t.Fatalf("BindCommonVerificationReservation() without active attempt = %v", err)
	}

	second := beginSDDBindingAttempt(t, fixture.store, finished.Revision, "begin-second")
	if _, err := fixture.store.BindCommonVerificationReservation(
		context.Background(),
		"different-work-run",
		sddBindingTestRef("cross-workrun-revision"),
		sddBindingTestRef("cross-workrun-reservation"),
		sddBindingTestRef("cross-workrun-ticket"),
	); !errors.Is(err, ErrSDDWorkRunBindingConflict) {
		t.Fatalf("cross-WorkRun reservation error = %v", err)
	}
	if _, err := fixture.store.BindCommonVerificationReservation(
		context.Background(),
		fixture.workRunID,
		workRunRevision,
		sddBindingTestRef("stale-revision-reservation"),
		sddBindingTestRef("stale-revision-ticket"),
	); !errors.Is(err, ErrSDDReservationBindingConflict) {
		t.Fatalf("stale WorkRun revision error = %v", err)
	}
	if _, err := fixture.store.BindCommonVerificationReservation(
		context.Background(),
		fixture.workRunID,
		sddBindingTestRef("mismatch-revision"),
		reservationRef,
		sddBindingTestRef("mismatch-ticket"),
	); !errors.Is(err, ErrSDDReservationBindingConflict) {
		t.Fatalf("reservation rebind error = %v", err)
	}

	secondBound, err := fixture.store.BindCommonVerificationReservation(
		context.Background(),
		fixture.workRunID,
		sddBindingTestRef("workrun-revision-two"),
		sddBindingTestRef("reservation-two"),
		sddBindingTestRef("action-ticket-two"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if secondBound.ObjectiveID != second.ActiveAttempt.ObjectiveID ||
		secondBound.ObjectiveGeneration != second.ActiveAttempt.ObjectiveGeneration ||
		secondBound.AttemptOrdinal != 2 {
		t.Fatalf("second reservation binding = %#v; active=%#v", secondBound, second.ActiveAttempt)
	}
}

func TestSDDCommonVerificationReservationRejectsMissingWorkRunAndWrongAttempt(t *testing.T) {
	repo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, "reservation-preconditions")
	if err != nil {
		t.Fatal(err)
	}
	seedSDDBindingChange(t, repo, store.Change)
	started := beginSDDBindingAttempt(t, store, "", "begin-without-workrun")
	if _, err := store.BindCommonVerificationReservation(
		context.Background(),
		"unbound-work-run",
		sddBindingTestRef("unbound-revision"),
		sddBindingTestRef("unbound-reservation"),
		sddBindingTestRef("unbound-ticket"),
	); !errors.Is(err, ErrSDDWorkRunBindingNotFound) {
		t.Fatalf("reservation without WorkRun binding error = %v", err)
	}
	if countRuntimeRecords(t, store.Dir) != 1 {
		t.Fatalf("rejected unbound reservation mutated ledger: records=%d", countRuntimeRecords(t, store.Dir))
	}

	// A canonical but forged successor that points at another ordinal remains
	// unreadable even when its record hash and request digest are self-consistent.
	finishSDDBindingAttempt(t, store, started, "finish-without-workrun")

	fixture := newSDDReservationFixture(t, "wrong-attempt-record")
	active := beginSDDBindingAttempt(t, fixture.store, "", "begin-wrong-attempt")
	bound, err := fixture.store.BindCommonVerificationReservation(
		context.Background(),
		fixture.workRunID,
		sddBindingTestRef("wrong-attempt-revision"),
		sddBindingTestRef("wrong-attempt-reservation"),
		sddBindingTestRef("wrong-attempt-ticket"),
	)
	if err != nil {
		t.Fatal(err)
	}
	status, err := fixture.store.Status()
	if err != nil {
		t.Fatal(err)
	}
	record, err := fixture.store.loadRecord(status.Revision)
	if err != nil {
		t.Fatal(err)
	}
	record.ReservationBinding.AttemptOrdinal = active.ActiveAttempt.Ordinal + 1
	record.ReservationBinding.BindingRef = commonVerificationReservationBindingRef(
		*record.ReservationBinding,
	)
	revision, payload, err := runtimeRecordRevision(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.publishRecord(revision, payload); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.publishHead(revision); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Status(); err == nil ||
		!strings.Contains(err.Error(), "exact active SDD attempt") {
		t.Fatalf("forged attempt record replay error = %v; original=%#v", err, bound)
	}
}

func TestSDDCommonVerificationReservationConcurrentReplayPublishesOnce(t *testing.T) {
	fixture := newSDDReservationFixture(t, "reservation-concurrent")
	beginSDDBindingAttempt(t, fixture.store, "", "begin-concurrent-reservation")
	workRunRevision := sddBindingTestRef("concurrent-workrun-revision")
	reservationRef := sddBindingTestRef("concurrent-reservation")
	actionTicketRef := sddBindingTestRef("concurrent-action-ticket")

	const writers = 12
	start := make(chan struct{})
	var successes atomic.Int32
	var unexpectedMu sync.Mutex
	var unexpected []error
	var wait sync.WaitGroup
	for range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, bindErr := fixture.store.BindCommonVerificationReservation(
				context.Background(),
				fixture.workRunID,
				workRunRevision,
				reservationRef,
				actionTicketRef,
			)
			if bindErr == nil {
				successes.Add(1)
				return
			}
			if !errors.Is(bindErr, ErrRuntimeConcurrentUpdate) {
				unexpectedMu.Lock()
				unexpected = append(unexpected, bindErr)
				unexpectedMu.Unlock()
			}
		}()
	}
	close(start)
	wait.Wait()
	if successes.Load() < 1 || len(unexpected) != 0 {
		t.Fatalf("concurrent reservation: successes=%d unexpected=%v", successes.Load(), unexpected)
	}
	replayed, err := fixture.store.BindCommonVerificationReservation(
		context.Background(),
		fixture.workRunID,
		workRunRevision,
		reservationRef,
		actionTicketRef,
	)
	if err != nil || replayed.ReservationRef != reservationRef ||
		countRuntimeRecords(t, fixture.store.Dir) != 3 {
		t.Fatalf("converged reservation = %#v, %v; records=%d", replayed, err, countRuntimeRecords(t, fixture.store.Dir))
	}
	status, err := fixture.store.Status()
	if err != nil || len(status.VerificationReservations) != 1 {
		t.Fatalf("converged reservation status = %#v, %v", status, err)
	}
}

func TestSDDWorkRunBindingsDoNotChangeFrozenStatusV1Projection(t *testing.T) {
	fixture := newSDDReservationFixture(t, "status-v1-binding")
	beginSDDBindingAttempt(t, fixture.store, "", "begin-status-v1")
	if _, err := fixture.store.BindCommonVerificationReservation(
		context.Background(),
		fixture.workRunID,
		sddBindingTestRef("status-v1-revision"),
		sddBindingTestRef("status-v1-reservation"),
		sddBindingTestRef("status-v1-ticket"),
	); err != nil {
		t.Fatal(err)
	}
	runtimeStatus, err := fixture.store.Status()
	if err != nil {
		t.Fatal(err)
	}
	status := baseStatus(fixture.store.Repo, nil, nil, "apply", nil)
	status.RuntimeStatus = &runtimeStatus
	projected, err := ProjectStatusV1(status)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"work_run_binding",
		"verification_reservations",
		fixture.workRunID,
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("SDD status v1 leaked %q: %s", forbidden, payload)
		}
	}
}

type sddReservationFixture struct {
	store     RuntimeStore
	workRunID string
}

func newSDDReservationFixture(t *testing.T, change string) sddReservationFixture {
	t.Helper()
	repo := initRuntimeLedgerRepo(t)
	store, err := OpenRuntimeStore(context.Background(), repo, change)
	if err != nil {
		t.Fatal(err)
	}
	seedSDDBindingChange(t, repo, change)
	workRunID := change + "-work-run"
	if _, err := store.BindWorkRun(
		context.Background(),
		workRunID,
		sddBindingTestRef(change+"-route"),
	); err != nil {
		t.Fatal(err)
	}
	return sddReservationFixture{store: store, workRunID: workRunID}
}

func seedSDDBindingChange(t *testing.T, repo, change string) string {
	t.Helper()
	root := filepath.Join(repo, "openspec", "changes", change)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "proposal.md"),
		[]byte("# Existing SDD change\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	return root
}

func beginSDDBindingAttempt(
	t *testing.T,
	store RuntimeStore,
	expectedRevision string,
	requestID string,
) RuntimeStatus {
	t.Helper()
	if expectedRevision == "" {
		status, err := store.Status()
		if err != nil {
			t.Fatal(err)
		}
		expectedRevision = status.Revision
	}
	started, err := store.Begin(context.Background(), BeginAttemptRequest{
		ExpectedRevision: expectedRevision, RequestID: requestID,
		WorkUnit: "common-verification", EvidenceGoal: "bind common verification reservation",
		MaxAttempts: 3, MaxChangedLines: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.ActiveAttempt == nil {
		t.Fatal("Begin() did not create an active SDD attempt")
	}
	return started
}

func finishSDDBindingAttempt(
	t *testing.T,
	store RuntimeStore,
	started RuntimeStatus,
	requestID string,
) RuntimeStatus {
	t.Helper()
	current, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if current.ActiveAttempt == nil || started.ActiveAttempt == nil ||
		*current.ActiveAttempt != *started.ActiveAttempt {
		t.Fatalf(
			"active attempt changed before finish: started=%#v current=%#v",
			started.ActiveAttempt,
			current.ActiveAttempt,
		)
	}
	finished, err := store.Finish(context.Background(), FinishAttemptRequest{
		ExpectedRevision: current.Revision, RequestID: requestID,
		Outcome: AttemptFailed, EvidenceRevision: sddBindingTestRef(requestID + "-evidence"),
		Diagnosis: "bounded verification failed", HarnessDisposition: HarnessReused,
		CleanupEvidence: "cleanup completed", ProcessEvidence: "no descendants remained",
	})
	if err != nil {
		t.Fatal(err)
	}
	return finished
}

func sddBindingTestRef(label string) string {
	return runtimeValueHash("gentle-ai.sdd-workrun-binding-test/v1", struct {
		Label string `json:"label"`
	}{Label: fmt.Sprintf("%s", label)})
}
