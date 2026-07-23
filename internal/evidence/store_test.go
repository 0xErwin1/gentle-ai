package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestStorePublishesCanonicalTicketEvidenceAndDiagnostic(t *testing.T) {
	t.Parallel()

	repo := initEvidenceRepository(t)
	store, err := OpenStore(context.Background(), repo)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	commonDir := strings.TrimSpace(runEvidenceGit(t, repo, "rev-parse", "--git-common-dir"))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repo, commonDir)
	}
	commonDir, err = filepath.EvalSymlinks(filepath.Clean(commonDir))
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(commonDir, filepath.FromSlash(storeRelativeRoot))
	if store.Root() != wantRoot {
		t.Fatalf("Store.Root() = %q, want %q", store.Root(), wantRoot)
	}
	rootInfo, err := os.Stat(store.Root())
	if err != nil || !privateEvidencePathSafe(store.Root(), rootInfo) {
		t.Fatalf("store root is not owner-only: mode=%v, err=%v", rootInfo.Mode().Perm(), err)
	}

	ticket := mustActionTicket(t, "exit", "0")
	if ref, err := store.PutTicket(ticket); err != nil || ref != ticket.TicketRef {
		t.Fatalf("PutTicket() = %q, %v", ref, err)
	}
	if _, err := store.PutTicket(ticket); err != nil {
		t.Fatalf("exact PutTicket() replay error = %v", err)
	}
	loadedTicket, err := store.ReadTicket(ticket.TicketRef)
	if err != nil {
		t.Fatalf("ReadTicket() error = %v", err)
	}
	if loadedTicket.TicketRef != ticket.TicketRef ||
		loadedTicket.RequestBinding != ticket.RequestBinding {
		t.Fatal("loaded ticket differs from its immutable source")
	}

	process := executeTicket(t, context.Background(), ticket, false)
	evidence, err := store.AdmitAndStore(ticket, process)
	if err != nil {
		t.Fatalf("AdmitAndStore() error = %v", err)
	}
	if _, err := store.PutExecution(evidence); err != nil {
		t.Fatalf("exact PutExecution() replay error = %v", err)
	}
	loadedEvidence, err := store.ReadExecutionForTicket(ticket.TicketRef)
	if err != nil {
		t.Fatalf("ReadExecutionForTicket() error = %v", err)
	}
	if loadedEvidence.EvidenceRef != evidence.EvidenceRef ||
		loadedEvidence.Outcome != ExecutionComplete {
		t.Fatalf("loaded evidence = %#v, want %s complete", loadedEvidence, evidence.EvidenceRef)
	}

	diagnostic, err := NewDiagnostic(DiagnosticInput{
		Operation: "verification.execute", Proof: ProofExecution,
		Reason: ReasonNonzeroExit, SubjectRef: ticket.SubjectRef,
		CandidateRef: ticket.CandidateRef, EvidenceRefs: []string{evidence.EvidenceRef},
		ImplementationDecisionRef:  testRef("continue-decision"),
		AvailableDeliveryRouteRefs: []string{},
		DecisionNeeded:             DecisionRevise, RemedyRefs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutDiagnostic(diagnostic); err != nil {
		t.Fatalf("PutDiagnostic() error = %v", err)
	}
	loadedDiagnostic, err := store.ReadDiagnostic(diagnostic.DiagnosticRef)
	if err != nil || loadedDiagnostic.DiagnosticRef != diagnostic.DiagnosticRef {
		t.Fatalf("ReadDiagnostic() = %#v, %v", loadedDiagnostic, err)
	}

	for _, path := range []string{
		mustObjectPath(t, store, "tickets", ticket.TicketRef, ".json"),
		mustObjectPath(t, store, "executions", evidence.EvidenceRef, ".json"),
		mustObjectPath(t, store, "diagnostics", diagnostic.DiagnosticRef, ".json"),
	} {
		info, err := os.Stat(path)
		if err != nil || !privateEvidencePathSafe(path, info) {
			t.Fatalf("immutable object %q is not owner-only: mode=%v, err=%v", path, info.Mode().Perm(), err)
		}
	}
}

func TestStoreRejectsConflictingTicketAndExecutionReplay(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(context.Background(), initEvidenceRepository(t))
	if err != nil {
		t.Fatal(err)
	}
	ticket := mustActionTicket(t, "exit", "0")
	if _, err := store.PutTicket(ticket); err != nil {
		t.Fatal(err)
	}

	changedRequest := requestFromTicket(ticket)
	changedRequest.Args = append(changedRequest.Args, "different")
	conflictingTicket, err := NewActionTicket(changedRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PutTicket(conflictingTicket)
	var conflict *ImmutableConflictError
	if !errors.As(err, &conflict) || conflict.Binding != "ticket-slots" {
		t.Fatalf("conflicting ticket error = %v, want ticket-slot conflict", err)
	}

	process := executeTicket(t, context.Background(), ticket, false)
	first, err := AdmitExecution(ticket, process)
	if err != nil {
		t.Fatal(err)
	}
	replayedProcess := executeTicket(t, context.Background(), ticket, false)
	second, err := AdmitExecution(ticket, replayedProcess)
	if err != nil {
		t.Fatal(err)
	}
	if second.EvidenceRef == first.EvidenceRef {
		t.Fatal("different execution facts produced the same evidence ref")
	}
	type executionResult struct {
		evidence ExecutionEvidence
		err      error
	}
	results := make(chan executionResult, 2)
	go func() {
		_, putErr := store.PutExecution(first)
		results <- executionResult{evidence: first, err: putErr}
	}()
	go func() {
		_, putErr := store.PutExecution(second)
		results <- executionResult{evidence: second, err: putErr}
	}()
	var winner, loser ExecutionEvidence
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			winner = result.evidence
		case errors.As(result.err, &conflict) && conflict.Binding == "ticket-executions":
			loser = result.evidence
		default:
			t.Fatalf("concurrent PutExecution() error = %v", result.err)
		}
	}
	if winner.EvidenceRef == "" || loser.EvidenceRef == "" {
		t.Fatalf("concurrent execution winner/loser = %q/%q", winner.EvidenceRef, loser.EvidenceRef)
	}
	loaded, err := store.ReadExecutionForTicket(ticket.TicketRef)
	if err != nil || loaded.EvidenceRef != winner.EvidenceRef {
		t.Fatalf("execution binding did not preserve its winner: %#v, %v", loaded, err)
	}
	_, err = store.ReadExecution(loser.EvidenceRef)
	if !errors.As(err, &conflict) ||
		conflict.Binding != "ticket-executions" ||
		conflict.ExistingRef != winner.EvidenceRef ||
		conflict.ProposedRef != loser.EvidenceRef {
		t.Fatalf("losing execution replay error = %v, want exact ticket-execution conflict", err)
	}
}

