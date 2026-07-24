package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestRunWorkTransitionUnsupportedContractIsJSONAndOpensNothing(t *testing.T) {
	t.Parallel()

	controller, activation, opener := cliController(
		cliValidRepository(),
		workprovider.ActivationEnabled,
	)
	var output bytes.Buffer
	err := runWorkTransition(
		context.Background(),
		[]string{"apply", "--contract=", "--json"},
		&output,
		controller,
	)
	if err != nil {
		t.Fatalf("runWorkTransition() error = %v", err)
	}
	var diagnostic workrun.WorkDiagnosticV1
	if err := json.Unmarshal(output.Bytes(), &diagnostic); err != nil {
		t.Fatalf("decode diagnostic: %v\n%s", err, output.String())
	}
	if diagnostic.Operation != workrun.DiagnosticOperationWorkTransitionApply ||
		diagnostic.Code != "unsupported_contract" ||
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
}

func TestRunWorkTransitionApplyUsesOnlyOpaqueReferenceAndRevision(t *testing.T) {
	t.Parallel()

	repository := cliValidRepository()
	controller, _, _ := cliController(repository, workprovider.ActivationEnabled)
	var output bytes.Buffer
	err := runWorkTransition(
		context.Background(),
		[]string{
			"apply",
			"--cwd", "/repo",
			"--work-run", "cli-work-run",
			"--contract", workrun.WorkTransitionContractV1,
			"--authorization-ref", "authorization:cli-test",
			"--expected-revision", cliRevision("a"),
			"--json",
		},
		&output,
		controller,
	)
	if err != nil {
		t.Fatalf("runWorkTransition() error = %v", err)
	}
	var transition workrun.WorkTransitionV1
	if err := json.Unmarshal(output.Bytes(), &transition); err != nil {
		t.Fatalf("decode transition: %v\n%s", err, output.String())
	}
	if err := transition.Validate(); err != nil {
		t.Fatalf("transition Validate() error = %v", err)
	}
	if repository.resolveCalls != 1 || repository.applyCalls != 1 {
		t.Fatalf(
			"resolve/apply calls = %d/%d",
			repository.resolveCalls,
			repository.applyCalls,
		)
	}
}

func TestRunWorkTransitionStaleAuthorizationDoesNotMutate(t *testing.T) {
	t.Parallel()

	repository := cliValidRepository()
	repository.resolveErr = workprovider.ErrAuthorizationStale
	controller, _, _ := cliController(repository, workprovider.ActivationEnabled)
	err := runWorkTransition(
		context.Background(),
		[]string{
			"apply",
			"--cwd", "/repo",
			"--work-run", "cli-work-run",
			"--contract", workrun.WorkTransitionContractV1,
			"--authorization-ref", "authorization:cli-test",
			"--expected-revision", cliRevision("a"),
			"--json",
		},
		&bytes.Buffer{},
		controller,
	)
	if !errors.Is(err, workprovider.ErrAuthorizationStale) {
		t.Fatalf("runWorkTransition() error = %v", err)
	}
	if repository.applyCalls != 0 {
		t.Fatalf("stale authorization mutated %d times", repository.applyCalls)
	}
}

func TestRunWorkTransitionHasOnlyApplyAndClosedFlags(t *testing.T) {
	t.Parallel()

	controller, _, opener := cliController(
		cliValidRepository(),
		workprovider.ActivationEnabled,
	)
	tests := [][]string{
		nil,
		{"start", "--json"},
		{"issue", "--json"},
		{"apply", "--json", "--plan", "fake"},
		{"apply", "--json", "-cwd", "/repo"},
		{"apply", "--json", "--cwd=x", "--cwd=y"},
		{"apply", "--json", "positional"},
		{"apply", "--contract", workrun.WorkTransitionContractV1},
	}
	for _, args := range tests {
		if err := runWorkTransition(
			context.Background(),
			args,
			&bytes.Buffer{},
			controller,
		); err == nil {
			t.Fatalf("runWorkTransition(%q) error = nil", strings.Join(args, " "))
		}
	}
	if opener.calls != 0 {
		t.Fatalf("invalid transition commands opened repository %d times", opener.calls)
	}
}
