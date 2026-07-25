package workprovider

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/hostruntime"
	"github.com/gentleman-programming/gentle-ai/internal/reviewtransaction"
)

func TestProductiveVerificationConnectorRejectsBoundContractTampering(
	t *testing.T,
) {
	catalogRequest, catalog := productiveVerificationCatalogTestFixture(t)
	catalogSubject := catalog
	catalogSubject.Subject.CandidateTree = strings.Repeat("c", 40)
	catalogDeadline := catalog
	catalogDeadline.Actions = append(
		[]ProductiveVerificationAction(nil),
		catalog.Actions...,
	)
	catalogDeadline.Actions[0].DeadlineMilliseconds = math.MaxInt64
	reviewRequest, reviewResult := productiveReviewTestFixture(t)
	reviewSubject := reviewResult
	reviewSubject.SubjectHash = runtimeConnectorTestRef("foreign-review-subject")

	responses := []struct {
		operation ProductiveRuntimeOperation
		payload   any
	}{
		{
			operation: ProductiveRuntimeOperationVerificationCatalog,
			payload:   catalogSubject,
		},
		{
			operation: ProductiveRuntimeOperationVerificationCatalog,
			payload:   catalogDeadline,
		},
		{
			operation: ProductiveRuntimeOperationReview,
			payload:   reviewSubject,
		},
	}
	repositoryRef := runtimeConnectorTestRef("repository")
	var calls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		runtimeConnectorRequireHeaders(t, request)
		switch request.URL.Path {
		case ProductiveRuntimeBootstrapPathV1:
			bootstrap := runtimeConnectorDecodeBootstrap(t, request)
			runtimeConnectorWriteJSON(
				t,
				writer,
				runtimeConnectorBootstrapResponse(t, bootstrap),
			)
		case ProductiveRuntimeCallPathV1:
			call := runtimeConnectorDecodeCall(t, request)
			index := int(calls.Add(1) - 1)
			if index >= len(responses) ||
				call.Operation != responses[index].operation {
				t.Fatalf("unexpected runtime call %d: %q", index, call.Operation)
			}
			response, err := NewProductiveRuntimeResponse(
				call,
				responses[index].payload,
			)
			if err != nil {
				t.Fatal(err)
			}
			runtimeConnectorWriteJSON(t, writer, response)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	connector, err := NewHTTPSProductiveRuntimeConnector(
		context.Background(),
		runtimeConnectorTestConfig(server, repositoryRef, 0, 0),
		runtimeConnectorTestBearer,
	)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		invoke func() error
	}{
		{
			name: "catalog subject",
			invoke: func() error {
				_, err := connector.ResolveVerificationCatalog(
					context.Background(),
					catalogRequest,
				)
				return err
			},
		},
		{
			name: "catalog deadline overflow",
			invoke: func() error {
				_, err := connector.ResolveVerificationCatalog(
					context.Background(),
					catalogRequest,
				)
				return err
			},
		},
		{
			name: "review subject",
			invoke: func() error {
				_, err := connector.ReviewCandidate(
					context.Background(),
					reviewRequest,
				)
				return err
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.invoke(); !errors.Is(
				err,
				ErrProductiveRuntimeBindingMismatch,
			) {
				t.Fatalf("bound contract tampering error = %v", err)
			}
			if want := int64(index + 1); calls.Load() != want {
				t.Fatalf(
					"owner authority calls = %d, want %d",
					calls.Load(),
					want,
				)
			}
		})
	}

	t.Run("review manifest before transport", func(t *testing.T) {
		tampered := reviewRequest
		tampered.ChangedPathManifest = append(
			[]reviewtransaction.ChangedPathManifestEntry(nil),
			reviewRequest.ChangedPathManifest...,
		)
		tampered.ChangedPathManifest[0].Path = "internal/other.go"
		_, err := connector.ReviewCandidate(context.Background(), tampered)
		if err == nil || !strings.Contains(
			err.Error(),
			"manifest does not bind its artifact subject",
		) {
			t.Fatalf("review manifest tampering error = %v", err)
		}
		if calls.Load() != int64(len(responses)) {
			t.Fatalf(
				"invalid review manifest reached owner authority %d times",
				calls.Load(),
			)
		}
	})
}

