package workrun

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestWorkRoutingV1FixturesMatchSchemasAndGoContracts(t *testing.T) {
	tests := []struct {
		name       string
		schemaName string
		fixture    string
		decode     func(*testing.T, []byte)
	}{
		{
			name:       "advertised runtime capabilities",
			schemaName: "work-capabilities.schema.json",
			fixture:    "work-capabilities-advertised.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value map[string]any
				decodeStrict(t, payload, &value)
			},
		},
		{
			name:       "dormant runtime capabilities",
			schemaName: "work-capabilities.schema.json",
			fixture:    "work-capabilities-dormant.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value map[string]any
				decodeStrict(t, payload, &value)
			},
		},
		{
			name:       "outcome-first start request",
			schemaName: "work-start-request.schema.json",
			fixture:    "work-start-request.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value map[string]any
				decodeStrict(t, payload, &value)
			},
		},
		{
			name: "direct work status", schemaName: "work-status.schema.json",
			fixture: "work-status-direct.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkStatusV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkStatusV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "pending SDD decision status",
			schemaName: "work-status.schema.json",
			fixture:    "work-status-decision-pending.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkStatusV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkStatusV1.Validate() error = %v", err)
				}
			},
		},
		{
			name: "SDD work status", schemaName: "work-status.schema.json",
			fixture: "work-status-sdd.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkStatusV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkStatusV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "accepted SDD route decision",
			schemaName: "work-route.schema.json",
			fixture:    "work-route-accept.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkRouteV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkRouteV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "declined SDD inline owner reroute",
			schemaName: "work-route.schema.json",
			fixture:    "work-route-decline-direct.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkRouteV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkRouteV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "declined SDD delegated owner reroute",
			schemaName: "work-route.schema.json",
			fixture:    "work-route-decline.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkRouteV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkRouteV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "bound accepted SDD runtime",
			schemaName: "work-route.schema.json",
			fixture:    "work-route-bind-sdd.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkRouteV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkRouteV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "confirmed productive reconciliation",
			schemaName: "work-reconcile.schema.json",
			fixture:    "work-reconcile-delivered.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkReconcileV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkReconcileV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "no-delivery productive reconciliation",
			schemaName: "work-reconcile.schema.json",
			fixture:    "work-reconcile-not-delivered.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkReconcileV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkReconcileV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "manual productive reconciliation",
			schemaName: "work-reconcile.schema.json",
			fixture:    "work-reconcile-manual.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkReconcileV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkReconcileV1.Validate() error = %v", err)
				}
			},
		},
		{
			name: "authorized transition result", schemaName: "work-transition.schema.json",
			fixture: "work-transition.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkTransitionV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkTransitionV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "ready productive advance",
			schemaName: "work-advance.schema.json",
			fixture:    "work-advance-ready.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkAdvanceV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkAdvanceV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "decision productive advance",
			schemaName: "work-advance.schema.json",
			fixture:    "work-advance-decision.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkAdvanceV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkAdvanceV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "reconciliation-required productive advance",
			schemaName: "work-advance.schema.json",
			fixture:    "work-advance-reconcile.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkAdvanceV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkAdvanceV1.Validate() error = %v", err)
				}
			},
		},
		{
			name: "unsupported contract diagnostic", schemaName: "diagnostic.schema.json",
			fixture: "unsupported-contract.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkDiagnosticV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkDiagnosticV1.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "owner-classified unmanaged outcome",
			schemaName: "diagnostic.schema.json",
			fixture:    "outcome-not-managed.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkDiagnosticV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkDiagnosticV1.Validate() error = %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := workRoutingContractRoot()
			payload, err := os.ReadFile(filepath.Join(root, "fixtures", tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			var document any
			if err := json.Unmarshal(payload, &document); err != nil {
				t.Fatal(err)
			}
			schema := compileWorkRoutingSchema(t, root, tt.schemaName)
			if err := schema.Validate(document); err != nil {
				t.Fatalf("%s rejected %s: %v", tt.schemaName, tt.fixture, err)
			}
			tt.decode(t, payload)
		})
	}
}

