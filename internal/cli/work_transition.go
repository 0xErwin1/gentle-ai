package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
)

func RunWorkTransition(args []string, stdout io.Writer) error {
	return runWorkTransition(
		context.Background(),
		args,
		stdout,
		workprovider.NewDefaultController(),
	)
}

func runWorkTransition(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	controller workprovider.Controller,
) error {
	if len(args) == 0 {
		return errors.New("work-transition requires the apply operation")
	}
	if args[0] != "apply" {
		return fmt.Errorf("unknown work-transition operation %q; only apply is supported", args[0])
	}
	operationArgs := args[1:]
	if err := validateExactWorkFlags(operationArgs, map[string]struct{}{
		"cwd": {}, "work-run": {}, "contract": {}, "authorization-ref": {},
		"expected-revision": {}, "json": {},
	}); err != nil {
		return err
	}
	flags := flag.NewFlagSet("work-transition apply", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cwd := flags.String("cwd", "", "repository path")
	workRunID := flags.String("work-run", "", "provider-owned WorkRun identifier")
	contract := flags.String("contract", "", "exact work-transition contract")
	authorizationRef := flags.String(
		"authorization-ref",
		"",
		"opaque owner-issued authorization reference",
	)
	expectedRevision := flags.String(
		"expected-revision",
		"",
		"exact WorkRun revision",
	)
	asJSON := flags.Bool("json", false, "emit the typed JSON contract")
	if err := flags.Parse(operationArgs); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("work-transition apply accepts no positional arguments")
	}
	if !*asJSON {
		return errors.New("work-transition apply requires --json")
	}

	result, err := controller.Apply(ctx, workprovider.ApplyRequest{
		Repo:             *cwd,
		WorkRunID:        *workRunID,
		Contract:         *contract,
		AuthorizationRef: *authorizationRef,
		ExpectedRevision: *expectedRevision,
	})
	if err != nil {
		return err
	}
	return encodeWorkJSON(stdout, result.Output())
}
