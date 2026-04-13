package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gentleman-programming/gentle-ai/internal/agents"
	"github.com/gentleman-programming/gentle-ai/internal/components/gga"
	"github.com/gentleman-programming/gentle-ai/internal/model"
)

type UninstallFlags struct {
	DryRun      bool
	Force       bool
	PurgeMemory bool
	KeepBackups bool
}

type UninstallTarget struct {
	Path        string
	Description string
}

type UninstallResult struct {
	DryRun    bool
	Cancelled bool
	Targets   []UninstallTarget
	Removed   []string
	Notes     []string
}

var osExecutable = os.Executable

func ParseUninstallFlags(args []string) (UninstallFlags, error) {
	var opts UninstallFlags

	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(ioDiscard{})
	fs.BoolVar(&opts.DryRun, "dry-run", false, "show what would be removed without executing")
	fs.BoolVar(&opts.Force, "force", false, "skip confirmation prompt")
	fs.BoolVar(&opts.PurgeMemory, "purge-memory", false, "also delete ~/.engram")
	fs.BoolVar(&opts.KeepBackups, "keep-backups", false, "preserve ~/.gentle-ai/backups")

	if err := fs.Parse(args); err != nil {
		return UninstallFlags{}, err
	}

	if fs.NArg() > 0 {
		return UninstallFlags{}, fmt.Errorf("unexpected uninstall argument %q", fs.Arg(0))
	}

	return opts, nil
}

func RunUninstall(args []string, stdout io.Writer) error {
	homeDir, err := osUserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	result, err := runUninstallWithHomeDir(args, stdout, os.Stdin, homeDir)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprint(stdout, RenderUninstallReport(result))
	return nil
}

func runUninstallWithHomeDir(args []string, stdout io.Writer, stdin io.Reader, homeDir string) (UninstallResult, error) {
	flags, err := ParseUninstallFlags(args)
	if err != nil {
		return UninstallResult{}, err
	}

	result := planUninstall(homeDir, flags)
	if len(result.Targets) == 0 {
		return result, nil
	}

	if !flags.DryRun && !flags.Force {
		confirmed, err := promptUninstallConfirm(stdin, stdout)
		if err != nil {
			return UninstallResult{}, err
		}
		if !confirmed {
			result.Cancelled = true
			return result, nil
		}
	}

	if flags.DryRun {
		return result, nil
	}

	for _, target := range result.Targets {
		if err := os.RemoveAll(target.Path); err != nil {
			return UninstallResult{}, fmt.Errorf("remove %s: %w", target.Path, err)
		}
		result.Removed = append(result.Removed, target.Path)
	}

	return result, nil
}

func planUninstall(homeDir string, flags UninstallFlags) UninstallResult {
	result := UninstallResult{DryRun: flags.DryRun}
	seen := map[string]bool{}
	add := func(path, description string) {
		if path == "" || seen[path] {
			return
		}
		if _, err := os.Stat(path); err != nil {
			return
		}
		seen[path] = true
		result.Targets = append(result.Targets, UninstallTarget{Path: path, Description: description})
	}

	gentleAIStateDir := filepath.Join(homeDir, ".gentle-ai")
	if flags.KeepBackups {
		add(filepath.Join(gentleAIStateDir, "state.json"), "gentle-ai state file")
	} else {
		add(filepath.Join(gentleAIStateDir, "state.json"), "gentle-ai state file")
		add(filepath.Join(gentleAIStateDir, "backups"), "gentle-ai backups")
	}

	if flags.PurgeMemory {
		add(filepath.Join(homeDir, ".engram"), "Engram memory database")
	}

	add(filepath.Join(homeDir, ".config", "gentle-ai"), "gentle-ai config directory")
	add(gga.ConfigPath(homeDir), "GGA global config")
	add(gga.AgentsTemplatePath(homeDir), "GGA AGENTS template")
	add(gga.RuntimePRModePath(homeDir), "GGA runtime library")
	add(gga.RuntimePS1Path(homeDir), "GGA PowerShell shim")
	add(filepath.Join(homeDir, ".local", "bin", "engram"), "managed engram binary")
	add(filepath.Join(homeDir, ".local", "bin", "engram.exe"), "managed engram binary")

	for _, target := range managedAgentTargets(homeDir) {
		add(target.Path, target.Description)
	}

	if !flags.PurgeMemory {
		result.Notes = append(result.Notes, "Engram memory at ~/.engram is preserved by default (use --purge-memory to delete it)")
	}

	result.Notes = append(result.Notes, uninstallNotes(homeDir)...)
	return result
}

func managedAgentTargets(homeDir string) []UninstallTarget {
	ids := DiscoverAgents(homeDir)
	reg, err := agents.NewDefaultRegistry()
	if err != nil {
		return nil
	}

	targets := make([]UninstallTarget, 0)
	for _, id := range ids {
		adapter, ok := reg.Get(model.AgentID(id))
		if !ok {
			continue
		}
		if adapter.SupportsSkills() {
			targets = append(targets, managedSkillTargets(adapter.SkillsDir(homeDir), string(id))...)
		}
		if adapter.SupportsSlashCommands() {
			targets = append(targets, managedCommandTargets(adapter.CommandsDir(homeDir), string(id))...)
		}
		if adapter.SupportsOutputStyles() {
			styleDir := adapter.OutputStyleDir(homeDir)
			if styleDir != "" {
				targets = append(targets, UninstallTarget{Path: filepath.Join(styleDir, "gentleman.md"), Description: fmt.Sprintf("%s Gentleman output style", id)})
			}
		}
	}

	return targets
}

