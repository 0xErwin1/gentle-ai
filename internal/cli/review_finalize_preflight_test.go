package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

func TestNegotiatedReviewFinalizeRejectsReviewerPreflightWithoutAuthorityMutation(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		missing bool
	}{
		{name: "missing result file", missing: true},
		{name: "malformed JSON", payload: `{`},
		{name: "invalid shape", payload: `{}`},
		{name: "non-concrete exact sentinel", payload: `{"findings":[],"evidence":["tbd"]}`},
		{name: "selected lens mismatch", payload: `{"lens":"review-risk","findings":[],"evidence":["reviewed exact candidate tree"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := initReviewCLIRepo(t)
			if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("candidate\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			lineage := "review-preflight-" + reviewTestSlug(tt.name)
			var startedOutput bytes.Buffer
			if err := RunReviewFacadeStart([]string{"--cwd", repo, "--lineage", lineage}, &startedOutput); err != nil {
				t.Fatal(err)
			}
			var started ReviewFacadeStartResult
			decodeStrictReviewJSON(t, startedOutput.Bytes(), &started)
			if !reflect.DeepEqual(started.SelectedLenses, []string{reviewtransaction.LensReliability}) {
				t.Fatalf("selected lenses = %v, want reliability", started.SelectedLenses)
			}
			store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, lineage)
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			beforeBytes := readReviewOperationFile(t, store.StatePath())

			resultPath := filepath.Join(t.TempDir(), "reviewer.json")
			if !tt.missing {
				if err := os.WriteFile(resultPath, []byte(tt.payload), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			runErr := RunReview([]string{
				"finalize", "--contract", ReviewIntegrationContractV1, "--cwd", repo,
				"--lineage", lineage, "--result", resultPath,
			}, &output)
			if runErr == nil {
				t.Fatalf("invalid reviewer payload succeeded: %s", output.String())
			}
			failure := decodeReviewIntegrationFailure(t, output.Bytes())
			if failure.Operation != ReviewIntegrationOperationFinalize || failure.Phase != "preflight" ||
				failure.Code != "invalid_request" || failure.MutationOutcome != ReviewMutationNotStarted ||
				!failure.RetrySafe || failure.Replayability != reviewtransaction.ReplayabilityNotReplayable ||
				failure.NextAction != "correct_request" || failure.LineageID != lineage {
				t.Fatalf("preflight failure = %#v", failure)
			}
			after, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if after.Revision != before.Revision || !bytes.Equal(readReviewOperationFile(t, store.StatePath()), beforeBytes) {
				t.Fatalf("invalid reviewer payload changed compact authority: before=%s after=%s", before.Revision, after.Revision)
			}
		})
	}
}

func TestNegotiatedReviewFinalizeRetriesSameLineageAfterReviewerSchemaRejection(t *testing.T) {
	repo := initReviewCLIRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lineage := "review-preflight-retry"
	var startedOutput bytes.Buffer
	if err := RunReviewFacadeStart([]string{"--cwd", repo, "--lineage", lineage}, &startedOutput); err != nil {
		t.Fatal(err)
	}
	var started ReviewFacadeStartResult
	decodeStrictReviewJSON(t, startedOutput.Bytes(), &started)
	if !reflect.DeepEqual(started.SelectedLenses, []string{reviewtransaction.LensReliability}) {
		t.Fatalf("selected lenses = %v, want reliability", started.SelectedLenses)
	}
	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes := readReviewOperationFile(t, store.StatePath())

	resultPath := filepath.Join(t.TempDir(), "reviewer.json")
	if err := os.WriteFile(resultPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"finalize", "--contract", ReviewIntegrationContractV1, "--cwd", repo,
		"--lineage", lineage, "--result", resultPath,
	}
	var output bytes.Buffer
	if err := RunReview(args, &output); err == nil {
		t.Fatalf("invalid reviewer payload succeeded: %s", output.String())
	}
	failure := decodeReviewIntegrationFailure(t, output.Bytes())
	if failure.Phase != "preflight" || failure.MutationOutcome != ReviewMutationNotStarted || !failure.RetrySafe {
		t.Fatalf("preflight failure = %#v", failure)
	}
	rejected, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Revision != before.Revision || !bytes.Equal(readReviewOperationFile(t, store.StatePath()), beforeBytes) {
		t.Fatalf("schema rejection changed compact authority: before=%s after=%s", before.Revision, rejected.Revision)
	}
	if _, err := os.Stat(store.FinalizeAttemptJournalPath()); !os.IsNotExist(err) {
		t.Fatalf("schema rejection created finalize attempt journal: %v", err)
	}

	writeReviewCLIJSON(t, resultPath, facadeReviewerResult{
		Lens: reviewtransaction.LensReliability, Findings: []facadeFinding{}, Evidence: []string{"reviewed exact candidate tree"},
	})
	output.Reset()
	if err := RunReview(args, &output); err != nil {
		t.Fatalf("corrected reviewer payload retry: %v\n%s", err, output.String())
	}
	var finalized ReviewFacadeFinalizeResult
	decodeStrictReviewJSON(t, decodeReviewOperationEnvelope(t, output.Bytes()).Result, &finalized)
	after, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if finalized.LineageID != lineage || finalized.State != reviewtransaction.StateValidating ||
		finalized.StoreRevision != after.Revision || after.Revision == before.Revision {
		t.Fatalf("finalize retry result = %#v, authority = %#v", finalized, after)
	}
	if after.State.LineageID != before.State.LineageID || after.State.Generation != before.State.Generation ||
		after.State.CorrectionBudget != before.State.CorrectionBudget {
		t.Fatalf("finalize retry changed frozen review identity or budget: before=%#v after=%#v", before.State, after.State)
	}
}

func TestNegotiatedReviewFinalizeRequiresExplicitLineageWhenAuthorityIsAmbiguous(t *testing.T) {
	repo := initReviewCLIRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var startedOutput bytes.Buffer
	if err := RunReviewFacadeStart([]string{"--cwd", repo, "--lineage", "review-finalize-ambiguous-a"}, &startedOutput); err != nil {
		t.Fatal(err)
	}
	var started ReviewFacadeStartResult
	decodeStrictReviewJSON(t, startedOutput.Bytes(), &started)
	first, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, started.LineageID)
	if err != nil {
		t.Fatal(err)
	}
	firstRecord, err := first.Load()
	if err != nil {
		t.Fatal(err)
	}
	lines := firstRecord.State.OriginalChangedLines
	clone, err := reviewtransaction.NewCompactState(reviewtransaction.Start{
		LineageID: "review-finalize-ambiguous-b", Mode: reviewtransaction.ModeOrdinaryBounded, Generation: firstRecord.State.Generation,
		Snapshot: firstRecord.State.InitialSnapshot, PolicyHash: firstRecord.State.PolicyHash, RiskLevel: firstRecord.State.RiskLevel,
		SelectedLenses: append([]string{}, firstRecord.State.SelectedLenses...), OriginalChangedLines: &lines,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, clone.LineageID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Replace("", "review/start", clone); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, store := range []reviewtransaction.CompactStore{first, second} {
		before[store.StatePath()], err = os.ReadFile(store.StatePath())
		if err != nil {
			t.Fatal(err)
		}
	}
	resultPath := filepath.Join(t.TempDir(), "reviewer.json")
	writeReviewCLIJSON(t, resultPath, facadeReviewerResult{
		Lens: started.SelectedLenses[0], Findings: []facadeFinding{}, Evidence: []string{"reviewed exact candidate tree"},
	})
	args := []string{"finalize", "--contract", ReviewIntegrationContractV1, "--cwd", repo, "--result", resultPath}
	var output bytes.Buffer
	if err := RunReview(args, &output); err == nil {
		t.Fatalf("ambiguous FINALIZE error = %v\n%s", err, output.String())
	}
	failure := decodeReviewIntegrationFailure(t, output.Bytes())
	if failure.MutationOutcome != ReviewMutationUnknown || failure.Replayability != reviewtransaction.ReplayabilityStatusRequired || failure.NextAction != "review.status" {
		t.Fatalf("ambiguous FINALIZE failure = %#v", failure)
	}
	for _, store := range []reviewtransaction.CompactStore{first, second} {
		after, readErr := os.ReadFile(store.StatePath())
		if readErr != nil || !bytes.Equal(before[store.StatePath()], after) {
			t.Fatalf("ambiguous FINALIZE changed %s: %v", store.StatePath(), readErr)
		}
		if _, statErr := os.Stat(store.FinalizeAttemptJournalPath()); !os.IsNotExist(statErr) {
			t.Fatalf("ambiguous FINALIZE created journal for %s: %v", store.StatePath(), statErr)
		}
	}

	output.Reset()
	args = append(args, "--lineage", started.LineageID)
	if err := RunReview(args, &output); err != nil {
		t.Fatalf("explicit FINALIZE failed: %v\n%s", err, output.String())
	}
	finalized, err := first.Load()
	if err != nil || finalized.State.State != reviewtransaction.StateValidating {
		t.Fatalf("explicit FINALIZE authority = %#v, %v", finalized, err)
	}
	secondAfter, err := os.ReadFile(second.StatePath())
	if err != nil || !bytes.Equal(before[second.StatePath()], secondAfter) {
		t.Fatalf("explicit FINALIZE changed unselected authority: %v", err)
	}
}

func TestPrepareCompactReviewerResultsPreservesSelectedLensOrderAndAliases(t *testing.T) {
	selected := []string{
		reviewtransaction.LensRisk,
		reviewtransaction.LensResilience,
		reviewtransaction.LensReadability,
		reviewtransaction.LensReliability,
	}
	results := []facadeReviewerResult{
		{Lens: "risk", Findings: []facadeFinding{}, Evidence: []string{"risk review evidence"}},
		{Lens: reviewtransaction.LensResilience, Findings: []facadeFinding{}, Evidence: []string{"resilience review evidence"}},
		{Lens: "readability", Findings: []facadeFinding{}, Evidence: []string{"readability review evidence"}},
		{Lens: reviewtransaction.LensReliability, Findings: []facadeFinding{}, Evidence: []string{"reliability review evidence"}},
	}
	input, err := prepareCompactReviewerResults(reviewtransaction.CompactState{SelectedLenses: selected}, results, facadeRefuterResult{})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(input.LensResults))
	for index := range input.LensResults {
		got[index] = input.LensResults[index].Lens
	}
	if !reflect.DeepEqual(got, selected) {
		t.Fatalf("canonical lens order = %v, want %v", got, selected)
	}
}

func reviewTestSlug(value string) string {
	result := make([]byte, 0, len(value))
	separator := false
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			result = append(result, byte(char))
			separator = false
			continue
		}
		if len(result) > 0 && !separator {
			result = append(result, '-')
			separator = true
		}
	}
	for len(result) > 0 && result[len(result)-1] == '-' {
		result = result[:len(result)-1]
	}
	return string(result)
}