func TestWorkCapabilitiesSchemaMatchesUnicodeReferenceBoundaries(t *testing.T) {
	root := workRoutingContractRoot()
	payload, err := os.ReadFile(filepath.Join(
		root,
		"fixtures",
		"work-capabilities-advertised.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	schema := compileWorkRoutingSchema(
		t,
		root,
		"work-capabilities.schema.json",
	)
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{
			name:  "240 Unicode code points",
			value: strings.Repeat("界", 240),
			valid: true,
		},
		{
			name:  "241 Unicode code points",
			value: strings.Repeat("界", 241),
		},
		{name: "U+0085 edge", value: "\u0085repository"},
		{name: "NBSP edge", value: "\u00A0repository"},
		{name: "U+2003 edge", value: "repository\u2003"},
		{name: "BOM edge", value: "repository\uFEFF"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(payload, &document); err != nil {
				t.Fatal(err)
			}
			document["repositoryRef"] = test.value
			err := schema.Validate(document)
			if (err == nil) != test.valid {
				t.Fatalf(
					"schema validation error = %v, want valid=%t",
					err,
					test.valid,
				)
			}
		})
	}
}

func TestWorkAdvanceV1RequiresTerminalExclusiveSHA256Authority(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join(
		workRoutingContractRoot(),
		"fixtures",
		"work-advance-ready.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var ready WorkAdvanceV1
	decodeStrict(t, payload, &ready)
	tests := []struct {
		name   string
		mutate func(*WorkAdvanceV1)
	}{
		{
			name: "same revision",
			mutate: func(value *WorkAdvanceV1) {
				value.Status.Revision = value.PreviousRevision
			},
		},
		{
			name: "ready with diagnostic",
			mutate: func(value *WorkAdvanceV1) {
				value.Diagnostic = &WorkAdvanceDiagnosticV1{
					Ref:     "sha256:" + strings.Repeat("1", 64),
					Code:    WorkAdvanceDiagnosticScopeMismatch,
					Message: workAdvanceDiagnosticMessages[WorkAdvanceDiagnosticScopeMismatch],
				}
			},
		},
		{
			name: "ready without result",
			mutate: func(value *WorkAdvanceV1) {
				value.DeliveryResultRef = ""
			},
		},
		{
			name: "ready status with decision diagnostic",
			mutate: func(value *WorkAdvanceV1) {
				value.DeliveryResultRef = ""
				value.Diagnostic = &WorkAdvanceDiagnosticV1{
					Ref:     "sha256:" + strings.Repeat("1", 64),
					Code:    WorkAdvanceDiagnosticScopeMismatch,
					Message: workAdvanceDiagnosticMessages[WorkAdvanceDiagnosticScopeMismatch],
				}
			},
		},
		{
			name: "ready with opaque result",
			mutate: func(value *WorkAdvanceV1) {
				value.DeliveryResultRef = "delivery:opaque"
			},
		},
		{
			name: "checking terminal",
			mutate: func(value *WorkAdvanceV1) {
				value.Status.PublicState = PublicStateChecking
			},
		},
		{
			name: "decision with delivery",
			mutate: func(value *WorkAdvanceV1) {
				value.Status.PublicState = PublicStateNeedsYourDecision
				value.Diagnostic = &WorkAdvanceDiagnosticV1{
					Ref:     "sha256:" + strings.Repeat("1", 64),
					Code:    WorkAdvanceDiagnosticScopeMismatch,
					Message: workAdvanceDiagnosticMessages[WorkAdvanceDiagnosticScopeMismatch],
				}
			},
		},
		{
			name: "terminal with authorized transition",
			mutate: func(value *WorkAdvanceV1) {
				value.Status.AuthorizedTransition = &AuthorizedTransitionV1{
					Contract:                   WorkTransitionContractV1,
					Operation:                  "apply",
					AuthorizationRef:           "authorization:terminal",
					ExpectedRevision:           value.Status.Revision,
					CandidateRef:               "candidate:terminal",
					ActionTicketRef:            "ticket:terminal",
					ApplicableAuthorizationRef: "authorization:applicability",
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := ready
			tt.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("WorkAdvanceV1.Validate() accepted an invalid terminal shape")
			}
		})
	}
}

func TestWorkAdvanceV1RejectsDivergentDiagnosticAuthority(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join(
		workRoutingContractRoot(),
		"fixtures",
		"work-advance-decision.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var advance WorkAdvanceV1
	decodeStrict(t, payload, &advance)
	if advance.Diagnostic == nil || advance.Status.Diagnostic == nil {
		t.Fatal("decision fixture must carry both diagnostic projections")
	}
	if err := advance.Diagnostic.Validate(); err != nil {
		t.Fatalf("top-level diagnostic is invalid: %v", err)
	}
	statusDiagnostic := *advance.Status.Diagnostic
	statusDiagnostic.Ref = "sha256:" + strings.Repeat("e", 64)
	if err := statusDiagnostic.Validate(); err != nil {
		t.Fatalf("divergent status diagnostic is independently invalid: %v", err)
	}
	advance.Status.Diagnostic = &statusDiagnostic
	if err := advance.Validate(); err == nil {
		t.Fatal("WorkAdvanceV1.Validate() accepted divergent diagnostic authority")
	}
}

func TestWorkAdvanceSchemaRejectsUnknownAndMixedTerminalFields(t *testing.T) {
	root := workRoutingContractRoot()
	payload, err := os.ReadFile(filepath.Join(
		root,
		"fixtures",
		"work-advance-ready.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	schema := compileWorkRoutingSchema(t, root, "work-advance.schema.json")
	document["runtimeStore"] = true
	if err := schema.Validate(document); err == nil {
		t.Fatal("work-advance schema accepted an unknown field")
	}
	delete(document, "runtimeStore")
	document["diagnostic"] = map[string]any{
		"ref":     "sha256:" + strings.Repeat("1", 64),
		"code":    "scope_mismatch",
		"message": workAdvanceDiagnosticMessages[WorkAdvanceDiagnosticScopeMismatch],
	}
	if err := schema.Validate(document); err == nil {
		t.Fatal("work-advance schema accepted mixed terminal authority")
	}
}

func TestWorkAdvanceSchemaRejectsInvalidTerminalBranches(t *testing.T) {
	root := workRoutingContractRoot()
	payload, err := os.ReadFile(filepath.Join(
		root,
		"fixtures",
		"work-advance-ready.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	schema := compileWorkRoutingSchema(t, root, "work-advance.schema.json")
	validDiagnostic := func() map[string]any {
		return map[string]any{
			"ref":        "sha256:" + strings.Repeat("1", 64),
			"code":       "scope_mismatch",
			"message":    workAdvanceDiagnosticMessages[WorkAdvanceDiagnosticScopeMismatch],
			"nextAction": "start_fresh_work_run",
		}
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "neither terminal branch",
			mutate: func(document map[string]any) {
				delete(document, "deliveryResultRef")
			},
		},
		{
			name: "ready result with checking status",
			mutate: func(document map[string]any) {
				document["status"].(map[string]any)["publicState"] = "checking"
			},
		},
		{
			name: "decision diagnostic with ready status",
			mutate: func(document map[string]any) {
				delete(document, "deliveryResultRef")
				document["diagnostic"] = validDiagnostic()
			},
		},
		{
			name: "unknown diagnostic code",
			mutate: func(document map[string]any) {
				delete(document, "deliveryResultRef")
				document["status"].(map[string]any)["publicState"] =
					"needs_your_decision"
				diagnostic := validDiagnostic()
				diagnostic["code"] = "invented"
				document["diagnostic"] = diagnostic
			},
		},
		{
			name: "known code with unstable message",
			mutate: func(document map[string]any) {
				delete(document, "deliveryResultRef")
				document["status"].(map[string]any)["publicState"] =
					"needs_your_decision"
				diagnostic := validDiagnostic()
				diagnostic["message"] = "Caller-authored message."
				document["diagnostic"] = diagnostic
			},
		},
		{
			name: "bad diagnostic sha",
			mutate: func(document map[string]any) {
				delete(document, "deliveryResultRef")
				document["status"].(map[string]any)["publicState"] =
					"needs_your_decision"
				diagnostic := validDiagnostic()
				diagnostic["ref"] = "sha256:BAD"
				document["diagnostic"] = diagnostic
			},
		},
		{
			name: "unknown nested diagnostic field",
			mutate: func(document map[string]any) {
				delete(document, "deliveryResultRef")
				document["status"].(map[string]any)["publicState"] =
					"needs_your_decision"
				diagnostic := validDiagnostic()
				diagnostic["detail"] = "hidden"
				document["diagnostic"] = diagnostic
			},
		},
		{
			name: "terminal authorized transition",
			mutate: func(document map[string]any) {
				status := document["status"].(map[string]any)
				status["authorizedTransition"] = map[string]any{
					"contract":                   WorkTransitionContractV1,
					"operation":                  "apply",
					"authorizationRef":           "authorization:terminal",
					"expectedRevision":           status["revision"],
					"candidateRef":               "candidate:terminal",
					"actionTicketRef":            "ticket:terminal",
					"applicableAuthorizationRef": "authorization:applicability",
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(payload, &document); err != nil {
				t.Fatal(err)
			}
			tt.mutate(document)
			if err := schema.Validate(document); err == nil {
				t.Fatal("work-advance schema accepted an invalid terminal branch")
			}
		})
	}
}

func TestWorkAdvanceSchemaRejectsReconciliationOnlyDiagnostics(t *testing.T) {
	root := workRoutingContractRoot()
	advanceSchema := compileWorkRoutingSchema(
		t,
		root,
		"work-advance.schema.json",
	)
	diagnosticSchema := compileWorkRoutingSchema(
		t,
		root,
		"work-advance-diagnostic.schema.json",
	)
	statusSchema := compileWorkRoutingSchema(
		t,
		root,
		"work-status.schema.json",
	)
	for _, fixture := range []string{
		"work-reconcile-not-delivered.fixture.json",
		"work-reconcile-manual.fixture.json",
	} {
		t.Run(fixture, func(t *testing.T) {
			payload, err := os.ReadFile(
				filepath.Join(root, "fixtures", fixture),
			)
			if err != nil {
				t.Fatal(err)
			}
			var reconciliation map[string]any
			if err := json.Unmarshal(payload, &reconciliation); err != nil {
				t.Fatal(err)
			}
			status, ok := reconciliation["status"].(map[string]any)
			if !ok {
				t.Fatal("reconciliation fixture has no status object")
			}
			diagnostic, ok := status["diagnostic"].(map[string]any)
			if !ok {
				t.Fatal("reconciliation fixture has no diagnostic object")
			}
			if err := diagnosticSchema.Validate(diagnostic); err != nil {
				t.Fatalf(
					"shared diagnostic schema rejected reconciliation evidence: %v",
					err,
				)
			}
			if err := statusSchema.Validate(status); err != nil {
				t.Fatalf(
					"shared status schema rejected reconciliation evidence: %v",
					err,
				)
			}
			advance := map[string]any{
				"schema":           WorkAdvanceContractV1,
				"contract":         WorkAdvanceContractV1,
				"previousRevision": reconciliation["previousRevision"],
				"status":           status,
				"diagnostic":       diagnostic,
			}
			if err := advanceSchema.Validate(advance); err == nil {
				t.Fatal(
					"work-advance schema accepted a reconciliation-only diagnostic",
				)
			}
		})
	}
}

func TestWorkAdvanceDiagnosticRequiresClosedCodeAndStableMessage(t *testing.T) {
	ref := "sha256:" + strings.Repeat("1", 64)
	tests := []WorkAdvanceDiagnosticV1{
		{
			Ref:     ref,
			Code:    "invented",
			Message: "Invented message.",
		},
		{
			Ref:     ref,
			Code:    WorkAdvanceDiagnosticScopeMismatch,
			Message: "A caller-authored explanation.",
		},
		{
			Ref:     ref,
			Code:    WorkAdvanceDiagnosticScopeMismatch,
			Message: workAdvanceDiagnosticMessages[WorkAdvanceDiagnosticScopeMismatch] + "\n",
		},
	}
	for _, diagnostic := range tests {
		if err := diagnostic.Validate(); err == nil {
			t.Fatalf("Validate() accepted %#v", diagnostic)
		}
	}
}

func TestWorkAdvanceDiagnosticCodeActionMatrixMatchesSchema(t *testing.T) {
	tests := []struct {
		code    WorkAdvanceDiagnosticCode
		allowed []WorkAdvanceDiagnosticNextAction
	}{
		{
			code:    WorkAdvanceDiagnosticCandidateBaseNotGit,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticCandidateNotCommitted,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticNoMutation,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticCandidateWrongBase,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticScopeMismatch,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticCandidateChanged,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticVerificationApplicabilityUnknown,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticCompleteVerificationPlanRequired,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticReviewRiskUnknown,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticReviewLensesUnknown,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticCompleteReviewRequired,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticDeliveryDecisionUnavailable,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code: WorkAdvanceDiagnosticDeliveryAuthorizationUnavailable,
			allowed: []WorkAdvanceDiagnosticNextAction{
				WorkAdvanceNextActionStartFresh,
				WorkAdvanceNextActionReconcile,
			},
		},
		{
			code:    WorkAdvanceDiagnosticDeliveryAuthorizationExpired,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticDeliveryConflict,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticDeliveryEffectFailed,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticDeliveryOutcomeIndeterminate,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionReconcile},
		},
		{
			code: WorkAdvanceDiagnosticProviderAuthorityUnavailable,
			allowed: []WorkAdvanceDiagnosticNextAction{
				WorkAdvanceNextActionStartFresh,
				WorkAdvanceNextActionReconcile,
			},
		},
		{
			code:    WorkAdvanceDiagnosticDeliveryNotCompleted,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionStartFresh},
		},
		{
			code:    WorkAdvanceDiagnosticManualResolutionRequired,
			allowed: []WorkAdvanceDiagnosticNextAction{WorkAdvanceNextActionManual},
		},
	}
	if len(tests) != len(workAdvanceDiagnosticMessages) {
		t.Fatalf(
			"diagnostic action matrix has %d codes, message catalog has %d",
			len(tests),
			len(workAdvanceDiagnosticMessages),
		)
	}
	schema := compileWorkRoutingSchema(
		t,
		workRoutingContractRoot(),
		"work-advance-diagnostic.schema.json",
	)
	ref := "sha256:" + strings.Repeat("1", 64)
	allActions := []WorkAdvanceDiagnosticNextAction{
		WorkAdvanceNextActionStartFresh,
		WorkAdvanceNextActionReconcile,
		WorkAdvanceNextActionManual,
	}
	seen := map[WorkAdvanceDiagnosticCode]bool{}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			if seen[test.code] {
				t.Fatalf("diagnostic code %q appears twice", test.code)
			}
			seen[test.code] = true
			message, ok := WorkAdvanceDiagnosticMessage(test.code)
			if !ok {
				t.Fatalf("diagnostic code %q has no stable message", test.code)
			}
			for _, action := range allActions {
				t.Run(string(action), func(t *testing.T) {
					want := false
					for _, allowed := range test.allowed {
						if action == allowed {
							want = true
							break
						}
					}
					diagnostic := WorkAdvanceDiagnosticV1{
						Ref:        ref,
						Code:       test.code,
						Message:    message,
						NextAction: action,
					}
					goErr := diagnostic.Validate()
					schemaErr := schema.Validate(map[string]any{
						"ref":        ref,
						"code":       string(test.code),
						"message":    message,
						"nextAction": string(action),
					})
					if (goErr == nil) != want ||
						(schemaErr == nil) != want {
						t.Fatalf(
							"code/action validation = go:%v schema:%v, want valid=%t",
							goErr,
							schemaErr,
							want,
						)
					}
				})
			}
		})
	}
}

func TestWorkDiagnosticV1MatchesPublicSchemaBoundaries(t *testing.T) {
	base := WorkDiagnosticV1{
		Schema:             WorkDiagnosticSchemaV1,
		Operation:          DiagnosticOperationWorkStatus,
		Code:               "unsupported_contract",
		Message:            "The requested contract is unsupported.",
		RequestedContract:  "caller-contract",
		SupportedContracts: []string{WorkStatusContractV1},
		MutationOutcome:    "not_started",
		NextAction:         "select_supported_contract",
	}
	tests := []struct {
		name   string
		valid  bool
		mutate func(*WorkDiagnosticV1)
	}{
		{
			name:  "empty requested contract remains representable",
			valid: true,
			mutate: func(value *WorkDiagnosticV1) {
				value.RequestedContract = ""
			},
		},
		{
			name:  "requested contract remains arbitrary",
			valid: true,
			mutate: func(value *WorkDiagnosticV1) {
				value.RequestedContract = " \n\x00 caller input "
			},
		},
		{
			name:  "requested contract accepts 240 Unicode code points",
			valid: true,
			mutate: func(value *WorkDiagnosticV1) {
				value.RequestedContract = strings.Repeat("界", 240)
			},
		},
		{
			name: "requested contract rejects 241 Unicode code points",
			mutate: func(value *WorkDiagnosticV1) {
				value.RequestedContract = strings.Repeat("界", 241)
			},
		},
		{
			name: "supported contracts reject duplicates",
			mutate: func(value *WorkDiagnosticV1) {
				value.SupportedContracts = append(
					value.SupportedContracts,
					WorkStatusContractV1,
				)
			},
		},
		{
			name:  "message accepts 240 Unicode code points",
			valid: true,
			mutate: func(value *WorkDiagnosticV1) {
				value.Message = strings.Repeat("界", 240)
			},
		},
		{
			name: "message rejects 241 Unicode code points",
			mutate: func(value *WorkDiagnosticV1) {
				value.Message = strings.Repeat("界", 241)
			},
		},
		{
			name: "message rejects U+0085 at edge",
			mutate: func(value *WorkDiagnosticV1) {
				value.Message = "\u0085" + value.Message
			},
		},
		{
			name: "message rejects BOM at edge",
			mutate: func(value *WorkDiagnosticV1) {
				value.Message += "\uFEFF"
			},
		},
		{
			name:  "message permits edge whitespace internally",
			valid: true,
			mutate: func(value *WorkDiagnosticV1) {
				value.Message = "Requested\u0085contract"
			},
		},
	}
	schema := compileWorkRoutingSchema(
		t,
		workRoutingContractRoot(),
		"diagnostic.schema.json",
	)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			value.SupportedContracts = append(
				[]string(nil),
				base.SupportedContracts...,
			)
			test.mutate(&value)
			goErr := value.Validate()
			payload, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var document any
			if err := json.Unmarshal(payload, &document); err != nil {
				t.Fatal(err)
			}
			schemaErr := schema.Validate(document)
			if (goErr == nil) != test.valid ||
				(schemaErr == nil) != test.valid {
				t.Fatalf(
					"validation = go:%v schema:%v, want valid=%t",
					goErr,
					schemaErr,
					test.valid,
				)
			}
		})
	}

	invalidUTF8 := base
	invalidUTF8.RequestedContract = string([]byte{0xff})
	if err := invalidUTF8.Validate(); err == nil {
		t.Fatal("WorkDiagnosticV1.Validate() accepted invalid UTF-8")
	}
}

