//go:build !windows

package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

// Linux contract. Other non-Windows hosts reuse it only to reach the
// backend's unsupported-platform refusal; it grants them no support.
const shellInstallHelp = `gentle-ai shell --help      print this help without starting a supervisor
gentle-ai shell install --target /owned/private-parent/shell --mode separate
  --mode shared --prefix /owned/selected-prefix --agent /owned/selected-agent
  --inspect                 print physical-selection confirmation without effects
  --confirm SHA256          approve that exact inspected selection
No flags: dedicated installer TUI. Commands live in TARGET/bin, outside npm's bin.
Installation also writes pinned fd and rg helpers to AGENT/bin, which stock Pi
prefers over PATH: the private agent (Separate) or the selected --agent (Shared).
gentle-ai shell launch ROOT [PI_ARGS...]
  Launch the selected stock Pi; normal use is through TARGET/bin/pi or gentle-shell.
gentle-ai shell recover ROOT inspect
  Replace inspect with its printed confirmation to restore shared preimages.
Requires Linux amd64 and qualified cgroup limits or an existing delegated
systemd user manager >=254. No sudo, delegation creation or container fallback.
No --channel: channel selection is Windows-only.
`

// The flag is registered only so an explicit selector is refused by name
// instead of being dropped: the Linux request has no channel to bind.
func shellInstallPlatformFlags(flags *flag.FlagSet, _ *shellinstaller.UserInstallRequest) func() error {
	channel := flags.String("channel", "", "Windows-only channel selection")
	return func() error {
		explicit := false
		flags.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "channel" })
		if explicit {
			return fmt.Errorf("--channel %q refused: channel selection is Windows-only; omit --channel on this platform", *channel)
		}
		return nil
	}
}

func shellEntryValues(req shellinstaller.UserInstallRequest) []string {
	return []string{req.Destination, req.Mode, req.SharedPrefix, req.SharedAgent, req.Confirmation}
}

func shellInstallInspected(stdout io.Writer, req shellinstaller.UserInstallRequest, token string) error {
	preview, err := shellinstaller.PreviewUserInstall(req, token)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Confirmation: %s\nCommands: %s/bin/gentle-shell, %s/bin/pi\n%s", token, req.Destination, req.Destination, preview)
	return err
}

func runShellInstallTUI(ctx context.Context, cancel context.CancelFunc, self string, stdout io.Writer) error {
	model := shellInstallModel{cancel: cancel, req: shellinstaller.UserInstallRequest{Mode: "separate"}}
	final, err := tea.NewProgram(model, tea.WithInput(os.Stdin), tea.WithOutput(stdout)).Run()
	if err != nil {
		return err
	}
	selection := final.(shellInstallModel)
	if !selection.confirmed {
		return selection.err
	}
	// Run only after Bubble Tea has restored the terminal and released stdin.
	return shellinstaller.RunUserEntry(ctx, self, append([]string{"install"}, shellEntryValues(selection.req)...), os.Stdin, stdout, os.Stderr)
}