func productiveVerificationCatalogTestFixture(
	t *testing.T,
) (ProductiveVerificationCatalogRequest, ProductiveVerificationCatalog) {
	t.Helper()
	subject := reviewtransaction.VerificationSubject{
		Kind:             reviewtransaction.TargetCurrentChanges,
		BaseTree:         strings.Repeat("a", 40),
		CandidateTree:    strings.Repeat("b", 40),
		PathsDigest:      runtimeConnectorTestRef("verification-paths"),
		SnapshotIdentity: runtimeConnectorTestRef("verification-snapshot"),
	}
	request, err := NewProductiveVerificationCatalogRequest(
		subject,
		[]string{"internal/app.go"},
		reviewtransaction.RiskAssessment{
			Level:        reviewtransaction.RiskMedium,
			ChangedLines: 12,
			Reasons: []reviewtransaction.RiskReason{{
				Code: reviewtransaction.RiskReasonExecutableChange,
				Path: "internal/app.go",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	catalog := ProductiveVerificationCatalog{
		Schema:  ProductiveVerificationCatalogSchemaV1,
		Subject: subject,
		Actions: []ProductiveVerificationAction{{
			ID:                   "go-test",
			Program:              program,
			Args:                 []string{"-test.run=^TestRuntimeConnectorHelperProcess$"},
			CWD:                  t.TempDir(),
			Environment:          map[string]string{"GO_WANT_RUNTIME_CONNECTOR_HELPER": "1"},
			Capability:           "go.test",
			Cost:                 reviewtransaction.VerificationCostQuick,
			RequiresConsent:      false,
			DeadlineMilliseconds: 5_000,
			OutputLimits: hostruntime.StreamLimits{
				StdoutBytes: 1 << 10,
				StderrBytes: 1 << 10,
			},
			RedactionLiterals: []string{},
		}},
	}
	if err := catalog.ValidateFor(request); err != nil {
		t.Fatal(err)
	}
	return request, catalog
}

func productiveReviewTestFixture(
	t *testing.T,
) (ProductiveReviewRequest, ProductiveReviewResult) {
	t.Helper()
	diff, err := reviewtransaction.NewFrozenCandidateDiff(
		[]byte("diff --git a/internal/app.go b/internal/app.go\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"internal/app.go"}
	frozen := reviewtransaction.FrozenCandidateContext{
		CandidateDiff: diff,
		ChangedPathManifest: []reviewtransaction.ChangedPathManifestEntry{{
			Path:    paths[0],
			Status:  reviewtransaction.CandidatePathModified,
			OldMode: "100644",
			NewMode: "100644",
		}},
	}
	state := reviewtransaction.CompactState{
		LineageID:      "runtime-connector-review",
		SelectedLenses: []string{reviewtransaction.LensReliability},
		InitialSnapshot: reviewtransaction.Snapshot{
			Identity: runtimeConnectorTestRef("review-snapshot"),
			Paths:    paths,
		},
	}
	subject, err := reviewtransaction.NewArtifactSubject(
		state,
		runtimeConnectorTestRef("review-authority"),
		frozen,
		reviewtransaction.LensReliability,
		0,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewProductiveReviewRequest(subject, frozen)
	if err != nil {
		t.Fatal(err)
	}
	result := ProductiveReviewResult{
		Schema:      ProductiveReviewResultSchemaV1,
		SubjectHash: subject.SubjectHash,
		Inspection: reviewtransaction.ArtifactInspection{
			Status: reviewtransaction.ArtifactInspectionCompleted,
			Paths:  append([]string(nil), paths...),
		},
		Findings: []reviewtransaction.Finding{},
		Evidence: []string{"inspected internal/app.go against the frozen candidate"},
	}
	if err := result.ValidateFor(request); err != nil {
		t.Fatal(err)
	}
	return request, result
}
