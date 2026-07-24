package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestRunWorkStatusUnsupportedContractIsJSONAndOpensNothing(t *testing.T) {
	t.Parallel()

	for _, contractArg := range []string{"--contract=", "--contract=unknown"} {
		contractArg := contractArg
		t.Run(contractArg, func(t *testing.T) {
			t.Parallel()
			controller, activation, opener := cliController(
				cliValidRepository(),
				workprovider.ActivationEnabled,
			)
			var output bytes.Buffer
			err := runWorkStatus(
				context.Background(),
				[]string{"--json", contractArg},
				&output,
				controller,
			)
			if err != nil {
				t.Fatalf("runWorkStatus() error = %v", err)
			}
			var diagnostic workrun.WorkDiagnosticV1
			if err := json.Unmarshal(output.Bytes(), &diagnostic); err != nil {
				t.Fatalf("decode diagnostic: %v\n%s", err, output.String())
			}
			if diagnostic.Code != "unsupported_contract" ||
				diagnostic.MutationOutcome != "not_started" {
				t.Fatalf("diagnostic = %#v", diagnostic)
			}
			if activation.calls != 0 || opener.calls != 0 {
				t.Fatalf(
					"unsupported contract activation/open = %d/%d",
					activation.calls,
					opener.calls,
				)
			}
		})
	}
}

func TestRunWorkStatusEmitsExactStatusContract(t *testing.T) {
	t.Parallel()

	controller, _, _ := cliController(
		cliValidRepository(),
		workprovider.ActivationReadOnly,
	)
	var output bytes.Buffer
	err := runWorkStatus(
		context.Background(),
		[]string{
			"--cwd", "/repo",
			"--work-run", "cli-work-run",
			"--contract", workrun.WorkStatusContractV1,
			"--json",
		},
		&output,
		controller,
	)
	if err != nil {
		t.Fatalf("runWorkStatus() error = %v", err)
	}
	var status workrun.WorkStatusV1
	if err := json.Unmarshal(output.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v\n%s", err, output.String())
	}
	if err := status.Validate(); err != nil {
		t.Fatalf("status Validate() error = %v", err)
	}
	if status.WorkRunID != "cli-work-run" ||
		status.Contract != workrun.WorkStatusContractV1 {
		t.Fatalf("status = %#v", status)
	}
}

func TestRunWorkStatusKeepsFlagsClosed(t *testing.T) {
	t.Parallel()

	controller, _, opener := cliController(
		cliValidRepository(),
		workprovider.ActivationEnabled,
	)
	tests := [][]string{
		{"--json", "--mystery"},
		{"--json", "-cwd", "/repo"},
		{"--json", "--contract=x", "--contract=y"},
		{"--json", "positional"},
		{"--contract", workrun.WorkStatusContractV1},
	}
	for _, args := range tests {
		var output bytes.Buffer
		if err := runWorkStatus(
			context.Background(),
			args,
			&output,
			controller,
		); err == nil {
			t.Fatalf("runWorkStatus(%q) error = nil", strings.Join(args, " "))
		}
	}
	if opener.calls != 0 {
		t.Fatalf("invalid flags opened repository %d times", opener.calls)
	}
}
