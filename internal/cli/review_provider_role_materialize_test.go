package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewerprovider"
	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// piRefuterReview builds a reviewing authority whose one captured lens carries
// a severe inferential finding, so the transaction-wide refuter batch is
// required before finalize.
func piRefuterReview(t *testing.T) (string, reviewtransaction.CompactStore, reviewtransaction.CompactRecord, string) {
	t.Helper()
	repo, started, store, record := newArtifactReview(t, false)
	result := admittedReviewerResultForTest(t, repo, record, record.State.SelectedLenses[0], 0)
	result.Findings = []facadeFinding{{
		ID: "R3-001", Location: "tracked.txt:1", Severity: "CRITICAL", Claim: "candidate failure",
		ProofRefs: []string{"tracked.txt:1 candidate-specific proof"}, EvidenceClass: reviewtransaction.EvidenceInferential,
		CausalDisposition: reviewtransaction.CausalBehaviorActivated,
	}}
	input := filepath.Join(t.TempDir(), "result.json")
	writeReviewCLIJSON(t, input, result)
	if err := RunReviewCaptureResult([]string{
		"--cwd", repo, "--lineage", started.LineageID, "--target", record.State.InitialSnapshot.Identity,
		"--lens", record.State.SelectedLenses[0], "--order", "0", "--input", input,
	}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	handle, err := reviewtransaction.PublishReviewRepositoryContext(t.Context(), repo, reviewtransaction.ReviewRepositoryContextBinding{
		LineageID: record.State.LineageID, TargetIdentity: record.State.InitialSnapshot.Identity, Revision: record.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	return repo, store, record, handle
}

func piRefuterBinding(record reviewtransaction.CompactRecord, handle string) []string {
	return []string{
		"--repository-context", handle, "--lineage", record.State.LineageID,
		"--target", record.State.InitialSnapshot.Identity, "--expected-revision", record.Revision,
	}
}

func piRefuterRawResult(t *testing.T, repo string, store reviewtransaction.CompactStore, record reviewtransaction.CompactRecord) []byte {
	t.Helper()
	request, err := reviewProviderNewRefuterRequest(t.Context(), repo, store.Dir, record.State, record.Revision)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(facadeRefuterResult{RequestHash: request.RequestHash, Results: []facadeRefuterOutcome{{
		FindingID: "R3-001", Outcome: reviewtransaction.OutcomeCorroborated, ProofRefs: []string{"independent reproduction"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReviewCaptureRefuterMaterializePrintsPiProviderTaskWithoutCapturing(t *testing.T) {
	t.Setenv(reviewPiHostRelayContractEnvironment, reviewPiHostRelayContract)
	repo, store, record, handle := piRefuterReview(t)
	binding := piRefuterBinding(record, handle)

	var first bytes.Buffer
	if err := RunReview(append(append([]string{"capture-refuter"}, binding...), "--agent", string(model.AgentPi), "--materialize=true"), &first); err != nil {
		t.Fatal(err)
	}
	request, err := reviewProviderNewRefuterRequest(t.Context(), repo, store.Dir, record.State, record.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), request.Invocation.Prompt()) {
		t.Fatalf("materialized bytes diverged from the Go-materialized refuter request\nmaterialize:\n%s\nnative:\n%s", first.Bytes(), request.Invocation.Prompt())
	}
	var second bytes.Buffer
	if err := RunReview(append(append([]string{"capture-refuter"}, binding...), "--agent", string(model.AgentPi), "--materialize=true"), &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("repeated refuter materialization changed the provider task bytes")
	}
	slot, err := reviewtransaction.ReadCompactRefuterResultSlot(store.Dir)
	if err != nil || slot.Occupied {
		t.Fatalf("refuter materialize occupied the refuter result slot: %#v, %v", slot, err)
	}
}

func TestReviewCaptureRefuterMaterializeRefusesWithoutInferentialFindings(t *testing.T) {
	t.Setenv(reviewPiHostRelayContractEnvironment, reviewPiHostRelayContract)
	repo, started, _, record := newArtifactReview(t, false)
	input := filepath.Join(t.TempDir(), "result.json")
	if err := os.WriteFile(input, admittedReviewerPayloadForTest(t, repo, record, record.State.SelectedLenses[0], 0), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunReviewCaptureResult([]string{
		"--cwd", repo, "--lineage", started.LineageID, "--target", record.State.InitialSnapshot.Identity,
		"--lens", record.State.SelectedLenses[0], "--order", "0", "--input", input,
	}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	handle, err := reviewtransaction.PublishReviewRepositoryContext(t.Context(), repo, reviewtransaction.ReviewRepositoryContextBinding{
		LineageID: record.State.LineageID, TargetIdentity: record.State.InitialSnapshot.Identity, Revision: record.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = RunReview(append(append([]string{"capture-refuter"}, piRefuterBinding(record, handle)...), "--agent", string(model.AgentPi), "--materialize=true"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no inferential findings") {
		t.Fatalf("refuter-not-required materialize refusal = %v", err)
	}
}

func TestReviewCaptureRefuterSubmitsRawBytesAndHostMediatedFinalizeDiscoversSlot(t *testing.T) {
	t.Setenv(reviewPiHostRelayContractEnvironment, reviewPiHostRelayContract)
	repo, store, record, handle := piRefuterReview(t)
	binding := piRefuterBinding(record, handle)
	if err := RunReview(append(append([]string{"capture-refuter"}, binding...), "--agent", string(model.AgentPi), "--materialize=true"), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	raw := piRefuterRawResult(t, repo, store, record)
	input := filepath.Join(t.TempDir(), "refuter.json")
	if err := os.WriteFile(input, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RunReview(append(append([]string{"capture-refuter"}, binding...), "--input", input), &output); err != nil {
		t.Fatal(err)
	}
	var artifact reviewProviderRoleCaptureArtifact
	decodeStrictReviewJSON(t, output.Bytes(), &artifact)
	if artifact.Schema != reviewProviderRoleCaptureSchema || artifact.Role != string(reviewerprovider.RoleRefuter) ||
		artifact.LineageID != record.State.LineageID || artifact.TargetIdentity != record.State.InitialSnapshot.Identity || !artifact.Captured {
		t.Fatalf("refuter capture artifact = %#v", artifact)
	}
	slot, err := reviewtransaction.ReadCompactRefuterResultSlot(store.Dir)
	if err != nil || !slot.Occupied {
		t.Fatalf("refuter submission did not occupy the refuter result slot: %#v, %v", slot, err)
	}
	// The ordinary host-mediated finalize (no --agent) must discover the
	// occupied slot exactly as the compiled path does.
	if err := RunReviewFacadeFinalize([]string{"--cwd", repo, "--lineage", record.State.LineageID, "--captured-results=true"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("host-mediated finalize did not discover the captured refuter slot: %v", err)
	}
	final, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if final.State.State != reviewtransaction.StateCorrectionRequired {
		t.Fatalf("finalize state = %q, want corroborated blocking finding to require correction", final.State.State)
	}
}

func TestReviewCaptureRefuterRefusals(t *testing.T) {
	fakeBinding := []string{
		"--repository-context", "rctx1_" + strings.Repeat("0", 64),
		"--lineage", "role-materialize-refusals", "--target", "sha256:" + strings.Repeat("0", 64),
		"--expected-revision", "sha256:" + strings.Repeat("1", 64),
	}
	tests := []struct {
		name string
		env  string
		argv []string
		want string
	}{
		{
			name: "compiled claude-code runtime", env: reviewPiHostRelayContract,
			argv: append(slices.Clone(fakeBinding), "--agent", string(model.AgentClaudeCode), "--materialize=true"),
			want: "materializes internally",
		},
		{
			name: "compiled codex runtime", env: reviewPiHostRelayContract,
			argv: append(slices.Clone(fakeBinding), "--agent", string(model.AgentCodex), "--materialize=true"),
			want: "materializes internally",
		},
		{
			name: "opencode keeps its host-mediated refusal", env: reviewPiHostRelayContract,
			argv: append(slices.Clone(fakeBinding), "--agent", string(model.AgentOpenCode), "--materialize=true"),
			want: "is host-mediated; use its live transport collection",
		},
		{
			name: "combined with input", env: reviewPiHostRelayContract,
			argv: append(slices.Clone(fakeBinding), "--agent", string(model.AgentPi), "--materialize=true", "--input", "-"),
			want: "cannot be combined with --input",
		},
		{
			name: "without agent", env: reviewPiHostRelayContract,
			argv: append(slices.Clone(fakeBinding), "--materialize=true"),
			want: "requires --agent",
		},
		{
			name: "agent without materialize", env: reviewPiHostRelayContract,
			argv: append(slices.Clone(fakeBinding), "--agent", string(model.AgentPi)),
			want: "only selects the host-relay materialize form",
		},
		{
			name: "without relay handshake", env: "",
			argv: append(slices.Clone(fakeBinding), "--agent", string(model.AgentPi), "--materialize=true"),
			want: "not eligible for immutable receipt review",
		},
		{
			name: "without materialize or input", env: reviewPiHostRelayContract,
			argv: slices.Clone(fakeBinding),
			want: "either --materialize",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(reviewPiHostRelayContractEnvironment, test.env)
			err := RunReview(append([]string{"capture-refuter"}, test.argv...), io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("capture-refuter refusal = %v, want %q", err, test.want)
			}
			validationErr := RunReview(append([]string{"capture-validation"}, append(slices.Clone(test.argv), "--request-hash", "sha256:"+strings.Repeat("2", 64))...), io.Discard)
			if validationErr == nil || !strings.Contains(validationErr.Error(), test.want) {
				t.Fatalf("capture-validation refusal = %v, want %q", validationErr, test.want)
			}
		})
	}
}

func TestNegotiatedStatusRendersPiHostRelayRefuterCollectInput(t *testing.T) {
	t.Setenv(reviewPiHostRelayContractEnvironment, reviewPiHostRelayContract)
	repo, store, record, _ := piRefuterReview(t)
	var output bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--lineage", record.State.LineageID, "--contract", ReviewIntegrationContractV2,
		"--agent", string(model.AgentPi), "--next-transition",
	}, &output); err != nil {
		t.Fatal(err)
	}
	var status ReviewTargetStatusResult
	decodeStrictReviewJSON(t, output.Bytes(), &status)
	if err := status.Validate(); err != nil {
		t.Fatalf("pi host relay refuter STATUS is invalid: %v", err)
	}
	if status.NextTransition == nil || status.NextTransition.Kind != reviewNextTransitionCollect ||
		status.NextTransition.ReasonCode != "provider_refuter_required" || status.NextTransition.Collect == nil ||
		len(status.NextTransition.Collect.Inputs) != 1 {
		t.Fatalf("pi host relay refuter transition = %#v", status.NextTransition)
	}
	input := status.NextTransition.Collect.Inputs[0]
	if input.CaptureOperation != "review.capture-refuter" || input.Name != "provider_refuter" || input.Schema != reviewRefuterSchemaID || input.ProviderTask != nil {
		t.Fatalf("pi host relay refuter input = %#v", input)
	}
	tokens := map[string]string{}
	for _, argument := range input.Arguments {
		tokens[argument.Name] = argument.Token
	}
	if tokens["agent"] != "--agent="+string(model.AgentPi) || tokens["materialize"] != "--materialize=true" {
		t.Fatalf("pi host relay refuter arguments = %#v", input.Arguments)
	}
	wantTokens := make([]string, 0, len(input.Arguments)-1)
	for _, argument := range input.Arguments {
		if argument.Name != "agent" && argument.Name != "materialize" {
			wantTokens = append(wantTokens, argument.Token)
		}
	}
	wantTokens = append(wantTokens, "--input={{value}}")
	if input.Submission == nil || input.Submission.OperationToken != "capture-refuter" ||
		!slices.Equal(input.Submission.ArgumentTokens, wantTokens) || len(input.Submission.Values) != 0 ||
		input.Submission.Value == nil || input.Submission.Value.Slot != "refuter_result" ||
		input.Submission.Value.Domain != "artifact_path_or_stdin" || input.Submission.Value.Schema != reviewRefuterSchemaID ||
		input.Submission.Value.SubstitutionLocation != len(wantTokens)-1 {
		t.Fatalf("pi host relay refuter submission = %#v, want tokens %v", input.Submission, wantTokens)
	}

	// The OpenCode rendering stays byte-identical: a Go-issued provider task,
	// no submission descriptor. Checked before the submission below occupies
	// the refuter slot and retires this collection state.
	var opencode bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--lineage", record.State.LineageID, "--contract", ReviewIntegrationContractV2,
		"--agent", string(model.AgentOpenCode), "--next-transition",
	}, &opencode); err != nil {
		t.Fatal(err)
	}
	var opencodeStatus ReviewTargetStatusResult
	decodeStrictReviewJSON(t, opencode.Bytes(), &opencodeStatus)
	if opencodeStatus.NextTransition == nil || opencodeStatus.NextTransition.ReasonCode != "provider_refuter_required" ||
		opencodeStatus.NextTransition.Collect == nil || len(opencodeStatus.NextTransition.Collect.Inputs) != 1 {
		t.Fatalf("OpenCode refuter transition changed: %#v", opencodeStatus.NextTransition)
	}
	opencodeInput := opencodeStatus.NextTransition.Collect.Inputs[0]
	task := opencodeInput.ProviderTask
	if opencodeInput.CaptureOperation != "external.run_provider_role" || opencodeInput.Submission != nil ||
		task == nil || task.Agent != "review-refuter" || task.Role != string(reviewerprovider.RoleRefuter) ||
		!strings.HasPrefix(task.Prompt, reviewProviderTaskBindingHeader+" ") {
		t.Fatalf("OpenCode refuter rendering changed: %#v", opencodeInput)
	}

	// The rendered transition is executable exactly as issued: the materialize
	// prelude prints the Go-issued prompt, the submission advances authority.
	prelude := []string{"capture-refuter"}
	for _, argument := range input.Arguments {
		prelude = append(prelude, argument.Token)
	}
	var prompt bytes.Buffer
	if err := RunReview(prelude, &prompt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(prompt.Bytes(), []byte(record.State.LineageID)) {
		t.Fatal("rendered materialize prelude omitted the reviewing lineage")
	}
	raw := piRefuterRawResult(t, repo, store, record)
	inputPath := filepath.Join(t.TempDir(), "refuter.json")
	if err := os.WriteFile(inputPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	submission := []string{input.Submission.OperationToken}
	for _, token := range input.Submission.ArgumentTokens {
		submission = append(submission, strings.ReplaceAll(token, reviewSubmissionValuePlaceholder, inputPath))
	}
	if err := RunReview(submission, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	slot, err := reviewtransaction.ReadCompactRefuterResultSlot(store.Dir)
	if err != nil || !slot.Occupied {
		t.Fatalf("rendered submission did not occupy the refuter result slot: %#v, %v", slot, err)
	}
}

func TestNegotiatedStatusPiRefuterSlotOccupiedKeepsCompiledRenderings(t *testing.T) {
	t.Setenv(reviewPiHostRelayContractEnvironment, reviewPiHostRelayContract)
	repo, _, record, _ := piRefuterReview(t)
	// The compiled claude-code rendering stays byte-identical: no provider
	// role collection, the ordinary captured_results_ready finalize.
	var compiled bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--lineage", record.State.LineageID, "--contract", ReviewIntegrationContractV2,
		"--agent", string(model.AgentClaudeCode), "--next-transition",
	}, &compiled); err != nil {
		t.Fatal(err)
	}
	var compiledStatus ReviewTargetStatusResult
	decodeStrictReviewJSON(t, compiled.Bytes(), &compiledStatus)
	if compiledStatus.NextTransition == nil || compiledStatus.NextTransition.Kind != reviewNextTransitionExecute ||
		compiledStatus.NextTransition.ReasonCode != "captured_results_ready" ||
		compiledStatus.NextTransition.Execute == nil || compiledStatus.NextTransition.Execute.Operation != "review.finalize" {
		t.Fatalf("compiled refuter-state rendering changed: %#v", compiledStatus.NextTransition)
	}
}

func TestReviewCaptureValidationMaterializesSubmitsAndFinalizeDiscovers(t *testing.T) {
	t.Setenv(reviewPiHostRelayContractEnvironment, reviewPiHostRelayContract)
	repo, lineage, request := providerCorrectionReady(t)
	store, err := reviewtransaction.CompactAuthoritativeStore(t.Context(), repo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--lineage", lineage, "--contract", ReviewIntegrationContractV2,
		"--agent", string(model.AgentPi), "--next-transition",
	}, &output); err != nil {
		t.Fatal(err)
	}
	var status ReviewTargetStatusResult
	decodeStrictReviewJSON(t, output.Bytes(), &status)
	if err := status.Validate(); err != nil {
		t.Fatalf("pi host relay validation STATUS is invalid: %v", err)
	}
	if status.NextTransition == nil || status.NextTransition.Kind != reviewNextTransitionCollect ||
		status.NextTransition.ReasonCode != "targeted_validation_required" || status.NextTransition.Collect == nil ||
		len(status.NextTransition.Collect.Inputs) != 1 {
		t.Fatalf("pi host relay validation transition = %#v", status.NextTransition)
	}
	input := status.NextTransition.Collect.Inputs[0]
	if input.CaptureOperation != "review.capture-validation" || input.Schema != reviewValidatorSchemaID ||
		input.ProviderTask != nil || input.ValidationRequest == nil || input.ValidationRequest.RequestHash != request.RequestHash {
		t.Fatalf("pi host relay validation input = %#v", input)
	}
	arguments := map[string]string{}
	tokens := map[string]string{}
	for _, argument := range input.Arguments {
		arguments[argument.Name] = argument.Value
		tokens[argument.Name] = argument.Token
	}
	if arguments["request-hash"] != request.RequestHash || arguments["target"] != request.CorrectionTargetIdentity ||
		tokens["agent"] != "--agent="+string(model.AgentPi) || tokens["materialize"] != "--materialize=true" {
		t.Fatalf("pi host relay validation arguments = %#v", input.Arguments)
	}
	wantTokens := make([]string, 0, len(input.Arguments)-1)
	for _, argument := range input.Arguments {
		if argument.Name != "agent" && argument.Name != "materialize" {
			wantTokens = append(wantTokens, argument.Token)
		}
	}
	wantTokens = append(wantTokens, "--input={{value}}")
	if input.Submission == nil || input.Submission.OperationToken != "capture-validation" ||
		!slices.Equal(input.Submission.ArgumentTokens, wantTokens) || len(input.Submission.Values) != 0 ||
		input.Submission.Value == nil || input.Submission.Value.Slot != "validation_result" ||
		input.Submission.Value.Domain != "artifact_path_or_stdin" || input.Submission.Value.Schema != reviewValidatorSchemaID ||
		input.Submission.Value.SubstitutionLocation != len(wantTokens)-1 {
		t.Fatalf("pi host relay validation submission = %#v, want tokens %v", input.Submission, wantTokens)
	}

	// Materialize prelude: idempotent, byte-identical to the Go-materialized
	// validator request, and slot-free.
	prelude := []string{"capture-validation"}
	for _, argument := range input.Arguments {
		prelude = append(prelude, argument.Token)
	}
	var first bytes.Buffer
	if err := RunReview(slices.Clone(prelude), &first); err != nil {
		t.Fatal(err)
	}
	correction, err := reviewProviderTargetedValidatorCorrection(t.Context(), repo, record.State)
	if err != nil {
		t.Fatal(err)
	}
	native, err := reviewProviderNewTargetedValidatorRequest(t.Context(), repo, record.State, record.Revision, correction)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), native.Invocation.Prompt()) {
		t.Fatal("materialized validator bytes diverged from the Go-materialized request")
	}
	var second bytes.Buffer
	if err := RunReview(slices.Clone(prelude), &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("repeated validator materialization changed the provider task bytes")
	}
	slot, err := reviewtransaction.ReadCompactTargetedValidatorResultSlot(store.Dir, request)
	if err != nil || slot.Occupied {
		t.Fatalf("validator materialize occupied the result slot: %#v, %v", slot, err)
	}

	// Submission: the raw provider bytes are admitted in Go and occupy the
	// compact validator slot; the host-mediated finalize then discovers it.
	inputPath := filepath.Join(t.TempDir(), "validation.json")
	if err := os.WriteFile(inputPath, providerTargetedValidationPayload(t, request), 0o600); err != nil {
		t.Fatal(err)
	}
	submission := []string{input.Submission.OperationToken}
	for _, token := range input.Submission.ArgumentTokens {
		submission = append(submission, strings.ReplaceAll(token, reviewSubmissionValuePlaceholder, inputPath))
	}
	var captured bytes.Buffer
	if err := RunReview(submission, &captured); err != nil {
		t.Fatal(err)
	}
	var artifact reviewProviderRoleCaptureArtifact
	decodeStrictReviewJSON(t, captured.Bytes(), &artifact)
	if artifact.Schema != reviewProviderRoleCaptureSchema || artifact.Role != string(reviewerprovider.RoleTargetedValidator) ||
		artifact.LineageID != lineage || artifact.TargetIdentity != request.CorrectionTargetIdentity || !artifact.Captured {
		t.Fatalf("validator capture artifact = %#v", artifact)
	}
	slot, err = reviewtransaction.ReadCompactTargetedValidatorResultSlot(store.Dir, request)
	if err != nil || !slot.Occupied {
		t.Fatalf("validator submission did not occupy the result slot: %#v, %v", slot, err)
	}
	if err := RunReviewFacadeFinalize([]string{"--cwd", repo, "--lineage", lineage, "--captured-evidence=true"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("host-mediated finalize did not discover the captured validator slot: %v", err)
	}
	final, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if final.State.State == reviewtransaction.StateCorrectionRequired {
		t.Fatalf("finalize state = %q, want the passed validator verdict to close the correction", final.State.State)
	}
}

func TestReviewCaptureValidationBindsFrozenRequestHash(t *testing.T) {
	t.Setenv(reviewPiHostRelayContractEnvironment, reviewPiHostRelayContract)
	repo, lineage, request := providerCorrectionReady(t)
	store, err := reviewtransaction.CompactAuthoritativeStore(t.Context(), repo, lineage)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	handle, err := reviewtransaction.PublishReviewRepositoryContext(t.Context(), repo, reviewtransaction.ReviewRepositoryContextBinding{
		LineageID: lineage, TargetIdentity: request.CorrectionTargetIdentity, Revision: record.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := []string{
		"--repository-context", handle, "--lineage", lineage,
		"--target", request.CorrectionTargetIdentity, "--expected-revision", record.Revision,
	}
	stale := append(slices.Clone(binding), "--request-hash", "sha256:"+strings.Repeat("0", 64),
		"--agent", string(model.AgentPi), "--materialize=true")
	err = RunReview(append([]string{"capture-validation"}, stale...), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "request hash does not match") {
		t.Fatalf("stale validation request hash refusal = %v", err)
	}
	missing := append(slices.Clone(binding), "--agent", string(model.AgentPi), "--materialize=true")
	err = RunReview(append([]string{"capture-validation"}, missing...), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--request-hash") {
		t.Fatalf("missing validation request hash refusal = %v", err)
	}
}

func TestNegotiatedStatusKeepsExternalValidationRenderingsForOtherRuntimes(t *testing.T) {
	t.Setenv(reviewPiHostRelayContractEnvironment, reviewPiHostRelayContract)
	repo, lineage, _ := providerCorrectionReady(t)

	// claude-code keeps the external targeted-validation collection with its
	// finalize submission descriptor.
	var compiled bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--lineage", lineage, "--contract", ReviewIntegrationContractV2,
		"--agent", string(model.AgentClaudeCode), "--next-transition",
	}, &compiled); err != nil {
		t.Fatal(err)
	}
	var compiledStatus ReviewTargetStatusResult
	decodeStrictReviewJSON(t, compiled.Bytes(), &compiledStatus)
	if compiledStatus.NextTransition == nil || compiledStatus.NextTransition.ReasonCode != "targeted_validation_required" ||
		compiledStatus.NextTransition.Collect == nil || len(compiledStatus.NextTransition.Collect.Inputs) != 1 {
		t.Fatalf("compiled validation transition changed: %#v", compiledStatus.NextTransition)
	}
	compiledInput := compiledStatus.NextTransition.Collect.Inputs[0]
	if compiledInput.CaptureOperation != "external.run_targeted_validation" || compiledInput.Submission == nil ||
		compiledInput.Submission.OperationToken != "finalize" || compiledInput.ValidationRequest == nil {
		t.Fatalf("compiled validation rendering changed: %#v", compiledInput)
	}

	// OpenCode keeps its Go-issued provider validator task.
	var opencode bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--lineage", lineage, "--contract", ReviewIntegrationContractV2,
		"--agent", string(model.AgentOpenCode), "--next-transition",
	}, &opencode); err != nil {
		t.Fatal(err)
	}
	var opencodeStatus ReviewTargetStatusResult
	decodeStrictReviewJSON(t, opencode.Bytes(), &opencodeStatus)
	if opencodeStatus.NextTransition == nil || opencodeStatus.NextTransition.ReasonCode != "targeted_validation_required" ||
		opencodeStatus.NextTransition.Collect == nil || len(opencodeStatus.NextTransition.Collect.Inputs) != 1 {
		t.Fatalf("OpenCode validation transition changed: %#v", opencodeStatus.NextTransition)
	}
	opencodeInput := opencodeStatus.NextTransition.Collect.Inputs[0]
	task := opencodeInput.ProviderTask
	if opencodeInput.CaptureOperation != "external.run_provider_role" || opencodeInput.Submission != nil ||
		task == nil || task.Agent != "review-validator" || task.Role != string(reviewerprovider.RoleTargetedValidator) ||
		!strings.HasPrefix(task.Prompt, reviewProviderTaskBindingHeader+" ") {
		t.Fatalf("OpenCode validation rendering changed: %#v", opencodeInput)
	}
}
