package workprovider

import (
	"context"
	"testing"

	"github.com/gentleman-programming/gentle-ai/internal/agents/capabilitymanifest"
	"github.com/gentleman-programming/gentle-ai/internal/model"
)

func TestEffectiveACIOverlayCanOnlyNarrowCanonicalAdvertisedManifest(t *testing.T) {
	t.Parallel()

	canonical, err := capabilitymanifest.ForAgent(model.AgentCodex)
	if err != nil {
		t.Fatalf("ForAgent() error = %v", err)
	}
	canonicalDigest, err := canonical.Digest()
	if err != nil {
		t.Fatalf("canonical Digest() error = %v", err)
	}
	if !canonical.Advertises(capabilitymanifest.ContractWorkRoutingV1) {
		t.Fatal("final canonical manifest does not advertise work routing")
	}

	for _, mode := range []ActivationMode{
		ActivationRecoveryOnly,
		ActivationReadOnly,
		ActivationDisabled,
	} {
		controller := NewController(nil, StaticActivationResolver{Mode: mode})
		effective, err := controller.EffectiveCapabilityManifest(
			context.Background(),
			"/repo",
			model.AgentCodex,
		)
		if err != nil {
			t.Fatalf("EffectiveCapabilityManifest(%q) error = %v", mode, err)
		}
		if effective.Advertises(capabilitymanifest.ContractWorkRoutingV1) {
			t.Fatalf("mode %q advertised dormant capability", mode)
		}
	}

	enabledController := NewController(
		nil,
		StaticActivationResolver{Mode: ActivationEnabled},
	)
	enabled, err := enabledController.EffectiveCapabilityManifest(
		context.Background(),
		"/repo",
		model.AgentCodex,
	)
	if err != nil {
		t.Fatalf("enabled EffectiveCapabilityManifest() error = %v", err)
	}
	if !enabled.Advertises(capabilitymanifest.ContractWorkRoutingV1) {
		t.Fatal("enabled effective manifest did not advertise work routing")
	}

	again, err := capabilitymanifest.ForAgent(model.AgentCodex)
	if err != nil {
		t.Fatalf("second ForAgent() error = %v", err)
	}
	againDigest, err := again.Digest()
	if err != nil {
		t.Fatalf("second Digest() error = %v", err)
	}
	if againDigest != canonicalDigest ||
		again.Contracts.WorkRoutingV1.Exposure != capabilitymanifest.ContractExposureAdvertised {
		t.Fatalf("canonical manifest changed after overlay: %#v", again)
	}
}

func TestDefaultControllerAdvertisesEnabledCapabilityWhenUnset(t *testing.T) {
	restoreDefaultActivationEnvironment(t)

	effective, err := NewDefaultController().EffectiveCapabilityManifest(
		context.Background(),
		"/repository-is-not-opened-for-manifest-resolution",
		model.AgentCodex,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !effective.Advertises(capabilitymanifest.ContractWorkRoutingV1) {
		t.Fatal("default controller did not advertise shipped work routing")
	}
}
