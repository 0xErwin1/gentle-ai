package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

// This file is Wave 4 S4 (design.md decision 5, task 5.1-5.6): transport
// capability admission, checked in `review start` before any authority,
// tier, lens, budget, or collection slot exists. The adapter declares (its
// canonical capabilitymanifest.AgentCapabilityManifest); the provider never
// probes. Absent, unrecognised, or non-self-consistent claims fail closed
// with zero authority artifacts created.

// TestAuthorizeReviewTransportCapabilityMatrix is the threat-matrix table
// (task 5.2, Process integration/adapters row): absent claim, unrecognised
// claim, and advertised-but-failing transport are three distinct cases.
func TestAuthorizeReviewTransportCapabilityMatrix(t *testing.T) {
	t.Run("no agent identity supplied is not gated at all", func(t *testing.T) {
		// The manual/non-agent compatibility path: there is no adapter here
		// to admit or deny.
		if err := authorizeReviewTransportCapability(""); err != nil {
			t.Fatalf("authorizeReviewTransportCapability(\"\") = %v, want nil", err)
		}
	})
	t.Run("advertised claim admits", func(t *testing.T) {
		if err := authorizeReviewTransportCapability(string(model.AgentClaudeCode)); err != nil {
			t.Fatalf("authorizeReviewTransportCapability(claude) = %v, want nil", err)
		}
	})
	t.Run("absent claim fails closed", func(t *testing.T) {
		// Pi declares only AutoInstall|SystemPrompt|MCP; its manifest does
		// not advertise ContractReviewTransportV1.
		if err := authorizeReviewTransportCapability(string(model.AgentPi)); err == nil {
			t.Fatal("authorizeReviewTransportCapability(pi) = nil, want an absent-claim refusal")
		}
	})
	t.Run("absent claim fails closed for Codex (#2418 dormant declaration)", func(t *testing.T) {
		// Codex has no reviewer lens assets (internal/assets/codex/ ships
		// only sdd-orchestrator.md), so its manifest no longer advertises
		// ContractReviewTransportV1 either — the same class of refusal as Pi.
		if err := authorizeReviewTransportCapability(string(model.AgentCodex)); err == nil {
			t.Fatal("authorizeReviewTransportCapability(codex) = nil, want an absent-claim refusal")
		}
	})
	t.Run("unrecognised claim fails closed", func(t *testing.T) {
		if err := authorizeReviewTransportCapability("unknown-runtime"); err == nil {
			t.Fatal("authorizeReviewTransportCapability(unknown-runtime) = nil, want an unrecognised-claim refusal")
		}
	})
	t.Run("advertised but self-inconsistent manifest fails closed", func(t *testing.T) {
		// Advertised-but-failing transport: the manifest claims the
		// contract, but does not pass its own canonical shape check. The
		// provider never probes a live runtime for this — it only ever
		// re-validates the adapter's own self-declared shape. Synthesized
		// through the resolution seam since no real, compiled agent
		// manifest is ever actually inconsistent with itself.
		previous := reviewTransportCapabilityForAgent
		reviewTransportCapabilityForAgent = func(agent model.AgentID) (capabilitymanifest.AgentCapabilityManifest, error) {
			manifest, err := previous(model.AgentClaudeCode)
			if err != nil {
				return capabilitymanifest.AgentCapabilityManifest{}, err
			}
			manifest.AgentID = agent // now advertised for `agent`, but Validate() rejects the identity mismatch
			return manifest, nil
		}
		defer func() { reviewTransportCapabilityForAgent = previous }()
		if err := authorizeReviewTransportCapability(string(model.AgentCodex)); err == nil {
			t.Fatal("authorizeReviewTransportCapability(codex) with a self-inconsistent injected manifest = nil, want a refusal")
		}
	})
}

