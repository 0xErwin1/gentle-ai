package shellinstaller

import "fmt"

// Profile contains deterministic new-install channel and terminal choices.
type Profile struct {
	Channel            Channel
	TerminalEntryPoint TerminalEntryPoint
}

// NewInstallDefaults selects the stable channel and Pi terminal.
func NewInstallDefaults() Profile {
	return Profile{
		Channel:            DefaultChannel,
		TerminalEntryPoint: TerminalEntryPointPi,
	}
}

// CommandModeIntent records a channel and terminal product requirement only.
// It cannot authorize inspection, consent, execution, replacement or Ready.
type CommandModeIntent struct {
	Channel    Channel
	EntryPoint TerminalEntryPoint
	Effect     CommandModeEffect
}

// DescribeCommandModeIntent is deliberately independent of Validate: the
// gentle-shell install path remains rejected until its own guarded route exists.
func (p Profile) DescribeCommandModeIntent() (CommandModeIntent, error) {
	if !p.Channel.Valid() {
		return CommandModeIntent{}, fmt.Errorf("invalid Gentle-Shell channel %q", p.Channel)
	}
	entryPoint := p.TerminalEntryPoint
	if entryPoint == "" {
		entryPoint = TerminalEntryPointPi // Legacy zero value remains Pi intent.
	}
	effect, ok := entryPoint.DescribeEffect()
	if !ok {
		return CommandModeIntent{}, fmt.Errorf("unsupported terminal entry point %q", entryPoint)
	}
	return CommandModeIntent{Channel: p.Channel, EntryPoint: entryPoint, Effect: effect}, nil
}

func (p Profile) Validate() error {
	if !p.Channel.Valid() {
		return fmt.Errorf("invalid Gentle-Shell channel %q (valid values: stable, main)", p.Channel)
	}
	// The zero value remains a legacy Pi target; syntactic recognition does not
	// authorize Gentle-Shell as an installation route.
	if p.TerminalEntryPoint != "" && p.TerminalEntryPoint != TerminalEntryPointPi {
		return fmt.Errorf("unsupported terminal entry point %q", p.TerminalEntryPoint)
	}
	return nil
}
