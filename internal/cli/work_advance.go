package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
)

func RunWorkAdvance(args []string, stdout io.Writer) error {
	return runWorkAdvance(
		context.Background(),
		args,
		stdout,
		workprovider.NewDefaultRuntimeController(),
	)
}

func runWorkAdvance(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	controller workprovider.RuntimeController,
) error {
	if err := validateExactWorkFlags(args, map[string]struct{}{
		"cwd": {}, "work-run": {}, "expected-revision": {},
		"contract": {}, "json": {},
	}); err != nil {
		return err
	}
	flags := flag.NewFlagSet("work-advance", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cwd := flags.String("cwd", "", "repository path")
	workRunID := flags.String("work-run", "", "provider-owned WorkRun identifier")
	expectedRevision := flags.String(
		"expected-revision",
		"",
		"exact WorkRun revision",
	)
	contract := flags.String("contract", "", "exact work-advance contract")
	asJSON := flags.Bool("json", false, "emit the typed JSON contract")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("work-advance accepts no positional arguments")
	}
	if !*asJSON {
		return errors.New("work-advance requires --json")
	}
	result, err := controller.Advance(ctx, workprovider.RuntimeAdvanceRequest{
		Repo:             *cwd,
		WorkRunID:        *workRunID,
		ExpectedRevision: *expectedRevision,
		Contract:         *contract,
	})
	if err != nil {
		return err
	}
	return encodeWorkJSON(stdout, result.Output())
}
