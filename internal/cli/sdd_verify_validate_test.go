package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/sddstatus"
)

func TestRunSDDVerifyValidate(t *testing.T) {
	report := "```yaml\nschema: gentle-ai.verify-result/v1\nevidence_revision: sha256:" + strings.Repeat("a", 64) + "\nverdict: fail\nblockers: 1\ncritical_findings: 0\nrequirements: 1/1\nscenarios: 1/1\ntest_command: go test ./...\ntest_exit_code: 0\ntest_output_hash: sha256:" + strings.Repeat("b", 64) + "\nbuild_command: go vet ./...\nbuild_exit_code: 0\nbuild_output_hash: sha256:" + strings.Repeat("c", 64) + "\n```"
	var output bytes.Buffer
	if err := runSDDVerifyValidate([]string{"--input", "-", "--requirements", "1", "--scenarios", "1"}, strings.NewReader(report), &output); err != nil {
		t.Fatalf("valid failure: %v", err)
	}
	if got := output.String(); !strings.Contains(got, `"valid": true`) || !strings.Contains(got, `"verdict": "fail"`) {
		t.Fatalf("output = %s", got)
	}
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSDDVerifyValidate([]string{"--input", path, "--requirements", "1", "--scenarios", "1"}, strings.NewReader("unused"), &bytes.Buffer{}); err != nil {
		t.Fatalf("file input: %v", err)
	}

	for _, tt := range []struct {
		name        string
		args        []string
		input, want string
	}{
		{"front matter", []string{"--input", "-", "--requirements", "1", "--scenarios", "1"}, "---\n" + report, "front matter"},
		{"missing count", []string{"--input", "-", "--requirements", "1"}, report, "requires --scenarios"},
		{"negative count", []string{"--input", "-", "--requirements", "-1", "--scenarios", "1"}, report, "nonnegative"},
		{"unexpected argument", []string{"--input", "-", "--requirements", "1", "--scenarios", "1", "extra"}, report, "unexpected"},
		{"oversized", []string{"--input", "-", "--requirements", "1", "--scenarios", "1"}, strings.Repeat("x", 1<<20+1), "exceeds"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := runSDDVerifyValidate(tt.args, strings.NewReader(tt.input), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRunSDDVerifyValidateHelpIsSuccessfulAndInputFree(t *testing.T) {
	stdin := &sddVerifyValidateReadSpy{}
	var output bytes.Buffer
	err := runSDDVerifyValidate([]string{
		"--input", filepath.Join(t.TempDir(), "missing.md"), "--requirements", "-1", "--help", "--scenarios", "-1",
	}, stdin, &output)
	if err != nil {
		t.Fatalf("runSDDVerifyValidate(--help): %v", err)
	}
	if stdin.reads != 0 {
		t.Fatalf("help read stdin %d times", stdin.reads)
	}
	for _, want := range []string{"Usage: gentle-ai sdd-verify-validate", "--input <path|->", "--requirements <n>", "--scenarios <n>"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("help missing %q:\n%s", want, output.String())
		}
	}
}

func TestRunSDDVerifyValidateHelpRendersAuthorityOnlyContract(t *testing.T) {
	var output bytes.Buffer
	if err := runSDDVerifyValidate([]string{"-h"}, strings.NewReader("must not be read"), &output); err != nil {
		t.Fatalf("runSDDVerifyValidate(-h): %v", err)
	}
	contract := sddstatus.VerifyReportValidationContract()
	for _, want := range append(append([]string{contract.Schema, contract.EmptyOutputHash}, contract.RequiredFields...), append(contract.Verdicts, contract.AuthorityOnlyFields...)...) {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("help missing contract value %q:\n%s", want, output.String())
		}
	}
	if !strings.Contains(output.String(), "maximum report size: 1048576 bytes (1 MiB)") ||
		!strings.Contains(output.String(), "test_exit_code 125") || !strings.Contains(output.String(), "build_exit_code 125") {
		t.Fatalf("help omits authority-only limits:\n%s", output.String())
	}
}

func TestRunSDDVerifyValidateRequiredFlagsRemainRequired(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, "requires --input"},
		{[]string{"--input", "-"}, "requires --requirements"},
		{[]string{"--input", "-", "--requirements", "1"}, "requires --scenarios"},
	}
	for _, tt := range tests {
		var output bytes.Buffer
		err := runSDDVerifyValidate(tt.args, strings.NewReader(""), &output)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("runSDDVerifyValidate(%v) = %v, want %q", tt.args, err, tt.want)
		}
	}
}

type sddVerifyValidateReadSpy struct{ reads int }

func (spy *sddVerifyValidateReadSpy) Read([]byte) (int, error) {
	spy.reads++
	return 0, errors.New("help must not read stdin")
}
