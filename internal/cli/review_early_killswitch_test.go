package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

func TestReviewFacadeValidateDisabledShortCircuitsEveryGateAndStoredState(t *testing.T) {
	states := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "nonterminal without receipt",
			setup: func(t *testing.T, repo string) {
				writeReviewStartCandidate(t, repo, "candidate.md", "nonterminal\n", 0o644)
				startFacadeReview(t, repo)
			},
		},
		{
			name: "exact receipt",
			setup: func(t *testing.T, repo string) {
				writeReviewStartCandidate(t, repo, "candidate.md", "exact\n", 0o644)
				finalizeFacadeReviewForRepo(t, repo)
			},
		},
		{
			name: "stale receipt",
			setup: func(t *testing.T, repo string) {
				writeReviewStartCandidate(t, repo, "candidate.md", "reviewed\n", 0o644)
				finalizeFacadeReviewForRepo(t, repo)
				writeReviewStartCandidate(t, repo, "candidate.md", "stale\n", 0o644)
			},
		},
		{
			name: "candidate decline",
			setup: func(t *testing.T, repo string) {
				writeReviewStartCandidate(t, repo, "candidate.md", "declined\n", 0o644)
				startFacadeReview(t, repo)
				snapshot, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).Build(context.Background(), reviewtransaction.Target{
					Kind: reviewtransaction.TargetCurrentChanges, Projection: reviewtransaction.ProjectionWorkspace, IntendedUntracked: []string{},
				})
				if err != nil {
					t.Fatalf("build declined candidate snapshot: %v", err)
				}
				if _, err := reviewtransaction.RecordCandidateDecline(context.Background(), repo, snapshot); err != nil {
					t.Fatalf("record candidate decline: %v", err)
				}
			},
		},
	}
	gates := []reviewtransaction.GateKind{
		reviewtransaction.GatePostApply,
		reviewtransaction.GatePreCommit,
		reviewtransaction.GatePrePush,
		reviewtransaction.GatePrePR,
		reviewtransaction.GateRelease,
	}

	for _, state := range states {
		for _, gate := range gates {
			t.Run(state.name+"/"+string(gate), func(t *testing.T) {
				reviewModeHome(t)
				repo := initReviewCLIRepo(t)
				state.setup(t, repo)
				disableReviewForClone(t, repo)

				before := reviewAuthorityFiles(t, filepath.Join(repo, ".git", "gentle-ai"))
				if _, ok := before[filepath.ToSlash(filepath.Join("review-transactions", "v2", "LOCK"))]; !ok {
					t.Fatal("fixture did not persist review-transactions/v2/LOCK")
				}

				originalPrePush := facadePrePushDeliversNothing
				originalEvaluate := facadeEvaluateCompactGate
				prePushCalls, compactGateCalls := 0, 0
				facadePrePushDeliversNothing = func(context.Context, string, string) (bool, error) {
					prePushCalls++
					return false, errors.New("pre-push derivation must not run while RDD is off")
				}
				facadeEvaluateCompactGate = func(context.Context, string, reviewtransaction.CompactReceipt, reviewtransaction.NativeGateRequestInput) reviewtransaction.NativeGateEvaluation {
					compactGateCalls++
					return reviewtransaction.NativeGateEvaluation{}
				}
				t.Cleanup(func() {
					facadePrePushDeliversNothing = originalPrePush
					facadeEvaluateCompactGate = originalEvaluate
				})

				var output bytes.Buffer
				if err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(gate)}, &output); err != nil {
					t.Fatalf("disabled %s gate returned an error: %v\n%s", gate, err, output.String())
				}
				assertNeutralDisabledGate(t, output.Bytes(), gate)
				if prePushCalls != 0 {
					t.Fatalf("disabled %s gate derived a pre-push remote target %d time(s)", gate, prePushCalls)
				}
				if compactGateCalls != 0 {
					t.Fatalf("disabled %s gate reached compact evaluation and its release-evidence derivation %d time(s)", gate, compactGateCalls)
				}
				after := reviewAuthorityFiles(t, filepath.Join(repo, ".git", "gentle-ai"))
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("disabled %s gate changed .git/gentle-ai bytes:\nbefore=%#v\nafter=%#v", gate, before, after)
				}
			})
		}
	}
}

func TestReviewFacadeValidateDisabledPreflightStillFailsClosed(t *testing.T) {
	t.Run("unknown gate is rejected before the mode bypass", func(t *testing.T) {
		reviewModeHome(t)
		repo := initReviewCLIRepo(t)
		disableReviewForClone(t, repo)

		var output bytes.Buffer
		err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", "unknown"}, &output)
		if err == nil || !strings.Contains(err.Error(), "post-apply") || !strings.Contains(err.Error(), "release") {
			t.Fatalf("unknown gate error = %v, want the valid gate names", err)
		}
		if output.Len() != 0 {
			t.Fatalf("unknown gate emitted a delivery result: %s", output.String())
		}
	})

	t.Run("missing repository is rejected before the mode bypass", func(t *testing.T) {
		reviewModeHome(t)
		missing := filepath.Join(t.TempDir(), "missing")
		err := RunReviewFacadeValidate([]string{"--cwd", missing, "--gate", string(reviewtransaction.GatePreCommit)}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "resolve review repository root") {
			t.Fatalf("missing repository error = %v", err)
		}
	})

	t.Run("malformed mode remains managed", func(t *testing.T) {
		reviewModeHome(t)
		repo := initReviewCLIRepo(t)
		disableReviewForClone(t, repo)
		modePath, err := reviewtransaction.CloneLocalRDDModeRecordPath(context.Background(), repo)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(modePath, []byte("{\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		var output bytes.Buffer
		err = RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}, &output)
		var unreadable *ReviewModeUnreadableError
		if !errors.As(err, &unreadable) {
			t.Fatalf("malformed RDD mode error = %T %v, want ReviewModeUnreadableError", err, err)
		}
		if output.Len() != 0 {
			t.Fatalf("malformed RDD mode emitted a disabled bypass: %s", output.String())
		}
	})
}

