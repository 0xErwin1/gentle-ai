package workprovider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/model"
)

// EffectiveCapabilityManifest is an immutable runtime view. Keeping the
// canonical ACI value private prevents callers from treating an enabled overlay
// as the frozen dormant manifest or attempting to validate it with the wrong
// contract.
type EffectiveCapabilityManifest struct {
	manifest capabilitymanifest.AgentCapabilityManifest
	mode     ActivationMode
}

// EffectiveCapabilityManifest resolves the central activation policy before
// overlaying ACI exposure. Callers cannot select an exposure directly.
func (controller Controller) EffectiveCapabilityManifest(
	ctx context.Context,
	repo string,
	agent model.AgentID,
) (EffectiveCapabilityManifest, error) {
	mode, err := controller.resolveActivation(ctx, repo)
	if err != nil {
		return EffectiveCapabilityManifest{}, err
	}
	return effectiveAgentCapabilityManifest(agent, mode)
}

// effectiveAgentCapabilityManifest overlays runtime exposure without changing
// the canonical dormant manifest or its frozen digest. Only enabled advertises
// the capability; recovery/read-only/disabled modes remain explicitly dormant.
func effectiveAgentCapabilityManifest(
	agent model.AgentID,
	mode ActivationMode,
) (EffectiveCapabilityManifest, error) {
	if err := mode.Validate(); err != nil {
		return EffectiveCapabilityManifest{}, err
	}
	manifest, err := capabilitymanifest.ForAgent(agent)
	if err != nil {
		return EffectiveCapabilityManifest{}, err
	}
	if mode == ActivationEnabled {
		manifest.Contracts.WorkRoutingV1.Exposure =
			capabilitymanifest.ContractExposureAdvertised
	}
	if err := validateEffectiveManifest(manifest, mode); err != nil {
		return EffectiveCapabilityManifest{}, err
	}
	return EffectiveCapabilityManifest{manifest: manifest, mode: mode}, nil
}

func (manifest EffectiveCapabilityManifest) Validate() error {
	return validateEffectiveManifest(manifest.manifest, manifest.mode)
}

func (manifest EffectiveCapabilityManifest) Advertises(
	contract capabilitymanifest.ContractID,
) bool {
	return manifest.manifest.Advertises(contract)
}

func (manifest EffectiveCapabilityManifest) MarshalJSON() ([]byte, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(manifest.manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal effective capability manifest: %w", err)
	}
	return payload, nil
}

func validateEffectiveManifest(
	manifest capabilitymanifest.AgentCapabilityManifest,
	mode ActivationMode,
) error {
	canonical, err := capabilitymanifest.ForAgent(manifest.AgentID)
	if err != nil {
		return err
	}
	expectedExposure := capabilitymanifest.ContractExposureDormant
	if mode == ActivationEnabled {
		expectedExposure = capabilitymanifest.ContractExposureAdvertised
	}
	manifestExposure := manifest.Contracts.WorkRoutingV1.Exposure
	manifest.Contracts.WorkRoutingV1.Exposure =
		capabilitymanifest.ContractExposureDormant
	if manifest != canonical || manifestExposure != expectedExposure {
		return fmt.Errorf(
			"effective capability manifest for %q does not match activation %q",
			manifest.AgentID,
			mode,
		)
	}
	return nil
}
