package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/gentleman-programming/gentle-ai/internal/workprovider"
	"github.com/gentleman-programming/gentle-ai/internal/workrun"
)

func RunWorkVerificationDecide(args []string, stdout io.Writer) error {
	return runWorkVerificationDecide(
		context.Background(),
		args,
		stdout,
		workprovider.NewDefaultRuntimeController(),
	)
}

func runWorkVerificationDecide(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	controller workprovider.RuntimeController,
) error {
	if err := validateExactWorkFlags(args, map[string]struct{}{
		"cwd": {}, "work-run": {}, "prompt-ref": {},
		"contract": {}, "choice": {}, "json": {},
	}); err != nil {
		return err
	}
	flags := flag.NewFlagSet(
		"work-verification-decide",
		flag.ContinueOnError,
	)
	flags.SetOutput(io.Discard)
	cwd := flags.String("cwd", "", "repository path")
	workRunID := flags.String(
		"work-run",
		"",
		"provider-owned WorkRun identifier",
	)
	promptRef := flags.String(
		"prompt-ref",
		"",
		"opaque owner-authored verification prompt reference",
	)
	contract := flags.String(
		"contract",
		"",
		"exact work verification decision contract",
	)
	choice := flags.String(
		"choice",
		"",
		"one exact owner-offered verification choice",
	)
	asJSON := flags.Bool("json", false, "emit the typed JSON contract")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New(
			"work-verification-decide accepts no positional arguments",
		)
	}
	if !*asJSON {
		return errors.New("work-verification-decide requires --json")
	}
	result, err := controller.DecideVerification(
		ctx,
		workprovider.RuntimeVerificationDecisionRequest{
			Repo:      *cwd,
			WorkRunID: *workRunID,
			Contract:  *contract,
			PromptRef: *promptRef,
			Choice: workrun.VerificationDecisionChoice(
				*choice,
			),
		},
	)
	if err != nil {
		return err
	}
	return encodeWorkJSON(stdout, result.Output())
}
