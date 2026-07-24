package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestWorkProviderCommandsDispatchBeforePlatformDetection(t *testing.T) {
	originalEnsure := ensureCurrentOSSupported
	t.Cleanup(func() { ensureCurrentOSSupported = originalEnsure })
	platformCalls := 0
	ensureCurrentOSSupported = func() error {
		platformCalls++
		return fmt.Errorf("platform detection must not run")
	}

	tests := [][]string{
		{"work-start", "--contract=", "--json"},
		{"work-route", "decide", "--contract=", "--json"},
		{"work-route", "bind-sdd", "--contract=", "--json"},
		{"work-reconcile", "--contract=", "--json"},
		{"work-status", "--contract=", "--json"},
		{"work-transition", "apply", "--contract=", "--json"},
	}
	for _, args := range tests {
		var output bytes.Buffer
		if err := RunArgs(args, &output); err != nil {
			t.Fatalf("RunArgs(%q) error = %v", strings.Join(args, " "), err)
		}
		var diagnostic workrun.WorkDiagnosticV1
		if err := json.Unmarshal(output.Bytes(), &diagnostic); err != nil {
			t.Fatalf("decode %q diagnostic: %v\n%s", args[0], err, output.String())
		}
		if diagnostic.Code != "unsupported_contract" ||
			diagnostic.MutationOutcome != "not_started" {
			t.Fatalf("%q diagnostic = %#v", args[0], diagnostic)
		}
	}
	if platformCalls != 0 {
		t.Fatalf("work provider early dispatch called platform detection %d times", platformCalls)
	}
}

func TestResumableWorkCommandsDispatchBeforePlatformDetection(t *testing.T) {
	originalEnsure := ensureCurrentOSSupported
	t.Cleanup(func() { ensureCurrentOSSupported = originalEnsure })
	platformCalls := 0
	ensureCurrentOSSupported = func() error {
		platformCalls++
		return fmt.Errorf("platform detection must not run")
	}

	for _, args := range [][]string{
		{
			"work-capabilities",
			"--contract=unknown",
			"--json",
		},
		{
			"work-advance",
			"--contract=unknown",
			"--json",
		},
		{
			"work-capabilities",
			"--contract=" + workrun.WorkCapabilitiesContractV1,
			"--json",
		},
		{
			"work-advance",
			"--contract=" + workrun.WorkAdvanceContractV1,
			"--json",
		},
		{
			"work-capabilities",
			"--contract=" + workprovider.RuntimeCapabilitiesContractV2,
			"--json",
		},
		{
			"work-advance",
			"--contract=" + workrun.WorkAdvanceContractV2,
			"--json",
		},
		{
			"work-verification-decide",
			"--contract=" + workrun.WorkVerificationDecideContractV1,
			"--json",
		},
	} {
		var output bytes.Buffer
		if err := RunArgs(args, &output); err == nil {
			t.Fatalf("RunArgs(%q) unexpectedly succeeded", strings.Join(args, " "))
		}
	}
	if platformCalls != 0 {
		t.Fatalf(
			"resumable work early dispatch called platform detection %d times",
			platformCalls,
		)
	}
}

func TestGlobalHelpDocumentsOnlyOpaqueWorkTransitionApply(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	printHelp(&output, "test")
	help := output.String()
	for _, required := range []string{
		"work-capabilities --cwd <repo> --contract gentle-ai.work-capabilities/v2 --json",
		"work-start --cwd <repo> --contract gentle-ai.work-start/v1 --json",
		"work-route decide --cwd <repo> --work-run <id> --expected-revision <revision>",
		"--contract gentle-ai.work-route/v1 --choice <accept_sdd|decline_sdd> --json",
		"work-route bind-sdd --cwd <repo> --work-run <id> --expected-revision <revision>",
		"--contract gentle-ai.work-route/v1 --run-ref <existing-run> --json",
		"work-advance --cwd <repo> --work-run <id> --expected-revision <revision> --contract gentle-ai.work-advance/v2 --json",
		"work-verification-decide --cwd <repo> --work-run <id> --prompt-ref <ref>",
		"--contract gentle-ai.work-verification-decide/v1",
		"--choice <run|defer|reduce_scope|deferred_runner> --json",
		"the receipt never contains or triggers an advance",
		"Only run permits one later work-advance/v2; every other choice stops",
		"work-reconcile --cwd <repo> --work-run <id> --expected-revision <revision>",
		"--diagnostic-ref <ref> --contract gentle-ai.work-reconcile/v1 --json",
		"work-status --cwd <repo> --work-run <id> --contract gentle-ai.work-status/v1 --json",
		"work-transition apply --cwd <repo> --work-run <id> --contract gentle-ai.work-transition/v1",
		"--authorization-ref <ref> --expected-revision <revision> --json",
		"work-capabilities --cwd <repo> --contract gentle-ai.work-capabilities/v1 --json",
		"frozen dormant six-contract compatibility envelope",
		"work-advance --cwd <repo> --work-run <id> --expected-revision <revision> --contract gentle-ai.work-advance/v1 --json",
	} {
		if !strings.Contains(help, required) {
			t.Fatalf("help missing %q:\n%s", required, help)
		}
	}
	for _, forbidden := range []string{
		"work-transition start",
		"work-transition issue",
		"--route",
		"--routing-facts",
		"--plan",
		"--argv",
		"--runner-ref",
		"--command",
	} {
		if strings.Contains(help, forbidden) {
			t.Fatalf("help exposes forbidden work API %q:\n%s", forbidden, help)
		}
	}
}
