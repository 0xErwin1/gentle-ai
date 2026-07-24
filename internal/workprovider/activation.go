package workprovider

import (
	"context"
	"errors"
	"fmt"
	"os"
)

const WorkRoutingModeEnvironment = "GENTLE_AI_WORK_ROUTING_MODE"

// ActivationMode controls exposure and use of the common-work provider. It is
// deliberately independent from every owner authorization: changing this mode
// can hide or narrow a capability, but can never create a transition.
type ActivationMode string

const (
	ActivationEnabled      ActivationMode = "enabled"
	ActivationRecoveryOnly ActivationMode = "recovery_only"
	ActivationReadOnly     ActivationMode = "read_only"
	ActivationDisabled     ActivationMode = "disabled"

	// DefaultActivationMode remains read-only until the owner repositories and
	// transition applier are integrated and the final ACI activation lands.
	DefaultActivationMode = ActivationReadOnly
)

var ErrInvalidActivationMode = errors.New("invalid work-routing activation mode")

type InvalidActivationModeError struct {
	Value string
}

func (err *InvalidActivationModeError) Error() string {
	return fmt.Sprintf("%v %q", ErrInvalidActivationMode, err.Value)
}

func (err *InvalidActivationModeError) Unwrap() error {
	return ErrInvalidActivationMode
}

func (mode ActivationMode) Validate() error {
	switch mode {
	case ActivationEnabled, ActivationRecoveryOnly, ActivationReadOnly, ActivationDisabled:
		return nil
	default:
		return &InvalidActivationModeError{Value: string(mode)}
	}
}

// ParseActivationMode returns disabled together with a typed error for unknown
// input. Callers therefore fail closed even if they accidentally ignore mode
// and inspect only the returned value.
func ParseActivationMode(value string) (ActivationMode, error) {
	mode := ActivationMode(value)
	if err := mode.Validate(); err != nil {
		return ActivationDisabled, err
	}
	return mode, nil
}

// ActivationResolver is the sole runtime kill-switch seam. Resolution happens
// only after the explicit contract preflight has succeeded.
type ActivationResolver interface {
	ResolveActivation(context.Context, string) (ActivationMode, error)
}

type StaticActivationResolver struct {
	Mode ActivationMode
}

func (resolver StaticActivationResolver) ResolveActivation(
	_ context.Context,
	_ string,
) (ActivationMode, error) {
	if err := resolver.Mode.Validate(); err != nil {
		return ActivationDisabled, err
	}
	return resolver.Mode, nil
}

// EnvironmentActivationResolver reads one operator-owned bootstrap setting.
// Unset means read-only. An explicitly empty or unknown value is invalid and
// resolves to disabled; it never silently enables the provider.
type EnvironmentActivationResolver struct {
	LookupEnv func(string) (string, bool)
}

func (resolver EnvironmentActivationResolver) ResolveActivation(
	_ context.Context,
	_ string,
) (ActivationMode, error) {
	lookup := resolver.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	value, exists := lookup(WorkRoutingModeEnvironment)
	if !exists {
		return DefaultActivationMode, nil
	}
	return ParseActivationMode(value)
}
