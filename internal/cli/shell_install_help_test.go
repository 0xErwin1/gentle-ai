package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestShellInstallHelpDisclosesSharedAgentTools(t *testing.T) {
	const disclosure = "Shared creates AGENT/bin/fd and AGENT/bin/rg; existing tools refuse before changes."
	for _, args := range [][]string{{"--help"}, {"install", "--help"}} {
		var output bytes.Buffer
		if err := RunShell(args, &output); err != nil {
			t.Fatalf("help %q failed: %v", args, err)
		}
		if output.String() != shellInstallHelp {
			t.Fatalf("help %q changed its output contract: %q", args, output.String())
		}
		if !strings.Contains(output.String(), "\n"+disclosure+"\n") {
			t.Fatalf("help %q omits the exact Shared tool disclosure: %q", args, output.String())
		}
	}
}
