package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
)

func RunWorkReconcile(args []string, stdout io.Writer) error {
	return runWorkReconcile(
		context.Background(),
		args,
		stdout,
		workprovider.NewDefaultRuntimeController(),
	)
}

func runWorkReconcile(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	controller workprovider.RuntimeController,
) error {
	if err := validateExactWorkFlags(args, map[string]struct{}{
		"cwd": {}, "work-run": {}, "expected-revision": {},
		"diagnostic-ref": {}, "contract": {}, "json": {},
	}); err != nil {
		return err
	}
	flags := flag.NewFlagSet("work-reconcile", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cwd := flags.String("cwd", "", "repository path")
	workRunID := flags.String(
		"work-run",
		"",
		"provider-owned WorkRun identifier",
	)
	expectedRevision := flags.String(
		"expected-revision",
		"",
		"exact terminal WorkRun revision",
	)
	diagnosticRef := flags.String(
		"diagnostic-ref",
		"",
		"exact owner diagnostic reference projected by work-status",
	)
	contract := flags.String("contract", "", "exact work-reconcile contract")
	asJSON := flags.Bool("json", false, "emit the typed JSON contract")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("work-reconcile accepts no positional arguments")
	}
	if !*asJSON {
		return errors.New("work-reconcile requires --json")
	}
	result, err := controller.Reconcile(
		ctx,
		workprovider.RuntimeReconcileRequest{
			Repo:             *cwd,
			WorkRunID:        *workRunID,
			ExpectedRevision: *expectedRevision,
			DiagnosticRef:    *diagnosticRef,
			Contract:         *contract,
		},
	)
	if err != nil {
		return err
	}
	return encodeWorkJSON(stdout, result.Output())
}