func TestWorkStartSchemaPreservesUnicodeAndMultilineBoundaries(t *testing.T) {
	schema := compileWorkRoutingSchema(
		t,
		workRoutingContractRoot(),
		"work-start-request.schema.json",
	)
	const maximumOutcomeCodePoints = 64 << 10
	tests := []struct {
		name    string
		outcome string
		valid   bool
	}{
		{
			name:    "multiline natural language",
			outcome: "First line.\nSecond line.",
			valid:   true,
		},
		{
			name:    "newline at edge",
			outcome: "\nFirst line.",
		},
		{
			name:    "U+0085 at edge",
			outcome: "\u0085First line.",
		},
		{
			name:    "NBSP at edge",
			outcome: "\u00A0First line.",
		},
		{
			name:    "U+2003 at edge",
			outcome: "First line.\u2003",
		},
		{
			name:    "BOM at edge",
			outcome: "First line.\uFEFF",
		},
		{
			name:    "NUL internally",
			outcome: "First\x00line.",
		},
		{
			name:    "Unicode code-point boundary",
			outcome: strings.Repeat("界", maximumOutcomeCodePoints),
			valid:   true,
		},
		{
			name: "over Unicode code-point boundary",
			outcome: strings.Repeat(
				"界",
				maximumOutcomeCodePoints+1,
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := schema.Validate(map[string]any{"outcome": test.outcome})
			if (err == nil) != test.valid {
				t.Fatalf(
					"schema validation error = %v, want valid=%t",
					err,
					test.valid,
				)
			}
		})
	}
}

