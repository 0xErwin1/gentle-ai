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
			"ref":     "sha256:" + strings.Repeat("1", 64),
			"code":    "scope_mismatch",
			"message": workAdvanceDiagnosticMessages[WorkAdvanceDiagnosticScopeMismatch],
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
	pendingSDD.ImplementationRoute = ""
	pendingSDD.SDDRunRef = ""
	pendingSDD.AuthorizedTransition = nil
	if err := pendingSDD.Validate(); err != nil {
		t.Fatalf("pending propose_sdd decision must remain unselected: %v", err)
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
