package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
)

// The documented negotiated lifecycle route in
// internal/assets/claude/commands/sdd-apply.md and
// internal/assets/opencode/commands/sdd-apply.md declares no runtime
// identity. A read-only STATUS creates no authority, tier, budget, or
// collection state, so it has nothing to fail closed over: an undeclared
// identity is the manual/non-agent compatibility path that
// runReviewFacadeStart already documents, not an unsupported transport.
func TestNegotiatedStatusWithoutRuntimeIdentityIsAnswered(t *testing.T) {
	repo := initReviewCLIRepo(t)

	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "documented route", args: []string{"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--next-transition"}},
		{name: "without transition", args: []string{"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := RunReview(test.args, &output)
			if err == nil {
				return
			}
			failure := decodeReviewIntegrationFailure(t, output.Bytes())
			if failure.Code == reviewImmutableTransportUnsupportedCode {
				t.Fatalf("undeclared runtime identity refused a read-only STATUS: %#v", failure)
			}
			t.Fatalf("negotiated STATUS failed: %v %#v", err, failure)
		})
	}
}

// A candidate-bearing repository must complete the documented route: STATUS
// answers, and the exact provider-returned START it names must execute
// rather than refuse the same undeclared identity one step later.
func TestNegotiatedRouteWithoutRuntimeIdentityReachesStart(t *testing.T) {
	repo := initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "candidate.go", "package candidate\n", 0o644)

	var statusOutput bytes.Buffer
	if err := RunReview([]string{
		"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--next-transition",
	}, &statusOutput); err != nil {
		t.Fatalf("negotiated STATUS failed: %v %s", err, statusOutput.String())
	}

	var status struct {
		NextTransition *struct {
			Execute *struct {
				Operation string `json:"operation"`
				Arguments []struct {
					Name  string `json:"name"`
					Token string `json:"token"`
				} `json:"arguments"`
			} `json:"execute"`
		} `json:"next_transition"`
	}
	if err := json.Unmarshal(statusOutput.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.NextTransition == nil || status.NextTransition.Execute == nil ||
		status.NextTransition.Execute.Operation != "review.start" {
		t.Fatalf("STATUS named no executable START: %s", statusOutput.String())
	}

	startArgs := []string{"start", "--cwd", repo}
	for _, argument := range status.NextTransition.Execute.Arguments {
		if argument.Name == "agent" {
			t.Fatalf("STATUS invented a runtime identity the caller never declared: %s", argument.Token)
		}
		startArgs = append(startArgs, argument.Token)
	}

	var startOutput bytes.Buffer
	if err := RunReview(startArgs, &startOutput); err != nil {
		failure := decodeReviewIntegrationFailure(t, startOutput.Bytes())
		if failure.Code == reviewImmutableTransportUnsupportedCode {
			t.Fatalf("undeclared runtime identity refused the provider-returned START: %#v", failure)
		}
		t.Fatalf("provider-returned START failed: %v %#v", err, failure)
	}
	if !strings.Contains(startOutput.String(), "consent") {
		t.Fatalf("negotiated START answered without the consent envelope: %s", startOutput.String())
	}
}

// The refusal for a genuinely unsupported declared runtime is untouched.
func TestDeclaredUnsupportedRuntimeStillRefusesNegotiatedStatus(t *testing.T) {
	repo := initReviewCLIRepo(t)
	for _, runtime := range []string{string(model.AgentOpenCode), string(model.AgentCodex), "unknown-runtime"} {
		t.Run(runtime, func(t *testing.T) {
			var output bytes.Buffer
			if err := RunReview([]string{
				"status", "--cwd", repo, "--contract", ReviewIntegrationContractV2, "--agent", runtime, "--next-transition",
			}, &output); err == nil {
				t.Fatalf("declared unsupported runtime %q was accepted", runtime)
			}
			failure := decodeReviewIntegrationFailure(t, output.Bytes())
			if failure.Code != reviewImmutableTransportUnsupportedCode || failure.NextAction != "stop" {
				t.Fatalf("declared unsupported runtime %q failure = %#v", runtime, failure)
			}
		})
	}
}