func TestWorkStatusV1KeepsDecisionSelectionAndSDDReferenceDistinct(t *testing.T) {
	base := loadWorkStatusFixture(t, "work-status-direct.fixture.json")
	tests := []struct {
		name   string
		mutate func(*WorkStatusV1)
		want   string
	}{
		{
			name: "selected route mismatches decision",
			mutate: func(status *WorkStatusV1) {
				status.ImplementationRoute = ImplementationRouteDelegatedDirect
			},
			want: "does not match route decision",
		},
		{
			name: "direct route carries SDD reference",
			mutate: func(status *WorkStatusV1) {
				status.SDDRunRef = "sdd:not-selected"
			},
			want: "sddRunRef requires",
		},
		{
			name: "selected SDD lacks SDD reference",
			mutate: func(status *WorkStatusV1) {
				status.RouteDecision = RouteDecisionProposeSDD
				status.ImplementationRoute = ImplementationRouteSDD
			},
			want: "sddRunRef must be",
		},
		{
			name: "transition does not bind current revision",
			mutate: func(status *WorkStatusV1) {
				status.AuthorizedTransition.ExpectedRevision = "sha256:" + strings.Repeat("f", 64)
			},
			want: "must bind the current work revision",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := base
			transition := *base.AuthorizedTransition
			status.AuthorizedTransition = &transition
			tt.mutate(&status)
			err := status.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}

	pendingSDD := base
	pendingSDD.RouteDecision = RouteDecisionProposeSDD
	pendingSDD.RoutePhase = RoutePhaseDecisionPending
	pendingSDD.ImplementationRoute = ""
	pendingSDD.SDDRunRef = ""
	pendingSDD.PublicState = PublicStateNeedsYourDecision
	pendingSDD.AuthorizedTransition = nil
	if err := pendingSDD.Validate(); err != nil {
		t.Fatalf("pending propose_sdd decision must remain unselected: %v", err)
	}
}

func TestWorkRoutingReferenceSchemasMatchGoEdgesAndLength(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{
			name:  "240 Unicode code points",
			value: strings.Repeat("界", 240),
			valid: true,
		},
		{
			name:  "241 Unicode code points",
			value: strings.Repeat("界", 241),
		},
		{
			name:  "U+0085 leading edge",
			value: "\u0085reference",
		},
		{
			name:  "NBSP leading edge",
			value: "\u00A0reference",
		},
		{
			name:  "U+2003 trailing edge",
			value: "reference\u2003",
		},
		{
			name:  "BOM trailing edge",
			value: "reference\uFEFF",
		},
		{
			name:  "internal U+0085",
			value: "reference\u0085value",
			valid: true,
		},
		{
			name:  "internal newline",
			value: "reference\nvalue",
		},
	}
	root := workRoutingContractRoot()
	statusSchema := compileWorkRoutingSchema(
		t,
		root,
		"work-status.schema.json",
	)
	transitionSchema := compileWorkRoutingSchema(
		t,
		root,
		"work-transition.schema.json",
	)
	statusBase := loadWorkStatusFixture(t, "work-status-direct.fixture.json")
	transitionPayload, err := os.ReadFile(filepath.Join(
		root,
		"fixtures",
		"work-transition.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var transitionBase WorkTransitionV1
	decodeStrict(t, transitionPayload, &transitionBase)

	for _, test := range tests {
		t.Run("status/"+test.name, func(t *testing.T) {
			status := statusBase
			status.WorkRunID = test.value
			assertGoAndSchemaValidity(
				t,
				status.Validate(),
				statusSchema,
				status,
				test.valid,
			)
		})
		t.Run("transition/"+test.name, func(t *testing.T) {
			transition := transitionBase
			transition.AppliedAuthorizationRef = test.value
			assertGoAndSchemaValidity(
				t,
				transition.Validate(),
				transitionSchema,
				transition,
				test.valid,
			)
		})
	}

	invalidUTF8 := statusBase
	invalidUTF8.WorkRunID = string([]byte{0xff})
	if err := invalidUTF8.Validate(); err == nil {
		t.Fatal("WorkStatusV1.Validate() accepted invalid UTF-8")
	}
}

func TestVerificationSummaryRequiresArrayShape(t *testing.T) {
	schema := compileWorkRoutingSchema(
		t,
		workRoutingContractRoot(),
		"work-status.schema.json",
	)
	status := loadWorkStatusFixture(t, "work-status-direct.fixture.json")
	status.Verification.ResultRefs = []string{}
	assertGoAndSchemaValidity(t, status.Validate(), schema, status, true)

	status.Verification.ResultRefs = nil
	assertGoAndSchemaValidity(t, status.Validate(), schema, status, false)
}

func TestWorkMutationContractsRejectUnchangedRevision(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		validate func(*testing.T, []byte) error
	}{
		{
			name:    "route",
			fixture: "work-route-accept.fixture.json",
			validate: func(t *testing.T, payload []byte) error {
				var route WorkRouteV1
				decodeStrict(t, payload, &route)
				route.Status.Revision = route.PreviousRevision
				return route.Validate()
			},
		},
		{
			name:    "reconcile",
			fixture: "work-reconcile-delivered.fixture.json",
			validate: func(t *testing.T, payload []byte) error {
				var reconcile WorkReconcileV1
				decodeStrict(t, payload, &reconcile)
				reconcile.Status.Revision = reconcile.PreviousRevision
				return reconcile.Validate()
			},
		},
		{
			name:    "transition",
			fixture: "work-transition.fixture.json",
			validate: func(t *testing.T, payload []byte) error {
				var transition WorkTransitionV1
				decodeStrict(t, payload, &transition)
				transition.Revision = transition.PreviousRevision
				return transition.Validate()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join(
				workRoutingContractRoot(),
				"fixtures",
				test.fixture,
			))
			if err != nil {
				t.Fatal(err)
			}
			if err := test.validate(t, payload); err == nil {
				t.Fatal("Validate() accepted an unchanged WorkRun revision")
			}
		})
	}
}

