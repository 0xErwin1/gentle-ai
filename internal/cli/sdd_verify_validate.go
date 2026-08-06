package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gentleman-programming/gentle-ai/v2/internal/sddstatus"
)

const maxVerifyReportBytes = sddstatus.MaxVerifyReportBytes

// RunSDDVerifyValidate validates a complete report without touching an artifact store.
func RunSDDVerifyValidate(args []string, stdout io.Writer) error {
	return runSDDVerifyValidate(args, os.Stdin, stdout)
}

func runSDDVerifyValidate(args []string, stdin io.Reader, stdout io.Writer) error {
	if hasSDDVerifyValidateHelp(args) {
		return renderSDDVerifyValidateHelp(stdout)
	}
	flags := flag.NewFlagSet("sdd-verify-validate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := flags.String("input", "", "report path or - for stdin")
	requirements := flags.Int("requirements", -2, "authoritative requirement count")
	scenarios := flags.Int("scenarios", -2, "authoritative scenario count")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected sdd-verify-validate argument %q", flags.Arg(0))
	}
	if strings.TrimSpace(*input) == "" {
		return errors.New("sdd-verify-validate requires --input")
	}
	if *requirements == -2 {
		return errors.New("sdd-verify-validate requires --requirements")
	}
	if *scenarios == -2 {
		return errors.New("sdd-verify-validate requires --scenarios")
	}
	if *requirements < 0 || *scenarios < 0 {
		return errors.New("requirement and scenario counts must be nonnegative")
	}
	reader := stdin
	if *input != "-" {
		file, err := os.Open(*input)
		if err != nil {
			return fmt.Errorf("read verify report: %w", err)
		}
		defer file.Close()
		reader = file
	}
	payload, err := io.ReadAll(io.LimitReader(reader, maxVerifyReportBytes+1))
	if err != nil {
		return fmt.Errorf("read verify report: %w", err)
	}
	if len(payload) > maxVerifyReportBytes {
		return fmt.Errorf("verify report exceeds %d-byte limit", maxVerifyReportBytes)
	}
	admission := sddstatus.ValidateVerifyReportAdmission(string(payload), sddstatus.SpecCounts{Requirements: *requirements, Scenarios: *scenarios})
	if !admission.Valid {
		return fmt.Errorf("verify report admission denied: %s", admission.Reason)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(admission)
}

func hasSDDVerifyValidateHelp(args []string) bool {
	for _, argument := range args {
		if argument == "--help" || argument == "-h" {
			return true
		}
	}
	return false
}

func renderSDDVerifyValidateHelp(stdout io.Writer) error {
	contract := sddstatus.VerifyReportValidationContract()
	_, _ = fmt.Fprintln(stdout, "Usage: gentle-ai sdd-verify-validate --input <path|-> --requirements <n> --scenarios <n>")
	_, _ = fmt.Fprintln(stdout, "\nRequired flags:")
	_, _ = fmt.Fprintln(stdout, "  --input <path|->      Verify report path; use - to read stdin")
	_, _ = fmt.Fprintln(stdout, "  --requirements <n>    Authoritative nonnegative requirement total")
	_, _ = fmt.Fprintln(stdout, "  --scenarios <n>       Authoritative nonnegative scenario total")
	_, _ = fmt.Fprintln(stdout, "\nReport contract:")
	_, _ = fmt.Fprintf(stdout, "  schema: %s\n", contract.Schema)
	_, _ = fmt.Fprintf(stdout, "  required envelope fields: %s\n", strings.Join(contract.RequiredFields, ", "))
	_, _ = fmt.Fprintf(stdout, "  accepted verdicts: %s\n", strings.Join(contract.Verdicts, ", "))
	_, _ = fmt.Fprintf(stdout, "  maximum report size: %d bytes (1 MiB)\n", contract.MaxBytes)
	_, _ = fmt.Fprintln(stdout, "  requirements and scenarios are completed/total; each completed count must not exceed its total.")
	_, _ = fmt.Fprintln(stdout, "  --requirements and --scenarios must exactly equal their report totals.")
	_, _ = fmt.Fprintln(stdout, "\nAuthority-only fail extension:")
	_, _ = fmt.Fprintf(stdout, "  all-or-none fields: %s\n", strings.Join(contract.AuthorityOnlyFields, ", "))
	_, _ = fmt.Fprintln(stdout, "  requires verdict fail, test_exit_code 125, build_exit_code 125, and nonzero blockers and critical_findings.")
	_, _ = fmt.Fprintln(stdout, "  values: authority_only_failure=true, missing_review_authority=true, substantive_failure=false, command_failed=false.")
	_, _ = fmt.Fprintf(stdout, "  test_output_hash and build_output_hash must equal %s.\n", contract.EmptyOutputHash)
	return nil
}
