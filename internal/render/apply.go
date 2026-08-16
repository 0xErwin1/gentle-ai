package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gentleman-programming/gentle-ai/v2/internal/config"
	"github.com/gentleman-programming/gentle-ai/v2/internal/pipeline"
)

type ApplyRequest struct {
	Diagnostics []config.Diagnostic
	Plan        ReconcilePlan
	Snapshot    Snapshot
	Destination string
	Persist     func() error
}

// Apply executes an admitted reconciliation and compensates applied files if persistence fails.
func Apply(request ApplyRequest) error {
	if err := admitApply(request); err != nil {
		return err
	}

	steps, err := applySteps(request)
	if err != nil {
		return err
	}

	orchestrator := pipeline.NewOrchestrator(pipeline.DefaultRollbackPolicy())
	result := orchestrator.Execute(pipeline.StagePlan{Apply: steps})
	if result.Err != nil {
		return result.Err
	}
	if request.Persist == nil {
		return nil
	}
	if err := request.Persist(); err != nil {
		rollback := orchestrator.Rollback(result)
		if rollback.Err != nil {
			return fmt.Errorf("persist reconciliation: %w; rollback: %v", err, rollback.Err)
		}
		return fmt.Errorf("persist reconciliation: %w", err)
	}
	return nil
}

func admitApply(request ApplyRequest) error {
	if len(request.Diagnostics) > 0 {
		return fmt.Errorf("apply refused: validation diagnostics remain")
	}
	for _, operation := range request.Plan.Operations {
		if operation.Kind == Conflict {
			return fmt.Errorf("apply refused: %s", operation.Code)
		}
	}
	return nil
}

func applySteps(request ApplyRequest) ([]pipeline.Step, error) {
	steps := make([]pipeline.Step, 0, len(request.Plan.Operations))
	for _, operation := range request.Plan.Operations {
		if operation.Kind == Skip {
			continue
		}
		if operation.Kind != Create && operation.Kind != Update && operation.Kind != Remove {
			return nil, fmt.Errorf("apply refused: unsupported operation %q", operation.Kind)
		}
		target, err := stagedPath(request.Destination, operation.Path)
		if err != nil {
			return nil, err
		}
		steps = append(steps, &fileStep{operation: operation, source: filepath.Join(request.Snapshot.Stage, filepath.FromSlash(operation.Path)), target: target})
	}
	return steps, nil
}

type fileStep struct {
	operation      Operation
	source, target string
	before         []byte
	existed        bool
}

func (step *fileStep) ID() string {
	return string(step.operation.Kind) + ":" + step.operation.Path + ":" + step.operation.Selector
}

func (step *fileStep) Run() error {
	data, err := os.ReadFile(step.target)
	if err == nil {
		step.before, step.existed = data, true
	} else if !os.IsNotExist(err) {
		return err
	}
	if step.operation.Selector == "file" {
		if step.operation.Kind == Remove {
			return os.Remove(step.target)
		}
		data, err = os.ReadFile(step.source)
		if err != nil {
			return err
		}
	} else if data, err = applyOpenCodeResource(step.operation, step.source, data); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(step.target), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(step.target, data, 0o644); err != nil {
		return err
	}
	if visible, err := os.ReadFile(step.target); err != nil {
		return fmt.Errorf("verify %q: %w", step.operation.Path, err)
	} else if !bytes.Equal(visible, data) {
		return fmt.Errorf("verify %q: content mismatch", step.operation.Path)
	}
	return nil
}

func applyOpenCodeResource(operation Operation, source string, target []byte) ([]byte, error) {
	name, ok := openCodeAgentName(operation.Selector)
	if !ok {
		return nil, fmt.Errorf("unsupported resource selector %q", operation.Selector)
	}
	settings := map[string]any{}
	if len(target) != 0 {
		if err := json.Unmarshal(target, &settings); err != nil {
			return nil, fmt.Errorf("parse target OpenCode settings: %w", err)
		}
	}
	agents, _ := settings["agent"].(map[string]any)
	if agents == nil {
		agents = map[string]any{}
		settings["agent"] = agents
	}
	if operation.Kind == Remove {
		delete(agents, name)
		return json.Marshal(settings)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}
	var staged map[string]any
	if err := json.Unmarshal(contents, &staged); err != nil {
		return nil, fmt.Errorf("parse staged OpenCode settings: %w", err)
	}
	stagedAgents, _ := staged["agent"].(map[string]any)
	agent, exists := stagedAgents[name]
	if !exists {
		return nil, fmt.Errorf("staged OpenCode agent %q is missing", name)
	}
	agents[name] = agent
	return json.Marshal(settings)
}

func openCodeAgentName(selector string) (string, bool) {
	const prefix = "/agent/"
	if len(selector) <= len(prefix) || selector[:len(prefix)] != prefix {
		return "", false
	}
	return selector[len(prefix):], true
}

func (step *fileStep) Rollback() error {
	if step.existed {
		return os.WriteFile(step.target, step.before, 0o644)
	}
	return os.Remove(step.target)
}
