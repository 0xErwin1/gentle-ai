package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func RunWorkRoute(args []string, stdout io.Writer) error {
	return runWorkRoute(
		context.Background(),
		args,
		stdout,
		workprovider.NewDefaultRuntimeController(),
	)
}

func runWorkRoute(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	controller workprovider.RuntimeController,
) error {
	if len(args) == 0 {
		return errors.New(
			"work-route requires the decide or bind-sdd operation",
		)
	}
	switch args[0] {
	case "decide":
		return runWorkRouteDecide(ctx, args[1:], stdout, controller)
	case "bind-sdd":
		return runWorkRouteBindSDD(ctx, args[1:], stdout, controller)
	default:
		return fmt.Errorf(
			"unknown work-route operation %q; only decide and bind-sdd are supported",
			args[0],
		)
	}
}

func runWorkRouteDecide(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	controller workprovider.RuntimeController,
) error {
	if err := validateExactWorkFlags(args, map[string]struct{}{
		"cwd": {}, "work-run": {}, "expected-revision": {},
		"contract": {}, "choice": {}, "json": {},
	}); err != nil {
		return err
	}
	flags := flag.NewFlagSet("work-route decide", flag.ContinueOnError)
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
		"exact WorkRun revision",
	)
	contract := flags.String("contract", "", "exact work-route contract")
	choice := flags.String(
		"choice",
		"",
		"human SDD choice: accept_sdd or decline_sdd",
	)
	asJSON := flags.Bool("json", false, "emit the typed JSON contract")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New(
			"work-route decide accepts no positional arguments",
		)
	}
	if !*asJSON {
		return errors.New("work-route decide requires --json")
	}
	result, err := controller.DecideRoute(
		ctx,
		workprovider.RuntimeRouteDecisionRequest{
			Repo:             *cwd,
			WorkRunID:        *workRunID,
			ExpectedRevision: *expectedRevision,
			Contract:         *contract,
			Choice:           workrun.WorkRouteChoice(*choice),
		},
	)
	if err != nil {
		return err
	}
	return encodeWorkJSON(stdout, result.Output())
}

func runWorkRouteBindSDD(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	controller workprovider.RuntimeController,
) error {
	if err := validateExactWorkFlags(args, map[string]struct{}{
		"cwd": {}, "work-run": {}, "expected-revision": {},
		"contract": {}, "run-ref": {}, "json": {},
	}); err != nil {
		return err
	}
	flags := flag.NewFlagSet("work-route bind-sdd", flag.ContinueOnError)
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
		"exact WorkRun revision",
	)
	contract := flags.String("contract", "", "exact work-route contract")
	runRef := flags.String(
		"run-ref",
		"",
		"existing canonical SDD runtime reference",
	)
	asJSON := flags.Bool("json", false, "emit the typed JSON contract")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New(
			"work-route bind-sdd accepts no positional arguments",
		)
	}
	if !*asJSON {
		return errors.New("work-route bind-sdd requires --json")
	}
	result, err := controller.BindSDDRoute(
		ctx,
		workprovider.RuntimeRouteBindSDDRequest{
			Repo:             *cwd,
			WorkRunID:        *workRunID,
			ExpectedRevision: *expectedRevision,
			Contract:         *contract,
			RunRef:           *runRef,
		},
	)
	if err != nil {
		return err
	}
	return encodeWorkJSON(stdout, result.Output())
}
