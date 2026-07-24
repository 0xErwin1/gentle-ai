package workrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkCapabilitiesV2FixturesMatchSchema(t *testing.T) {
	root := workRoutingContractRoot()
	schema := compileWorkRoutingSchema(
		t,
		root,
		"work-capabilities-v2.schema.json",
	)
	for _, fixture := range []string{
		"work-capabilities-v2-advertised.fixture.json",
		"work-capabilities-v2-dormant.fixture.json",
	} {
		t.Run(fixture, func(t *testing.T) {
			payload, err := os.ReadFile(
				filepath.Join(root, "fixtures", fixture),
			)
			if err != nil {
				t.Fatal(err)
			}
			var document any
			if err := json.Unmarshal(payload, &document); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(document); err != nil {
				t.Fatalf(
					"work-capabilities-v2 schema rejected %s: %v",
					fixture,
					err,
				)
			}
			var exact map[string]any
			decodeStrict(t, payload, &exact)
		})
	}
}

func TestWorkAdvanceV2AndVerificationDecisionFixturesMatchContracts(
	t *testing.T,
) {
	tests := []struct {
		name       string
		schemaName string
		fixture    string
		decode     func(*testing.T, []byte)
	}{
		{
			name:       "durable verification checkpoint",
			schemaName: "work-advance-v2.schema.json",
			fixture:    "work-advance-v2-checkpoint.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkAdvanceV2
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf("WorkAdvanceV2.Validate() error = %v", err)
				}
			},
		},
		{
			name:       "accepted run decision",
			schemaName: "work-verification-decide.schema.json",
			fixture:    "work-verification-decide-run.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkVerificationDecideV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf(
						"WorkVerificationDecideV1.Validate() error = %v",
						err,
					)
				}
			},
		},
		{
			name:       "deferred decision",
			schemaName: "work-verification-decide.schema.json",
			fixture:    "work-verification-decide-defer.fixture.json",
			decode: func(t *testing.T, payload []byte) {
				var value WorkVerificationDecideV1
				decodeStrict(t, payload, &value)
				if err := value.Validate(); err != nil {
					t.Fatalf(
						"WorkVerificationDecideV1.Validate() error = %v",
						err,
					)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := workRoutingContractRoot()
			payload, err := os.ReadFile(
				filepath.Join(root, "fixtures", test.fixture),
			)
			if err != nil {
				t.Fatal(err)
			}
			var document any
			if err := json.Unmarshal(payload, &document); err != nil {
				t.Fatal(err)
			}
			schema := compileWorkRoutingSchema(
				t,
				root,
				test.schemaName,
			)
			if err := schema.Validate(document); err != nil {
				t.Fatalf(
					"%s rejected %s: %v",
					test.schemaName,
					test.fixture,
					err,
				)
			}
			test.decode(t, payload)
		})
	}
}

func TestWorkAdvanceV1RemainsTerminalAndRejectsV2CheckpointShape(
	t *testing.T,
) {
	root := workRoutingContractRoot()
	payload, err := os.ReadFile(filepath.Join(
		root,
		"fixtures",
		"work-advance-v2-checkpoint.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	document["schema"] = WorkAdvanceContractV1
	document["contract"] = WorkAdvanceContractV1
	legacy := compileWorkRoutingSchema(t, root, "work-advance.schema.json")
	if err := legacy.Validate(document); err == nil {
		t.Fatal("terminal work-advance/v1 accepted a v2 decision checkpoint")
	}

	var checkpoint WorkAdvanceV2
	decodeStrict(t, payload, &checkpoint)
	legacyValue := WorkAdvanceV1{
		Schema:           WorkAdvanceContractV1,
		Contract:         WorkAdvanceContractV1,
		PreviousRevision: checkpoint.PreviousRevision,
		Status:           checkpoint.Status,
	}
	if err := legacyValue.Validate(); err == nil {
		t.Fatal("WorkAdvanceV1 accepted a non-terminal consent checkpoint")
	}
}

func TestWorkAdvanceV2ReusesV1TerminalSemantics(t *testing.T) {
	root := workRoutingContractRoot()
	schema := compileWorkRoutingSchema(t, root, "work-advance-v2.schema.json")
	for _, name := range []string{
		"work-advance-ready.fixture.json",
		"work-advance-decision.fixture.json",
	} {
		t.Run(name, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join(root, "fixtures", name))
			if err != nil {
				t.Fatal(err)
			}
			var legacy WorkAdvanceV1
			decodeStrict(t, payload, &legacy)
			if err := legacy.Validate(); err != nil {
				t.Fatal(err)
			}
			current := WorkAdvanceV2{
				Schema:            WorkAdvanceContractV2,
				Contract:          WorkAdvanceContractV2,
				PreviousRevision:  legacy.PreviousRevision,
				Status:            legacy.Status,
				DeliveryResultRef: legacy.DeliveryResultRef,
				Diagnostic:        legacy.Diagnostic,
			}
			if err := current.Validate(); err != nil {
				t.Fatalf("WorkAdvanceV2 terminal validation error = %v", err)
			}
			currentPayload, err := json.Marshal(current)
			if err != nil {
				t.Fatal(err)
			}
			var document any
			if err := json.Unmarshal(currentPayload, &document); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(document); err != nil {
				t.Fatalf("v2 schema rejected v1 terminal semantics: %v", err)
			}
		})
	}
}