// TestUnsupportedReviewTransportCapabilityStopsBeforeAnyAuthority is task
// 5.1's RED/GREEN: a store inspection asserting an empty authority root —
// the unsupported-transport denial happens before any authority, tier,
// lens, budget, or collection state exists.
func TestUnsupportedReviewTransportCapabilityStopsBeforeAnyAuthority(t *testing.T) {
	repo := initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "candidate.go", "package candidate\n", 0o644)

	var output bytes.Buffer
	err := RunReview([]string{
		"start", "--contract", ReviewIntegrationContractV2, "--cwd", repo, "--agent", string(model.AgentPi),
	}, &output)
	if err == nil {
		t.Fatalf("review start with an unsupported-transport agent succeeded:\n%s", output.String())
	}
	failure := decodeReviewIntegrationFailure(t, output.Bytes())
	if failure.Code != reviewTransportCapabilityUnsupportedCode || failure.NextAction != "stop" {
		t.Fatalf("unsupported transport failure = %#v", failure)
	}

	stores, err := reviewtransaction.DiscoverCompactStores(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(stores) != 0 {
		t.Fatalf("unsupported-transport review start created review authority: %#v", stores)
	}
}

// TestReviewTransportCapabilityRefusalNamesItsExits asserts the refusal a
// Codex user actually hits (the broader admission gate) names the kill
// switch first, then the runtimes that genuinely support review transport —
// read from the compiled capability declaration, never hardcoded prose. This
// repo has shipped refused-as-printed commands twice; execution evidence for
// the exact command lives in the manual verification recorded alongside this
// change, not in this unit test.
func TestReviewTransportCapabilityRefusalNamesItsExits(t *testing.T) {
	repo := initReviewCLIRepo(t)
	writeReviewStartCandidate(t, repo, "candidate.go", "package candidate\n", 0o644)

	var output bytes.Buffer
	err := RunReview([]string{
		"start", "--contract", ReviewIntegrationContractV2, "--cwd", repo, "--agent", string(model.AgentCodex),
	}, &output)
	if err == nil {
		t.Fatalf("review start for Codex succeeded:\n%s", output.String())
	}
	failure := decodeReviewIntegrationFailure(t, output.Bytes())
	if failure.Code != reviewTransportCapabilityUnsupportedCode {
		t.Fatalf("codex refusal code = %q, want %q", failure.Code, reviewTransportCapabilityUnsupportedCode)
	}

	const exitCommand = "gentle-ai review mode disable --scope clone --cwd <repo>"
	exitIndex := strings.Index(failure.Cause, exitCommand)
	if exitIndex < 0 {
		t.Fatalf("refusal cause does not name the kill-switch exit %q:\n%s", exitCommand, failure.Cause)
	}

	const supportedRuntime = "claude-code"
	runtimeIndex := strings.Index(failure.Cause, supportedRuntime)
	if runtimeIndex < 0 {
		t.Fatalf("refusal cause does not name a genuinely supported runtime %q:\n%s", supportedRuntime, failure.Cause)
	}
	if exitIndex > runtimeIndex {
		t.Fatalf("refusal cause names the exit after the supported runtime, want the exit first:\n%s", failure.Cause)
	}
	// The refused runtime itself is legitimately named as the subject of the
	// refusal (e.g. `runtime "codex" does not advertise ...`); what must NOT
	// happen is a refused runtime appearing in the supported-runtimes listing
	// itself, so scope the check to the text after the exit command. OpenCode
	// moved to the other side of this line when issue #2417 restored its
	// genuine provider-injected transport: because the listing is derived
	// from the compiled capability declaration rather than hardcoded prose,
	// it now names opencode without this test changing any fixture.
	listing := failure.Cause[exitIndex:]
	if !strings.Contains(listing, string(model.AgentOpenCode)) {
		t.Errorf("refusal cause's exit/listing segment omits %q, which genuinely supports review transport since #2417:\n%s", model.AgentOpenCode, listing)
	}
	for _, unsupported := range []string{"\"pi\"", " pi,", " pi."} {
		if strings.Contains(listing, unsupported) {
			t.Errorf("refusal cause's exit/listing segment names %q as if it genuinely supported review transport:\n%s", unsupported, listing)
		}
	}
}
