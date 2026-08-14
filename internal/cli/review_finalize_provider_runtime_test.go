package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewerprovider"
)

// TestReviewFinalizeProviderRuntimeSurvivesContextLevelObservation drives the
// exact production path a compiled runtime uses: provider-executed lens
// capture followed by negotiated `finalize --captured-results --agent`. The
// finalize path observes ReviewerContextLevel onto the in-memory state before
// materializing the provider refuter request; that materialization re-derives
// the revision from the state it is handed and must keep matching the frozen
// record revision, or every compiled-runtime finalize dies with "provider
// refuter request requires the current compact authority revision".
func TestReviewFinalizeProviderRuntimeSurvivesContextLevelObservation(t *testing.T) {
	repo, args, record, _ := newCandidateInspectionReview(t, "candidate\n", true)
	handle := args[slices.Index(args, "--repository-context")+1]
	previous := reviewProviderAdapterFor
	t.Cleanup(func() { reviewProviderAdapterFor = previous })
	reviewProviderAdapterFor = func(_ reviewerprovider.Contract, agent model.AgentID) (reviewerprovider.Adapter, error) {
		if agent != model.AgentClaudeCode {
			return nil, errors.New("unexpected runtime")
		}
		return providerTestAdapterFunc(func(_ context.Context, _ reviewerprovider.Invocation) ([]byte, error) {
			return admittedReviewerPayloadForTest(t, repo, record, record.State.SelectedLenses[0], 0), nil
		}), nil
	}
	if err := RunReviewCaptureResult([]string{
		"--repository-context", handle,
		"--lineage", record.State.LineageID, "--target", record.State.InitialSnapshot.Identity,
		"--lens", record.State.SelectedLenses[0], "--order", "0", "--expected-revision", record.Revision,
		"--agent", string(model.AgentClaudeCode),
	}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	// The rendered transition must carry the contract binding its own
	// finalize preflight demands for --agent; a bare command that STATUS
	// advertises and finalize then refuses is a convergence break.
	var status bytes.Buffer
	if err := RunReview([]string{
		"status", "--contract", ReviewIntegrationContractV2, "--agent", string(model.AgentClaudeCode),
		"--next-transition", "--cwd", repo,
	}, &status); err != nil {
		t.Fatalf("negotiated status failed: %v\n%s", err, status.String())
	}
	command := reviewNextTransitionCommandForTest(t, status.Bytes())
	if !strings.Contains(command, "review finalize") ||
		!strings.Contains(command, "--contract="+ReviewIntegrationContractV2) ||
		!strings.Contains(command, "--agent="+string(model.AgentClaudeCode)) {
		t.Fatalf("rendered finalize transition lacks its own preflight bindings: %s", command)
	}

	var output bytes.Buffer
	if err := RunReviewFacadeFinalize([]string{
		"--contract", ReviewIntegrationContractV2, "--cwd", repo,
		"--lineage", record.State.LineageID, "--captured-results",
		"--agent", string(model.AgentClaudeCode),
	}, &output); err != nil {
		t.Fatalf("negotiated compiled-runtime finalize failed: %v\n%s", err, output.String())
	}
}

func reviewNextTransitionCommandForTest(t *testing.T, payload []byte) string {
	t.Helper()
	var decoded struct {
		NextTransition struct {
			Execute struct {
				Command string `json:"command"`
			} `json:"execute"`
		} `json:"next_transition"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode negotiated status: %v\n%s", err, payload)
	}
	return decoded.NextTransition.Execute.Command
}
