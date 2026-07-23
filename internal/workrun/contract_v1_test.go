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
	payload, err := os.ReadFile(filepath.Join(root, "schemas", name))
	if err != nil {
		t.Fatal(err)
	}
	var resource any
	if err := json.Unmarshal(payload, &resource); err != nil {
		t.Fatal(err)
	}
	location := "https://gentle-ai.dev/contracts/work-routing/v1/schemas/" + name
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(location, resource); err != nil {
		t.Fatal(err)
	}
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
