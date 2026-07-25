package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/gentleman-programming/gentle-ai/internal/model"
	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
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
		"cwd": {}, "contract": {}, "agent": {}, "json": {},
	}); err != nil {
		return err
	}
	flags := flag.NewFlagSet("work-capabilities", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cwd := flags.String("cwd", "", "repository path")
	contract := flags.String("contract", "", "exact work capabilities contract")
	agent := flags.String("agent", "", "invoking agent identity")
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

	request := workprovider.RuntimeCapabilitiesRequest{
		Repo: *cwd, Contract: *contract, AgentID: model.AgentID(*agent),
	}
	switch *contract {
	case workprovider.RuntimeCapabilitiesContractV2:
		result, err := controller.CapabilitiesV2(ctx, request)
		if err != nil {
			return err
		}
		return encodeWorkJSON(stdout, result.Output())
	case workrun.WorkCapabilitiesContractV1:
		result, err := controller.Capabilities(ctx, request)
		if err != nil {
			return err
		}
		return encodeWorkJSON(stdout, result.Output())
	default:
		return errors.New("the requested work capabilities contract is unsupported")
	}
}
