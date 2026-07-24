package workrun

import (
	"errors"
	"testing"
)

func TestDecideImplementationRouteCanonicalThresholds(t *testing.T) {
	tests := []struct {
		name     string
		input    ImplementationRouteInput
		decision RouteDecision
	}{
		{
			name: "one decision file stays inline",
			input: ImplementationRouteInput{
				ReadIntent: ReadIntentDecideOrVerify, ReadFileCount: 1,
			},
			decision: RouteDecisionDirectInline,
		},
		{
			name: "three decision files stay inline",
			input: ImplementationRouteInput{
				ReadIntent: ReadIntentDecideOrVerify, ReadFileCount: 3,
			},
			decision: RouteDecisionDirectInline,
		},
		{
			name: "four understanding files delegate",
			input: ImplementationRouteInput{
				ReadIntent: ReadIntentExploreUnderstand, ReadFileCount: 4,
			},
			decision: RouteDecisionDelegatedDirect,
		},
		{
			name: "preparation read delegates with the write",
			input: ImplementationRouteInput{
				ReadIntent: ReadIntentPrepareWrite, ReadFileCount: 1,
				WriteIntent: WriteIntentAtomicMechanical, WriteFileCount: 1,
			},
			decision: RouteDecisionDelegatedDirect,
		},
		{
			name: "one mechanical file stays inline",
			input: ImplementationRouteInput{
				WriteIntent: WriteIntentAtomicMechanical, WriteFileCount: 1,
			},
			decision: RouteDecisionDirectInline,
		},
		{
			name: "two analytical files delegate one writer",
			input: ImplementationRouteInput{
				WriteIntent: WriteIntentAnalytical, WriteFileCount: 2,
			},
			decision: RouteDecisionDelegatedDirect,
		},
		{
			name: "broad research delegates without selecting SDD",
			input: ImplementationRouteInput{
				BroadResearch: true,
			},
			decision: RouteDecisionDelegatedDirect,
		},
		{
			name: "durable architecture planning proposes SDD",
			input: ImplementationRouteInput{
				DurablePlanning: []DurablePlanningReason{PlanningArchitecture},
			},
			decision: RouteDecisionProposeSDD,
		},
		{
			name: "explicit request remains a proposal decision",
			input: ImplementationRouteInput{
				ExplicitSDDRequestRef: testSHARef("explicit-sdd"),
			},
			decision: RouteDecisionProposeSDD,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecideImplementationRoute(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Decision != tt.decision {
				t.Fatalf("decision = %q, want %q", got.Decision, tt.decision)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("decision validation failed: %v", err)
			}
			again, err := DecideImplementationRoute(tt.input)
			if err != nil || again.Digest != got.Digest {
				t.Fatalf("decision is not deterministic: again=%#v err=%v", again, err)
			}
		})
	}
}

func TestDecideImplementationRouteFailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		input ImplementationRouteInput
		want  error
	}{
		{
			name:  "no facts",
			input: ImplementationRouteInput{},
			want:  ErrInsufficientRoutingFacts,
		},
		{
			name: "four files cannot masquerade as decide verify",
			input: ImplementationRouteInput{
				ReadIntent: ReadIntentDecideOrVerify, ReadFileCount: 4,
			},
			want: ErrInvalidRoutingFacts,
		},
		{
			name: "three files cannot claim exploration threshold",
			input: ImplementationRouteInput{
				ReadIntent: ReadIntentExploreUnderstand, ReadFileCount: 3,
			},
			want: ErrInvalidRoutingFacts,
		},
		{
			name: "one analytical write is not silently called mechanical",
			input: ImplementationRouteInput{
				WriteIntent: WriteIntentAnalytical, WriteFileCount: 1,
			},
			want: ErrInsufficientRoutingFacts,
		},
		{
			name: "bounded read does not hide ambiguous analytical write",
			input: ImplementationRouteInput{
				ReadIntent: ReadIntentDecideOrVerify, ReadFileCount: 2,
				WriteIntent: WriteIntentAnalytical, WriteFileCount: 1,
			},
			want: ErrInsufficientRoutingFacts,
		},
		{
			name: "mutable explicit request is rejected",
			input: ImplementationRouteInput{
				ExplicitSDDRequestRef: "please-use-sdd",
			},
			want: ErrInvalidRoutingFacts,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecideImplementationRoute(tt.input)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, tt.want)
			}
		})
	}
}

func TestImplementationRouteDecisionRejectsTampering(t *testing.T) {
	decision, err := DecideImplementationRoute(ImplementationRouteInput{
		ReadIntent: ReadIntentExploreUnderstand, ReadFileCount: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	decision.Decision = RouteDecisionDirectInline
	if err := decision.Validate(); err == nil {
		t.Fatal("tampered decision unexpectedly validated")
	}
}
