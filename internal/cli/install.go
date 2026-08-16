package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

type InstallFlags struct {
	Config     string
	Agents     []string
	Components []string
	Skills     []string
	Persona    string
	Preset     string
	SDDMode    string
	Scope      string
	Channel    string
	DryRun     bool

	OpenCodeBackgroundSubagents    string
	OpenCodeBackgroundSubagentsSet bool

	PiBackgroundSubagents    string
	PiBackgroundSubagentsSet bool
}

func HasConfigFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--config" || strings.HasPrefix(arg, "--config=") {
			return true
		}
	}
	return false
}

func ValidateInstallConfigFlags(args []string) error { _, err := ParseInstallFlags(args); return err }

const installChannelHelp = "Gentle AI channel: stable (default), beta, or nightly (alias for beta) — env: GENTLE_AI_CHANNEL"

func PrintInstallHelp(w io.Writer) {
	fmt.Fprint(w, `USAGE
  gentle-ai install [flags]

FLAGS
  --agent, --agents <list>           Agents to install
  --component, --components <list>   Components to install
  --skill, --skills <list>           Skills to install
  --persona <name>                   Persona to apply
  --preset <name>                    Preset to apply
  --sdd-mode single|multi            SDD orchestrator mode
  --scope global|workspace           Install scope (env: GENTLE_AI_INSTALL_SCOPE)
  --channel stable|beta|nightly      Release channel; nightly is an alias for beta (env: GENTLE_AI_CHANNEL)
  --opencode-background-subagents=auto|on|off
                                     Resolve OpenCode capability and manage a launcher when eligible; env: GENTLE_AI_OPENCODE_BACKGROUND_SUBAGENTS
                                     auto inherits managed on/off, unsupported/unknown stays foreground, off removes only owned launchers
  --pi-background-subagents=auto|on|off
                                     Project the resolved Pi background-subagent policy for gentle-pi; env: GENTLE_AI_PI_BACKGROUND_SUBAGENTS
                                     auto inherits managed on/off and never enables by itself; only managed policy files are ever overwritten
  --dry-run                          Preview plan without executing
  --help, -h                         Show this help
`)
}

func ParseInstallFlags(args []string) (InstallFlags, error) {
	var opts InstallFlags

	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})
	fs.StringVar(&opts.Config, "config", "", "desired configuration file")
	registerListFlag(fs, "agent", &opts.Agents)
	registerListFlag(fs, "agents", &opts.Agents)
	registerListFlag(fs, "component", &opts.Components)
	registerListFlag(fs, "components", &opts.Components)
	registerListFlag(fs, "skill", &opts.Skills)
	registerListFlag(fs, "skills", &opts.Skills)
	fs.StringVar(&opts.Persona, "persona", "", "persona to apply")
	fs.StringVar(&opts.Preset, "preset", "", "preset to apply")
	fs.StringVar(&opts.SDDMode, "sdd-mode", "", "SDD orchestrator mode: single or multi (default: single)")
	fs.StringVar(&opts.Scope, "scope", "", "install scope: global (default) or workspace — env: GENTLE_AI_INSTALL_SCOPE")
	fs.StringVar(&opts.Channel, "channel", "", installChannelHelp)
	fs.StringVar(&opts.OpenCodeBackgroundSubagents, "opencode-background-subagents", "", "--opencode-background-subagents=auto|on|off; env: GENTLE_AI_OPENCODE_BACKGROUND_SUBAGENTS; eligible versions use a managed launcher")
	fs.StringVar(&opts.PiBackgroundSubagents, "pi-background-subagents", "", "--pi-background-subagents=auto|on|off; env: GENTLE_AI_PI_BACKGROUND_SUBAGENTS; the resolved policy is projected for gentle-pi")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "preview plan without executing")

	if err := fs.Parse(args); err != nil {
		return InstallFlags{}, err
	}

	if fs.NArg() > 0 {
		return InstallFlags{}, fmt.Errorf("unexpected install argument %q", fs.Arg(0))
	}
	// A document is the whole selection, so a semantic flag alongside it has no
	// unambiguous meaning. Tracking that a flag appeared keeps the config path
	// intact for the error message instead of overwriting it with a marker.
	semanticFlagUsed := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "opencode-background-subagents":
			opts.OpenCodeBackgroundSubagentsSet = true
			semanticFlagUsed = true
		case "pi-background-subagents":
			opts.PiBackgroundSubagentsSet = true
		case "agent", "agents", "component", "components", "skill", "skills", "persona", "preset", "sdd-mode":
			semanticFlagUsed = true
		}
	})
	if opts.Config != "" && semanticFlagUsed {
		return InstallFlags{}, fmt.Errorf("config.flags.exclusive: --config cannot be combined with semantic selection flags; run gentle-ai install --config <path> without selection flags")
	}

	return opts, nil
}

type csvListFlag struct {
	values *[]string
}

func (f csvListFlag) String() string {
	if f.values == nil {
		return ""
	}

	return strings.Join(*f.values, ",")
}

func (f csvListFlag) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		*f.values = append(*f.values, item)
	}

	return nil
}

func registerListFlag(fs *flag.FlagSet, name string, values *[]string) {
	fs.Var(csvListFlag{values: values}, name, "comma-separated list")
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
