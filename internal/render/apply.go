package render

import (
	"bytes"
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

func (step *fileStep) ID() string { return string(step.operation.Kind) + ":" + step.operation.Path }

func (step *fileStep) Run() error {
	data, err := os.ReadFile(step.target)
	if err == nil {
		step.before, step.existed = data, true
	} else if !os.IsNotExist(err) {
		return err
	}
	if step.operation.Kind == Remove {
		return os.Remove(step.target)
	}
	data, err = os.ReadFile(step.source)
	if err != nil {
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

func (step *fileStep) Rollback() error {
	if step.existed {
		return os.WriteFile(step.target, step.before, 0o644)
	}
	return os.Remove(step.target)
}
