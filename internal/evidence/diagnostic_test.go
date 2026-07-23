package evidence

import "testing"

func TestDiagnosticBindsTypedActionableChoices(t *testing.T) {
	t.Parallel()

	diagnostic, err := NewDiagnostic(DiagnosticInput{
		Operation: "verification.execute", Proof: ProofCapability,
		Reason:     ReasonCapabilityUnavailable,
		SubjectRef: testRef("subject"), CandidateRef: testRef("candidate"),
		EvidenceRefs:               []string{testRef("evidence-b"), testRef("evidence-a")},
		ImplementationDecisionRef:  testRef("continue-decision"),
		AvailableDeliveryRouteRefs: []string{testRef("route-b"), testRef("route-a")},
		DecisionNeeded:             DecisionSelectTrustedRunner,
		RemedyRefs:                 []string{testRef("remedy")},
		Detail:                     "The trusted runner is not available in this environment.",
	})
	if err != nil {
		t.Fatalf("NewDiagnostic() error = %v", err)
	}
	if err := diagnostic.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if diagnostic.EvidenceRefs[0] != testRef("evidence-a") ||
		diagnostic.AvailableDeliveryRouteRefs[0] != testRef("route-a") {
		t.Fatalf("diagnostic collections are not canonical: %#v", diagnostic)
	}

	tampered := diagnostic
	tampered.CandidateRef = testRef("other-candidate")
	if err := tampered.Validate(); err == nil {
		t.Fatal("Validate() accepted a cross-candidate diagnostic")
	}
}

func TestDiagnosticRejectsUntypedOrAmbiguousInputs(t *testing.T) {
	t.Parallel()

	base := DiagnosticInput{
		Operation: "verification.execute", Proof: ProofExecution,
		Reason:     ReasonDeadlineExceeded,
		SubjectRef: testRef("subject"), CandidateRef: testRef("candidate"),
		EvidenceRefs: []string{}, ImplementationDecisionRef: testRef("continue-decision"),
		AvailableDeliveryRouteRefs: []string{},
		DecisionNeeded:             DecisionDefer, RemedyRefs: []string{},
	}
	tests := []struct {
		name   string
		mutate func(*DiagnosticInput)
	}{
		{name: "unknown proof", mutate: func(input *DiagnosticInput) { input.Proof = "maybe" }},
		{name: "unknown reason", mutate: func(input *DiagnosticInput) { input.Reason = "something_happened" }},
		{name: "unknown decision", mutate: func(input *DiagnosticInput) { input.DecisionNeeded = "ask_somehow" }},
		{name: "free form operation", mutate: func(input *DiagnosticInput) { input.Operation = "Run whatever" }},
		{name: "duplicate evidence", mutate: func(input *DiagnosticInput) {
			input.EvidenceRefs = []string{testRef("same"), testRef("same")}
		}},
		{name: "invalid route ref", mutate: func(input *DiagnosticInput) {
			input.AvailableDeliveryRouteRefs = []string{"pr.with.issue"}
		}},
		{name: "unbounded detail", mutate: func(input *DiagnosticInput) {
			input.Detail = string(make([]byte, 2049))
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := base
			test.mutate(&input)
			if _, err := NewDiagnostic(input); err == nil {
				t.Fatal("NewDiagnostic() accepted invalid input")
			}
		})
	}
}
