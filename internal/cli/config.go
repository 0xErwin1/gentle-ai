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
	"github.com/gentleman-programming/gentle-ai/v2/internal/render"
)

// RunConfig performs read-only declarative configuration operations.
func RunConfig(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: gentle-ai config <validate|render|plan|diff> --config <path>")
	}
	operation := args[0]
	flags := flag.NewFlagSet("config "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "configuration file")
	destination := flags.String("destination", "", "live destination to inspect")
	stage := flags.String("stage", "", "isolated staging root")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *configPath == "" {
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

func writeConfigResult(stdout io.Writer, result map[string]any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