func TestWorkRoutingSchemasRejectUnknownFieldsAndRouteContradictions(t *testing.T) {
	root := workRoutingContractRoot()
	payload, err := os.ReadFile(filepath.Join(root, "fixtures", "work-status-direct.fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	schema := compileWorkRoutingSchema(t, root, "work-status.schema.json")

	document["runtimeStore"] = map[string]any{"enabled": true}
	if err := schema.Validate(document); err == nil {
		t.Fatal("work-status schema accepted an unknown runtimeStore field")
	}
	delete(document, "runtimeStore")
	document["implementationRoute"] = "sdd"
	if err := schema.Validate(document); err == nil {
		t.Fatal("work-status schema accepted route decision/selection mismatch")
	}
}

func assertGoAndSchemaValidity(
	t *testing.T,
	goErr error,
	schema *jsonschema.Schema,
	value any,
	valid bool,
) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	schemaErr := schema.Validate(document)
	if (goErr == nil) != valid || (schemaErr == nil) != valid {
		t.Fatalf(
			"validation = go:%v schema:%v, want valid=%t",
			goErr,
			schemaErr,
			valid,
		)
	}
}

func loadWorkStatusFixture(t *testing.T, name string) WorkStatusV1 {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(workRoutingContractRoot(), "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	var value WorkStatusV1
	decodeStrict(t, payload, &value)
	return value
}

func compileWorkRoutingSchema(t *testing.T, root, name string) *jsonschema.Schema {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "schemas"))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		payload, err := os.ReadFile(
			filepath.Join(root, "schemas", entry.Name()),
		)
		if err != nil {
			t.Fatal(err)
		}
		var resource any
		if err := json.Unmarshal(payload, &resource); err != nil {
			t.Fatal(err)
		}
		location := "https://gentle-ai.dev/contracts/work-routing/v1/schemas/" +
			entry.Name()
		if err := compiler.AddResource(location, resource); err != nil {
			t.Fatal(err)
		}
	}
	location := "https://gentle-ai.dev/contracts/work-routing/v1/schemas/" + name
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func decodeStrict(t *testing.T, payload []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("fixture contains trailing JSON: %v", err)
	}
}

func workRoutingContractRoot() string {
	return filepath.Join("..", "..", "contracts", "work-routing", "v1")
}
