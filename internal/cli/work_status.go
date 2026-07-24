package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
)

func RunWorkStatus(args []string, stdout io.Writer) error {
	return runWorkStatus(
		context.Background(),
		args,
		stdout,
		workprovider.NewDefaultController(),
	)
}

func runWorkStatus(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	controller workprovider.Controller,
) error {
	if err := validateExactWorkFlags(args, map[string]struct{}{
		"cwd": {}, "work-run": {}, "contract": {}, "json": {},
	}); err != nil {
		return err
	}
	flags := flag.NewFlagSet("work-status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cwd := flags.String("cwd", "", "repository path")
	workRunID := flags.String("work-run", "", "provider-owned WorkRun identifier")
	contract := flags.String("contract", "", "exact work-status contract")
	asJSON := flags.Bool("json", false, "emit the typed JSON contract")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("work-status accepts no positional arguments")
	}
	if !*asJSON {
		return errors.New("work-status requires --json")
	}

	result, err := controller.Status(ctx, workprovider.StatusRequest{
		Repo: *cwd, WorkRunID: *workRunID, Contract: *contract,
	})
	if err != nil {
		return err
	}
	return encodeWorkJSON(stdout, result.Output())
}