func TestStoreRequiresAdmissionSealButTrustedReplayRestoresIt(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(context.Background(), initEvidenceRepository(t))
	if err != nil {
		t.Fatal(err)
	}
	ticket := mustActionTicket(t, "exit", "0")
	if _, err := store.PutTicket(ticket); err != nil {
		t.Fatal(err)
	}
	process := executeTicket(t, context.Background(), ticket, false)
	admitted, err := AdmitExecution(ticket, process)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(admitted)
	if err != nil {
		t.Fatal(err)
	}
	var shapeOnly ExecutionEvidence
	if err := json.Unmarshal(payload, &shapeOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutExecution(shapeOnly); err == nil {
		t.Fatal("PutExecution() accepted structurally valid JSON without EPD admission provenance")
	}
	if _, err := store.PutExecution(admitted); err != nil {
		t.Fatal(err)
	}
	replayed, err := store.ReadExecution(admitted.EvidenceRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := replayed.ValidateFor(ticket); err != nil {
		t.Fatalf("trusted durable replay was not re-admitted: %v", err)
	}
	if _, err := store.PutExecution(replayed); err != nil {
		t.Fatalf("trusted exact replay could not be stored idempotently: %v", err)
	}
}

func TestStoreConcurrentExactAndConflictingSlotPublication(t *testing.T) {
	t.Parallel()

	t.Run("same ticket is idempotent", func(t *testing.T) {
		store, err := OpenStore(context.Background(), initEvidenceRepository(t))
		if err != nil {
			t.Fatal(err)
		}
		ticket := mustActionTicket(t, "exit", "0")
		const workers = 12
		results := make(chan error, workers)
		var wait sync.WaitGroup
		for range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				_, err := store.PutTicket(ticket)
				results <- err
			}()
		}
		wait.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatalf("concurrent exact PutTicket() error = %v", err)
			}
		}
	})

	t.Run("issuer cannot evade one slot", func(t *testing.T) {
		store, err := OpenStore(context.Background(), initEvidenceRepository(t))
		if err != nil {
			t.Fatal(err)
		}
		request := actionTicketRequest(t, "exit", "0")
		first, err := NewActionTicket(request)
		if err != nil {
			t.Fatal(err)
		}
		request.IssuerRef = testRef("other-issuer")
		second, err := NewActionTicket(request)
		if err != nil {
			t.Fatal(err)
		}
		firstSlot, _ := first.SlotBindingRef()
		secondSlot, _ := second.SlotBindingRef()
		if firstSlot != secondSlot {
			t.Fatal("issuer change evaded subject/revision/context/slot identity")
		}
		type ticketResult struct {
			ticket ActionTicket
			err    error
		}
		results := make(chan ticketResult, 2)
		go func() {
			_, putErr := store.PutTicket(first)
			results <- ticketResult{ticket: first, err: putErr}
		}()
		go func() {
			_, putErr := store.PutTicket(second)
			results <- ticketResult{ticket: second, err: putErr}
		}()
		successes, conflicts := 0, 0
		var winner, loser ActionTicket
		for range 2 {
			result := <-results
			var conflict *ImmutableConflictError
			switch {
			case result.err == nil:
				successes++
				winner = result.ticket
			case errors.As(result.err, &conflict) && conflict.Binding == "ticket-slots":
				conflicts++
				loser = result.ticket
			default:
				t.Fatalf("concurrent conflicting PutTicket() error = %v", result.err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("success/conflict = %d/%d, want 1/1", successes, conflicts)
		}
		if loaded, err := store.ReadTicket(winner.TicketRef); err != nil ||
			loaded.TicketRef != winner.TicketRef {
			t.Fatalf("winning ticket replay = %q, %v", loaded.TicketRef, err)
		}
		var conflict *ImmutableConflictError
		if _, err := store.ReadTicket(loser.TicketRef); !errors.As(err, &conflict) ||
			conflict.Binding != "ticket-slots" ||
			conflict.ExistingRef != winner.TicketRef ||
			conflict.ProposedRef != loser.TicketRef {
			t.Fatalf("losing ticket replay error = %v, want exact slot conflict", err)
		}
		process := executeTicket(t, context.Background(), loser, false)
		evidence, err := AdmitExecution(loser, process)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutExecution(evidence); !errors.As(err, &conflict) ||
			conflict.Binding != "ticket-slots" ||
			conflict.ExistingRef != winner.TicketRef ||
			conflict.ProposedRef != loser.TicketRef {
			t.Fatalf("losing issuer execution error = %v, want exact slot conflict", err)
		}
		if _, err := store.ReadExecution(evidence.EvidenceRef); !errors.Is(err, ErrEvidenceNotFound) {
			t.Fatalf("losing issuer execution was published: %v", err)
		}
	})
}

func TestStoreFailsClosedOnTamperAndDoesNotCreateMissingReadShards(t *testing.T) {
	t.Parallel()

	store, err := OpenStore(context.Background(), initEvidenceRepository(t))
	if err != nil {
		t.Fatal(err)
	}
	missing := testRef("missing-execution")
	if _, err := store.ReadExecution(missing); !errors.Is(err, ErrEvidenceNotFound) {
		t.Fatalf("ReadExecution(missing) error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(store.Root(), "executions")); !os.IsNotExist(err) {
		t.Fatal("read of a missing evidence ref mutated the store")
	}

	ticket := mustActionTicket(t, "exit", "0")
	if _, err := store.PutTicket(ticket); err != nil {
		t.Fatal(err)
	}
	process := executeTicket(t, context.Background(), ticket, false)
	evidence, err := AdmitExecution(ticket, process)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutExecution(evidence); err != nil {
		t.Fatal(err)
	}
	path := mustObjectPath(t, store, "executions", evidence.EvidenceRef, ".json")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(payload, []byte(`"outcome":"complete"`), []byte(`"outcome":"failed"`), 1)
	if bytes.Equal(tampered, payload) {
		t.Fatal("test failed to locate outcome field")
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	var corruption *CorruptionError
	if _, err := store.ReadExecution(evidence.EvidenceRef); !errors.As(err, &corruption) {
		t.Fatalf("ReadExecution(tampered) error = %v, want corruption", err)
	}
}

func TestOpenStoreRejectsSymlinkedPrivateRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege is environment-dependent on Windows")
	}
	t.Parallel()

	repo := initEvidenceRepository(t)
	commonDir := strings.TrimSpace(runEvidenceGit(t, repo, "rev-parse", "--git-common-dir"))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repo, commonDir)
	}
	parent := filepath.Join(commonDir, "gentle-ai")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(parent, "execution-evidence")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(context.Background(), repo); err == nil {
		t.Fatal("OpenStore() followed a symlinked provider-private root")
	}
}

