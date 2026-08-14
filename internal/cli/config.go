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

var writeConfigState = state.WriteDesiredAndManifest

// RunConfig performs declarative configuration operations.
func RunConfig(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gentle-ai config <validate|render|plan|diff|apply|reconcile|export>")
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
	desired, diagnostics := configdomain.Decode(document)
	result := map[string]any{"operation": operation, "diagnostics": diagnostics}
	if len(diagnostics) > 0 || operation == "validate" {
		return writeConfigResult(stdout, result)
	}
	if operation != "render" && operation != "plan" && operation != "diff" && operation != "apply" && operation != "reconcile" {
		return fmt.Errorf("unknown config operation %q; run gentle-ai config <validate|render|plan|diff|apply|reconcile> --config <path>", operation)
	}
	if *destination == "" || *stage == "" {
		return fmt.Errorf("config %s requires --destination and --stage; run gentle-ai config %s --config <path> --destination <path> --stage <path>", operation, operation)
	}
	previous := configdomain.DesiredState{}
	if operation == "apply" || operation == "reconcile" {
		if *home == "" {
			return fmt.Errorf("config %s requires --home for persisted desired state", operation)
		}
		previous, err = readConfigDesired(*home)
		if err != nil {
			return err
		}
	}
	baseline, live, err := configBaseline(*destination, previous)
	if err != nil {
		return err
	}
	snapshot, err := render.New(render.OpenCodeProvider{}).Render(render.Request{State: desired, Destination: *destination, StageRoot: *stage, Baseline: baseline})
	if err != nil {
		return err
	}
	manifest, err := render.ManifestFor(snapshot)
	if err != nil {
		return err
	}
	result["manifest"] = manifest
	if operation == "render" {
		return writeConfigResult(stdout, result)
	}
	plan := render.Plan(render.Manifest{}, manifest, live)
	if operation == "apply" || operation == "reconcile" {
		previous, err := readConfigManifest(*home, *destination)
		if err != nil {
			return err
		}
		plan = render.Plan(previous, manifest, live)
		if err := render.Apply(render.ApplyRequest{
			Plan: plan, Snapshot: snapshot, Destination: *destination,
			Persist: func() error { return writeConfigState(*home, *destination, desired, manifest) },
		}); err != nil {
			return err
		}
	}
	result["plan"] = plan
	return writeConfigResult(stdout, result)
}

func readConfigManifest(home, destination string) (render.Manifest, error) {
	manifest, err := state.ReadManifest(home, destination)
	if os.IsNotExist(err) {
		return render.Manifest{}, nil
	}
	if err != nil {
		return render.Manifest{}, fmt.Errorf("read managed manifest: %w", err)
	}
	return manifest, nil
}

func readConfigDesired(home string) (configdomain.DesiredState, error) {
	desired, err := state.ReadDesired(home)
	if os.IsNotExist(err) {
		return configdomain.DesiredState{}, nil
	}
	if err != nil {
		return configdomain.DesiredState{}, fmt.Errorf("read desired state: %w", err)
	}
	return desired, nil
}

func configBaseline(destination string, previous ...configdomain.DesiredState) (map[string][]byte, map[render.ResourceKey]string, error) {
	path := filepath.Join(destination, ".config", "opencode", "opencode.json")
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, map[render.ResourceKey]string{}, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read destination: %w", err)
	}
	liveDigest := digest(contents)
	prior := configdomain.DesiredState{}
	if len(previous) > 0 {
		prior = previous[0]
	}
	contents, err = withoutPriorRoleNames(contents, prior)
	if err != nil {
		return nil, nil, err
	}
	resource := render.Resource{Path: ".config/opencode/opencode.json", Selector: "file"}
	return map[string][]byte{resource.Path: contents}, map[render.ResourceKey]string{{Path: resource.Path, Selector: resource.Selector}: liveDigest}, nil
}

func withoutPriorRoleNames(contents []byte, previous configdomain.DesiredState) ([]byte, error) {
	if len(previous.Roles) == 0 {
		return contents, nil
	}
	var settings map[string]any
	if err := json.Unmarshal(contents, &settings); err != nil {
		return nil, fmt.Errorf("read destination: parse OpenCode settings: %w", err)
	}
	agents, _ := settings["agent"].(map[string]any)
	for _, role := range previous.Roles {
		name := role.RenderedName
		if name == "" {
			name = string(role.ID)
		}
		delete(agents, name)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("read destination: encode OpenCode settings: %w", err)
	}
	return encoded, nil
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
	if !legacy.SelectionConfigured {
		result.Diagnostics = append(result.Diagnostics, configdomain.Diagnostic{
			Code: "config.export.loss.ambiguous-intent", Path: "$", Severity: configdomain.Error,
			Message: "legacy install state cannot distinguish inferred defaults from an explicit selection",
		})
	}
	if legacy.RDDMode != "" {
		result.Diagnostics = append(result.Diagnostics, configdomain.Diagnostic{
			Code: "config.export.loss.user-owned", Path: "$.rdd_mode", Severity: configdomain.Error,
			Message: "user-owned review policy remains local and is excluded from desired configuration",
		})
	}
	result.Lossless = false
	return writeConfigResult(stdout, result)
}

func legacyAgentIDs(values []string) []model.AgentID {
	agents := make([]model.AgentID, len(values))
	for index, value := range values {
		agents[index] = model.AgentID(value)
	}
	return agents
}

func writeConfigResult(stdout io.Writer, result any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