func TestReviewFacadeValidateReenablePreservesAuthorityAndRequiresFreshValidation(t *testing.T) {
	reviewModeHome(t)
	repo := initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "candidate.md", "reviewed\n", 0o644)
	started := startFacadeReview(t, repo)
	evidencePath := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(evidencePath, []byte("focused tests pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	finalizeArgs := append([]string{"--cwd", repo, "--lineage", started.LineageID}, facadeReviewerResultArgs(t, repo, started)...)
	if err := RunReviewFacadeFinalize(append(finalizeArgs, "--evidence", evidencePath), io.Discard); err != nil {
		t.Fatal(err)
	}
	runReviewCLIGit(t, repo, "add", "candidate.md")

	store, err := reviewtransaction.CompactAuthoritativeStore(context.Background(), repo, started.LineageID)
	if err != nil {
		t.Fatal(err)
	}
	receiptBefore, err := os.ReadFile(store.ReceiptPath())
	if err != nil {
		t.Fatal(err)
	}
	transactionsRoot := filepath.Dir(store.Dir)
	transactionsBefore := reviewAuthorityFiles(t, transactionsRoot)

	disableReviewForClone(t, repo)
	var disabled bytes.Buffer
	if err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}, &disabled); err != nil {
		t.Fatalf("disabled exact receipt gate: %v\n%s", err, disabled.String())
	}
	assertNeutralDisabledGate(t, disabled.Bytes(), reviewtransaction.GatePreCommit)
	if after := reviewAuthorityFiles(t, transactionsRoot); !reflect.DeepEqual(after, transactionsBefore) {
		t.Fatalf("disabled gate changed preserved review authority:\nbefore=%#v\nafter=%#v", transactionsBefore, after)
	}

	var enabled bytes.Buffer
	if err := RunReviewMode([]string{"enable", "--cwd", repo, "--scope", "clone", "--json"}, &enabled); err != nil {
		t.Fatalf("re-enable review mode: %v\n%s", err, enabled.String())
	}
	if after := reviewAuthorityFiles(t, transactionsRoot); !reflect.DeepEqual(after, transactionsBefore) {
		t.Fatalf("re-enabling rewrote preserved review authority:\nbefore=%#v\nafter=%#v", transactionsBefore, after)
	}

	var exact bytes.Buffer
	if err := RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}, &exact); err != nil {
		t.Fatalf("exact receipt did not resume normal validation after re-enable: %v\n%s", err, exact.String())
	}
	assertReviewGateResult(t, exact.Bytes(), reviewtransaction.GateAllow)

	writeReviewStartCandidate(t, repo, "candidate.md", "new delivery\n", 0o644)
	runReviewCLIGit(t, repo, "add", "candidate.md")
	var stale bytes.Buffer
	err = RunReviewFacadeValidate([]string{"--cwd", repo, "--gate", string(reviewtransaction.GatePreCommit)}, &stale)
	var denied ReviewGateDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("re-enabled stale delivery did not require fresh validation: %T %v\n%s", err, err, stale.String())
	}
	receiptAfter, err := os.ReadFile(store.ReceiptPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(receiptAfter, receiptBefore) {
		t.Fatal("re-enabled stale validation rewrote the old receipt")
	}
}

func assertNeutralDisabledGate(t *testing.T, payload []byte, gate reviewtransaction.GateKind) {
	t.Helper()
	if fields := strictReviewJSONFields(t, payload); !reflect.DeepEqual(fields, []string{"action", "allowed", "context", "delivery", "reason", "result", "schema"}) {
		t.Fatalf("disabled gate fields = %v", fields)
	}
	var result ReviewValidateResult
	decodeStrictReviewJSON(t, payload, &result)
	wantContext := reviewtransaction.GateContext{
		Gate:   gate,
		Denial: &reviewtransaction.GateDenial{Stage: "rdd-mode", Code: "rdd_disabled"},
	}
	if result.Schema != ReviewValidateSchema || result.Result != reviewtransaction.GateInvalidated || result.Allowed ||
		result.Action != reviewDeliveryPolicyAction || result.Delivery != reviewtransaction.RDDDeliveryDisabledUnmanaged ||
		result.Reason != reviewDisabledUnmanagedReason || !reflect.DeepEqual(result.Context, wantContext) {
		t.Fatalf("disabled gate result = %#v, want neutral disabled/unmanaged result", result)
	}
	var raw struct {
		Context map[string]json.RawMessage `json:"context"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Context) != 2 || raw.Context["gate"] == nil || raw.Context["denial"] == nil {
		t.Fatalf("disabled gate leaked review context: %s", payload)
	}
}

func reviewAuthorityFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unexpected non-regular authority entry %s", path)
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = payload
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot authority bytes: %v", err)
	}
	return files
}