func managedSkillTargets(skillDir, agentID string) []UninstallTarget {
	if skillDir == "" {
		return nil
	}

	sharedFiles := []string{
		"engram-convention.md",
		"openspec-convention.md",
		"persistence-contract.md",
		"sdd-phase-common.md",
		"skill-resolver.md",
	}
	skillDirs := []string{
		"branch-pr",
		"go-testing",
		"issue-creation",
		"judgment-day",
		"sdd-apply",
		"sdd-archive",
		"sdd-design",
		"sdd-explore",
		"sdd-init",
		"sdd-onboard",
		"sdd-propose",
		"sdd-spec",
		"sdd-tasks",
		"sdd-verify",
		"skill-creator",
		"skill-registry",
	}

	targets := make([]UninstallTarget, 0, len(sharedFiles)+len(skillDirs))
	for _, fileName := range sharedFiles {
		targets = append(targets, UninstallTarget{
			Path:        filepath.Join(skillDir, "_shared", fileName),
			Description: fmt.Sprintf("%s managed shared skill file", agentID),
		})
	}
	for _, dirName := range skillDirs {
		targets = append(targets, UninstallTarget{
			Path:        filepath.Join(skillDir, dirName),
			Description: fmt.Sprintf("%s managed skill %s", agentID, dirName),
		})
	}

	return targets
}

func managedCommandTargets(commandsDir, agentID string) []UninstallTarget {
	if commandsDir == "" {
		return nil
	}

	commandFiles := []string{
		"sdd-apply.md",
		"sdd-archive.md",
		"sdd-continue.md",
		"sdd-explore.md",
		"sdd-ff.md",
		"sdd-init.md",
		"sdd-new.md",
		"sdd-onboard.md",
		"sdd-verify.md",
	}

	targets := make([]UninstallTarget, 0, len(commandFiles))
	for _, fileName := range commandFiles {
		targets = append(targets, UninstallTarget{
			Path:        filepath.Join(commandsDir, fileName),
			Description: fmt.Sprintf("%s managed slash command %s", agentID, fileName),
		})
	}

	return targets
}

func uninstallNotes(homeDir string) []string {
	notes := []string{}

	if exe, err := osExecutable(); err == nil && exe != "" {
		switch {
		case strings.Contains(exe, "/Cellar/") || strings.Contains(exe, "/Homebrew/"):
			notes = append(notes, "Remove the running gentle-ai binary manually after this command exits: brew uninstall gentle-ai")
		case strings.HasPrefix(exe, filepath.Join(homeDir, ".local", "bin")) || strings.HasPrefix(exe, filepath.Join(homeDir, "bin")) || strings.HasPrefix(exe, "/usr/local/bin"):
			notes = append(notes, fmt.Sprintf("Remove the running gentle-ai binary manually after this command exits: rm %q", exe))
		default:
			notes = append(notes, fmt.Sprintf("Current gentle-ai binary still exists at %q and may need manual removal", exe))
		}
	}

	notes = append(notes, "Agent-specific settings overlays (for example JSON settings entries) may still need manual cleanup if you customized them by hand")
	return notes
}

func promptUninstallConfirm(stdin io.Reader, stdout io.Writer) (bool, error) {
	_, _ = fmt.Fprint(stdout, "This will remove Gentle AI managed files from your home directory. Type 'yes' to confirm: ")
	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("read confirmation input: %w", err)
		}
		return false, fmt.Errorf("no confirmation provided (use --force to skip prompt)")
	}
	return strings.EqualFold(strings.TrimSpace(scanner.Text()), "yes"), nil
}

func RenderUninstallReport(result UninstallResult) string {
	var b strings.Builder

	if result.Cancelled {
		b.WriteString("Uninstall cancelled.\n")
		return b.String()
	}

	if result.DryRun {
		b.WriteString("Uninstall (dry-run)\n")
	} else {
		b.WriteString("Uninstall\n")
	}
	b.WriteString("=========\n\n")

	if len(result.Targets) == 0 {
		b.WriteString("  No managed files were found.\n")
	} else {
		b.WriteString("  Targets:\n")
		for _, target := range result.Targets {
			fmt.Fprintf(&b, "  - %s — %s\n", target.Path, target.Description)
		}
	}

	if !result.DryRun && len(result.Removed) > 0 {
		b.WriteString("\n  Removed:\n")
		for _, path := range result.Removed {
			fmt.Fprintf(&b, "  - %s\n", path)
		}
	}

	if len(result.Notes) > 0 {
		b.WriteString("\n  Notes:\n")
		for _, note := range result.Notes {
			fmt.Fprintf(&b, "  - %s\n", note)
		}
	}

	return b.String()
}
