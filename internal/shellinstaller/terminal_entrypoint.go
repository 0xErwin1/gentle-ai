package shellinstaller

// TerminalEntryPoint identifies a syntactically recognized terminal target.
type TerminalEntryPoint string

const (
	TerminalEntryPointPi          TerminalEntryPoint = "pi"
	TerminalEntryPointGentleShell TerminalEntryPoint = "gentle-shell"
)

func (entryPoint TerminalEntryPoint) Valid() bool {
	return entryPoint == TerminalEntryPointPi || entryPoint == TerminalEntryPointGentleShell
}

// ExistingPiIntent describes a desired boundary, not permission to change Pi.
type ExistingPiIntent string

const (
	ExistingPiReplaceAfterBoundConsent ExistingPiIntent = "replace-after-instance-consent-and-rollback"
	ExistingPiPreserve                 ExistingPiIntent = "leave-existing-pi-untouched"
)

// PiInstallationIntent describes where a future Pi would live, not an install.
type PiInstallationIntent string

const (
	PiInstallationReplaceExisting PiInstallationIntent = "replace-existing-pi"
	PiInstallationSeparate        PiInstallationIntent = "separate-pi-home-and-executable"
)

// CommandModeEffect is only a product requirement; it is not an approval or Ready state.
type CommandModeEffect struct {
	ExistingPi     ExistingPiIntent
	PiInstallation PiInstallationIntent
}

// DescribeEffect recognizes syntax even when the independent install route is blocked.
func (entryPoint TerminalEntryPoint) DescribeEffect() (CommandModeEffect, bool) {
	switch entryPoint {
	case TerminalEntryPointPi:
		return CommandModeEffect{ExistingPiReplaceAfterBoundConsent, PiInstallationReplaceExisting}, true
	case TerminalEntryPointGentleShell:
		// A separate Pi, home and executable are reached only via gentle-shell.
		return CommandModeEffect{ExistingPiPreserve, PiInstallationSeparate}, true
	default:
		return CommandModeEffect{}, false
	}
}
