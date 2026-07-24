package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

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

func TestGlobalHelpDocumentsOnlyOpaqueWorkTransitionApply(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	printHelp(&output, "test")
	help := output.String()
	for _, required := range []string{
		"work-status --cwd <repo> --work-run <id> --contract gentle-ai.work-status/v1 --json",
		"work-transition apply --cwd <repo> --work-run <id> --contract gentle-ai.work-transition/v1",
		"--authorization-ref <ref> --expected-revision <revision> --json",
	} {
		if !strings.Contains(help, required) {
			t.Fatalf("help missing %q:\n%s", required, help)
		}
	}
	for _, forbidden := range []string{
		"work-transition start",
		"work-transition issue",
		"--plan",
		"--argv",
	} {
		if strings.Contains(help, forbidden) {
			t.Fatalf("help exposes forbidden work API %q:\n%s", forbidden, help)
		}
	}
}
