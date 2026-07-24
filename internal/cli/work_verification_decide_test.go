package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func TestRunWorkVerificationDecideKeepsOwnerAuthorityClosed(t *testing.T) {
	t.Parallel()
	base := []string{
		"--cwd", "/repo",
		"--work-run", "verification-cli",
		"--prompt-ref", cliVerificationRef("prompt"),
		"--contract", workrun.WorkVerificationDecideContractV1,
		"--choice", "run",
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "json is required",
			args: append([]string{}, base...),
			want: "requires --json",
		},
		{
			name: "caller cannot supply CAS",
			args: append(
				append([]string{}, base...),
				"--json",
				"--expected-revision",
				cliVerificationRef("cas"),
			),
			want: "not defined",
		},
		{
			name: "caller cannot name runner",
			args: append(
				append([]string{}, base...),
				"--json",
				"--runner-ref",
				cliVerificationRef("runner"),
			),
			want: "not defined",
		},
		{
			name: "caller cannot supply command",
			args: append(
				append([]string{}, base...),
				"--json",
				"--command",
				"go test ./...",
			),
			want: "not defined",
		},
		{
			name: "positional argument",
			args: append(append([]string{}, base...), "--json", "advance"),
			want: "no positional arguments",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opener := &cliRuntimeOpener{}
			err := runWorkVerificationDecide(
				context.Background(),
				test.args,
				&bytes.Buffer{},
				workprovider.NewRuntimeController(opener),
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf(
					"runWorkVerificationDecide(%q) error = %v",
					test.args,
					err,
				)
			}
			if opener.calls != 0 {
				t.Fatalf("invalid CLI opened runtime %d times", opener.calls)
			}
		})
	}
}

func TestRunWorkVerificationDecideRejectsUnsupportedInputBeforeOpen(
	t *testing.T,
) {
	t.Parallel()
	tests := [][]string{
		{"--contract=unknown", "--json"},
		{
			"--cwd=/repo",
			"--work-run=verification-cli",
			"--prompt-ref=" + cliVerificationRef("prompt"),
			"--contract=" + workrun.WorkVerificationDecideContractV1,
			"--choice=retry",
			"--json",
		},
	}
	for _, args := range tests {
		opener := &cliRuntimeOpener{}
		err := runWorkVerificationDecide(
			context.Background(),
			args,
			&bytes.Buffer{},
			workprovider.NewRuntimeController(opener),
		)
		if err == nil {
			t.Fatalf("runWorkVerificationDecide(%q) succeeded", args)
		}
		if opener.calls != 0 {
			t.Fatalf(
				"invalid verification decision opened runtime %d times",
				opener.calls,
			)
		}
	}
}

func TestRunWorkVerificationDecideEmitsPureReceipt(t *testing.T) {
	t.Parallel()
	const workRunID = "verification-cli"
	promptRef := cliVerificationRef("prompt")
	decision := cliVerificationDecision(workRunID, promptRef)
	runtime := &cliRuntime{decision: decision}
	opener := &cliRuntimeOpener{runtime: runtime}
	var output bytes.Buffer
	err := runWorkVerificationDecide(
		context.Background(),
		[]string{
			"--cwd=/repo",
			"--work-run=" + workRunID,
			"--prompt-ref=" + promptRef,
			"--contract=" + workrun.WorkVerificationDecideContractV1,
			"--choice=run",
			"--json",
		},
		&output,
		workprovider.NewRuntimeController(opener),
	)
	if err != nil {
		t.Fatal(err)
	}
	expectedJSON, err := json.MarshalIndent(decision, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	expectedJSON = append(expectedJSON, '\n')
	if !bytes.Equal(output.Bytes(), expectedJSON) {
		t.Fatalf(
			"verification decision JSON differs:\ngot:\n%s\nwant:\n%s",
			output.Bytes(),
			expectedJSON,
		)
	}
	if opener.calls != 1 ||
		opener.repo != "/repo" ||
		!reflect.DeepEqual(runtime.decisionRuns, []string{workRunID}) ||
		!reflect.DeepEqual(runtime.decisionPrompts, []string{promptRef}) ||
		!reflect.DeepEqual(
			runtime.decisionChoices,
			[]workrun.VerificationDecisionChoice{
				workrun.VerificationDecisionRun,
			},
		) {
		t.Fatalf(
			"decision authority = %d/%q/%#v/%#v/%#v",
			opener.calls,
			opener.repo,
			runtime.decisionRuns,
			runtime.decisionPrompts,
			runtime.decisionChoices,
		)
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if _, mixed := envelope["advance"]; mixed {
		t.Fatal("verification decision receipt exposed inline advance")
	}
}

func cliVerificationDecision(
	workRunID string,
	promptRef string,
) workrun.WorkVerificationDecideV1 {
	return workrun.WorkVerificationDecideV1{
		Schema:           workrun.WorkVerificationDecideContractV1,
		Contract:         workrun.WorkVerificationDecideContractV1,
		WorkRunID:        workRunID,
		PreviousRevision: cliVerificationRef("previous"),
		PromptRef:        promptRef,
		ForecastRef:      cliVerificationRef("forecast"),
		AssumptionsRef:   cliVerificationRef("assumptions"),
		Choice:           workrun.VerificationDecisionRun,
		DispositionRef:   cliVerificationRef("disposition"),
		Status: workrun.WorkStatusV1{
			Schema:              workrun.WorkStatusContractV1,
			Contract:            workrun.WorkStatusContractV1,
			WorkRunID:           workRunID,
			Revision:            cliVerificationRef("decided"),
			PublicState:         workrun.PublicStateChecking,
			RouteDecision:       workrun.RouteDecisionDirectInline,
			RoutePhase:          workrun.RoutePhaseImplementationSelected,
			ImplementationRoute: workrun.ImplementationRouteDirectInline,
			Verification: workrun.VerificationSummaryV1{
				Outcome:    workrun.VerificationPending,
				ResultRefs: []string{},
			},
			DeliveryIntentRef: cliVerificationRef("intent"),
		},
	}
}

func cliVerificationRef(label string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(label)))
}
