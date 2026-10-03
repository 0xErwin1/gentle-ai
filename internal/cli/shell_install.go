package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gentleman-programming/gentle-ai/v3/internal/shellinstaller"
)

const shellInstallHelp = `gentle-ai shell install --target /owned/private-parent/shell --mode separate
  --mode shared --prefix /owned/selected-prefix --agent /owned/selected-agent
  --inspect                 print physical-selection confirmation without effects
  --confirm SHA256          approve that exact inspected selection
No flags: dedicated installer TUI. Commands live in TARGET/bin, outside npm's bin.
gentle-ai shell recover ROOT inspect
  Replace inspect with its printed confirmation to restore shared preimages.
Requires Linux amd64 and qualified cgroup limits or an existing delegated
systemd user manager >=254. No sudo, delegation creation or container fallback.
`

func parseShellInstall(args []string, stdout io.Writer) (shellinstaller.UserInstallRequest, bool, error) {
	var req shellinstaller.UserInstallRequest
	flags := flag.NewFlagSet("shell install", flag.ContinueOnError)
	flags.SetOutput(stdout)
	flags.StringVar(&req.Destination, "target", "", "owned installation target")
	flags.StringVar(&req.Mode, "mode", "separate", "separate or shared")
	flags.StringVar(&req.SharedPrefix, "prefix", "", "selected existing global Pi prefix")
	flags.StringVar(&req.SharedAgent, "agent", "", "selected existing Pi configuration")
	flags.StringVar(&req.Confirmation, "confirm", "", "physical selection SHA256")
	inspect := flags.Bool("inspect", false, "inspect without installation")
	flags.Usage = func() { _, _ = io.WriteString(stdout, shellInstallHelp) }
	if err := flags.Parse(args); err != nil {
		return req, false, err
	}
	if flags.NArg() != 0 {
		return req, false, errors.New("unexpected shell install positional arguments")
	}
	return req, *inspect, nil
}

// Dedicated early route, deliberately independent of the generic installer,
// profile detector, self-update, gate and ordinary Gentle AI startup TUI.
func RunShell(args []string, stdout io.Writer) (resultErr error) {
	defer func() {
		var failure *shellinstaller.PrivateRuntimeError
		if errors.As(resultErr, &failure) && (failure.Workspace != "" || failure.Destination != "") {
			resultErr = fmt.Errorf("%w\nPreserve evidence: workspace=%q destination/unit=%q\nFor shared installation recovery, inspect ROOT=workspace/installed or published destination with gentle-ai shell recover ROOT inspect", resultErr, failure.Workspace, failure.Destination)
		}
	}()
	if len(args) == 0 {
		_, err := io.WriteString(stdout, shellInstallHelp)
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if args[0] != "install" {
		return shellinstaller.RunUserEntry(ctx, self, args, os.Stdin, stdout, os.Stderr)
	}
	if len(args) == 1 {
		model := shellInstallModel{ctx: ctx, cancel: cancel, self: self, stdout: stdout, req: shellinstaller.UserInstallRequest{Mode: "separate"}}
		final, err := tea.NewProgram(model, tea.WithInput(os.Stdin), tea.WithOutput(stdout)).Run()
		if err != nil {
			return err
		}
		return final.(shellInstallModel).err
	}
	req, inspect, err := parseShellInstall(args[1:], stdout)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	token, err := shellinstaller.InspectUserInstall(req)
	if err != nil {
		return err
	}
	if inspect {
		_, err = fmt.Fprintf(stdout, "Confirmation: %s\nCommands: %s/bin/gentle-shell, %s/bin/pi\n", token, req.Destination, req.Destination)
		return err
	}
	if req.Confirmation != token {
		return errors.New("inspect the physical selection first, then pass its --confirm SHA256")
	}
	return shellinstaller.RunUserEntry(ctx, self, append([]string{"install"}, shellEntryValues(req)...), os.Stdin, stdout, os.Stderr)
}

func shellEntryValues(req shellinstaller.UserInstallRequest) []string {
	return []string{req.Destination, req.Mode, req.SharedPrefix, req.SharedAgent, req.Confirmation}
}

type shellInstallDone struct{ err error }

type shellInstallModel struct {
	ctx      context.Context
	cancel   context.CancelFunc
	self     string
	stdout   io.Writer
	req      shellinstaller.UserInstallRequest
	field    int
	review   bool
	busy     bool
	err      error
}

func (m shellInstallModel) Init() tea.Cmd { return nil }

func (m shellInstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if done, ok := msg.(shellInstallDone); ok {
		m.busy, m.err = false, done.err
		return m, tea.Quit
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if key.String() == "ctrl+c" || key.String() == "esc" {
		m.cancel()
		if m.busy {
			return m, nil // Await actual stop/reap; never abandon the install goroutine.
		}
		return m, tea.Quit
	}
	if m.busy {
		return m, nil
	}
	if m.review {
		if key.String() != "y" {
			m.review = false
			return m, nil
		}
		m.busy = true
		return m, func() tea.Msg {
			err := shellinstaller.RunUserEntry(m.ctx, m.self, append([]string{"install"}, shellEntryValues(m.req)...), os.Stdin, m.stdout, os.Stderr)
			return shellInstallDone{err}
		}
	}
	switch key.String() {
	case "tab":
		m.field = (m.field + 1) % 4
	case "enter":
		token, err := shellinstaller.InspectUserInstall(m.req)
		m.err = err
		if err == nil {
			m.req.Confirmation, m.review = token, true
		}
	case "left", "right", " ":
		if m.field == 1 {
			if m.req.Mode == "separate" {
				m.req.Mode = "shared"
			} else {
				m.req.Mode, m.req.SharedPrefix, m.req.SharedAgent = "separate", "", ""
			}
		}
	default:
		fields := []*string{&m.req.Destination, nil, &m.req.SharedPrefix, &m.req.SharedAgent}
		if field := fields[m.field]; field != nil {
			if key.Type == tea.KeyBackspace && len(*field) > 0 {
				value := []rune(*field)
				*field = string(value[:len(value)-1])
			} else if key.Type == tea.KeyRunes {
				*field += string(key.Runes)
			}
		}
	}
	return m, nil
}

func (m shellInstallModel) View() string {
	if m.busy {
		return "Installing selected Gentle Shell. Ctrl-C cancels; waiting for stop/reap.\n"
	}
	rows := []string{"Gentle Shell Linux user installer", "Target: " + m.req.Destination, "Mode: " + m.req.Mode, "Shared prefix: " + m.req.SharedPrefix, "Shared agent: " + m.req.SharedAgent,
		"Commands: " + m.req.Destination + "/bin/gentle-shell and " + m.req.Destination + "/bin/pi", "Tab selects field; arrows change mode; Enter reviews; Escape cancels."}
	rows[m.field+1] = "> " + rows[m.field+1]
	if m.review {
		rows = append(rows, "Confirm this physical selection and both command bindings? y installs; any other key edits.", m.req.Confirmation)
	}
	if m.err != nil {
		rows = append(rows, m.err.Error())
	}
	return strings.Join(rows, "\n") + "\n"
}
