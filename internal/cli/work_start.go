package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
)

const maximumWorkStartInputBytes = 128 << 10

func RunWorkStart(args []string, stdin io.Reader, stdout io.Writer) error {
	return runWorkStart(
		context.Background(),
		args,
		stdin,
		stdout,
		workprovider.NewDefaultRuntimeController(),
	)
}

func runWorkStart(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	controller workprovider.RuntimeController,
) error {
	if err := validateExactWorkFlags(args, map[string]struct{}{
		"cwd": {}, "contract": {}, "json": {},
	}); err != nil {
		return err
	}
	flags := flag.NewFlagSet("work-start", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cwd := flags.String("cwd", "", "repository path")
	contract := flags.String("contract", "", "exact work start contract")
	asJSON := flags.Bool("json", false, "emit the typed JSON contract")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("work-start accepts no positional arguments")
	}
	if !*asJSON {
		return errors.New("work-start requires --json")
	}
	if stdin == nil {
		return errors.New("work-start requires a JSON request on stdin")
	}
	payload, err := io.ReadAll(io.LimitReader(stdin, maximumWorkStartInputBytes+1))
	if err != nil {
		return fmt.Errorf("read work-start request: %w", err)
	}
	if len(payload) > maximumWorkStartInputBytes {
		return errors.New("work-start request exceeds the input limit")
	}

	result, err := controller.Start(ctx, workprovider.RuntimeStartRequest{
		Repo: *cwd, Contract: *contract, Payload: payload,
	})
	if err != nil {
		return err
	}
	return encodeWorkJSON(stdout, result.Output())
}
