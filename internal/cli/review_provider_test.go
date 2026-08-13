package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

func TestReviewProviderRoleRegistryIsClosedAndSchemaValid(t *testing.T) {
	contracts := reviewProviderRoleContracts()
	if got := []string{contracts[0].ID, contracts[1].ID, contracts[2].ID}; !slices.Equal(got, []string{
		reviewProviderRoleLens, reviewProviderRoleRefuter, reviewProviderRoleTargetedValidator,
	}) {
		t.Fatalf("role registry = %v", got)
	}
	for _, contract := range contracts {
		t.Run(contract.ID, func(t *testing.T) {
			if !json.Valid(contract.ResultSchema) || contract.Slot == "" || contract.RequestSchemaID == "" || contract.ResultSchemaID == "" {
				t.Fatalf("invalid role contract: %#v", contract)
			}
			if !slices.Equal(contract.RequiredCapabilities, []string{reviewProviderTransportCapability}) {
				t.Fatalf("role capabilities = %v", contract.RequiredCapabilities)
			}
		})
	}
	if _, err := reviewProviderRoleContractFor("unknown"); err == nil {
		t.Fatal("unknown provider role was accepted")
	}
}

func TestReviewProviderMaterializationMatchesNativeLensContext(t *testing.T) {
	_, args, _, _ := newCandidateInspectionReview(t, "candidate\n", true)
	handle := args[slices.Index(args, "--repository-context")+1]
	lens := args[slices.Index(args, "--lens")+1]

	request, err := reviewProviderMaterialize(context.Background(), reviewLensContextDependencies(), handle, lens)
	if err != nil {
		t.Fatal(err)
	}
	var native bytes.Buffer
	if err := RunReview([]string{"lens-context", "--repository-context", handle, "--lens", lens}, &native); err != nil {
		t.Fatal(err)
	}
	if got := request.Invocation.Prompt(); !bytes.Equal(got, native.Bytes()) {
		t.Fatalf("provider materialization diverged from native lens context\nprovider:\n%s\nnative:\n%s", got, native.Bytes())
	}
}

func TestReviewProviderLensAdmissionUsesNativeValidator(t *testing.T) {
	repo, _, _, record := newArtifactReview(t, false)
	lens := record.State.SelectedLenses[0]
	raw := admittedReviewerPayloadForTest(t, repo, record, lens, 0)
	frozen, err := (reviewtransaction.SnapshotBuilder{Repo: repo}).FrozenCandidateContext(t.Context(), record.State.InitialSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := reviewtransaction.NewArtifactSubject(record.State, record.Revision, frozen, lens, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := reviewProviderAdmitLensRaw(t.Context(), repo, record.State, record.Revision, frozen, subject, raw)
	if err != nil || result.Lens != lens {
		t.Fatalf("provider lens admission = %#v, %v", result, err)
	}
	if _, err := reviewProviderExtractRoleRaw(reviewProviderRoleLens, append(raw, raw...)); err == nil {
		t.Fatal("multiple objects passed provider raw extraction")
	}
}
