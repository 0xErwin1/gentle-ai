package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
)

func RunWorkCapabilities(args []string, stdout io.Writer) error {
	return runWorkCapabilities(
		context.Background(),
		args,
		stdout,
		workprovider.NewDefaultRuntimeController(),
	)
}

func runWorkCapabilities(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	controller workprovider.RuntimeController,
) error {
	if err := validateExactWorkFlags(args, map[string]struct{}{
		"cwd": {}, "contract": {}, "json": {},
	}); err != nil {
		return err
	}
	flags := flag.NewFlagSet("work-capabilities", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cwd := flags.String("cwd", "", "repository path")
	contract := flags.String("contract", "", "exact work capabilities contract")
	asJSON := flags.Bool("json", false, "emit the typed JSON contract")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("work-capabilities accepts no positional arguments")
	}
	if !*asJSON {
		return errors.New("work-capabilities requires --json")
	}

	result, err := controller.Capabilities(
		ctx,
		workprovider.RuntimeCapabilitiesRequest{
			Repo: *cwd, Contract: *contract,
		},
	)
	if err != nil {
		return err
	}
	return encodeWorkJSON(stdout, result.Output())
}