func TestCanonicalGitDirectoryRejectsAmbiguousOrEscapingRecords(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	tests := [][]byte{
		{},
		[]byte("--path-format=absolute\n"),
		[]byte(".git\nother\n"),
		[]byte("../outside"),
		[]byte(".git\x00outside"),
	}
	for _, record := range tests {
		if _, err := canonicalGitDirectory(base, record, false); err == nil {
			t.Fatalf("canonicalGitDirectory() accepted %q", record)
		}
	}
}

func TestCreatedStoreDirectoriesAreCrashSynced(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "first", "second")
	original := syncEvidenceDirectory
	var synced []string
	syncEvidenceDirectory = func(path string) error {
		synced = append(synced, filepath.Clean(path))
		return original(path)
	}
	t.Cleanup(func() { syncEvidenceDirectory = original })
	if err := ensureDirectoryBelow(base, target, 0o700, true); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		filepath.Join(base, "first"), base,
		target, filepath.Join(base, "first"),
	} {
		found := false
		for _, path := range synced {
			if path == filepath.Clean(required) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("new directory durability omitted fsync(%q): %v", required, synced)
		}
	}
}

func TestConcurrentObserversSyncExistingStoreDirectory(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "shared")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	original := syncEvidenceDirectory
	var lock sync.Mutex
	syncCounts := map[string]int{}
	syncEvidenceDirectory = func(path string) error {
		lock.Lock()
		syncCounts[filepath.Clean(path)]++
		lock.Unlock()
		return nil
	}
	t.Cleanup(func() { syncEvidenceDirectory = original })

	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- ensureDirectoryBelow(base, target, 0o755, false)
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent ensureDirectoryBelow() error = %v", err)
		}
	}

	lock.Lock()
	defer lock.Unlock()
	for _, path := range []string{target, base} {
		if got := syncCounts[filepath.Clean(path)]; got != workers {
			t.Fatalf("concurrent observers fsync(%q) count = %d, want %d", path, got, workers)
		}
	}
}

func TestPrivatePathValidationRejectsBroadPOSIXAuthority(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows authority is covered by ACL-specific tests")
	}
	path := filepath.Join(t.TempDir(), "broad")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if privateEvidencePathSafe(path, info) {
		t.Fatal("POSIX group/world permissions were accepted for private evidence")
	}
}

func initEvidenceRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	command := exec.Command("git", "init", "--quiet", repo)
	command.Env = sanitizedGitEnvironment(os.Environ())
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v: %s", err, output)
	}
	return repo
}

func runEvidenceGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"--no-replace-objects", "-C", repo}, args...)
	command := exec.Command("git", commandArgs...)
	command.Env = sanitizedGitEnvironment(os.Environ())
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}

func mustObjectPath(t *testing.T, store Store, kind, ref, suffix string) string {
	t.Helper()
	path, err := store.objectPath(kind, ref, suffix, false)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