func TestVerificationDecisionContractsRejectMixedOrTamperedAuthority(
	t *testing.T,
) {
	root := workRoutingContractRoot()
	checkpointPayload, err := os.ReadFile(filepath.Join(
		root,
		"fixtures",
		"work-advance-v2-checkpoint.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint WorkAdvanceV2
	decodeStrict(t, checkpointPayload, &checkpoint)
	if err := checkpoint.Validate(); err != nil {
		t.Fatal(err)
	}

	mixed := checkpoint
	mixed.DeliveryResultRef = testSHARef("mixed-delivery")
	if err := mixed.Validate(); err == nil {
		t.Fatal("WorkAdvanceV2 accepted checkpoint plus delivery authority")
	}
	tampered := checkpoint
	prompt := *checkpoint.VerificationDecision
	prompt.AssumptionsRef = testSHARef("tampered-assumptions")
	tampered.VerificationDecision = &prompt
	if err := tampered.Validate(); err == nil {
		t.Fatal("WorkAdvanceV2 accepted a tampered owner prompt")
	}

	decisionPayload, err := os.ReadFile(filepath.Join(
		root,
		"fixtures",
		"work-verification-decide-defer.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var decision WorkVerificationDecideV1
	decodeStrict(t, decisionPayload, &decision)
	decision.Choice = VerificationDecisionRun
	if err := decision.Validate(); err == nil {
		t.Fatal("run choice accepted a non-resumable needs-decision status")
	}
}

func TestVerificationDecisionPromptSchemaAndGoRejectUnsafeChoices(
	t *testing.T,
) {
	root := workRoutingContractRoot()
	payload, err := os.ReadFile(filepath.Join(
		root,
		"fixtures",
		"work-advance-v2-checkpoint.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint WorkAdvanceV2
	decodeStrict(t, payload, &checkpoint)
	promptSchema := compileWorkRoutingSchema(
		t,
		root,
		"verification-decision-prompt.schema.json",
	)
	tests := []struct {
		name   string
		mutate func(*VerificationDecisionPromptV1)
	}{
		{
			name: "unknown cost cannot launch",
			mutate: func(prompt *VerificationDecisionPromptV1) {
				prompt.Cost = "unknown"
			},
		},
		{
			name: "reduce scope escape is mandatory",
			mutate: func(prompt *VerificationDecisionPromptV1) {
				prompt.Choices = []VerificationDecisionChoice{
					VerificationDecisionRun,
					VerificationDecisionDefer,
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prompt := *checkpoint.VerificationDecision
			prompt.Choices = append(
				[]VerificationDecisionChoice{},
				prompt.Choices...,
			)
			test.mutate(&prompt)
			prompt.PromptRef, err = verificationDecisionPromptRef(prompt)
			if err != nil {
				t.Fatal(err)
			}
			if err := prompt.Validate(); err == nil {
				t.Fatal("Go contract accepted unsafe owner choices")
			}
			promptPayload, err := json.Marshal(prompt)
			if err != nil {
				t.Fatal(err)
			}
			var document any
			if err := json.Unmarshal(promptPayload, &document); err != nil {
				t.Fatal(err)
			}
			if err := promptSchema.Validate(document); err == nil {
				t.Fatal("JSON schema accepted unsafe owner choices")
			}
		})
	}
}

func TestVerificationDecisionReceiptRejectsInlineAdvance(t *testing.T) {
	root := workRoutingContractRoot()
	payload, err := os.ReadFile(filepath.Join(
		root,
		"fixtures",
		"work-verification-decide-run.fixture.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	document["advance"] = map[string]any{}
	schema := compileWorkRoutingSchema(
		t,
		root,
		"work-verification-decide.schema.json",
	)
	if err := schema.Validate(document); err == nil {
		t.Fatal("verification decision receipt accepted inline advance")
	}
}
