package cli

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	configdomain "github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/render"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
)

// RunConfig performs read-only declarative configuration operations.
func RunConfig(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gentle-ai config <validate|render|plan|diff|export>")
	}
	operation := args[0]
	flags := flag.NewFlagSet("config "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "configuration file")
	home := flags.String("home", "", "home directory for persisted state")
	destination := flags.String("destination", "", "live destination to inspect")
	stage := flags.String("stage", "", "isolated staging root")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("config %s does not accept positional arguments; run gentle-ai config %s --help", operation, operation)
	}
	if operation == "export" {
		return exportConfig(stdout, *configPath, *home)
	}
	if *configPath == "" {
		return fmt.Errorf("config %s requires --config; run gentle-ai config %s --config <path>", operation, operation)
	}
	document, err := os.ReadFile(*configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	state, diagnostics := configdomain.Decode(document)
	result := map[string]any{"operation": operation, "diagnostics": diagnostics}
	if len(diagnostics) > 0 || operation == "validate" {
		return writeConfigResult(stdout, result)
	}
	if operation != "render" && operation != "plan" && operation != "diff" {
		return fmt.Errorf("unknown config operation %q; run gentle-ai config <validate|render|plan|diff> --config <path>", operation)
	}
	if *destination == "" || *stage == "" {
		return fmt.Errorf("config %s requires --destination and --stage; run gentle-ai config %s --config <path> --destination <path> --stage <path>", operation, operation)
	}
	baseline, live, err := configBaseline(*destination)
	if err != nil {
		return err
	}
	snapshot, err := render.New(render.OpenCodeProvider{}).Render(render.Request{State: state, Destination: *destination, StageRoot: *stage, Baseline: baseline})
	if err != nil {
		return err
	}
	manifest, err := render.ManifestFor(snapshot)
	if err != nil {
		return err
	}
	result["manifest"] = manifest
	if operation != "render" {
		result["plan"] = render.Plan(render.Manifest{}, manifest, live)
	}
	return writeConfigResult(stdout, result)
}

func configBaseline(destination string) (map[string][]byte, map[render.ResourceKey]string, error) {
	path := filepath.Join(destination, ".config", "opencode", "opencode.json")
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, map[render.ResourceKey]string{}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read destination: %w", err)
	}
	resource := render.Resource{Path: ".config/opencode/opencode.json", Selector: "file"}
	return map[string][]byte{resource.Path: contents}, map[render.ResourceKey]string{{Path: resource.Path, Selector: resource.Selector}: digest(contents)}, nil
}

func digest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return fmt.Sprintf("%x", sum)
}

func loadConfigSelection(path string) (model.Selection, error) {
	document, err := os.ReadFile(path)
	if err != nil {
		return model.Selection{}, fmt.Errorf("read config: %w", err)
	}
	state, diagnostics := configdomain.Decode(document)
	if len(diagnostics) != 0 {
		return model.Selection{}, fmt.Errorf("config validation failed: %s; run gentle-ai config validate --config %q", diagnostics[0].Code, path)
	}
	return configdomain.Project(state), nil
}

func exportConfig(stdout io.Writer, configPath, home string) error {
	if configPath != "" {
		document, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("read config: %w", err)
		}
		desired, diagnostics := configdomain.Decode(document)
		if len(diagnostics) != 0 {
			return writeConfigResult(stdout, configdomain.ExportResult{Diagnostics: diagnostics})
		}
		return writeConfigResult(stdout, configdomain.Export(desired))
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve user home directory: %w", err)
		}
	}
	desired, err := state.ReadDesired(home)
	if err == nil {
		return writeConfigResult(stdout, configdomain.Export(desired))
	}
	legacy, legacyErr := state.Read(home)
	if legacyErr != nil {
		return fmt.Errorf("read desired state: %w", err)
	}
	result := configdomain.Export(configdomain.FromSelection(model.Selection{
		Agents:     legacyAgentIDs(legacy.InstalledAgents),
		Components: legacy.Components,
		Skills:     legacy.Skills,
		Persona:    model.PersonaID(legacy.Persona),
		Preset:     legacy.Preset,
		SDDMode:    legacy.SDDMode,
		StrictTDD:  legacy.StrictTDD,
	}))
	result.Diagnostics = append(result.Diagnostics, configdomain.Diagnostic{
		Code: "config.export.loss.legacy-operational", Path: "$", Severity: configdomain.Error,
		Message: "legacy install state omits runtime and provenance fields from desired configuration",
	})
	result.Lossless = false
	return writeConfigResult(stdout, result)
}

func legacyAgentIDs(values []string) []model.AgentID {
	agents := make([]model.AgentID, 0, len(values))
	for _, value := range values {
		agents = append(agents, model.AgentID(value))
	}
	return agents
}

func writeConfigResult(stdout io.Writer, result any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
